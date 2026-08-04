package main

import (
	"strings"
	"testing"
)

func TestAutomaticMemoryLimit(t *testing.T) {
	for _, test := range []struct {
		memory int64
		want   int64
	}{
		{memory: 0, want: 6 << 30},
		{memory: 256 << 20, want: 512 << 20},
		{memory: 8 << 30, want: 6 << 30},
		{memory: 64 << 30, want: 16 << 30},
	} {
		if got := automaticMemoryLimit(test.memory); got != test.want {
			t.Errorf("automaticMemoryLimit(%d) = %d, want %d", test.memory, got, test.want)
		}
	}
}

func TestVersionCommandPrintsInjectedVersion(t *testing.T) {
	old := version
	version = "9.9.9-test"
	t.Cleanup(func() { version = old })

	output, code := captureStdout(t, func() int {
		return run([]string{"--version"})
	})
	if code != 0 {
		t.Fatalf("run exit = %d, want 0", code)
	}

	if strings.TrimSpace(output) != "9.9.9-test" {
		t.Fatalf("version output = %q, want %q", output, "9.9.9-test")
	}
}
