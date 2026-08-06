package compile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildTimings(t *testing.T) {
	t.Run("unchanged rebuild performs zero output writes", func(t *testing.T) {
		dir := writeProject(t, "@scope/timings-warm", "")
		if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
			t.Fatalf("seed build: %v (diags: %v)", err, diags)
		}
		timings := NewBuildTimings()

		if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings}); err != nil {
			t.Fatalf("warm build: %v (diags: %v)", err, diags)
		}
		if timings.Counts.ActualWrites != 0 {
			t.Fatalf("actual writes = %d, want 0", timings.Counts.ActualWrites)
		}
		if timings.Counts.HashSkips == 0 {
			t.Fatal("hash skips = 0, want at least one")
		}
	})

	t.Run("pluginless build records zero sidecar and overlay stages", func(t *testing.T) {
		// Given
		dir := writeProject(t, "@scope/timings-pluginless", "")
		timings := NewBuildTimings()

		// When
		_, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings})
		// Then
		if err != nil {
			t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
		}
		if timings.SchemaVersion != BuildTimingSchemaVersion {
			t.Errorf("schemaVersion = %d, want %d", timings.SchemaVersion, BuildTimingSchemaVersion)
		}
		if timings.Stages.SidecarRoundTripMs != 0 || timings.Stages.OverlayProgramMs != 0 {
			t.Errorf("pluginless sidecar stages = %+v, want zero", timings.Stages)
		}
		if timings.Counts.TotalSources != 1 || timings.Counts.SelectedSources != 1 {
			t.Errorf("source counts = %+v, want one total and selected source", timings.Counts)
		}
		assertBuildTimingsNonNegative(t, timings)
	})

	t.Run("transformer build records separate sidecar and overlay stages", func(t *testing.T) {
		// Given
		setRepoSidecarPath(t)
		closeSidecarSessions()
		dir := writeProject(t, "@scope/timings-transformer", "")
		t.Cleanup(closeSidecarSessions)
		writeSidecarPluginFixture(t, dir, "", `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out",
		"plugins": [{"transform": "./plugins/prefix-string.js", "prefix": "timings"}]
	},
	"include": ["src"]
}`)
		if err := os.WriteFile(filepath.Join(dir, "plugins", "prefix-string.js"), []byte(prefixStringPlugin), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const value = \"value\";\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		timings := NewBuildTimings()

		// When
		_, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings})
		// Then
		if err != nil {
			t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
		}
		if !timings.sidecarRoundTripRecorded || !timings.overlayProgramRecorded {
			t.Errorf("transformer stages were not separately recorded: %+v", timings.Stages)
		}
		assertBuildTimingsNonNegative(t, timings)
	})
}

func assertBuildTimingsNonNegative(t *testing.T, timings *BuildTimings) {
	t.Helper()
	for label, duration := range map[string]int64{
		"total":                         timings.TotalMs,
		"initial program":               timings.Stages.InitialProgramMs,
		"incremental selection cleanup": timings.Stages.IncrementalSelectionCleanupCopyMs,
		"sidecar round trip":            timings.Stages.SidecarRoundTripMs,
		"overlay program":               timings.Stages.OverlayProgramMs,
		"project context":               timings.Stages.ProjectContextMs,
		"native compile":                timings.Stages.NativeDiagnosticsTransformRenderMs,
		"compiled output writes":        timings.Stages.CompiledOutputWritesMs,
		"declaration emit writes":       timings.Stages.DeclarationEmitWritesMs,
		"incremental manifest":          timings.Stages.IncrementalManifestMs,
		"persistence":                   timings.Stages.PersistenceMs,
	} {
		if duration < 0 {
			t.Errorf("%s = %dms, want nonnegative", label, duration)
		}
	}
}
