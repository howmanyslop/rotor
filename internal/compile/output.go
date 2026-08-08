package compile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rotor/internal/assets"
	"rotor/internal/includefiles"
	"rotor/internal/logservice"
	"rotor/internal/luau/cst"
	"rotor/internal/rojo"
	"rotor/tsgo/ast"
	"rotor/tsgo/compiler"
	"rotor/tsgo/core"
	"rotor/tsgo/vfs/osvfs"
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
	writer := newOutputWriter()
	timings := opts.Timings
	if timings != nil {
		defer timings.finish()
		timings.setEffectiveWriteWorkers(writeWorkers())
	}
	stopInitialProgram := timings.startStage(initialProgramStage)
	dir, program, diags, err := newProjectProgramWithOptions(projectDir, opts.TsConfigPath, opts)
	stopInitialProgram()
	if err != nil {
		return nil, diags, err
	}
	pathTranslator := createPathTranslator(program, !opts.LuaExtension)
	sourceFiles := projectSourceFiles(program)
	timings.setSourceCounts(len(sourceFiles), len(sourceFiles))
	stopManifest := timings.startStage(incrementalManifestStage)
	manifestPath := pathTranslator.BuildInfoOutputPath
	if manifestPath == "" {
		manifestPath = outputManifestPath(filepath.FromSlash(dir), program.Options().ConfigFilePath)
	}
	previousManifest, err := readIncrementalManifest(manifestPath)
	if err != nil {
		stopManifest()
		return nil, nil, err
	}
	salt, err := incrementalSalt(program, opts, manifestPath)
	if err != nil {
		stopManifest()
		return nil, nil, err
	}
	var currentManifest *incrementalManifest
	if program.Options().IsIncremental() && pathTranslator.BuildInfoOutputPath != "" {
		currentManifest, err = buildIncrementalManifest(program, sourceFiles, salt, previousManifest)
		if err != nil {
			stopManifest()
			return nil, nil, err
		}
		if previousManifest != nil && previousManifest.Salt == currentManifest.Salt {
			for path, hash := range previousManifest.Outputs {
				currentManifest.Outputs[path] = hash
			}
		}
	} else {
		currentManifest = &incrementalManifest{
			Version: 2,
			Salt:    salt,
			Files:   map[string]incrementalFileState{},
			Outputs: map[string]string{},
		}
	}
	previousOutputs := map[string]string{}
	if previousManifest != nil && previousManifest.Salt == currentManifest.Salt {
		previousOutputs = previousManifest.Outputs
	}
	if err := writer.useHashes(filepath.FromSlash(dir), previousOutputs, currentManifest.Outputs); err != nil {
		stopManifest()
		return nil, nil, err
	}
	stopManifest()
	defer writer.close()
	previousPresence := writer.newOutputPresenceIndex(previousOutputs)
	if opts.EmitDeclarationOnly {
		if !program.Options().GetEmitDeclarations() {
			msg := "Option 'emitDeclarationOnly' cannot be specified without specifying option 'declaration' or option 'composite'."
			return nil, []string{msg}, errors.New("compile: TypeScript diagnostics")
		}
		prepared, sidecarDiags, err := prepareTransformerProgram(dir, program, sourceFiles, opts.Overlays)
		if err != nil {
			return nil, sidecarDiags, err
		}
		timings.recordPreparedTransformerProgram(prepared)
		stopDeclarations := timings.startStage(declarationEmitWritesStage)
		emitted, err := emitDeclarations(prepared.program, nil, opts.WriteOnlyChanged, prepared.declarations, writer, timings)
		stopDeclarations()
		if err != nil {
			return nil, nil, err
		}
		timings.setHashSkips(writer.hashSkipCount())
		stopPersistence := timings.startStage(persistenceStage)
		currentPresence := writer.newOutputPresenceIndex(currentManifest.Outputs)
		pruneMissingOutputs(currentPresence, currentManifest.Outputs)
		if !sameIncrementalManifest(previousManifest, currentManifest) {
			if err := writeIncrementalManifest(manifestPath, currentManifest); err != nil {
				stopPersistence()
				return nil, nil, err
			}
		}
		stopPersistence()
		timings.setEmittedEntries(len(emitted))
		return &BuildResult{Outputs: map[string]string{}, EmittedFiles: emitted}, nil, nil
	}

	stopSelectionCleanupCopy := timings.startStage(incrementalSelectionCleanupCopyStage)
	sourceOutputPaths := make([]string, len(sourceFiles))
	for i, sourceFile := range sourceFiles {
		sourceOutputPaths[i] = outputPathRelativeToDir(dir, pathTranslator.GetOutputPath(sourceFile.FileName()))
	}
	if err := rejectDuplicateOutputPaths(sourceOutputPaths); err != nil {
		stopSelectionCleanupCopy()
		return nil, nil, err
	}
	rojoCache := loadRojoCachesPreBuild(dir, opts)
	opts.rojoCache = rojoCache

	selectedFiles := sourceFiles
	if program.Options().IsIncremental() && pathTranslator.BuildInfoOutputPath != "" {
		selectedFiles = selectIncrementalSourceFiles(sourceFiles, currentManifest, previousManifest)
		if opts.forceFullBuild {
			selectedFiles = sourceFiles
		} else if len(previousOutputs) > 0 {
			selectedPaths := make(map[string]struct{}, len(selectedFiles))
			for _, sourceFile := range selectedFiles {
				selectedPaths[normalizeSourceFilePath(sourceFile.FileName())] = struct{}{}
			}
			for outputPath := range previousOutputs {
				if previousPresence.hasRegular(outputPath) {
					continue
				}
				absolutePath := filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(outputPath))
				inputPath := strings.TrimSuffix(absolutePath, ".map")
				for _, candidate := range pathTranslator.GetInputPaths(inputPath) {
					selectedPaths[normalizeSourceFilePath(candidate)] = struct{}{}
				}
			}
			selectedFiles = selectedFiles[:0]
			for _, sourceFile := range sourceFiles {
				if _, ok := selectedPaths[normalizeSourceFilePath(sourceFile.FileName())]; ok {
					selectedFiles = append(selectedFiles, sourceFile)
				}
			}
		}
	}

	copyFilesGate := loadCopyFilesGatePreBuild(copyFilesGateInputs{
		RootDirs:       getRootDirs(program),
		OutDir:         pathTranslator.OutDir,
		Declaration:    program.Options().Declaration.IsTrue(),
		PathTranslator: pathTranslator,
		Snapshot:       copyFilesChangedSnapshot(program, selectedFiles),
	})
	if !copyFilesGate.SkipCleanup {
		cleanupOutputs(pathTranslator, program.Options().SourceMap.IsTrue())
	}

	if err := maybeCopyInclude(dir, opts); err != nil {
		stopSelectionCleanupCopy()
		return nil, nil, err
	}
	if !copyFilesGate.SkipCopyFiles {
		if err := copyNonCompiledFiles(pathTranslator, getRootDirs(program), opts.WriteOnlyChanged); err != nil {
			stopSelectionCleanupCopy()
			return nil, nil, err
		}
	}
	stopSelectionCleanupCopy()
	timings.setSourceCounts(len(sourceFiles), len(selectedFiles))

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

	prepared, diags, err := prepareTransformerProgram(dir, program, selectedFiles, opts.Overlays)
	if err != nil {
		return nil, diags, err
	}
	timings.recordPreparedTransformerProgram(prepared)
	program = prepared.program
	selectedFiles = prepared.sourceFiles
	timings.setSourceCounts(len(sourceFiles), len(selectedFiles))

	stopProjectContext := timings.startStage(projectContextStage)
	pctx, diags, err := newProjectContext(dir, program, opts)
	stopProjectContext()
	if err != nil {
		return nil, diags, err
	}
	pctx.sourceTraces = prepared.sourceTraces
	stopNativeCompile := timings.startStage(nativeDiagnosticsTransformRenderStage)
	outputs, sourceMaps, infos, err := compileProjectSourceFiles(dir, program, pctx, selectedFiles, opts)
	stopNativeCompile()
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

	stopCompiledOutputWrites := timings.startStage(compiledOutputWritesStage)
	emittedFiles := make([]string, 0, len(outputs))
	relOuts := make([]string, 0, len(outputs))
	for _, sourceFile := range selectedFiles {
		relOut := outputPathRelativeToDir(dir, pathTranslator.GetOutputPath(sourceFile.FileName()))
		if _, ok := outputs[relOut]; ok {
			relOuts = append(relOuts, relOut)
		}
	}
	// The write loop is pure independent file I/O; fan it out across workers
	// (Windows file writes are ~30x slower per syscall than macOS/APFS, so
	// sequential writes dominate full-build wall time there). ROTOR_WRITE_WORKERS=1
	// reproduces the pre-parallel behavior for A/B timing.
	wrote := make([]bool, len(relOuts))
	jobs := make([]func() error, len(relOuts))
	writePaths := make([]string, 0, len(relOuts)*2)
	sourceFilesByOutput := make(map[string]*ast.SourceFile, len(selectedFiles))
	for _, sourceFile := range selectedFiles {
		relOut := outputPathRelativeToDir(dir, pathTranslator.GetOutputPath(sourceFile.FileName()))
		sourceFilesByOutput[relOut] = sourceFile
	}
	for i, relOut := range relOuts {
		sourceMap, hasSourceMap := sourceMaps[relOut+".map"]
		if hasSourceMap {
			timings.addScheduledSourceMapWrite()
		}
		if hasSourceMap {
			sourceFile := sourceFilesByOutput[relOut]
			if trace := prepared.sourceTraces[normalizeSourceFilePath(sourceFile.FileName())]; trace != nil {
				sourceMap, err = rewriteSourceMapWithTrace(sourceMap, trace)
				if err != nil {
					stopCompiledOutputWrites()
					return nil, nil, err
				}
			}
		}
		jobs[i] = func() error {
			// Defense-in-depth: output paths are derived from source/Rojo path
			// mappings; refuse any that would escape the project directory.
			if err := assertLocalOutputPath(relOut); err != nil {
				return err
			}
			absOut := filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(relOut))
			w, err := writer.write(absOut, outputs[relOut], opts.WriteOnlyChanged)
			wrote[i] = w
			timings.recordOutputWrite(absOut, w)
			if err != nil || !hasSourceMap {
				return err
			}
			mapWrote, err := writer.write(absOut+".map", sourceMap, opts.WriteOnlyChanged)
			timings.recordOutputWrite(absOut+".map", mapWrote)
			return err
		}
		absOut := filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(relOut))
		writePaths = append(writePaths, absOut)
		if hasSourceMap {
			writePaths = append(writePaths, absOut+".map")
		}
	}
	if err := writer.prepare(writePaths); err != nil {
		stopCompiledOutputWrites()
		return nil, nil, err
	}
	err = parallelize(writeWorkers(), jobs)
	stopCompiledOutputWrites()
	if err != nil {
		return nil, nil, err
	}
	for i, w := range wrote {
		if w {
			emittedFiles = append(emittedFiles, filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(relOuts[i])))
		}
	}
	timings.setEmittedEntries(len(emittedFiles))

	selectedPaths := make(map[string]struct{}, len(selectedFiles))
	for _, sourceFile := range selectedFiles {
		selectedPaths[normalizeSourceFilePath(sourceFile.FileName())] = struct{}{}
	}

	stopDeclarations := timings.startStage(declarationEmitWritesStage)
	declFiles, err := emitDeclarations(program, selectedPaths, opts.WriteOnlyChanged, prepared.declarations, writer, timings)
	stopDeclarations()
	if err != nil {
		return nil, nil, err
	}
	emittedFiles = append(emittedFiles, declFiles...)
	timings.setHashSkips(writer.hashSkipCount())

	stopPersistence := timings.startStage(persistenceStage)
	if copyFilesGate.SkipCleanup {
		// No cleanup ran and nothing was selected, so the output tree is
		// exactly the pre-build tree: the previous-output index is still valid.
		pruneMissingOutputs(previousPresence, currentManifest.Outputs)
	} else {
		// Cleanup and/or emission changed the tree; index the freshly written
		// output set so just-written files are never pruned.
		currentPresence := writer.newOutputPresenceIndex(currentManifest.Outputs)
		pruneMissingOutputs(currentPresence, currentManifest.Outputs)
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
			stopPersistence()
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
			stopPersistence()
			return nil, nil, fmt.Errorf("compile: writing %s: %w", assetsLockfileName, err)
		}
		wroteLockfile = true
	}
	persist := func() error {
		if rojoCache != nil {
			rojoCache.Persist()
		}
		if copyFilesGate.Persist != nil {
			if err := copyFilesGate.Persist(); err != nil {
				return err
			}
		}
		if currentManifest != nil && !sameIncrementalManifest(previousManifest, currentManifest) {
			return writeIncrementalManifest(manifestPath, currentManifest)
		}
		return nil
	}
	if opts.pendingSolutionPersists != nil {
		*opts.pendingSolutionPersists = append(*opts.pendingSolutionPersists, persist)
	} else {
		if err := persist(); err != nil {
			stopPersistence()
			return nil, nil, err
		}
	}
	stopPersistence()
	timings.setEmittedEntries(len(emittedFiles))

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

func loadRojoCachesPreBuild(dir string, opts ProjectOptions) *rojo.RojoResolverCache {
	rojoConfigPath, _, err := resolveRojoConfigPath(dir, opts.RojoConfigPath)
	if err != nil || rojoConfigPath == "" {
		return nil
	}
	cache := rojo.NewRojoResolverCacheWithDeferredPersist(filepath.Join(filepath.FromSlash(dir), ".rotor", "cache", "rojo"), core.Version(), opts.deferRojoCachePersist)
	cache.Load(rojoConfigPath)
	return cache
}

func emitDeclarations(program *compiler.Program, selectedPaths map[string]struct{}, writeOnlyChanged bool, sidecarDeclarations []sidecarOutputFile, writer *outputWriter, timings *BuildTimings) ([]string, error) {
	if !program.Options().GetEmitDeclarations() {
		return nil, nil
	}
	if len(sidecarDeclarations) > 0 {
		return writeSidecarDeclarations(sidecarDeclarations, writeOnlyChanged, writer, timings)
	}

	ctx := context.Background()
	type declarationWrite struct {
		path string
		text string
		data *compiler.WriteFileData
	}
	var pendingByJob [][]declarationWrite
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
		jobIndex := len(jobs)
		pendingByJob = append(pendingByJob, nil)
		jobs = append(jobs, func() error {
			var pending []declarationWrite
			result := program.Emit(ctx, compiler.EmitOptions{
				TargetSourceFile: sourceFile,
				EmitOnly:         compiler.EmitOnlyDts,
				WriteFile: func(fileName string, text string, data *compiler.WriteFileData) error {
					text = rewriteDeclarationTypeReferences(text)
					pending = append(pending, declarationWrite{path: filepath.FromSlash(fileName), text: text, data: data})
					return nil
				},
			})
			pendingByJob[jobIndex] = pending
			if result == nil {
				return nil
			}
			if len(result.Diagnostics) > 0 {
				return errors.New("compile: declaration emit diagnostics")
			}
			return nil
		})
	}
	if err := parallelize(writeWorkers(), jobs); err != nil {
		return nil, err
	}
	var pending []declarationWrite
	for _, writes := range pendingByJob {
		pending = append(pending, writes...)
	}
	paths := make([]string, len(pending))
	for i, write := range pending {
		paths[i] = write.path
	}
	if err := rejectDuplicateOutputPaths(paths); err != nil {
		return nil, err
	}
	timings.addScheduledDeclarationWrites(len(pending))
	if err := writer.prepare(paths); err != nil {
		return nil, err
	}
	wrote := make([]bool, len(pending))
	writeJobs := make([]func() error, len(pending))
	for i, write := range pending {
		writeJobs[i] = func() error {
			var err error
			wrote[i], err = writer.write(write.path, write.text, writeOnlyChanged)
			timings.recordOutputWrite(write.path, wrote[i])
			if !wrote[i] && write.data != nil {
				write.data.SkippedDtsWrite = true
			}
			return err
		}
	}
	if err := parallelize(writeWorkers(), writeJobs); err != nil {
		return nil, err
	}
	emitted := make([]string, 0, len(pending))
	for i, write := range pending {
		if wrote[i] {
			emitted = append(emitted, write.path)
		}
	}
	return emitted, nil
}

func writeSidecarDeclarations(declarations []sidecarOutputFile, writeOnlyChanged bool, writer *outputWriter, timings *BuildTimings) ([]string, error) {
	paths := make([]string, len(declarations))
	for i, declaration := range declarations {
		paths[i] = filepath.FromSlash(declaration.FileName)
	}
	if err := rejectDuplicateOutputPaths(paths); err != nil {
		return nil, err
	}
	timings.addScheduledDeclarationWrites(len(declarations))
	if err := writer.prepare(paths); err != nil {
		return nil, err
	}
	wrote := make([]bool, len(declarations))
	jobs := make([]func() error, len(declarations))
	for i, declaration := range declarations {
		jobs[i] = func() error {
			var err error
			path := filepath.FromSlash(declaration.FileName)
			wrote[i], err = writer.write(path, declaration.Text, writeOnlyChanged)
			timings.recordOutputWrite(path, wrote[i])
			return err
		}
	}
	if err := parallelize(writeWorkers(), jobs); err != nil {
		return nil, err
	}
	emitted := make([]string, 0, len(declarations))
	for i, declaration := range declarations {
		if wrote[i] {
			emitted = append(emitted, filepath.FromSlash(declaration.FileName))
		}
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

func outputPathRelativeToDir(dir, path string) string {
	relOut, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(path))
	if err != nil {
		relOut = filepath.FromSlash(path)
	}
	return filepath.ToSlash(relOut)
}

func rejectDuplicateOutputPaths(paths []string) error {
	seen := make(map[string]string, len(paths))
	caseSensitive := osvfs.FS().UseCaseSensitiveFileNames()
	for _, path := range paths {
		absolute, err := filepath.Abs(filepath.FromSlash(path))
		if err != nil {
			return fmt.Errorf("compile: resolving output path %q: %w", path, err)
		}
		key := filepath.Clean(absolute)
		if !caseSensitive {
			key = strings.ToLower(key)
		}
		if previous, ok := seen[key]; ok {
			return fmt.Errorf("compile: duplicate output path detected: %s and %s", previous, path)
		}
		seen[key] = path
	}
	return nil
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

func cleanupOutputs(pathTranslator *rojo.PathTranslator, sourceMapsEnabled bool) {
	cleanupDirRecursively(pathTranslator, pathTranslator.OutDir, sourceMapsEnabled)
}

func cleanupDirRecursively(pathTranslator *rojo.PathTranslator, dir string, sourceMapsEnabled bool) {
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
			cleanupDirRecursively(pathTranslator, itemPath, sourceMapsEnabled)
		}
		tryRemoveOutput(pathTranslator, itemPath, sourceMapsEnabled)
	}
}

func tryRemoveOutput(pathTranslator *rojo.PathTranslator, outPath string, sourceMapsEnabled bool) {
	if !isOutputFileOrphanedWithSourceMaps(pathTranslator, outPath, sourceMapsEnabled) {
		return
	}
	if err := os.RemoveAll(outPath); err == nil {
		logservice.WriteLineIfVerbose("remove " + outPath)
	}
}

func isOutputFileOrphaned(pathTranslator *rojo.PathTranslator, outPath string) bool {
	return isOutputFileOrphanedWithSourceMaps(pathTranslator, outPath, false)
}

func isOutputFileOrphanedWithSourceMaps(pathTranslator *rojo.PathTranslator, outPath string, sourceMapsEnabled bool) bool {
	if protectedOutputFilenames[filepath.Base(outPath)] {
		return false
	}
	if strings.HasSuffix(outPath, ".luau.map") {
		if !sourceMapsEnabled {
			return true
		}
		for _, inputPath := range pathTranslator.GetInputPaths(strings.TrimSuffix(outPath, ".map")) {
			if _, err := os.Stat(inputPath); err == nil {
				return false
			}
		}
		return true
	}
	if strings.HasSuffix(outPath, ".d.ts") && !pathTranslator.Declaration {
		return true
	}
	for _, inputPath := range pathTranslator.GetInputPaths(outPath) {
		if _, err := os.Stat(inputPath); err == nil {
			return false
		}
	}
	if strings.HasSuffix(outPath, ".map") {
		for _, inputPath := range pathTranslator.GetInputPaths(strings.TrimSuffix(outPath, ".map")) {
			if _, err := os.Stat(inputPath); err == nil {
				return false
			}
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
		if pathContains(itemPath, pathTranslator.OutDir) {
			entries, err := os.ReadDir(itemPath)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Name() == "node_modules" {
					continue
				}
				childPath := filepath.Join(itemPath, entry.Name())
				if filepath.Clean(childPath) == filepath.Clean(pathTranslator.OutDir) {
					continue
				}
				if err := copyItem(pathTranslator, childPath, writeOnlyChanged); err != nil {
					return err
				}
			}
			return nil
		}
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
			if bytes.Equal(existing, incoming) {
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

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
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
