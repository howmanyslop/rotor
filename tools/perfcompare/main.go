package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("perfcompare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "path to benchmark manifest JSON")
	outputPath := flags.String("output", "", "path for verdict JSON")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *inputPath == "" || *outputPath == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: perfcompare --input <manifest.json> --output <verdict.json>")
		return 1
	}

	input, err := os.ReadFile(*inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "read input: %v\n", err)
		return 1
	}
	manifest, err := loadManifest(input)
	if err != nil {
		return writeErrorVerdict(*outputPath, err, stderr)
	}
	verdict, err := evaluate(manifest)
	if err != nil {
		return writeErrorVerdict(*outputPath, err, stderr)
	}
	if err := writeVerdict(*outputPath, verdict); err != nil {
		fmt.Fprintf(stderr, "write verdict: %v\n", err)
		return 1
	}
	if verdict.Status == StatusPass {
		return 0
	}
	return 1
}

func writeErrorVerdict(outputPath string, cause error, stderr io.Writer) int {
	verdict := Verdict{
		Schema:  schemaVersion,
		Status:  StatusFail,
		Reasons: []Reason{{Code: ReasonInvalidManifest, Message: cause.Error()}},
	}
	if err := writeVerdict(outputPath, verdict); err != nil {
		fmt.Fprintf(stderr, "write error verdict: %v\n", err)
		return 1
	}
	fmt.Fprintln(stderr, cause)
	return 1
}

func writeVerdict(outputPath string, verdict Verdict) error {
	output, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return fmt.Errorf("encode verdict: %w", err)
	}
	if err := os.WriteFile(outputPath, append(output, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	return nil
}
