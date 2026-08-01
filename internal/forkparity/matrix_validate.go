package forkparity

import (
	"encoding/json"
	"strconv"
	"strings"
)

func transformerFixtureDrifts(fixture TransformerFixture, result *RunResult) []MatrixDrift {
	if len(fixture.Diagnostics) > 0 {
		drifts := []MatrixDrift{}
		if result.ExitCode == 0 {
			drifts = append(drifts, matrixExitDrift(1, result.ExitCode))
		}
		if len(result.OutputTree) != 0 {
			drifts = append(drifts, MatrixDrift{
				Surface: MatrixSurfaceByte,
				Detail:  "diagnostic fixture emitted output",
			})
		}
		actual := matrixANSISequence.ReplaceAllString(result.Stdout+result.Stderr, "")
		for _, expected := range fixture.Diagnostics {
			if !strings.Contains(actual, expected) {
				drift := matrixTextDrift(MatrixSurfaceDiagnostic, "diagnostics", expected, actual)
				drift.Detail = actual
				drifts = append(drifts, drift)
			}
		}
		return drifts
	}

	want := MatrixObservation{
		ExitCode: 0,
		Artifacts: map[string][]byte{
			"out/main.luau": []byte(fixture.ExpectedLuau),
		},
		WriteTrace:      []string{},
		PluginTrace:     []string{},
		WatchTranscript: []string{},
	}
	got := MatrixObservation{
		ExitCode:        result.ExitCode,
		Artifacts:       matrixOutputArtifacts(result.OutputTree),
		WriteTrace:      []string{},
		PluginTrace:     []string{},
		WatchTranscript: []string{},
	}
	drifts := CompareMatrixObservations(want, got)
	if result.ExitCode != 0 {
		drifts = append(drifts, MatrixDrift{
			Surface: MatrixSurfaceDiagnostic,
			Detail:  matrixANSISequence.ReplaceAllString(result.Stdout+result.Stderr, ""),
		})
	}
	return drifts
}

func projectFixtureDrifts(fixture ProjectFixture, result *RunResult, artifacts map[string][]byte) []MatrixDrift {
	drifts := []MatrixDrift{}
	combined := matrixANSISequence.ReplaceAllString(result.Stdout+result.Stderr, "")
	if result.ExitCode != fixture.ExpectedExitCode {
		drifts = append(drifts, matrixExitDrift(fixture.ExpectedExitCode, result.ExitCode))
		drifts = append(drifts, MatrixDrift{Surface: MatrixSurfaceDiagnostic, Detail: combined})
	}
	for _, expected := range []string{fixture.ExpectedStdout, fixture.ExpectedStderr} {
		if expected != "" && !strings.Contains(combined, expected) {
			drifts = append(drifts, matrixTextDrift(MatrixSurfaceDiagnostic, "diagnostics", expected, combined))
		}
	}
	if fixture.ExpectedExitCode != 0 {
		return drifts
	}
	if !hasArtifactSuffix(artifacts, ".luau") {
		drifts = append(drifts, MatrixDrift{Surface: MatrixSurfaceByte, Detail: "successful project emitted no Luau artifact"})
	}
	switch fixture.Name {
	case "build-declarations", "cross-project-dts", "transformer-declarations":
		if !hasArtifactSuffix(artifacts, ".d.ts") {
			drifts = append(drifts, MatrixDrift{Surface: MatrixSurfaceDeclaration, Detail: "declaration fixture emitted no declaration artifact"})
		}
		if fixture.Name == "transformer-declarations" {
			if !hasArtifactText(artifacts, ".d.ts", "__DECLARATION_MARKER__") {
				drifts = append(drifts, MatrixDrift{Surface: MatrixSurfaceDeclaration, Detail: "afterDeclarations marker missing from declarations"})
			}
			if hasArtifactText(artifacts, ".luau", "__DECLARATION_MARKER__") {
				drifts = append(drifts, MatrixDrift{Surface: MatrixSurfacePlugin, Detail: "afterDeclarations marker leaked into Luau"})
			}
		}
	case "per-project-rojo":
		if !hasArtifactComponent(artifacts, ".rotor/cache/rojo/") {
			drifts = append(drifts, MatrixDrift{Surface: MatrixSurfaceCache, Detail: "Rojo fixture emitted no resolver cache artifact"})
		}
	case "transformer-ordering":
		if !hasArtifactText(artifacts, ".luau", "__ORDER_BEFORE__") || !hasArtifactText(artifacts, ".luau", "__ORDER_AFTER__") {
			drifts = append(drifts, MatrixDrift{Surface: MatrixSurfacePlugin, Detail: "transformer order markers missing from Luau"})
		}
	case "transformer-sourcemap":
		if !matrixSourceMapValid(artifacts) {
			drifts = append(drifts, MatrixDrift{Surface: MatrixSurfaceSourceMap, Detail: "source-map fixture emitted no valid Luau source map"})
		}
	}
	return drifts
}

func matrixOutputArtifacts(tree map[string][]byte) map[string][]byte {
	artifacts := make(map[string][]byte, len(tree))
	for path, contents := range tree {
		if strings.HasSuffix(path, ".luau") {
			artifacts["out/"+path] = contents
		}
	}
	return artifacts
}

func matrixExitDrift(want, got int) MatrixDrift {
	return MatrixDrift{
		Surface:        MatrixSurfaceExit,
		ExpectedDigest: digest([]byte(strconv.Itoa(want))),
		ActualDigest:   digest([]byte(strconv.Itoa(got))),
		Detail:         "exit status differs",
	}
}

func hasArtifactSuffix(artifacts map[string][]byte, suffix string) bool {
	for path := range artifacts {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func hasArtifactPrefix(artifacts map[string][]byte, prefix string) bool {
	for path := range artifacts {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func hasArtifactComponent(artifacts map[string][]byte, component string) bool {
	for path := range artifacts {
		if strings.Contains(path, component) {
			return true
		}
	}
	return false
}

func hasArtifactText(artifacts map[string][]byte, suffix, text string) bool {
	for path, contents := range artifacts {
		if strings.HasSuffix(path, suffix) && strings.Contains(string(contents), text) {
			return true
		}
	}
	return false
}

func matrixSourceMapValid(artifacts map[string][]byte) bool {
	for path, contents := range artifacts {
		if !strings.HasSuffix(path, ".luau.map") {
			continue
		}
		var sourceMap struct {
			Version        int      `json:"version"`
			SourcesContent []string `json:"sourcesContent"`
			Mappings       string   `json:"mappings"`
		}
		if json.Unmarshal(contents, &sourceMap) == nil && sourceMap.Version == 3 && len(sourceMap.SourcesContent) > 0 && sourceMap.Mappings != "" {
			return true
		}
	}
	return false
}
