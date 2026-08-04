package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"rotor/internal/cloud"
	"rotor/internal/config"
	"rotor/internal/deploy"
	"rotor/internal/term"
)

// cmdDeploy is `rotor deploy <plan|apply> [path] -e <env> [--yes]
// [--allow-deletes]`: the mantle-style IaC front end over internal/deploy.
// plan diffs the config's resource graph against .rotor/deploy/<env>.json
// and prints terraform-style +/~/-/· lines without touching the network;
// apply executes the plan in dependency order against Open Cloud
// (ROBLOX_API_KEY), persisting state after every resource.
func cmdDeploy(args []string) int {
	return deployMain(args, os.Stdin, os.Stdout, os.Stderr)
}

// deployMain is cmdDeploy with injectable streams so tests can drive the
// confirmation prompt and inspect output.
func deployMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "sloptor deploy: missing subcommand (plan or apply)")
		deployUsage(stderr)
		return 1
	}
	if args[0] == "-h" || args[0] == "--help" {
		deployUsage(stdout)
		return 0
	}
	sub := args[0]
	if sub != "plan" && sub != "apply" {
		fmt.Fprintf(stderr, "sloptor deploy: unknown subcommand %q (want plan or apply)\n", sub)
		deployUsage(stderr)
		return 1
	}

	projectDir := "."
	env := ""
	yes := false
	allowDeletes := false
	haveDir := false
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-h" || a == "--help":
			deployUsage(stdout)
			return 0
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--allow-deletes":
			allowDeletes = true
		case a == "-e" || a == "--env":
			if i+1 >= len(rest) {
				fmt.Fprintf(stderr, "sloptor deploy: %s requires an environment name\n", a)
				return 1
			}
			i++
			env = rest[i]
		case strings.HasPrefix(a, "--env="):
			env = strings.TrimPrefix(a, "--env=")
		case strings.HasPrefix(a, "-e="):
			env = strings.TrimPrefix(a, "-e=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "sloptor deploy: unknown flag %q\n\n", a)
			deployUsage(stderr)
			return 1
		default:
			if haveDir {
				fmt.Fprintf(stderr, "sloptor deploy: unexpected extra argument %q\n\n", a)
				deployUsage(stderr)
				return 1
			}
			projectDir = a
			haveDir = true
		}
	}
	if env == "" {
		fmt.Fprintln(stderr, "sloptor deploy: an environment is required (-e <env>)")
		return 1
	}

	u := newUI(stdout)
	errUI := newUI(stderr)
	u.banner("deploy " + sub + "  " + env)

	// Load + validate the config, build the desired graph, load state, plan.
	// None of this needs an API key or the network.
	cfg, err := config.Load(projectDir)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			errUI.failLine(fmt.Sprintf("sloptor deploy: no rotor.toml found in %s", projectDir))
			return 1
		}
		errUI.failLine(fmt.Sprintf("sloptor deploy: %v", err))
		return 1
	}
	for _, w := range cfg.Warnings {
		errUI.warn("sloptor deploy: " + w)
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			errUI.failLine(fmt.Sprintf("sloptor deploy: config: %v", e))
		}
		return 1
	}

	resources, universeID, err := deploy.BuildResources(projectDir, cfg, env)
	if err != nil {
		errUI.failLine(fmt.Sprintf("sloptor deploy: %v", err))
		return 1
	}
	statePath := deploy.StatePath(projectDir, env)
	state, err := deploy.LoadState(statePath)
	if err != nil {
		errUI.failLine(fmt.Sprintf("sloptor deploy: %v", err))
		return 1
	}
	plan, err := deploy.BuildPlan(resources, state, deploy.PlanOptions{AllowDeletes: allowDeletes})
	if err != nil {
		errUI.failLine(fmt.Sprintf("sloptor deploy: %v", err))
		return 1
	}

	if sub == "plan" {
		printDeployPlan(stdout, plan)
		return 0
	}

	// apply
	if plan.BlockedDeletes > 0 {
		errUI.failLine(fmt.Sprintf("sloptor deploy: plan contains %s no longer in the config; re-run with --allow-deletes to remove them",
			plural(plan.BlockedDeletes, "resource")))
		return 1
	}
	if !plan.HasChanges() {
		printDeployPlan(stdout, plan)
		s := term.For(stdout)
		fmt.Fprintf(stdout, "  %s\n\n", s.Muted("nothing to apply"))
		return 0
	}
	printDeployPlan(stdout, plan)

	client, err := cloud.FromEnv()
	if err != nil {
		if errors.Is(err, cloud.ErrNoAPIKey) {
			errUI.failLine("sloptor deploy: ROBLOX_API_KEY is not set")
			fmt.Fprintln(stderr, "    create an Open Cloud API key at https://create.roblox.com/dashboard/credentials")
			fmt.Fprintln(stderr, "    (scopes: universe + place publishing, badges, asset upload)")
			return 1
		}
		errUI.failLine(fmt.Sprintf("sloptor deploy: %v", err))
		return 1
	}

	if !yes {
		s := term.For(stdout)
		fmt.Fprintf(stdout, "  %s  type the environment name to confirm: ", s.WarnBold(s.Glyphs().Warn))
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.TrimSpace(line) != env {
			errUI.failLine("sloptor deploy: confirmation did not match; aborted (use --yes to skip)")
			return 1
		}
	}

	start := time.Now()
	s := term.For(stdout)
	result, err := deploy.Apply(context.Background(), plan, deploy.ApplyOptions{
		Providers:  deploy.DefaultProviders(),
		Cloud:      client,
		UniverseID: universeID,
		ProjectDir: projectDir,
		State:      state,
		SaveState:  func(st *deploy.State) error { return st.Save(statePath) },
		OnStep:     func(r deploy.StepResult) { printDeployStep(stdout, s, r) },
	})
	if err != nil {
		errUI.failLine(fmt.Sprintf("sloptor deploy: %v", err))
		return 1
	}
	printDeploySummary(stdout, s, result, time.Since(start))
	if result.Failed > 0 {
		return 1
	}
	return 0
}

// printDeployPlan renders the terraform-style plan listing (the banner above
// it is printed by deployMain; the +/~/-/· row colors are deliberate
// terraform idiom and stay as-is).
func printDeployPlan(w io.Writer, plan *deploy.Plan) {
	s := term.For(w)
	for _, step := range plan.Steps {
		key := step.Ref.Key()
		switch step.Op {
		case deploy.OpCreate:
			fmt.Fprintf(w, "  %s %-7s %s\n", s.Green("+"), "create", key)
		case deploy.OpUpdate:
			fmt.Fprintf(w, "  %s %-7s %s\n", s.Yellow("~"), "update", key)
		case deploy.OpDelete:
			fmt.Fprintf(w, "  %s %-7s %s\n", s.Red("-"), "delete", key)
		case deploy.OpBlockedDelete:
			fmt.Fprintf(w, "  %s %-7s %s %s\n", s.Red("-"), "delete", key,
				s.Muted("(blocked: pass --allow-deletes)"))
		case deploy.OpNoop:
			fmt.Fprintf(w, "  %s %-7s %s\n", s.Muted("·"), "no-op", s.Muted(key))
		}
	}
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
	fmt.Fprintf(w, "\n  %s\n\n", s.Bold(line))
}

// printDeployStep renders one apply progress line.
func printDeployStep(w io.Writer, s *term.Styler, r deploy.StepResult) {
	key := r.Step.Ref.Key()
	switch r.Status {
	case deploy.StatusApplied:
		verb := map[deploy.Op]string{
			deploy.OpCreate: "created",
			deploy.OpUpdate: "updated",
			deploy.OpDelete: "deleted",
		}[r.Step.Op]
		fmt.Fprintf(w, "  %s  %s %s\n", s.SuccessBold(s.Glyphs().Check), key, s.Muted(verb))
	case deploy.StatusUnchanged:
		fmt.Fprintf(w, "  %s  %s\n", s.Muted("·"), s.Muted(key+" unchanged"))
	case deploy.StatusFailed:
		fmt.Fprintf(w, "  %s  %s %s\n", s.ErrorBold(s.Glyphs().Cross), key, s.Error(fmt.Sprintf("failed: %v", r.Err)))
	case deploy.StatusSkipped:
		fmt.Fprintf(w, "  %s  %s %s\n", s.WarnBold(s.Glyphs().Warn), key, s.Muted(fmt.Sprintf("skipped (%v)", r.Err)))
	}
}

// printDeploySummary renders the closing tally in the house ok/x shape.
func printDeploySummary(w io.Writer, s *term.Styler, r *deploy.ApplyResult, elapsed time.Duration) {
	line := fmt.Sprintf("Applied: %d created, %d updated, %d deleted, %d unchanged",
		r.Created, r.Updated, r.Deleted, r.Unchanged)
	suffix := s.Muted(fmt.Sprintf("in %d ms", elapsed.Milliseconds()))
	if r.Failed > 0 || r.Skipped > 0 {
		line += fmt.Sprintf(", %d failed, %d skipped", r.Failed, r.Skipped)
		fmt.Fprintf(w, "\n  %s  %s %s\n\n", s.ErrorBold(s.Glyphs().Cross), s.Bold(line), suffix)
		return
	}
	fmt.Fprintf(w, "\n  %s  %s %s\n\n", s.SuccessBold(s.Glyphs().Check), s.Bold(line), suffix)
}

func deployUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: sloptor deploy <plan|apply> [path] -e <env> [--yes] [--allow-deletes]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  plan             show what apply would do (no network, no API key needed)")
	fmt.Fprintln(w, "  apply            execute the plan against Open Cloud (needs ROBLOX_API_KEY)")
	fmt.Fprintln(w, "  path             project directory containing rotor.toml (default \".\")")
	fmt.Fprintln(w, "  -e, --env <env>  deploy environment from rotor.toml (required)")
	fmt.Fprintln(w, "  --yes            skip the type-the-environment-name confirmation prompt")
	fmt.Fprintln(w, "  --allow-deletes  permit removing resources that left the config")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "State lives in .rotor/deploy/<env>.json and updates after every applied resource.")
}
