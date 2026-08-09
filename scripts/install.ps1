[CmdletBinding()]
param(
    [string]$Version = "latest",
    [ValidateSet("lite", "private-local", "full")]
    [string]$Profile = "lite",
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\InterviewCraft\bin"),
    [ValidateSet("", "openai-compatible", "ollama")]
    [string]$Provider = "",
    [string]$Endpoint = "",
    [string]$Model = "",
    [switch]$ApiKeyStdin,
    [switch]$NonInteractive,
    [switch]$SkipSetup
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$repo = "https://github.com/wenbokun434-sketch/interviewcraft"
$cosignVersion = "v3.1.3"
$oidcIssuer = "https://token.actions.githubusercontent.com"
$manifestHeader = "interviewcraft-release-v1"
$receiptHeader = "interviewcraft-install-receipt-v1"
$pathBegin = "# >>> InterviewCraft PATH >>>"
$pathEnd = "# <<< InterviewCraft PATH <<<"
$testMode = $env:INTERVIEWCRAFT_INSTALL_TEST_MODE -eq "1"

function Write-Stage {
    param([int]$Current, [int]$Total, [string]$Message)
    Write-Host "[$Current/$Total] $Message"
}

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-Download {
    param([Parameter(Mandatory = $true)][string]$Uri, [Parameter(Mandatory = $true)][string]$Path)
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $Path
    }
    catch {
        Remove-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
        throw "download failed: $Uri. Check network/proxy settings and retry."
    }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf) -or (Get-Item -LiteralPath $Path).Length -le 0) {
        throw "download was empty: $Uri"
    }
}

function Resolve-ReleaseVersion {
    param([string]$Requested)
    if ($Requested -ne "latest") {
        $normalized = $Requested.TrimStart("v")
        if ($normalized -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$') {
            throw "version must be latest or a semantic version"
        }
        return $normalized
    }
    if ($testMode) {
        throw "test fixture must request an explicit version"
    }
    try {
        $latest = Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/wenbokun434-sketch/interviewcraft/releases/latest"
        $normalized = ([string]$latest.tag_name).TrimStart("v")
    }
    catch {
        throw "could not resolve latest release. Specify -Version explicitly and retry."
    }
    if ($normalized -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$') {
        throw "latest release returned an invalid version"
    }
    $normalized
}

function Get-Architecture {
    $architecture = "$env:PROCESSOR_ARCHITEW6432 $env:PROCESSOR_ARCHITECTURE".ToUpperInvariant()
    if ($architecture.Contains("ARM64")) {
        return "arm64"
    }
    if ($architecture.Contains("AMD64") -or [Environment]::Is64BitOperatingSystem) {
        return "amd64"
    }
    throw "unsupported Windows architecture; InterviewCraft requires amd64 or arm64"
}

function Get-CosignRecord {
    param([string]$Architecture)
    # Cosign v3.1.3 does not publish a Windows arm64 executable. Windows ARM64
    # uses the official amd64 verifier through the OS x64 compatibility layer.
    [PSCustomObject]@{
        Filename = "cosign-windows-amd64.exe"
        SHA256 = "9fe59be0eca1271873ce019061335eb1ac419b7059202e797828467ddabe33be"
    }
}

function Assert-Filename {
    param([string]$Filename)
    if ($Filename -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$' -or
        $Filename -eq "." -or $Filename -eq ".." -or
        [IO.Path]::GetFileName($Filename) -ne $Filename) {
        throw "manifest contains an invalid filename"
    }
}

function Read-StrictManifest {
    param([string]$Path, [string]$ExpectedVersion, [string]$Architecture)
    $content = [IO.File]::ReadAllText($Path)
    $lines = @($content -split "\r?\n")
    if ($lines.Count -gt 0 -and $lines[$lines.Count - 1] -eq "") {
        if ($lines.Count -eq 1) { $lines = @() } else { $lines = @($lines[0..($lines.Count - 2)]) }
    }
    if ($lines.Count -lt 2 -or $lines[0] -ne $manifestHeader) {
        throw "release manifest is empty or has an unsupported schema"
    }
    $platforms = @{}
    $filenames = @{}
    $meta = $false
    $checksum = $false
    $sboms = 0
    $selected = $null
    for ($index = 1; $index -lt $lines.Count; $index++) {
        $fields = @($lines[$index].Split([char]9))
        $lineNumber = $index + 1
        if ($fields.Count -eq 0 -or [string]::IsNullOrEmpty($lines[$index])) {
            throw "manifest line $lineNumber is blank"
        }
        switch ($fields[0]) {
            "meta" {
                if ($fields.Count -ne 4 -or $meta -or $lineNumber -ne 2 -or
                    $fields[1] -ne $ExpectedVersion -or $fields[2] -notmatch '^[0-9a-f]{7,64}$' -or
                    $fields[3] -notmatch '^\d{4}-\d{2}-\d{2}T.*Z$') {
                    throw "manifest meta row is invalid or does not match the requested version"
                }
                $meta = $true
                continue
            }
            "asset" {
                if (-not $meta -or $checksum -or $fields.Count -ne 6) {
                    throw "manifest asset row is invalid"
                }
                $key = "$($fields[1])/$($fields[2])"
                if ($key -notmatch '^(darwin|linux|windows)/(amd64|arm64)$' -or $platforms.ContainsKey($key)) {
                    throw "manifest platform is unsupported or duplicated"
                }
                $platforms[$key] = $true
            }
            "checksum" {
                if (-not $meta -or $checksum -or $fields.Count -ne 6 -or
                    $fields[1] -ne "-" -or $fields[2] -ne "-" -or $fields[3] -ne "checksums.txt") {
                    throw "manifest checksum row is invalid"
                }
                $checksum = $true
            }
            "sbom" {
                if (-not $checksum -or $fields.Count -ne 6 -or $fields[1] -ne "-" -or
                    $fields[2] -ne "-" -or -not $fields[3].EndsWith(".spdx.json", [StringComparison]::Ordinal)) {
                    throw "manifest SBOM row is invalid"
                }
                $sboms++
            }
            default { throw "manifest contains an unknown row kind" }
        }
        if ($fields[0] -eq "meta") { continue }
        Assert-Filename -Filename $fields[3]
        if ($filenames.ContainsKey($fields[3]) -or $fields[4] -notmatch '^[0-9a-f]{64}$' -or $fields[5] -notmatch '^[1-9][0-9]*$') {
            throw "manifest contains a duplicate file, invalid hash, or invalid size"
        }
        $filenames[$fields[3]] = $true
        if ($fields[0] -eq "asset" -and $fields[1] -eq "windows" -and $fields[2] -eq $Architecture) {
            $selected = [PSCustomObject]@{ Filename = $fields[3]; SHA256 = $fields[4]; Size = [Int64]$fields[5] }
        }
    }
    foreach ($os in @("darwin", "linux", "windows")) {
        foreach ($arch in @("amd64", "arm64")) {
            if (-not $platforms.ContainsKey("$os/$arch")) { throw "manifest is missing $os/$arch" }
        }
    }
    if (-not $meta -or -not $checksum -or $sboms -lt 1 -or $null -eq $selected) {
        throw "release manifest is incomplete for this platform"
    }
    $selected
}

function Assert-SafeZipAndExtract {
    param([string]$Archive, [string]$Destination)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $allowed = @{
        "interviewcraft.exe" = $true
        "README.md" = $true
        "docs/DEPLOYMENT.md" = $true
        "docs/SECURITY.md" = $true
        "scripts/install.ps1" = $true
        "scripts/install.sh" = $true
        "scripts/uninstall.ps1" = $true
        "scripts/uninstall.sh" = $true
        "scripts/cosign-v3.1.3-sha256.txt" = $true
    }
    $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        $binaryCount = 0
        $seen = @{}
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace("\", "/")
            $segments = @($name.Split("/"))
            if ($name.StartsWith("/", [StringComparison]::Ordinal) -or $name -match '^[A-Za-z]:' -or
                $segments -contains ".." -or $segments -contains "." -or
                (($entry.ExternalAttributes -shr 16) -band 0xF000) -eq 0xA000) {
                throw "archive contains an absolute, traversal, or symbolic-link entry"
            }
            if ($seen.ContainsKey($name)) { throw "archive contains a duplicate entry: $name" }
            $seen[$name] = $true
            if ($name.EndsWith("/", [StringComparison]::Ordinal)) {
                if ($name -notin @("docs/", "scripts/")) { throw "archive contains an unexpected directory: $name" }
                continue
            }
            if (-not $allowed.ContainsKey($name)) {
                throw "archive contains an unexpected file or executable: $name"
            }
            if ($name -eq "interviewcraft.exe") { $binaryCount++ }
        }
        if ($binaryCount -ne 1) { throw "archive must contain exactly one interviewcraft.exe" }
    }
    finally {
        $zip.Dispose()
    }
    [IO.Compression.ZipFile]::ExtractToDirectory($Archive, $Destination)
}

function Get-InstalledVersion {
    param([string]$Binary)
    if (-not (Test-Path -LiteralPath $Binary -PathType Leaf)) { return "" }
    try {
        $json = (& $Binary version --json) -join "`n"
        if ($LASTEXITCODE -ne 0) { throw "version failed" }
        return [string](($json | ConvertFrom-Json).version)
    }
    catch {
        throw "an existing interviewcraft.exe is unreadable; move it aside manually before installing"
    }
}

function Get-ReceiptPath {
    if ($testMode -and -not [string]::IsNullOrWhiteSpace($env:INTERVIEWCRAFT_INSTALL_TEST_RECEIPT)) {
        return [IO.Path]::GetFullPath($env:INTERVIEWCRAFT_INSTALL_TEST_RECEIPT)
    }
    Join-Path $HOME ".interviewcraft\install-receipt.txt"
}

function Add-ManagedPath {
    param([string]$Directory)
    if ($testMode -and -not [string]::IsNullOrWhiteSpace($env:INTERVIEWCRAFT_INSTALL_TEST_PATH_FILE)) {
        $pathFile = [IO.Path]::GetFullPath($env:INTERVIEWCRAFT_INSTALL_TEST_PATH_FILE)
        $parent = Split-Path -Parent $pathFile
        New-Item -ItemType Directory -Force -Path $parent | Out-Null
        $content = if (Test-Path -LiteralPath $pathFile) { [IO.File]::ReadAllText($pathFile) } else { "" }
        $pattern = '(?ms)^# >>> InterviewCraft PATH >>>\r?\n.*?^# <<< InterviewCraft PATH <<<\r?\n?'
        $content = [regex]::Replace($content, $pattern, "").TrimEnd()
        if ($content.Length -gt 0) { $content += "`r`n" }
        $content += "$pathBegin`r`n$Directory`r`n$pathEnd`r`n"
        [IO.File]::WriteAllText($pathFile, $content, (New-Object Text.UTF8Encoding($false)))
        return $pathFile
    }
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $entries = @($userPath -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if (-not ($entries | Where-Object { $_.TrimEnd('\') -ieq $Directory.TrimEnd('\') })) {
        $entries += $Directory
        [Environment]::SetEnvironmentVariable("Path", ($entries -join ';'), "User")
    }
    if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\') -ieq $Directory.TrimEnd('\') })) {
        $env:Path = "$Directory;$env:Path"
    }
    "HKCU\Environment\Path"
}

function Remove-ManagedPath {
    param([string]$Directory, [string]$Target)
    if ($Target -ne "HKCU\Environment\Path") {
        if (-not (Test-Path -LiteralPath $Target)) { return }
        $content = [IO.File]::ReadAllText($Target)
        $pattern = '(?ms)^# >>> InterviewCraft PATH >>>\r?\n.*?^# <<< InterviewCraft PATH <<<\r?\n?'
        [IO.File]::WriteAllText($Target, [regex]::Replace($content, $pattern, ""), (New-Object Text.UTF8Encoding($false)))
        return
    }
    $entries = @([Environment]::GetEnvironmentVariable("Path", "User") -split ';' |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) -and $_.TrimEnd('\') -ine $Directory.TrimEnd('\') })
    [Environment]::SetEnvironmentVariable("Path", ($entries -join ';'), "User")
}

function Write-Receipt {
    param([string]$Path, [string]$ReleaseVersion, [string]$Directory, [string]$Binary, [string]$PathTarget)
    foreach ($value in @($ReleaseVersion, $Directory, $Binary, $PathTarget)) {
        if ($value.Contains("`t") -or $value.Contains("`r") -or $value.Contains("`n")) { throw "receipt value contains an invalid control character" }
    }
    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $temporary = Join-Path $parent (".install-receipt-" + [guid]::NewGuid().ToString("N") + ".tmp")
    $lines = @($receiptHeader, "version`t$ReleaseVersion", "install_dir`t$Directory", "binary_path`t$Binary", "path_target`t$PathTarget")
    [IO.File]::WriteAllLines($temporary, $lines, (New-Object Text.UTF8Encoding($false)))
    Move-Item -LiteralPath $temporary -Destination $Path -Force
}

if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA) -and $InstallDir -eq "") {
    throw "LOCALAPPDATA is unavailable; specify -InstallDir"
}
$resolvedInstallDir = [IO.Path]::GetFullPath($InstallDir)
$architecture = Get-Architecture
$releaseVersion = Resolve-ReleaseVersion -Requested $Version
$tag = "v$releaseVersion"
$binaryPath = Join-Path $resolvedInstallDir "interviewcraft.exe"
$existingVersion = Get-InstalledVersion -Binary $binaryPath
if ($existingVersion -ne "" -and $existingVersion -ne $releaseVersion) {
    throw "InterviewCraft $existingVersion is already installed. Automatic upgrades are reserved for T-028; uninstall or wait for update."
}

$temporaryRoot = Join-Path ([IO.Path]::GetTempPath()) ("interviewcraft-install-" + [guid]::NewGuid().ToString("N"))
$installedNew = $false
$pathTarget = ""
New-Item -ItemType Directory -Path $temporaryRoot | Out-Null
try {
    $releaseBase = "$repo/releases/download"
    if ($testMode) {
        $releaseBase = $env:INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL
        $uri = [Uri]$releaseBase
        if (-not $uri.IsLoopback) { throw "test release fixture must use a loopback URL" }
    }
    $tagBase = "$($releaseBase.TrimEnd('/'))/$tag"
    $manifestPath = Join-Path $temporaryRoot "release-manifest.txt"
    $bundlePath = Join-Path $temporaryRoot "release-manifest.sigstore.json"
    Write-Stage 1 7 "downloading signed release metadata"
    Get-Download -Uri "$tagBase/release-manifest.txt" -Path $manifestPath
    Get-Download -Uri "$tagBase/release-manifest.sigstore.json" -Path $bundlePath

    Write-Stage 2 7 "preparing pinned Cosign verifier"
    if ($testMode) {
        $cosignPath = [IO.Path]::GetFullPath($env:INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH)
        $expectedCosign = $env:INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256
        if (-not $cosignPath.StartsWith([IO.Path]::GetFullPath([IO.Path]::GetTempPath()), [StringComparison]::OrdinalIgnoreCase)) {
            throw "test Cosign fixture must be under the temporary directory"
        }
    }
    else {
        $cosign = Get-CosignRecord -Architecture $architecture
        $cosignPath = Join-Path $temporaryRoot $cosign.Filename
        $expectedCosign = $cosign.SHA256
        Get-Download -Uri "https://github.com/sigstore/cosign/releases/download/$cosignVersion/$($cosign.Filename)" -Path $cosignPath
    }
    if ((Get-SHA256 -Path $cosignPath) -cne $expectedCosign) { throw "Cosign verifier hash does not match the repository-pinned matrix" }

    Write-Stage 3 7 "verifying manifest signature and publisher identity"
    $identity = "https://github.com/wenbokun434-sketch/interviewcraft/.github/workflows/release.yml@refs/tags/$tag"
    & $cosignPath verify-blob --bundle $bundlePath --certificate-identity $identity --certificate-oidc-issuer $oidcIssuer $manifestPath
    if ($LASTEXITCODE -ne 0) { throw "release manifest signature or publisher identity is invalid" }
    $asset = Read-StrictManifest -Path $manifestPath -ExpectedVersion $releaseVersion -Architecture $architecture

    Write-Stage 4 7 "downloading and hashing $($asset.Filename)"
    $archivePath = Join-Path $temporaryRoot $asset.Filename
    Get-Download -Uri "$tagBase/$($asset.Filename)" -Path $archivePath
    if ((Get-Item -LiteralPath $archivePath).Length -ne $asset.Size -or (Get-SHA256 -Path $archivePath) -cne $asset.SHA256) {
        throw "application archive hash or size does not match the signed manifest"
    }
    $availableBytes = [Int64]::MaxValue
    if ($testMode -and -not [string]::IsNullOrWhiteSpace($env:INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES)) {
        $availableBytes = [Int64]$env:INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES
    }
    if ($availableBytes -lt ($asset.Size * 3)) { throw "insufficient disk space for verified extraction" }

    Write-Stage 5 7 "validating archive paths and embedded version"
    $extractRoot = Join-Path $temporaryRoot "extract"
    New-Item -ItemType Directory -Path $extractRoot | Out-Null
    Assert-SafeZipAndExtract -Archive $archivePath -Destination $extractRoot
    $stagedBinary = Join-Path $extractRoot "interviewcraft.exe"
    $stagedInfo = ((& $stagedBinary version --json) -join "`n") | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $stagedInfo.version -ne $releaseVersion -or $stagedInfo.goos -ne "windows" -or $stagedInfo.goarch -ne $architecture) {
        throw "archive binary version or platform does not match the signed manifest"
    }

    Write-Stage 6 7 "installing and completing setup/doctor"
    if ($existingVersion -eq "") {
        New-Item -ItemType Directory -Force -Path $resolvedInstallDir | Out-Null
        $temporaryBinary = Join-Path $resolvedInstallDir (".interviewcraft-" + [guid]::NewGuid().ToString("N") + ".tmp")
        Copy-Item -LiteralPath $stagedBinary -Destination $temporaryBinary
        Move-Item -LiteralPath $temporaryBinary -Destination $binaryPath
        $installedNew = $true
    }
    if (-not $SkipSetup) {
        $setupArgs = @("setup", "--profile", $Profile)
        if ($Provider -ne "") { $setupArgs += @("--provider", $Provider) }
        if ($Endpoint -ne "") { $setupArgs += @("--endpoint", $Endpoint) }
        if ($Model -ne "") { $setupArgs += @("--model", $Model) }
        if ($ApiKeyStdin) { $setupArgs += "--api-key-stdin" }
        if ($NonInteractive) { $setupArgs += "--non-interactive" }
        & $binaryPath @setupArgs
        if ($LASTEXITCODE -ne 0) { throw "setup failed; fix the reported dependency and rerun the installer" }
        & $binaryPath doctor
        if ($LASTEXITCODE -ne 0) { throw "doctor failed; fix the reported dependency and rerun the installer" }
    }

    Write-Stage 7 7 "managing user PATH and writing receipt"
    $pathTarget = Add-ManagedPath -Directory $resolvedInstallDir
    $receiptPath = Get-ReceiptPath
    Write-Receipt -Path $receiptPath -ReleaseVersion $releaseVersion -Directory $resolvedInstallDir -Binary $binaryPath -PathTarget $pathTarget
    Write-Host "InterviewCraft $releaseVersion installed at $binaryPath"
    Write-Host "Open a new terminal and run: interviewcraft version"
}
catch {
    if ($pathTarget -ne "") { Remove-ManagedPath -Directory $resolvedInstallDir -Target $pathTarget }
    if ($installedNew) { Remove-Item -LiteralPath $binaryPath -Force -ErrorAction SilentlyContinue }
    throw
}
finally {
    $resolvedTemporary = [IO.Path]::GetFullPath($temporaryRoot)
    $resolvedSystemTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if ($resolvedTemporary.StartsWith($resolvedSystemTemp, [StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedTemporary).StartsWith("interviewcraft-install-")) {
        Remove-Item -LiteralPath $resolvedTemporary -Recurse -Force -ErrorAction SilentlyContinue
    }
}
