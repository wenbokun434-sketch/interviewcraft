[CmdletBinding()]
param(
    [string]$GoBinary = "go"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$repoRoot = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("interviewcraft-release-metadata-" + [guid]::NewGuid().ToString("N"))
$manifestScript = Join-Path $PSScriptRoot "release-manifest.ps1"
$version = "9.8.7"
$commit = "0123456789abcdef0123456789abcdef01234567"
$createdUTC = "2026-08-10T12:00:00Z"

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Expect-Failure {
    param([Parameter(Mandatory = $true)][scriptblock]$Action)
    $failed = $false
    try {
        & $Action
    }
    catch {
        $failed = $true
    }
    if (-not $failed) {
        throw "expected release metadata operation to fail"
    }
}

New-Item -ItemType Directory -Path $testRoot | Out-Null
Push-Location $repoRoot
try {
    $dist = Join-Path $testRoot "dist"
    New-Item -ItemType Directory -Path $dist | Out-Null
    $checksumLines = New-Object System.Collections.Generic.List[string]
    foreach ($targetOS in @("darwin", "linux", "windows")) {
        foreach ($targetArch in @("amd64", "arm64")) {
            $extension = ".tar.gz"
            if ($targetOS -eq "windows") {
                $extension = ".zip"
            }
            $filename = "interviewcraft_${version}_${targetOS}_${targetArch}${extension}"
            $path = Join-Path $dist $filename
            [System.IO.File]::WriteAllText($path, "fixture ${targetOS}/${targetArch}", (New-Object System.Text.UTF8Encoding($false)))
            $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
            $checksumLines.Add("${hash}  ${filename}")
        }
    }
    [System.IO.File]::WriteAllLines((Join-Path $dist "checksums.txt"), $checksumLines, (New-Object System.Text.UTF8Encoding($false)))
    [System.IO.File]::WriteAllText(
        (Join-Path $dist "interviewcraft_${version}.spdx.json"),
        '{"spdxVersion":"SPDX-2.3","name":"InterviewCraft fixture"}',
        (New-Object System.Text.UTF8Encoding($false))
    )

    & $manifestScript -Mode Generate -DistDirectory $dist -Version $version -Commit $commit -CreatedUTC $createdUTC
    & $manifestScript -Mode Verify -DistDirectory $dist -Version $version -Commit $commit

    $asset = Join-Path $dist "interviewcraft_${version}_linux_amd64.tar.gz"
    $originalAsset = [System.IO.File]::ReadAllBytes($asset)
    [System.IO.File]::WriteAllText($asset, "tampered", (New-Object System.Text.UTF8Encoding($false)))
    Expect-Failure { & $manifestScript -Mode Verify -DistDirectory $dist -Version $version -Commit $commit }
    [System.IO.File]::WriteAllBytes($asset, $originalAsset)

    $manifestPath = Join-Path $dist "release-manifest.txt"
    $originalManifest = [System.IO.File]::ReadAllText($manifestPath)
    $upperHashManifest = [regex]::Replace($originalManifest, '[0-9a-f]{64}', { param($match) $match.Value.ToUpperInvariant() }, 1)
    [System.IO.File]::WriteAllText($manifestPath, $upperHashManifest, (New-Object System.Text.UTF8Encoding($false)))
    Expect-Failure { & $manifestScript -Mode Verify -DistDirectory $dist }
    [System.IO.File]::WriteAllText($manifestPath, $originalManifest, (New-Object System.Text.UTF8Encoding($false)))

    $extension = (& $GoBinary env GOEXE).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "$GoBinary env GOEXE failed"
    }
    $binary = Join-Path $testRoot ("interviewcraft-version-fixture" + $extension)
    $ldflags = "-X github.com/interviewcraft/interviewcraft/internal/version.ApplicationVersion=${version} " +
        "-X github.com/interviewcraft/interviewcraft/internal/version.GitCommit=${commit} " +
        "-X github.com/interviewcraft/interviewcraft/internal/version.BuildTime=${createdUTC}"
    Invoke-Native -FilePath $GoBinary -Arguments @("build", "-buildvcs=false", "-trimpath", "-ldflags", $ldflags, "-o", $binary, "./cmd/interviewcraft")
    $versionJSON = (& $binary version --json) -join "`n"
    if ($LASTEXITCODE -ne 0) {
        throw "version fixture failed"
    }
    $info = $versionJSON | ConvertFrom-Json
    if ($info.schema_version -ne "interviewcraft-version-v1" -or
        $info.version -ne $version -or $info.git_commit -ne $commit -or
        $info.build_time -ne $createdUTC -or
        [string]::IsNullOrWhiteSpace($info.goos) -or [string]::IsNullOrWhiteSpace($info.goarch)) {
        throw "injected version metadata does not match"
    }

    $workflow = [System.IO.File]::ReadAllText((Join-Path $repoRoot ".github/workflows/release.yml"))
    foreach ($required in @(
        "id-token: write",
        "attestations: write",
        "sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6",
        "cosign-release: 'v3.1.3'",
        "https://token.actions.githubusercontent.com",
        "https://github.com/wenbokun434-sketch/interviewcraft/.github/workflows/release.yml@refs/tags/",
        "--certificate-identity",
        "--draft",
        "--draft=false"
    )) {
        if (-not $workflow.Contains($required)) {
            throw "release workflow is missing required policy: $required"
        }
    }
    $goreleaser = [System.IO.File]::ReadAllText((Join-Path $repoRoot ".goreleaser.yaml"))
    foreach ($required in @(
        "internal/version.ApplicationVersion={{ .Version }}",
        "internal/version.GitCommit={{ .Commit }}",
        "internal/version.BuildTime={{ .Date }}",
        "artifacts: archive",
        '${artifact}.spdx.json'
    )) {
        if (-not $goreleaser.Contains($required)) {
            throw "GoReleaser configuration is missing: $required"
        }
    }

    Write-Output "InterviewCraft release metadata fixture passed."
}
finally {
    Pop-Location
    $resolvedTestRoot = [System.IO.Path]::GetFullPath($testRoot)
    $resolvedTempRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    if ($resolvedTestRoot.StartsWith($resolvedTempRoot, [StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedTestRoot).StartsWith("interviewcraft-release-metadata-")) {
        Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
