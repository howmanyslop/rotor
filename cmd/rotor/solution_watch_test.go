package main

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"rotor/internal/compile"
	"rotor/tsgo/fswatch"
)

func TestCompileGate(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	transcript := []string{}
	gate := newCompileGate(func() {
		transcript = append(transcript, "start")
		started <- struct{}{}
		<-release
		transcript = append(transcript, "end")
	})

	gate.Trigger()
	<-started
	gate.Trigger()
	gate.Trigger()
	release <- struct{}{}
	<-started
	release <- struct{}{}
	gate.Drain()

	if want := []string{"start", "end", "start", "end"}; !reflect.DeepEqual(transcript, want) {
		t.Fatalf("gate transcript = %v, want %v", transcript, want)
	}
}

func TestSolutionWatchRojoDirectoryAssetEvent(t *testing.T) {
	project := "/project/tsconfig.json"
	assetPath := "/project/assets/nested/icon.png"
	events := &solutionWatchEvents{
		projects: map[string]struct{}{},
		configs:  map[string]struct{}{},
		assets:   map[string]map[string]bool{},
		paths:    map[string]struct{}{},
	}
	set := compile.SolutionWatchSet{
		RootDirs:        []string{"/project/src"},
		RojoDirectories: []string{"/project/assets"},
	}

	events.add(project, []fswatch.Event{{Kind: fswatch.EventDelete, Path: assetPath}}, nil)

	if got := solutionWatchDirectories(set); !reflect.DeepEqual(got, []string{"/project/src", "/project/assets"}) {
		t.Fatalf("solutionWatchDirectories() = %v, want root and Rojo directories", got)
	}
	if deleted, ok := events.assets[project][assetPath]; !ok || !deleted {
		t.Fatalf("asset event = %v, want deleted Rojo-directory asset", events.assets)
	}
}

func TestStderrWriter(t *testing.T) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriterPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStdout, previousStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriterPipe
	t.Cleanup(func() {
		os.Stdout, os.Stderr = previousStdout, previousStderr
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriterPipe.Close()
	})

	if _, err := fmt.Fprint(stderrWriter{}, "watch error\n"); err != nil {
		t.Fatal(err)
	}
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWriterPipe.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if string(stderr) != "watch error\n" {
		t.Fatalf("stderr = %q, want watch error", stderr)
	}
}
