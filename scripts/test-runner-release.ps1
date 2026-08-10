[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$repoRoot = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("interviewcraft-runner-release-" + [guid]::NewGuid().ToString("N"))
$manifestScript = Join-Path $PSScriptRoot "runner-manifest.ps1"
$version = "9.8.7"
$commit = "0123456789abcdef0123456789abcdef01234567"
$amd64 = "sha256:" + ("a" * 64)
$arm64 = "sha256:" + ("b" * 64)

function Expect-Failure {
    param([Parameter(Mandatory = $true)][scriptblock]$Action)
    $failed = $false
    try { & $Action } catch { $failed = $true }
    if (-not $failed) { throw "expected Runner release verification to fail" }
}

New-Item -ItemType Directory -Path $testRoot | Out-Null
try {
    & $manifestScript -Mode Generate -DistDirectory $testRoot -Version $version -Commit $commit -CreatedUTC "2026-08-11T00:00:00Z" -AMD64Digest $amd64 -ARM64Digest $arm64
    & $manifestScript -Mode Verify -DistDirectory $testRoot -Version $version -Commit $commit

    $manifestPath = Join-Path $testRoot "runner-manifest.txt"
    $valid = [IO.File]::ReadAllText($manifestPath)
    foreach ($invalid in @(
        $valid.Replace("image`tlinux`tarm64", "image`tlinux`tamd64"),
        $valid.Replace($amd64, $amd64.ToUpperInvariant()),
        $valid.Replace("ghcr.io/wenbokun434-sketch/interviewcraft-runner", "ghcr.io/attacker/runner"),
        $valid.Replace("65532:65532", "0:0"),
        $valid.Replace("interviewcraft-runner-response-v1", "runner-v0")
    )) {
        [IO.File]::WriteAllText($manifestPath, $invalid, [Text.UTF8Encoding]::new($false))
        Expect-Failure { & $manifestScript -Mode Verify -DistDirectory $testRoot -Version $version -Commit $commit }
    }
    [IO.File]::WriteAllText($manifestPath, $valid, [Text.UTF8Encoding]::new($false))

    $workflow = [IO.File]::ReadAllText((Join-Path $repoRoot ".github/workflows/release.yml"))
    foreach ($required in @(
        "packages: write",
        "docker/setup-qemu-action@ce360397dd3f832beb865e1373c09c0e9f86d70a",
        "docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c",
        "docker/login-action@65b78e6e13532edd9afa3aa52ac7964289d1a9c1",
        "docker/build-push-action@f2a1d5e99d037542a71f64918e516c093c6f3fc4",
        "platforms: linux/amd64",
        "platforms: linux/arm64",
        "cosign sign --yes",
        "cosign verify --certificate-identity",
        "runner-manifest.sigstore.json"
    )) {
        if (-not $workflow.Contains($required)) { throw "release workflow is missing Runner gate: $required" }
    }
    if ($workflow -match 'docker/(setup-qemu-action|setup-buildx-action|login-action|build-push-action)@v[0-9]') {
        throw "Runner release actions must be pinned to commits"
    }

    $dockerfile = [IO.File]::ReadAllText((Join-Path $repoRoot "docker/runner/Dockerfile"))
    foreach ($required in @("io.interviewcraft.runner", "io.interviewcraft.version", "io.interviewcraft.protocol", "USER 65532:65532")) {
        if (-not $dockerfile.Contains($required)) { throw "Runner Dockerfile is missing policy metadata: $required" }
    }
    Write-Output "Runner release metadata fixture passed."
}
finally {
    $resolvedTestRoot = [IO.Path]::GetFullPath($testRoot)
    $resolvedTempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if ($resolvedTestRoot.StartsWith($resolvedTempRoot, [StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedTestRoot).StartsWith("interviewcraft-runner-release-")) {
        Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
