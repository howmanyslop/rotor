package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"rotor/internal/rojo"
)

type SolutionWatchSet struct {
	ProjectPath         string
	TsConfigPaths       []string
	SourceFiles         []string
	RootDirs            []string
	RojoConfigs         []string
	RojoDirectories     []string
	ArtifactDirectories []string
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
		set.TsConfigPaths = canonicalWatchPaths(set.TsConfigPaths)
		dir, program, _, err := newProjectProgram(filepath.Dir(project.ConfigPath), project.ConfigPath)
		if err == nil {
			set.RootDirs = getRootDirs(program)
			pathTranslator := createPathTranslator(program, !project.Options.LuaExtension)
			set.ArtifactDirectories = solutionWatchArtifactDirectories(
				dir,
				pathTranslator.OutDir,
				project.Options.IncludePath,
			)
			for _, file := range program.SourceFiles() {
				if pathWithinAnyRoot(file.FileName(), set.RootDirs) {
					set.SourceFiles = append(set.SourceFiles, filepath.FromSlash(file.FileName()))
				}
			}
			rojoPath, _, rojoErr := resolveRojoConfigPath(dir, project.Options.RojoConfigPath)
			if rojoErr == nil && rojoPath != "" {
				state := rojo.FromPath(rojoPath).GetState()
				set.RojoConfigs = canonicalWatchPaths(state.WalkedConfigs)
				set.RojoDirectories = filterRojoWatchDirectories(dir, state.WalkedDirectories, set.ArtifactDirectories)
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
		artifactDirectories := solutionWatchArtifactDirectories(
			dir,
			translator.OutDir,
			state.Project.Options.IncludePath,
		)
		rojoDirectories := rojo.FromPath(rojoPath).GetState().WalkedDirectories
		assetDirectories = append(assetDirectories, filterRojoWatchDirectories(dir, rojoDirectories, artifactDirectories)...)
	}
	changes := map[string]bool{}
	for _, event := range events {
		changes[rebaseWatchAssetPath(event.Path, assetDirectories)] = event.Deleted
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

func solutionWatchArtifactDirectories(projectDir, outputDir, includePath string) []string {
	directories := []string{filepath.Clean(filepath.FromSlash(outputDir))}
	if includeDir, err := resolveIncludePath(projectDir, includePath); err == nil {
		directories = appendDistinctWatchDirectory(directories, includeDir)
	}
	return appendDistinctWatchDirectory(directories, filepath.Join(filepath.FromSlash(projectDir), ".rotor"))
}

func appendDistinctWatchDirectory(directories []string, directory string) []string {
	directory = filepath.Clean(filepath.FromSlash(directory))
	if slices.Contains(directories, directory) {
		return directories
	}
	return append(directories, directory)
}

func canonicalWatchDirectory(directory string) string {
	directory = filepath.Clean(filepath.FromSlash(directory))
	if resolved, err := filepath.EvalSymlinks(directory); err == nil {
		return resolved
	}
	return filepath.Join(canonicalWatchDirectoryParent(directory), filepath.Base(directory))
}

func canonicalWatchPaths(paths []string) []string {
	canonicalPaths := make([]string, len(paths))
	for index, path := range paths {
		canonicalPaths[index] = canonicalWatchDirectory(path)
	}
	return canonicalPaths
}

func canonicalWatchDirectoryParent(directory string) string {
	parent := filepath.Dir(directory)
	if parent == directory {
		return directory
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return resolved
	}
	return canonicalWatchDirectoryParent(parent)
}

func rebaseWatchAssetPath(path string, assetDirectories []string) string {
	path = filepath.Clean(filepath.FromSlash(path))
	canonicalPath := canonicalWatchDirectory(path)
	for _, directory := range assetDirectories {
		relative, err := filepath.Rel(canonicalWatchDirectory(directory), canonicalPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.Join(filepath.Clean(filepath.FromSlash(directory)), relative)
	}
	return path
}

func filterRojoWatchDirectories(projectDir string, directories, artifactDirectories []string) []string {
	filtered := make([]string, 0, len(directories))
	for _, directory := range directories {
		directory = rebaseRojoWatchDirectory(directory, projectDir)
		if pathWithinAnyRoot(directory, artifactDirectories) {
			continue
		}
		filtered = append(filtered, directory)
	}
	return filtered
}

func rebaseRojoWatchDirectory(directory, projectDir string) string {
	canonicalProjectDir := canonicalWatchDirectory(projectDir)
	relative, err := filepath.Rel(canonicalProjectDir, canonicalWatchDirectory(directory))
	if err != nil {
		return filepath.Clean(filepath.FromSlash(directory))
	}
	return filepath.Join(filepath.Clean(filepath.FromSlash(projectDir)), relative)
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
