[CmdletBinding()]
param(
    [string]$GoBinary = "go",
    [string]$EvidencePath = "",
    [switch]$FullPractice
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$repoRoot = Split-Path -Parent $PSScriptRoot
$started = [DateTime]::UtcNow
$timer = [Diagnostics.Stopwatch]::StartNew()
$failure = $null
$activeResult = ""
$results = [ordered]@{
    clean_install_setup_update_rollback_uninstall = "not_run"
    complete_lite_training_and_restart = "not_run"
    full_setup = if ($FullPractice) { "not_run" } else { "not_requested" }
    runner_release = if ($FullPractice) { "not_run" } else { "not_requested" }
    runner_isolation = if ($FullPractice) { "not_run" } else { "not_requested" }
}
$saved = @{
    Deployment = $env:INTERVIEWCRAFT_DEPLOYMENT_E2E
    GoBinary = $env:GO_BINARY
    GoCache = $env:GOCACHE
    CachePath = $env:PSModuleAnalysisCachePath
}

function Invoke-Native {
    param([string]$FilePath, [string[]]$Arguments)
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$FilePath $($Arguments -join ' ') failed with exit code $LASTEXITCODE" }
}

Push-Location $repoRoot
try {
    $env:INTERVIEWCRAFT_DEPLOYMENT_E2E = "1"
    $env:GO_BINARY = $GoBinary
    $env:GOCACHE = Join-Path ([IO.Path]::GetTempPath()) "interviewcraft-go-build-cache"
    $env:PSModuleAnalysisCachePath = Join-Path ([IO.Path]::GetTempPath()) "interviewcraft-powershell-module-cache"
    $activeResult = "clean_install_setup_update_rollback_uninstall"
    $results[$activeResult] = "running"
    Invoke-Native -FilePath $GoBinary -Arguments @("test", "-count=1", "-v", "./internal/e2e", "-run", "^TestCleanDeploymentInstallSetupUpdateRollbackUninstall$")
    $results.clean_install_setup_update_rollback_uninstall = "passed"
    $activeResult = "complete_lite_training_and_restart"
    $results[$activeResult] = "running"
    Invoke-Native -FilePath $GoBinary -Arguments @("test", "-count=1", "-v", "./internal/e2e", "-run", "^TestLiteMVPJourneyFromFreshInitThroughTransfer$")
    $results.complete_lite_training_and_restart = "passed"
    if ($FullPractice) {
        $activeResult = "full_setup"
        $results[$activeResult] = "running"
        Invoke-Native -FilePath $GoBinary -Arguments @(
            "test", "-count=1", "./internal/setup",
            "-run", "TestSetupFullEnablesOnlyProvisionedSignedRunner|TestSetupFullFailureKeepsExistingConfigDisabled|TestSetupFullReportsRunnerStagesAndCancellation|TestSetupFullResumeRevalidatesProvisionedMetadataWithoutRepull"
        )
        $results.full_setup = "passed"
        $activeResult = "runner_release"
        $results[$activeResult] = "running"
        & (Join-Path $PSScriptRoot "test-runner-release.ps1")
        $results.runner_release = "passed"
        $activeResult = "runner_isolation"
        $results[$activeResult] = "running"
        & (Join-Path $PSScriptRoot "test-runner-isolation.ps1") -GoBinary $GoBinary
        $results.runner_isolation = "passed"
    }
    $activeResult = ""
}
catch {
    if (-not [string]::IsNullOrWhiteSpace($activeResult) -and $results[$activeResult] -ne "passed") {
        $results[$activeResult] = "failed"
    }
    $failure = $_
}
finally {
    $timer.Stop()
    $commit = (& git -c "safe.directory=$repoRoot" rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw "git rev-parse HEAD failed" }
    $dirtyLines = @(& git -c "safe.directory=$repoRoot" status --porcelain --untracked-files=no)
    if ($LASTEXITCODE -ne 0) { throw "git status failed" }
    $goVersion = (& $GoBinary version).Trim()
    if ($LASTEXITCODE -ne 0) { throw "$GoBinary version failed" }
    if ([string]::IsNullOrWhiteSpace($EvidencePath)) {
        $EvidencePath = Join-Path ([IO.Path]::GetTempPath()) "interviewcraft-deployment-evidence.json"
    }
    $resolvedEvidence = [IO.Path]::GetFullPath($EvidencePath)
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $resolvedEvidence) | Out-Null
    $evidence = [ordered]@{
        schema = "interviewcraft-deployment-evidence-v1"
        platform = [Environment]::OSVersion.Platform.ToString()
        architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
        application_versions = @("1.0.0", "1.1.0")
        go_version = $goVersion
        git_commit = $commit
        worktree_dirty = ($dirtyLines.Count -ne 0)
        started_utc = $started.ToString("o")
        finished_utc = [DateTime]::UtcNow.ToString("o")
        duration_seconds = [Math]::Round($timer.Elapsed.TotalSeconds, 3)
        results = $results
    }
    [IO.File]::WriteAllText($resolvedEvidence, (($evidence | ConvertTo-Json -Depth 5) + "`n"), (New-Object Text.UTF8Encoding($false)))
    Pop-Location
    $env:INTERVIEWCRAFT_DEPLOYMENT_E2E = $saved.Deployment
    $env:GO_BINARY = $saved.GoBinary
    $env:GOCACHE = $saved.GoCache
    $env:PSModuleAnalysisCachePath = $saved.CachePath
}
if ($null -ne $failure) {
    throw $failure
}
Write-Output "Deployment E2E passed in $($evidence.duration_seconds)s; evidence: $resolvedEvidence"
