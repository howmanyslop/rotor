// Command rotor is the rotor CLI.
//
// `rotor check` is a fast native TypeScript project checker; `rotor build`
// compiles to Luau with the full rbxtsc build flag surface (ProjectOptions
// merged from defaults < tsconfig `rbxts` key < argv). Build watch mode is
// available; incremental rebuild selection remains later Phase 4 work.
//
// Exit-code policy: 0 = success, 1 = ANY failure including usage errors —
// matching upstream rbxtsc, whose yargs `.fail` handler sets exit code 1
// (CLI/cli.ts L30-35). rotor previously exited 2 for usage errors; that
// divergence was removed in Phase 4 for drop-in parity.
package main

import (
	"fmt"
	"io"
	"os"

	rotorversion "rotor/internal/version"
)

const banner = "sloptor — an all-in-one Roblox toolchain (rbxtsc-parity compiler, Luau tools, assets, deploy)"

// version is rotor's own release version, used for `--version` and the
// `rotor build` emit header (`-- Compiled with rotor v...`). Library/test
// compilation keeps the upstream rbxtsc header so differential
// byte-comparison stays strict. The value is defined in code
// (internal/version) — no ldflags injection; kept as a var so tests can
// override it.
var version = rotorversion.Version

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	applyRuntimePolicy()
	if len(args) == 0 {
		usage(os.Stderr)
		return 1
	}
	switch args[0] {
	case "check":
		return cmdCheck(args[1:])
	case "build":
		return cmdBuild(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "minify":
		return cmdMinify(args[1:])
	case "bundle":
		return cmdBundle(args[1:])
	case "dev":
		return cmdDev(args[1:])
	case "pack":
		return cmdPack(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "migrate":
		return cmdMigrate(args[1:])
	case "schema":
		return cmdSchema(args[1:])
	case "sourcemap":
		return cmdSourcemap(args[1:])
	case "asset":
		return cmdAsset(args[1:])
	case "deploy":
		return cmdDeploy(args[1:])
	case "clean":
		return cmdClean(args[1:])
	case "add":
		return cmdAdd(args[1:])
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	case "version", "-v", "--version":
		// Upstream prints the bare COMPILER_VERSION (CLI/cli.ts L14); rotor
		// prints its OWN version, not rbxtsc's.
		fmt.Println(version)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "sloptor: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		return 1
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, banner)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  sloptor check [path] [-w]      typecheck the project (native, full strictness)")
	fmt.Fprintln(w, "  sloptor build [options] [path] compile the project to Luau (writes to tsconfig outDir")
	fmt.Fprintln(w, "                               and copies the runtime library to the include folder)")
	fmt.Fprintln(w, "  sloptor doctor [path]          diagnose the project setup (tsconfig, @rbxts packages,")
	fmt.Fprintln(w, "                               Node.js + transformer plugins, Rojo wiring)")
	fmt.Fprintln(w, "  sloptor minify <file> [-o out] minify a Luau file (strips comments + whitespace,")
	fmt.Fprintln(w, "                               keeps --! directives; writes to stdout without -o)")
	fmt.Fprintln(w, "  sloptor bundle <entry> [-o out] [--minify]")
	fmt.Fprintln(w, "                               inline a Luau require graph into one runnable file")
	fmt.Fprintln(w, "  sloptor dev [path] [--no-serve] watch + incrementally compile, and serve to Studio")
	fmt.Fprintln(w, "                               via `rojo serve` (the dev inner loop)")
	fmt.Fprintln(w, "  sloptor pack [path] [--as luau|rbxmx|rbxm] [-o out] [--entry inst.path] [--rojo-tree]")
	fmt.Fprintln(w, "                               package a Rojo project into one self-reconstructing")
	fmt.Fprintln(w, "                               Luau script (native, no rojo needed for script trees;")
	fmt.Fprintln(w, "                               --rojo-tree forces rojo), or a Roblox model file")
	fmt.Fprintln(w, "  sloptor init [dir] [--template game|package|plain]")
	fmt.Fprintln(w, "                               scaffold a new project (rbxts game by default; package")
	fmt.Fprintln(w, "                               library, or plain Luau) with tsconfig, Rojo project,")
	fmt.Fprintln(w, "                               rotor.toml, rotor.d.ts, and starter src (interactive in a tty)")
	fmt.Fprintln(w, "  sloptor migrate [path] [--force]")
	fmt.Fprintln(w, "                               convert a legacy rotor.config.ts to rotor.toml")
	fmt.Fprintln(w, "  sloptor schema [--rbxts]       print a JSON Schema to stdout: rotor.toml by default,")
	fmt.Fprintln(w, "                               or the tsconfig.json \"rbxts\" extension schema with")
	fmt.Fprintln(w, "                               --rbxts; redirect to a file for a local/offline copy")
	fmt.Fprintln(w, "  sloptor clean [path] [--types] [--dry-run]")
	fmt.Fprintln(w, "                               remove build outputs (outDir, include); --types also")
	fmt.Fprintln(w, "                               removes generated rotor-env.d.ts / rotor-asset.d.ts")
	fmt.Fprintln(w, "  sloptor add [--dev] <pkg>...    add @rbxts/* (or any) deps to package.json")
	fmt.Fprintln(w, "  sloptor sourcemap [path] [-o out.json]")
	fmt.Fprintln(w, "                               emit a Rojo-compatible sourcemap.json for luau-lsp")
	fmt.Fprintln(w, "                               (native for script trees, no rojo needed; falls back")
	fmt.Fprintln(w, "                               to `rojo sourcemap` otherwise; stdout without -o)")
	fmt.Fprintln(w, "  sloptor asset <sync|list> [path] [--dry-run]")
	fmt.Fprintln(w, "                               upload project assets via Open Cloud (sync: scan the")
	fmt.Fprintln(w, "                               assets globs from rotor.toml, upload new/changed,")
	fmt.Fprintln(w, "                               write rotor-lock.json + typed assets.luau/.d.ts;")
	fmt.Fprintln(w, "                               list: show the lockfile; needs ROBLOX_API_KEY)")
	fmt.Fprintln(w, "  sloptor deploy <plan|apply> [path] -e <env> [--yes] [--allow-deletes]")
	fmt.Fprintln(w, "                               declarative Open Cloud deployment from rotor.toml:")
	fmt.Fprintln(w, "                               plan diffs config vs .rotor/deploy/<env>.json state")
	fmt.Fprintln(w, "                               (no network); apply publishes places, universe")
	fmt.Fprintln(w, "                               settings, badges + icons (needs ROBLOX_API_KEY)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Build options (rbxtsc-compatible; booleans accept --flag, --flag=false, --no-flag):")
	fmt.Fprintln(w, "  -p, --project <path>      project path (default \".\"): a tsconfig file, a directory")
	fmt.Fprintln(w, "                            containing one, or any path to search upward from")
	fmt.Fprintln(w, "  -b, --build [path]        build project references (optionally select a tsconfig path)")
	fmt.Fprintln(w, "  --builders <n>             number of projects to build concurrently (default 4; only with --build)")
	fmt.Fprintln(w, "  --checkers <n>             number of checkers per project (default 4; build and check)")
	fmt.Fprintln(w, "  --emitDeclarationOnly     only emit declaration files for a solution build (requires --build)")
	fmt.Fprintln(w, "  -w, --watch               enable watch mode")
	fmt.Fprintln(w, "  --usePolling              use polling for watch mode (requires --watch)")
	fmt.Fprintln(w, "  --verbose                 enable verbose logs")
	fmt.Fprintln(w, "  --noInclude               do not copy include files")
	fmt.Fprintln(w, "  --logTruthyChanges        logs changes to truthiness evaluation from Lua truthiness rules")
	fmt.Fprintln(w, "  --writeOnlyChanged        skip rewriting output files whose contents are unchanged")
	fmt.Fprintln(w, "  --writeTransformedFiles   not supported by sloptor (parsed and ignored)")
	fmt.Fprintln(w, "  --optimizedLoops          numeric-for loop optimization (default true)")
	fmt.Fprintln(w, "  --type <kind>             override project type (choices: game, model, package)")
	fmt.Fprintln(w, "  -i, --includePath <dir>   folder to copy runtime files to (default <project>/include)")
	fmt.Fprintln(w, "  --rojo <path>             manually select Rojo project file")
	fmt.Fprintln(w, "  --allowCommentDirectives  allow @ts-ignore et al.")
	fmt.Fprintln(w, "  --luau                    emit files with .luau extension (default true; --luau=false emits .lua)")
	fmt.Fprintln(w, "  --cpuprofile <path>       write a pprof CPU profile of the build (diagnostics)")
	fmt.Fprintln(w, "  --trace-out <path>        write a Go execution trace of the build (diagnostics)")
	fmt.Fprintln(w, "  --blockprofile <path>     write a blocking profile sampled at 1 ms (diagnostics)")
	fmt.Fprintln(w, "  --mutexprofile <path>     write a mutex contention profile at fraction 5 (diagnostics)")
	fmt.Fprintln(w, "  --heapprofile <path>      write a heap profile after the build (diagnostics)")
	fmt.Fprintln(w, "  --timings <path>          write aggregate one-shot build timings as JSON (not with --watch)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Diagnostics & watch DX (sloptor extensions):")
	fmt.Fprintln(w, "  --minify                  minify emitted Luau (strip comments/whitespace, t[\"x\"] -> t.x)")
	fmt.Fprintln(w, "  --max-errors <n>          cap the rendered code frames on failure (default 50; 0 = all)")
	fmt.Fprintln(w, "  --json                    emit one machine-readable result object instead of styled output")
	fmt.Fprintln(w, "  --bell                    ring the terminal bell on a watch fail<->pass transition")
	fmt.Fprintln(w, "  --no-clear                keep scroll history instead of clearing the screen each rebuild")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Generated build state:")
	fmt.Fprintln(w, "  rbxts.copyfiles.json       copy-files gate manifest in outDir")
	fmt.Fprintln(w, "  rbxts.rojocache.json       fork-compatible Rojo cache name protected from cleanup")
	fmt.Fprintln(w, "  .rotor/cache/rojo/          Sloptor's hashed Rojo resolver cache directory")
	fmt.Fprintln(w, "  *.luau.map                 Luau source maps when compilerOptions.sourceMap is true")
	fmt.Fprintln(w, "  *.rbxtsc.tsbuildinfo       Sloptor incremental build state (the .rbxtsc infix is intentional)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment:")
	fmt.Fprintln(w, "  RBXTSC_WRITE_CONCURRENCY   override output-write workers (maximum 256)")
	fmt.Fprintln(w, "  ROTOR_WRITE_WORKERS        Sloptor-specific output-write worker override")
	fmt.Fprintln(w, "  UV_THREADPOOL_SIZE         configure the Node sidecar libuv pool (not Go output writers)")
	fmt.Fprintln(w, "  GOGC                       Go GC target percentage (default 400 when unset)")
	fmt.Fprintln(w, "  GOMEMLIMIT                 Go memory limit (default 75% of effective memory when unset)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Color: auto-detected for terminals; NO_COLOR disables, FORCE_COLOR forces.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options may also be set under the top-level \"rbxts\" key of tsconfig.json;")
	fmt.Fprintln(w, "merge order: defaults < rbxts < command line.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Other:")
	fmt.Fprintln(w, "  -h, --help                show this help")
	fmt.Fprintln(w, "  -v, --version             print sloptor's version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Exit codes: 0 = success, 1 = any failure (compile errors, config or usage errors — rbxtsc parity)")
}
