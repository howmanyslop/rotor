package compile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"rotor/internal/assetresolve"
	"rotor/internal/assets"
	"rotor/internal/cloud"
	"rotor/internal/config"
	"rotor/internal/dotenv"
	"rotor/internal/logservice"
	"rotor/internal/rojo"
	"rotor/internal/transformer"
	"rotor/tsgo/ast"
	"rotor/tsgo/bundled"
	"rotor/tsgo/compiler"
	"rotor/tsgo/core"
	"rotor/tsgo/outputpaths"
	"rotor/tsgo/tspath"
	"rotor/tsgo/vfs/cachedvfs"
	"rotor/tsgo/vfs/osvfs"
)

// packageNameRegex ports createProjectData.ts PACKAGE_REGEX: a project whose
// package.json name has an npm scope compiles as ProjectType.Package.
var packageNameRegex = regexp.MustCompile(`^@[a-z0-9-]*/`)

// filenameWarnings ports Shared/constants.ts FILENAME_WARNINGS: `init.*` file
// names collide with Rojo's directory-script convention; checkFileName
// suggests the `index.*` spelling.
var filenameWarnings = func() map[string]string {
	m := make(map[string]string)
	for _, scriptType := range []string{".server", ".client", ""} {
		for _, fileType := range []string{".ts", ".tsx", ".d.ts"} {
			m["init"+scriptType+fileType] = "index" + scriptType + fileType
		}
	}
	return m
}()

// projectContext is the Go analog of upstream ProjectData plus the
// compileFiles.ts locals computed once per compilation pass (L49-98):
// RojoResolver, PathTranslator, inferred ProjectType, and the validated
// runtimeLibRbxPath, packaged as the transformer's RojoContext.
type projectContext struct {
	dir         string // abs slash project dir (upstream projectPath)
	projectType transformer.ProjectType
	rojoContext *transformer.RojoContext

	// env is the compile-time environment snapshot for the rotor $env macro,
	// loaded once per compile pass from the tsconfig directory (process env >
	// .env.<NODE_ENV> > .env; see internal/dotenv). Watch/incremental rebuilds
	// construct a fresh projectContext per pass, so .env edits are picked up
	// on the next build — but note the incremental manifest hashes sources
	// only, so files that inlined a stale value are not re-selected by an env
	// change alone (documented on dotenv.Env).
	env *dotenv.Env

	// assets is the build-time resolver for the rotor $asset macro, built once
	// per compile pass from rotor-lock.json + the [assets] config (creator) +
	// an optional Open Cloud client (cloud.FromEnv; nil offline). Shared by
	// every file's State (concurrency-safe). After a successful build the
	// pipeline persists rotor-lock.json when the resolver is Dirty (see
	// compileProjectSourceFiles' callers). nil only when construction itself
	// fails (it never does — a missing config/lockfile yields an offline
	// resolver that errors clearly on a $asset miss).
	assets *assetresolve.Resolver

	// files is the build-time data-file reader for the rotor $file macro, rooted
	// at the project dir; shared by every file's State.
	files *fileResolver

	// stamps is the build/VCS provider for the rotor $git / $buildTime macros,
	// built once per pass with a fixed build timestamp; shared by every file's
	// State (concurrency-safe; reads memoized). The interface type lets tests
	// inject a deterministic fake via stampProviderOverride.
	stamps transformer.StampProvider
}

// newProjectProgram builds the tsgo Program for projectDir over the sanitized
// config — the shared front half of CompileFile and CompileProject.
// tsConfigPath selects a custom config file ("" = projectDir/tsconfig.json;
// upstream `--project` may name any config file, CLI/commands/build.ts
// L31-40).
func newProjectProgram(projectDir, tsConfigPath string) (string, *compiler.Program, []string, error) {
	dir, err := filepath.Abs(projectDir)
	if err != nil {
		return "", nil, nil, err
	}
	dir = filepath.ToSlash(dir)

	configPath := dir + "/tsconfig.json"
	if tsConfigPath != "" {
		abs, err := filepath.Abs(tsConfigPath)
		if err != nil {
			return "", nil, nil, err
		}
		configPath = filepath.ToSlash(abs)
	}

	// PERF: a single compile reads each candidate path many times — module
	// resolution alone re-stats overlapping node_modules/@rbxts directories
	// once per importing file (profiled at ~40% of warm compile time, mostly
	// GetFileAttributesEx syscalls). cachedvfs memoizes Stat/FileExists/
	// DirectoryExists/Realpath/GetAccessibleEntries (ReadFile passes through, so
	// no content blowup) behind thread-safe SyncMaps — the same wrapper tsgo's
	// own project/LSP host uses. Safe because a build never mutates its source
	// tree mid-pass, so cached file metadata cannot go stale.
	fs := cachedvfs.From(SanitizeFSWithConfigPath(bundled.WrapFS(osvfs.FS()), configPath))
	program, diags, err := newProjectProgramFromFS(dir, configPath, fs)
	if err != nil {
		return "", nil, diags, err
	}
	return dir, program, nil, nil
}

// projectIsPackage ports the isPackage detection of createProjectData.ts
// L15-26: package.json discovery walks up from the project path
// (ts.findPackageJson); a missing package.json is an error, an unreadable one
// just means "not a package". A scoped name (PACKAGE_REGEX) marks the project
// as a package.
func projectIsPackage(dir string) (pkgJSONPath string, isPackage bool, err error) {
	pkgJSONPath = findPackageJSON(dir)
	if pkgJSONPath == "" {
		return "", false, errors.New("compile: Unable to find package.json")
	}
	if data, err := os.ReadFile(pkgJSONPath); err == nil {
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			isPackage = packageNameRegex.MatchString(pkg.Name)
		}
	}
	return pkgJSONPath, isPackage, nil
}

// resolveIncludePath ports createProjectData.ts L29:
// `path.resolve(projectOptions.includePath || path.join(projectPath, "include"))`
// — the empty string (DEFAULT_PROJECT_OPTIONS.includePath) falls back to
// <projectPath>/include; a non-empty --includePath is resolved against the
// process working directory exactly like Node's path.resolve.
func resolveIncludePath(dir, includePath string) (string, error) {
	if includePath == "" {
		return filepath.Join(filepath.FromSlash(dir), "include"), nil
	}
	return filepath.Abs(filepath.FromSlash(includePath))
}

// newProjectContext ports the project-level setup of compileFiles.ts L56-100
// (with createProjectData.ts feeding it): RojoResolver construction,
// checkRojoConfig/checkFileName, ProjectType selection, and runtimeLibRbxPath
// discovery + validation. The four plain-text emit failures (compileFiles.ts
// L83-96) are hard errors, returned as diagnostics alongside the error.
// opts.IncludePath is the raw --includePath value ("" = default), resolved
// per createProjectData.ts L29 before the RuntimeLib.lua Rojo lookup
// (compileFiles.ts L88-89).
//
// ProjectType selection, as upstream (compileFiles.ts L80): opts.Type when
// set (the --type override), else inferred:
//   - package.json name has an npm scope (PACKAGE_REGEX)  -> Package
//   - the Rojo tree declares $className DataModel (isGame) -> Game
//   - otherwise                                            -> Model
func newProjectContext(dir string, program *compiler.Program, opts ProjectOptions) (*projectContext, []string, error) {
	options := program.Options()
	outDir := options.OutDir

	pkgJSONPath, isPackage, err := projectIsPackage(dir)
	if err != nil {
		return nil, nil, err
	}

	includePath, err := resolveIncludePath(dir, opts.IncludePath)
	if err != nil {
		return nil, nil, err
	}

	// createProjectData.ts L33-43: a truthy --rojo overrides discovery
	// (path.resolve'd); QUIRK: `--rojo ""` (empty string) falls through to
	// auto-discovery, whose warnings go to LogService.warn (they never fail a
	// compile upstream).
	var rojoConfigPath string
	if opts.RojoConfigPath != "" {
		abs, err := filepath.Abs(filepath.FromSlash(opts.RojoConfigPath))
		if err != nil {
			return nil, nil, err
		}
		rojoConfigPath = abs
	} else {
		var rojoWarnings []string
		rojoConfigPath, rojoWarnings = rojo.FindRojoConfigFilePath(filepath.FromSlash(dir))
		for _, warning := range rojoWarnings {
			logservice.Warn(warning)
		}
	}

	// compileFiles.ts L61-63.
	var rojoResolver *rojo.RojoResolver
	if rojoConfigPath != "" {
		rojoResolver = rojo.FromPath(rojoConfigPath)
	} else {
		rojoResolver = rojo.Synthetic(filepath.FromSlash(outDir))
	}

	// compileFiles.ts L65-67: resolver parse warnings → LogService.warn.
	for _, warning := range rojoResolver.GetWarnings() {
		logservice.Warn(warning)
	}

	pathTranslator := createPathTranslator(program, !opts.LuaExtension)

	// checkRojoConfig + checkFileName queue project-level diagnostics
	// (compileFiles.ts L69-75); upstream flushes them only after the emit
	// failures below get their early returns, so the queue is checked last.
	var checkDiags []string
	checkDiags = append(checkDiags, checkRojoConfig(rojoConfigPath, rojoResolver, getRootDirs(program), pathTranslator)...)
	nodeModulesPath := filepath.Join(filepath.Dir(pkgJSONPath), "node_modules")
	for _, sourceFile := range program.SourceFiles() {
		fileName := filepath.FromSlash(sourceFile.FileName())
		if !strings.HasPrefix(fileName, nodeModulesPath) {
			if msg := checkFileName(fileName); msg != "" {
				checkDiags = append(checkDiags, msg)
			}
		}
	}

	// compileFiles.ts L80: data.projectOptions.type ?? inferProjectType(...).
	projectType := opts.Type
	if projectType == "" {
		switch {
		case isPackage:
			projectType = transformer.ProjectTypePackage
		case rojoResolver.IsGame:
			projectType = transformer.ProjectTypeGame
		default:
			projectType = transformer.ProjectTypeModel
		}
	}

	// The four plain-text emit failures (compileFiles.ts L82-98) — hard
	// errors per digest §7/§8.
	if projectType != transformer.ProjectTypePackage && rojoConfigPath == "" {
		return nil, []string{"Non-package projects must have a Rojo project file!"}, errors.New("compile: emit failure")
	}
	var runtimeLibRbxPath rojo.RbxPath
	if projectType != transformer.ProjectTypePackage {
		var ok bool
		runtimeLibRbxPath, ok = rojoResolver.GetRbxPathFromFilePath(filepath.Join(includePath, "RuntimeLib.lua"))
		if !ok {
			return nil, []string{"Rojo project contained no data for include folder!"}, errors.New("compile: emit failure")
		} else if rojoResolver.GetNetworkType(runtimeLibRbxPath) != rojo.NetworkTypeUnknown {
			return nil, []string{"Runtime library cannot be in a server-only or client-only container!"}, errors.New("compile: emit failure")
		} else if rojoResolver.IsIsolated(runtimeLibRbxPath) {
			return nil, []string{"Runtime library cannot be in an isolated container!"}, errors.New("compile: emit failure")
		}
	}

	if len(checkDiags) > 0 {
		return nil, checkDiags, errors.New("compile: project configuration diagnostics")
	}

	// Import-resolution context (compileFiles.ts L77-78): one synthetic
	// resolver per typeRoot for Package-project node_modules imports, and the
	// types-entry -> shipped-main mapping for everyone else. tsgo resolves
	// compilerOptions.typeRoots to absolute slash paths during config parse.
	useCaseSensitiveFileNames := osvfs.FS().UseCaseSensitiveFileNames()
	typeRoots := options.TypeRoots
	pkgRojoResolvers := make([]*rojo.RojoResolver, 0, len(typeRoots))
	for _, typeRoot := range typeRoots {
		pkgRojoResolvers = append(pkgRojoResolvers, rojo.Synthetic(filepath.FromSlash(typeRoot)))
	}

	// $env macro environment: the .env files live in the directory containing
	// the tsconfig (== dir unless --project pointed elsewhere).
	envDir := filepath.FromSlash(dir)
	if configFilePath := options.ConfigFilePath; configFilePath != "" {
		envDir = filepath.Dir(filepath.FromSlash(configFilePath))
	}

	return &projectContext{
		dir:         dir,
		projectType: projectType,
		env:         dotenv.Load(envDir),
		assets:      newAssetResolver(envDir),
		files:       newFileResolver(envDir),
		stamps:      resolveStampProvider(envDir),
		rojoContext: &transformer.RojoContext{
			Resolver:          rojoResolver,
			PathTranslator:    pathTranslator,
			RuntimeLibRbxPath: runtimeLibRbxPath,
			ProjectPath:       filepath.FromSlash(dir),

			PkgRojoResolvers:          pkgRojoResolvers,
			NodeModulesPathMapping:    createNodeModulesPathMapping(typeRoots, useCaseSensitiveFileNames),
			NodeModulesPath:           nodeModulesPath,
			TypeRoots:                 typeRoots,
			UseCaseSensitiveFileNames: useCaseSensitiveFileNames,
		},
	}, nil, nil
}

// newAssetResolver builds the build-time $asset resolver for one compile pass
// (mirroring how dotenv.Load builds the $env snapshot). It loads
// rotor-lock.json from projectDir (a missing/corrupt lockfile yields an empty
// one — corrupt is tolerated here so a bad lockfile cannot abort an otherwise
// valid build; the $asset macro then errors clearly per-reference), reads the
// [assets].creator from rotor.toml (ErrNotFound tolerated → no creator), and
// constructs an Open Cloud client via cloud.FromEnv (nil when ROBLOX_API_KEY
// is unset). With no client/creator the resolver is deterministic and offline:
// a $asset cache hit inlines, a cache miss errors with rotorAssetNotCached.
func newAssetResolver(projectDir string) *assetresolve.Resolver {
	lock, err := assets.LoadLockfile(projectDir)
	if err != nil {
		// A corrupt lockfile shouldn't abort the build; start empty so every
		// $asset reference surfaces a clear miss diagnostic instead.
		lock = assets.NewLockfile()
	}

	var creator cloud.Creator
	var base string
	if cfg, err := config.Load(projectDir); err == nil && cfg.Assets != nil {
		base = cfg.Assets.Base
		switch cfg.Assets.Creator.Type {
		case "user":
			creator.UserID = cfg.Assets.Creator.ID
		case "group":
			creator.GroupID = cfg.Assets.Creator.ID
		}
	}

	// Only build a cloud client when a creator is configured (uploads need an
	// owner anyway); cloud.FromEnv returns nil without ROBLOX_API_KEY.
	var client assets.Cloud
	if creator.UserID != 0 || creator.GroupID != 0 {
		if c, err := cloud.FromEnv(); err == nil {
			client = c
		}
	}

	return assetresolve.New(assetresolve.Options{
		ProjectDir: projectDir,
		Base:       base,
		Lockfile:   lock,
		Client:     client,
		Creator:    creator,
	})
}

// createNodeModulesPathMapping ports
// Project/functions/createNodeModulesPathMapping.ts: for each package under
// each typeRoot, map the canonical resolved types/typings entry (.d.ts) to
// the resolved main entry (the shipped .lua) — only when main is present.
func createNodeModulesPathMapping(typeRoots []string, useCaseSensitiveFileNames bool) map[string]string {
	mapping := make(map[string]string)
	for _, typeRoot := range typeRoots {
		scopePath := filepath.FromSlash(typeRoot)
		entries, err := os.ReadDir(scopePath)
		if err != nil {
			continue // fs.pathExistsSync guard
		}
		for _, entry := range entries {
			pkgPath := filepath.Join(scopePath, entry.Name())
			// realPathExistsSync: os.ReadFile follows symlinks; a missing or
			// unreadable package.json just skips the package.
			data, err := os.ReadFile(filepath.Join(pkgPath, "package.json"))
			if err != nil {
				continue
			}
			var pkg struct {
				Main    string `json:"main"`
				Typings string `json:"typings"`
				Types   string `json:"types"`
			}
			if json.Unmarshal(data, &pkg) != nil {
				continue
			}
			// both "types" and "typings" are valid
			typesPath := pkg.Types
			if typesPath == "" {
				typesPath = pkg.Typings
			}
			if typesPath == "" {
				typesPath = "index.d.ts"
			}
			if pkg.Main != "" {
				key := rojo.CanonicalFileName(resolveAgainst(pkgPath, typesPath), useCaseSensitiveFileNames)
				mapping[key] = resolveAgainst(pkgPath, pkg.Main)
			}
		}
	}
	return mapping
}

// resolveAgainst mirrors Node path.resolve(base, p) for the two-argument
// case used above.
func resolveAgainst(base, p string) string {
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// ProjectOptions carries the CLI-controlled knobs of upstream ProjectOptions
// (Shared/types.ts) that affect compilation. Zero value = upstream defaults
// without any include emission, preserving the original CompileProject
// behavior (pure: nothing but the returned map is produced).
type ProjectOptions struct {
	// IncludePath is the raw --includePath value; "" applies upstream's
	// default of <projectDir>/include (createProjectData.ts L29). It feeds
	// both the RuntimeLib.lua Rojo-path validation (compileFiles.ts L88-89)
	// and, when EmitIncludeFiles is set, the copy destination.
	IncludePath string

	// EmitIncludeFiles asks the compile to perform copyInclude.ts: write the
	// embedded runtime library (RuntimeLib.lua, Promise.lua) to the resolved
	// include path. `rotor build` sets this unless --noInclude
	// (copyInclude.ts L8); tests and `rotor check` leave it false.
	EmitIncludeFiles bool

	// Type overrides ProjectType inference — upstream's
	// `data.projectOptions.type ?? inferProjectType(...)` (compileFiles.ts
	// L80), fed by the `--type` CLI flag (CLI/commands/build.ts L98-101,
	// choices game|model|package). Empty means infer.
	Type transformer.ProjectType

	// TsConfigPath selects a custom config file (the CLI's --project may
	// resolve to any file path, CLI/commands/build.ts L31-40). "" means
	// <projectDir>/tsconfig.json — the original CompileProject behavior.
	TsConfigPath string

	// RojoConfigPath is the --rojo override (createProjectData.ts L33-43):
	// non-empty values are path.resolve'd and used verbatim; QUIRK verbatim
	// from upstream's truthiness check, "" (including an explicit `--rojo ""`)
	// falls through to auto-discovery.
	RojoConfigPath string

	// LogTruthyChanges plumbs --logTruthyChanges into the transformer's
	// truthiness warnings (State.LogTruthyChanges, consumed by
	// createTruthinessChecks).
	LogTruthyChanges bool

	// AllowCommentDirectives plumbs --allowCommentDirectives. Its consumer —
	// the fileUsesCommentDirectives pre-emit check (digest §2.9) — is Phase 4
	// Task 2 work; until then the option is carried but nothing reads it (the
	// @ts-ignore diagnostics it would suppress are not yet emitted either, so
	// behavior already matches allowCommentDirectives=true).
	AllowCommentDirectives bool

	// NoOptimizedLoops is the INVERSE of upstream optimizedLoops (default
	// true, DEFAULT_PROJECT_OPTIONS), inverted so this struct's zero value
	// keeps the upstream-default (optimized) behavior for all existing
	// callers. Set from `--optimizedLoops=false`; gates
	// transformForStatementOptimized via State.OptimizedLoops.
	NoOptimizedLoops bool

	// LuaExtension is the INVERSE of upstream luau (default true), inverted
	// for the same zero-value reason: zero emits `.luau`
	// (DEFAULT_PROJECT_OPTIONS.luau = true); set from `--luau=false` to emit
	// `.lua` (createPathTranslator.ts L17).
	LuaExtension bool

	// WriteOnlyChanged ports the build write-phase and copyItem byte-compare
	// skip: unchanged compiled outputs and copied passthrough files are left
	// untouched on disk.
	WriteOnlyChanged bool

	// MinifyOutput is a rotor extension (no rbxtsc analog): when set, every
	// emitted `.luau`/`.lua` source is passed through the Luau minifier
	// (internal/luau/cst) before it is written, stripping comments + whitespace
	// and collapsing string indexing to field access. Semantics-preserving; the
	// default (false) leaves output byte-identical to rbxtsc. Declaration
	// (`.d.ts`) and include files are never minified.
	MinifyOutput bool
}

// CompileProject compiles every file of the project rooted at projectDir —
// the Go analog of upstream compileFiles.ts: ONE Program, the Rojo context
// computed once, then per file: pre-emit diagnostics -> TransformState ->
// transformSourceFile -> render. The result maps project-relative output
// paths (slash-separated, e.g. "out/main.luau") to rendered Luau text.
// Like CompileFile, any diagnostics fail the compile: text map nil,
// diagnostics returned as strings alongside a hard error.
func CompileProject(projectDir string) (map[string]string, []string, error) {
	return CompileProjectWithOptions(projectDir, ProjectOptions{})
}

// CompileProjectWithOptions is CompileProject with the CLI knobs applied
// (--type ProjectType override, --includePath, include emission); the zero
// options value is exactly CompileProject. The include copy happens at
// upstream's point in the pipeline (CLI/commands/build.ts L140-145:
// createProjectProgram, then copyInclude, then compileFiles): after the
// Program builds — a broken tsconfig still prevents the copy — but before any
// project validation or per-file diagnostics, so type errors in source files
// do not stop the runtime library from landing.
func CompileProjectWithOptions(projectDir string, opts ProjectOptions) (map[string]string, []string, error) {
	dir, program, diags, err := newProjectProgram(projectDir, opts.TsConfigPath)
	if err != nil {
		return nil, diags, err
	}
	if err := maybeCopyInclude(dir, opts); err != nil {
		return nil, nil, err
	}
	outputs, infos, err := compileProjectProgram(dir, program, opts)
	return outputs, diagnosticInfoMessages(infos), err
}

func compileProjectProgram(dir string, program *compiler.Program, opts ProjectOptions) (map[string]string, []DiagnosticInfo, error) {
	sourceFiles := projectSourceFiles(program)
	program, sourceFiles, diags, err := prepareProjectProgramForCompile(dir, program, sourceFiles)
	if err != nil {
		return nil, stringDiagnostics(diags), err
	}
	pctx, pctxDiags, err := newProjectContext(dir, program, opts)
	if err != nil {
		return nil, stringDiagnostics(pctxDiags), err
	}
	return compileProjectSourceFiles(dir, program, pctx, sourceFiles, opts)
}

func projectSourceFiles(program *compiler.Program) []*ast.SourceFile {
	var sourceFiles []*ast.SourceFile
	for _, sourceFile := range program.SourceFiles() {
		fileName := sourceFile.FileName()
		if sourceFile.IsDeclarationFile ||
			(!strings.HasSuffix(fileName, ".ts") && !strings.HasSuffix(fileName, ".tsx")) {
			continue
		}
		sourceFiles = append(sourceFiles, sourceFile)
	}
	return sourceFiles
}

type checkerSourceFileGroup struct {
	indices []int
	files   []*ast.SourceFile
}

type compiledProjectSourceFile struct {
	relOut string
	text   string
	diags  []DiagnosticInfo
	err    error
}

type precheckedProjectSourceFile struct {
	tsDiags      []*ast.Diagnostic
	commentDiags []string
}

func compileProjectSourceFiles(dir string, program *compiler.Program, pctx *projectContext, sourceFiles []*ast.SourceFile, opts ProjectOptions) (map[string]string, []DiagnosticInfo, error) {
	ctx := context.Background()

	// Program-level option diagnostics fail the compile before any file is
	// transformed, mirroring CompileFile.
	if tsDiags := program.GetProgramDiagnostics(); len(tsDiags) > 0 {
		return nil, tsDiagnosticInfos(tsDiags), errors.New("compile: TypeScript diagnostics")
	}

	// compileFiles.ts L102 — note the TWO dots.
	logservice.WriteLineIfVerbose("compiling as " + string(pctx.projectType) + "..")

	results := make([]compiledProjectSourceFile, len(sourceFiles))
	prechecks := make([]precheckedProjectSourceFile, len(sourceFiles))
	progressLabels := compileProjectProgressLabels(sourceFiles)
	groups := groupSourceFilesByChecker(ctx, program, sourceFiles)

	wg := core.NewWorkGroup(program.SingleThreaded() || len(groups) <= 1)
	for _, group := range groups {
		group := group
		wg.Queue(func() {
			for i, sourceFile := range group.files {
				prechecks[group.indices[i]] = precheckProjectSourceFile(ctx, program, sourceFile, opts)
			}
		})
	}
	wg.RunAndWait()

	for _, precheck := range prechecks {
		if len(precheck.tsDiags) > 0 {
			return nil, tsDiagnosticInfos(precheck.tsDiags), errors.New("compile: TypeScript diagnostics")
		}
		if len(precheck.commentDiags) > 0 {
			return nil, stringDiagnostics(precheck.commentDiags), errors.New("compile: comment directive diagnostics")
		}
	}
	if tsDiags := program.GetGlobalDiagnostics(ctx); len(tsDiags) > 0 {
		return nil, tsDiagnosticInfos(tsDiags), errors.New("compile: TypeScript diagnostics")
	}

	wg = core.NewWorkGroup(program.SingleThreaded() || len(groups) <= 1)
	for _, group := range groups {
		group := group
		wg.Queue(func() {
			multi := transformer.NewMultiState()
			for i, sourceFile := range group.files {
				results[group.indices[i]] = compileProjectSourceFile(ctx, dir, program, pctx, sourceFile, opts, multi, progressLabels[group.indices[i]])
			}
		})
	}
	wg.RunAndWait()

	outputs := make(map[string]string, len(results))
	for _, result := range results {
		if result.err != nil {
			return nil, result.diags, result.err
		}
		outputs[result.relOut] = result.text
	}

	return outputs, nil, nil
}

func compileProjectProgressLabels(sourceFiles []*ast.SourceFile) []string {
	progressMaxLength := len(fmt.Sprintf("%d/%d", len(sourceFiles), len(sourceFiles)))
	cwd, cwdErr := os.Getwd()
	labels := make([]string, len(sourceFiles))
	for i, sourceFile := range sourceFiles {
		progress := fmt.Sprintf("%*s", progressMaxLength, fmt.Sprintf("%d/%d", i+1, len(sourceFiles)))
		relName := filepath.FromSlash(sourceFile.FileName())
		if cwdErr == nil {
			if rel, err := filepath.Rel(cwd, relName); err == nil {
				relName = rel
			}
		}
		labels[i] = progress + " compile " + relName
	}
	return labels
}

func groupSourceFilesByChecker(ctx context.Context, program *compiler.Program, sourceFiles []*ast.SourceFile) []checkerSourceFileGroup {
	groupsByChecker := map[any]int{}
	var groups []checkerSourceFileGroup
	for i, sourceFile := range sourceFiles {
		chk, release := program.GetTypeCheckerForFileExclusive(ctx, sourceFile)
		key := any(chk)
		release()

		groupIndex, ok := groupsByChecker[key]
		if !ok {
			groupIndex = len(groups)
			groupsByChecker[key] = groupIndex
			groups = append(groups, checkerSourceFileGroup{})
		}
		groups[groupIndex].indices = append(groups[groupIndex].indices, i)
		groups[groupIndex].files = append(groups[groupIndex].files, sourceFile)
	}
	return groups
}

func precheckProjectSourceFile(ctx context.Context, program *compiler.Program, sourceFile *ast.SourceFile, opts ProjectOptions) precheckedProjectSourceFile {
	result := precheckedProjectSourceFile{}
	result.tsDiags = preEmitProjectFileDiagnostics(ctx, program, sourceFile)
	if len(result.tsDiags) == 0 && !opts.AllowCommentDirectives {
		result.commentDiags = commentDirectiveDiagnostics(sourceFile)
	}
	return result
}

func compileProjectSourceFile(ctx context.Context, dir string, program *compiler.Program, pctx *projectContext, sourceFile *ast.SourceFile, opts ProjectOptions, multi *transformer.MultiState, progressLabel string) compiledProjectSourceFile {
	result := compiledProjectSourceFile{}
	logservice.BenchmarkIfVerbose(progressLabel, func() {
		chk, release := program.GetTypeCheckerForFile(ctx, sourceFile)
		defer release()

		state := transformer.NewState(program, chk, sourceFile, transformer.NewDiagService(), multi)
		// Macro registration audit (digest §6), mirroring upstream's
		// ProjectError-at-construction: the first NewState built the pass
		// MacroManager; fail before transforming anything when
		// registrations are missing while the types packages are present.
		if missing := state.Macros().Missing(); len(missing) > 0 {
			result.diags = stringDiagnostics(missing)
			result.err = errors.New("compile: macro registration failure")
			return
		}
		state.SetRojoContext(pctx.rojoContext, pctx.projectType)
		state.Env = pctx.env
		state.Assets = pctx.assets
		state.Files = pctx.files
		state.Stamps = pctx.stamps
		state.LogTruthyChanges = opts.LogTruthyChanges
		state.OptimizedLoops = !opts.NoOptimizedLoops

		text, diags, err := transformAndRenderDetailed(state)
		if err != nil {
			result.err = err
			return
		}
		if len(diags) > 0 {
			result.diags = diags
			result.err = errors.New("compile: transformer diagnostics")
			return
		}

		outPath := pctx.rojoContext.PathTranslator.GetOutputPath(sourceFile.FileName())
		relOut, err := filepath.Rel(filepath.FromSlash(dir), outPath)
		if err != nil {
			relOut = outPath
		}
		result.relOut = filepath.ToSlash(relOut)
		result.text = text
	})
	return result
}

// createPathTranslator ports Project/functions/createPathTranslator.ts:
// rootDir is the common ancestor of the program's common source directory and
// the configured rootDir(s); the buildInfo path is irrelevant to translation
// (the translator stores but never consults it). useLuauExtension is
// upstream's `data.projectOptions.luau` (createPathTranslator.ts L17,
// DEFAULT_PROJECT_OPTIONS.luau = true → .luau; --luau=false → .lua).
func createPathTranslator(program *compiler.Program, useLuauExtension bool) *rojo.PathTranslator {
	options := program.Options()
	dirs := append([]string{program.CommonSourceDirectory()}, getRootDirs(program)...)
	rootDir := findAncestorDir(dirs)
	outDir := filepath.FromSlash(options.OutDir)
	currentDirectory := rootDir
	if options.ConfigFilePath != "" {
		currentDirectory = filepath.Dir(filepath.FromSlash(options.ConfigFilePath))
	}
	buildInfoOutputPath := outputpaths.GetBuildInfoFileName(options, tspath.ComparePathsOptions{
		CurrentDirectory:          currentDirectory,
		UseCaseSensitiveFileNames: osvfs.FS().UseCaseSensitiveFileNames(),
	})
	return rojo.NewPathTranslator(rootDir, outDir, filepath.FromSlash(buildInfoOutputPath), options.Declaration.IsTrue(), useLuauExtension)
}

// rawEnforcedOptions carries the user-written values of the compilerOptions
// that validateCompilerOptions must inspect but that the pipeline alters
// before tsoptions parses the config:
//
//   - moduleResolution: SanitizeTSConfig rewrites "Node"/"node10" to
//     "bundler" (TS7 removed node10), so the parsed option can never equal
//     Node10 — upstream's check (L57-59) is only satisfiable against the raw
//     value.
//   - types: SanitizeTSConfig injects `"types": ["*"]` when the user wrote
//     none (TS5 auto-inclusion repair); upstream's per-entry existence check
//     (L70-86) must see the USER's entries — none, when absent — or the
//     injected "*" would produce a spurious "were not found" error.
//   - importsNotUsedAsValues: tsgo doesn't declare the option at all (removed
//     post-TS5), so tsoptions would fail with "Unknown compiler option" before
//     validation ever ran; SanitizeTSConfig strips it and the raw value feeds
//     upstream's deprecation error (L97-104) byte-exactly.
//
// Everything else validateCompilerOptions checks (noLib, strict, module,
// moduleDetection, allowSyntheticDefaultImports, typeRoots, rootDir/rootDirs,
// outDir) is untouched by the sanitizer and validated on the PARSED options,
// exactly like upstream.
type rawEnforcedOptions struct {
	moduleResolution    string // raw text; "" when absent or non-string
	hasModuleResolution bool
	types               []string // raw entries; nil when absent
	importsNotUsed      string   // raw text; "" when absent or non-string
	hasImportsNotUsed   bool
}

// readRawEnforcedOptions extracts rawEnforcedOptions from the unsanitized
// tsconfig.json text. Same root-file-only scope as SanitizeTSConfig (its
// documented "extends" gap): an extended config carrying these options is
// neither sanitized nor raw-validated. Unreadable/unparsable input returns the
// zero value — tsoptions reports the parse error itself, before validation.
func readRawEnforcedOptions(configPath string) rawEnforcedOptions {
	var raw rawEnforcedOptions
	data, err := os.ReadFile(configPath)
	if err != nil {
		return raw
	}
	var root map[string]any
	if json.Unmarshal([]byte(stripJSONC(string(data))), &root) != nil {
		return raw
	}
	co, ok := root["compilerOptions"].(map[string]any)
	if !ok {
		return raw
	}
	if v, ok := co["moduleResolution"]; ok {
		raw.hasModuleResolution = true
		raw.moduleResolution, _ = v.(string)
	}
	if list, ok := co["types"].([]any); ok {
		for _, e := range list {
			if s, ok := e.(string); ok {
				raw.types = append(raw.types, s)
			}
		}
	}
	if v, ok := co["importsNotUsedAsValues"]; ok {
		raw.hasImportsNotUsed = true
		raw.importsNotUsed, _ = v.(string)
	}
	return raw
}

// validateCompilerOptions is the full port of
// Project/functions/validateCompilerOptions.ts: every check upstream enforces,
// in upstream order, with the exact ProjectError message text (L107-115) —
// kleur.yellow stripped (color, not bytes, when piped), per-error trailing
// newlines included. projectPath is the abs slash project dir (upstream
// data.projectPath = dirname(tsConfigPath)); raw carries the pre-sanitization
// option values (see rawEnforcedOptions for the per-option rationale).
func validateCompilerOptions(options *core.CompilerOptions, projectPath string, raw rawEnforcedOptions) string {
	var errs []string

	// required compiler options (L37-63). The Tristate/enum zero values mean
	// "not written", matching upstream's `!== <enforced>` over possibly
	// undefined raw options.
	if options.NoLib != core.TSTrue {
		errs = append(errs, `"noLib" must be true`)
	}
	if options.Strict != core.TSTrue {
		errs = append(errs, `"strict" must be true`)
	}
	// L45-47: the target check is commented out upstream — not enforced.
	if options.Module != core.ModuleKindCommonJS {
		errs = append(errs, `"module" must be commonjs`)
	}
	if options.ModuleDetection != core.ModuleDetectionKindForce {
		errs = append(errs, `"moduleDetection" must be "force"`)
	}
	// L57-59: raw value (sanitizer rewrites it; see rawEnforcedOptions).
	// "node" and "node10" are the two spellings TS5 parses to Node10 —
	// tsconfig enum values are matched case-insensitively (the same set
	// SanitizeTSConfig rewrites).
	if !raw.hasModuleResolution || !isNode10ModuleResolutionText(raw.moduleResolution) {
		errs = append(errs, `"moduleResolution" must be "Node"`)
	}
	if options.AllowSyntheticDefaultImports != core.TSTrue {
		errs = append(errs, `"allowSyntheticDefaultImports" must be true`)
	}

	// L65-68: typeRoots must contain <projectPath>/node_modules/@rbxts.
	// tsoptions resolves typeRoots entries to absolute slash paths during
	// config parse (mirroring upstream, where the path-typed option is already
	// normalized in parsedCommandLine.options), so validateTypeRoots'
	// path.resolve comparison reduces to cleaned-path equality. The message
	// prints the native (upstream path.join) form.
	rbxtsModules := filepath.Join(filepath.FromSlash(projectPath), "node_modules", "@rbxts")
	if options.TypeRoots == nil || !typeRootsContain(options.TypeRoots, projectPath, rbxtsModules) {
		errs = append(errs, `"typeRoots" must contain `+rbxtsModules)
	}

	// L70-86: every raw "types" entry must exist under some typeRoot (parsed
	// typeRoots, or upstream's literal fallback when undefined), as-is or with
	// the .d.ts extension. Raw entries (sanitizer injects "*" when absent);
	// upstream runs this even when the typeRoots check above already failed.
	typeRoots := options.TypeRoots
	if typeRoots == nil {
		typeRoots = []string{"node_modules/@rbxts"}
	}
	for _, typesLocation := range raw.types {
		found := false
		for _, typeRoot := range typeRoots {
			typesPath := resolveAgainst(resolveAgainst(filepath.FromSlash(projectPath), filepath.FromSlash(typeRoot)), filepath.FromSlash(typesLocation))
			if pathExists(typesPath) || pathExists(typesPath+".d.ts") {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, `"types" `+typesLocation+" were not found. Make sure the path is relative to `typeRoots`")
		}
	}

	// configurable compiler options (L89-95). RootDirs nil/non-nil mirrors
	// upstream's undefined/defined: an explicit empty array passes here (and
	// getRootDirs returns it, as upstream's assert-then-return does).
	if options.RootDir == "" && options.RootDirs == nil {
		errs = append(errs, `"rootDir" or "rootDirs" must be defined`)
	}
	if options.OutDir == "" {
		errs = append(errs, `"outDir" must be defined`)
	}

	// L97-104: raw value (tsgo rejects the removed option outright; the
	// sanitizer strips it so this byte-exact upstream error wins). Upstream
	// suggests "true" only for the parsed Preserve value; enum values are
	// matched case-insensitively.
	if raw.hasImportsNotUsed {
		suggestedValue := "false"
		if strings.EqualFold(raw.importsNotUsed, "preserve") {
			suggestedValue = "true"
		}
		errs = append(errs, `"importsNotUsedAsValues" is no longer supported, use "verbatimModuleSyntax": `+suggestedValue+` instead`)
	}

	if len(errs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Invalid \"tsconfig.json\" configuration!\n")
	sb.WriteString("https://roblox-ts.com/docs/quick-start#project-folder-setup\n")
	for _, e := range errs {
		sb.WriteString("- " + e + "\n")
	}
	return sb.String()
}

// isNode10ModuleResolutionText reports whether a raw tsconfig moduleResolution
// value parses to TS5's ModuleResolutionKind.Node10 — the value upstream's
// ENFORCED_OPTIONS requires.
func isNode10ModuleResolutionText(value string) bool {
	switch strings.ToLower(value) {
	case "node", "node10":
		return true
	}
	return false
}

// typeRootsContain ports validateTypeRoots (validateCompilerOptions.ts
// L23-31): path.resolve(typeRoot) === path.resolve(nodeModulesPath) for some
// typeRoot. Entries are resolved against projectPath (parsed typeRoots are
// already absolute; resolveAgainst then just cleans them) and compared as
// cleaned slash paths — exact equality, as upstream.
func typeRootsContain(typeRoots []string, projectPath, rbxtsModules string) bool {
	want := filepath.ToSlash(filepath.Clean(rbxtsModules))
	for _, typeRoot := range typeRoots {
		resolved := resolveAgainst(filepath.FromSlash(projectPath), filepath.FromSlash(typeRoot))
		if filepath.ToSlash(filepath.Clean(resolved)) == want {
			return true
		}
	}
	return false
}

// pathExists mirrors fs.existsSync.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// getRootDirs ports Shared/util/getRootDirs.ts: rootDir if set, else rootDirs
// (the assert is upstream's; validateCompilerOptions has already rejected
// configs with neither, so the panic is an unreachable internal invariant).
func getRootDirs(program *compiler.Program) []string {
	options := program.Options()
	if options.RootDir != "" {
		return []string{options.RootDir}
	}
	if options.RootDirs != nil {
		// Non-nil mirrors upstream's `!== undefined` assert: an explicit empty
		// array passed validation and returns empty, as upstream.
		return options.RootDirs
	}
	panic("compile: getRootDirs: neither rootDir nor rootDirs is set") // upstream assert
}

// findAncestorDir ports Shared/util/findAncestorDir.ts: the deepest directory
// containing every input directory.
func findAncestorDir(dirs []string) string {
	sep := string(filepath.Separator)
	normalized := make([]string, len(dirs))
	for i, dir := range dirs {
		dir = filepath.Clean(filepath.FromSlash(dir))
		if !strings.HasSuffix(dir, sep) {
			dir += sep
		}
		normalized[i] = dir
	}
	currentDir := normalized[0]
	for !allHavePrefix(normalized, currentDir) {
		currentDir = filepath.Join(currentDir, "..") + sep
	}
	return filepath.Clean(currentDir)
}

func allHavePrefix(dirs []string, prefix string) bool {
	for _, dir := range dirs {
		if !strings.HasPrefix(dir, prefix) {
			return false
		}
	}
	return true
}

// findPackageJSON walks up from dir looking for package.json (the
// ts.findPackageJson call in createProjectData.ts L16). Returns "" when no
// ancestor has one.
func findPackageJSON(dir string) string {
	current := filepath.Clean(filepath.FromSlash(dir))
	for {
		candidate := filepath.Join(current, "package.json")
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// checkFileName ports Project/functions/checkFileName.ts; returns the
// incorrectFileName diagnostic message or "".
func checkFileName(filePath string) string {
	baseName := filepath.Base(filePath)
	if nameWarning, ok := filenameWarnings[baseName]; ok {
		return transformer.DiagIncorrectFileName(baseName, nameWarning, filePath).Message
	}
	return ""
}

// checkRojoConfig ports Project/functions/checkRojoConfig.ts: a Rojo $path
// partition pointing INSIDE a TypeScript root dir means the user mapped src
// instead of out.
func checkRojoConfig(rojoConfigPath string, resolver *rojo.RojoResolver, rootDirs []string, pathTranslator *rojo.PathTranslator) []string {
	if rojoConfigPath == "" {
		return nil
	}
	var messages []string
	rojoConfigDir := filepath.Dir(rojoConfigPath)
	for _, partition := range resolver.GetPartitions() {
		for _, rootDir := range rootDirs {
			if isPathDescendantOf(partition.FsPath, filepath.FromSlash(rootDir)) {
				outPath := pathTranslator.GetOutputPath(partition.FsPath)
				inputPath := relOrSelf(rojoConfigDir, partition.FsPath)
				suggestedPath := relOrSelf(rojoConfigDir, outPath)
				messages = append(messages, transformer.DiagRojoPathInSrc(inputPath, suggestedPath).Message)
			}
		}
	}
	return messages
}

// isPathDescendantOf mirrors Shared/util/isPathDescendantOf.ts (same quirk as
// the rojo package's private copy).
func isPathDescendantOf(filePath, dirPath string) bool {
	if dirPath == filePath {
		return true
	}
	rel, err := filepath.Rel(dirPath, filePath)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

func relOrSelf(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}
