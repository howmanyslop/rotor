package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newCompletionCommand generates a native shell completion script for
// bash/zsh/fish/powershell using Cobra's generators. The successful action
// writes ONLY the script to stdout — no banner, status row, install attempt,
// or color — so `sloptor completion fish | source` and redirection work
// unchanged. Its custom help carries one install example per shell.
func newCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "completion [bash|zsh|fish|powershell]",
		Short:                 "generate a shell completion script",
		Args:                  cobra.ExactValidArgs(1),
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		DisableFlagsInUseLine: true,
		Example: `sloptor completion bash > /etc/bash_completion.d/rotor
	sloptor completion zsh > "${fpath[1]}/_sloptor"
	sloptor completion fish > ~/.config/fish/completions/sloptor.fish
	sloptor completion powershell | Out-String | Invoke-Expression`,
		RunE: func(cmd *cobra.Command, argv []string) error {
			shell := argv[0]
			w := cmd.OutOrStdout()
			var err error
			switch shell {
			case "bash":
				err = cmd.Root().GenBashCompletionV2(w, true)
			case "zsh":
				err = cmd.Root().GenZshCompletion(w)
			case "fish":
				err = cmd.Root().GenFishCompletion(w, true)
			case "powershell":
				err = cmd.Root().GenPowerShellCompletionWithDesc(w)
			default:
				return usageFailure("unknown shell %q (choose bash, zsh, fish, or powershell)", shell)
			}
			if err != nil {
				return runtimeFailure(fmt.Errorf("generate %s completion: %w", shell, err))
			}
			return nil
		},
	}
	return cmd
}
