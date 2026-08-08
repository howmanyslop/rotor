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
	"rotor/tsgo/vfs"
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

	// sourceTraces maps each file the transformer sidecar reprinted back to the
	// text on disk. Every position this pass produces is an index into the
	// reprint, so a diagnostic is only reportable after it has been through
	// these. Empty for a project with no transformer plugins.
	sourceTraces diagnosticTraces
}

// newProjectProgram builds the tsgo Program for projectDir over the sanitized
// config — the shared front half of CompileFile and CompileProject.
// tsConfigPath selects a custom config file ("" = projectDir/tsconfig.json;
// upstream `--project` may name any config file, CLI/commands/build.ts
// L31-40).
func newProjectProgram(projectDir, tsConfigPath string) (string, *compiler.Program, []string, error) {
	return newProjectProgramWithOptions(projectDir, tsConfigPath, ProjectOptions{})
}

func newProjectProgramWithOptions(projectDir, tsConfigPath string, opts ProjectOptions) (string, *compiler.Program, []string, error) {
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
	// Source overrides (ProjectOptions.Overlays) go through the same overlay FS
	// the sidecar builds its transformed-source program on. The unwrapped path
	// stays the default so a build without overlays keeps exactly the previous
	// filesystem stack.
	var fs vfs.FS = cachedvfs.From(SanitizeFSWithConfigPath(bundled.WrapFS(osvfs.FS()), configPath))
	if len(opts.Overlays) > 0 {
		fs = newOverlayFS(osvfs.FS(), configPath, normalizeOverlays(opts.Overlays))
	}
	program, diags, err := newProjectProgramFromFSWithOptions(dir, configPath, fs, opts.Checkers)
	if err != nil {
		return "", nil, diags, err
	}
	if len(opts.Overlays) > 0 {
		matched, err := matchProgramOverlays(program, opts)
		if err != nil {
			return "", nil, []string{err.Error()}, err
		}
		if opts.census != nil {
			opts.census.overlayMatches = matched
		}
	}
	return dir, program, nil, nil
}

// matchProgramOverlays checks the overlays against the program they were
// applied to, and reports how many of them landed on a file it holds.
//
// The rule — every overlay must match something — is scoped to one program when
// there is only one, and to the whole solution when opts came from
// CompileSolutionDiagnostics. See matchSolutionOverlaysToProgram for why it
// cannot be applied per project under --build.
func matchProgramOverlays(program *compiler.Program, opts ProjectOptions) (int, error) {
	if opts.solutionOverlays != nil {
		return matchSolutionOverlaysToProgram(program, opts.Overlays, opts.solutionOverlays)
	}
	return matchOverlaysToProgram(program, opts.Overlays)
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

	rojoConfigPath, rojoWarnings, err := resolveRojoConfigPath(dir, opts.RojoConfigPath)
	if err != nil {
		return nil, nil, err
	}
	for _, warning := range rojoWarnings {
		logservice.Warn(warning)
	}

	// compileFiles.ts L61-63.
	var rojoResolver *rojo.RojoResolver
	if rojoConfigPath != "" {
		if opts.rojoCache != nil {
			rojoResolver = opts.rojoCache.Load(rojoConfigPath)
		} else {
			rojoResolver = rojo.FromPath(rojoConfigPath)
		}
	} else {
		rojoResolver = rojo.Synthetic(filepath.FromSlash(outDir))
	}

	// compileFiles.ts L65-67: resolver parse warnings → LogService.warn.
	for _, warning := range rojoResolver.GetWarnings() {
		logservice.Warn(warning)
	}

	pathTranslator := createPathTranslator(program, !opts.LuaExtension)
	importPathMap := opts.crossProjectImportPathMap
	if importPathMap == nil {
		importPathMap = createCrossProjectImportPathMap(program, !opts.LuaExtension)
	}

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
		assets:      newAssetResolver(envDir, opts.census != nil),
		files:       newFileResolver(envDir),
		stamps:      resolveStampProvider(envDir),
		rojoContext: &transformer.RojoContext{
			Resolver:          rojoResolver,
			PathTranslator:    pathTranslator,
			RuntimeLibRbxPath: runtimeLibRbxPath,
			ProjectPath:       filepath.FromSlash(dir),

			PkgRojoResolvers:          pkgRojoResolvers,
			NodeModulesPathMapping:    createNodeModulesPathMapping(typeRoots, useCaseSensitiveFileNames, options.CustomConditions),
			NodeModulesPath:           nodeModulesPath,
			TypeRoots:                 typeRoots,
			UseCaseSensitiveFileNames: useCaseSensitiveFileNames,
			ImportPathMap:             importPathMap,
		},
	}, nil, nil
}

func resolveRojoConfigPath(dir, configuredPath string) (string, []string, error) {
	if configuredPath == "" {
		path, warnings := rojo.FindRojoConfigFilePath(filepath.FromSlash(dir))
		return path, warnings, nil
	}
	abs, err := filepath.Abs(filepath.FromSlash(configuredPath))
	if err != nil {
		return "", nil, err
	}
	return abs, nil, nil
}

func createCrossProjectImportPathMap(program *compiler.Program, useLuauExtension bool) map[string]string {
	useCaseSensitiveFileNames := osvfs.FS().UseCaseSensitiveFileNames()
	result := make(map[string]string)
	for _, reference := range program.GetResolvedProjectReferences() {
		if reference == nil {
			continue
		}
		options := reference.CompilerOptions()
		if options == nil || options.OutDir == "" {
			continue
		}
		rootDirs := options.RootDirs
		if options.RootDir != "" {
			rootDirs = []string{options.RootDir}
		}
		if len(rootDirs) == 0 {
			continue
		}
		translator := rojo.NewPathTranslator(findAncestorDir(rootDirs), filepath.FromSlash(options.OutDir), "", options.Declaration.IsTrue(), useLuauExtension)
		for _, fileName := range reference.FileNames() {
			if !strings.HasSuffix(fileName, ".ts") && !strings.HasSuffix(fileName, ".tsx") {
				continue
			}
			importPath := translator.GetImportPath(fileName, false)
			canonical := rojo.CanonicalFileName(fileName, useCaseSensitiveFileNames)
			if _, exists := result[canonical]; !exists {
				result[canonical] = importPath
			}
			if options.Declaration.IsTrue() && !strings.HasSuffix(fileName, ".d.ts") {
				declarationPath := translator.GetOutputDeclarationPath(fileName)
				declarationCanonical := rojo.CanonicalFileName(declarationPath, useCaseSensitiveFileNames)
				if _, exists := result[declarationCanonical]; !exists {
					result[declarationCanonical] = importPath
				}
			}
		}
	}
	return result
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
// offline suppresses the cloud client so a compile can never upload. A census
// run must not: the lockfile persist lives only on the Build path
// (output.go), so an upload here would be forgotten and repeated every run.
func newAssetResolver(projectDir string, offline bool) *assetresolve.Resolver {
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

	return assetresolve.New(assetresolve.Options{
		ProjectDir: projectDir,
		Base:       base,
		Lockfile:   lock,
		Client:     assetCloudClient(creator, offline),
		Creator:    creator,
	})
}

// assetCloudClient builds the Open Cloud client for $asset cache misses. Only
// build one when a creator is configured (uploads need an owner anyway);
// cloud.FromEnv returns nil without ROBLOX_API_KEY. offline returns nil
// unconditionally.
func assetCloudClient(creator cloud.Creator, offline bool) assets.Cloud {
	if offline || (creator.UserID == 0 && creator.GroupID == 0) {
		return nil
	}
	if c, err := cloud.FromEnv(); err == nil {
		return c
	}
	return nil
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
	rojoCache *rojo.RojoResolverCache
	Timings   *BuildTimings

	crossProjectImportPathMap map[string]string
	pendingSolutionPersists   *[]func() error
	deferRojoCachePersist     bool

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

	// Checkers overrides compilerOptions.checkers when set by the CLI. A nil
	// value preserves the parsed config and upstream default behavior.
	Checkers *int

	// Builders overrides the project builder count when set by the CLI. A nil
	// value preserves the upstream default behavior.
	Builders *int

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

	EmitDeclarationOnly bool

	// census turns the four sequential compile gates (program-option
	// diagnostics, the per-file precheck, the global checker diagnostics, the
	// transform drain) into record-and-continue classifications, so every file
	// is reported instead of the compile stopping at the first failure, and
	// receives the classifications.
	//
	// Deliberately unexported and with no exported setter:
	// CompileProjectDiagnostics is the only entry point that may set it. Census
	// mode transforms type-broken files, so its verdicts must never be able to
	// reach a Build — an external caller flipping this on a build would get
	// silently wrong Luau written to disk with zero diagnostics. Nil is stock
	// `rotor build` / `rotor check` behavior, unchanged.
	census *censusCollector

	// Overlays replaces the on-disk text of individual source files for the
	// lifetime of one compile, keyed by absolute path (any separator style —
	// keys are normalized the same way the sidecar's transformed-file overlay
	// is). Nothing is written back to disk. Empty (the default) leaves the
	// filesystem wrapping byte-for-byte as it was before overlays existed.
	//
	// Two limits, both enforced rather than silently tolerated:
	//   - Overlays REPLACE files; they cannot ADD one. newOverlayFS overrides
	//     FileExists and ReadFile, not directory enumeration, so a path the
	//     tsconfig include never walks to is never asked for.
	//   - A key naming no file in the resulting program is an error. See
	//     matchOverlaysToProgram: a silently ignored overlay yields a clean
	//     report on the UNMODIFIED tree, which a caller cannot detect.
	//
	// Projects with transformer plugins are not a limit. changedFilesFor ships
	// every overlay to the Node worker, whose overrides map backs the
	// LanguageService the plugins and the declaration emit both run against.
	Overlays map[string]string

	// solutionOverlays is set only by CompileSolutionDiagnostics, and only on
	// the per-project options it derives. Its presence is what tells
	// matchProgramOverlays that this program holds one project's share of a
	// solution's files rather than all of them, so the "every overlay must
	// match" rule is answered against the union of every project instead of
	// this one. Nil — every other caller — is the single-project rule,
	// unchanged.
	solutionOverlays *solutionOverlayMatches

	forceFullBuild bool
}

func ProjectOptionsForReferencedConfig(entry ProjectOptions, tsConfigPath string, inheritEntryTypeAndRojo bool) (ProjectOptions, error) {
	declared, err := ReadRbxtsOptions(tsConfigPath)
	if err != nil {
		return ProjectOptions{}, err
	}
	if declared == nil {
		if !inheritEntryTypeAndRojo {
			entry.Type = ""
			entry.RojoConfigPath = ""
		}
		return entry, nil
	}

	entry.Type = ""
	entry.RojoConfigPath = ""
	if declared.IncludePath != nil {
		entry.IncludePath = *declared.IncludePath
	}
	if declared.Rojo != nil {
		entry.RojoConfigPath = *declared.Rojo
	}
	if declared.Type != nil {
		entry.Type = transformer.ProjectType(*declared.Type)
	}
	if declared.LogTruthyChanges != nil {
		entry.LogTruthyChanges = *declared.LogTruthyChanges
	}
	if declared.AllowCommentDirectives != nil {
		entry.AllowCommentDirectives = *declared.AllowCommentDirectives
	}
	if declared.NoInclude != nil {
		entry.EmitIncludeFiles = !*declared.NoInclude
	}
	if declared.OptimizedLoops != nil {
		entry.NoOptimizedLoops = !*declared.OptimizedLoops
	}
	if declared.Luau != nil {
		entry.LuaExtension = !*declared.Luau
	}
	return entry, nil
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
	dir, program, diags, err := newProjectProgramWithOptions(projectDir, opts.TsConfigPath, opts)
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
	program, sourceFiles, traces, diags, err := prepareProjectProgramForCompile(dir, program, sourceFiles, opts.Overlays)
	if err != nil {
		return nil, stringDiagnostics(diags), err
	}
	pctx, pctxDiags, err := newProjectContext(dir, program, opts)
	if err != nil {
		return nil, stringDiagnostics(pctxDiags), err
	}
	pctx.sourceTraces = traces
	outputs, _, infos, err := compileProjectSourceFiles(dir, program, pctx, sourceFiles, opts)
	return outputs, infos, err
}

func projectSourceFiles(program *compiler.Program) []*ast.SourceFile {
	var sourceFiles []*ast.SourceFile
	for _, sourceFile := range program.SourceFiles() {
		fileName := sourceFile.FileName()
		if sourceFile.IsDeclarationFile ||
			program.IsSourceFromProjectReference(sourceFile.Path()) ||
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
	relOut    string
	text      string
	sourceMap string
	diags     []DiagnosticInfo
	err       error
	// transformed records that the transformer ran to completion and returned
	// a verdict on this file — diagnostics included. Only a panic, or a
	// failure before the transform started, leaves it false. Census mode
	// reports the total so a silently shrinking census is visible.
	transformed bool
}

type precheckedProjectSourceFile struct {
	tsDiags      []*ast.Diagnostic
	commentDiags []string
}

func compileProjectSourceFiles(dir string, program *compiler.Program, pctx *projectContext, sourceFiles []*ast.SourceFile, opts ProjectOptions) (map[string]string, map[string]string, []DiagnosticInfo, error) {
	ctx := context.Background()

	// A non-nil collector is what turns census mode on; nil is stock.
	census := opts.census
	if census != nil {
		census.traces = pctx.sourceTraces
	}

	// Gate 1 of 4. Program-level option diagnostics fail the compile before any
	// file is transformed, mirroring CompileFile. Census mode records them as
	// project-level diagnostics and carries on.
	if tsDiags := program.GetProgramDiagnostics(); len(tsDiags) > 0 {
		if census == nil {
			return nil, nil, tsDiagnosticInfos(tsDiags, pctx.sourceTraces), errors.New("compile: TypeScript diagnostics")
		}
		census.addProjectDiagnostics(tsDiagnosticInfos(tsDiags, pctx.sourceTraces))
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
			// Deliberately unguarded: a recover() here would be worse than the
			// crash it catches. GetSemanticDiagnostics takes the checker mutex
			// and releases it without defer (tsgo/compiler/program.go,
			// collectCheckerDiagnostics), so a recovered panic leaves the lock
			// held and this goroutine deadlocks on the next file in its checker
			// group — a loud crash traded for a silent hang. Gate 3's
			// GetGlobalDiagnostics is unguarded for the same reason. Fixing the
			// lock belongs in tools/mirror/overlay, not here.
			for i, sourceFile := range group.files {
				prechecks[group.indices[i]] = precheckProjectSourceFile(ctx, program, sourceFile, opts)
			}
		})
	}
	wg.RunAndWait()

	// Gate 2 of 4, the one that matters most: this returns at the FIRST file
	// with type errors, before the transform work group below is ever queued —
	// so a single type error suppresses the whole transform stage. Census mode
	// keeps the diagnostics on the file and lets execution reach the
	// transformer.
	if census == nil {
		for _, precheck := range prechecks {
			if len(precheck.tsDiags) > 0 {
				return nil, nil, tsDiagnosticInfos(precheck.tsDiags, pctx.sourceTraces), errors.New("compile: TypeScript diagnostics")
			}
			if len(precheck.commentDiags) > 0 {
				return nil, nil, stringDiagnostics(precheck.commentDiags), errors.New("compile: comment directive diagnostics")
			}
		}
	}

	// Gate 3 of 4. Unguarded on purpose, like the precheck above: this takes
	// and releases the checker mutex without defer.
	if tsDiags := program.GetGlobalDiagnostics(ctx); len(tsDiags) > 0 {
		if census == nil {
			return nil, nil, tsDiagnosticInfos(tsDiags, pctx.sourceTraces), errors.New("compile: TypeScript diagnostics")
		}
		census.addProjectDiagnostics(tsDiagnosticInfos(tsDiags, pctx.sourceTraces))
	}

	wg = core.NewWorkGroup(program.SingleThreaded() || len(groups) <= 1)
	for _, group := range groups {
		group := group
		wg.Queue(func() {
			multi := transformer.NewMultiState()
			for i, sourceFile := range group.files {
				index := group.indices[i]
				results[index] = compileProjectSourceFile(ctx, dir, program, pctx, sourceFile, opts, multi, progressLabels[index])
			}
		})
	}
	wg.RunAndWait()

	// Gate 4 of 4: the transform drain. Census mode accumulates every file's
	// outcome instead of returning at the first error.
	outputs := make(map[string]string, len(results))
	sourceMaps := make(map[string]string, len(results))
	for i, result := range results {
		if census != nil {
			census.record(sourceFiles[i], prechecks[i], result)
			// Only a file that actually transformed cleanly has output worth
			// keeping. A file that never reached the transformer leaves a
			// zero-value result, whose relOut is ""; a type-broken one has
			// Luau the transformer derived from broken types, which it uses
			// for truthiness, coercion and loop lowering. Census callers
			// discard this map anyway — this is here so that a future caller
			// that does not cannot write either to disk.
			if !result.transformed || result.err != nil || len(prechecks[i].tsDiags) > 0 {
				continue
			}
		} else if result.err != nil {
			return nil, nil, result.diags, result.err
		}
		outputs[result.relOut] = result.text
		if result.sourceMap != "" {
			sourceMaps[result.relOut+".map"] = result.sourceMap
		}
	}

	return outputs, sourceMaps, nil, nil
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

		var text string
		var sourceMap string
		var diags []DiagnosticInfo
		var err error
		if program.Options().SourceMap.IsTrue() {
			text, sourceMap, diags, err = transformAndRenderSourceMapDetailed(state, sourceFile)
		} else {
			text, diags, err = transformAndRenderDetailed(state)
		}
		if err != nil {
			result.err = err
			return
		}
		result.transformed = true
		if len(diags) > 0 {
			// Node-located diagnostics index the reprinted text the same way
			// TypeScript's do, so they need the same trip back through the trace.
			result.diags = pctx.sourceTraces.remapAll(diags, sourceFile.Text())
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
		result.sourceMap = sourceMap
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
	hasTypes            bool
	importsNotUsed      string // raw text; "" when absent or non-string
	hasImportsNotUsed   bool
}

// readRawEnforcedOptions merges raw values from the extends chain before
// tsoptions sees the sanitizer's TS7 compatibility rewrites. Unreadable or
// unparsable configs return zero values so tsoptions reports the parse error.
func readRawEnforcedOptions(configPath string) rawEnforcedOptions {
	return readRawEnforcedOptionsFromChain(configPath, make(map[string]struct{}))
}

func readRawEnforcedOptionsFromChain(configPath string, visited map[string]struct{}) rawEnforcedOptions {
	normalized, err := filepath.Abs(configPath)
	if err != nil {
		return rawEnforcedOptions{}
	}
	normalized = filepath.Clean(normalized)
	if _, ok := visited[normalized]; ok {
		return rawEnforcedOptions{}
	}
	visited[normalized] = struct{}{}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return rawEnforcedOptions{}
	}
	var root map[string]any
	if json.Unmarshal([]byte(stripJSONC(string(data))), &root) != nil {
		return rawEnforcedOptions{}
	}

	base := rawEnforcedOptions{}
	if extends, ok := root["extends"].(string); ok {
		parent, err := resolveExtendsPath(filepath.Dir(normalized), extends)
		if err == nil {
			base = readRawEnforcedOptionsFromChain(parent, visited)
		}
	}

	current := rawEnforcedOptions{}
	co, ok := root["compilerOptions"].(map[string]any)
	if !ok {
		return base
	}
	if v, ok := co["moduleResolution"]; ok {
		current.hasModuleResolution = true
		current.moduleResolution, _ = v.(string)
	}
	if value, present := co["types"]; present {
		current.hasTypes = true
		list, _ := value.([]any)
		for _, e := range list {
			if s, ok := e.(string); ok {
				current.types = append(current.types, s)
			}
		}
	}
	if v, ok := co["importsNotUsedAsValues"]; ok {
		current.hasImportsNotUsed = true
		current.importsNotUsed, _ = v.(string)
	}
	return mergeRawEnforcedOptions(base, current)
}

func mergeRawEnforcedOptions(base, overlay rawEnforcedOptions) rawEnforcedOptions {
	if overlay.hasModuleResolution {
		base.moduleResolution = overlay.moduleResolution
		base.hasModuleResolution = true
	}
	if overlay.hasTypes {
		base.types = overlay.types
		base.hasTypes = true
	}
	if overlay.hasImportsNotUsed {
		base.importsNotUsed = overlay.importsNotUsed
		base.hasImportsNotUsed = true
	}
	return base
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
	isCommonJS := options.Module == core.ModuleKindCommonJS
	isPreserve := options.Module == core.ModuleKindPreserve
	if !isCommonJS && !isPreserve {
		errs = append(errs, `"module" must be commonjs`)
	}
	if options.ModuleDetection != core.ModuleDetectionKindForce {
		errs = append(errs, `"moduleDetection" must be "force"`)
	}
	// L57-59: raw value (sanitizer rewrites it; see rawEnforcedOptions).
	// "node" and "node10" are the two spellings TS5 parses to Node10 —
	// tsconfig enum values are matched case-insensitively (the same set
	// SanitizeTSConfig rewrites).
	moduleResolutionValid := raw.hasModuleResolution &&
		!isPreserve && isNode10ModuleResolutionText(raw.moduleResolution)
	moduleResolutionValid = moduleResolutionValid ||
		(isPreserve && options.GetModuleResolutionKind() == core.ModuleResolutionKindBundler)
	if !moduleResolutionValid {
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
		if pathExists(resolved) {
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
