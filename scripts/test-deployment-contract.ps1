[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$repoRoot = Split-Path -Parent $PSScriptRoot

function Require-Text {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$Pattern,
        [Parameter(Mandatory = $true)][string]$Description
    )
    if ($Text -notmatch $Pattern) {
        throw "deployment contract is missing $Description"
    }
}

function Test-PowerShellSyntax {
    param([Parameter(Mandatory = $true)][string]$Path)
    $tokens = $null
    $errors = $null
    [void][Management.Automation.Language.Parser]::ParseFile($Path, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) {
        throw "PowerShell syntax failed for $Path`: $($errors[0].Message)"
    }
}

Push-Location $repoRoot
try {
    foreach ($script in @(
        "scripts/install.ps1",
        "scripts/uninstall.ps1",
        "scripts/test-deployment-e2e.ps1",
        "scripts/test-deployment-contract.ps1",
        "scripts/test-release-quality.ps1"
    )) {
        Test-PowerShellSyntax -Path (Join-Path $repoRoot $script)
    }

    $workflow = [IO.File]::ReadAllText((Join-Path $repoRoot ".github/workflows/deployment.yml"))
    foreach ($platform in @("windows-latest", "ubuntu-latest", "macos-latest")) {
        Require-Text $workflow ([regex]::Escape($platform)) "the $platform clean deployment job"
    }
    Require-Text $workflow "actions/checkout@v6" "the checkout v6 action"
    Require-Text $workflow "actions/setup-go@v7" "the setup-go v7 action"
    Require-Text $workflow "actions/upload-artifact@v7" "the upload-artifact v7 action"
    Require-Text $workflow "test-deployment-e2e\.ps1 -GoBinary go -EvidencePath" "the documented platform lifecycle command"
    Require-Text $workflow "test-deployment-e2e\.ps1 -GoBinary go -FullPractice -EvidencePath" "the Full Practice isolation command"
    Require-Text $workflow "deployment-evidence-\$\{\{ runner\.os \}\}" "per-platform evidence artifacts"

    $harness = [IO.File]::ReadAllText((Join-Path $repoRoot "scripts/test-deployment-e2e.ps1"))
    foreach ($field in @("application_versions", "git_commit", "worktree_dirty", "started_utc", "finished_utc", "duration_seconds")) {
        Require-Text $harness ([regex]::Escape($field)) "the $field evidence field"
    }
    foreach ($state in @(
        "clean_install_setup_update_rollback_uninstall",
        "complete_lite_training_and_restart",
        "full_setup",
        "runner_release",
        "runner_isolation"
    )) {
        Require-Text $harness ([regex]::Escape($state)) "the $state result"
    }

    $readme = [IO.File]::ReadAllText((Join-Path $repoRoot "README.md"))
    Require-Text $readme "powershell -NoProfile -ExecutionPolicy Bypass -File \.\\scripts\\test-deployment-e2e\.ps1" "the Windows copy/paste deployment acceptance command"
    Require-Text $readme "pwsh -NoProfile -File \./scripts/test-deployment-e2e\.ps1" "the POSIX copy/paste deployment acceptance command"

    $release = [IO.File]::ReadAllText((Join-Path $repoRoot ".goreleaser.yaml"))
    foreach ($asset in @("scripts/install.ps1", "scripts/install.sh", "scripts/uninstall.ps1", "scripts/uninstall.sh")) {
        Require-Text $release ([regex]::Escape($asset)) "$asset in release archives"
    }

    $shell = Get-Command sh -ErrorAction SilentlyContinue
    if ($null -ne $shell) {
        foreach ($script in @("scripts/install.sh", "scripts/uninstall.sh", "scripts/test-installers-posix.sh")) {
            & $shell.Source -n $script
            if ($LASTEXITCODE -ne 0) {
                throw "POSIX shell syntax failed for $script"
            }
        }
    }
    Write-Output "Deployment workflow, evidence, documentation, and script contracts passed."
}
finally {
    Pop-Location
}
