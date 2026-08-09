[CmdletBinding()]
param(
    [string]$GoBinary = "go",
    [switch]$SkipRunnerIsolation
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$buildRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("interviewcraft-release-gate-" + [guid]::NewGuid().ToString("N"))

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

Push-Location $repoRoot
New-Item -ItemType Directory -Path $buildRoot | Out-Null
try {
    $env:CGO_ENABLED = "0"
    if ([string]::IsNullOrWhiteSpace($env:GOCACHE)) {
        $env:GOCACHE = Join-Path ([System.IO.Path]::GetTempPath()) "interviewcraft-go-build-cache"
    }

    $goRoot = (& $GoBinary env GOROOT).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($goRoot)) {
        throw "$GoBinary env GOROOT failed"
    }
    $goFmt = Join-Path $goRoot "bin/gofmt"
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        $goFmt += ".exe"
    }
    $goFiles = @(
        Get-ChildItem -Path "cmd", "internal", "docker/runner/agent" -Recurse -Filter "*.go" -File |
            ForEach-Object { $_.FullName }
    )
    $unformatted = @(& $goFmt -l @goFiles)
    if ($LASTEXITCODE -ne 0) {
        throw "gofmt check failed"
    }
    if ($unformatted.Count -ne 0) {
        throw "gofmt required for: $($unformatted -join ', ')"
    }

    Invoke-Native -FilePath "git" -Arguments @("diff", "--check")
    Invoke-Native -FilePath $GoBinary -Arguments @("mod", "verify")
    Invoke-Native -FilePath $GoBinary -Arguments @("vet", "./...")
    Invoke-Native -FilePath $GoBinary -Arguments @("test", "-count=1", "-cover", "./...")
    Invoke-Native -FilePath $GoBinary -Arguments @("test", "-count=1", "./internal/db", "-run", "Migration|Migrations")

    Push-Location "docker/runner/agent"
    try {
        Invoke-Native -FilePath $GoBinary -Arguments @("mod", "verify")
        Invoke-Native -FilePath $GoBinary -Arguments @("vet", "./...")
        Invoke-Native -FilePath $GoBinary -Arguments @("test", "-count=1", "-cover", "./...")
    }
    finally {
        Pop-Location
    }

    $extension = (& $GoBinary env GOEXE).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "$GoBinary env GOEXE failed"
    }
    $binary = Join-Path $buildRoot ("interviewcraft" + $extension)
    Invoke-Native -FilePath $GoBinary -Arguments @("build", "-trimpath", "-o", $binary, "./cmd/interviewcraft")
    & (Join-Path $PSScriptRoot "test-fresh-install.ps1") -GoBinary $GoBinary -BinaryPath $binary
    & (Join-Path $PSScriptRoot "test-release-metadata.ps1") -GoBinary $GoBinary
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($SkipRunnerIsolation) {
            & (Join-Path $PSScriptRoot "test-installers.ps1") -GoBinary $GoBinary -SkipLinuxContainer
        }
        else {
            & (Join-Path $PSScriptRoot "test-installers.ps1") -GoBinary $GoBinary
        }
    }
    else {
        $savedGoBinary = $env:GO_BINARY
        try {
            $env:GO_BINARY = $GoBinary
            Invoke-Native -FilePath "sh" -Arguments @((Join-Path $PSScriptRoot "test-installers-posix.sh"))
        }
        finally {
            [Environment]::SetEnvironmentVariable("GO_BINARY", $savedGoBinary, "Process")
        }
    }

    $savedGOOS = [Environment]::GetEnvironmentVariable("GOOS", "Process")
    $savedGOARCH = [Environment]::GetEnvironmentVariable("GOARCH", "Process")
    try {
        $releaseRoot = Join-Path $buildRoot "release-matrix"
        New-Item -ItemType Directory -Path $releaseRoot | Out-Null
        foreach ($targetOS in @("windows", "linux", "darwin")) {
            foreach ($targetArch in @("amd64", "arm64")) {
                $env:GOOS = $targetOS
                $env:GOARCH = $targetArch
                $targetExtension = ""
                if ($targetOS -eq "windows") {
                    $targetExtension = ".exe"
                }
                $targetBinary = Join-Path $releaseRoot ("interviewcraft_${targetOS}_${targetArch}${targetExtension}")
                Invoke-Native -FilePath $GoBinary -Arguments @("build", "-trimpath", "-o", $targetBinary, "./cmd/interviewcraft")
            }
        }
    }
    finally {
        [Environment]::SetEnvironmentVariable("GOOS", $savedGOOS, "Process")
        [Environment]::SetEnvironmentVariable("GOARCH", $savedGOARCH, "Process")
    }

    if (-not $SkipRunnerIsolation) {
        & (Join-Path $PSScriptRoot "test-runner-isolation.ps1") -GoBinary $GoBinary
        if ($LASTEXITCODE -ne 0) {
            throw "Runner isolation gate failed with exit code $LASTEXITCODE"
        }
    }

    Invoke-Native -FilePath "git" -Arguments @("diff", "--check")
    Write-Output "InterviewCraft release quality gate passed."
}
finally {
    Pop-Location
    $resolvedBuildRoot = [System.IO.Path]::GetFullPath($buildRoot)
    $resolvedTempRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    if ($resolvedBuildRoot.StartsWith($resolvedTempRoot, [System.StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedBuildRoot).StartsWith("interviewcraft-release-gate-")) {
        Remove-Item -LiteralPath $resolvedBuildRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
