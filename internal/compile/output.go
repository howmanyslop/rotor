package compile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"rotor/internal/assets"
	"rotor/internal/includefiles"
	"rotor/internal/logservice"
	"rotor/internal/luau/cst"
	"rotor/internal/rojo"
	"rotor/tsgo/compiler"
)

// assetsLockfileName is the lockfile the $asset pipeline persists (aliased so
// error text stays in sync with internal/assets).
const assetsLockfileName = assets.LockfileName

// BuildResult is the disk-writing sibling of CompileProject's pure text map.
// Outputs contains the compiled Luau sources keyed by project-relative output
// path; EmittedFiles contains the compiled output paths actually written to
// disk this pass (mirroring compileFiles.ts' emittedFiles and excluding copied
// passthrough files).
type BuildResult struct {
	Outputs      map[string]string
	EmittedFiles []string
	OutputDir    string

	// UsesEnvMacro reports whether any project source file references the
	// rotor $env macro (cheap text scan over the already-loaded sources).
	UsesEnvMacro bool
	// UsesAssetMacro reports whether any project source file references the
	// rotor $asset macro (cheap text scan).
	UsesAssetMacro bool
	// UsesMacros reports whether any project source file references one of the
	// rotor $nameof / $keys / $file / $git / $buildTime macros (cheap text scan).
	UsesMacros bool

	// WroteRotorTypes reports whether the consolidated rotor.d.ts editor
	// companion was (re)written this pass (true when the project references any
	// macro and the on-disk file was missing or stale; see rotortypes.go).
	WroteRotorTypes bool
	// WroteLockfile reports whether rotor-lock.json was persisted this pass
	// (true only when $asset uploaded a new asset on a cache miss).
	WroteLockfile bool

	// Diagnostics holds the structured diagnostics from this build (populated
	// even when the build fails on diagnostics). Empty on success.
	Diagnostics []DiagnosticInfo
}

// BuildProjectWithOptions runs the Phase 4 output pipeline for `rotor build`:
// cleanup -> copyInclude -> copy non-compiled files -> compile -> write
// compiled outputs. CompileProject remains the pure library API; this is the
// writing entry point for the CLI and future watch/incremental layers.
func BuildProjectWithOptions(projectDir string, opts ProjectOptions) (*BuildResult, []string, error) {
	dir, program, diags, err := newProjectProgram(projectDir, opts.TsConfigPath)
	if err != nil {
		return nil, diags, err
	}

	pathTranslator := createPathTranslator(program, !opts.LuaExtension)
	cleanupOutputs(pathTranslator)

	if err := maybeCopyInclude(dir, opts); err != nil {
		return nil, nil, err
	}
	if err := copyNonCompiledFiles(pathTranslator, getRootDirs(program), opts.WriteOnlyChanged); err != nil {
		return nil, nil, err
	}

	sourceFiles := projectSourceFiles(program)

	// rotor extension: detect $env usage on the already-loaded source text
	// (no extra IO; a substring scan per file). Drives the rotor-env.d.ts
	// editor-companion refresh after a successful compile. Files that import
	// the rbxts-transform-env plugin are excluded: their `$env` is the
	// plugin's MODULE export (which shadows the global per-file), so rotor's
	// macro never fires there and no editor companion is needed.
	usesEnvMacro := false
	usesAssetMacro := false
	usesMacros := false
	for _, sourceFile := range sourceFiles {
		text := sourceFile.Text()
		if strings.Contains(text, "$env") && !strings.Contains(text, "rbxts-transform-env") {
			usesEnvMacro = true
		}
		if strings.Contains(text, "$asset") {
			usesAssetMacro = true
		}
		if SourceUsesMacros(text) {
			usesMacros = true
		}
	}

	selectedFiles := sourceFiles
	var previousManifest *incrementalManifest
	var currentManifest *incrementalManifest
	if program.Options().Incremental.IsTrue() && pathTranslator.BuildInfoOutputPath != "" {
		previousManifest, err = readIncrementalManifest(pathTranslator.BuildInfoOutputPath)
		if err != nil {
			return nil, nil, err
		}
		currentManifest, err = buildIncrementalManifest(program, sourceFiles, incrementalSalt(program, opts, pathTranslator.BuildInfoOutputPath))
		if err != nil {
			return nil, nil, err
		}
		selectedFiles = selectIncrementalSourceFiles(sourceFiles, currentManifest, previousManifest)
	}
	program, selectedFiles, diags, err = prepareProjectProgramForCompile(dir, program, selectedFiles)
	if err != nil {
		return nil, diags, err
	}

	pctx, diags, err := newProjectContext(dir, program, opts)
	if err != nil {
		return nil, diags, err
	}
	outputs, infos, err := compileProjectSourceFiles(dir, program, pctx, selectedFiles, opts)
	if err != nil {
		return &BuildResult{Diagnostics: infos}, diagnosticInfoMessages(infos), err
	}

	// rotor extension: --minify post-processes each compiled Luau source through
	// the minifier before write + manifest. The incremental manifest hashes
	// SOURCE files (not output content), so this never desyncs incremental
	// builds; semantics are preserved (see ProjectOptions.MinifyOutput).
	if opts.MinifyOutput {
		if err := minifyOutputs(outputs); err != nil {
			return nil, nil, err
		}
	}

	emittedFiles := make([]string, 0, len(outputs))
	relOuts := make([]string, 0, len(outputs))
	for relOut := range outputs {
		relOuts = append(relOuts, relOut)
	}
	sort.Strings(relOuts)

	// The write loop is pure independent file I/O; fan it out across workers
	// (Windows file writes are ~30x slower per syscall than macOS/APFS, so
	// sequential writes dominate full-build wall time there). ROTOR_WRITE_WORKERS=1
	// reproduces the pre-parallel behavior for A/B timing.
	wrote := make([]bool, len(relOuts))
	jobs := make([]func() error, len(relOuts))
	for i, relOut := range relOuts {
		i, relOut := i, relOut
		jobs[i] = func() error {
			// Defense-in-depth: output paths are derived from source/Rojo path
			// mappings; refuse any that would escape the project directory.
			if err := assertLocalOutputPath(relOut); err != nil {
				return err
			}
			absOut := filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(relOut))
			w, err := writeOutputFile(absOut, outputs[relOut], opts.WriteOnlyChanged)
			wrote[i] = w
			return err
		}
	}
	if err := parallelize(writeWorkers(), jobs); err != nil {
		return nil, nil, err
	}
	for i, w := range wrote {
		if w {
			emittedFiles = append(emittedFiles, filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(relOuts[i])))
		}
	}

	selectedPaths := make(map[string]struct{}, len(selectedFiles))
	for _, sourceFile := range selectedFiles {
		selectedPaths[normalizeSourceFilePath(sourceFile.FileName())] = struct{}{}
	}

	declFiles, err := emitDeclarations(program, selectedPaths, opts.WriteOnlyChanged)
	if err != nil {
		return nil, nil, err
	}
	emittedFiles = append(emittedFiles, declFiles...)

	if currentManifest != nil && !sameIncrementalManifest(previousManifest, currentManifest) {
		if err := writeIncrementalManifest(pathTranslator.BuildInfoOutputPath, currentManifest); err != nil {
			return nil, nil, err
		}
	}

	// rotor extension: keep the consolidated on-disk rotor.d.ts editor companion
	// fresh for projects that reference any macro ($env / $asset / $nameof /
	// $keys / $file / $git / $buildTime). Editors never see the synthetic
	// in-memory declarations, so this single file is what stops the macros from
	// red-squiggling in VS Code. Written only when missing or stale (rotortypes.go).
	wroteRotorTypes := false
	if usesEnvMacro || usesAssetMacro || usesMacros {
		wroteRotorTypes, err = WriteRotorTypes(filepath.FromSlash(dir))
		if err != nil {
			return nil, nil, fmt.Errorf("compile: writing %s: %w", RotorTypesFileName, err)
		}
	}

	// rotor extension: the $asset lockfile flush. The transformer never writes
	// files/network beyond the upload inside Resolve; the lockfile PERSIST
	// happens HERE, after a successful build, so a cache-hit build does zero IO
	// beyond reading the lockfile (deterministic/parity-safe) and only a genuine
	// upload-on-miss rewrites rotor-lock.json atomically.
	wroteLockfile := false
	if pctx.assets != nil && pctx.assets.Dirty() {
		if err := pctx.assets.Lockfile().Save(filepath.FromSlash(dir)); err != nil {
			return nil, nil, fmt.Errorf("compile: writing %s: %w", assetsLockfileName, err)
		}
		wroteLockfile = true
	}

	return &BuildResult{
		Outputs:         outputs,
		EmittedFiles:    emittedFiles,
		OutputDir:       pathTranslator.OutDir,
		UsesEnvMacro:    usesEnvMacro,
		UsesAssetMacro:  usesAssetMacro,
		UsesMacros:      usesMacros,
		WroteRotorTypes: wroteRotorTypes,
		WroteLockfile:   wroteLockfile,
	}, nil, nil
}

func emitDeclarations(program *compiler.Program, selectedPaths map[string]struct{}, writeOnlyChanged bool) ([]string, error) {
	if !program.Options().Declaration.IsTrue() {
		return nil, nil
	}

	ctx := context.Background()
	var (
		emittedMu sync.Mutex
		emitted   []string
	)
	var jobs []func() error
	for _, sourceFile := range program.SourceFiles() {
		if sourceFile.IsDeclarationFile || !isCompilableFile(sourceFile.FileName()) {
			continue
		}
		if selectedPaths != nil {
			if _, ok := selectedPaths[normalizeSourceFilePath(sourceFile.FileName())]; !ok {
				continue
			}
		}
		jobs = append(jobs, func() error {
			result := program.Emit(ctx, compiler.EmitOptions{
				TargetSourceFile: sourceFile,
				EmitOnly:         compiler.EmitOnlyDts,
				WriteFile: func(fileName string, text string, data *compiler.WriteFileData) error {
					text = rewriteDeclarationTypeReferences(text)
					wrote, err := writeOutputFile(filepath.FromSlash(fileName), text, writeOnlyChanged)
					if !wrote && data != nil {
						data.SkippedDtsWrite = true
					}
					return err
				},
			})
			if result == nil {
				return nil
			}
			if len(result.Diagnostics) > 0 {
				return errors.New("compile: declaration emit diagnostics")
			}
			emittedMu.Lock()
			emitted = append(emitted, result.EmittedFiles...)
			emittedMu.Unlock()
			return nil
		})
	}
	if err := parallelize(writeWorkers(), jobs); err != nil {
		return nil, err
	}
	return emitted, nil
}

func rewriteDeclarationTypeReferences(text string) string {
	return strings.ReplaceAll(text, `types="types"`, `types="@rbxts/types"`)
}

// minifyOutputs rewrites every compiled .luau/.lua entry in outputs to its
// minified form in place (rotor's --minify build flag). A minifier diagnostic
// on rotor-generated Luau is an internal error — the compiler emits Luau the
// minifier's parser handles — so it fails the build loudly rather than writing
// truncated output.
func minifyOutputs(outputs map[string]string) error {
	for rel, text := range outputs {
		lower := strings.ToLower(rel)
		if !strings.HasSuffix(lower, ".luau") && !strings.HasSuffix(lower, ".lua") {
			continue
		}
		minified, diags := cst.Minify(text)
		if len(diags) != 0 {
			return fmt.Errorf("compile: --minify: internal error minifying %s: %s", rel, diags[0].Message)
		}
		outputs[rel] = minified
	}
	return nil
}

// assertLocalOutputPath rejects project-relative output paths that are
// absolute or traverse outside the project dir (e.g. "../x", "C:\x").
func assertLocalOutputPath(relOut string) error {
	if !filepath.IsLocal(filepath.FromSlash(relOut)) {
		return fmt.Errorf("compile: refusing to write output outside the project directory: %q", relOut)
	}
	return nil
}

func writeOutputFile(path string, text string, writeOnlyChanged bool) (bool, error) {
	if writeOnlyChanged {
		if existing, err := os.ReadFile(path); err == nil && string(existing) == text {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func maybeCopyInclude(dir string, opts ProjectOptions) error {
	if !opts.EmitIncludeFiles || opts.Type == "package" {
		return nil
	}
	_, isPackage, err := projectIsPackage(dir)
	if err != nil {
		return err
	}
	if opts.Type == "" && isPackage {
		return nil
	}

	includePath, err := resolveIncludePath(dir, opts.IncludePath)
	if err != nil {
		return err
	}
	var copyErr error
	logservice.BenchmarkIfVerbose("copy include files", func() {
		copyErr = includefiles.Copy(includePath)
	})
	return copyErr
}

func cleanupOutputs(pathTranslator *rojo.PathTranslator) {
	cleanupDirRecursively(pathTranslator, pathTranslator.OutDir)
}

func cleanupDirRecursively(pathTranslator *rojo.PathTranslator, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		itemPath := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if entry.Name() == ".git" {
				continue
			}
			cleanupDirRecursively(pathTranslator, itemPath)
		}
		tryRemoveOutput(pathTranslator, itemPath)
	}
}

func tryRemoveOutput(pathTranslator *rojo.PathTranslator, outPath string) {
	if !isOutputFileOrphaned(pathTranslator, outPath) {
		return
	}
	if err := os.RemoveAll(outPath); err == nil {
		logservice.WriteLineIfVerbose("remove " + outPath)
	}
}

func isOutputFileOrphaned(pathTranslator *rojo.PathTranslator, outPath string) bool {
	if strings.HasSuffix(outPath, ".d.ts") && !pathTranslator.Declaration {
		return true
	}
	for _, inputPath := range pathTranslator.GetInputPaths(outPath) {
		if _, err := os.Stat(inputPath); err == nil {
			return false
		}
	}
	if pathTranslator.BuildInfoOutputPath == outPath {
		return false
	}
	return true
}

func copyNonCompiledFiles(pathTranslator *rojo.PathTranslator, rootDirs []string, writeOnlyChanged bool) error {
	for _, rootDir := range rootDirs {
		rootDir = filepath.FromSlash(rootDir)
		if _, err := os.Stat(rootDir); err != nil {
			continue
		}
		if err := copyItem(pathTranslator, rootDir, writeOnlyChanged); err != nil {
			return err
		}
	}
	return nil
}

func copyItem(pathTranslator *rojo.PathTranslator, itemPath string, writeOnlyChanged bool) error {
	info, err := os.Stat(itemPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		outDir := pathTranslator.GetOutputPath(itemPath)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(itemPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyItem(pathTranslator, filepath.Join(itemPath, entry.Name()), writeOnlyChanged); err != nil {
				return err
			}
		}
		return nil
	}

	if strings.HasSuffix(itemPath, ".d.ts") {
		if !pathTranslator.Declaration {
			return nil
		}
	} else if isCompilableFile(itemPath) {
		return nil
	}

	dest := pathTranslator.GetOutputPath(itemPath)
	if writeOnlyChanged {
		if existing, err := os.ReadFile(dest); err == nil {
			incoming, err := os.ReadFile(itemPath)
			if err != nil {
				return err
			}
			if string(existing) == string(incoming) {
				return nil
			}
		}
	}

	data, err := os.ReadFile(itemPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

func isCompilableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if strings.HasSuffix(path, ".d.ts") {
		return false
	}
	return strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")
}
