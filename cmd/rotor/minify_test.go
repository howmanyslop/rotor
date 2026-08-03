package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what
// was written, plus fn's return value. Mirrors captureStdout in build_test.go.
// The pipe is drained concurrently: Windows pipes buffer only ~4KB, so a
// reader that starts after fn returns would deadlock on larger output.
func captureStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	prev := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan struct{})
	var data []byte
	go func() {
		data, _ = io.ReadAll(r)
		close(done)
	}()
	code := fn()
	_ = w.Close()
	os.Stderr = prev
	<-done
	return string(data), code
}

func TestCmdMinifyToFile(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.luau")
	out := filepath.Join(dir, "out.luau")
	src := "--!strict\n-- drop me\nlocal   x   =   1\nreturn x\n"
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdMinify([]string{in, "-o", out}); code != 0 {
		t.Fatalf("cmdMinify exit code = %d", code)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// Note the space in "1 return": `1return` would be a malformed number to
	// Luau's greedy number scanner, so the dense writer must separate them.
	want := "--!strict\nlocal x=1 return x"
	if string(got) != want {
		t.Fatalf("minified = %q, want %q", got, want)
	}
}

func TestCmdMinifyMissingInput(t *testing.T) {
	if code := cmdMinify(nil); code != 1 {
		t.Fatalf("expected exit 1 for missing input, got %d", code)
	}
}

func TestCmdMinifyParseError(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "bad.luau")
	// An unterminated string is a lexer diagnostic; minify must fail (exit 1).
	if err := os.WriteFile(in, []byte("local x = \"oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdMinify([]string{in}); code != 1 {
		t.Fatalf("expected exit 1 for parse error, got %d", code)
	}
}

func TestCmdMinifyParseErrorCodeFrame(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "bad.luau")
	// "1 2" adjacent literals are a parse error; the code frame must include
	// the source line and a caret.
	src := "local x = 1\nreturn print(1 2)\n"
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, code := captureStderr(t, func() int {
		return cmdMinify([]string{in})
	})
	if code != 1 {
		t.Fatalf("expected exit 1 for parse error, got %d", code)
	}
	// The code frame must contain the source line with the error.
	if !strings.Contains(stderr, "return print(1 2)") {
		t.Errorf("stderr does not contain the source line %q\nstderr:\n%s", "return print(1 2)", stderr)
	}
	// The code frame must contain a caret pointing at the error.
	if !strings.Contains(stderr, "^") {
		t.Errorf("stderr does not contain a caret '^'\nstderr:\n%s", stderr)
	}
}
