package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const schemaVersion = 1

var ErrInvalidManifest = errors.New("invalid benchmark manifest")

type Phase string

const (
	PhaseCold     Phase = "cold"
	PhaseNoChange Phase = "no_change"
)

type RunOrder string

const (
	RunOrderAB RunOrder = "AB"
	RunOrderBA RunOrder = "BA"
)

type BinaryName string

const (
	BinaryBaseline  BinaryName = "baseline"
	BinaryCandidate BinaryName = "candidate"
)

type Manifest struct {
	Schema    int      `json:"schema"`
	Machine   Machine  `json:"machine"`
	Baseline  Binary   `json:"baseline"`
	Candidate Binary   `json:"candidate"`
	Records   []Record `json:"records"`
}

type Machine struct {
	OS             string            `json:"os"`
	Version        string            `json:"version"`
	CPU            string            `json:"cpu"`
	RAMBytes       int64             `json:"ram_bytes"`
	Storage        string            `json:"storage"`
	Power          string            `json:"power"`
	RunOrder       []RunOrder        `json:"run_order"`
	SidecarTimeout string            `json:"sidecar_timeout"`
	Environment    map[string]string `json:"environment"`
}

type Binary struct {
	Revision string `json:"revision"`
	Command  string `json:"command"`
}

type Record struct {
	Pair              int        `json:"pair"`
	Phase             Phase      `json:"phase"`
	Order             RunOrder   `json:"order"`
	Binary            BinaryName `json:"binary"`
	DurationMS        int64      `json:"duration_ms"`
	ExitCode          int        `json:"exit_code"`
	DiagnosticsDigest string     `json:"diagnostics_digest"`
	OutputTreeDigest  string     `json:"output_tree_digest"`
}

func loadManifest(input []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(input, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode JSON: %w", ErrInvalidManifest, err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Schema != schemaVersion {
		return invalidManifest("schema must be %d, got %d", schemaVersion, manifest.Schema)
	}
	if err := validateMachine(manifest.Machine); err != nil {
		return err
	}
	if err := validateBinary("baseline", manifest.Baseline); err != nil {
		return err
	}
	if err := validateBinary("candidate", manifest.Candidate); err != nil {
		return err
	}
	if len(manifest.Records) == 0 {
		return invalidManifest("records must not be empty")
	}

	return validateRecords(manifest.Records)
}

func validateMachine(machine Machine) error {
	if machine.OS == "" || machine.Version == "" || machine.CPU == "" || machine.Storage == "" || machine.Power == "" || machine.SidecarTimeout == "" {
		return invalidManifest("machine metadata is incomplete")
	}
	if machine.RAMBytes <= 0 {
		return invalidManifest("machine ram_bytes must be positive")
	}
	if len(machine.Environment) == 0 {
		return invalidManifest("machine environment must not be empty")
	}
	if !hasRunOrder(machine.RunOrder, RunOrderAB) || !hasRunOrder(machine.RunOrder, RunOrderBA) {
		return invalidManifest("machine run_order must include AB and BA")
	}
	return nil
}

func hasRunOrder(orders []RunOrder, wanted RunOrder) bool {
	for _, order := range orders {
		if order == wanted {
			return true
		}
	}
	return false
}

func validateBinary(name string, binary Binary) error {
	if binary.Revision == "" || binary.Command == "" {
		return invalidManifest("%s binary metadata is incomplete", name)
	}
	return nil
}

func validateRecords(records []Record) error {
	if len(records)%2 != 0 {
		return invalidManifest("records must contain ordered baseline/candidate pairs")
	}

	seenPairs := map[pairKey]struct{}{}
	phaseCounts := map[Phase]int{}
	orders := map[RunOrder]struct{}{}
	for index := 0; index < len(records); index += 2 {
		first := records[index]
		second := records[index+1]
		if err := validatePair(first, second, seenPairs); err != nil {
			return err
		}
		phaseCounts[first.Phase]++
		orders[first.Order] = struct{}{}
	}
	if phaseCounts[PhaseCold] < 10 {
		return invalidManifest("cold pairs = %d, want at least 10", phaseCounts[PhaseCold])
	}
	if phaseCounts[PhaseNoChange] < 20 {
		return invalidManifest("no-change pairs = %d, want at least 20", phaseCounts[PhaseNoChange])
	}
	if _, exists := orders[RunOrderAB]; !exists {
		return invalidManifest("records must include AB run order")
	}
	if _, exists := orders[RunOrderBA]; !exists {
		return invalidManifest("records must include BA run order")
	}
	return nil
}

type pairKey struct {
	phase Phase
	pair  int
}

func validatePair(first, second Record, seenPairs map[pairKey]struct{}) error {
	if err := validateRecord(first); err != nil {
		return err
	}
	if err := validateRecord(second); err != nil {
		return err
	}
	if first.Pair != second.Pair || first.Phase != second.Phase || first.Order != second.Order {
		return invalidManifest("records must be paired by pair, phase, and order")
	}
	key := pairKey{phase: first.Phase, pair: first.Pair}
	if _, exists := seenPairs[key]; exists {
		return invalidManifest("duplicate %s pair %d", first.Phase, first.Pair)
	}
	seenPairs[key] = struct{}{}
	if first.Order == RunOrderAB && (first.Binary != BinaryBaseline || second.Binary != BinaryCandidate) {
		return invalidManifest("pair %d with AB order must be baseline then candidate", first.Pair)
	}
	if first.Order == RunOrderBA && (first.Binary != BinaryCandidate || second.Binary != BinaryBaseline) {
		return invalidManifest("pair %d with BA order must be candidate then baseline", first.Pair)
	}
	return nil
}

func validateRecord(record Record) error {
	if record.Pair <= 0 {
		return invalidManifest("record pair must be positive")
	}
	if record.Phase != PhaseCold && record.Phase != PhaseNoChange {
		return invalidManifest("record pair %d has unsupported phase %q", record.Pair, record.Phase)
	}
	if record.Order != RunOrderAB && record.Order != RunOrderBA {
		return invalidManifest("record pair %d has unsupported order %q", record.Pair, record.Order)
	}
	if record.Binary != BinaryBaseline && record.Binary != BinaryCandidate {
		return invalidManifest("record pair %d has unsupported binary %q", record.Pair, record.Binary)
	}
	if record.DurationMS <= 0 || record.DurationMS > math.MaxInt64/2 {
		return invalidManifest("record pair %d has invalid duration_ms", record.Pair)
	}
	if record.DiagnosticsDigest == "" || record.OutputTreeDigest == "" {
		return invalidManifest("record pair %d is missing a digest", record.Pair)
	}
	return nil
}

func invalidManifest(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidManifest}, args...)...)
}
