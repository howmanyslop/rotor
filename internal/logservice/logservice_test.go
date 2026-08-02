package logservice

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// capture redirects Output to a buffer for one test and resets the
// partial-line tracker.
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldOut, oldVerbose, oldPartial := Output, Verbose, partial
	Output = &buf
	partial = false
	t.Cleanup(func() {
		Output, Verbose, partial = oldOut, oldVerbose, oldPartial
	})
	return &buf
}

func TestWriteLineInjectsNewlineAfterPartialWrite(t *testing.T) {
	buf := capture(t)
	Write("compiling")        // partial benchmark-style write
	WriteLine("Hello there.") // upstream injects "\n" first (LogService.ts L13-15)
	if got, want := buf.String(), "compiling\nHello there.\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteLineNoInjectionAfterCompleteLine(t *testing.T) {
	buf := capture(t)
	Write("done\n")
	WriteLine("next")
	if got, want := buf.String(), "done\nnext\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWarnFormat(t *testing.T) {
	buf := capture(t)
	// Output is a bytes.Buffer, not a TTY, so the prefix is uncolored.
	Warn("Multiple *.project.json files found, using a.project.json")
	want := "Compiler Warning: Multiple *.project.json files found, using a.project.json\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteLineIfVerboseGate(t *testing.T) {
	buf := capture(t)
	Verbose = false
	WriteLineIfVerbose("hidden")
	if buf.Len() != 0 {
		t.Errorf("non-verbose write leaked: %q", buf.String())
	}
	Verbose = true
	WriteLineIfVerbose("compiling as model..")
	if got, want := buf.String(), "compiling as model..\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBenchmarkIfVerboseFormat(t *testing.T) {
	buf := capture(t)
	Verbose = true
	ran := false
	BenchmarkIfVerbose("copy include files", func() { ran = true })
	if !ran {
		t.Fatal("callback did not run")
	}
	// Exact upstream shape: `name ( N ms )\n` (benchmark.ts L4, L9).
	if !regexp.MustCompile(`^copy include files \( \d+ ms \)\n$`).MatchString(buf.String()) {
		t.Errorf("benchmark line %q does not match upstream format", buf.String())
	}
}

func TestBenchmarkIfVerboseSilentWhenNotVerbose(t *testing.T) {
	buf := capture(t)
	Verbose = false
	ran := false
	BenchmarkIfVerbose("writing compiled files", func() { ran = true })
	if !ran {
		t.Fatal("callback did not run")
	}
	if buf.Len() != 0 {
		t.Errorf("non-verbose benchmark leaked output: %q", buf.String())
	}
}

func TestConcurrentBenchmarkIfVerboseAndWarn(t *testing.T) {
	buf := capture(t)
	Verbose = true

	const benchmarkCount = 3
	names := []string{"compile alpha", "compile beta", "compile gamma"}
	started := make(chan string, benchmarkCount)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBenchmarks := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseBenchmarks()
	// hung-test safety comes from go test -timeout, not from in-test timers, per the plan guardrail.
	var wg sync.WaitGroup
	wg.Add(benchmarkCount)

	for _, name := range names {
		name := name
		go func() {
			defer wg.Done()
			BenchmarkIfVerbose(name, func() {
				started <- name
				<-release
			})
		}()
	}

	for range names {
		<-started
	}

	warnDone := make(chan struct{})
	go func() {
		Warn("parallel compile in progress")
		close(warnDone)
	}()

	<-warnDone
	releaseBenchmarks()
	wg.Wait()

	got := buf.String()
	if count := strings.Count(got, "Compiler Warning: parallel compile in progress\n"); count != 1 {
		t.Fatalf("warning line count = %d, want 1; output: %q", count, got)
	}
	for _, name := range names {
		line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` \( \d+ ms \)$`)
		if !line.MatchString(got) {
			t.Fatalf("missing benchmark line for %q; output: %q", name, got)
		}
	}
	if strings.Contains(got, "Compiler Warning: parallel compile in progress (") {
		t.Fatalf("warning glued to benchmark line: %q", got)
	}
}
