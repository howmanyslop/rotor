package main

import (
	"errors"
	"testing"
)

func TestEvaluate_returns_expected_verdict_for_thresholds_and_correctness(t *testing.T) {
	tests := []struct {
		name       string
		modify     func(*Manifest)
		wantStatus Status
		wantError  error
		wantReason string
	}{
		{
			name:       "passes exact cold and no change boundaries",
			modify:     func(*Manifest) {},
			wantStatus: StatusPass,
		},
		{
			name: "fails cold ratio of 0.6001",
			modify: func(manifest *Manifest) {
				setPhaseDurations(manifest, PhaseCold, 10000, 6001)
			},
			wantStatus: StatusFail,
			wantReason: ReasonColdPerformance,
		},
		{
			name: "fails no change ratio of 1.101",
			modify: func(manifest *Manifest) {
				setPhaseDurations(manifest, PhaseNoChange, 10000, 11010)
			},
			wantStatus: StatusFail,
			wantReason: ReasonNoChangePerformance,
		},
		{
			name: "rejects too few cold pairs",
			modify: func(manifest *Manifest) {
				manifest.Records = manifest.Records[2:]
			},
			wantError: ErrInvalidManifest,
		},
		{
			name: "rejects unpaired AB records",
			modify: func(manifest *Manifest) {
				manifest.Records = append(manifest.Records[:1], manifest.Records[2:]...)
			},
			wantError: ErrInvalidManifest,
		},
		{
			name: "fails nonzero exit code",
			modify: func(manifest *Manifest) {
				manifest.Records[1].ExitCode = 1
			},
			wantStatus: StatusFail,
			wantReason: ReasonExitCode,
		},
		{
			name: "fails diagnostics drift",
			modify: func(manifest *Manifest) {
				manifest.Records[1].DiagnosticsDigest = "changed"
			},
			wantStatus: StatusFail,
			wantReason: ReasonDiagnostics,
		},
		{
			name: "fails output tree drift",
			modify: func(manifest *Manifest) {
				manifest.Records[1].OutputTreeDigest = "changed"
			},
			wantStatus: StatusFail,
			wantReason: ReasonOutputTree,
		},
		{
			name: "fails intentionally slowed candidate",
			modify: func(manifest *Manifest) {
				setCandidateDuration(manifest, PhaseCold, 900)
				manifest.Records[1].OutputTreeDigest = "changed"
			},
			wantStatus: StatusFail,
			wantReason: ReasonColdPerformance,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			manifest := passingManifest()
			test.modify(&manifest)

			// When
			verdict, err := evaluate(manifest)

			// Then
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("evaluate error = %v, want %v", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if verdict.Status != test.wantStatus {
				t.Errorf("status = %q, want %q", verdict.Status, test.wantStatus)
			}
			if test.wantReason != "" && !hasReason(verdict.Reasons, test.wantReason) {
				t.Errorf("reasons = %#v, missing %q", verdict.Reasons, test.wantReason)
			}
		})
	}
}

func TestEvaluate_reports_exact_boundary_statistics(t *testing.T) {
	// Given
	manifest := passingManifest()

	// When
	verdict, err := evaluate(manifest)

	// Then
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if verdict.Statistics.Cold.Ratio != coldLimit {
		t.Errorf("cold ratio = %#v, want %#v", verdict.Statistics.Cold.Ratio, coldLimit)
	}
	if verdict.Statistics.NoChange.Ratio != noChangeLimit {
		t.Errorf("no-change ratio = %#v, want %#v", verdict.Statistics.NoChange.Ratio, noChangeLimit)
	}
}

func TestLoadManifest_rejects_malformed_schema(t *testing.T) {
	// Given
	input := []byte(`{"schema":2}`)

	// When
	_, err := loadManifest(input)

	// Then
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("loadManifest error = %v, want %v", err, ErrInvalidManifest)
	}
}

func passingManifest() Manifest {
	manifest := Manifest{
		Schema: 1,
		Machine: Machine{
			OS:             "Windows",
			Version:        "11",
			CPU:            "test CPU",
			RAMBytes:       16 << 30,
			Storage:        "SSD",
			Power:          "plugged in",
			RunOrder:       []RunOrder{RunOrderAB, RunOrderBA},
			SidecarTimeout: "300s",
			Environment:    map[string]string{"ROTOR_SIDECAR_TIMEOUT": "300s"},
		},
		Baseline:  Binary{Revision: "baseline", Command: "rotor build"},
		Candidate: Binary{Revision: "candidate", Command: "rotor build"},
		Records:   []Record{},
	}

	for pair := 1; pair <= 10; pair++ {
		appendPair(&manifest, PhaseCold, pair, 1000, 600)
	}
	for pair := 1; pair <= 20; pair++ {
		appendPair(&manifest, PhaseNoChange, pair, 1000, 1100)
	}

	return manifest
}

func appendPair(manifest *Manifest, phase Phase, pair int, baselineDuration, candidateDuration int64) {
	order := RunOrderAB
	if pair%2 == 0 {
		order = RunOrderBA
	}
	baseline := Record{
		Pair:              pair,
		Phase:             phase,
		Order:             order,
		Binary:            BinaryBaseline,
		DurationMS:        baselineDuration,
		ExitCode:          0,
		DiagnosticsDigest: "diagnostics",
		OutputTreeDigest:  "output",
	}
	candidate := Record{
		Pair:              pair,
		Phase:             phase,
		Order:             order,
		Binary:            BinaryCandidate,
		DurationMS:        candidateDuration,
		ExitCode:          0,
		DiagnosticsDigest: "diagnostics",
		OutputTreeDigest:  "output",
	}
	if order == RunOrderAB {
		manifest.Records = append(manifest.Records, baseline, candidate)
		return
	}
	manifest.Records = append(manifest.Records, candidate, baseline)
}

func setCandidateDuration(manifest *Manifest, phase Phase, duration int64) {
	for index := range manifest.Records {
		if manifest.Records[index].Phase == phase && manifest.Records[index].Binary == BinaryCandidate {
			manifest.Records[index].DurationMS = duration
		}
	}
}

func setPhaseDurations(manifest *Manifest, phase Phase, baselineDuration, candidateDuration int64) {
	for index := range manifest.Records {
		if manifest.Records[index].Phase != phase {
			continue
		}
		if manifest.Records[index].Binary == BinaryBaseline {
			manifest.Records[index].DurationMS = baselineDuration
			continue
		}
		manifest.Records[index].DurationMS = candidateDuration
	}
}

func hasReason(reasons []Reason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}
