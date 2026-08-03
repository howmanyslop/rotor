[CmdletBinding()]
param(
    [string]$BaselineExe,
    [string]$CandidateExe,
    [string]$Fixture,
    [int]$ColdPairs,
    [int]$NoChangeRuns,
    [string]$SidecarTimeout,
    [string]$EvidenceDir,
    [switch]$ValidateOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-RepositoryRoot {
    return [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
}

function Get-EnvironmentSnapshot {
    $snapshot = [ordered]@{}
    foreach ($item in [Environment]::GetEnvironmentVariables().GetEnumerator() | Sort-Object Key) {
        $snapshot[[string]$item.Key] = Get-Sha256Text ([string]$item.Value)
    }
    return $snapshot
}

function Assert-EnvironmentEqual {
    param([hashtable]$Expected, [hashtable]$Actual)

    if ($Expected.Count -ne $Actual.Count) {
        throw 'environment changed during benchmark setup'
    }
    foreach ($key in $Expected.Keys) {
        if (-not $Actual.Contains($key) -or $Expected[$key] -cne $Actual[$key]) {
            throw "environment changed for $key"
        }
    }
}

function ConvertTo-SidecarTimeout {
    param([string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value)) {
        throw 'SidecarTimeout is required'
    }
    if ($Value -match '^(\d+)(ms|s|m|h)$') {
        $amount = [int64]$Matches[1]
        $unit = $Matches[2]
        $timeout = switch ($unit) {
            'ms' { [TimeSpan]::FromMilliseconds($amount) }
            's' { [TimeSpan]::FromSeconds($amount) }
            'm' { [TimeSpan]::FromMinutes($amount) }
            'h' { [TimeSpan]::FromHours($amount) }
        }
    } else {
        try {
            $timeout = [TimeSpan]::Parse($Value, [Globalization.CultureInfo]::InvariantCulture)
        } catch {
            throw "SidecarTimeout $Value must be a positive duration such as 300s"
        }
    }
    if ($timeout -le [TimeSpan]::Zero) {
        throw 'SidecarTimeout must be positive'
    }
    return $timeout
}

function Get-Sha256Text {
    param([string]$Text)

    $bytes = [Text.Encoding]::UTF8.GetBytes($Text)
    return ([Security.Cryptography.SHA256]::HashData($bytes) | ForEach-Object ToString x2) -join ''
}

function Add-HashBytes {
    param(
        [Security.Cryptography.HashAlgorithm]$Hash,
        [byte[]]$Bytes
    )

    if ($Bytes.Length -gt 0) {
        [void]$Hash.TransformBlock($Bytes, 0, $Bytes.Length, $Bytes, 0)
    }
}

function Get-TreeDigest {
    param(
        [string]$Root,
        [switch]$OutputStateOnly
    )

    $rootPath = [IO.Path]::GetFullPath($Root)
    $files = Get-ChildItem -LiteralPath $rootPath -Recurse -Force -File |
        Where-Object {
            $relative = [IO.Path]::GetRelativePath($rootPath, $_.FullName)
            if ($relative -match '(^|[\\/])\.git([\\/]|$)') {
                return $false
            }
            if (-not $OutputStateOnly) {
                return $true
            }
            if ($relative -match '(^|[\\/])(node_modules|\.rotor)([\\/]|$)') {
                return $false
            }
            $first = ($relative -split '[\\/]')[0]
            return $first -in @('out', 'out-test', 'out-tsc') -or $_.Name -like '*.rbxtsc.tsbuildinfo'
        } |
        Sort-Object FullName

    $hash = [Security.Cryptography.SHA256]::Create()
    $separator = [byte[]](0)
    $buffer = New-Object byte[] 65536
    try {
        foreach ($file in $files) {
            $relative = [IO.Path]::GetRelativePath($rootPath, $file.FullName).Replace('\', '/')
            Add-HashBytes $hash ([Text.Encoding]::UTF8.GetBytes($relative))
            Add-HashBytes $hash $separator
            $stream = [IO.File]::OpenRead($file.FullName)
            try {
                while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                    if ($read -eq $buffer.Length) {
                        Add-HashBytes $hash $buffer
                    } else {
                        Add-HashBytes $hash $buffer[0..($read - 1)]
                    }
                }
            } finally {
                $stream.Dispose()
            }
            Add-HashBytes $hash $separator
        }
        [void]$hash.TransformFinalBlock([byte[]]::new(0), 0, 0)
        return ($hash.Hash | ForEach-Object ToString x2) -join ''
    } finally {
        $hash.Dispose()
    }
}

function Test-AllowedColdStateTarget {
    param([string]$WorkCopy, [string]$Target)

    $root = [IO.Path]::GetFullPath($WorkCopy).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $path = [IO.Path]::GetFullPath($Target)
    if (-not $path.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        return $false
    }
    $relative = [IO.Path]::GetRelativePath($root, $path).Replace('\', '/')
    if ($relative -in @('out', 'out-test', 'out-tsc')) {
        return $true
    }
    if ($relative -match '(^|/)(node_modules|\.rotor|\.git)(/|$)') {
        return $false
    }
    return [IO.Path]::GetFileName($path) -like '*.rbxtsc.tsbuildinfo'
}

function Reset-ColdState {
    param([string]$WorkCopy)

    foreach ($name in @('out', 'out-test', 'out-tsc')) {
        $target = Join-Path $WorkCopy $name
        if ((Test-Path -LiteralPath $target) -and -not (Test-AllowedColdStateTarget $WorkCopy $target)) {
            throw "refusing undeclared cold-state cleanup target $target"
        }
        if (Test-Path -LiteralPath $target) {
            Remove-Item -LiteralPath $target -Recurse -Force
        }
    }
    foreach ($state in Get-ChildItem -LiteralPath $WorkCopy -Recurse -Force -File -Filter '*.rbxtsc.tsbuildinfo') {
        if (Test-AllowedColdStateTarget $WorkCopy $state.FullName) {
            Remove-Item -LiteralPath $state.FullName -Force
        }
    }
}

function Get-GitState {
    param([string]$Path)

    $inside = & git -C $Path rev-parse --is-inside-work-tree 2>$null
    if ($LASTEXITCODE -ne 0 -or $inside -ne 'true') {
        return $null
    }
    $status = & git -C $Path status --porcelain=v1 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw "cannot inspect git state at $Path"
    }
    return ($status -join "`n")
}

function Assert-CleanGitWorktree {
    param([string]$Path, [string]$Name)

    $state = Get-GitState $Path
    if ($null -ne $state -and $state -ne '') {
        throw "$Name is a dirty git worktree"
    }
    return $state
}

function Get-GitRoot {
    param([string]$Path)

    $root = & git -C $Path rev-parse --show-toplevel 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $null
    }
    return [IO.Path]::GetFullPath(($root -join "`n"))
}

function Test-PathInside {
    param([string]$Child, [string]$Parent)

    $childPath = [IO.Path]::GetFullPath($Child)
    $parentPath = [IO.Path]::GetFullPath($Parent).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    return $childPath.StartsWith($parentPath + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)
}

function Assert-BenchmarkInputs {
    param([string]$RepositoryRoot)

    foreach ($input in @(
            @{ Name = 'BaselineExe'; Value = $BaselineExe; Kind = 'Leaf' },
            @{ Name = 'CandidateExe'; Value = $CandidateExe; Kind = 'Leaf' },
            @{ Name = 'Fixture'; Value = $Fixture; Kind = 'Container' },
            @{ Name = 'EvidenceDir'; Value = $EvidenceDir; Kind = 'Container' }
        )) {
        if ([string]::IsNullOrWhiteSpace([string]$input.Value)) {
            throw "$($input.Name) is required"
        }
    }
    if (-not (Test-Path -LiteralPath $BaselineExe -PathType Leaf)) {
        throw "BaselineExe does not exist: $BaselineExe"
    }
    if (-not (Test-Path -LiteralPath $CandidateExe -PathType Leaf)) {
        throw "CandidateExe does not exist: $CandidateExe"
    }
    if (-not (Test-Path -LiteralPath $Fixture -PathType Container)) {
        throw "Fixture does not exist: $Fixture"
    }
    if ($ColdPairs -lt 1 -or $NoChangeRuns -lt 1) {
        throw 'ColdPairs and NoChangeRuns must both be positive'
    }
    [void](ConvertTo-SidecarTimeout $SidecarTimeout)
    $trackedRoots = [Collections.Generic.List[string]]::new()
    foreach ($path in @($RepositoryRoot, $Fixture, (Split-Path -LiteralPath $BaselineExe), (Split-Path -LiteralPath $CandidateExe))) {
        $root = Get-GitRoot $path
        if ($null -eq $root) {
            $root = [IO.Path]::GetFullPath($path)
        }
        if (-not $trackedRoots.Contains($root)) {
            $trackedRoots.Add($root)
        }
    }
    foreach ($root in $trackedRoots) {
        if (Test-PathInside $EvidenceDir $root) {
            throw "EvidenceDir must be outside benchmark input root $root"
        }
    }
    $states = [Collections.Generic.List[object]]::new()
    foreach ($root in $trackedRoots) {
        $states.Add([ordered]@{ Root = $root; State = Assert-CleanGitWorktree $root 'Benchmark input worktree' })
    }
    return @($states)
}

function Invoke-ProcessWithTimeout {
    param(
        [string]$Executable,
        [string[]]$Arguments,
        [string]$WorkingDirectory,
        [TimeSpan]$Timeout,
        [string]$StdoutPath,
        [string]$StderrPath
    )

    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $Executable
    $startInfo.WorkingDirectory = $WorkingDirectory
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    foreach ($argument in $Arguments) {
        [void]$startInfo.ArgumentList.Add($argument)
    }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    try {
        if (-not $process.Start()) {
            throw "could not start $Executable"
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $milliseconds = [Math]::Min([int]::MaxValue, [Math]::Ceiling($Timeout.TotalMilliseconds))
        if (-not $process.WaitForExit([int]$milliseconds)) {
            if ($IsWindows) {
                & taskkill /PID $process.Id /T /F *> $null
            } else {
                $process.Kill($true)
            }
            throw "command timed out after ${Timeout}: $Executable $($Arguments -join ' ')"
        }
        $process.WaitForExit()
        [IO.File]::WriteAllText($StdoutPath, $stdoutTask.GetAwaiter().GetResult(), [Text.UTF8Encoding]::new($false))
        [IO.File]::WriteAllText($StderrPath, $stderrTask.GetAwaiter().GetResult(), [Text.UTF8Encoding]::new($false))
        return [ordered]@{
            DurationMS = [Math]::Max(1, [int64][Math]::Round($stopwatch.Elapsed.TotalMilliseconds))
            ExitCode = $process.ExitCode
        }
    } finally {
        $stopwatch.Stop()
        $process.Dispose()
    }
}

function Invoke-ScoredBuild {
    param(
        [string]$Executable,
        [string]$WorkCopy,
        [TimeSpan]$Timeout,
        [string]$EvidencePath
    )

    $result = Invoke-ProcessWithTimeout $Executable @('build', '--project', $WorkCopy, '--json') $WorkCopy $Timeout "$EvidencePath.stdout.json" "$EvidencePath.stderr.txt"
    $stdout = [IO.File]::ReadAllText("$EvidencePath.stdout.json")
    try {
        $publicResult = $stdout | ConvertFrom-Json
        $diagnostics = $publicResult.diagnostics | ConvertTo-Json -Compress -Depth 32
    } catch {
        throw "prebuilt binary did not emit a public --json result: $Executable"
    }
    return [ordered]@{
        DurationMS = $result.DurationMS
        ExitCode = $result.ExitCode
        DiagnosticsDigest = Get-Sha256Text $diagnostics
        OutputTreeDigest = Get-TreeDigest $WorkCopy -OutputStateOnly
    }
}

function New-ManifestRecord {
    param(
        [int]$Pair,
        [string]$Phase,
        [string]$Order,
        [string]$Binary,
        [hashtable]$Result
    )

    return [ordered]@{
        pair = $Pair
        phase = $Phase
        order = $Order
        binary = $Binary
        duration_ms = $Result.DurationMS
        exit_code = $Result.ExitCode
        diagnostics_digest = $Result.DiagnosticsDigest
        output_tree_digest = $Result.OutputTreeDigest
    }
}

function Get-OrderForPair {
    param([int]$Pair)

    if ($Pair % 2 -eq 1) {
        return 'AB'
    }
    return 'BA'
}

function Get-WindowsMachineMetadata {
    param([string]$TimeoutValue, [hashtable]$Environment)

    $os = Get-CimInstance Win32_OperatingSystem
    $cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
    $computer = Get-CimInstance Win32_ComputerSystem
    $storage = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='$($env:SystemDrive)'" | Select-Object -First 1
    $power = (& powercfg /getactivescheme 2>$null) -join ' '
    return [ordered]@{
        os = $os.Caption
        version = $os.Version
        cpu = $cpu.Name
        ram_bytes = [int64]$computer.TotalPhysicalMemory
        storage = "$($storage.DeviceID) size=$($storage.Size) free=$($storage.FreeSpace)"
        power = $power
        run_order = @('AB', 'BA')
        sidecar_timeout = $TimeoutValue
        environment = $Environment
    }
}

function Invoke-PerfCompare {
    param([string]$RepositoryRoot, [string]$ManifestPath, [string]$VerdictPath)

    $tool = Join-Path $RepositoryRoot 'tools/perfcompare'
    if (-not (Test-Path -LiteralPath $tool -PathType Container) -or -not (Get-Command go -ErrorAction SilentlyContinue)) {
        return $null
    }
    $output = & go run ./tools/perfcompare --input $ManifestPath --output $VerdictPath 2>&1
    return [ordered]@{ ExitCode = $LASTEXITCODE; Output = ($output -join "`n") }
}

function Remove-TestOwnedWorkCopies {
    param([string]$EvidencePath, [string]$WorkRoot)

    if ((Test-Path -LiteralPath $WorkRoot) -and (Test-PathInside $WorkRoot $EvidencePath) -and ([IO.Path]::GetFileName($WorkRoot) -like 'work-*')) {
        Remove-Item -LiteralPath $WorkRoot -Recurse -Force
    }
}

function Invoke-RealBenchmark {
    param([string]$RepositoryRoot)

    if (-not $IsWindows) {
        throw 'Windows benchmark runs require Windows. Use -ValidateOnly on other platforms.'
    }
    $trackedWorktrees = Assert-BenchmarkInputs $RepositoryRoot
    $timeout = ConvertTo-SidecarTimeout $SidecarTimeout
    $fixturePath = [IO.Path]::GetFullPath($Fixture)
    $evidencePath = [IO.Path]::GetFullPath($EvidenceDir)
    [IO.Directory]::CreateDirectory($evidencePath) | Out-Null
    $workRoot = Join-Path $evidencePath ("work-" + [Guid]::NewGuid().ToString('N'))
    $baselineWork = Join-Path $workRoot 'baseline'
    $candidateWork = Join-Path $workRoot 'candidate'
    $manifestPath = Join-Path $evidencePath 'windows-build-performance-manifest.json'
    $verdictPath = Join-Path $evidencePath 'windows-build-performance-verdict.json'
    $fixtureDigest = Get-TreeDigest $fixturePath
    $environment = Get-EnvironmentSnapshot
    $oldSidecarTimeout = [Environment]::GetEnvironmentVariable('ROTOR_SIDECAR_TIMEOUT', 'Process')
    $records = [Collections.Generic.List[object]]::new()
    try {
        $env:ROTOR_SIDECAR_TIMEOUT = $SidecarTimeout
        $environment = Get-EnvironmentSnapshot
        [IO.Directory]::CreateDirectory($workRoot) | Out-Null
        Copy-Item -LiteralPath $fixturePath -Destination $baselineWork -Recurse -Force
        Copy-Item -LiteralPath $fixturePath -Destination $candidateWork -Recurse -Force
        foreach ($phaseInfo in @(@{ Name = 'cold'; Count = $ColdPairs }, @{ Name = 'no_change'; Count = $NoChangeRuns })) {
            for ($pair = 1; $pair -le $phaseInfo.Count; $pair++) {
                $order = Get-OrderForPair $pair
                if ($phaseInfo.Name -eq 'cold') {
                    Reset-ColdState $baselineWork
                    Reset-ColdState $candidateWork
                }
                $sequence = if ($order -eq 'AB') { @('baseline', 'candidate') } else { @('candidate', 'baseline') }
                foreach ($binary in $sequence) {
                    $workCopy = if ($binary -eq 'baseline') { $baselineWork } else { $candidateWork }
                    $executable = if ($binary -eq 'baseline') { $BaselineExe } else { $CandidateExe }
                    $prefix = Join-Path $evidencePath ("$($phaseInfo.Name)-$pair-$binary")
                    $result = Invoke-ScoredBuild $executable $workCopy $timeout $prefix
                    $records.Add((New-ManifestRecord $pair $phaseInfo.Name $order $binary $result))
                }
            }
        }
        $timingPath = Join-Path $evidencePath 'candidate-timings.json'
        [void](Invoke-ProcessWithTimeout $CandidateExe @('build', '--project', $candidateWork, '--json', '--timings', $timingPath) $candidateWork $timeout (Join-Path $evidencePath 'candidate-timings.stdout.json') (Join-Path $evidencePath 'candidate-timings.stderr.txt'))
        if ((Get-TreeDigest $fixturePath) -ne $fixtureDigest) {
            throw 'canonical fixture or source worktree changed during benchmark'
        }
        foreach ($worktree in $trackedWorktrees) {
            if ((Get-GitState $worktree.Root) -cne $worktree.State) {
                throw "benchmark input worktree changed during benchmark: $($worktree.Root)"
            }
        }
        Assert-EnvironmentEqual $environment (Get-EnvironmentSnapshot)
        $manifest = [ordered]@{
            schema = 1
            machine = Get-WindowsMachineMetadata $SidecarTimeout $environment
            baseline = [ordered]@{ revision = "sha256:$(Get-FileHash -LiteralPath $BaselineExe -Algorithm SHA256 | Select-Object -ExpandProperty Hash)"; command = "$BaselineExe build --project <work-copy> --json" }
            candidate = [ordered]@{ revision = "sha256:$(Get-FileHash -LiteralPath $CandidateExe -Algorithm SHA256 | Select-Object -ExpandProperty Hash)"; command = "$CandidateExe build --project <work-copy> --json" }
            records = @($records)
        }
        $manifest | ConvertTo-Json -Depth 32 | Set-Content -LiteralPath $manifestPath -Encoding utf8NoBOM
        $evaluation = Invoke-PerfCompare $RepositoryRoot $manifestPath $verdictPath
        if ($null -ne $evaluation -and $evaluation.ExitCode -ne 0) {
            throw "perfcompare rejected benchmark evidence: $($evaluation.Output)"
        }
    } finally {
        if ($null -eq $oldSidecarTimeout) {
            Remove-Item Env:ROTOR_SIDECAR_TIMEOUT -ErrorAction SilentlyContinue
        } else {
            $env:ROTOR_SIDECAR_TIMEOUT = $oldSidecarTimeout
        }
        Remove-TestOwnedWorkCopies $evidencePath $workRoot
    }
}

function Invoke-HarnessValidation {
    param([string]$RepositoryRoot)

    $validationRoot = if ($EvidenceDir) { [IO.Path]::GetFullPath($EvidenceDir) } else { Join-Path ([IO.Path]::GetTempPath()) ("rotor-bench-validation-" + [Guid]::NewGuid().ToString('N')) }
    [IO.Directory]::CreateDirectory($validationRoot) | Out-Null
    $fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("rotor-fixture-validation-" + [Guid]::NewGuid().ToString('N'))
    $workCopy = Join-Path $validationRoot 'work-validation'
    try {
        [IO.Directory]::CreateDirectory((Join-Path $fixtureRoot 'src')) | Out-Null
        [IO.Directory]::CreateDirectory((Join-Path $fixtureRoot 'node_modules')) | Out-Null
        [IO.Directory]::CreateDirectory((Join-Path $fixtureRoot '.rotor')) | Out-Null
        Set-Content -LiteralPath (Join-Path $fixtureRoot 'src/input.ts') -Value 'export const value = 1' -NoNewline
        Set-Content -LiteralPath (Join-Path $fixtureRoot 'node_modules/dependency.rbxtsc.tsbuildinfo') -Value 'preserve' -NoNewline
        Set-Content -LiteralPath (Join-Path $fixtureRoot '.rotor/cache.rbxtsc.tsbuildinfo') -Value 'preserve' -NoNewline
        $fixtureDigest = Get-TreeDigest $fixtureRoot
        if ((Get-OrderForPair 1) -ne 'AB' -or (Get-OrderForPair 2) -ne 'BA') {
            throw 'AB/BA ordering validation failed'
        }
        Copy-Item -LiteralPath $fixtureRoot -Destination $workCopy -Recurse -Force
        foreach ($name in @('out', 'out-test', 'out-tsc')) {
            [IO.Directory]::CreateDirectory((Join-Path $workCopy $name)) | Out-Null
        }
        Set-Content -LiteralPath (Join-Path $workCopy 'src/state.rbxtsc.tsbuildinfo') -Value 'delete' -NoNewline
        Reset-ColdState $workCopy
        if ((Test-Path -LiteralPath (Join-Path $workCopy 'out')) -or (Test-Path -LiteralPath (Join-Path $workCopy 'src/state.rbxtsc.tsbuildinfo')) -or -not (Test-Path -LiteralPath (Join-Path $workCopy 'node_modules/dependency.rbxtsc.tsbuildinfo')) -or -not (Test-Path -LiteralPath (Join-Path $workCopy '.rotor/cache.rbxtsc.tsbuildinfo'))) {
            throw 'cold cleanup allowlist validation failed'
        }
        if ((Get-TreeDigest $fixtureRoot) -ne $fixtureDigest) {
            throw 'canonical fixture immutability validation failed'
        }
        $environment = Get-EnvironmentSnapshot
        Assert-EnvironmentEqual $environment (Get-EnvironmentSnapshot)
        $records = [Collections.Generic.List[object]]::new()
        foreach ($pair in 1..2) {
            $order = Get-OrderForPair $pair
            $first = if ($order -eq 'AB') { 'baseline' } else { 'candidate' }
            $second = if ($first -eq 'baseline') { 'candidate' } else { 'baseline' }
            $result = @{ DurationMS = 1; ExitCode = 0; DiagnosticsDigest = 'diagnostics'; OutputTreeDigest = 'output' }
            $records.Add((New-ManifestRecord $pair 'cold' $order $first $result))
            $records.Add((New-ManifestRecord $pair 'cold' $order $second $result))
        }
        $manifestPath = Join-Path $validationRoot 'windows-build-performance-validation-manifest.json'
        $verdictPath = Join-Path $validationRoot 'windows-build-performance-validation-verdict.json'
        $manifest = [ordered]@{
            schema = 1
            machine = [ordered]@{ os = 'validation'; version = '1'; cpu = 'validation'; ram_bytes = 1; storage = 'validation'; power = 'validation'; run_order = @('AB', 'BA'); sidecar_timeout = '300s'; environment = $environment }
            baseline = [ordered]@{ revision = 'validation'; command = 'validation baseline' }
            candidate = [ordered]@{ revision = 'validation'; command = 'validation candidate' }
            records = @($records)
        }
        $manifest | ConvertTo-Json -Depth 32 | Set-Content -LiteralPath $manifestPath -Encoding utf8NoBOM
        $evaluation = Invoke-PerfCompare $RepositoryRoot $manifestPath $verdictPath
        if ($null -eq $evaluation -or $evaluation.ExitCode -eq 0 -or $evaluation.Output -notmatch 'cold pairs = 2, want at least 10') {
            throw 'perfcompare schema compatibility validation failed'
        }
        Write-Output 'ValidateOnly: AB/BA ordering, cleanup allowlist, fixture immutability, environment equality, manifest schema, and evaluator rejection passed.'
        Write-Output "ValidateOnly: manifest=$manifestPath"
        Write-Output "ValidateOnly: verdict=$verdictPath"
    } finally {
        if (Test-Path -LiteralPath $fixtureRoot) {
            Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
        }
        if (-not $EvidenceDir -and (Test-Path -LiteralPath $validationRoot)) {
            Remove-Item -LiteralPath $validationRoot -Recurse -Force
        }
    }
}

$repositoryRoot = Get-RepositoryRoot
if ($ValidateOnly) {
    Invoke-HarnessValidation $repositoryRoot
    exit 0
}

Invoke-RealBenchmark $repositoryRoot
