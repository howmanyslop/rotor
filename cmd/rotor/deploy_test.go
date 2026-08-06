package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDeployFixture creates a minimal project: rotor.toml with one dev
// environment (a place and a badge with an icon) plus the referenced files.
func writeDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	configTOML := `[assets.creator]
type = "group"
id = 99

[deploy.environments.dev]
universeId = 111
[deploy.environments.dev.places.start]
file = "game.rbxl"
placeId = 222
[deploy.environments.dev.badges.winner]
name = "Winner!"
icon = "icon.png"
`
	for name, data := range map[string]string{
		"rotor.toml": configTOML,
		"game.rbxl":  "rbxl-bytes",
		"icon.png":   "png-bytes",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runDeploy(t *testing.T, args []string, stdin string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = execute(append([]string{"deploy"}, args...), cliStreams{in: strings.NewReader(stdin), out: &out, err: &errBuf})
	return code, out.String(), errBuf.String()
}

func TestCmdDeployArgErrors(t *testing.T) {
	cases := [][]string{
		{},                               // missing subcommand
		{"destroy", "-e", "dev"},         // unknown subcommand
		{"plan", "-e"},                   // -e without value
		{"plan", "."},                    // env required
		{"plan", "--bogus", "-e", "dev"}, // unknown flag
		{"plan", "a", "b", "-e", "dev"},  // extra positional
	}
	for _, args := range cases {
		if code := cmdDeploy(args); code != 1 {
			t.Fatalf("args %v: exit %d, want 1", args, code)
		}
	}
	// help exits 0
	if code := cmdDeploy([]string{"--help"}); code != 0 {
		t.Fatalf("--help: exit %d", code)
	}
}

func TestCmdDeployPlanFreshState(t *testing.T) {
	dir := writeDeployFixture(t)
	code, out, errOut := runDeploy(t, []string{"plan", dir, "-e", "dev"}, "")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"create", "place_file/start", "badge/winner", "asset/icon.png", "3 to create"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan output missing %q:\n%s", want, out)
		}
	}
}

func TestCmdDeployPlanWithFakeState(t *testing.T) {
	dir := writeDeployFixture(t)
	stateDir := filepath.Join(dir, ".rotor", "deploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":1,"resources":{
		"place_file/start":{"inputsHash":"sha256:stale"},
		"badge/old":{"inputsHash":"sha256:x","outputs":{"badgeId":900}}}}`
	if err := os.WriteFile(filepath.Join(stateDir, "dev.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runDeploy(t, []string{"plan", dir, "-e", "dev"}, "")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"update", "place_file/start", "badge/old", "blocked", "--allow-deletes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan output missing %q:\n%s", want, out)
		}
	}

	// With --allow-deletes the delete is no longer blocked.
	code, out, _ = runDeploy(t, []string{"plan", dir, "-e", "dev", "--allow-deletes"}, "")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "blocked") {
		t.Fatalf("delete still blocked with --allow-deletes:\n%s", out)
	}
	if !strings.Contains(out, "1 to delete") {
		t.Fatalf("plan summary missing delete:\n%s", out)
	}
}

func TestCmdDeployUnknownEnv(t *testing.T) {
	dir := writeDeployFixture(t)
	code, _, errOut := runDeploy(t, []string{"plan", dir, "-e", "prod"}, "")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "prod") || !strings.Contains(errOut, "dev") {
		t.Fatalf("error should name the bad env and the available ones:\n%s", errOut)
	}
}

func TestCmdDeployApplyNeedsKey(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "")
	dir := writeDeployFixture(t)
	code, _, errOut := runDeploy(t, []string{"apply", dir, "-e", "dev", "--yes"}, "")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "ROBLOX_API_KEY") {
		t.Fatalf("error should name ROBLOX_API_KEY:\n%s", errOut)
	}
}

func TestCmdDeployApplyConfirmAbort(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "test-key")
	dir := writeDeployFixture(t)
	code, out, errOut := runDeploy(t, []string{"apply", dir, "-e", "dev"}, "wrong\n")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "type the environment name to confirm") {
		t.Fatalf("missing confirmation prompt:\n%s", out)
	}
	if !strings.Contains(errOut, "aborted") {
		t.Fatalf("missing abort message:\n%s", errOut)
	}
}

func TestCmdDeployApplyBlockedDeleteErrors(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "test-key")
	dir := writeDeployFixture(t)
	stateDir := filepath.Join(dir, ".rotor", "deploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":1,"resources":{"badge/old":{"inputsHash":"sha256:x"}}}`
	if err := os.WriteFile(filepath.Join(stateDir, "dev.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runDeploy(t, []string{"apply", dir, "-e", "dev", "--yes"}, "")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "--allow-deletes") {
		t.Fatalf("error should mention --allow-deletes:\n%s", errOut)
	}
}
