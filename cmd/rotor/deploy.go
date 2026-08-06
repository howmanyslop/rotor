package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/cloud"
	"rotor/internal/config"
	"rotor/internal/deploy"
	"rotor/internal/term"
)

// newDeployCommand is `sloptor deploy <plan|apply> [path] -e <env> [--yes]
// [--allow-deletes]`: the mantle-style IaC front end over internal/deploy.
// plan diffs the config's resource graph against .rotor/deploy/<env>.json
// and prints planned event rows without touching the network; apply executes
// the plan in dependency order against Open Cloud (ROBLOX_API_KEY),
// persisting state after every resource.
func newDeployCommand(streams cliStreams) *cobra.Command {
	deployCmd := &cobra.Command{
		Use:                   "deploy <plan|apply> [path] -e <env> [--yes] [--allow-deletes]",
		Short:                 "declarative Open Cloud deployment from rotor.toml (plan | apply)",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageFailure("expected a subcommand (plan or apply)")
			}
			return usageFailure("unknown subcommand %q (want plan or apply)", args[0])
		},
	}
	for _, sub := range []string{"plan", "apply"} {
		child := newDeploySubcommand(streams, sub)
		deployCmd.AddCommand(child)
	}
	return deployCmd
}

// deployFlags is the shared plan/apply flag surface.
type deployFlags struct {
	projectDir   string
	env          string
	yes          bool
	allowDeletes bool
}

func newDeploySubcommand(streams cliStreams, sub string) *cobra.Command {
	var flags deployFlags
	short := "show what apply would do (no network, no API key needed)"
	if sub == "apply" {
		short = "execute the plan against Open Cloud (needs ROBLOX_API_KEY)"
	}
	cmd := &cobra.Command{
		Use:                   sub + " [path] -e <env> [--yes] [--allow-deletes]",
		Short:                 short,
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			flags.projectDir = "."
			if len(argv) > 0 {
				flags.projectDir = argv[0]
			}
			if flags.env == "" {
				return usageFailure("an environment is required (-e <env>)")
			}
			return runDeployCommand(streams, sub, &flags)
		},
	}
	cmd.Flags().SortFlags = false
	addStringFlag(cmd, &flags.env, "env", "e", "", "<env>", "deploy environment from rotor.toml (required)")
	addBoolFlag(cmd, &flags.yes, "yes", "y", false, "skip the type-the-environment-name confirmation prompt")
	addBoolFlag(cmd, &flags.allowDeletes, "allow-deletes", "", false, "permit removing resources that left the config")
	return cmd
}

func runDeployCommand(streams cliStreams, sub string, flags *deployFlags) error {
	u := newUI(streams.out)
	errUI := newUI(streams.err)
	u.banner("deploy " + sub + "  " + flags.env)

	// Load + validate the config, build the desired graph, load state, plan.
	// None of this needs an API key or the network.
	cfg, err := config.Load(flags.projectDir)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return runtimeFailure(fmt.Errorf("no rotor.toml found in %s", flags.projectDir))
		}
		return runtimeFailure(err)
	}
	for _, w := range cfg.Warnings {
		errUI.warn(w)
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		var b strings.Builder
		for _, e := range errs {
			b.WriteString("config: " + e.Error() + "\n")
		}
		return runtimeFailure(errors.New(strings.TrimSuffix(b.String(), "\n")))
	}

	resources, universeID, err := deploy.BuildResources(flags.projectDir, cfg, flags.env)
	if err != nil {
		return runtimeFailure(err)
	}
	statePath := deploy.StatePath(flags.projectDir, flags.env)
	state, err := deploy.LoadState(statePath)
	if err != nil {
		return runtimeFailure(err)
	}
	plan, err := deploy.BuildPlan(resources, state, deploy.PlanOptions{AllowDeletes: flags.allowDeletes})
	if err != nil {
		return runtimeFailure(err)
	}

	if sub == "plan" {
		printDeployPlan(streams.out, plan)
		return nil
	}

	// apply
	if plan.BlockedDeletes > 0 {
		return runtimeFailure(fmt.Errorf("plan contains %s no longer in the config; re-run with --allow-deletes to remove them",
			plural(plan.BlockedDeletes, "resource")))
	}
	if !plan.HasChanges() {
		printDeployPlan(streams.out, plan)
		s := termFor(streams.out)
		fmt.Fprintf(streams.out, "  %s\n\n", s.Muted("nothing to apply"))
		return nil
	}
	printDeployPlan(streams.out, plan)

	client, err := cloud.FromEnv()
	if err != nil {
		if errors.Is(err, cloud.ErrNoAPIKey) {
			fmt.Fprintln(streams.err, "ROBLOX_API_KEY is not set")
			fmt.Fprintln(streams.err, "    create an Open Cloud API key at https://create.roblox.com/dashboard/credentials")
			fmt.Fprintln(streams.err, "    (scopes: universe + place publishing, badges, asset upload)")
			return runtimeFailure(err)
		}
		return runtimeFailure(err)
	}

	if !flags.yes {
		s := termFor(streams.out)
		fmt.Fprintf(streams.out, "  %s  type the environment name to confirm: ", s.WarnBold(s.Glyphs().Warn))
		line, _ := bufio.NewReader(streams.in).ReadString('\n')
		if strings.TrimSpace(line) != flags.env {
			return runtimeFailure(errors.New("confirmation did not match; aborted (use --yes to skip)"))
		}
	}

	start := time.Now()
	result, err := deploy.Apply(context.Background(), plan, deploy.ApplyOptions{
		Providers:  deploy.DefaultProviders(),
		Cloud:      client,
		UniverseID: universeID,
		ProjectDir: flags.projectDir,
		State:      state,
		SaveState:  func(st *deploy.State) error { return st.Save(statePath) },
		OnStep:     func(r deploy.StepResult) { deployStepEvent(streams.out, r) },
	})
	if err != nil {
		return runtimeFailure(err)
	}
	printDeploySummary(streams.out, result, time.Since(start))
	if result.Failed > 0 {
		return reportedFailure(errors.New("deploy had failures"))
	}
	return nil
}

// termFor returns a Styler for a writer, used by the deploy rendering paths
// that bypass the ui row helpers.
func termFor(w io.Writer) *term.Styler { return term.For(w) }

// printDeployPlan renders the plan as aligned event rows (Planned / Unchanged)
// plus the terraform-style "Plan:" tally line.
func printDeployPlan(w io.Writer, plan *deploy.Plan) {
	var events []uiEvent
	for _, step := range plan.Steps {
		key := step.Ref.Key()
		switch step.Op {
		case deploy.OpCreate:
			events = append(events, uiEvent{Status: eventPlanned, Target: key, Detail: "create"})
		case deploy.OpUpdate:
			events = append(events, uiEvent{Status: eventPlanned, Target: key, Detail: "update"})
		case deploy.OpDelete:
			events = append(events, uiEvent{Status: eventPlanned, Target: key, Detail: "delete"})
		case deploy.OpBlockedDelete:
			events = append(events, uiEvent{Status: eventPlanned, Target: key, Detail: "delete (blocked: pass --allow-deletes)"})
		case deploy.OpNoop:
			events = append(events, uiEvent{Status: eventUnchanged, Target: key, Detail: "no-op"})
		}
	}
	newUI(w).events(events)
	parts := []string{
		fmt.Sprintf("%d to create", plan.Creates),
		fmt.Sprintf("%d to update", plan.Updates),
		fmt.Sprintf("%d to delete", plan.Deletes),
		fmt.Sprintf("%d unchanged", plan.Noops),
	}
	line := "Plan: " + strings.Join(parts, ", ")
	if plan.BlockedDeletes > 0 {
		line += fmt.Sprintf(", %d delete(s) blocked", plan.BlockedDeletes)
	}
	s := termFor(w)
	fmt.Fprintf(w, "\n  %s\n\n", s.Bold(line))
}

// deployStepEvent maps one apply step result to an event row.
func deployStepEvent(w io.Writer, r deploy.StepResult) {
	key := r.Step.Ref.Key()
	var event uiEvent
	switch r.Status {
	case deploy.StatusApplied:
		switch r.Step.Op {
		case deploy.OpCreate:
			event = uiEvent{Status: eventCreated, Target: key}
		case deploy.OpUpdate:
			event = uiEvent{Status: eventUpdated, Target: key}
		case deploy.OpDelete:
			event = uiEvent{Status: eventRemoved, Target: key}
		default:
			event = uiEvent{Status: eventUpdated, Target: key}
		}
	case deploy.StatusUnchanged:
		event = uiEvent{Status: eventUnchanged, Target: key}
	case deploy.StatusFailed:
		event = uiEvent{Status: eventFailed, Target: key, Detail: fmt.Sprintf("failed: %v", r.Err)}
	case deploy.StatusSkipped:
		event = uiEvent{Status: eventSkipped, Target: key, Detail: fmt.Sprintf("skipped (%v)", r.Err)}
	default:
		event = uiEvent{Status: eventUnchanged, Target: key}
	}
	newUI(w).events([]uiEvent{event})
}

// printDeploySummary renders the closing tally as the final event row.
func printDeploySummary(w io.Writer, r *deploy.ApplyResult, elapsed time.Duration) {
	line := fmt.Sprintf("Applied: %d created, %d updated, %d deleted, %d unchanged",
		r.Created, r.Updated, r.Deleted, r.Unchanged)
	status := eventFinished
	if r.Failed > 0 || r.Skipped > 0 {
		line += fmt.Sprintf(", %d failed, %d skipped", r.Failed, r.Skipped)
		status = eventFailed
	}
	newUI(w).events([]uiEvent{{Status: status, Target: line, Elapsed: elapsed}})
	fmt.Fprintln(w)
}
