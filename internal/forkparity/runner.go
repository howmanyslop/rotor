package forkparity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const defaultRunTimeout = time.Minute

// Runner configures fork and Rotor compiler invocations.
type Runner struct {
	ForkCLIPath  string
	RotorBinPath string
	FixtureDir   string
	OutputDir    string
	Timeout      time.Duration
}

// RunResult contains the observable result of one compiler invocation.
type RunResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	OutputTree map[string][]byte
	Error      error
}

type commandSpec struct {
	name string
	args []string
	dir  string
}

// RunFork compiles fixtureDir with the archived fork and captures outDir.
func (r Runner) RunFork(ctx context.Context, fixtureDir, outDir string) (*RunResult, error) {
	fixtureDir, outDir = r.paths(fixtureDir, outDir)
	return r.run(
		ctx,
		commandSpec{
			name: "node",
			args: []string{r.ForkCLIPath, "--project", fixtureDir},
			dir:  fixtureDir,
		},
		outDir,
	)
}

// RunRotor compiles fixtureDir with rotorBin and captures outDir.
func (r Runner) RunRotor(ctx context.Context, rotorBin, fixtureDir, outDir string) (*RunResult, error) {
	if rotorBin == "" {
		rotorBin = r.RotorBinPath
	}
	fixtureDir, outDir = r.paths(fixtureDir, outDir)
	return r.run(
		ctx,
		commandSpec{
			name: rotorBin,
			args: []string{"build", "--project", fixtureDir},
			dir:  fixtureDir,
		},
		outDir,
	)
}

func (r Runner) paths(fixtureDir, outDir string) (string, string) {
	if fixtureDir == "" {
		fixtureDir = r.FixtureDir
	}
	if outDir == "" {
		outDir = r.OutputDir
	}
	return fixtureDir, outDir
}

func (r Runner) run(ctx context.Context, spec commandSpec, outDir string) (*RunResult, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, spec.name, spec.args...)
	cmd.Dir = spec.dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	result := &RunResult{
		ExitCode:   exitCode(err),
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		OutputTree: map[string][]byte{},
		Error:      err,
	}
	tree, walkErr := collectOutputTree(outDir)
	if walkErr != nil {
		return result, fmt.Errorf("collect output tree %q: %w", outDir, walkErr)
	}
	result.OutputTree = tree
	return result, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func collectOutputTree(root string) (map[string][]byte, error) {
	tree := map[string][]byte{}
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return tree, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat output directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("output path is not a directory")
	}

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("find relative output path: %w", err)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read output file %q: %w", path, err)
		}
		tree[filepath.ToSlash(rel)] = contents
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk output directory: %w", err)
	}
	return tree, nil
}
