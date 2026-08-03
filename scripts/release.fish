#!/usr/bin/env fish
# Interactive release helper for rotor.
#
# Maintainer flow (see CONTRIBUTING.md):
#   1. Bump internal/version/version.go + package.json in lockstep
#   2. Commit
#   3. Tag vX.Y.Z (must match the Version constant)
#   4. Push the tag → release.yml
#
# Usage:
#   ./scripts/release.fish                 # interactive
#   ./scripts/release.fish --help
#   ./scripts/release.fish --dry-run
#   ./scripts/release.fish --bump fork --yes --no-push
#   ./scripts/release.fish --version 2.3.0 --yes
#   ./scripts/release.fish --tag-only --yes
#   ./scripts/release.fish --snapshot
#   ./scripts/release.fish --build --os windows --arch amd64,arm64
#   ./scripts/release.fish --build --os windows --compress 7z
#
# Requires: git, fish, go (for --build). Optional: gum (nicer prompts),
# goreleaser (full-matrix --snapshot only), 7z (turbo compress).
# UPX is intentionally not supported.

set -g SCRIPT_NAME (status filename)
set -g REPO_ROOT (git rev-parse --show-toplevel 2>/dev/null)
or begin
    echo "error: not inside a git repository" >&2
    exit 1
end

set -g VERSION_GO "$REPO_ROOT/internal/version/version.go"
set -g PACKAGE_JSON "$REPO_ROOT/package.json"

# Size-oriented link flags. -s -w drop symbol/DWARF tables; -buildid= makes
# builds reproducible and sheds a few KB. Cannot get a tsgo-linked rotor
# under ~10MiB by flags alone — use --compress 7z for archives.
set -g ROTOR_SIZE_LDFLAGS "-s -w -buildid="

set -g FLAG_HELP 0
set -g FLAG_YES 0
set -g FLAG_DRY_RUN 0
set -g FLAG_NO_PUSH 0
set -g FLAG_NO_COMMIT 0
set -g FLAG_TAG_ONLY 0
set -g FLAG_SNAPSHOT 0
set -g FLAG_BUILD 0
set -g FLAG_SKIP_CHECKS 0
set -g OPT_BUMP ""
set -g OPT_VERSION ""
set -g OPT_REMOTE origin
set -g OPT_MESSAGE ""
set -g OPT_OS ""
set -g OPT_ARCH ""
# compress: "" | skip | 7z
set -g OPT_COMPRESS ""
# Soft target for packed artifacts (MiB). Default 10.
set -g OPT_MAX_MIB 10

function usage
    echo "Usage: $SCRIPT_NAME [options]"
    echo
    echo "Interactive (default) or flag-driven release cutter for rotor."
    echo
    echo "Options:"
    echo "  --bump KIND       patch | minor | major | fork | custom"
    echo "  --version VER     exact version (no leading v); implies custom bump"
    echo "  --remote NAME     git remote to push (default: origin)"
    echo "  --message TEXT    commit message (default: chore(release): prepare vX.Y.Z)"
    echo "  --yes             skip confirmation prompts"
    echo "  --dry-run         print actions; write nothing; push nothing"
    echo "  --no-push         do not push commit/tag"
    echo "  --no-commit       bump files only (no commit/tag/push)"
    echo "  --tag-only        versions already bumped; create+push tag only"
    echo "  --snapshot        full-matrix local goreleaser snapshot (no tag)"
    echo "  --build           local go build into dist/ (pick OS/arch; no tag)"
    echo "  --os LIST         comma list: windows,linux,darwin (with --build)"
    echo "  --arch LIST       comma list: amd64,arm64 (with --build)"
    echo "  --compress MODE   after build: skip | 7z | zpaq  (UPX not allowed)"
    echo "                    7z  = universal archive; auto-splits into <target volumes"
    echo "                    zpaq = smallest single file (~9.9MiB), needs zpaq to extract"
    echo "  --max-mib N       size budget for packed artifacts (default: 10)"
    echo "  --skip-checks     skip dirty-tree / remote-tag probes"
    echo "  -h, --help        show this help"
    echo
    echo "Examples:"
    echo "  $SCRIPT_NAME"
    echo "  $SCRIPT_NAME --bump fork --yes"
    echo "  $SCRIPT_NAME --version 2.3.0-fork.1 --dry-run"
    echo "  $SCRIPT_NAME --tag-only --yes --remote upstream"
    echo "  $SCRIPT_NAME --snapshot"
    echo "  $SCRIPT_NAME --build --os windows --arch amd64"
    echo "  $SCRIPT_NAME --build --os windows --compress 7z --max-mib 10"
end

function has_gum
    command -q gum
end

function info
    # Always stderr so callers can capture pure data on stdout.
    if has_gum
        gum style --foreground 212 -- "$argv" >&2
    else
        echo "→ $argv" >&2
    end
end

function warn
    if has_gum
        gum style --foreground 214 -- "warning: $argv" >&2
    else
        echo "warning: $argv" >&2
    end
end

function die
    if has_gum
        gum style --foreground 196 --bold -- "error: $argv" >&2
    else
        echo "error: $argv" >&2
    end
    exit 1
end

function confirm --argument-names prompt default_yes
    if test $FLAG_YES -eq 1
        return 0
    end
    if has_gum
        if test "$default_yes" = 1
            gum confirm --default=true -- "$prompt"
        else
            gum confirm --default=false -- "$prompt"
        end
        return $status
    end
    set -l suffix "[y/N]"
    if test "$default_yes" = 1
        set suffix "[Y/n]"
    end
    read -P "$prompt $suffix " answer
    or return 1
    set answer (string lower -- (string trim -- $answer))
    if test -z "$answer"
        test "$default_yes" = 1
        return $status
    end
    string match -qr '^(y|yes)$' -- $answer
end

function choose --argument-names header
    # Remaining argv after header are options.
    set -l options $argv[2..-1]
    if has_gum
        printf '%s\n' $options | gum choose --header "$header"
        return $status
    end
    if command -q fzf
        printf '%s\n' $options | fzf --prompt "$header > " --height 12 --reverse
        return $status
    end
    echo $header >&2
    set -l i 1
    for opt in $options
        echo "  $i) $opt" >&2
        set i (math $i + 1)
    end
    read -P "Choice [1-"(count $options)"]: " pick
    or return 1
    if not string match -qr '^[0-9]+$' -- $pick
        return 1
    end
    if test $pick -lt 1 -o $pick -gt (count $options)
        return 1
    end
    echo $options[$pick]
end

function choose_multi --argument-names header
    # Multi-select. Remaining argv after header are options. Prints one per line.
    set -l options $argv[2..-1]
    if has_gum
        printf '%s\n' $options | gum choose --no-limit --header "$header (space to toggle, enter to confirm)"
        return $status
    end
    if command -q fzf
        printf '%s\n' $options | fzf --multi --prompt "$header > " --height 12 --reverse
        return $status
    end
    echo "$header (comma-separated indexes, e.g. 1,3)" >&2
    set -l i 1
    for opt in $options
        echo "  $i) $opt" >&2
        set i (math $i + 1)
    end
    read -P "Choices: " pick
    or return 1
    set -l picked
    for part in (string split , -- $pick)
        set part (string trim -- $part)
        if not string match -qr '^[0-9]+$' -- $part
            return 1
        end
        if test $part -lt 1 -o $part -gt (count $options)
            return 1
        end
        set -a picked $options[$part]
    end
    test (count $picked) -gt 0; or return 1
    printf '%s\n' $picked
end

function ask_text --argument-names prompt placeholder
    if has_gum
        if test -n "$placeholder"
            gum input --placeholder "$placeholder" --prompt "$prompt "
        else
            gum input --prompt "$prompt "
        end
        return $status
    end
    if test -n "$placeholder"
        read -P "$prompt [$placeholder]: " value
        or return 1
        if test -z (string trim -- $value)
            echo $placeholder
            return 0
        end
        echo $value
        return 0
    end
    read -P "$prompt: " value
    or return 1
    echo $value
end

function read_code_version
    set -l v (sed -n 's/^const Version = "\(.*\)"$/\1/p' $VERSION_GO | string trim)
    test -n "$v"; or die "could not read Version from internal/version/version.go"
    echo $v
end

function read_pkg_version
    set -l v (node -p "require('./package.json').version" 2>/dev/null)
    if test -z "$v"
        set v (sed -n 's/^  "version": "\(.*\)",$/\1/p' $PACKAGE_JSON | head -n1 | string trim)
    end
    test -n "$v"; or die "could not read version from package.json"
    echo $v
end

function validate_version --argument-names ver
    # SemVer-ish: MAJOR.MINOR.PATCH with optional -prerelease (dots/hyphens ok).
    string match -qr '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$' -- $ver
end

function parse_core --argument-names ver
    echo (string split -m1 - $ver)[1]
end

function bump_version --argument-names kind current
    set -l core (parse_core $current)
    set -l parts (string split . $core)
    if test (count $parts) -ne 3
        die "cannot parse core version from '$current'"
    end
    set -l major $parts[1]
    set -l minor $parts[2]
    set -l patch $parts[3]

    switch $kind
        case major
            echo (math $major + 1).0.0
        case minor
            echo $major.(math $minor + 1).0
        case patch
            echo $major.$minor.(math $patch + 1)
        case fork
            if string match -qr -- '-fork\.([0-9]+)$' $current
                set -l n (string replace -r '.*-fork\.([0-9]+)$' '$1' -- $current)
                echo $core-fork.(math $n + 1)
            else if string match -q '*-fork' -- $current
                echo $current.1
            else
                echo $core-fork.1
            end
        case '*'
            die "unknown bump kind: $kind"
    end
end

function write_versions --argument-names new_ver
    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] would set Version = \"$new_ver\" in internal/version/version.go"
        info "[dry-run] would set package.json version to $new_ver"
        return 0
    end

    # version.go
    set -l tmp_go (mktemp)
    sed "s/^const Version = \".*\"\$/const Version = \"$new_ver\"/" $VERSION_GO >$tmp_go
    or die "failed to rewrite $VERSION_GO"
    string match -q "*const Version = \"$new_ver\"*" <$tmp_go
    or die "rewrite of version.go did not stick"
    mv $tmp_go $VERSION_GO

    # package.json — keep formatting (2-space, trailing comma on version line).
    set -l tmp_pkg (mktemp)
    if command -q node
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
' $PACKAGE_JSON $new_ver
        or die "failed to rewrite package.json via node"
        mv $PACKAGE_JSON.tmp $PACKAGE_JSON
    else
        sed "s/^  \"version\": \".*\",\$/  \"version\": \"$new_ver\",/" $PACKAGE_JSON >$tmp_pkg
        or die "failed to rewrite package.json"
        string match -q "*\"version\": \"$new_ver\"*" <$tmp_pkg
        or die "rewrite of package.json did not stick"
        mv $tmp_pkg $PACKAGE_JSON
    end

    # Verify lockstep.
    set -l code_v (read_code_version)
    set -l pkg_v (read_pkg_version)
    test "$code_v" = "$new_ver" -a "$pkg_v" = "$new_ver"
    or die "post-write mismatch: code=$code_v pkg=$pkg_v want=$new_ver"
end

function run_or_echo
    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] $argv"
        return 0
    end
    $argv
end

function ensure_repo_root
    cd $REPO_ROOT
    or die "cannot cd to $REPO_ROOT"
    test -f $VERSION_GO -a -f $PACKAGE_JSON
    or die "missing version files — run from the rotor checkout"
end

function preflight --argument-names expect_clean_for_bump
    set -l code_v (read_code_version)
    set -l pkg_v (read_pkg_version)

    if test "$code_v" != "$pkg_v"
        die "version mismatch before release: version.go=$code_v package.json=$pkg_v (fix lockstep first)"
    end

    if test $FLAG_SKIP_CHECKS -eq 1
        echo $code_v
        return 0
    end

    set -l dirty (git status --porcelain --untracked-files=no)
    if test -n "$dirty"
        if test "$expect_clean_for_bump" = 1
            warn "working tree has tracked changes:"
            git status --short --untracked-files=no >&2
            confirm "Continue anyway?" 0
            or die "aborted (dirty tree)"
        end
    end

    echo $code_v
end

function tag_exists_local --argument-names tag
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null 2>&1
end

function tag_exists_remote --argument-names remote tag
    git ls-remote --exit-code --tags $remote "refs/tags/$tag" >/dev/null 2>&1
end

function do_snapshot_full
    # Full 6-target goreleaser snapshot. Prefer do_build for filtered OS/arch.
    command -q goreleaser
    or die "goreleaser not on PATH (mise install goreleaser, or brew install goreleaser)"
    info "Building full-matrix local snapshot with goreleaser (no publish)…"
    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] goreleaser release --snapshot --clean --skip=publish"
        return 0
    end
    goreleaser release --snapshot --clean --skip=publish
    or die "goreleaser snapshot failed"
    info "Snapshot artifacts in dist/"
    if test -d dist
        ls -1 dist | head -n 40
    end
end

function do_snapshot
    # Interactive: always pick targets first. Full matrix → goreleaser;
    # anything narrower → plain go build (same as --build).
    if test $FLAG_YES -eq 1 -a -z "$OPT_OS"
        do_snapshot_full
        return
    end

    if test -z "$OPT_OS" -a $FLAG_YES -eq 0
        set -l scope (choose "Local build scope" \
            "Pick OS/arch (windows, linux, darwin…)" \
            "Full matrix via goreleaser (all 6)")
        or die cancelled
        if test "$scope" = "Full matrix via goreleaser (all 6)"
            do_snapshot_full
            return
        end
        # Fall through to filtered build with interactive presets.
        set FLAG_BUILD 1
        do_build
        return
    end

    # --snapshot --os windows → filtered go build
    if test -n "$OPT_OS"
        set FLAG_BUILD 1
        do_build
        return
    end

    do_snapshot_full
end

function parse_os_list --argument-names raw
    set -l out
    for part in (string split , -- $raw)
        set part (string lower (string trim -- $part))
        test -n "$part"; or continue
        switch $part
            case windows win
                set -a out windows
            case linux
                set -a out linux
            case darwin macos mac osx
                set -a out darwin
            case '*'
                die "unknown OS '$part' (want windows|linux|darwin)"
        end
    end
    test (count $out) -gt 0; or die "empty --os list"
    set -l uniq
    for o in $out
        if not contains -- $o $uniq
            set -a uniq $o
        end
    end
    printf '%s\n' $uniq
end

function parse_arch_list --argument-names raw
    set -l out
    for part in (string split , -- $raw)
        set part (string lower (string trim -- $part))
        test -n "$part"; or continue
        switch $part
            case amd64 x64 x86_64
                set -a out amd64
            case arm64 aarch64
                set -a out arm64
            case '*'
                die "unknown arch '$part' (want amd64|arm64)"
        end
    end
    test (count $out) -gt 0; or die "empty --arch list"
    set -l uniq
    for a in $out
        if not contains -- $a $uniq
            set -a uniq $a
        end
    end
    printf '%s\n' $uniq
end

function pick_build_targets
    # Prints lines of "os arch". Uses OPT_OS/OPT_ARCH when set.
    set -l oses
    set -l arches

    if test -n "$OPT_OS"
        set oses (parse_os_list $OPT_OS)
    else if test $FLAG_YES -eq 1
        die "--build with --yes requires --os (and optionally --arch)"
    else
        set -l preset (choose "Which targets?" \
            "windows (amd64 + arm64)" \
            "windows amd64 only" \
            "windows arm64 only" \
            "linux (amd64 + arm64)" \
            "darwin (amd64 + arm64)" \
            "all platforms (6 binaries)" \
            "custom multi-select…")
        or die cancelled

        switch $preset
            case "windows (amd64 + arm64)"
                set oses windows
                set arches amd64 arm64
            case "windows amd64 only"
                set oses windows
                set arches amd64
            case "windows arm64 only"
                set oses windows
                set arches arm64
            case "linux (amd64 + arm64)"
                set oses linux
                set arches amd64 arm64
            case "darwin (amd64 + arm64)"
                set oses darwin
                set arches amd64 arm64
            case "all platforms (6 binaries)"
                set oses windows linux darwin
                set arches amd64 arm64
            case "custom multi-select…"
                set oses (choose_multi "OS" windows linux darwin)
                or die cancelled
                set arches (choose_multi "Arch" amd64 arm64)
                or die cancelled
            case '*'
                die "unknown preset: $preset"
        end
    end

    if test -n "$OPT_ARCH"
        set arches (parse_arch_list $OPT_ARCH)
    else if test (count $arches) -eq 0
        if test $FLAG_YES -eq 1
            set arches amd64 arm64
        else
            set arches (choose_multi "Arch" amd64 arm64)
            or die cancelled
        end
    end

    test (count $oses) -gt 0; or die "no OS selected"
    test (count $arches) -gt 0; or die "no arch selected"

    for os in $oses
        for arch in $arches
            echo $os $arch
        end
    end
end

function binary_name --argument-names ver os arch
    set -l suffix ""
    if test "$os" = windows
        set suffix .exe
    end
    # Matches release.yml bare-bin asset naming.
    echo "rotor-v$ver-$os-$arch-bin$suffix"
end

function file_bytes --argument-names path
    if test -f "$path"
        stat -f %z -- $path 2>/dev/null
        or stat -c %s -- $path 2>/dev/null
    else
        echo 0
    end
end

function fmt_mib --argument-names bytes
    # One decimal MiB, e.g. 11.6
    awk -v b=$bytes 'BEGIN { printf "%.1f" , b / 1024 / 1024 }'
end

function max_bytes
    # OPT_MAX_MIB may be int or float-ish; treat as MiB.
    awk -v m=$OPT_MAX_MIB 'BEGIN { printf "%.0f" , m * 1024 * 1024 }'
end

function pick_compress_mode
    if test -n "$OPT_COMPRESS"
        echo $OPT_COMPRESS
        return
    end
    if test $FLAG_YES -eq 1
        echo skip
        return
    end

    set -l choice (choose "Turbo-compress for easy distribution? (target ≤ $OPT_MAX_MIB MiB)" \
        "7z — standard archive (auto-splits into <10MiB volumes if needed)" \
        "zpaq — smallest single file (needs zpaq to extract)" \
        "skip — leave raw build")
    or die cancelled

    switch $choice
        case "7z*"
            echo 7z
        case "zpaq*"
            echo zpaq
        case "skip*"
            echo skip
        case '*'
            die "unknown compress choice: $choice"
    end
end

function require_compress_tools --argument-names mode
    switch $mode
        case 7z
            command -q 7z; or command -q 7zz; or die "7z not on PATH (brew install p7zip)"
        case zpaq
            command -q zpaq; or die "zpaq not on PATH (brew install zpaq)"
        case skip
            return 0
        case '*'
            die "unknown compress mode: $mode (want 7z|zpaq|skip; UPX is not allowed)"
    end
end

function sevenz_bin
    if command -q 7z
        echo 7z
    else if command -q 7zz
        echo 7zz
    else
        die "7z not on PATH"
    end
end

function remove_stale_parts --argument-names src
    # Delete existing foo.7z.001/002/… for a source binary. Uses find because
    # fish globs support only '*'; find -name handles [0-9] classes.
    set -l dir (dirname -- $src)
    set -l base (basename -- $src)
    find -- $dir -maxdepth 1 -name "$base.7z.[0-9][0-9][0-9]" -delete 2>/dev/null
    return 0
end

function compress_7z --argument-names src
    # Ultra LZMA2 solid archive next to the binary. Measured on the real
    # windows binary: md=64m→10.93MiB, md=1536m→10.79MiB (best single-file).
    # If the archive still exceeds the target, also split into <10MiB
    # volumes (foo.7z.001, .002…) — 7-Zip reassembles them from part .001.
    set -l dst $src.7z
    set -l bin (sevenz_bin)

    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] $bin a -t7z -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on $dst $src"
        echo $dst
        return 0
    end

    rm -f -- $dst
    remove_stale_parts $src
    # -mfb=273 max fast bytes, 1.5G dict, solid — best measured ratio for PE.
    $bin a -t7z -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on -mmt=on -- $dst $src >/dev/null
    or die "7z failed on $src"
    test -s $dst; or die "empty archive: $dst"
    echo $dst
end

function compress_7z_volumes --argument-names src
    # Split the archive into volumes each under the byte target. Returns the
    # part count. Parts live at foo.7z.001, foo.7z.002, …
    set -l dst $src.7z
    set -l bin (sevenz_bin)
    # 0.5MiB headroom under the limit for safety (and Discord byte math).
    set -l vol_mib (awk -v m=$OPT_MAX_MIB 'BEGIN { v=m-0.5; if (v<1) v=1; printf "%d", int(v) }')
    set -l vol_bytes (math "$vol_mib * 1024 * 1024")

    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] $bin a -t7z -v$vol_mib""m -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on $dst $src"
        echo 2
        return 0
    end

    rm -f -- $dst
    remove_stale_parts $src
    $bin a -t7z "-v$vol_mib""m" -m0=lzma2 -mx=9 -mfb=273 -md=1536m -ms=on -mmt=on -- $dst $src >/dev/null
    or die "7z volume split failed on $src"

    set -l parts (find (dirname -- $src) -maxdepth 1 -name (basename -- $src).7z.[0-9][0-9][0-9] -print 2>/dev/null)
    test (count $parts) -gt 0; or die "7z produced no volumes: $dst"
    # Sanity: every part under limit.
    for p in $parts
        test (file_bytes $p) -le (max_bytes); or warn "volume over target: $p"
    end
    echo (count $parts)
end

function compress_zpaq --argument-names src
    # Smallest single-file lossless option measured: zpaq -m5 → 9.92 MiB
    # (beats 7z 10.79). Recipient needs zpaq to extract.
    set -l dst $src.zpaq

    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] zpaq add $dst $src -m5 -threads 8"
        echo $dst
        return 0
    end

    rm -f -- $dst
    zpaq add $dst $src -m5 -threads 8 >/dev/null 2>&1
    or die "zpaq failed on $src"
    test -s $dst; or die "empty archive: $dst"
    echo $dst
end

function report_size --argument-names path kind
    set -l bytes (file_bytes $path)
    set -l mib (fmt_mib $bytes)
    set -l limit (max_bytes)
    set -l limit_mib (fmt_mib $limit)
    if test $bytes -le $limit
        info "  ✓ $kind $path  $mib MiB  (≤ $limit_mib MiB)"
        return 0
    end
    set -l over (math $bytes - $limit)
    set -l over_mib (fmt_mib $over)
    warn "  ✗ $kind $path  $mib MiB  ($over_mib MiB over $limit_mib MiB target)"
    return 1
end

function turbo_compress_files
    # argv: list of built binary paths
    set -l files $argv
    test (count $files) -gt 0; or return 0

    set -l mode (pick_compress_mode)
    set OPT_COMPRESS $mode
    if test "$mode" = skip
        info "Skipping compress."
        return 0
    end

    require_compress_tools $mode

    set -l limit_mib (fmt_mib (max_bytes))
    info "Turbo compress mode=$mode  target≤$limit_mib MiB"
    info "Measured on the real windows exe: 7z 10.79MiB, zpaq 9.92MiB (raw ~50MiB)."

    set -l failed 0
    for src in $files
        if not test -f $src
            warn "missing $src — skip"
            continue
        end

        set -l raw_mib (fmt_mib (file_bytes $src))
        info "Packing $src ($raw_mib MiB raw)"

        switch $mode
            case 7z
                set -l out (compress_7z $src)
                if test (file_bytes $out) -gt (max_bytes)
                    warn "$out over target — splitting into volumes"
                    set -l n (compress_7z_volumes $src)
                    info "  ✓ $src.7z.001 …  $n parts, each < $limit_mib MiB"
                    # Leave the single archive too? No — remove to avoid confusion.
                    rm -f -- $out
                    info "  Tip: put all $src.7z.* parts in one folder and open .001 with 7-Zip."
                else
                    report_size $out 7z
                    or set failed 1
                end
            case zpaq
                set -l out (compress_zpaq $src)
                report_size $out zpaq
                or set failed 1
                info "  Tip: recipient extracts with: zpaq x (basename $out)"
            case '*'
                die "unknown compress mode: $mode"
        end
    end

    if test $failed -ne 0
        warn "One or more archives exceeded $limit_mib MiB. Single-file floor is 7z 10.79 / zpaq 9.92 MiB for current rotor."
        # Non-zero only when user set an explicit compress mode via flags;
        # interactive stays non-fatal so the build still "worked".
        if test $FLAG_YES -eq 1
            return 1
        end
    end
    return 0
end

function do_build
    command -q go
    or die "go not on PATH"

    set -l ver (read_code_version)
    set -l pkg_v (read_pkg_version)
    if test "$ver" != "$pkg_v"
        warn "version.go=$ver package.json=$pkg_v (build still uses version.go)"
    end

    set -l targets (pick_build_targets)
    test (count $targets) -gt 0; or die "no build targets"

    echo >&2
    info "Local build plan (version $ver, size ldflags: $ROTOR_SIZE_LDFLAGS):"
    for line in $targets
        set -l parts (string split ' ' -- $line)
        set -l os $parts[1]
        set -l arch $parts[2]
        info "  → dist/"(binary_name $ver $os $arch)
    end
    echo >&2

    confirm "Build these binaries into dist/?" 1
    or die aborted

    if test $FLAG_DRY_RUN -eq 1
        set -l planned
        for line in $targets
            set -l parts (string split ' ' -- $line)
            set -l os $parts[1]
            set -l arch $parts[2]
            set -l out dist/(binary_name $ver $os $arch)
            info "[dry-run] GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags=\"$ROTOR_SIZE_LDFLAGS\" -o $out ./cmd/rotor"
            set -a planned $out
        end
        turbo_compress_files $planned
        info "Dry run complete."
        return 0
    end

    mkdir -p dist
    or die "mkdir dist failed"

    set -l built
    for line in $targets
        set -l parts (string split ' ' -- $line)
        set -l os $parts[1]
        set -l arch $parts[2]
        set -l out dist/(binary_name $ver $os $arch)
        info "Building $os/$arch → $out"
        env GOOS=$os GOARCH=$arch CGO_ENABLED=0 \
            go build -trimpath -buildvcs=false -ldflags="$ROTOR_SIZE_LDFLAGS" -o $out ./cmd/rotor
        or die "go build failed for $os/$arch"
        test -s $out; or die "empty binary: $out"
        set -a built $out
    end

    info "Built "(count $built)" binary(ies):"
    for f in $built
        set -l mib (fmt_mib (file_bytes $f))
        info "  $f  $mib MiB"
    end

    turbo_compress_files $built
end

function parse_args
    set -l i 1
    while test $i -le (count $argv)
        set -l arg $argv[$i]
        switch $arg
            case -h --help
                set FLAG_HELP 1
            case --yes -y
                set FLAG_YES 1
            case --dry-run
                set FLAG_DRY_RUN 1
            case --no-push
                set FLAG_NO_PUSH 1
            case --no-commit
                set FLAG_NO_COMMIT 1
            case --tag-only
                set FLAG_TAG_ONLY 1
            case --snapshot
                set FLAG_SNAPSHOT 1
            case --build
                set FLAG_BUILD 1
            case --skip-checks
                set FLAG_SKIP_CHECKS 1
            case --bump
                set i (math $i + 1)
                test $i -le (count $argv); or die "--bump needs a value"
                set OPT_BUMP $argv[$i]
            case --version
                set i (math $i + 1)
                test $i -le (count $argv); or die "--version needs a value"
                set OPT_VERSION $argv[$i]
            case --remote
                set i (math $i + 1)
                test $i -le (count $argv); or die "--remote needs a value"
                set OPT_REMOTE $argv[$i]
            case --message
                set i (math $i + 1)
                test $i -le (count $argv); or die "--message needs a value"
                set OPT_MESSAGE $argv[$i]
            case --os
                set i (math $i + 1)
                test $i -le (count $argv); or die "--os needs a value"
                set OPT_OS $argv[$i]
            case --arch
                set i (math $i + 1)
                test $i -le (count $argv); or die "--arch needs a value"
                set OPT_ARCH $argv[$i]
            case --compress
                set i (math $i + 1)
                test $i -le (count $argv); or die "--compress needs a value (skip|7z|zpaq)"
                set OPT_COMPRESS $argv[$i]
            case --max-mib
                set i (math $i + 1)
                test $i -le (count $argv); or die "--max-mib needs a number"
                set OPT_MAX_MIB $argv[$i]
            case '--*'
                die "unknown option: $arg (try --help)"
            case '*'
                die "unexpected argument: $arg (try --help)"
        end
        set i (math $i + 1)
    end
end

function pick_mode
    if test $FLAG_BUILD -eq 1
        echo build
        return
    end
    if test $FLAG_SNAPSHOT -eq 1
        echo snapshot
        return
    end
    if test $FLAG_TAG_ONLY -eq 1
        echo tag-only
        return
    end
    if test -n "$OPT_OS" -o -n "$OPT_ARCH"
        set FLAG_BUILD 1
        echo build
        return
    end
    if test -n "$OPT_BUMP" -o -n "$OPT_VERSION" -o $FLAG_YES -eq 1
        echo full
        return
    end

    set -l choice (choose "What do you want to do?" \
        "Full release (bump → commit → tag → push)" \
        "Tag + push only (versions already bumped)" \
        "Bump versions only (no commit/tag)" \
        "Local build (pick OS/arch — windows, linux, darwin)" \
        "Quit")
    or die cancelled

    switch $choice
        case "Full release (bump → commit → tag → push)"
            echo full
        case "Tag + push only (versions already bumped)"
            echo tag-only
        case "Bump versions only (no commit/tag)"
            echo bump-only
        case "Local build (pick OS/arch — windows, linux, darwin)"
            echo build
        case Quit
            echo quit
        case '*'
            die "unknown mode: $choice"
    end
end

function pick_bump --argument-names current
    if test -n "$OPT_VERSION"
        echo custom
        return
    end
    if test -n "$OPT_BUMP"
        echo $OPT_BUMP
        return
    end

    set -l fork_next (bump_version fork $current)
    set -l patch_next (bump_version patch $current)
    set -l minor_next (bump_version minor $current)
    set -l major_next (bump_version major $current)

    set -l choice (choose "Bump type (current $current)" \
        "fork  → $fork_next" \
        "patch → $patch_next" \
        "minor → $minor_next" \
        "major → $major_next" \
        "custom version")
    or die cancelled

    switch $choice
        case "fork*"
            echo fork
        case "patch*"
            echo patch
        case "minor*"
            echo minor
        case "major*"
            echo major
        case "custom version"
            echo custom
        case '*'
            die "unknown bump choice: $choice"
    end
end

function resolve_new_version --argument-names kind current
    if test -n "$OPT_VERSION"
        set -l v (string trim -l -c 'v' -- $OPT_VERSION)
        validate_version $v; or die "invalid --version '$OPT_VERSION'"
        echo $v
        return
    end

    if test "$kind" = custom
        set -l typed (ask_text "New version (no leading v)" "$current")
        or die cancelled
        set typed (string trim -l -c 'v' -- (string trim -- $typed))
        validate_version $typed; or die "invalid version '$typed'"
        echo $typed
        return
    end

    bump_version $kind $current
end

function pick_remote
    set -l remotes (git remote)
    if test (count $remotes) -eq 0
        die "no git remotes configured"
    end

    # Non-interactive / single remote: keep --remote default (origin) when valid.
    if test $FLAG_YES -eq 1
        if not contains -- $OPT_REMOTE $remotes
            die "remote '$OPT_REMOTE' not found (have: $remotes)"
        end
        echo $OPT_REMOTE
        return
    end

    if test (count $remotes) -eq 1
        echo $remotes[1]
        return
    end

    set -l choice (choose "Push remote" $remotes)
    or die cancelled
    echo $choice
end

function do_full_or_bump --argument-names mode
    set -l current (preflight 1)
    info "Current version: $current"

    set -l kind (pick_bump $current)
    if not contains -- $kind fork patch minor major custom
        die "invalid bump kind: $kind (use patch|minor|major|fork|custom)"
    end

    set -l new_ver (resolve_new_version $kind $current)
    if test "$new_ver" = "$current"
        die "new version equals current ($current) — nothing to do"
    end

    set -l tag "v$new_ver"
    set -l remote $OPT_REMOTE
    if test "$mode" = full -a $FLAG_NO_PUSH -eq 0
        set remote (pick_remote)
        set OPT_REMOTE $remote
    end

    if test $FLAG_SKIP_CHECKS -eq 0
        if tag_exists_local $tag
            die "local tag $tag already exists"
        end
        if test "$mode" = full -a $FLAG_NO_PUSH -eq 0
            if tag_exists_remote $remote $tag
                die "remote $remote already has tag $tag"
            end
        end
    end

    set -l commit_msg $OPT_MESSAGE
    if test -z "$commit_msg"
        set commit_msg "chore(release): prepare $tag"
    end

    echo >&2
    if has_gum
        gum style --border rounded --padding "0 1" --border-foreground 212 \
            "Release plan" \
            "  current : $current" \
            "  new     : $new_ver" \
            "  tag     : $tag" \
            "  mode    : $mode" \
            "  remote  : $remote" \
            "  commit  : $commit_msg" \
            "  dry-run : $FLAG_DRY_RUN" \
            "  push    : "(if test $FLAG_NO_PUSH -eq 1; echo no; else; echo yes; end) >&2
    else
        echo "Release plan" >&2
        echo "  current : $current" >&2
        echo "  new     : $new_ver" >&2
        echo "  tag     : $tag" >&2
        echo "  mode    : $mode" >&2
        echo "  remote  : $remote" >&2
        echo "  commit  : $commit_msg" >&2
        echo "  dry-run : $FLAG_DRY_RUN" >&2
        echo "  push    : "(if test $FLAG_NO_PUSH -eq 1; echo no; else; echo yes; end) >&2
    end
    echo >&2

    confirm "Proceed?" 1
    or die aborted

    write_versions $new_ver
    info "Versions set to $new_ver"

    if test "$mode" = bump-only -o $FLAG_NO_COMMIT -eq 1
        info "Stopped after bump (--no-commit / bump-only)."
        info "Next: commit, then: git tag $tag && git push $remote $tag"
        return 0
    end

    # Commit only the two version files.
    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] git add internal/version/version.go package.json"
        info "[dry-run] git commit -m \"$commit_msg\""
        info "[dry-run] git tag $tag"
        if test $FLAG_NO_PUSH -eq 0
            info "[dry-run] git push $remote HEAD"
            info "[dry-run] git push $remote $tag"
        end
        info "Dry run complete."
        return 0
    end

    git add -- internal/version/version.go package.json
    or die "git add failed"

    # Refuse if nothing staged (e.g. identical rewrite).
    git diff --cached --quiet
    and die "nothing staged after version bump"

    git commit -m "$commit_msg"
    or die "git commit failed"
    info "Committed: $commit_msg"

    git tag "$tag"
    or die "git tag failed"
    info "Tagged $tag"

    if test $FLAG_NO_PUSH -eq 1
        info "Skipped push (--no-push)."
        info "When ready: git push $remote HEAD && git push $remote $tag"
        return 0
    end

    confirm "Push HEAD and $tag to $remote? This triggers release." 1
    or begin
        warn "Tag created locally but not pushed."
        info "Push later: git push $remote HEAD && git push $remote $tag"
        return 0
    end

    git push $remote HEAD
    or die "git push $remote HEAD failed"
    git push $remote $tag
    or die "git push $remote $tag failed"

    info "Pushed $tag → $remote"
    info "Watch: gh run list --workflow=release.yml --limit 5"
end

function do_tag_only
    set -l current (preflight 0)
    set -l tag "v$current"
    set -l remote $OPT_REMOTE

    if test $FLAG_YES -eq 0
        set remote (pick_remote)
        set OPT_REMOTE $remote
    end

    info "Tag-only release at $tag (code + package.json already $current)"

    if test $FLAG_SKIP_CHECKS -eq 0
        if tag_exists_local $tag
            die "local tag $tag already exists"
        end
        if test $FLAG_NO_PUSH -eq 0
            if tag_exists_remote $remote $tag
                die "remote $remote already has tag $tag"
            end
        end
    end

    # Require HEAD commit to include the version (soft check: version files match).
    set -l dirty_versions (git status --porcelain -- internal/version/version.go package.json)
    if test -n "$dirty_versions"
        die "version files are dirty — commit the bump before --tag-only"
    end

    confirm "Create and "(if test $FLAG_NO_PUSH -eq 1; echo "keep local"; else; echo "push"; end)" tag $tag on $remote?" 1
    or die aborted

    if test $FLAG_DRY_RUN -eq 1
        info "[dry-run] git tag $tag"
        if test $FLAG_NO_PUSH -eq 0
            info "[dry-run] git push $remote $tag"
        end
        info "Dry run complete."
        return 0
    end

    git tag "$tag"
    or die "git tag failed"
    info "Tagged $tag"

    if test $FLAG_NO_PUSH -eq 1
        info "Skipped push (--no-push). Later: git push $remote $tag"
        return 0
    end

    git push $remote $tag
    or die "git push $remote $tag failed"
    info "Pushed $tag → $remote"
    info "Watch: gh run list --workflow=release.yml --limit 5"
end

# --- main ---
parse_args $argv

if test $FLAG_HELP -eq 1
    usage
    exit 0
end

ensure_repo_root

if test -n "$OPT_VERSION"
    set OPT_VERSION (string trim -l -c 'v' -- $OPT_VERSION)
    validate_version $OPT_VERSION; or die "invalid --version '$OPT_VERSION'"
end

if test -n "$OPT_BUMP"
    if not contains -- $OPT_BUMP fork patch minor major custom
        die "--bump must be patch|minor|major|fork|custom"
    end
end

if test -n "$OPT_COMPRESS"
    if not contains -- $OPT_COMPRESS skip 7z zpaq
        die "--compress must be skip|7z|zpaq (UPX is not allowed)"
    end
    # Compress implies a local build unless snapshot was requested.
    if test $FLAG_SNAPSHOT -eq 0 -a $FLAG_TAG_ONLY -eq 0
        set FLAG_BUILD 1
    end
end

if not string match -qr '^[0-9]+([.][0-9]+)?$' -- $OPT_MAX_MIB
    die "--max-mib must be a number (got '$OPT_MAX_MIB')"
end

# Mutual exclusion soft rules
set -l mode_flags 0
test $FLAG_SNAPSHOT -eq 1; and set mode_flags (math $mode_flags + 1)
test $FLAG_TAG_ONLY -eq 1; and set mode_flags (math $mode_flags + 1)
test $FLAG_BUILD -eq 1; and set mode_flags (math $mode_flags + 1)
if test $mode_flags -gt 1
    die "use only one of --snapshot, --tag-only, --build"
end

set -l mode (pick_mode)

switch $mode
    case quit
        info "Cancelled."
        exit 0
    case snapshot
        do_snapshot
    case build
        do_build
    case tag-only
        do_tag_only
    case bump-only
        set FLAG_NO_COMMIT 1
        do_full_or_bump bump-only
    case full
        do_full_or_bump full
    case '*'
        die "internal error: bad mode $mode"
end
