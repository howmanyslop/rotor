package compile

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"rotor/tsgo/vfs"
	"rotor/tsgo/vfs/wrapvfs"
)

// SanitizeFS wraps an FS so that any ReadFile of a file named exactly
// "tsconfig.json" returns sanitized config text (see SanitizeTSConfig), and
// any ReadFile of an @rbxts/compiler-types .d.ts returns text with the
// Iterable interfaces rewritten to tsgo's expected arity (see
// RewriteIterableArity).
func SanitizeFS(inner vfs.FS) vfs.FS {
	return SanitizeFSWithConfigPath(inner, "")
}

// SanitizeFSWithConfigPath is SanitizeFS that additionally sanitizes the
// exact (slash-separated, absolute) config file path — needed when
// `--project` points at a config file NOT named tsconfig.json (upstream
// findTsConfigPath accepts any file path, CLI/commands/build.ts L31-40) and
// every config file in its resolved extends chain. An empty configPath adds
// nothing.
func SanitizeFSWithConfigPath(inner vfs.FS, configPath string) vfs.FS {
	chainPaths := make(map[string]struct{})
	if configPath != "" {
		_, paths, _ := ReadRbxtsOptionsWithChain(configPath)
		for _, chainPath := range paths {
			chainPaths[filepath.ToSlash(chainPath)] = struct{}{}
		}
	}

	return wrapvfs.Wrap(inner, wrapvfs.Replacements{
		ReadFile: func(rawPath string) (string, bool) {
			contents, ok := inner.ReadFile(rawPath)
			if !ok {
				return contents, ok
			}
			// vfs paths are slash-separated; the caller may pass native
			// separators (Windows), so normalize before matching.
			path := filepath.ToSlash(rawPath)
			_, inExtendsChain := chainPaths[path]
			if isTSConfigPath(path) || (configPath != "" && path == filepath.ToSlash(configPath)) || inExtendsChain {
				return SanitizeTSConfig(contents), true
			}
			if isCompilerTypesDTSPath(path) {
				return RewriteIterableArity(contents), true
			}
			return contents, ok
		},
	})
}

// isTSConfigPath reports whether path's basename is exactly "tsconfig.json"
// (vfs paths are slash-separated). A bare suffix match would also intercept
// unrelated files like "my-tsconfig.json".
func isTSConfigPath(path string) bool {
	return path == "tsconfig.json" || strings.HasSuffix(path, "/tsconfig.json")
}

// isCompilerTypesDTSPath reports whether path is a declaration file of the
// @rbxts/compiler-types package. Matched by package directory rather than by
// the specific file (types/Iterable.d.ts today) because declarations may move
// across compiler-types versions.
func isCompilerTypesDTSPath(path string) bool {
	return strings.Contains(path, "/node_modules/@rbxts/compiler-types/") && strings.HasSuffix(path, ".d.ts")
}

// iterableArityPattern matches the four arity-1 iteration-protocol interface
// declarations of @rbxts/compiler-types (anchored to a single type parameter
// named exactly T — the shape across compiler-types 2.x/3.x). References like
// `Iterable<T>` in extends clauses or bodies do NOT match: only the
// `interface Name<T>` declaration form does.
var iterableArityPattern = regexp.MustCompile(`\binterface (Iterable|IterableIterator|AsyncIterable|AsyncIterableIterator)<T>`)

// RewriteIterableArity rewrites @rbxts/compiler-types' arity-1 iteration
// interfaces to the TS5.6+ lib shape `<T, TReturn = any, TNext = any>`.
//
// Why: tsgo (TS7) resolves the iteration globals (Iterable,
// IterableIterator, ...) at arity 3 (checker.go getGlobalTypeResolver(name,
// 3, ...)); compiler-types declares them at arity 1, so getGlobalType
// returns emptyGenericType and the checker's entire iteration-protocol
// branch is skipped — for-of/spread/destructuring over Set/Map/generators
// degrade to the array-like fallback and report "can only be iterated
// through when using the '--downlevelIteration' flag" (an option the
// sanitizer must strip — TS7 removed it). rbxtsc pins TS 5.5 where the
// checker resolved arity 1, which is why upstream never hits this.
//
// The defaults are `any` (maximally permissive), NOT the lib's strict
// BuiltinIteratorReturn shape: TReturn/TNext only feed assignability checks
// that TS5.5 never performed, and `any` keeps them vacuous so no NEW
// diagnostics appear on code rbxtsc accepts. Defaulted parameters keep
// existing 1-arg references legal, and getGlobalType counts ALL type
// parameters including defaulted ones, so the rewritten interfaces resolve
// at arity 3. Declaration merging is not an alternative (merged declarations
// must repeat identical type parameter lists), hence the in-place rewrite.
// The transform is textual, idempotent (the rewritten form no longer matches
// `<T>`), and has zero emit impact (.d.ts text only).
func RewriteIterableArity(src string) string {
	return iterableArityPattern.ReplaceAllString(src, "interface $1<T, TReturn = any, TNext = any>")
}

// SanitizeTSConfig rewrites a tsconfig.json that rbxtsc requires into one
// tsgo (TS7) accepts. tsgo's Program.verifyCompilerOptions (tsgo/compiler/
// program.go, "Removed in TS7" section) hard-errors on three options that
// rbxtsc's project validation REQUIRES users to set:
//
//   - "downlevelIteration" (any value)  -> removed
//   - "baseUrl" (any value)             -> removed
//   - "moduleResolution": "Node"/node10 -> replaced with "bundler"
//
// plus one option tsgo no longer even declares (parse would fail with
// "Unknown compiler option"):
//
//   - "importsNotUsedAsValues" (any value) -> removed (validation still
//     rejects it with upstream's text, from the raw config)
//
// One TS5->TS7 semantic repair on top of the removals: TS7 no longer
// auto-includes every package under typeRoots when "types" is unspecified
// (module.GetAutomaticTypeDirectiveNames returns [] unless the types array
// holds the "*" wildcard — tsgo/module/resolver.go:2075-2081). rbxtsc
// projects depend on auto-inclusion to load @rbxts/types globals (print,
// Array, ...), so when "types" is absent the sanitizer injects
// "types": ["*"], which reproduces TS5's typeRoots walk exactly. An explicit
// "types" array is left untouched (TS5 also disabled auto-inclusion then).
//
// moduleResolution choice, determined empirically against the fixture project
// (testdata/diff/project, module=commonjs, typeRoots=node_modules/@rbxts):
//   - "node16" FAILS: tsgo emits "Option 'module' must be set to 'Node16'
//     when option 'moduleResolution' is set to 'Node16'" (program.go:1140-44)
//     because the fixture's module is commonjs.
//   - removing the key entirely WORKS: CompilerOptions.GetModuleResolutionKind
//     defaults Unknown to bundler when module is commonjs
//     (tsgo/core/compileroptions.go:221-231) — zero diagnostics.
//   - "bundler" WORKS: explicitly exempted for module=commonjs
//     (program.go:1126) — zero diagnostics, and bundler resolution still finds
//     node_modules packages and @rbxts typeRoots.
//
// "bundler" is chosen over key-removal because it pins the same resolution
// kind the default would pick today, guarding against future default changes.
//
// Input may be JSONC (// and /* */ comments, trailing commas — both legal in
// tsconfig.json); output is plain JSON. Key order is not preserved, which is
// fine: tsconfig key order carries no meaning. Malformed input is returned
// untouched so tsoptions reports the parse error itself.
func SanitizeTSConfig(src string) string {
	clean := stripJSONC(src)
	var root map[string]any
	if err := json.Unmarshal([]byte(clean), &root); err != nil {
		return src
	}
	co, ok := root["compilerOptions"].(map[string]any)
	if !ok {
		return clean
	}
	delete(co, "downlevelIteration")
	// importsNotUsedAsValues: tsgo doesn't declare the option at all, so a
	// config carrying it would fail tsoptions parse with "Unknown compiler
	// option" before validateCompilerOptions could emit upstream's byte-exact
	// "no longer supported, use verbatimModuleSyntax" error (the raw value is
	// read pre-sanitization — see readRawEnforcedOptions). Any value is an
	// error there, so the stripped option never influences a successful
	// compile.
	delete(co, "importsNotUsedAsValues")
	if baseURL, ok := co["baseUrl"].(string); ok {
		injectBaseURLPaths(co, baseURL)
	}
	delete(co, "baseUrl")
	if mr, ok := co["moduleResolution"].(string); ok {
		// Case-insensitive value match: tsconfig enum values are
		// case-insensitive ("Node" parses as node10).
		switch strings.ToLower(mr) {
		case "node", "node10":
			co["moduleResolution"] = "bundler"
		}
	}
	if _, hasTypes := co["types"]; !hasTypes {
		co["types"] = []any{"*"}
	}
	out, err := json.MarshalIndent(root, "", "\t")
	if err != nil {
		return clean
	}
	return string(out)
}

// injectBaseURLPaths rewrites a stripped `"baseUrl": B` into the equivalent
// "paths" wildcard — the exact replacement tsgo's own removed-option
// diagnostic suggests (tsgo/compiler/program.go:791-803): for
// `baseUrl: "src"`, `"paths": { "*": ["./src/*"] }` in the same config file.
//
// Equivalence (tsgo resolver mechanics, empirically validated against the
// fixture project): paths substitutions are resolved against PathsBasePath =
// the directory of the config file that DECLARES paths
// (tsoptions/tsconfigparsing.go) — the same anchor TS5 resolved baseUrl
// against, so no path math is needed; a matched-but-failed "*" pattern
// returns continueSearching() and resolution proceeds to
// loadModuleFromNearestNodeModulesDirectory, so `import "@rbxts/foo"` still
// resolves through node_modules exactly like TS5's baseUrl-miss fallback.
//
// When the config already has "paths" (rare in rbxts projects), TS5's
// paths-relative-to-baseUrl + baseUrl-fallback semantics are reproduced:
//   - every relative substitution s is rewritten to ./B/s (TS5 resolved
//     substitutions against baseUrl; tsgo resolves against the config dir);
//   - ./B/<pattern> (pattern text, * kept) is appended to each pattern's
//     substitution list — restoring TS5's "specific pattern failed ->
//     baseUrl lookup of the FULL name" fallback, because tsgo's
//     MatchPatternOrExact picks only the single longest-prefix pattern and
//     never retries "*";
//   - the "*" wildcard is added if absent, else ./B/* is appended to it.
//
// Order within a substitution list is significant (tried in order): user
// entries stay first, injected baseUrl fallbacks go last, matching TS5's
// paths-before-baseUrl priority.
func injectBaseURLPaths(co map[string]any, baseURL string) {
	b := strings.TrimSuffix(strings.TrimPrefix(strings.ReplaceAll(baseURL, "\\", "/"), "./"), "/")
	prefix := "./"
	switch {
	case isAbsolutePathText(b):
		// Absolute baseUrl (rare): substitutions stay absolute —
		// tspath.CombinePaths returns an absolute second argument unchanged.
		prefix = b + "/"
	case b != "" && b != ".":
		prefix = "./" + b + "/"
	}
	wild := prefix + "*"

	if _, present := co["paths"]; !present {
		co["paths"] = map[string]any{"*": []any{wild}}
		return
	}
	paths, ok := co["paths"].(map[string]any)
	if !ok {
		return // malformed "paths"; leave it for tsoptions to report
	}

	// rebase maps a baseUrl-relative path text onto the config dir, keeping
	// the "./" anchor tsgo's own suggestion uses (program.go:795-797).
	rebase := func(s string) string {
		if isAbsolutePathText(s) {
			return s
		}
		cleaned := path.Clean(prefix + strings.TrimPrefix(s, "./"))
		if !isAbsolutePathText(cleaned) && !strings.HasPrefix(cleaned, "./") && !strings.HasPrefix(cleaned, "../") {
			cleaned = "./" + cleaned
		}
		return cleaned
	}

	for pattern, subsAny := range paths {
		subs, ok := subsAny.([]any)
		if !ok {
			continue // malformed; leave for tsoptions to report
		}
		out := make([]any, 0, len(subs)+1)
		for _, subAny := range subs {
			sub, ok := subAny.(string)
			if !ok {
				out = append(out, subAny)
				continue
			}
			out = append(out, rebase(sub))
		}
		// The full-name baseUrl fallback; for the "*" pattern itself this is
		// exactly `wild`, which the loop below appends anyway.
		if pattern != "*" {
			out = append(out, rebase(pattern))
		}
		paths[pattern] = out
	}

	if starAny, ok := paths["*"]; ok {
		if star, ok := starAny.([]any); ok {
			paths["*"] = append(star, wild)
		}
	} else {
		paths["*"] = []any{wild}
	}
}

// isAbsolutePathText reports whether a tsconfig path text is absolute
// (POSIX "/...", Windows drive "C:...", or UNC "\\..."); absolute
// substitutions need no rebasing.
func isAbsolutePathText(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "\\") {
		return true
	}
	return len(s) >= 2 && s[1] == ':' &&
		(('a' <= s[0] && s[0] <= 'z') || ('A' <= s[0] && s[0] <= 'Z'))
}

// StripJSONC exposes the JSONC stripper to other packages: the CLI's raw
// `rbxts` tsconfig-key read (CLI/commands/build.ts L22-29) mirrors upstream's
// ts.parseConfigFileTextToJson, which accepts comments and trailing commas.
func StripJSONC(src string) string {
	return stripJSONC(src)
}

// stripJSONC removes // line comments, /* */ block comments, and trailing
// commas (all outside string literals) so encoding/json can parse tsconfig
// text.
func stripJSONC(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	inString := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				b.WriteByte(src[i])
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i++ // land on '/'; loop increment steps past it
		case c == ',':
			// Trailing comma: drop if the next non-whitespace,
			// non-comment byte closes an object/array. Cheap scan that
			// only skips whitespace — a comma separated from its
			// closer by a comment is rare enough to leave to the JSON
			// parser.
			j := i + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r' || src[j] == '\n') {
				j++
			}
			if j < len(src) && (src[j] == '}' || src[j] == ']') {
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
