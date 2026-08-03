package main

import "fmt"

const (
	ReasonInvalidManifest     = "invalid_manifest"
	ReasonExitCode            = "exit_code_mismatch"
	ReasonDiagnostics         = "diagnostics_digest_mismatch"
	ReasonOutputTree          = "output_tree_digest_mismatch"
	ReasonColdPerformance     = "cold_median_ratio_exceeded"
	ReasonNoChangePerformance = "no_change_p95_ratio_exceeded"
)

var (
	coldLimit     = Fraction{Numerator: 3, Denominator: 5}
	noChangeLimit = Fraction{Numerator: 11, Denominator: 10}
)

type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
)

type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Verdict struct {
	Schema     int         `json:"schema"`
	Status     Status      `json:"status"`
	Reasons    []Reason    `json:"reasons"`
	Statistics *Statistics `json:"statistics,omitempty"`
	Thresholds *Thresholds `json:"thresholds,omitempty"`
}

type Statistics struct {
	Cold     MetricStatistics `json:"cold"`
	NoChange MetricStatistics `json:"no_change"`
}

type MetricStatistics struct {
	PairCount         int      `json:"pair_count"`
	BaselineMetricMS  Fraction `json:"baseline_metric_ms"`
	CandidateMetricMS Fraction `json:"candidate_metric_ms"`
	Ratio             Fraction `json:"ratio"`
}

type Thresholds struct {
	ColdMedianRatio  Fraction `json:"cold_median_ratio"`
	NoChangeP95Ratio Fraction `json:"no_change_p95_ratio"`
}

func evaluate(manifest Manifest) (Verdict, error) {
	if err := validateManifest(manifest); err != nil {
		return Verdict{}, err
	}
	cold, noChange, reasons := comparePairs(manifest.Records)
	coldStats, err := metricStatistics(cold, median)
	if err != nil {
		return Verdict{}, fmt.Errorf("cold statistics: %w", err)
	}
	noChangeStats, err := metricStatistics(noChange, p95Fraction)
	if err != nil {
		return Verdict{}, fmt.Errorf("no-change statistics: %w", err)
	}

	statistics := Statistics{Cold: coldStats, NoChange: noChangeStats}
	if exceeds(coldStats.Ratio, coldLimit) {
		reasons = append(reasons, Reason{
			Code:    ReasonColdPerformance,
			Message: fmt.Sprintf("cold median ratio %d/%d exceeds %d/%d", coldStats.Ratio.Numerator, coldStats.Ratio.Denominator, coldLimit.Numerator, coldLimit.Denominator),
		})
	}
	if exceeds(noChangeStats.Ratio, noChangeLimit) {
		reasons = append(reasons, Reason{
			Code:    ReasonNoChangePerformance,
			Message: fmt.Sprintf("no-change p95 ratio %d/%d exceeds %d/%d", noChangeStats.Ratio.Numerator, noChangeStats.Ratio.Denominator, noChangeLimit.Numerator, noChangeLimit.Denominator),
		})
	}

	status := StatusPass
	if len(reasons) > 0 {
		status = StatusFail
	}
	return Verdict{
		Schema:     schemaVersion,
		Status:     status,
		Reasons:    reasons,
		Statistics: &statistics,
		Thresholds: &Thresholds{ColdMedianRatio: coldLimit, NoChangeP95Ratio: noChangeLimit},
	}, nil
}

type pairedSamples struct {
	baseline  []int64
	candidate []int64
}

func comparePairs(records []Record) (pairedSamples, pairedSamples, []Reason) {
	cold := pairedSamples{baseline: []int64{}, candidate: []int64{}}
	noChange := pairedSamples{baseline: []int64{}, candidate: []int64{}}
	reasons := []Reason{}
	for index := 0; index < len(records); index += 2 {
		first := records[index]
		second := records[index+1]
		baseline, candidate := first, second
		if first.Binary == BinaryCandidate {
			baseline, candidate = second, first
		}
		reasons = append(reasons, correctnessReasons(baseline, candidate)...)
		samples := &cold
		if baseline.Phase == PhaseNoChange {
			samples = &noChange
		}
		samples.baseline = append(samples.baseline, baseline.DurationMS)
		samples.candidate = append(samples.candidate, candidate.DurationMS)
	}
	return cold, noChange, reasons
}

func correctnessReasons(baseline, candidate Record) []Reason {
	reasons := []Reason{}
	if baseline.ExitCode != 0 || candidate.ExitCode != 0 || baseline.ExitCode != candidate.ExitCode {
		reasons = append(reasons, Reason{
			Code:    ReasonExitCode,
			Message: fmt.Sprintf("%s pair %d exit codes baseline=%d candidate=%d", baseline.Phase, baseline.Pair, baseline.ExitCode, candidate.ExitCode),
		})
	}
	if baseline.DiagnosticsDigest != candidate.DiagnosticsDigest {
		reasons = append(reasons, Reason{
			Code:    ReasonDiagnostics,
			Message: fmt.Sprintf("%s pair %d diagnostics digest differs", baseline.Phase, baseline.Pair),
		})
	}
	if baseline.OutputTreeDigest != candidate.OutputTreeDigest {
		reasons = append(reasons, Reason{
			Code:    ReasonOutputTree,
			Message: fmt.Sprintf("%s pair %d output-tree digest differs", baseline.Phase, baseline.Pair),
		})
	}
	return reasons
}

type metric func([]int64) (Fraction, error)

func metricStatistics(samples pairedSamples, calculate metric) (MetricStatistics, error) {
	baseline, err := calculate(samples.baseline)
	if err != nil {
		return MetricStatistics{}, err
	}
	candidate, err := calculate(samples.candidate)
	if err != nil {
		return MetricStatistics{}, err
	}
	return MetricStatistics{
		PairCount:         len(samples.baseline),
		BaselineMetricMS:  baseline,
		CandidateMetricMS: candidate,
		Ratio:             ratio(candidate, baseline),
	}, nil
}

func p95Fraction(durations []int64) (Fraction, error) {
	p95, err := nearestRankP95(durations)
	if err != nil {
		return Fraction{}, err
	}
	return Fraction{Numerator: p95, Denominator: 1}, nil
}
