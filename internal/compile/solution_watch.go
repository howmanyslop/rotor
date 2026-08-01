package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"rotor/internal/rojo"
)

type SolutionWatchSet struct {
	ProjectPath     string
	TsConfigPaths   []string
	SourceFiles     []string
	RootDirs        []string
	RojoConfigs     []string
	RojoDirectories []string
}

type WatchAssetEvent struct {
	Path    string
	Deleted bool
}

func (c *SolutionCoordinator) WatchSets() []SolutionWatchSet {
	sets := make([]SolutionWatchSet, 0, len(c.graph.Projects))
	for _, project := range c.graph.Projects {
		set := SolutionWatchSet{ProjectPath: project.ConfigPath}
		_, set.TsConfigPaths, _ = ReadRbxtsOptionsWithChain(project.ConfigPath)
		if len(set.TsConfigPaths) == 0 {
			set.TsConfigPaths = []string{project.ConfigPath}
		}
		dir, program, _, err := newProjectProgram(filepath.Dir(project.ConfigPath), project.ConfigPath)
		if err == nil {
			set.RootDirs = getRootDirs(program)
			for _, file := range program.SourceFiles() {
				if pathWithinAnyRoot(file.FileName(), set.RootDirs) {
					set.SourceFiles = append(set.SourceFiles, filepath.FromSlash(file.FileName()))
				}
			}
			rojoPath, _, rojoErr := resolveRojoConfigPath(dir, project.Options.RojoConfigPath)
			if rojoErr == nil && rojoPath != "" {
				state := rojo.FromPath(rojoPath).GetState()
				set.RojoConfigs, set.RojoDirectories = state.WalkedConfigs, state.WalkedDirectories
			}
		}
		sets = append(sets, set)
	}
	return sets
}

func (c *SolutionCoordinator) DrainAssets(tsConfigPath string, events []WatchAssetEvent) error {
	state, ok := c.ProjectState(tsConfigPath)
	if !ok {
		return nil
	}
	_, program, _, err := newProjectProgram(filepath.Dir(state.Project.ConfigPath), state.Project.ConfigPath)
	if err != nil {
		return err
	}
	translator := createPathTranslator(program, !state.Project.Options.LuaExtension)
	assetDirectories := append([]string{}, getRootDirs(program)...)
	dir := filepath.Dir(state.Project.ConfigPath)
	if rojoPath, _, rojoErr := resolveRojoConfigPath(dir, state.Project.Options.RojoConfigPath); rojoErr == nil && rojoPath != "" {
		assetDirectories = append(assetDirectories, rojo.FromPath(rojoPath).GetState().WalkedDirectories...)
	}
	changes := map[string]bool{}
	for _, event := range events {
		changes[filepath.Clean(event.Path)] = event.Deleted
	}
	paths := make([]string, 0, len(changes))
	for path := range changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if !pathWithinAnyRoot(path, assetDirectories) || watchCompilablePath(path) {
			continue
		}
		deleted := changes[path]
		if !deleted {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				deleted = true
			}
		}
		if deleted {
			tryRemoveOutput(translator, translator.GetOutputPath(path), program.Options().SourceMap.IsTrue())
			continue
		}
		if err := copyItem(translator, path, state.Project.Options.WriteOnlyChanged); err != nil {
			return err
		}
	}
	return nil
}

func (c *SolutionCoordinator) ReloadForWatch(tsConfigPath string, entry ProjectOptions) (func() error, error) {
	previous := make(map[string]solutionWatchProjectOutputs, len(c.graph.Projects))
	for _, project := range c.graph.Projects {
		outputs, err := solutionWatchOutputPaths(project)
		if err != nil {
			return nil, err
		}
		previous[project.ConfigPath] = solutionWatchProjectOutputs{
			luaExtension: project.Options.LuaExtension,
			paths:        outputs,
		}
	}
	if err := c.Reload(tsConfigPath, entry); err != nil {
		return nil, err
	}

	staleOutputs := []string{}
	for _, project := range c.graph.Projects {
		old, ok := previous[project.ConfigPath]
		if !ok || old.luaExtension == project.Options.LuaExtension {
			continue
		}
		staleOutputs = append(staleOutputs, old.paths...)
	}
	sort.Strings(staleOutputs)
	return func() error {
		for _, path := range staleOutputs {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale solution watch output %q: %w", path, err)
			}
			if err := os.Remove(path + ".map"); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale solution watch source map %q: %w", path+".map", err)
			}
		}
		return nil
	}, nil
}

type solutionWatchProjectOutputs struct {
	luaExtension bool
	paths        []string
}

func solutionWatchOutputPaths(project SolutionProject) ([]string, error) {
	_, program, _, err := newProjectProgram(filepath.Dir(project.ConfigPath), project.ConfigPath)
	if err != nil {
		return nil, err
	}
	translator := createPathTranslator(program, !project.Options.LuaExtension)
	paths := make([]string, 0, len(projectSourceFiles(program)))
	for _, sourceFile := range projectSourceFiles(program) {
		paths = append(paths, translator.GetOutputPath(sourceFile.FileName()))
	}
	return paths, nil
}

func watchCompilablePath(path string) bool {
	return strings.HasSuffix(path, ".d.ts") || strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")
}
