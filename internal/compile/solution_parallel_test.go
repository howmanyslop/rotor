package compile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

type parallelSolutionDrainer struct {
	mu             sync.Mutex
	started        chan string
	release        <-chan struct{}
	failures       map[string]error
	persistFailure map[string]bool
	persists       []func() error
	persisted      []string
	drained        []string
}

func (d *parallelSolutionDrainer) Drain(project SolutionProject) (*BuildResult, []string, error) {
	name := filepath.Base(filepath.Dir(project.ConfigPath))
	if d.started != nil {
		d.started <- name
	}
	if d.release != nil {
		<-d.release
	}

	d.mu.Lock()
	d.drained = append(d.drained, name)
	failure := d.failures[name]
	d.mu.Unlock()
	if failure != nil {
		return &BuildResult{Diagnostics: []DiagnosticInfo{{Message: "diagnostic:" + name}}}, []string{"message:" + name}, failure
	}
	persist := func() error {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.persisted = append(d.persisted, name)
		if d.persistFailure[name] {
			return errors.New("persist " + name)
		}
		return nil
	}
	if project.Options.pendingSolutionPersists != nil {
		*project.Options.pendingSolutionPersists = append(*project.Options.pendingSolutionPersists, persist)
	}
	return &BuildResult{
		Outputs:      map[string]string{name: "output:" + name},
		EmittedFiles: []string{"emitted:" + name},
		Diagnostics:  []DiagnosticInfo{{Message: "diagnostic:" + name}},
	}, []string{"message:" + name}, nil
}

func (d *parallelSolutionDrainer) appendPersists(persists []func() error) {
	d.mu.Lock()
	d.persists = append(d.persists, persists...)
	d.mu.Unlock()
}

func (d *parallelSolutionDrainer) persist() error {
	d.mu.Lock()
	persists := append([]func() error{}, d.persists...)
	d.mu.Unlock()
	for index, persist := range persists {
		if err := persist(); err != nil {
			d.mu.Lock()
			d.persists = d.persists[index:]
			d.mu.Unlock()
			return fmt.Errorf("persist solution build state: %w", err)
		}
	}
	d.mu.Lock()
	d.persists = nil
	d.mu.Unlock()
	return nil
}

func (d *parallelSolutionDrainer) persistOrder() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.persisted...)
}

func (d *parallelSolutionDrainer) drainOrder() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.drained...)
}

func TestSolutionCoordinatorRunsIndependentProjectsConcurrently(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
	writeMetadataSolutionProject(t, filepath.Join(root, "first"), filepath.Join(root, "out-first"))
	writeMetadataSolutionProject(t, filepath.Join(root, "second"), filepath.Join(root, "out-second"))
	synctest.Test(t, func(t *testing.T) {
		started := make(chan string, 2)
		release := make(chan struct{})
		drainer := &parallelSolutionDrainer{started: started, release: release}
		builders := 2
		coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
		if err != nil {
			t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
		}
		drainDone := make(chan error, 1)
		go func() {
			_, _, drainErr := coordinator.Drain()
			drainDone <- drainErr
		}()
		synctest.Wait()
		first := <-started
		secondStarted := false
		select {
		case <-started:
			secondStarted = true
		default:
		}
		close(release)
		if err := <-drainDone; err != nil {
			t.Fatalf("Drain: %v", err)
		}
		if !secondStarted || first == "" {
			t.Fatalf("independent projects did not overlap: first=%q secondStarted=%t", first, secondStarted)
		}
	})
}

func TestSolutionCoordinatorSerializesWriteConflicts(t *testing.T) {
	root := t.TempDir()
	sharedOut := filepath.Join(root, "shared-out")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
	writeMetadataSolutionProject(t, filepath.Join(root, "first"), sharedOut)
	writeMetadataSolutionProject(t, filepath.Join(root, "second"), sharedOut)
	started := make(chan string, 2)
	release := make(chan struct{})
	drainer := &parallelSolutionDrainer{started: started, release: release}
	builders := 2
	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}

	drainDone := make(chan error, 1)
	go func() {
		_, _, drainErr := coordinator.Drain()
		drainDone <- drainErr
	}()
	if got := <-started; got != "first" {
		t.Fatalf("first started project = %q, want first", got)
	}
	select {
	case got := <-started:
		t.Fatalf("conflicting project %q started before first finished", got)
	default:
	}
	release <- struct{}{}
	if got := <-started; got != "second" {
		t.Fatalf("second started project = %q, want second", got)
	}
	close(release)
	if err := <-drainDone; err != nil {
		t.Fatalf("Drain: %v", err)
	}
}

func TestSolutionCoordinatorDeterministicReductionAndPersists(t *testing.T) {
	type run struct {
		result    *BuildResult
		messages  []string
		persisted []string
		firstErr  error
	}
	results := make([]run, 0, 3)
	for _, builders := range []int{1, 2, 4} {
		root := t.TempDir()
		writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
		writeMetadataSolutionProject(t, filepath.Join(root, "first"), filepath.Join(root, "out-first"))
		writeMetadataSolutionProject(t, filepath.Join(root, "second"), filepath.Join(root, "out-second"))
		drainer := &parallelSolutionDrainer{}
		coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
		if err != nil {
			t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
		}
		result, messages, drainErr := coordinator.Drain()
		normalized := *result
		normalized.Outputs = make(map[string]string, len(result.Outputs))
		for path, output := range result.Outputs {
			normalized.Outputs[filepath.Base(path)] = output
		}
		results = append(results, run{result: &normalized, messages: messages, persisted: drainer.persistOrder(), firstErr: drainErr})
	}
	for index := 1; index < len(results); index++ {
		left := results[0]
		right := results[index]
		if !reflect.DeepEqual(*left.result, *right.result) || !reflect.DeepEqual(left.messages, right.messages) || !reflect.DeepEqual(left.persisted, right.persisted) {
			t.Fatalf("builders 1 and %d differ:\nleft=%+v\nright=%+v", index+1, left, right)
		}
		if (left.firstErr == nil) != (right.firstErr == nil) {
			t.Fatalf("builders 1 and %d differ in first error: %v vs %v", index+1, left.firstErr, right.firstErr)
		}
	}
	failureResults := make([]run, 0, 3)
	for _, builders := range []int{1, 2, 4} {
		root := t.TempDir()
		writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
		writeSolutionConfig(t, filepath.Join(root, "first"), "tsconfig.json", nil, false)
		writeSolutionConfig(t, filepath.Join(root, "second"), "tsconfig.json", nil, false)
		drainer := &parallelSolutionDrainer{failures: map[string]error{"first": errors.New("first"), "second": errors.New("second")}}
		coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
		if err != nil {
			t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
		}
		result, messages, drainErr := coordinator.Drain()
		failureResults = append(failureResults, run{result: result, messages: messages, firstErr: drainErr})
	}
	for index := 1; index < len(failureResults); index++ {
		left := failureResults[0]
		right := failureResults[index]
		if !reflect.DeepEqual(*left.result, *right.result) || !reflect.DeepEqual(left.messages, right.messages) || left.firstErr.Error() != right.firstErr.Error() {
			t.Fatalf("failing builders 1 and %d differ:\nleft=%+v\nright=%+v", index+1, left, right)
		}
	}
}

func TestSolutionCoordinatorIndependentFailureBlocksOnlyDependents(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./broken", "./sibling", "./app"}, true)
	writeSolutionConfig(t, filepath.Join(root, "broken"), "tsconfig.json", nil, false)
	writeSolutionConfig(t, filepath.Join(root, "sibling"), "tsconfig.json", nil, false)
	writeSolutionConfig(t, filepath.Join(root, "app"), "tsconfig.json", []string{"../broken"}, false)
	brokenConfig := filepath.Join(root, "broken", "tsconfig.json")
	drainer := &parallelSolutionDrainer{failures: map[string]error{"broken": errors.New("broken")}}
	builders := 2
	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	_, messages, err := coordinator.Drain()
	if err == nil || !reflect.DeepEqual(messages, []string{"diagnostic:broken", "diagnostic:sibling", "compile: project " + filepath.Join(root, "app", "tsconfig.json") + " blocked by failed dependency " + brokenConfig}) {
		t.Fatalf("first Drain = messages %v, err %v", messages, err)
	}
	if state, ok := coordinator.ProjectState(filepath.Join(root, "app", "tsconfig.json")); !ok || state.BlockedBy != brokenConfig {
		t.Fatalf("app state = %+v, found=%t", state, ok)
	}
	got := drainer.drainOrder()
	sort.Strings(got)
	if want := []string{"broken", "sibling"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first drained projects = %v, want %v", got, want)
	}
	firstDrainCount := len(got)

	drainer.mu.Lock()
	drainer.failures = nil
	drainer.mu.Unlock()
	if _, _, err := coordinator.Drain(); err != nil {
		t.Fatalf("Drain after recovery: %v", err)
	}
	got = drainer.drainOrder()
	if want := []string{"broken", "app"}; !reflect.DeepEqual(got[firstDrainCount:], want) {
		t.Fatalf("drained projects after recovery = %v, want suffix %v", got, want)
	}
}

func TestSolutionCoordinatorRetriesFailingAndLaterPersists(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second", "./third"}, true)
	writeSolutionConfig(t, filepath.Join(root, "first"), "tsconfig.json", nil, false)
	writeSolutionConfig(t, filepath.Join(root, "second"), "tsconfig.json", nil, false)
	writeSolutionConfig(t, filepath.Join(root, "third"), "tsconfig.json", nil, false)
	drainer := &parallelSolutionDrainer{persistFailure: map[string]bool{"second": true}}
	builders := 4
	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	if _, _, err := coordinator.Drain(); err == nil {
		t.Fatal("Drain unexpectedly succeeded with persist failure")
	}
	drainer.mu.Lock()
	drainer.persistFailure = nil
	drainer.mu.Unlock()
	if _, _, err := coordinator.Drain(); err != nil {
		t.Fatalf("Drain retry: %v", err)
	}
	if got, want := drainer.persistOrder(), []string{"first", "second", "second", "third"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("persist order = %v, want %v", got, want)
	}
}

func TestSolutionCoordinatorReloadForWatchRetainsBuildersAndRebuildsConflicts(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "first")
	secondDir := filepath.Join(root, "second")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
	writeMetadataSolutionProject(t, firstDir, filepath.Join(root, "out-first"))
	writeMetadataSolutionProject(t, secondDir, filepath.Join(root, "out-second"))
	drainer := &parallelSolutionDrainer{}
	builders := 2
	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	sharedOut := filepath.Join(root, "shared-out")
	firstConfig := filepath.Join(firstDir, "tsconfig.json")
	secondConfig := filepath.Join(secondDir, "tsconfig.json")
	for _, configPath := range []string{firstConfig, secondConfig} {
		config := string(mustReadFile(t, configPath))
		// writeMetadataSolutionProject embeds outDir with %q, so JSON-escaped
		// forms are needed for the replacement to match on Windows (raw
		// backslashes differ from the \\ escapes in the file text).
		config = strings.Replace(config, strconv.Quote(filepath.Join(root, "out-first")), strconv.Quote(sharedOut), 1)
		config = strings.Replace(config, strconv.Quote(filepath.Join(root, "out-second")), strconv.Quote(sharedOut), 1)
		if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := coordinator.ReloadForWatch(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}); err != nil {
		t.Fatalf("ReloadForWatch: %v", err)
	}
	if coordinator.builders != builders {
		t.Fatalf("builders after reload = %d, want %d", coordinator.builders, builders)
	}
	if got := coordinator.waitOnlyDependencies[secondConfig]; !reflect.DeepEqual(got, []string{firstConfig}) {
		t.Fatalf("wait-only dependencies after reload = %v, want %v", got, []string{firstConfig})
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	drainer.started = started
	drainer.release = release
	drainDone := make(chan error, 1)
	go func() {
		_, _, drainErr := coordinator.Drain()
		drainDone <- drainErr
	}()
	if got := <-started; got != "first" {
		t.Fatalf("first started project after reload = %q, want first", got)
	}
	select {
	case got := <-started:
		t.Fatalf("conflicting project %q started before first finished after reload", got)
	default:
	}
	release <- struct{}{}
	if got := <-started; got != "second" {
		t.Fatalf("second started project after reload = %q, want second", got)
	}
	close(release)
	if err := <-drainDone; err != nil {
		t.Fatalf("Drain after reload: %v", err)
	}
}
