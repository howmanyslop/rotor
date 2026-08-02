package main

import (
	"fmt"
	"io"
	"os"

	"rotor/internal/compile"
	"rotor/internal/config"
)

// cmdSchema prints the canonical rotor.toml JSON Schema (config.Schema) to
// stdout. Projects no longer carry a per-project rotor.schema.json: rotor.toml's
// `#:schema` directive points at the schema hosted on raw GitHub
// (config.SchemaDirective), so editors fetch it directly. This command emits the
// schema for two purposes — refreshing the committed file that the hosted URL
// serves, and giving a project that wants a local/offline copy an easy way to
// produce one:
//
//	rotor schema > rotor.schema.json
func cmdSchema(args []string) int {
	rbxts := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Fprintln(os.Stdout, "rotor schema — print a Rotor JSON Schema to stdout")
			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, "Default output is the rotor.toml schema. --rbxts selects the separate")
			fmt.Fprintln(os.Stdout, "tsconfig.json rbxts extension schema:")
			fmt.Fprintln(os.Stdout, "  rotor schema > rotor.schema.json")
			fmt.Fprintln(os.Stdout, "  rotor schema --rbxts > rbxts-tsconfig.schema.json")
			return 0
		case "--rbxts":
			rbxts = true
		default:
			fmt.Fprintf(os.Stderr, "rotor schema: unexpected argument %q\n", a)
			return 1
		}
	}
	if rbxts {
		return writeRbxtsSchema(os.Stdout)
	}
	return writeSchema(os.Stdout)
}

// writeSchema writes the JSON Schema to w verbatim. Split from cmdSchema so the
// emitted bytes can be asserted in tests without capturing os.Stdout.
func writeSchema(w io.Writer) int {
	if _, err := io.WriteString(w, config.Schema); err != nil {
		fmt.Fprintf(os.Stderr, "rotor schema: %v\n", err)
		return 1
	}
	return 0
}

func writeRbxtsSchema(w io.Writer) int {
	if _, err := io.WriteString(w, compile.RbxtsTsConfigSchema); err != nil {
		fmt.Fprintf(os.Stderr, "rotor schema: %v\n", err)
		return 1
	}
	return 0
}
