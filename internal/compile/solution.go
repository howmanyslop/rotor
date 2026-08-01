package compile

import (
	"errors"
	"fmt"
	"path/filepath"
)

type SolutionProject struct {
	ConfigPath  string
	References  []string
	Options     ProjectOptions
	Coordinator bool
}

type SolutionGraph struct {
	Projects []SolutionProject
}

type SolutionProjectDrainer interface {
	Drain(project SolutionProject) (*BuildResult, []string, error)
}

type SolutionProjectState struct {
	Project   SolutionProject
	Result    *BuildResult
	UpToDate  bool
	BlockedBy string
	Err       error
}

type SolutionCoordinator struct {
	graph   *SolutionGraph
	drainer SolutionProjectDrainer
	states  map[string]SolutionProjectState
}

type solutionProjectVisit uint8

const (
	solutionProjectUnvisited solutionProjectVisit = iota
	solutionProjectVisiting
	solutionProjectVisited
)

func BuildSolutionGraph(tsConfigPath string, entry ProjectOptions) (*SolutionGraph, error) {
	projects := map[string]SolutionProject{}
	visits := map[string]solutionProjectVisit{}
	stack := []string{}

	var visit func(string, ProjectOptions, bool) error
	visit = func(configPath string, options ProjectOptions, inheritEntryTypeAndRojo bool) error {
		configPath, err := filepath.Abs(configPath)
		if err != nil {
			return fmt.Errorf("compile: resolve project reference %q: %w", configPath, err)
		}
		configPath = filepath.Clean(configPath)
		switch visits[configPath] {
		case solutionProjectVisiting:
			return solutionCycleError(stack, configPath)
		case solutionProjectVisited:
			return nil
		}

		visits[configPath] = solutionProjectVisiting
		stack = append(stack, configPath)
		defer func() {
			stack = stack[:len(stack)-1]
		}()

		references, coordinator, err := readProjectReferencePaths(configPath)
		if err != nil {
			return fmt.Errorf("compile: read project reference %q: %w", configPath, err)
		}
		project := SolutionProject{
			ConfigPath:  configPath,
			References:  references,
			Options:     options,
			Coordinator: coordinator,
		}
		projects[configPath] = project
		if len(stack) == 1 {
			inheritEntryTypeAndRojo = coordinator
		}
		for _, reference := range references {
			referenceOptions, err := ProjectOptionsForReferencedConfig(options, reference, inheritEntryTypeAndRojo)
			if err != nil {
				return fmt.Errorf("compile: read referenced project options %q: %w", reference, err)
			}
			if err := visit(reference, referenceOptions, inheritEntryTypeAndRojo); err != nil {
				return err
			}
		}
		visits[configPath] = solutionProjectVisited
		if !coordinator {
			projects[configPath] = project
		}
		return nil
	}

	rootPath, err := filepath.Abs(tsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("compile: resolve solution config %q: %w", tsConfigPath, err)
	}
	if err := visit(rootPath, entry, false); err != nil {
		return nil, err
	}

	graph := &SolutionGraph{}
	for _, configPath := range postOrderProjectPaths(rootPath, projects) {
		project := projects[configPath]
		if !project.Coordinator {
			graph.Projects = append(graph.Projects, project)
		}
	}
	return graph, nil
}

func NewSolutionCoordinator(tsConfigPath string, entry ProjectOptions) (*SolutionCoordinator, error) {
	return NewSolutionCoordinatorWithDrainer(tsConfigPath, entry, solutionBuildDrainer{})
}

func NewSolutionCoordinatorWithDrainer(tsConfigPath string, entry ProjectOptions, drainer SolutionProjectDrainer) (*SolutionCoordinator, error) {
	if drainer == nil {
		return nil, errors.New("compile: solution project drainer is nil")
	}
	graph, err := BuildSolutionGraph(tsConfigPath, entry)
	if err != nil {
		return nil, err
	}
	states := make(map[string]SolutionProjectState, len(graph.Projects))
	for _, project := range graph.Projects {
		states[project.ConfigPath] = SolutionProjectState{Project: project}
	}
	return &SolutionCoordinator{graph: graph, drainer: drainer, states: states}, nil
}

func (c *SolutionCoordinator) Drain() (*BuildResult, []string, error) {
	result := &BuildResult{Outputs: map[string]string{}}
	var firstErr error
	for _, project := range c.graph.Projects {
		state := c.states[project.ConfigPath]
		if state.UpToDate {
			continue
		}
		if blockedBy := c.failedDependency(project); blockedBy != "" {
			state.BlockedBy = blockedBy
			state.Err = fmt.Errorf("compile: project %s blocked by failed dependency %s", project.ConfigPath, blockedBy)
			c.states[project.ConfigPath] = state
			result.Diagnostics = append(result.Diagnostics, DiagnosticInfo{Message: state.Err.Error()})
			if firstErr == nil {
				firstErr = state.Err
			}
			continue
		}

		built, messages, err := c.drainer.Drain(project)
		state.Result = built
		if built != nil {
			mergeSolutionBuildResult(result, project, built)
		}
		if err != nil {
			state.Err = err
			if built == nil || len(built.Diagnostics) == 0 {
				for _, message := range messages {
					result.Diagnostics = append(result.Diagnostics, DiagnosticInfo{Message: message})
				}
			}
			if firstErr == nil {
				firstErr = err
			}
		} else {
			state.BlockedBy = ""
			state.Err = nil
			state.UpToDate = true
		}
		c.states[project.ConfigPath] = state
	}
	if firstErr != nil {
		return result, diagnosticInfoMessages(result.Diagnostics), firstErr
	}
	return result, nil, nil
}

func (c *SolutionCoordinator) ProjectState(tsConfigPath string) (SolutionProjectState, bool) {
	configPath, err := filepath.Abs(tsConfigPath)
	if err != nil {
		return SolutionProjectState{}, false
	}
	state, ok := c.states[filepath.Clean(configPath)]
	return state, ok
}

func (c *SolutionCoordinator) failedDependency(project SolutionProject) string {
	for _, reference := range project.References {
		if state, ok := c.states[reference]; ok && state.Err != nil {
			return reference
		}
	}
	return ""
}

type solutionBuildDrainer struct{}

func (solutionBuildDrainer) Drain(project SolutionProject) (*BuildResult, []string, error) {
	options := project.Options
	options.TsConfigPath = project.ConfigPath
	return BuildProjectWithOptions(filepath.Dir(project.ConfigPath), options)
}

func BuildSolutionWithOptions(tsConfigPath string, entry ProjectOptions) (*BuildResult, []string, error) {
	coordinator, err := NewSolutionCoordinator(tsConfigPath, entry)
	if err != nil {
		return nil, nil, err
	}
	return coordinator.Drain()
}

func postOrderProjectPaths(rootPath string, projects map[string]SolutionProject) []string {
	paths := []string{}
	visited := map[string]struct{}{}
	var visit func(string)
	visit = func(configPath string) {
		if _, ok := visited[configPath]; ok {
			return
		}
		visited[configPath] = struct{}{}
		project := projects[configPath]
		for _, reference := range project.References {
			visit(reference)
		}
		paths = append(paths, configPath)
	}
	visit(rootPath)
	return paths
}

func solutionCycleError(stack []string, repeated string) error {
	start := 0
	for index, configPath := range stack {
		if configPath == repeated {
			start = index
			break
		}
	}
	cycle := append([]string{}, stack[start:]...)
	cycle = append(cycle, repeated)
	return fmt.Errorf("compile: circular project reference %s", cycle)
}

func mergeSolutionBuildResult(result *BuildResult, project SolutionProject, built *BuildResult) {
	for path, text := range built.Outputs {
		result.Outputs[filepath.Join(filepath.Dir(project.ConfigPath), path)] = text
	}
	result.EmittedFiles = append(result.EmittedFiles, built.EmittedFiles...)
	result.Diagnostics = append(result.Diagnostics, built.Diagnostics...)
	result.WroteRotorTypes = result.WroteRotorTypes || built.WroteRotorTypes
	result.WroteLockfile = result.WroteLockfile || built.WroteLockfile
}
