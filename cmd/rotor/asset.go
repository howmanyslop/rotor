package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/assets"
	"rotor/internal/cloud"
	"rotor/internal/compile"
	"rotor/internal/config"
)

// newAssetCommand is `sloptor asset <sync|list>`: an asphalt-style asset
// pipeline. `sync` scans the globs configured under `[assets]` in rotor.toml,
// uploads new/changed files via Open Cloud, records ids in rotor-lock.json,
// and regenerates the typed accessor modules (assets.luau + assets.d.ts).
// `list` prints the lockfile. `--dry-run` shows the sync plan and stops
// before any upload (no API key needed).
func newAssetCommand(streams cliStreams) *cobra.Command {
	asset := &cobra.Command{
		Use:                   "asset <sync|list> [path] [--dry-run]",
		Short:                 "upload project assets via Open Cloud (sync | list)",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageFailure("expected a subcommand (sync or list)")
			}
			return usageFailure("unknown subcommand %q (want sync or list)", args[0])
		},
	}
	var dryRun bool
	syncCmd := &cobra.Command{
		Use:                   "sync [path] [--dry-run]",
		Short:                 "scan the asset globs from rotor.toml, upload new/changed files, and regenerate typed accessors",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			dir := "."
			if len(argv) > 0 {
				dir = argv[0]
			}
			return runAssetSync(streams, dir, dryRun)
		},
	}
	syncCmd.Flags().SortFlags = false
	addBoolFlag(syncCmd, &dryRun, "dry-run", "", false,
		"print the sync plan without uploading (no API key needed)")
	listCmd := &cobra.Command{
		Use:                   "list [path]",
		Short:                 "print the lockfile (path, asset id, content hash)",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			dir := "."
			if len(argv) > 0 {
				dir = argv[0]
			}
			return runAssetList(streams, dir)
		},
	}
	asset.AddCommand(syncCmd, listCmd)
	return asset
}

// runAssetSync implements `sloptor asset sync`. A real sync buffers the serial
// upload callback results and prints final Created/Updated/Failed rows once,
// followed by generated output-file rows and the timed final row; only a
// --dry-run prints the preflight planned rows.
func runAssetSync(streams cliStreams, dir string, dryRun bool) error {
	u := newUI(streams.out)
	errUI := newUI(streams.err)
	start := time.Now()

	sub := "asset sync"
	if dryRun {
		sub += "  (dry run)"
	}
	u.banner(sub)

	cfg, err := config.Load(dir)
	if errors.Is(err, config.ErrNotFound) {
		return runtimeFailure(fmt.Errorf("no rotor.toml found in %q (asset sync needs an [assets] section)", dir))
	}
	if err != nil {
		return runtimeFailure(err)
	}
	for _, w := range cfg.Warnings {
		errUI.warn(w)
	}
	if cfg.Assets == nil || len(cfg.Assets.Paths) == 0 {
		return runtimeFailure(errors.New("rotor.toml has no [assets] section (or assets.paths is empty)"))
	}

	scan, err := assets.Scan(dir, cfg.Assets.Paths)
	if err != nil {
		return runtimeFailure(err)
	}
	lock, err := assets.LoadLockfile(dir)
	if err != nil {
		return runtimeFailure(err)
	}
	plan := assets.BuildPlan(scan, lock)

	var events []uiEvent
	for _, p := range plan.Skipped {
		events = append(events, uiEvent{Status: eventSkipped, Target: p, Detail: "(unknown extension, skipped)"})
	}
	if dryRun {
		for _, it := range plan.Items {
			switch it.Action {
			case assets.ActionCreate:
				events = append(events, uiEvent{
					Status: eventPlanned,
					Target: it.File.Path,
					Detail: fmt.Sprintf("create, %s", strings.ToLower(string(it.File.Type))),
				})
			case assets.ActionUpdate:
				events = append(events, uiEvent{
					Status: eventPlanned,
					Target: it.File.Path,
					Detail: fmt.Sprintf("update, asset %d", it.AssetID),
				})
			}
		}
		events = append(events, uiEvent{
			Status: eventFinished,
			Detail: fmt.Sprintf("dry run — nothing uploaded · %d to create · %d to update · %d unchanged · %d skipped",
				plan.Count(assets.ActionCreate), plan.Count(assets.ActionUpdate),
				plan.Count(assets.ActionUnchanged), len(plan.Skipped)),
			Elapsed: time.Since(start),
		})
		u.events(events)
		fmt.Fprintln(streams.out)
		return nil
	}

	if plan.Changes() == 0 {
		written, err := assetWriteOutputs(dir, cfg.Assets, lock)
		if err != nil {
			return runtimeFailure(err)
		}
		for _, p := range written {
			events = append(events, uiEvent{Status: eventWrote, Target: p, Detail: "(generated)"})
		}
		events = append(events, uiEvent{Status: eventFinished, Target: "everything up to date", Elapsed: time.Since(start)})
		u.events(events)
		fmt.Fprintln(streams.out)
		return nil
	}

	// Uploads ahead: validate the creator and build the cloud client.
	creator := cloud.Creator{}
	switch cfg.Assets.Creator.Type {
	case "user":
		creator.UserID = cfg.Assets.Creator.ID
	case "group":
		creator.GroupID = cfg.Assets.Creator.ID
	default:
		return runtimeFailure(fmt.Errorf("assets.creator.type must be \"user\" or \"group\" (got %q) in rotor.toml", cfg.Assets.Creator.Type))
	}
	if cfg.Assets.Creator.ID == 0 {
		return runtimeFailure(errors.New("assets.creator.id is required in rotor.toml (the user or group that owns uploaded assets)"))
	}
	client, err := cloud.FromEnv()
	if errors.Is(err, cloud.ErrNoAPIKey) {
		fmt.Fprintln(streams.err, "ROBLOX_API_KEY is not set")
		fmt.Fprintln(streams.err, "    create an Open Cloud API key with the asset read/write scopes at")
		fmt.Fprintln(streams.err, "    https://create.roblox.com/dashboard/credentials and export it as ROBLOX_API_KEY")
		return runtimeFailure(err)
	}
	if err != nil {
		return runtimeFailure(err)
	}

	var uploadEvents []uiEvent
	res, err := assets.Sync(context.Background(), client, dir, plan, lock, assets.SyncOptions{
		Creator: creator,
		OnFile: func(item assets.PlanItem, assetID int64, err error) {
			if err != nil {
				uploadEvents = append(uploadEvents, uiEvent{Status: eventFailed, Target: item.File.Path, Detail: err.Error()})
				return
			}
			status := eventCreated
			if item.Action == assets.ActionUpdate {
				status = eventUpdated
			}
			uploadEvents = append(uploadEvents, uiEvent{
				Status: status,
				Target: item.File.Path,
				Detail: fmt.Sprintf("rbxassetid://%d", assetID),
			})
		},
	})
	if err != nil {
		return runtimeFailure(err)
	}

	events = append(events, uploadEvents...)
	written, err := assetWriteOutputs(dir, cfg.Assets, lock)
	if err != nil {
		return runtimeFailure(err)
	}
	for _, p := range written {
		events = append(events, uiEvent{Status: eventWrote, Target: p, Detail: "(generated)"})
	}

	if len(res.Errors) > 0 {
		events = append(events, uiEvent{
			Status:  eventFailed,
			Target:  fmt.Sprintf("synced with %s", plural(len(res.Errors), "failure")),
			Detail:  fmt.Sprintf("%d created · %d updated", res.Created, res.Updated),
			Elapsed: time.Since(start),
		})
		u.events(events)
		fmt.Fprintln(streams.out)
		return reportedFailure(errors.New("asset sync had failures"))
	}
	events = append(events, uiEvent{
		Status:  eventFinished,
		Target:  fmt.Sprintf("synced %s", plural(res.Created+res.Updated, "asset")),
		Detail:  fmt.Sprintf("%d created · %d updated", res.Created, res.Updated),
		Elapsed: time.Since(start),
	})
	u.events(events)
	fmt.Fprintln(streams.out)
	return nil
}

// assetWriteOutputs performs the mode-aware output step of `sloptor asset sync`,
// returning the paths written. In "module" mode (default) it regenerates
// assets.luau + assets.d.ts from the lockfile; in "macro" mode it writes the
// consolidated rotor.d.ts editor companion (and no assets.luau).
func assetWriteOutputs(dir string, cfg *config.AssetsConfig, lock *assets.Lockfile) ([]string, error) {
	written, err := assets.EmitForMode(
		dir,
		assets.ParseMode(cfg.Mode),
		struct {
			Luau  string
			Types string
		}{Luau: cfg.Output.Luau, Types: cfg.Output.Types},
		assets.MacroCompanion{FileName: compile.RotorTypesFileName, Text: compile.RotorTypesFileText},
		lock,
	)
	if err != nil {
		return nil, err
	}
	return written, nil
}

// runAssetList implements `sloptor asset list`: a lockfile view.
func runAssetList(streams cliStreams, dir string) error {
	u := newUI(streams.out)
	u.banner("asset list")
	start := time.Now()
	lock, err := assets.LoadLockfile(dir)
	if err != nil {
		return runtimeFailure(err)
	}
	var events []uiEvent
	if len(lock.Assets) == 0 {
		events = append(events, uiEvent{
			Status: eventUnchanged,
			Target: "no assets in " + assets.LockfileName,
			Detail: "run `sloptor asset sync` first",
		})
	} else {
		paths := make([]string, 0, len(lock.Assets))
		for p := range lock.Assets {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			e := lock.Assets[p]
			events = append(events, uiEvent{
				Status: eventUnchanged,
				Target: p,
				Detail: fmt.Sprintf("rbxassetid://%d · %s", e.AssetID, shortHash(e.Hash)),
			})
		}
	}
	events = append(events, uiEvent{Status: eventFinished, Detail: plural(len(lock.Assets), "asset"), Elapsed: time.Since(start)})
	u.events(events)
	fmt.Fprintln(streams.out)
	return nil
}

// shortHash abbreviates "sha256:<64 hex>" for display.
func shortHash(h string) string {
	hex := strings.TrimPrefix(h, "sha256:")
	if len(hex) > 12 {
		hex = hex[:12]
	}
	return "sha256:" + hex
}
