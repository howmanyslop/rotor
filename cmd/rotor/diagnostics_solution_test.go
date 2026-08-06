package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// writeDiagnosticsSolutionProject lays down one referenced project of a census
// solution: a scoped package name (so no Rojo file is needed), composite +
// declaration (what a referenced project must be), and one file per entry of
// files under src/.
func writeDiagnosticsSolutionProject(t *testing.T, dir, pkgName string, files map[string]string) {
	t.Helper()
	config := `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"},"include":["src"]}`
	mustWrite(t, filepath.Join(dir, "tsconfig.json"), config)
	mustWrite(t, filepath.Join(dir, "package.json"), `{"name":"`+pkgName+`"}`)
	mustWrite(t, filepath.Join(dir, "src", "globals.d.ts"), noLibGlobalStubs)
	for name, text := range files {
		mustWrite(t, filepath.Join(dir, "src", name), text)
	}
}

// writeDiagnosticsSolution lays down a coordinator tsconfig referencing one
// project per entry of projects, and returns the solution root.
func writeDiagnosticsSolution(t *testing.T, projects map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)

	references := make([]string, 0, len(names))
	for _, name := range names {
		references = append(references, `{"path":"./`+name+`"}`)
		writeDiagnosticsSolutionProject(t, filepath.Join(root, name), "@scope/"+name, projects[name])
	}
	mustWrite(t, filepath.Join(root, "tsconfig.json"),
		`{"files":[],"include":[],"references":[`+strings.Join(references, ",")+`]}`)
	return root
}

func diagnosticsProjectsByDir(res jsonDiagnosticsResult) map[string]jsonProjectDiagnostics {
	byDir := make(map[string]jsonProjectDiagnostics, len(res.Projects))
	for _, project := range res.Projects {
		byDir[filepath.Base(filepath.Dir(filepath.FromSlash(project.ConfigPath)))] = project
	}
	return byDir
}

// jsonObjectKeys decodes output as a generic object and returns its keys,
// sorted — what a consumer pinning the shape actually sees.
func jsonObjectKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestCmdDiagnosticsSingleProjectJSONShapeIsUnchanged(t *testing.T) {
	// Given the single-project census a consumer already parses
	dir := writeDiagnosticsProject(t, map[string]string{
		"main.ts":  "export const clean = 1;\n",
		"noany.ts": "declare const loose: any;\nexport const taken = loose.field;\n",
	})

	// When it runs without --build
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then it emits exactly the keys it emitted before solutions existed —
	// neither `projects` nor a per-file `project` appears. Solution support is
	// additive, and a consumer that never passes --build must not have to
	// change.
	var raw json.RawMessage = []byte(strings.TrimSpace(output))
	wantTop := []string{"diagnostics", "durationMs", "fileDiagnostics", "files", "ok", "overlayMatches", "transformed", "version"}
	if got := jsonObjectKeys(t, raw); !reflect.DeepEqual(got, wantTop) {
		t.Errorf("top-level keys = %v, want %v", got, wantTop)
	}

	var envelope struct {
		FileDiagnostics []json.RawMessage `json:"fileDiagnostics"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.FileDiagnostics) == 0 {
		t.Fatal("no file entries to check")
	}
	wantFile := []string{"diagnostics", "file", "outcome", "transformed"}
	for _, entry := range envelope.FileDiagnostics {
		got := jsonObjectKeys(t, entry)
		// internalError is PR-1 behavior and only present when set.
		got = slicesWithout(got, "internalError")
		if !reflect.DeepEqual(got, wantFile) {
			t.Errorf("file entry keys = %v, want %v", got, wantFile)
		}
	}
}

func slicesWithout(values []string, drop string) []string {
	out := values[:0:0]
	for _, value := range values {
		if value != drop {
			out = append(out, value)
		}
	}
	return out
}

func TestCmdDiagnosticsBuildAttributesEveryProject(t *testing.T) {
	// Given a solution of two projects that fail in different ways
	root := writeDiagnosticsSolution(t, map[string]map[string]string{
		"alpha": {"alpha.ts": "declare const loose: any;\nexport const taken = loose.field;\n"},
		"beta":  {"beta.ts": "export const clean = 1;\n"},
	})

	// When the census runs with --build
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--build", root, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	res := decodeDiagnosticsResult(t, output)

	// Then every project is reported with its own counts, every file says which
	// project it came from, and the top-level numbers are the solution's totals
	byDir := diagnosticsProjectsByDir(res)
	if len(byDir) != 2 {
		t.Fatalf("projects = %+v, want alpha and beta", res.Projects)
	}
	if byDir["alpha"].OK {
		t.Error("alpha ok = true despite a transformer diagnostic")
	}
	if !byDir["beta"].OK {
		t.Errorf("beta ok = false on a clean project: %+v", byDir["beta"])
	}
	for _, name := range []string{"alpha", "beta"} {
		if byDir[name].Files != 1 {
			t.Errorf("%s files = %d, want 1", name, byDir[name].Files)
		}
	}
	if res.Files != 2 || res.Transformed != 2 {
		t.Errorf("files/transformed = %d/%d, want 2/2 (the solution totals)", res.Files, res.Transformed)
	}
	if res.OK {
		t.Error("ok = true on a solution with a transformer diagnostic")
	}
	byFile := diagnosticsByFile(res)
	for name, wantProject := range map[string]string{"alpha.ts": "alpha", "beta.ts": "beta"} {
		file, ok := byFile[name]
		if !ok {
			t.Fatalf("%s missing from fileDiagnostics: %+v", name, res.FileDiagnostics)
		}
		if got := filepath.Base(filepath.Dir(filepath.FromSlash(file.Project))); got != wantProject {
			t.Errorf("%s project = %q, want the %s project", name, file.Project, wantProject)
		}
	}
}

func TestCmdDiagnosticsBuildOverlayRoutesToTheOwningProject(t *testing.T) {
	// Given a solution that is clean on disk, and an overlay for one file of it
	root := writeDiagnosticsSolution(t, map[string]map[string]string{
		"alpha": {"alpha.ts": "export const clean = 1;\n"},
		"beta":  {"beta.ts": "export const clean = 1;\n"},
	})
	request, err := json.Marshal(map[string]any{
		"overlays": map[string]string{
			filepath.Join(root, "beta", "src", "beta.ts"): "declare const loose: any;\nexport const taken = loose.field;\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// When the census runs with --build
	output, code := captureStdout(t, func() int {
		return withStdin(t, string(request), func() int {
			return cmdDiagnostics([]string{"--build", root, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	res := decodeDiagnosticsResult(t, output)

	// Then the overlay applied to the project that owns the file, and only
	// there — an overlay is keyed by absolute path precisely so it routes
	byFile := diagnosticsByFile(res)
	if got := byFile["beta.ts"].Outcome; got != "transformerDiagnostic" {
		t.Errorf("beta.ts outcome = %q, want transformerDiagnostic — the overlay did not apply", got)
	}
	if got := byFile["alpha.ts"].Outcome; got != "ok" {
		t.Errorf("alpha.ts outcome = %q, want ok — the overlay leaked into another project", got)
	}
	if res.OverlayMatches != 1 {
		t.Errorf("overlayMatches = %d, want 1 (one overlay, matched once across the solution)", res.OverlayMatches)
	}
	byDir := diagnosticsProjectsByDir(res)
	if byDir["beta"].OverlayMatches != 1 || byDir["alpha"].OverlayMatches != 0 {
		t.Errorf("per-project overlayMatches alpha/beta = %d/%d, want 0/1",
			byDir["alpha"].OverlayMatches, byDir["beta"].OverlayMatches)
	}
}

func TestCmdDiagnosticsBuildUnmatchedOverlayFails(t *testing.T) {
	// Given an overlay naming a file no project of the solution holds
	root := writeDiagnosticsSolution(t, map[string]map[string]string{
		"alpha": {"alpha.ts": "export const clean = 1;\n"},
		"beta":  {"beta.ts": "export const clean = 1;\n"},
	})
	request, err := json.Marshal(map[string]any{
		"overlays": map[string]string{
			filepath.Join(root, "beta", "src", "typo.ts"): "export const nothing = 1;\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// When the census runs with --build
	output, code := captureStdout(t, func() int {
		return withStdin(t, string(request), func() int {
			return cmdDiagnostics([]string{"--build", root, "--json"})
		})
	})

	// Then the run fails rather than reporting a green census of the
	// unmodified tree — the union of every project matched nothing
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero; output:\n%s", output)
	}
	res := decodeDiagnosticsResult(t, output)
	if res.OK {
		t.Error("ok = true after an unmatched overlay")
	}
	joined := ""
	for _, d := range res.Diagnostics {
		joined += d.Message
	}
	if !strings.Contains(joined, "typo.ts") {
		t.Errorf("diagnostics = %+v, want the unmatched overlay named", res.Diagnostics)
	}
}

func TestCmdDiagnosticsBuildFailingProjectStillReportsTheRest(t *testing.T) {
	// Given a solution whose first project cannot be set up at all — a
	// non-package project with no Rojo file
	root := writeDiagnosticsSolution(t, map[string]map[string]string{
		"alpha": {"alpha.ts": "export const clean = 1;\n"},
	})
	writeDiagnosticsSolutionProject(t, filepath.Join(root, "broken"), "broken", map[string]string{
		"broken.ts": "export const clean = 1;\n",
	})
	mustWrite(t, filepath.Join(root, "tsconfig.json"),
		`{"files":[],"include":[],"references":[{"path":"./broken"},{"path":"./alpha"}]}`)

	// When the census runs with --build
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--build", root, "--json"})
		})
	})

	// Then the failure is reported and every other project is censused anyway.
	// A build blocks the rest; a census that did would silently shrink.
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero; output:\n%s", output)
	}
	res := decodeDiagnosticsResult(t, output)
	byDir := diagnosticsProjectsByDir(res)
	broken, ok := byDir["broken"]
	if !ok {
		t.Fatalf("the failing project is missing from projects: %+v", res.Projects)
	}
	if len(broken.Diagnostics) == 0 {
		t.Error("the failing project carries no diagnostic explaining why")
	}
	if _, ok := diagnosticsByFile(res)["alpha.ts"]; !ok {
		t.Errorf("alpha.ts was not censused after an earlier project failed: %+v", res.FileDiagnostics)
	}
}

func TestParseDiagnosticsArgsBuildFlagsMatchBuild(t *testing.T) {
	// Given the --build/--builders argv shapes `sloptor build` accepts
	cases := []struct {
		name string
		args []string
	}{
		{"bare --build", []string{"--build"}},
		{"-b alias", []string{"-b"}},
		{"--build with a path", []string{"--build", "some/tsconfig.json"}},
		{"--build=path", []string{"--build=some/tsconfig.json"}},
		{"--build then a flag", []string{"--build", "--json"}},
		{"--builders value", []string{"--build", "--builders", "4"}},
		{"--builders=value", []string{"--build", "--builders=4"}},
		{"--build --project", []string{"--build", "--project", "some"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// When both commands parse the same argv
			diagnostics := parseDiagnosticsArgsForTest(t, testCase.args)
			build := parseBuildArgsForTest(t, testCase.args)

			// Then they agree on every field the two commands share
			if diagnostics.build != build.build {
				t.Errorf("build = %t, want %t", diagnostics.build, build.build)
			}
			if diagnostics.buildPath != build.buildPath {
				t.Errorf("buildPath = %q, want %q", diagnostics.buildPath, build.buildPath)
			}
			if !reflect.DeepEqual(diagnostics.builders, build.builders) {
				t.Errorf("builders = %v, want %v", diagnostics.builders, build.builders)
			}
			if diagnostics.project != build.project {
				t.Errorf("project = %q, want %q", diagnostics.project, build.project)
			}
		})
	}
}

func TestParseDiagnosticsArgsRejectsBuildersWithoutBuild(t *testing.T) {
	// Given --builders without --build, which `sloptor build` rejects
	stderr, code := captureStderr(t, func() int { return cmdDiagnostics([]string{"--builders", "4"}) })
	if code != 1 {
		t.Fatalf("diagnostics --builders without --build exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "--builders requires --build") {
		t.Errorf("stderr = %q, want it to name --build", stderr)
	}
	if code := cmdBuild([]string{"--builders", "4"}); code != 1 {
		t.Fatal("the fixture is wrong: build must also reject --builders without --build")
	}
}

func TestCmdDiagnosticsBuildTextOutputNamesEveryProject(t *testing.T) {
	// Given a solution with a failing file in one project
	root := writeDiagnosticsSolution(t, map[string]map[string]string{
		"alpha": {"alpha.ts": "declare const loose: any;\nexport const taken = loose.field;\n"},
		"beta":  {"beta.ts": "export const clean = 1;\n"},
	})

	// When the census runs with --build and no --json
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--build", root})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then each project heads its own section, so a reader can tell which
	// project a file belongs to
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(filepath.ToSlash(output), name+"/tsconfig.json") {
			t.Errorf("output does not name the %s project:\n%s", name, output)
		}
	}
	if !strings.Contains(output, "transformerDiagnostic") {
		t.Errorf("output does not carry the failing file's outcome:\n%s", output)
	}
}
