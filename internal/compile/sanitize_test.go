package compile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rotor/tsgo/bundled"
	"rotor/tsgo/compiler"
	"rotor/tsgo/tsoptions"
	"rotor/tsgo/vfs/osvfs"
)

func TestSanitizeTSConfigStripsRejectedOptions(t *testing.T) {
	src := `{
	// rbxtsc requires these three options; tsgo (TS7) rejects them.
	"compilerOptions": {
		"downlevelIteration": true, /* removed in TS7 */
		"baseUrl": ".",
		"moduleResolution": "Node",
		"module": "commonjs",
		"strict": true,
	},
	"include": ["src"]
}`
	got := SanitizeTSConfig(src)

	var root map[string]any
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatalf("sanitized output is not valid JSON: %v\n%s", err, got)
	}
	co, ok := root["compilerOptions"].(map[string]any)
	if !ok {
		t.Fatalf("compilerOptions missing from sanitized output:\n%s", got)
	}
	if _, present := co["downlevelIteration"]; present {
		t.Error("downlevelIteration not stripped")
	}
	if _, present := co["baseUrl"]; present {
		t.Error("baseUrl not stripped")
	}
	if mr := co["moduleResolution"]; mr != "bundler" {
		t.Errorf("moduleResolution = %v, want %q", mr, "bundler")
	}
	if co["module"] != "commonjs" || co["strict"] != true {
		t.Errorf("unrelated options were altered: %v", co)
	}
	if inc, _ := root["include"].([]any); len(inc) != 1 || inc[0] != "src" {
		t.Errorf("include was altered: %v", root["include"])
	}
}

// TestSanitizeTSConfigBaseURLBecomesPathsWildcard: stripping `baseUrl: B`
// must inject the equivalent `"paths": {"*": ["./B/*"]}` (the exact rewrite
// tsgo's removed-option diagnostic suggests) so non-relative project-internal
// imports keep resolving.
func TestSanitizeTSConfigBaseURLBecomesPathsWildcard(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"src", "./src/*"},
		{"./src", "./src/*"},
		{"src/", "./src/*"},
		{".", "./*"},
		{"./", "./*"},
		{"src/sub", "./src/sub/*"},
	}
	for _, tc := range cases {
		src := `{"compilerOptions": {"baseUrl": "` + tc.baseURL + `"}}`
		got := SanitizeTSConfig(src)
		var root map[string]any
		if err := json.Unmarshal([]byte(got), &root); err != nil {
			t.Fatalf("baseUrl %q: not valid JSON: %v\n%s", tc.baseURL, err, got)
		}
		co := root["compilerOptions"].(map[string]any)
		if _, present := co["baseUrl"]; present {
			t.Errorf("baseUrl %q: not stripped", tc.baseURL)
		}
		paths, ok := co["paths"].(map[string]any)
		if !ok {
			t.Fatalf("baseUrl %q: no paths injected:\n%s", tc.baseURL, got)
		}
		star, _ := paths["*"].([]any)
		if len(star) != 1 || star[0] != tc.want {
			t.Errorf("baseUrl %q: paths[*] = %v, want [%q]", tc.baseURL, star, tc.want)
		}
	}
}

// TestSanitizeTSConfigBaseURLMergesExistingPaths: when the config already has
// "paths", the injection must reproduce TS5's paths-relative-to-baseUrl +
// baseUrl-fallback semantics: relative substitutions rebased onto ./B/, the
// full-name fallback ./B/<pattern> appended per pattern, absolute
// substitutions untouched, the "*" wildcard appended last.
func TestSanitizeTSConfigBaseURLMergesExistingPaths(t *testing.T) {
	src := `{"compilerOptions": {
		"baseUrl": "src",
		"paths": {
			"lib/*": ["vendor/*", "./fallback/*", "/abs/*"],
			"jquery": ["vendor/jquery"],
			"*": ["generated/*"]
		}
	}}`
	got := SanitizeTSConfig(src)
	var root map[string]any
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	paths := root["compilerOptions"].(map[string]any)["paths"].(map[string]any)

	wants := map[string][]any{
		// user entries rebased (in order), then the full-name fallback.
		"lib/*":  {"./src/vendor/*", "./src/fallback/*", "/abs/*", "./src/lib/*"},
		"jquery": {"./src/vendor/jquery", "./src/jquery"},
		// the existing wildcard gains the baseUrl wildcard LAST (TS5 tried
		// paths before baseUrl).
		"*": {"./src/generated/*", "./src/*"},
	}
	if len(paths) != len(wants) {
		t.Errorf("paths has %d patterns, want %d: %v", len(paths), len(wants), paths)
	}
	for pattern, want := range wants {
		gotSubs, _ := paths[pattern].([]any)
		if len(gotSubs) != len(want) {
			t.Errorf("paths[%q] = %v, want %v", pattern, gotSubs, want)
			continue
		}
		for i := range want {
			if gotSubs[i] != want[i] {
				t.Errorf("paths[%q][%d] = %v, want %v", pattern, i, gotSubs[i], want[i])
			}
		}
	}
}

// TestSanitizeTSConfigBaseURLEdgeCases: an absolute baseUrl keeps absolute
// substitutions (CombinePaths returns absolute seconds unchanged); a
// malformed "paths" value is left for tsoptions to report.
func TestSanitizeTSConfigBaseURLEdgeCases(t *testing.T) {
	got := SanitizeTSConfig(`{"compilerOptions": {"baseUrl": "C:/proj/src"}}`)
	var root map[string]any
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	paths := root["compilerOptions"].(map[string]any)["paths"].(map[string]any)
	if star, _ := paths["*"].([]any); len(star) != 1 || star[0] != "C:/proj/src/*" {
		t.Errorf("absolute baseUrl: paths[*] = %v, want [C:/proj/src/*]", star)
	}

	got = SanitizeTSConfig(`{"compilerOptions": {"baseUrl": "src", "paths": "bogus"}}`)
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	if p := root["compilerOptions"].(map[string]any)["paths"]; p != "bogus" {
		t.Errorf("malformed paths altered: %v, want untouched %q", p, "bogus")
	}
}

// TestRewriteIterableArity: the four arity-1 iteration interfaces gain
// `TReturn = any, TNext = any`; declarations already at arity 3 (Iterator,
// Generator), interface BODIES, extends clauses, and type REFERENCES like
// `Iterable<T>` inside other declarations stay untouched. Idempotent.
func TestRewriteIterableArity(t *testing.T) {
	src := `interface Iterator<Yields, Returns = void, Next = undefined> {}
interface Generator<Yields = unknown, Returns = void, Next = unknown> extends Iterator<Yields, Returns, Next> {}
interface AsyncIterable<T> {
	[Symbol.asyncIterator](): AsyncIterator<T>;
}
interface Iterable<T> {
	[Symbol.iterator](): Iterator<T>;
}
interface AsyncIterableIterator<T> extends AsyncIterator<T> {}
interface IterableIterator<T> extends Iterator<T> {}
interface IterableFunction<T> extends Iterable<T> {
	(): T;
}`
	want := `interface Iterator<Yields, Returns = void, Next = undefined> {}
interface Generator<Yields = unknown, Returns = void, Next = unknown> extends Iterator<Yields, Returns, Next> {}
interface AsyncIterable<T, TReturn = any, TNext = any> {
	[Symbol.asyncIterator](): AsyncIterator<T>;
}
interface Iterable<T, TReturn = any, TNext = any> {
	[Symbol.iterator](): Iterator<T>;
}
interface AsyncIterableIterator<T, TReturn = any, TNext = any> extends AsyncIterator<T> {}
interface IterableIterator<T, TReturn = any, TNext = any> extends Iterator<T> {}
interface IterableFunction<T> extends Iterable<T> {
	(): T;
}`
	got := RewriteIterableArity(src)
	if got != want {
		t.Errorf("RewriteIterableArity:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if again := RewriteIterableArity(got); again != got {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// TestSanitizeFSRewritesCompilerTypesIterables: reads of compiler-types
// declaration files through SanitizeFS apply the arity rewrite; an equally
// named interface OUTSIDE the compiler-types package does not.
func TestSanitizeFSRewritesCompilerTypesIterables(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "diff", "project"))
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.ToSlash(dir)

	wrapped := SanitizeFS(osvfs.FS())
	iterablePath := dir + "/node_modules/@rbxts/compiler-types/types/Iterable.d.ts"
	got, ok := wrapped.ReadFile(iterablePath)
	if !ok {
		t.Fatalf("unreadable: %s", iterablePath)
	}
	for _, decl := range []string{
		"interface Iterable<T, TReturn = any, TNext = any>",
		"interface IterableIterator<T, TReturn = any, TNext = any>",
		"interface AsyncIterable<T, TReturn = any, TNext = any>",
		"interface AsyncIterableIterator<T, TReturn = any, TNext = any>",
	} {
		if !strings.Contains(got, decl) {
			t.Errorf("missing rewritten declaration %q", decl)
		}
	}
	if strings.Contains(got, "interface Iterable<T>") {
		t.Error("arity-1 Iterable declaration survived the rewrite")
	}
	// IterableFunction references Iterable<T>; the reference must survive.
	if !strings.Contains(got, "interface IterableFunction<T> extends Iterable<T>") {
		t.Error("Iterable<T> REFERENCE was rewritten (only declarations may be)")
	}

	// A d.ts outside compiler-types passes through verbatim.
	tmp := filepath.ToSlash(t.TempDir())
	raw := "interface Iterable<T> {}\n"
	if err := os.WriteFile(filepath.Join(tmp, "other.d.ts"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := wrapped.ReadFile(tmp + "/other.d.ts"); !ok || got != raw {
		t.Errorf("non-compiler-types d.ts was rewritten; got %q, want %q", got, raw)
	}
}

func TestSanitizeTSConfigLeavesValidConfigsAlone(t *testing.T) {
	src := `{"compilerOptions": {"module": "commonjs", "strict": true}}`
	got := SanitizeTSConfig(src)
	var root map[string]any
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatalf("sanitized output is not valid JSON: %v", err)
	}
	co := root["compilerOptions"].(map[string]any)
	if co["module"] != "commonjs" || co["strict"] != true {
		t.Errorf("options altered: %v", co)
	}
}

func TestSanitizeTSConfigMalformedJSONPassesThrough(t *testing.T) {
	src := `{ this is not json `
	if got := SanitizeTSConfig(src); got != src {
		t.Errorf("malformed input must pass through untouched for tsoptions to report; got %q", got)
	}
}

// TestFixtureProjectTypechecks is the acceptance test for the sanitizer: the
// rbxtsc fixture project (whose tsconfig.json carries all three rejected
// options) must produce ZERO config, options, and semantic diagnostics for
// src/01_literals.ts when loaded through the sanitized FS.
func TestFixtureProjectTypechecks(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "diff", "project"))
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.ToSlash(dir)

	fs := SanitizeFS(bundled.WrapFS(osvfs.FS()))
	host := compiler.NewCompilerHost(dir, fs, bundled.LibPath(), nil, nil)

	parsed, configDiags := tsoptions.GetParsedCommandLineOfConfigFile(dir+"/tsconfig.json", nil, nil, host, nil)
	if len(configDiags) > 0 {
		for _, d := range configDiags {
			t.Errorf("config diagnostic: %v", d.String())
		}
		t.FailNow()
	}

	program := compiler.NewProgram(compiler.ProgramOptions{Host: host, Config: parsed})
	for _, d := range program.GetProgramDiagnostics() {
		t.Errorf("program diagnostic: %v", d.String())
	}

	sf := program.GetSourceFile(dir + "/src/01_literals.ts")
	if sf == nil {
		t.Fatal("01_literals.ts not in program")
	}
	for _, d := range program.GetSemanticDiagnostics(context.Background(), sf) {
		t.Errorf("semantic diagnostic: %v", d.String())
	}
}

// TestSanitizeFSOnlyTouchesTSConfig guards the wrapper's path filter.
func TestSanitizeFSOnlyTouchesTSConfig(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "diff", "project"))
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.ToSlash(dir)

	inner := osvfs.FS()
	wrapped := SanitizeFS(inner)

	pkgPath := dir + "/package.json"
	want, ok1 := inner.ReadFile(pkgPath)
	got, ok2 := wrapped.ReadFile(pkgPath)
	if !ok1 || !ok2 || got != want {
		t.Error("non-tsconfig file was altered by SanitizeFS")
	}

	cfg, ok := wrapped.ReadFile(dir + "/tsconfig.json")
	if !ok {
		t.Fatal("tsconfig.json unreadable through SanitizeFS")
	}
	if strings.Contains(cfg, "downlevelIteration") {
		t.Error("tsconfig.json read through SanitizeFS still contains downlevelIteration")
	}

	// Only files named exactly "tsconfig.json" are intercepted: a file whose
	// name merely ends in "tsconfig.json" must pass through untouched.
	tmp := filepath.ToSlash(t.TempDir())
	raw := `{"compilerOptions": {"downlevelIteration": true}}`
	if err := os.WriteFile(filepath.Join(tmp, "my-tsconfig.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok = wrapped.ReadFile(tmp + "/my-tsconfig.json")
	if !ok {
		t.Fatal("my-tsconfig.json unreadable through SanitizeFS")
	}
	if got != raw {
		t.Errorf("my-tsconfig.json was sanitized; got %q, want %q", got, raw)
	}

	configPath := filepath.Join(tmp, "tsconfig.json")
	parentPath := filepath.Join(tmp, "tsconfig.base.json")
	parentRaw := `{"compilerOptions": {"downlevelIteration": true, "baseUrl": ".", "moduleResolution": "Node"}}`
	if err := os.WriteFile(configPath, []byte(`{"extends": "./tsconfig.base.json"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parentPath, []byte(parentRaw), 0o644); err != nil {
		t.Fatal(err)
	}

	chainWrapped := SanitizeFSWithConfigPath(inner, configPath)
	got, ok = chainWrapped.ReadFile(parentPath)
	if !ok {
		t.Fatal("tsconfig.base.json unreadable through SanitizeFSWithConfigPath")
	}
	if strings.Contains(got, "downlevelIteration") || strings.Contains(got, "baseUrl") || !strings.Contains(got, `"moduleResolution": "bundler"`) {
		t.Errorf("tsconfig.base.json in extends chain was not sanitized: %s", got)
	}
}
