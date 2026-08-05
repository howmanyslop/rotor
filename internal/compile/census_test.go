package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCensusProject lays down a package-type project (no Rojo file needed)
// whose src/ holds one file per entry of files, plus the noLib global stubs.
func writeCensusProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := writeProject(t, "@scope/census-fixture", "")
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, "src", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCompileProjectOverlaysOverrideFileText(t *testing.T) {
	// Given a project whose on-disk main.ts declares nothing
	dir := writeCensusProject(t, map[string]string{"main.ts": "export const fromDisk = 1;\n"})
	mainPath := filepath.Join(dir, "src", "main.ts")

	// When the file text is overridden in memory
	outputs, diags, err := CompileProjectWithOptions(dir, ProjectOptions{
		Overlays: map[string]string{mainPath: "export const fromOverlay = 2;\n"},
	})

	// Then the compiled Luau reflects the overlay, not the disk
	if err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}
	text, ok := outputs["out/main.luau"]
	if !ok {
		t.Fatalf("out/main.luau missing; outputs: %v", keys(outputs))
	}
	if !strings.Contains(text, "fromOverlay") {
		t.Errorf("compiled output did not use the overlay text:\n%s", text)
	}
	if strings.Contains(text, "fromDisk") {
		t.Errorf("compiled output still used the on-disk text:\n%s", text)
	}
}

func TestCompileProjectOverlaysLeaveDiskUntouched(t *testing.T) {
	// Given a project with a known on-disk file
	const onDisk = "export const fromDisk = 1;\n"
	dir := writeCensusProject(t, map[string]string{"main.ts": onDisk})
	mainPath := filepath.Join(dir, "src", "main.ts")

	// When it is compiled through an overlay
	if _, diags, err := CompileProjectWithOptions(dir, ProjectOptions{
		Overlays: map[string]string{mainPath: "export const fromOverlay = 2;\n"},
	}); err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}

	// Then the file on disk is unchanged
	got, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != onDisk {
		t.Errorf("overlay wrote through to disk: %q", string(got))
	}
}
