package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"rotor/internal/compile"
	"rotor/internal/config"
)

// newSchemaCommand prints the canonical rotor.toml JSON Schema (config.Schema)
// to stdout. Projects no longer carry a per-project rotor.schema.json:
// rotor.toml's `#:schema` directive points at the schema hosted on raw GitHub
// (config.SchemaDirective), so editors fetch it directly. This command emits
// the schema for two purposes — refreshing the committed file that the hosted
// URL serves, and giving a project that wants a local/offline copy an easy
// way to produce one:
//
//	sloptor schema > rotor.schema.json
func newSchemaCommand(streams cliStreams) *cobra.Command {
	var rbxts bool
	cmd := &cobra.Command{
		Use:                   "schema [--rbxts]",
		Short:                 "print a JSON Schema to stdout (rotor.toml, or tsconfig \"rbxts\")",
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if rbxts {
				if writeRbxtsSchema(streams.out) != 0 {
					return reportedFailure(fmt.Errorf("schema write failed"))
				}
				return nil
			}
			if writeSchema(streams.out) != 0 {
				return reportedFailure(fmt.Errorf("schema write failed"))
			}
			return nil
		},
	}
	cmd.Flags().SortFlags = false
	addBoolFlag(cmd, &rbxts, "rbxts", "", false,
		"print the tsconfig.json \"rbxts\" extension schema instead of the rotor.toml schema")
	return cmd
}

// writeSchema writes the JSON Schema to w verbatim. Split from the command so
// the emitted bytes can be asserted in tests without capturing os.Stdout.
func writeSchema(w io.Writer) int {
	if _, err := io.WriteString(w, config.Schema); err != nil {
		fmt.Fprintf(os.Stderr, "sloptor schema: %v\n", err)
		return 1
	}
	return 0
}

func writeRbxtsSchema(w io.Writer) int {
	if _, err := io.WriteString(w, compile.RbxtsTsConfigSchema); err != nil {
		fmt.Fprintf(os.Stderr, "sloptor schema: %v\n", err)
		return 1
	}
	return 0
}
