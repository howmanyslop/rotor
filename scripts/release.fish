#!/usr/bin/env fish
# Interactive release helper for rotor.
#
# Maintainer flow (see CONTRIBUTING.md):
#   1. Bump internal/version/version.go + package.json in lockstep
#   2. Commit
#   3. Tag vX.Y.Z (must match the Version constant)
#   4. Push the tag → release.yml + npm-publish.yml
#
# Usage:
#   ./scripts/release.fish                 # interactive
#   ./scripts/release.fish --help
#   ./scripts/release.fish --dry-run
#   ./scripts/release.fish --bump fork --yes --no-push
#   ./scripts/release.fish --version 2.3.0 --yes
#   ./scripts/release.fish --tag-only --yes
#   ./scripts/release.fish --snapshot
#
# Requires: git, fish. Optional: gum (nicer prompts), goreleaser (snapshot builds).

set -g SCRIPT_NAME (status filename)
set -g REPO_ROOT (git rev-parse --show-toplevel 2>/dev/null)
or begin
    echo "error: not inside a git repository" >&2
    exit 1
end

set -g VERSION_GO "$REPO_ROOT/internal/version/version.go"
set -g PACKAGE_JSON "$REPO_ROOT/package.json"

set -g FLAG_HELP 0
set -g FLAG_YES 0
set -g FLAG_DRY_RUN 0
set -g FLAG_NO_PUSH 0
set -g FLAG_NO_COMMIT 0
set -g FLAG_TAG_ONLY 0
set -g FLAG_SNAPSHOT 0
set -g FLAG_SKIP_CHECKS 0
set -g OPT_BUMP ""
set -g OPT_VERSION ""
set -g OPT_REMOTE origin
set -g OPT_MESSAGE ""

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
    echo "  --snapshot        local goreleaser snapshot build into dist/ (no tag)"
    echo "  --skip-checks     skip dirty-tree / remote-tag probes"
    echo "  -h, --help        show this help"
    echo
    echo "Examples:"
    echo "  $SCRIPT_NAME"
    echo "  $SCRIPT_NAME --bump fork --yes"
    echo "  $SCRIPT_NAME --version 2.3.0-fork.1 --dry-run"
    echo "  $SCRIPT_NAME --tag-only --yes --remote upstream"
    echo "  $SCRIPT_NAME --snapshot"
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

function do_snapshot
    command -q goreleaser
    or die "goreleaser not on PATH (mise install goreleaser, or brew install goreleaser)"
    info "Building local snapshot with goreleaser (no publish)…"
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
            case '--*'
                die "unknown option: $arg (try --help)"
            case '*'
                die "unexpected argument: $arg (try --help)"
        end
        set i (math $i + 1)
    end
end

function pick_mode
    if test $FLAG_SNAPSHOT -eq 1
        echo snapshot
        return
    end
    if test $FLAG_TAG_ONLY -eq 1
        echo tag-only
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
        "Local snapshot build (goreleaser, no tag)" \
        "Quit")
    or die cancelled

    switch $choice
        case "Full release (bump → commit → tag → push)"
            echo full
        case "Tag + push only (versions already bumped)"
            echo tag-only
        case "Bump versions only (no commit/tag)"
            echo bump-only
        case "Local snapshot build (goreleaser, no tag)"
            echo snapshot
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

    confirm "Push HEAD and $tag to $remote? This triggers release + npm-publish." 1
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
    info "       gh run list --workflow=npm-publish.yml --limit 5"
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

# Mutual exclusion soft rules
if test $FLAG_SNAPSHOT -eq 1 -a $FLAG_TAG_ONLY -eq 1
    die "use either --snapshot or --tag-only, not both"
end

set -l mode (pick_mode)

switch $mode
    case quit
        info "Cancelled."
        exit 0
    case snapshot
        do_snapshot
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
