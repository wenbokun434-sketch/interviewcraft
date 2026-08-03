[CmdletBinding()]
param(
    [string]$GoBinary = "go",
    [string]$BinaryPath = ""
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$smokeRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("interviewcraft-fresh-install-" + [guid]::NewGuid().ToString("N"))
$ownsBinary = [string]::IsNullOrWhiteSpace($BinaryPath)
$savedEnvironment = @{}

function Invoke-NativeOutput {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )

    $output = @(& $FilePath @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath $($Arguments -join ' ') failed with exit code $LASTEXITCODE`n$($output -join [Environment]::NewLine)"
    }
    return $output
}

function Set-SmokeEnvironment {
    param([Parameter(Mandatory = $true)][string]$Name, [Parameter(Mandatory = $true)][string]$Value)

    $savedEnvironment[$Name] = [Environment]::GetEnvironmentVariable($Name, "Process")
    [Environment]::SetEnvironmentVariable($Name, $Value, "Process")
}

New-Item -ItemType Directory -Path $smokeRoot | Out-Null
try {
    if ($ownsBinary) {
        $extension = & $GoBinary env GOEXE
        if ($LASTEXITCODE -ne 0) {
            throw "$GoBinary env GOEXE failed"
        }
        $BinaryPath = Join-Path $smokeRoot ("interviewcraft" + $extension)
        & $GoBinary build -trimpath -o $BinaryPath ./cmd/interviewcraft
        if ($LASTEXITCODE -ne 0) {
            throw "$GoBinary build failed with exit code $LASTEXITCODE"
        }
    }

    $resolvedBinary = (Resolve-Path -LiteralPath $BinaryPath).Path
    Set-SmokeEnvironment -Name "INTERVIEWCRAFT_DATA_DIR" -Value (Join-Path $smokeRoot "data")
    Set-SmokeEnvironment -Name "RUNNER_MODE" -Value "disabled"
    Set-SmokeEnvironment -Name "COLUMNS" -Value "80"
    Set-SmokeEnvironment -Name "LINES" -Value "24"

    $help = Invoke-NativeOutput -FilePath $resolvedBinary -Arguments @("--help")
    if (($help -join "`n") -notmatch "init" -or ($help -join "`n") -notmatch "doctor") {
        throw "fresh binary help does not expose required commands"
    }

    Invoke-NativeOutput -FilePath $resolvedBinary -Arguments @("init") | Out-Null
    $run = Invoke-NativeOutput -FilePath $resolvedBinary -Arguments @("run", "--ascii", "--reduce-motion", "--no-color")
    if ($run.Count -ne 24 -or ($run -join "`n") -notmatch "InterviewCraft") {
        throw "fresh binary did not render a complete 80x24 Lite frame"
    }
    Invoke-NativeOutput -FilePath $resolvedBinary -Arguments @("export", "--help") | Out-Null
    Invoke-NativeOutput -FilePath $resolvedBinary -Arguments @("import", "--help") | Out-Null

    Write-Output "Fresh-install smoke passed with Runner disabled and local SQLite only."
}
finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], "Process")
    }
    $resolvedSmokeRoot = [System.IO.Path]::GetFullPath($smokeRoot)
    $resolvedTempRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    if ($resolvedSmokeRoot.StartsWith($resolvedTempRoot, [System.StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedSmokeRoot).StartsWith("interviewcraft-fresh-install-")) {
        Remove-Item -LiteralPath $resolvedSmokeRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
