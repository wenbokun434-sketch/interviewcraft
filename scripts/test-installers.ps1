[CmdletBinding()]
param(
    [string]$GoBinary = "go",
    [switch]$SkipLinuxContainer
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$repoRoot = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("interviewcraft-installer-fixture-" + [guid]::NewGuid().ToString("N"))
$version = "1.2.3"
$tag = "v$version"
$commit = "0123456789abcdef0123456789abcdef01234567"
$createdUTC = "2026-08-10T12:00:00Z"
$serverProcess = $null
$savedHome = $env:HOME
$savedUserProfile = $env:USERPROFILE

function Invoke-Native {
    param([string]$FilePath, [string[]]$Arguments)
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$FilePath failed with exit code $LASTEXITCODE" }
}

function Invoke-Installer {
    param([string[]]$Arguments, [switch]$ExpectFailure)
    $savedPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "install.ps1") @Arguments 2>&1
        $code = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $savedPreference
    }
    if ($ExpectFailure) {
        if ($code -eq 0) { throw "installer unexpectedly succeeded: $($output -join [Environment]::NewLine)" }
    }
    elseif ($code -ne 0) {
        throw "installer failed: $($output -join [Environment]::NewLine)"
    }
    [PSCustomObject]@{ Code = $code; Output = ($output -join [Environment]::NewLine) }
}

function Write-ValidManifest {
    param([string]$ReleaseDirectory)
    & (Join-Path $PSScriptRoot "release-manifest.ps1") -Mode Generate -DistDirectory $ReleaseDirectory -Version $version -Commit $commit -CreatedUTC $createdUTC | Out-Null
}

function Write-ValidZip {
    param([string]$ReleaseDirectory, [string]$Binary, [string]$Architecture)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $source = Join-Path $testRoot "zip-source"
    if (Test-Path -LiteralPath $source) { Remove-Item -LiteralPath $source -Recurse -Force }
    New-Item -ItemType Directory -Path $source | Out-Null
    Copy-Item -LiteralPath $Binary -Destination (Join-Path $source "interviewcraft.exe")
    $archive = Join-Path $ReleaseDirectory "interviewcraft_${version}_windows_${Architecture}.zip"
    Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    [IO.Compression.ZipFile]::CreateFromDirectory($source, $archive)
    $archive
}

function Write-ZipSlip {
    param([string]$ReleaseDirectory, [string]$Binary, [string]$Architecture)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = Join-Path $ReleaseDirectory "interviewcraft_${version}_windows_${Architecture}.zip"
    Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    $stream = [IO.File]::Open($archive, [IO.FileMode]::CreateNew)
    $zip = New-Object IO.Compression.ZipArchive($stream, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($entryName in @("interviewcraft.exe", "../escaped.exe")) {
            $entry = $zip.CreateEntry($entryName)
            $output = $entry.Open()
            try {
                $payload = if ($entryName -eq "interviewcraft.exe") { [IO.File]::ReadAllBytes($Binary) } else { [Text.Encoding]::UTF8.GetBytes("evil") }
                $output.Write($payload, 0, $payload.Length)
            }
            finally { $output.Dispose() }
        }
    }
    finally { $zip.Dispose(); $stream.Dispose() }
    $archive
}

function Assert-OneMarkerBlock {
    param([string]$Path)
    $content = [IO.File]::ReadAllText($Path)
    $matches = [regex]::Matches($content, [regex]::Escape("# >>> InterviewCraft PATH >>>"))
    if ($matches.Count -ne 1) { throw "PATH marker count=$($matches.Count), want 1" }
}

New-Item -ItemType Directory -Path $testRoot | Out-Null
Push-Location $repoRoot
try {
    $architecture = if ("$env:PROCESSOR_ARCHITEW6432 $env:PROCESSOR_ARCHITECTURE".ToUpperInvariant().Contains("ARM64")) { "arm64" } else { "amd64" }
    $extension = (& $GoBinary env GOEXE).Trim()
    if ($LASTEXITCODE -ne 0) { throw "go env GOEXE failed" }
    $binary = Join-Path $testRoot ("interviewcraft-fixture" + $extension)
    $savedGOOS = $env:GOOS
    $savedGOARCH = $env:GOARCH
    try {
        $env:GOOS = "windows"
        $env:GOARCH = $architecture
        $ldflags = "-X github.com/interviewcraft/interviewcraft/internal/version.ApplicationVersion=${version} " +
            "-X github.com/interviewcraft/interviewcraft/internal/version.GitCommit=${commit} " +
            "-X github.com/interviewcraft/interviewcraft/internal/version.BuildTime=${createdUTC}"
        Invoke-Native -FilePath $GoBinary -Arguments @("build", "-buildvcs=false", "-trimpath", "-ldflags", $ldflags, "-o", $binary, "./cmd/interviewcraft")
    }
    finally {
        $env:GOOS = $savedGOOS
        $env:GOARCH = $savedGOARCH
    }

    $webRoot = Join-Path $testRoot "web"
    $releaseDirectory = Join-Path $webRoot $tag
    New-Item -ItemType Directory -Path $releaseDirectory -Force | Out-Null
    foreach ($os in @("darwin", "linux", "windows")) {
        foreach ($arch in @("amd64", "arm64")) {
            $ext = if ($os -eq "windows") { ".zip" } else { ".tar.gz" }
            [IO.File]::WriteAllText((Join-Path $releaseDirectory "interviewcraft_${version}_${os}_${arch}${ext}"), "fixture ${os}/${arch}")
        }
    }
    if (-not $SkipLinuxContainer) {
        $linuxBinary = Join-Path $testRoot "interviewcraft-linux"
        $savedGOOS = $env:GOOS
        $savedGOARCH = $env:GOARCH
        try {
            $env:GOOS = "linux"
            $env:GOARCH = $architecture
            Invoke-Native -FilePath $GoBinary -Arguments @("build", "-buildvcs=false", "-trimpath", "-ldflags", $ldflags, "-o", $linuxBinary, "./cmd/interviewcraft")
        }
        finally {
            $env:GOOS = $savedGOOS
            $env:GOARCH = $savedGOARCH
        }
        $linuxSource = Join-Path $testRoot "linux-source"
        New-Item -ItemType Directory -Path $linuxSource | Out-Null
        Copy-Item -LiteralPath $linuxBinary -Destination (Join-Path $linuxSource "interviewcraft")
        $linuxArchive = Join-Path $releaseDirectory "interviewcraft_${version}_linux_${architecture}.tar.gz"
        $tar = (Get-Command tar.exe -ErrorAction Stop).Source
        Invoke-Native -FilePath $tar -Arguments @("-czf", $linuxArchive, "-C", $linuxSource, "interviewcraft")
    }
    $validArchive = Write-ValidZip -ReleaseDirectory $releaseDirectory -Binary $binary -Architecture $architecture
    [IO.File]::WriteAllText((Join-Path $releaseDirectory "checksums.txt"), "fixture checksums")
    [IO.File]::WriteAllText((Join-Path $releaseDirectory "interviewcraft_${version}.spdx.json"), '{"spdxVersion":"SPDX-2.3"}')
    [IO.File]::WriteAllText((Join-Path $releaseDirectory "release-manifest.sigstore.json"), "VALID FIXTURE BUNDLE")
    Write-ValidManifest -ReleaseDirectory $releaseDirectory
    $ollamaDirectory = Join-Path $webRoot "ollama\api"
    New-Item -ItemType Directory -Path $ollamaDirectory -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $ollamaDirectory "tags"), '{"models":[{"name":"fixture"}]}')

    $fakeCosign = Join-Path $testRoot "cosign-fixture.cmd"
    [IO.File]::WriteAllLines($fakeCosign, @(
        "@echo off",
        'findstr /C:"VALID FIXTURE BUNDLE" "%~3" >nul',
        "if errorlevel 1 exit /b 1",
        "exit /b 0"
    ), (New-Object Text.UTF8Encoding($false)))
    $cosignHash = (Get-FileHash -LiteralPath $fakeCosign -Algorithm SHA256).Hash.ToLowerInvariant()
    $fakeCosignShell = Join-Path $testRoot "cosign-fixture.sh"
    $fakeCosignContent = @'
#!/bin/sh
grep -q 'VALID FIXTURE BUNDLE' "$3"
'@
    [IO.File]::WriteAllText($fakeCosignShell, ($fakeCosignContent + "`n"), (New-Object Text.UTF8Encoding($false)))
    $cosignShellHash = (Get-FileHash -LiteralPath $fakeCosignShell -Algorithm SHA256).Hash.ToLowerInvariant()

    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    $listener.Stop()
    $serverProcess = Start-Process -FilePath "python" -ArgumentList @("-m", "http.server", "$port", "--bind", "127.0.0.1", "--directory", $webRoot) -PassThru -WindowStyle Hidden
    $baseURL = "http://127.0.0.1:$port"
    $ready = $false
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        try { Invoke-WebRequest -UseBasicParsing -Uri "$baseURL/$tag/release-manifest.txt" | Out-Null; $ready = $true; break } catch { Start-Sleep -Milliseconds 100 }
    }
    if (-not $ready) { throw "fixture HTTP server did not start" }

    $fixtureHome = Join-Path $testRoot "home"
    $installDir = Join-Path $testRoot "install\bin"
    $pathFile = Join-Path $fixtureHome "path-fixture.txt"
    $receipt = Join-Path $fixtureHome ".interviewcraft\install-receipt.txt"
    New-Item -ItemType Directory -Path $fixtureHome -Force | Out-Null
    [IO.File]::WriteAllText($pathFile, "user-content`r`n")
    $env:HOME = $fixtureHome
    $env:USERPROFILE = $fixtureHome
    $env:INTERVIEWCRAFT_INSTALL_TEST_MODE = "1"
    $env:INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL = $baseURL
    $env:INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH = $fakeCosign
    $env:INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256 = $cosignHash
    $env:INTERVIEWCRAFT_INSTALL_TEST_PATH_FILE = $pathFile
    $env:INTERVIEWCRAFT_INSTALL_TEST_RECEIPT = $receipt
    $env:INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES = "999999999"

    $arguments = @("-Version", $version, "-Profile", "lite", "-InstallDir", $installDir, "-SkipSetup")
    $mainArguments = @(
        "-Version", $version, "-Profile", "private-local", "-InstallDir", $installDir,
        "-Provider", "ollama", "-Endpoint", "$baseURL/ollama", "-Model", "fixture", "-NonInteractive"
    )
    $result = Invoke-Installer -Arguments $mainArguments
    if (-not $result.Output.Contains("[7/7]") -or -not (Test-Path -LiteralPath (Join-Path $installDir "interviewcraft.exe")) -or -not (Test-Path -LiteralPath $receipt)) {
        throw "main installer flow did not complete: $($result.Output)"
    }
    Assert-OneMarkerBlock -Path $pathFile
    Invoke-Installer -Arguments $mainArguments | Out-Null
    Assert-OneMarkerBlock -Path $pathFile
    Invoke-Installer -Arguments @("-Version", "9.9.9", "-InstallDir", $installDir, "-SkipSetup") -ExpectFailure | Out-Null

    $dataFile = Join-Path $fixtureHome ".interviewcraft\keep.txt"
    New-Item -ItemType Directory -Path (Split-Path -Parent $dataFile) -Force | Out-Null
    [IO.File]::WriteAllText($dataFile, "preserve me")
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "uninstall.ps1")
    if ($LASTEXITCODE -ne 0 -or (Test-Path -LiteralPath (Join-Path $installDir "interviewcraft.exe")) -or -not (Test-Path -LiteralPath $dataFile) -or [IO.File]::ReadAllText($pathFile).Contains("InterviewCraft PATH")) {
        throw "uninstall did not precisely remove binary/PATH while preserving data"
    }

    Invoke-Installer -Arguments @(
        "-Version", $version, "-Profile", "private-local", "-InstallDir", $installDir,
        "-Provider", "ollama", "-Endpoint", "http://127.0.0.1:1", "-Model", "unreachable", "-NonInteractive"
    ) -ExpectFailure | Out-Null
    if (Test-Path -LiteralPath (Join-Path $installDir "interviewcraft.exe")) { throw "setup failure left a new binary behind" }
    if ([IO.File]::ReadAllText($pathFile).Contains("InterviewCraft PATH")) { throw "setup failure changed PATH" }

    $validManifest = [IO.File]::ReadAllText((Join-Path $releaseDirectory "release-manifest.txt"))
    $missingPlatformManifest = (($validManifest -split "`r?`n") | Where-Object { $_ -notmatch "^asset`twindows`t${architecture}`t" }) -join "`n"
    [IO.File]::WriteAllText((Join-Path $releaseDirectory "release-manifest.txt"), ($missingPlatformManifest.TrimEnd() + "`n"))
    Invoke-Installer -Arguments $arguments -ExpectFailure | Out-Null
    [IO.File]::WriteAllText((Join-Path $releaseDirectory "release-manifest.txt"), $validManifest)

    [IO.File]::WriteAllText((Join-Path $releaseDirectory "release-manifest.sigstore.json"), "INVALID BUNDLE")
    Invoke-Installer -Arguments $arguments -ExpectFailure | Out-Null
    if (Test-Path -LiteralPath (Join-Path $installDir "interviewcraft.exe")) { throw "signature failure installed a binary" }
    [IO.File]::WriteAllText((Join-Path $releaseDirectory "release-manifest.sigstore.json"), "VALID FIXTURE BUNDLE")

    [IO.File]::AppendAllText($validArchive, "tamper")
    Invoke-Installer -Arguments $arguments -ExpectFailure | Out-Null
    $validArchive = Write-ValidZip -ReleaseDirectory $releaseDirectory -Binary $binary -Architecture $architecture
    Write-ValidManifest -ReleaseDirectory $releaseDirectory

    [IO.File]::WriteAllText($validArchive, "truncated but signed")
    Write-ValidManifest -ReleaseDirectory $releaseDirectory
    Invoke-Installer -Arguments $arguments -ExpectFailure | Out-Null

    $validArchive = Write-ZipSlip -ReleaseDirectory $releaseDirectory -Binary $binary -Architecture $architecture
    Write-ValidManifest -ReleaseDirectory $releaseDirectory
    Invoke-Installer -Arguments $arguments -ExpectFailure | Out-Null
    if (Test-Path -LiteralPath (Join-Path $testRoot "escaped.exe")) { throw "Zip Slip escaped extraction root" }

    $validArchive = Write-ValidZip -ReleaseDirectory $releaseDirectory -Binary $binary -Architecture $architecture
    Write-ValidManifest -ReleaseDirectory $releaseDirectory
    $env:INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES = "0"
    Invoke-Installer -Arguments $arguments -ExpectFailure | Out-Null
    $env:INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES = "999999999"

    $blockedParent = Join-Path $testRoot "blocked-parent"
    [IO.File]::WriteAllText($blockedParent, "not a directory")
    Invoke-Installer -Arguments @("-Version", $version, "-InstallDir", (Join-Path $blockedParent "bin"), "-SkipSetup") -ExpectFailure | Out-Null

    $env:INTERVIEWCRAFT_INSTALL_TEST_PATH_FILE = Join-Path $blockedParent "path.txt"
    Invoke-Installer -Arguments $arguments -ExpectFailure | Out-Null
    if (Test-Path -LiteralPath (Join-Path $installDir "interviewcraft.exe")) { throw "PATH failure left a new binary behind" }
    $env:INTERVIEWCRAFT_INSTALL_TEST_PATH_FILE = $pathFile

    $matrix = [IO.File]::ReadAllText((Join-Path $PSScriptRoot "cosign-v3.1.3-sha256.txt"))
    $unixHashes = @(
        "2347488e5d5b25336644024dfeca5601b190e91197a71a917bda44744aff106c",
        "5cf948c2f4dfe59687bdd0b8523709067383e03982cc543475c8a7dc70e92a76",
        "4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71",
        "c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a"
    )
    $windowsHash = "9fe59be0eca1271873ce019061335eb1ac419b7059202e797828467ddabe33be"
    $shellInstaller = [IO.File]::ReadAllText((Join-Path $PSScriptRoot "install.sh"))
    foreach ($hash in $unixHashes) {
        if (-not $matrix.Contains($hash) -or -not $shellInstaller.Contains($hash)) {
            throw "Cosign v3.1.3 matrix drifted for $hash"
        }
    }
    if (-not $matrix.Contains($windowsHash) -or -not [IO.File]::ReadAllText((Join-Path $PSScriptRoot "install.ps1")).Contains($windowsHash)) {
        throw "Cosign v3.1.3 Windows matrix drifted"
    }

    if (-not $SkipLinuxContainer) {
        $linuxWork = Join-Path $testRoot "linux-work"
        New-Item -ItemType Directory -Path $linuxWork | Out-Null
        $containerScript = @'
set -eu
mkdir -p /work/tmp /work/home /work/bin
cp /fixture-cosign /work/tmp/cosign-fixture
chmod 700 /work/tmp/cosign-fixture
export HOME=/work/home
export TMPDIR=/work/tmp
export SHELL=/bin/sh
export INTERVIEWCRAFT_INSTALL_TEST_MODE=1
export INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL=file:///fixture
export INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH=/work/tmp/cosign-fixture
export INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256=__COSIGN_SHA__
export INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES=999999999
sh /repo/scripts/install.sh --version 1.2.3 --profile lite --install-dir /work/bin --skip-setup
sh /repo/scripts/install.sh --version 1.2.3 --profile lite --install-dir /work/bin --skip-setup
grep -c '^# >>> InterviewCraft PATH >>>$' /work/home/.profile > /work/path-marker-count
grep -qx '1' /work/path-marker-count
mkdir -p /work/home/.interviewcraft
printf 'preserve me\n' > /work/home/.interviewcraft/keep.txt
sh /repo/scripts/uninstall.sh
test ! -e /work/bin/interviewcraft
test -f /work/home/.interviewcraft/keep.txt
! grep -q 'InterviewCraft PATH' /work/home/.profile
'@
        $containerScript = $containerScript.Replace("__COSIGN_SHA__", $cosignShellHash)
        Invoke-Native -FilePath "docker" -Arguments @(
            "run", "--rm", "--user", "65532:65532", "--entrypoint", "/bin/sh",
            "-v", "${repoRoot}:/repo:ro", "-v", "${webRoot}:/fixture:ro",
            "-v", "${fakeCosignShell}:/fixture-cosign:ro", "-v", "${linuxWork}:/work",
            "interviewcraft-runner:local", "-c", $containerScript
        )
        if (Test-Path -LiteralPath (Join-Path $linuxWork "bin\interviewcraft")) { throw "Linux uninstall left the binary behind" }
        if (-not (Test-Path -LiteralPath (Join-Path $linuxWork "home\.interviewcraft\keep.txt"))) { throw "Linux uninstall removed user data" }
        Write-Output "InterviewCraft Linux container installer smoke passed."
    }
    Write-Output "InterviewCraft PowerShell installer fixture passed."
}
finally {
    if ($null -ne $serverProcess -and -not $serverProcess.HasExited) { Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue }
    Pop-Location
    foreach ($name in @(
        "INTERVIEWCRAFT_INSTALL_TEST_MODE", "INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL",
        "INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH", "INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256",
        "INTERVIEWCRAFT_INSTALL_TEST_PATH_FILE", "INTERVIEWCRAFT_INSTALL_TEST_RECEIPT",
        "INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES"
    )) { [Environment]::SetEnvironmentVariable($name, $null, "Process") }
    [Environment]::SetEnvironmentVariable("HOME", $savedHome, "Process")
    [Environment]::SetEnvironmentVariable("USERPROFILE", $savedUserProfile, "Process")
    $resolved = [IO.Path]::GetFullPath($testRoot)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if ($resolved.StartsWith($temp, [StringComparison]::OrdinalIgnoreCase) -and (Split-Path -Leaf $resolved).StartsWith("interviewcraft-installer-fixture-")) {
        Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction SilentlyContinue
    }
}
