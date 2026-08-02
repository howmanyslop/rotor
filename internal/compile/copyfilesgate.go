package compile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"rotor/internal/rojo"
	"rotor/tsgo/ast"
	"rotor/tsgo/compiler"
	"rotor/tsgo/core"
)

const copyFilesManifestVersion = 2

const copyFilesCacheFilename = "rbxts.copyfiles.json"

const copyFilesResolverCacheFilename = "rbxts.rojocache.json"

const copyFilesTypeRootsCacheFilename = "rbxts.typeroots.rojocache.json"

var protectedOutputFilenames = map[string]bool{
	copyFilesCacheFilename:          true,
	copyFilesResolverCacheFilename:  true,
	copyFilesTypeRootsCacheFilename: true,
}

type CopyFilesFileEntry struct {
	Src         string
	Dest        string
	SrcMtimeMs  int64
	DestMtimeMs int64
}

func (entry CopyFilesFileEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal([4]any{entry.Src, entry.Dest, entry.SrcMtimeMs, entry.DestMtimeMs})
}

func (entry *CopyFilesFileEntry) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || len(values) != 4 {
		return os.ErrInvalid
	}
	if err := json.Unmarshal(values[0], &entry.Src); err != nil {
		return err
	}
	if err := json.Unmarshal(values[1], &entry.Dest); err != nil {
		return err
	}
	if err := json.Unmarshal(values[2], &entry.SrcMtimeMs); err != nil {
		return err
	}
	return json.Unmarshal(values[3], &entry.DestMtimeMs)
}

type CopyFilesDirEntry struct {
	Path    string
	MtimeMs int64
}

func (entry CopyFilesDirEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]any{entry.Path, entry.MtimeMs})
}

func (entry *CopyFilesDirEntry) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || len(values) != 2 {
		return os.ErrInvalid
	}
	if err := json.Unmarshal(values[0], &entry.Path); err != nil {
		return err
	}
	return json.Unmarshal(values[1], &entry.MtimeMs)
}

type CopyFilesManifest struct {
	Version         int                  `json:"version"`
	CompilerVersion string               `json:"compilerVersion"`
	Key             string               `json:"key"`
	Declaration     bool                 `json:"declaration"`
	Files           []CopyFilesFileEntry `json:"files"`
	Dirs            []CopyFilesDirEntry  `json:"dirs"`
}

type BuildStateSnapshot struct {
	ChangedFilePaths map[string]struct{}
}

type CopyFilesManifestInputs struct {
	RootDirs       []string
	OutDir         string
	Declaration    bool
	Key            string
	PathTranslator *rojo.PathTranslator
}

func buildCopyFilesManifest(inputs CopyFilesManifestInputs) CopyFilesManifest {
	manifest := CopyFilesManifest{
		Version:         copyFilesManifestVersion,
		CompilerVersion: core.Version(),
		Key:             inputs.Key,
		Declaration:     inputs.Declaration,
		Files:           make([]CopyFilesFileEntry, 0),
		Dirs:            make([]CopyFilesDirEntry, 0),
	}
	normalizedOutDir := filepath.Clean(inputs.OutDir)
	var walk func(string)
	walk = func(dir string) {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return
		}
		manifest.Dirs = append(manifest.Dirs, CopyFilesDirEntry{Path: dir, MtimeMs: info.ModTime().UnixMilli()})
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.Name() == "node_modules" {
				continue
			}
			itemPath := filepath.Join(dir, entry.Name())
			if filepath.Clean(itemPath) == normalizedOutDir {
				continue
			}
			info, err := os.Stat(itemPath)
			if err != nil {
				continue
			}
			if info.IsDir() {
				walk(itemPath)
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if strings.HasSuffix(itemPath, ".d.ts") {
				if !inputs.Declaration {
					continue
				}
			} else if isCompilableFile(itemPath) {
				continue
			}
			dest := inputs.PathTranslator.GetOutputPath(itemPath)
			destMtime := int64(0)
			if destInfo, err := os.Stat(dest); err == nil {
				destMtime = destInfo.ModTime().UnixMilli()
			}
			manifest.Files = append(manifest.Files, CopyFilesFileEntry{
				Src:         itemPath,
				Dest:        dest,
				SrcMtimeMs:  info.ModTime().UnixMilli(),
				DestMtimeMs: destMtime,
			})
		}
	}
	for _, rootDir := range inputs.RootDirs {
		walk(filepath.Clean(rootDir))
	}
	return manifest
}

func writeCopyFilesManifest(path string, manifest CopyFilesManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal copy-files manifest: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create copy-files manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".copyfiles-*.tmp")
	if err != nil {
		return fmt.Errorf("create copy-files manifest temporary file: %w", err)
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	chmodErr := tmp.Chmod(0o644)
	closeErr := tmp.Close()
	if writeErr != nil || chmodErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return fmt.Errorf("write copy-files manifest: %w", writeErr)
		}
		if chmodErr != nil {
			return fmt.Errorf("set copy-files manifest permissions: %w", chmodErr)
		}
		return fmt.Errorf("close copy-files manifest: %w", closeErr)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace copy-files manifest: %w", err)
	}
	return nil
}

func tryLoadCopyFilesManifest(path, key string, declaration bool) *CopyFilesManifest {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var manifest CopyFilesManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	if manifest.Version != copyFilesManifestVersion || manifest.CompilerVersion != core.Version() ||
		manifest.Key != key || manifest.Declaration != declaration || manifest.Files == nil || manifest.Dirs == nil {
		return nil
	}
	return &manifest
}

type copyFilesGateInputs struct {
	RootDirs       []string
	OutDir         string
	Declaration    bool
	PathTranslator *rojo.PathTranslator
	Snapshot       BuildStateSnapshot
}

type copyFilesGateOutputs struct {
	SkipCleanup   bool
	SkipCopyFiles bool
	Persist       func() error
}

func buildCopyFilesManifestKey(outDir string, rootDirs []string) string {
	normalized := make([]string, len(rootDirs))
	for i, rootDir := range rootDirs {
		normalized[i] = filepath.Clean(rootDir)
	}
	sort.Strings(normalized)
	return filepath.Clean(outDir) + "|" + strings.Join(normalized, ",")
}

func copyFilesDirsEqual(a, b []CopyFilesDirEntry) bool {
	if len(a) != len(b) {
		return false
	}
	lookup := make(map[string]int64, len(a))
	for _, entry := range a {
		lookup[entry.Path] = entry.MtimeMs
	}
	for _, entry := range b {
		if lookup[entry.Path] != entry.MtimeMs {
			return false
		}
	}
	return true
}

func copyFilesFilesEqual(a, b []CopyFilesFileEntry) bool {
	if len(a) != len(b) {
		return false
	}
	lookup := make(map[string]int64, len(a))
	for _, entry := range a {
		lookup[entry.Src] = entry.SrcMtimeMs
	}
	for _, entry := range b {
		if lookup[entry.Src] != entry.SrcMtimeMs {
			return false
		}
	}
	return true
}

func copyFilesDestsValid(files []CopyFilesFileEntry) bool {
	for _, entry := range files {
		info, err := os.Stat(entry.Dest)
		if err != nil || info.ModTime().UnixMilli() != entry.DestMtimeMs {
			return false
		}
	}
	return true
}

func copyFilesFastValidate(manifest CopyFilesManifest, snapshot BuildStateSnapshot) bool {
	if len(snapshot.ChangedFilePaths) != 0 {
		return false
	}
	for _, entry := range manifest.Dirs {
		info, err := os.Stat(entry.Path)
		if err != nil || info.ModTime().UnixMilli() != entry.MtimeMs {
			return false
		}
	}
	for _, entry := range manifest.Files {
		srcInfo, err := os.Stat(entry.Src)
		if err != nil || srcInfo.ModTime().UnixMilli() != entry.SrcMtimeMs {
			return false
		}
		destInfo, err := os.Stat(entry.Dest)
		if err != nil || destInfo.ModTime().UnixMilli() != entry.DestMtimeMs {
			return false
		}
	}
	return true
}

func loadCopyFilesGatePreBuild(inputs copyFilesGateInputs) copyFilesGateOutputs {
	cachePath := filepath.Join(inputs.OutDir, copyFilesCacheFilename)
	key := buildCopyFilesManifestKey(inputs.OutDir, inputs.RootDirs)
	cached := tryLoadCopyFilesManifest(cachePath, key, inputs.Declaration)
	if cached != nil && copyFilesFastValidate(*cached, inputs.Snapshot) {
		return copyFilesGateOutputs{
			SkipCleanup:   true,
			SkipCopyFiles: true,
		}
	}
	current := buildCopyFilesManifest(CopyFilesManifestInputs{
		RootDirs:       inputs.RootDirs,
		OutDir:         inputs.OutDir,
		Declaration:    inputs.Declaration,
		Key:            key,
		PathTranslator: inputs.PathTranslator,
	})
	persist := func() error {
		refreshed := make([]CopyFilesFileEntry, len(current.Files))
		for i, entry := range current.Files {
			entry.DestMtimeMs = 0
			if info, err := os.Stat(entry.Dest); err == nil {
				entry.DestMtimeMs = info.ModTime().UnixMilli()
			}
			refreshed[i] = entry
		}
		current.Files = refreshed
		return writeCopyFilesManifest(cachePath, current)
	}
	if cached == nil {
		return copyFilesGateOutputs{Persist: persist}
	}
	dirsMatch := copyFilesDirsEqual(current.Dirs, cached.Dirs)
	filesMatch := copyFilesFilesEqual(current.Files, cached.Files)
	destsValid := copyFilesDestsValid(cached.Files)
	return copyFilesGateOutputs{
		SkipCleanup:   dirsMatch && filesMatch && destsValid && len(inputs.Snapshot.ChangedFilePaths) == 0,
		SkipCopyFiles: filesMatch && destsValid,
		Persist:       persist,
	}
}

// copyFilesChangedSnapshot mirrors the fork's snapshotBuildState: it reports
// what the build considers changed, which is what gates cleanup.
//
// A non-incremental build reports the whole program, never an empty set. The
// fork always compiles through a BuilderProgram, and one built without
// rehydrated incremental state seeds changedFilesSet with every file, so
// cleanup only ever gets skipped when incremental state proves nothing changed.
// Reporting an empty set here instead left cleanup gated purely on directory
// mtimes, which cannot see a source deletion that lands within the same
// millisecond as the previous build's pre-build walk (routine on fast
// filesystems), leaving that source's outputs orphaned in outDir forever.
func copyFilesChangedSnapshot(program *compiler.Program, selectedFiles []*ast.SourceFile) BuildStateSnapshot {
	changedFiles := selectedFiles
	if !program.Options().Incremental.IsTrue() {
		changedFiles = program.SourceFiles()
	}
	changed := make(map[string]struct{}, len(changedFiles))
	for _, file := range changedFiles {
		changed[normalizeSourceFilePath(file.FileName())] = struct{}{}
	}
	return BuildStateSnapshot{ChangedFilePaths: changed}
}
