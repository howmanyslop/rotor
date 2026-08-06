package compile

import (
	"context"
	"path/filepath"
	"runtime/trace"
	"sync"
	"time"
)

const BuildTimingSchemaVersion = 1

type BuildTimings struct {
	SchemaVersion int               `json:"schemaVersion"`
	OK            bool              `json:"ok"`
	TotalMs       int64             `json:"totalMs"`
	Stages        BuildTimingStages `json:"stages"`
	Counts        BuildTimingCounts `json:"counts"`

	mu                       sync.Mutex
	started                  time.Time
	finished                 bool
	preparedDirectories      map[string]struct{}
	sidecarRoundTripRecorded bool
	overlayProgramRecorded   bool
}

type BuildTimingStages struct {
	InitialProgramMs                   int64 `json:"initialProgramMs"`
	IncrementalSelectionCleanupCopyMs  int64 `json:"incrementalSelectionCleanupCopyMs"`
	SidecarRoundTripMs                 int64 `json:"sidecarRoundTripMs"`
	OverlayProgramMs                   int64 `json:"overlayProgramMs"`
	ProjectContextMs                   int64 `json:"projectContextMs"`
	NativeDiagnosticsTransformRenderMs int64 `json:"nativeDiagnosticsTransformRenderMs"`
	CompiledOutputWritesMs             int64 `json:"compiledOutputWritesMs"`
	DeclarationEmitWritesMs            int64 `json:"declarationEmitWritesMs"`
	IncrementalManifestMs              int64 `json:"incrementalManifestMs"`
	PersistenceMs                      int64 `json:"persistenceMs"`
}

type BuildTimingCounts struct {
	TotalSources               int `json:"totalSources"`
	SelectedSources            int `json:"selectedSources"`
	EmittedEntries             int `json:"emittedEntries"`
	ScheduledSourceMapWrites   int `json:"scheduledSourceMapWrites"`
	ScheduledDeclarationWrites int `json:"scheduledDeclarationWrites"`
	ActualWrites               int `json:"actualWrites"`
	HashSkips                  int `json:"hashSkips"`
	UniquePreparedDirectories  int `json:"uniquePreparedDirectories"`
	EffectiveWriteWorkers      int `json:"effectiveWriteWorkers"`
}

func (timings *BuildTimings) setHashSkips(skips int) {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Counts.HashSkips = skips
}

type buildTimingStage uint8

const (
	initialProgramStage buildTimingStage = iota
	incrementalSelectionCleanupCopyStage
	projectContextStage
	nativeDiagnosticsTransformRenderStage
	compiledOutputWritesStage
	declarationEmitWritesStage
	incrementalManifestStage
	persistenceStage
)

func NewBuildTimings() *BuildTimings {
	return &BuildTimings{
		SchemaVersion:       BuildTimingSchemaVersion,
		started:             time.Now(),
		preparedDirectories: map[string]struct{}{},
	}
}

func (timings *BuildTimings) startStage(stage buildTimingStage) func() {
	region := trace.StartRegion(context.Background(), stage.traceName())
	started := time.Now()
	return func() {
		region.End()
		if timings != nil {
			timings.addStageDuration(stage, time.Since(started))
		}
	}
}

func (stage buildTimingStage) traceName() string {
	switch stage {
	case initialProgramStage:
		return "program creation and parse/load"
	case incrementalSelectionCleanupCopyStage:
		return "incremental selection and cleanup"
	case projectContextStage:
		return "project context"
	case nativeDiagnosticsTransformRenderStage:
		return "diagnostics and transform/render"
	case compiledOutputWritesStage:
		return "compiled output writes"
	case declarationEmitWritesStage:
		return "declaration emit/write"
	case incrementalManifestStage:
		return "incremental manifest"
	case persistenceStage:
		return "persistence"
	default:
		return "build"
	}
}

func (timings *BuildTimings) addStageDuration(stage buildTimingStage, duration time.Duration) {
	timings.mu.Lock()
	defer timings.mu.Unlock()
	milliseconds := duration.Milliseconds()
	switch stage {
	case initialProgramStage:
		timings.Stages.InitialProgramMs += milliseconds
	case incrementalSelectionCleanupCopyStage:
		timings.Stages.IncrementalSelectionCleanupCopyMs += milliseconds
	case projectContextStage:
		timings.Stages.ProjectContextMs += milliseconds
	case nativeDiagnosticsTransformRenderStage:
		timings.Stages.NativeDiagnosticsTransformRenderMs += milliseconds
	case compiledOutputWritesStage:
		timings.Stages.CompiledOutputWritesMs += milliseconds
	case declarationEmitWritesStage:
		timings.Stages.DeclarationEmitWritesMs += milliseconds
	case incrementalManifestStage:
		timings.Stages.IncrementalManifestMs += milliseconds
	case persistenceStage:
		timings.Stages.PersistenceMs += milliseconds
	}
}

func (timings *BuildTimings) recordPreparedTransformerProgram(prepared *preparedTransformerProgram) {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Stages.SidecarRoundTripMs += prepared.sidecarRoundTripDuration.Milliseconds()
	timings.Stages.OverlayProgramMs += prepared.overlayProgramDuration.Milliseconds()
	timings.sidecarRoundTripRecorded = prepared.sidecarRoundTripRecorded
	timings.overlayProgramRecorded = prepared.overlayProgramRecorded
}

func (timings *BuildTimings) setSourceCounts(totalSources, selectedSources int) {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Counts.TotalSources = totalSources
	timings.Counts.SelectedSources = selectedSources
}

func (timings *BuildTimings) setEffectiveWriteWorkers(workers int) {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Counts.EffectiveWriteWorkers = workers
}

func (timings *BuildTimings) addScheduledSourceMapWrite() {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Counts.ScheduledSourceMapWrites++
}

func (timings *BuildTimings) addScheduledDeclarationWrites(writes int) {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Counts.ScheduledDeclarationWrites += writes
}

func (timings *BuildTimings) recordOutputWrite(path string, wrote bool) {
	if timings == nil || !wrote {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Counts.ActualWrites++
	timings.preparedDirectories[filepath.Clean(filepath.Dir(path))] = struct{}{}
	timings.Counts.UniquePreparedDirectories = len(timings.preparedDirectories)
}

func (timings *BuildTimings) setEmittedEntries(entries int) {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.Counts.EmittedEntries = entries
}

func (timings *BuildTimings) finish() {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	if timings.finished {
		return
	}
	timings.TotalMs = time.Since(timings.started).Milliseconds()
	timings.finished = true
}

func (timings *BuildTimings) SetOK(ok bool) {
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.OK = ok
}
