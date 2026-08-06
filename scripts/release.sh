#!/usr/bin/env bash
# Interactive release helper for rotor (Bash port of scripts/release.fish).
#
# Maintainer flow (see CONTRIBUTING.md):
#   1. Bump internal/version/version.go + package.json in lockstep
#   2. Commit
#   3. Tag vX.Y.Z (must match the Version constant)
#   4. Push the tag → release.yml
#
# Usage:
#   ./scripts/release.sh                 # interactive
#   ./scripts/release.sh --help
#   ./scripts/release.sh --dry-run
#   ./scripts/release.sh --bump fork --yes --no-push
#   ./scripts/release.sh --version 2.3.0 --yes
#   ./scripts/release.sh --tag-only --yes
#   ./scripts/release.sh --snapshot
#   ./scripts/release.sh --build --os windows --arch amd64,arm64
#   ./scripts/release.sh --build --os windows --compress 7z
#
# Requires: git, bash, go (for --build). Optional: gum (nicer prompts),
# goreleaser (full-matrix --snapshot only), 7z/zpaq (turbo compress).
# UPX is intentionally not supported.
#
# Drop-in equivalent of scripts/release.fish — keep both in sync when
# changing behavior. Works on bash 3.2+ (macOS stock bash included).

set -euo pipefail

SCRIPT_NAME=$(basename "$0")
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "error: not inside a git repository" >&2
    exit 1
}

VERSION_GO="$REPO_ROOT/internal/version/version.go"
PACKAGE_JSON="$REPO_ROOT/package.json"

# Size-oriented link flags. -s -w drop symbol/DWARF tables; -buildid= makes
# builds reproducible and sheds a few KB. Cannot get a tsgo-linked rotor
# under ~10MiB by flags alone — use --compress 7z for archives.
ROTOR_SIZE_LDFLAGS="-s -w -buildid="

FLAG_HELP=0
FLAG_YES=0
FLAG_DRY_RUN=0
FLAG_NO_PUSH=0
FLAG_NO_COMMIT=0
FLAG_TAG_ONLY=0
FLAG_SNAPSHOT=0
FLAG_BUILD=0
FLAG_SKIP_CHECKS=0
OPT_BUMP=""
OPT_VERSION=""
OPT_REMOTE=origin
OPT_MESSAGE=""
OPT_OS=""
OPT_ARCH=""
# compress: "" | skip | 7z | zpaq
OPT_COMPRESS=""
# Soft target for packed artifacts (MiB). Default 10.
OPT_MAX_MIB=10

usage() {
    cat <<EOF
Usage: $SCRIPT_NAME [options]

Interactive (default) or flag-driven release cutter for rotor.

Options:
  --bump KIND       patch | minor | major | fork | custom
  --version VER     exact version (no leading v); implies custom bump
  --remote NAME     git remote to push (default: origin)
  --message TEXT    commit message (default: chore(release): prepare vX.Y.Z)
  --yes             skip confirmation prompts
  --dry-run         print actions; write nothing; push nothing
  --no-push         do not push commit/tag
  --no-commit       bump files only (no commit/tag/push)
  --tag-only        versions already bumped; create+push tag only
  --snapshot        full-matrix local goreleaser snapshot (no tag)
  --build           local go build into dist/ (pick OS/arch; no tag)
  --os LIST         comma list: windows,linux,darwin (with --build)
  --arch LIST       comma list: amd64,arm64 (with --build)
  --compress MODE   after build: skip | 7z | zpaq  (UPX not allowed)
                    7z  = universal archive; auto-splits into <target volumes
                    zpaq = smallest single file (~9.9MiB), needs zpaq to extract
  --max-mib N       size budget for packed artifacts (default: 10)
  --skip-checks     skip dirty-tree / remote-tag probes
  -h, --help        show this help

Examples:
  $SCRIPT_NAME
  $SCRIPT_NAME --bump fork --yes
  $SCRIPT_NAME --version 2.3.0-fork.1 --dry-run
  $SCRIPT_NAME --tag-only --yes --remote upstream
  $SCRIPT_NAME --snapshot
  $SCRIPT_NAME --build --os windows --arch amd64
  $SCRIPT_NAME --build --os windows --compress 7z --max-mib 10
EOF
}

has_gum() {
    command -v gum >/dev/null 2>&1
}

info() {
    # Always stderr so callers can capture pure data on stdout.
    if has_gum; then
        gum style --foreground 212 -- "$*" >&2
    else
        echo "→ $*" >&2
    fi
}

warn() {
    if has_gum; then
        gum style --foreground 214 -- "warning: $*" >&2
    else
        echo "warning: $*" >&2
    fi
}

die() {
    if has_gum; then
        gum style --foreground 196 --bold -- "error: $*" >&2
    else
        echo "error: $*" >&2
    fi
    exit 1
}

confirm() {
    # $1 prompt, $2 default_yes (1 or 0)
    local prompt="$1" default_yes="$2"
    if [[ "$FLAG_YES" == 1 ]]; then
        return 0
    fi
    if has_gum; then
        if [[ "$default_yes" == 1 ]]; then
            gum confirm --default=true -- "$prompt"
        else
            gum confirm --default=false -- "$prompt"
        fi
        return $?
    fi
    local suffix="[y/N]"
    if [[ "$default_yes" == 1 ]]; then
        suffix="[Y/n]"
    fi
    local answer
    read -r -p "$prompt $suffix " answer || return 1
    answer=$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')
    if [[ -z "$answer" ]]; then
        [[ "$default_yes" == 1 ]]
        return $?
    fi
    [[ "$answer" =~ ^(y|yes)$ ]]
}

choose() {
    # $1 header; remaining args are options. An option may carry a
    # tab-delimited description ("label\tdescription") so the menu can
    # preview each choice's consequences; only the label is echoed back.
    local header="$1"
    shift
    local -a options=("$@")
    local picked
    if has_gum; then
        picked=$(printf '%s\n' "${options[@]}" | gum choose --header "$header") || return 1
    elif command -v fzf >/dev/null 2>&1; then
        # fzf can't render tab-delimited descriptions; show labels only.
        picked=$(printf '%s\n' "${options[@]}" | awk -F '\t' '{print $1}' | fzf --prompt "$header > " --height 12 --reverse) || return 1
    else
        echo "$header" >&2
        local i=1 opt label desc pick
        for opt in "${options[@]}"; do
            label="${opt%%$'\t'*}"
            printf '  %s) %s\n' "$i" "$label" >&2
            if [[ "$opt" == *$'\t'* ]]; then
                desc="${opt#*$'\t'}"
                printf '      %s\n' "$desc" >&2
            fi
            ((i += 1))
        done
        read -r -p "Choice [1-${#options[@]}]: " pick || return 1
        if [[ ! "$pick" =~ ^[0-9]+$ ]]; then
            return 1
        fi
        if (( pick < 1 || pick > ${#options[@]} )); then
            return 1
        fi
        picked="${options[$((pick - 1))]}"
    fi
    # Strip the description (gum drops it on selection; fzf/plain keep it).
    echo "${picked%%$'\t'*}"
}

choose_multi() {
    # Multi-select. $1 header; remaining args are options. Prints one per line.
    local header="$1"
    shift
    local -a options=("$@")
    if has_gum; then
        printf '%s\n' "${options[@]}" | gum choose --no-limit --header "$header (space to toggle, enter to confirm)"
        return $?
    fi
    if command -v fzf >/dev/null 2>&1; then
        printf '%s\n' "${options[@]}" | fzf --multi --prompt "$header > " --height 12 --reverse
        return $?
    fi
    echo "$header (comma-separated indexes, e.g. 1,3)" >&2
    local i=1 opt pick
    for opt in "${options[@]}"; do
        printf '  %s) %s\n' "$i" "$opt" >&2
        ((i += 1))
    done
    read -r -p "Choices: " pick || return 1
    local -a picked=()
    local part
    while IFS= read -r part; do
        part=$(printf '%s' "$part" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        if [[ ! "$part" =~ ^[0-9]+$ ]]; then
            return 1
        fi
        if (( part < 1 || part > ${#options[@]} )); then
            return 1
        fi
        picked+=("${options[$((part - 1))]}")
    # %s\n: tr ',' '\n' leaves no trailing newline, and bash read returns 1
    # on EOF-without-newline — the final field would be dropped by the loop.
    done < <(printf '%s\n' "$pick" | tr ',' '\n')
    if (( ${#picked[@]} == 0 )); then
        return 1
    fi
    printf '%s\n' "${picked[@]}"
}

ask_text() {
    local prompt="$1" placeholder="$2"
    if has_gum; then
        if [[ -n "$placeholder" ]]; then
            gum input --placeholder "$placeholder" --prompt "$prompt "
        else
            gum input --prompt "$prompt "
        fi
        return $?
    fi
    local value
    if [[ -n "$placeholder" ]]; then
        read -r -p "$prompt [$placeholder]: " value || return 1
        if [[ -z "$value" ]]; then
            echo "$placeholder"
            return 0
        fi
        echo "$value"
        return 0
    fi
    read -r -p "$prompt: " value || return 1
    echo "$value"
}

read_code_version() {
    local v
    v=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$VERSION_GO" 2>/dev/null || true)
    [[ -n "$v" ]] || die "could not read Version from internal/version/version.go"
    echo "$v"
}

read_pkg_version() {
    local v
    v=$(node -p "require('./package.json').version" 2>/dev/null || true)
    if [[ -z "$v" ]]; then
        v=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$PACKAGE_JSON" | head -n1 || true)
    fi
    [[ -n "$v" ]] || die "could not read version from package.json"
    echo "$v"
}

validate_version() {
    # SemVer-ish: MAJOR.MINOR.PATCH with optional -prerelease (dots/hyphens ok).
    [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]
}

parse_core() {
    echo "${1%%-*}"
}

bump_version() {
    local kind="$1" current="$2"
    local core parts
    core=$(parse_core "$current")
    IFS='.' read -r -a parts <<< "$core"
    if (( ${#parts[@]} != 3 )); then
        die "cannot parse core version from '$current'"
    fi
    local major="${parts[0]}" minor="${parts[1]}" patch="${parts[2]}"
    case "$kind" in
        major)
            echo "$(( major + 1 )).0.0"
            ;;
        minor)
            echo "$major.$(( minor + 1 )).0"
            ;;
        patch)
            echo "$major.$minor.$(( patch + 1 ))"
            ;;
        fork)
            if [[ "$current" =~ -fork\.([0-9]+)$ ]]; then
                local n="${BASH_REMATCH[1]}"
                echo "${core}-fork.$(( n + 1 ))"
            elif [[ "$current" == *-fork ]]; then
                echo "${current}.1"
            else
                echo "${core}-fork.1"
            fi
            ;;
        *)
            die "unknown bump kind: $kind"
            ;;
    esac
}

write_versions() {
    local new_ver="$1"
    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] would set Version = \"$new_ver\" in internal/version/version.go"
        info "[dry-run] would set package.json version to $new_ver"
        return 0
    fi

    # version.go
    local tmp_go
    tmp_go=$(mktemp) || die "mktemp failed"
    sed "s/^const Version = \".*\"\$/const Version = \"$new_ver\"/" "$VERSION_GO" > "$tmp_go" || die "failed to rewrite $VERSION_GO"
    grep -q "const Version = \"$new_ver\"" "$tmp_go" || die "rewrite of version.go did not stick"
    mv "$tmp_go" "$VERSION_GO"

    # package.json — keep formatting (2-space, trailing comma on version line).
    local tmp_pkg
    if command -v node >/dev/null 2>&1; then
        node -e '
const fs = require("fs");
const p = process.argv[1];
const v = process.argv[2];
const text = fs.readFileSync(p, "utf8");
const next = text.replace(
  /^(\s*"version"\s*:\s*")([^"]+)(")/m,
  (_, a, _old, c) => a + v + c
);
if (next === text) {
  console.error("package.json version field not found");
  process.exit(1);
}
fs.writeFileSync(p + ".tmp", next);
' "$PACKAGE_JSON" "$new_ver" || die "failed to rewrite package.json via node"
        mv "$PACKAGE_JSON.tmp" "$PACKAGE_JSON"
    else
        tmp_pkg=$(mktemp) || die "mktemp failed"
        sed "s/^  \"version\": \".*\",\$/  \"version\": \"$new_ver\",/" "$PACKAGE_JSON" > "$tmp_pkg" || die "failed to rewrite package.json"
        grep -q "\"version\": \"$new_ver\"" "$tmp_pkg" || die "rewrite of package.json did not stick"
        mv "$tmp_pkg" "$PACKAGE_JSON"
    fi

    # Verify lockstep.
    local code_v pkg_v
    code_v=$(read_code_version)
    pkg_v=$(read_pkg_version)
    if [[ "$code_v" != "$new_ver" || "$pkg_v" != "$new_ver" ]]; then
        die "post-write mismatch: code=$code_v pkg=$pkg_v want=$new_ver"
    fi
}

run_or_echo() {
    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] $*"
        return 0
    fi
    "$@"
}

ensure_repo_root() {
    cd "$REPO_ROOT" || die "cannot cd to $REPO_ROOT"
    if [[ ! -f "$VERSION_GO" || ! -f "$PACKAGE_JSON" ]]; then
        die "missing version files — run from the rotor checkout"
    fi
}

preflight() {
    # $1 expect_clean_for_bump (1 or 0)
    local expect_clean_for_bump="$1"
    local code_v pkg_v dirty
    code_v=$(read_code_version)
    pkg_v=$(read_pkg_version)

    if [[ "$code_v" != "$pkg_v" ]]; then
        die "version mismatch before release: version.go=$code_v package.json=$pkg_v (fix lockstep first)"
    fi

    if [[ "$FLAG_SKIP_CHECKS" == 1 ]]; then
        echo "$code_v"
        return 0
    fi

    dirty=$(git status --porcelain --untracked-files=no)
    if [[ -n "$dirty" ]]; then
        if [[ "$expect_clean_for_bump" == 1 ]]; then
            warn "working tree has tracked changes:"
            git status --short --untracked-files=no >&2
            confirm "Continue anyway?" 0 || die "aborted (dirty tree)"
        fi
    fi

    echo "$code_v"
}

tag_exists_local() {
    git rev-parse -q --verify "refs/tags/$1" >/dev/null 2>&1
}

tag_exists_remote() {
    git ls-remote --exit-code --tags "$1" "refs/tags/$2" >/dev/null 2>&1
}

do_snapshot_full() {
    # Full 6-target goreleaser snapshot. Prefer do_build for filtered OS/arch.
    if ! command -v goreleaser >/dev/null 2>&1; then
        die "goreleaser not on PATH (mise install goreleaser, or brew install goreleaser)"
    fi
    info "Building full-matrix local snapshot with goreleaser (no publish)…"
    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] goreleaser release --snapshot --clean --skip=publish"
        return 0
    fi
    goreleaser release --snapshot --clean --skip=publish || die "goreleaser snapshot failed"
    info "Snapshot artifacts in dist/"
    if [[ -d dist ]]; then
        ls -1 dist | head -n 40 || true
    fi
}

do_snapshot() {
    # Interactive: always pick targets first. Full matrix → goreleaser;
    # anything narrower → plain go build (same as --build).
    if [[ "$FLAG_YES" == 1 && -z "$OPT_OS" ]]; then
        do_snapshot_full
        return
    fi

    if [[ -z "$OPT_OS" && "$FLAG_YES" == 0 ]]; then
        local scope
        scope=$(choose "Local build scope" \
            "Pick OS/arch (windows, linux, darwin…)" \
            "Full matrix via goreleaser (all 6)") || die cancelled
        if [[ "$scope" == "Full matrix via goreleaser (all 6)" ]]; then
            do_snapshot_full
            return
        fi
        # Fall through to filtered build with interactive presets.
        FLAG_BUILD=1
        do_build
        return
    fi

    # --snapshot --os windows → filtered go build
    if [[ -n "$OPT_OS" ]]; then
        FLAG_BUILD=1
        do_build
        return
    fi

    do_snapshot_full
}

parse_os_list() {
    local raw="$1" part
    local -a out=()
    local IFS=','
    for part in $raw; do
        part=$(printf '%s' "$part" | tr '[:upper:]' '[:lower:]' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        [[ -n "$part" ]] || continue
        case "$part" in
            windows|win)
                out+=("windows")
                ;;
            linux)
                out+=("linux")
                ;;
            darwin|macos|mac|osx)
                out+=("darwin")
                ;;
            *)
                die "unknown OS '$part' (want windows|linux|darwin)"
                ;;
        esac
    done
    if (( ${#out[@]} == 0 )); then
        die "empty --os list"
    fi
    local -a uniq=()
    local o joined=""
    for o in "${out[@]}"; do
        # String accumulator, not ${uniq[*]} — IFS is ',' here so array
        # joins would use commas and break the " $o " containment check.
        case " $joined " in
            *" $o "*) ;;
            *) uniq+=("$o"); joined="$joined $o" ;;
        esac
    done
    printf '%s\n' "${uniq[@]}"
}

parse_arch_list() {
    local raw="$1" part
    local -a out=()
    local IFS=','
    for part in $raw; do
        part=$(printf '%s' "$part" | tr '[:upper:]' '[:lower:]' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        [[ -n "$part" ]] || continue
        case "$part" in
            amd64|x64|x86_64)
                out+=("amd64")
                ;;
            arm64|aarch64)
                out+=("arm64")
                ;;
            *)
                die "unknown arch '$part' (want amd64|arm64)"
                ;;
        esac
    done
    if (( ${#out[@]} == 0 )); then
        die "empty --arch list"
    fi
    local -a uniq=()
    local a joined=""
    for a in "${out[@]}"; do
        # String accumulator, not ${uniq[*]} — IFS is ',' here so array
        # joins would use commas and break the " $a " containment check.
        case " $joined " in
            *" $a "*) ;;
            *) uniq+=("$a"); joined="$joined $a" ;;
        esac
    done
    printf '%s\n' "${uniq[@]}"
}

pick_build_targets() {
    # Prints lines of "os arch". Uses OPT_OS/OPT_ARCH when set.
    local -a oses=()
    local -a arches=()

    if [[ -n "$OPT_OS" ]]; then
        while IFS= read -r o; do
            oses+=("$o")
        done < <(parse_os_list "$OPT_OS")
    elif [[ "$FLAG_YES" == 1 ]]; then
        die "--build with --yes requires --os (and optionally --arch)"
    else
        local preset
        preset=$(choose "Which targets?" \
            "windows (amd64 + arm64)" \
            "windows amd64 only" \
            "windows arm64 only" \
            "linux (amd64 + arm64)" \
            "darwin (amd64 + arm64)" \
            "all platforms (6 binaries)" \
            "custom multi-select…") || die cancelled

        case "$preset" in
            "windows (amd64 + arm64)")
                oses=(windows)
                arches=(amd64 arm64)
                ;;
            "windows amd64 only")
                oses=(windows)
                arches=(amd64)
                ;;
            "windows arm64 only")
                oses=(windows)
                arches=(arm64)
                ;;
            "linux (amd64 + arm64)")
                oses=(linux)
                arches=(amd64 arm64)
                ;;
            "darwin (amd64 + arm64)")
                oses=(darwin)
                arches=(amd64 arm64)
                ;;
            "all platforms (6 binaries)")
                oses=(windows linux darwin)
                arches=(amd64 arm64)
                ;;
            "custom multi-select…")
                local multi_out o a
                multi_out=$(choose_multi "OS" windows linux darwin) || die cancelled
                while IFS= read -r o; do
                    oses+=("$o")
                done <<< "$multi_out"
                multi_out=$(choose_multi "Arch" amd64 arm64) || die cancelled
                while IFS= read -r a; do
                    arches+=("$a")
                done <<< "$multi_out"
                ;;
            *)
                die "unknown preset: $preset"
                ;;
        esac
    fi

    if [[ -n "$OPT_ARCH" ]]; then
        arches=()
        while IFS= read -r a; do
            arches+=("$a")
        done < <(parse_arch_list "$OPT_ARCH")
    elif (( ${#arches[@]} == 0 )); then
        if [[ "$FLAG_YES" == 1 ]]; then
            arches=(amd64 arm64)
        else
            local multi_out a
            multi_out=$(choose_multi "Arch" amd64 arm64) || die cancelled
            while IFS= read -r a; do
                arches+=("$a")
            done <<< "$multi_out"
        fi
    fi

    if (( ${#oses[@]} == 0 )); then
        die "no OS selected"
    fi
    if (( ${#arches[@]} == 0 )); then
        die "no arch selected"
    fi

    local os arch
    for os in "${oses[@]}"; do
        for arch in "${arches[@]}"; do
            echo "$os $arch"
        done
    done
}

binary_name() {
    local ver="$1" os="$2" arch="$3"
    local suffix=""
    if [[ "$os" == windows ]]; then
        suffix=".exe"
    fi
    # Matches release.yml bare-bin asset naming.
    echo "rotor-v$ver-$os-$arch-bin$suffix"
}

file_bytes() {
    local path="$1"
    if [[ -f "$path" ]]; then
        stat -f %z "$path" 2>/dev/null || stat -c %s "$path" 2>/dev/null || echo 0
    else
        echo 0
    fi
}

fmt_mib() {
    # One decimal MiB, e.g. 11.6
    awk -v b="$1" 'BEGIN { printf "%.1f", b / 1024 / 1024 }'
}

max_bytes() {
    # OPT_MAX_MIB may be int or float-ish; treat as MiB.
    awk -v m="$OPT_MAX_MIB" 'BEGIN { printf "%.0f", m * 1024 * 1024 }'
}

pick_compress_mode() {
    if [[ -n "$OPT_COMPRESS" ]]; then
        echo "$OPT_COMPRESS"
        return
    fi
    if [[ "$FLAG_YES" == 1 ]]; then
        echo skip
        return
    fi

    local choice
    choice=$(choose "Turbo-compress for easy distribution? (target ≤ $OPT_MAX_MIB MiB)" \
        "7z — standard archive (auto-splits into <10MiB volumes if needed)" \
        "zpaq — smallest single file (needs zpaq to extract)" \
        "skip — leave raw build") || die cancelled

    case "$choice" in
        7z*)
            echo 7z
            ;;
        zpaq*)
            echo zpaq
            ;;
        skip*)
            echo skip
            ;;
        *)
            die "unknown compress choice: $choice"
            ;;
    esac
}

require_compress_tools() {
    local mode="$1"
    case "$mode" in
        7z)
            command -v 7z >/dev/null 2>&1 || command -v 7zz >/dev/null 2>&1 || die "7z not on PATH (brew install p7zip)"
            ;;
        zpaq)
            command -v zpaq >/dev/null 2>&1 || die "zpaq not on PATH (brew install zpaq)"
            ;;
        skip)
            return 0
            ;;
        *)
            die "unknown compress mode: $mode (want 7z|zpaq|skip; UPX is not allowed)"
            ;;
    esac
}

sevenz_bin() {
    if command -v 7z >/dev/null 2>&1; then
        echo 7z
    elif command -v 7zz >/dev/null 2>&1; then
        echo 7zz
    else
        die "7z not on PATH"
    fi
}

remove_stale_parts() {
    # Delete existing foo.7z.001/002/… for a source binary. No `--` on find:
    # BSD find (macOS) rejects it, and the directory comes from dirname of a
    # build artifact so it never starts with '-'.
    local src="$1"
    local dir base
    dir=$(dirname -- "$src")
    base=$(basename -- "$src")
    find "$dir" -maxdepth 1 -name "$base.7z.[0-9][0-9][0-9]" -delete 2>/dev/null || true
    return 0
}

compress_7z() {
    # Ultra LZMA2 solid archive next to the binary. Measured on the real
    # windows binary: md=64m→10.93MiB, md=1536m→10.79MiB (best single-file).
    # If the archive still exceeds the target, also split into <10MiB
    # volumes (foo.7z.001, .002…) — 7-Zip reassembles them from part .001.
    local src="$1"
    local dst="$src.7z"
    local bin
    bin=$(sevenz_bin)

    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] $bin a -t7z -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on $dst $src"
        echo "$dst"
        return 0
    fi

    rm -f -- "$dst"
    remove_stale_parts "$src"
    # -mfb=273 max fast bytes, 1.5G dict, solid — best measured ratio for PE.
    $bin a -t7z -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on -mmt=on -- "$dst" "$src" >/dev/null \
        || die "7z failed on $src"
    [[ -s "$dst" ]] || die "empty archive: $dst"
    echo "$dst"
}

compress_7z_volumes() {
    # Split the archive into volumes each under the byte target. Returns the
    # part count. Parts live at foo.7z.001, foo.7z.002, …
    local src="$1"
    local dst="$src.7z"
    local bin
    bin=$(sevenz_bin)
    # 0.5MiB headroom under the limit for safety (and Discord byte math).
    local vol_mib
    vol_mib=$(awk -v m="$OPT_MAX_MIB" 'BEGIN { v=m-0.5; if (v<1) v=1; printf "%d", int(v) }')

    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] $bin a -t7z -v${vol_mib}m -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on $dst $src"
        echo 2
        return 0
    fi

    rm -f -- "$dst"
    remove_stale_parts "$src"
    $bin a -t7z "-v${vol_mib}m" -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on -mmt=on -- "$dst" "$src" >/dev/null \
        || die "7z volume split failed on $src"

    local dir base p
    dir=$(dirname -- "$src")
    base=$(basename -- "$src")
    local -a parts=()
    while IFS= read -r p; do
        parts+=("$p")
    done < <(find "$dir" -maxdepth 1 -name "$base.7z.[0-9][0-9][0-9]" -print 2>/dev/null)
    if (( ${#parts[@]} == 0 )); then
        die "7z produced no volumes: $dst"
    fi
    # Sanity: every part under limit.
    for p in "${parts[@]}"; do
        if (( $(file_bytes "$p") > $(max_bytes) )); then
            warn "volume over target: $p"
        fi
    done
    echo "${#parts[@]}"
}

compress_zpaq() {
    # Smallest single-file lossless option measured: zpaq -m5 → 9.92 MiB
    # (beats 7z 10.79). Recipient needs zpaq to extract.
    local src="$1"
    local dst="$src.zpaq"

    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] zpaq add $dst $src -m5 -threads 8"
        echo "$dst"
        return 0
    fi

    rm -f -- "$dst"
    zpaq add "$dst" "$src" -m5 -threads 8 >/dev/null 2>&1 || die "zpaq failed on $src"
    [[ -s "$dst" ]] || die "empty archive: $dst"
    echo "$dst"
}

report_size() {
    local path="$1" kind="$2"
    local bytes mib limit limit_mib
    bytes=$(file_bytes "$path")
    mib=$(fmt_mib "$bytes")
    limit=$(max_bytes)
    limit_mib=$(fmt_mib "$limit")
    if (( bytes <= limit )); then
        info "  ✓ $kind $path  $mib MiB  (≤ $limit_mib MiB)"
        return 0
    fi
    local over over_mib
    over=$(( bytes - limit ))
    over_mib=$(fmt_mib "$over")
    warn "  ✗ $kind $path  $mib MiB  ($over_mib MiB over $limit_mib MiB target)"
    return 1
}

turbo_compress_files() {
    # $@: list of built binary paths
    local -a files=("$@")
    if (( ${#files[@]} == 0 )); then
        return 0
    fi

    local mode
    mode=$(pick_compress_mode)
    OPT_COMPRESS="$mode"
    if [[ "$mode" == skip ]]; then
        info "Skipping compress."
        return 0
    fi

    require_compress_tools "$mode"

    local limit_mib
    limit_mib=$(fmt_mib "$(max_bytes)")
    info "Turbo compress mode=$mode  target≤$limit_mib MiB"
    info "Measured on the real windows exe: 7z 10.79MiB, zpaq 9.92MiB (raw ~50MiB)."

    local failed=0 src raw_mib out n
    for src in "${files[@]}"; do
        if [[ ! -f "$src" ]]; then
            warn "missing $src — skip"
            continue
        fi

        raw_mib=$(fmt_mib "$(file_bytes "$src")")
        info "Packing $src ($raw_mib MiB raw)"

        case "$mode" in
            7z)
                out=$(compress_7z "$src")
                if (( $(file_bytes "$out") > $(max_bytes) )); then
                    warn "$out over target — splitting into volumes"
                    n=$(compress_7z_volumes "$src")
                    info "  ✓ $src.7z.001 …  $n parts, each < $limit_mib MiB"
                    # Leave the single archive too? No — remove to avoid confusion.
                    rm -f -- "$out"
                    info "  Tip: put all $src.7z.* parts in one folder and open .001 with 7-Zip."
                else
                    report_size "$out" 7z || failed=1
                fi
                ;;
            zpaq)
                out=$(compress_zpaq "$src")
                report_size "$out" zpaq || failed=1
                info "  Tip: recipient extracts with: zpaq x $(basename -- "$out")"
                ;;
            *)
                die "unknown compress mode: $mode"
                ;;
        esac
    done

    if (( failed != 0 )); then
        warn "One or more archives exceeded $limit_mib MiB. Single-file floor is 7z 10.79 / zpaq 9.92 MiB for current rotor."
        # Non-zero only when user set an explicit compress mode via flags;
        # interactive stays non-fatal so the build still "worked".
        if [[ "$FLAG_YES" == 1 ]]; then
            return 1
        fi
    fi
    return 0
}

do_build() {
    if ! command -v go >/dev/null 2>&1; then
        die "go not on PATH"
    fi

    local ver pkg_v line os arch out
    ver=$(read_code_version)
    pkg_v=$(read_pkg_version)
    if [[ "$ver" != "$pkg_v" ]]; then
        warn "version.go=$ver package.json=$pkg_v (build still uses version.go)"
    fi

    local -a targets=()
    while IFS= read -r line; do
        targets+=("$line")
    done < <(pick_build_targets)
    if (( ${#targets[@]} == 0 )); then
        die "no build targets"
    fi

    echo >&2
    info "Local build plan (version $ver, size ldflags: $ROTOR_SIZE_LDFLAGS):"
    for line in "${targets[@]}"; do
        os="${line%% *}"
        arch="${line#* }"
        info "  → dist/$(binary_name "$ver" "$os" "$arch")"
    done
    echo >&2

    confirm "Build these binaries into dist/?" 1 || die aborted

    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        local -a planned=()
        for line in "${targets[@]}"; do
            os="${line%% *}"
            arch="${line#* }"
            out="dist/$(binary_name "$ver" "$os" "$arch")"
            info "[dry-run] GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags=\"$ROTOR_SIZE_LDFLAGS\" -o $out ./cmd/rotor"
            planned+=("$out")
        done
        turbo_compress_files "${planned[@]}"
        info "Dry run complete."
        return 0
    fi

    mkdir -p dist || die "mkdir dist failed"

    local -a built=()
    for line in "${targets[@]}"; do
        os="${line%% *}"
        arch="${line#* }"
        out="dist/$(binary_name "$ver" "$os" "$arch")"
        info "Building $os/$arch → $out"
        env GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
            go build -trimpath -buildvcs=false -ldflags="$ROTOR_SIZE_LDFLAGS" -o "$out" ./cmd/rotor \
            || die "go build failed for $os/$arch"
        [[ -s "$out" ]] || die "empty binary: $out"
        built+=("$out")
    done

    info "Built ${#built[@]} binary(ies):"
    local f mib
    for f in "${built[@]}"; do
        mib=$(fmt_mib "$(file_bytes "$f")")
        info "  $f  $mib MiB"
    done

    turbo_compress_files "${built[@]}"
}

parse_args() {
    local i=1 arg
    while (( i <= $# )); do
        arg="${!i}"
        case "$arg" in
            -h|--help)
                FLAG_HELP=1
                ;;
            --yes|-y)
                FLAG_YES=1
                ;;
            --dry-run)
                FLAG_DRY_RUN=1
                ;;
            --no-push)
                FLAG_NO_PUSH=1
                ;;
            --no-commit)
                FLAG_NO_COMMIT=1
                ;;
            --tag-only)
                FLAG_TAG_ONLY=1
                ;;
            --snapshot)
                FLAG_SNAPSHOT=1
                ;;
            --build)
                FLAG_BUILD=1
                ;;
            --skip-checks)
                FLAG_SKIP_CHECKS=1
                ;;
            --bump|--version|--remote|--message|--os|--arch|--compress|--max-mib)
                (( i += 1 ))
                if (( i > $# )); then
                    case "$arg" in
                        --bump) die "--bump needs a value" ;;
                        --version) die "--version needs a value" ;;
                        --remote) die "--remote needs a value" ;;
                        --message) die "--message needs a value" ;;
                        --os) die "--os needs a value" ;;
                        --arch) die "--arch needs a value" ;;
                        --compress) die "--compress needs a value (skip|7z|zpaq)" ;;
                        --max-mib) die "--max-mib needs a number" ;;
                    esac
                fi
                case "$arg" in
                    --bump) OPT_BUMP="${!i}" ;;
                    --version) OPT_VERSION="${!i}" ;;
                    --remote) OPT_REMOTE="${!i}" ;;
                    --message) OPT_MESSAGE="${!i}" ;;
                    --os) OPT_OS="${!i}" ;;
                    --arch) OPT_ARCH="${!i}" ;;
                    --compress) OPT_COMPRESS="${!i}" ;;
                    --max-mib) OPT_MAX_MIB="${!i}" ;;
                esac
                ;;
            --*)
                die "unknown option: $arg (try --help)"
                ;;
            *)
                die "unexpected argument: $arg (try --help)"
                ;;
        esac
        (( i += 1 ))
    done
}

pick_mode() {
    if [[ "$FLAG_BUILD" == 1 ]]; then
        echo build
        return
    fi
    if [[ "$FLAG_SNAPSHOT" == 1 ]]; then
        echo snapshot
        return
    fi
    if [[ "$FLAG_TAG_ONLY" == 1 ]]; then
        echo tag-only
        return
    fi
    if [[ -n "$OPT_OS" || -n "$OPT_ARCH" ]]; then
        FLAG_BUILD=1
        echo build
        return
    fi
    if [[ -n "$OPT_BUMP" || -n "$OPT_VERSION" || "$FLAG_YES" == 1 ]]; then
        echo full
        return
    fi

    # Live previews: each menu option shows what it will actually change.
    # The bump example follows the repo's fork.x habit when the current
    # version is fork-shaped, otherwise patch.
    local current example_next tag push_hint choice
    current=$(read_code_version)
    if [[ "$current" =~ -fork\.[0-9]+$ ]]; then
        example_next=$(bump_version fork "$current")
    else
        example_next=$(bump_version patch "$current")
    fi
    tag="v$current"
    push_hint="push → $OPT_REMOTE"
    if [[ "$FLAG_NO_PUSH" == 1 ]]; then
        push_hint="no push (--no-push)"
    fi

    choice=$(choose "What do you want to do?" \
        "$(printf '%s\t%s' "Full release (bump → commit → tag → push)" "edit version.go + package.json ($current → e.g. $example_next), commit, tag v$example_next, $push_hint")" \
        "$(printf '%s\t%s' "Tag + push only (versions already bumped)" "no file edits — create+push tag $tag on current HEAD")" \
        "$(printf '%s\t%s' "Bump versions only (no commit/tag)" "edit version.go + package.json only ($current → e.g. $example_next); no commit/tag/push")" \
        "$(printf '%s\t%s' "Local build (pick OS/arch — windows, linux, darwin)" "go build binaries into dist/ (pick OS/arch), then optional 7z/zpaq compress")" \
        "Quit") || die cancelled

    case "$choice" in
        "Full release (bump → commit → tag → push)")
            info "Full release: bump version.go+package.json ($current → e.g. $example_next), commit, tag v$example_next, $push_hint"
            echo full
            ;;
        "Tag + push only (versions already bumped)")
            info "Tag + push: no file edits — create+push tag $tag on current HEAD"
            echo tag-only
            ;;
        "Bump versions only (no commit/tag)")
            info "Bump only: edit version.go + package.json ($current → e.g. $example_next); no commit/tag/push"
            echo bump-only
            ;;
        "Local build (pick OS/arch — windows, linux, darwin)")
            echo build
            ;;
        Quit)
            echo quit
            ;;
        *)
            die "unknown mode: $choice"
            ;;
    esac
}

pick_bump() {
    local current="$1"
    if [[ -n "$OPT_VERSION" ]]; then
        echo custom
        return
    fi
    if [[ -n "$OPT_BUMP" ]]; then
        echo "$OPT_BUMP"
        return
    fi

    local fork_next patch_next minor_next major_next choice
    fork_next=$(bump_version fork "$current")
    patch_next=$(bump_version patch "$current")
    minor_next=$(bump_version minor "$current")
    major_next=$(bump_version major "$current")

    choice=$(choose "Bump type (current $current)" \
        "fork  → $fork_next" \
        "patch → $patch_next" \
        "minor → $minor_next" \
        "major → $major_next" \
        "custom version") || die cancelled

    case "$choice" in
        fork*)
            echo fork
            ;;
        patch*)
            echo patch
            ;;
        minor*)
            echo minor
            ;;
        major*)
            echo major
            ;;
        "custom version")
            echo custom
            ;;
        *)
            die "unknown bump choice: $choice"
            ;;
    esac
}

resolve_new_version() {
    local kind="$1" current="$2"
    local v typed
    if [[ -n "$OPT_VERSION" ]]; then
        v="$OPT_VERSION"
        while [[ "$v" == v* ]]; do
            v="${v#v}"
        done
        validate_version "$v" || die "invalid --version '$OPT_VERSION'"
        echo "$v"
        return
    fi

    if [[ "$kind" == custom ]]; then
        typed=$(ask_text "New version (no leading v)" "$current") || die cancelled
        typed=$(printf '%s' "$typed" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        while [[ "$typed" == v* ]]; do
            typed="${typed#v}"
        done
        validate_version "$typed" || die "invalid version '$typed'"
        echo "$typed"
        return
    fi

    bump_version "$kind" "$current"
}

pick_remote() {
    local -a remotes=()
    local r
    while IFS= read -r r; do
        remotes+=("$r")
    done < <(git remote)
    if (( ${#remotes[@]} == 0 )); then
        die "no git remotes configured"
    fi

    # Non-interactive / single remote: keep --remote default (origin) when valid.
    if [[ "$FLAG_YES" == 1 ]]; then
        case " ${remotes[*]:-} " in
            *" $OPT_REMOTE "*)
                echo "$OPT_REMOTE"
                return
                ;;
            *)
                die "remote '$OPT_REMOTE' not found (have: ${remotes[*]})"
                ;;
        esac
    fi

    if (( ${#remotes[@]} == 1 )); then
        echo "${remotes[0]}"
        return
    fi

    local choice
    choice=$(choose "Push remote" "${remotes[@]}") || die cancelled
    echo "$choice"
}

do_full_or_bump() {
    local mode="$1"
    local current kind new_ver tag remote commit_msg
    current=$(preflight 1)
    info "Current version: $current"

    kind=$(pick_bump "$current")
    case "$kind" in
        fork|patch|minor|major|custom) ;;
        *) die "invalid bump kind: $kind (use patch|minor|major|fork|custom)" ;;
    esac

    new_ver=$(resolve_new_version "$kind" "$current")
    if [[ "$new_ver" == "$current" ]]; then
        die "new version equals current ($current) — nothing to do"
    fi

    tag="v$new_ver"
    remote="$OPT_REMOTE"
    if [[ "$mode" == full && "$FLAG_NO_PUSH" == 0 ]]; then
        remote=$(pick_remote)
        OPT_REMOTE="$remote"
    fi

    if [[ "$FLAG_SKIP_CHECKS" == 0 ]]; then
        if tag_exists_local "$tag"; then
            die "local tag $tag already exists"
        fi
        if [[ "$mode" == full && "$FLAG_NO_PUSH" == 0 ]]; then
            if tag_exists_remote "$remote" "$tag"; then
                die "remote $remote already has tag $tag"
            fi
        fi
    fi

    commit_msg="$OPT_MESSAGE"
    if [[ -z "$commit_msg" ]]; then
        commit_msg="chore(release): prepare $tag"
    fi

    echo >&2
    if has_gum; then
        gum style --border rounded --padding "0 1" --border-foreground 212 \
            "Release plan" \
            "  current : $current" \
            "  new     : $new_ver" \
            "  tag     : $tag" \
            "  mode    : $mode" \
            "  remote  : $remote" \
            "  commit  : $commit_msg" \
            "  dry-run : $FLAG_DRY_RUN" \
            "  push    : $(if [[ "$FLAG_NO_PUSH" == 1 ]]; then echo no; else echo yes; fi)" >&2
    else
        echo "Release plan" >&2
        echo "  current : $current" >&2
        echo "  new     : $new_ver" >&2
        echo "  tag     : $tag" >&2
        echo "  mode    : $mode" >&2
        echo "  remote  : $remote" >&2
        echo "  commit  : $commit_msg" >&2
        echo "  dry-run : $FLAG_DRY_RUN" >&2
        echo "  push    : $(if [[ "$FLAG_NO_PUSH" == 1 ]]; then echo no; else echo yes; fi)" >&2
    fi
    echo >&2

    confirm "Proceed?" 1 || die aborted

    write_versions "$new_ver"
    info "Versions set to $new_ver"

    if [[ "$mode" == bump-only || "$FLAG_NO_COMMIT" == 1 ]]; then
        info "Stopped after bump (--no-commit / bump-only)."
        info "Next: commit, then: git tag $tag && git push $remote $tag"
        return 0
    fi

    # Commit only the two version files.
    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] git add internal/version/version.go package.json"
        info "[dry-run] git commit -m \"$commit_msg\""
        info "[dry-run] git tag $tag"
        if [[ "$FLAG_NO_PUSH" == 0 ]]; then
            info "[dry-run] git push $remote HEAD"
            info "[dry-run] git push $remote $tag"
        fi
        info "Dry run complete."
        return 0
    fi

    git add -- internal/version/version.go package.json || die "git add failed"

    # Refuse if nothing staged (e.g. identical rewrite).
    if git diff --cached --quiet; then
        die "nothing staged after version bump"
    fi

    git commit -m "$commit_msg" || die "git commit failed"
    info "Committed: $commit_msg"

    git tag "$tag" || die "git tag failed"
    info "Tagged $tag"

    if [[ "$FLAG_NO_PUSH" == 1 ]]; then
        info "Skipped push (--no-push)."
        info "When ready: git push $remote HEAD && git push $remote $tag"
        return 0
    fi

    if confirm "Push HEAD and $tag to $remote? This triggers release." 1; then
        git push "$remote" HEAD || die "git push $remote HEAD failed"
        git push "$remote" "$tag" || die "git push $remote $tag failed"

        info "Pushed $tag → $remote"
        info "Watch: gh run list --workflow=release.yml --limit 5"
    else
        warn "Tag created locally but not pushed."
        info "Push later: git push $remote HEAD && git push $remote $tag"
        return 0
    fi
}

do_tag_only() {
    local current tag remote dirty_versions push_word
    current=$(preflight 0)
    tag="v$current"
    remote="$OPT_REMOTE"

    if [[ "$FLAG_YES" == 0 ]]; then
        remote=$(pick_remote)
        OPT_REMOTE="$remote"
    fi

    info "Tag-only release at $tag (code + package.json already $current)"

    if [[ "$FLAG_SKIP_CHECKS" == 0 ]]; then
        if tag_exists_local "$tag"; then
            die "local tag $tag already exists"
        fi
        if [[ "$FLAG_NO_PUSH" == 0 ]]; then
            if tag_exists_remote "$remote" "$tag"; then
                die "remote $remote already has tag $tag"
            fi
        fi
    fi

    # Require HEAD commit to include the version (soft check: version files match).
    dirty_versions=$(git status --porcelain -- internal/version/version.go package.json)
    if [[ -n "$dirty_versions" ]]; then
        die "version files are dirty — commit the bump before --tag-only"
    fi

    push_word="push"
    if [[ "$FLAG_NO_PUSH" == 1 ]]; then
        push_word="keep local"
    fi
    confirm "Create and $push_word tag $tag on $remote?" 1 || die aborted

    if [[ "$FLAG_DRY_RUN" == 1 ]]; then
        info "[dry-run] git tag $tag"
        if [[ "$FLAG_NO_PUSH" == 0 ]]; then
            info "[dry-run] git push $remote $tag"
        fi
        info "Dry run complete."
        return 0
    fi

    git tag "$tag" || die "git tag failed"
    info "Tagged $tag"

    if [[ "$FLAG_NO_PUSH" == 1 ]]; then
        info "Skipped push (--no-push). Later: git push $remote $tag"
        return 0
    fi

    git push "$remote" "$tag" || die "git push $remote $tag failed"
    info "Pushed $tag → $remote"
    info "Watch: gh run list --workflow=release.yml --limit 5"
}

# --- main ---
parse_args "$@"

if [[ "$FLAG_HELP" == 1 ]]; then
    usage
    exit 0
fi

ensure_repo_root

if [[ -n "$OPT_VERSION" ]]; then
    while [[ "$OPT_VERSION" == v* ]]; do
        OPT_VERSION="${OPT_VERSION#v}"
    done
    validate_version "$OPT_VERSION" || die "invalid --version '$OPT_VERSION'"
fi

if [[ -n "$OPT_BUMP" ]]; then
    case "$OPT_BUMP" in
        fork|patch|minor|major|custom) ;;
        *) die "--bump must be patch|minor|major|fork|custom" ;;
    esac
fi

if [[ -n "$OPT_COMPRESS" ]]; then
    case "$OPT_COMPRESS" in
        skip|7z|zpaq) ;;
        *) die "--compress must be skip|7z|zpaq (UPX is not allowed)" ;;
    esac
    # Compress implies a local build unless snapshot was requested.
    if [[ "$FLAG_SNAPSHOT" == 0 && "$FLAG_TAG_ONLY" == 0 ]]; then
        FLAG_BUILD=1
    fi
fi

if [[ ! "$OPT_MAX_MIB" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    die "--max-mib must be a number (got '$OPT_MAX_MIB')"
fi

# Mutual exclusion soft rules
mode_flags=0
[[ "$FLAG_SNAPSHOT" == 1 ]] && mode_flags=$(( mode_flags + 1 ))
[[ "$FLAG_TAG_ONLY" == 1 ]] && mode_flags=$(( mode_flags + 1 ))
[[ "$FLAG_BUILD" == 1 ]] && mode_flags=$(( mode_flags + 1 ))
if (( mode_flags > 1 )); then
    die "use only one of --snapshot, --tag-only, --build"
fi

mode=$(pick_mode)

case "$mode" in
    quit)
        info "Cancelled."
        exit 0
        ;;
    snapshot)
        do_snapshot
        ;;
    build)
        do_build
        ;;
    tag-only)
        do_tag_only
        ;;
    bump-only)
        FLAG_NO_COMMIT=1
        do_full_or_bump bump-only
        ;;
    full)
        do_full_or_bump full
        ;;
    *)
        die "internal error: bad mode $mode"
        ;;
esac
