# Regenerates the conformance-corpus goldens by running the REAL rbxtsc 3.0.0
# over testdata/conformance/project (roblox-ts's own upstream test sources).
# Requires Node plus Bun or npm (mise-managed). Run from repo root:
#   powershell -File tools/oracle/conformance-oracle.ps1
$ErrorActionPreference = "Stop"
$root = git rev-parse --show-toplevel
$proj = Join-Path $root "testdata/conformance/project"
$golden = Join-Path $root "testdata/conformance/golden"

if (-not (Test-Path (Join-Path $proj "node_modules"))) {
	Push-Location $proj
	if (Get-Command bun -ErrorAction SilentlyContinue) {
		bun install --no-save
	} else {
		npm install --no-audit --no-fund
	}
	Pop-Location
}

# clean output so removed fixtures don't leave stale goldens
Remove-Item -Recurse -Force (Join-Path $proj "out") -ErrorAction SilentlyContinue
Push-Location $proj
# node is mise-managed; Bun installs do not provide npm's Windows .cmd shim, so
# invoke the pinned roblox-ts CLI entrypoint directly.
# --allowCommentDirectives: upstream's own test driver (src/CLI/test.ts) sets
# allowCommentDirectives:true because the test sources use @ts-ignore/@ts-expect-error.
node .\node_modules\roblox-ts\out\CLI\cli.js --type model --allowCommentDirectives
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "rbxtsc failed with exit code $LASTEXITCODE" }
Pop-Location

$protected = @(
	"tests/array.spec.luau",
	"tests/class.spec.luau",
	"tests/destructure.spec.luau",
	"tests/exportLet.spec.luau",
	"tests/function.spec.luau",
	"tests/loop.spec.luau",
	"tests/object.spec.luau"
)
# These fork-authoritative Rotor goldens must never be deleted or regenerated
# by the upstream 3.0 oracle. See testdata/conformance/README.md.
Get-ChildItem $golden -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object {
	$rel = $_.FullName.Substring($golden.Length + 1).Replace("\\", "/")
	if ($protected -notcontains $rel) {
		Remove-Item -Force $_.FullName
	}
}
New-Item -ItemType Directory -Force $golden | Out-Null
# the corpus has subdirectories (tests/, helpers/) — preserve structure
$outDir = (Resolve-Path (Join-Path $proj "out")).Path
Get-ChildItem $outDir -Recurse -Filter *.luau | ForEach-Object {
	$rel = $_.FullName.Substring($outDir.Length + 1)
	$rel = $rel.Replace("\\", "/")
	if ($protected -contains $rel) {
		return
	}
	$dest = Join-Path $golden $rel
	New-Item -ItemType Directory -Force (Split-Path $dest) | Out-Null
	Copy-Item $_.FullName $dest
}
Write-Host "goldens regenerated: $((Get-ChildItem $golden -Recurse -File).Count) files ($($protected.Count) fork-divergent fixtures preserved)"
