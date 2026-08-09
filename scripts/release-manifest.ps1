[CmdletBinding()]
param(
    [ValidateSet("Generate", "Verify")]
    [string]$Mode = "Verify",
    [Parameter(Mandatory = $true)]
    [string]$DistDirectory,
    [string]$ManifestPath,
    [string]$Version,
    [string]$Commit,
    [string]$CreatedUTC
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$header = "interviewcraft-release-v1"
$platforms = @(
    @("darwin", "amd64"),
    @("darwin", "arm64"),
    @("linux", "amd64"),
    @("linux", "arm64"),
    @("windows", "amd64"),
    @("windows", "arm64")
)

function Assert-ReleaseFilename {
    param([Parameter(Mandatory = $true)][string]$Filename)
    if ($Filename -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$' -or
        $Filename -eq "." -or $Filename -eq ".." -or
        [System.IO.Path]::GetFileName($Filename) -ne $Filename) {
        throw "invalid release filename: $Filename"
    }
}

function Assert-Metadata {
    param(
        [Parameter(Mandatory = $true)][string]$ReleaseVersion,
        [Parameter(Mandatory = $true)][string]$GitCommit,
        [Parameter(Mandatory = $true)][string]$Timestamp
    )
    if ($ReleaseVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?(?:\+[0-9A-Za-z][0-9A-Za-z.-]*)?$') {
        throw "invalid release version"
    }
    if ($GitCommit -notmatch '^[0-9a-f]{7,64}$') {
        throw "invalid lowercase Git commit"
    }
    $parsed = [DateTimeOffset]::MinValue
    if (-not $Timestamp.EndsWith("Z", [StringComparison]::Ordinal) -or
        -not [DateTimeOffset]::TryParse($Timestamp, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal, [ref]$parsed) -or
        $parsed.Offset -ne [TimeSpan]::Zero) {
        throw "invalid created UTC time"
    }
}

function Get-ReleaseFileRecord {
    param(
        [Parameter(Mandatory = $true)][string]$Kind,
        [Parameter(Mandatory = $true)][string]$GOOS,
        [Parameter(Mandatory = $true)][string]$GOARCH,
        [Parameter(Mandatory = $true)][string]$Path
    )
    $item = Get-Item -LiteralPath $Path -ErrorAction Stop
    if ($item.Length -le 0) {
        throw "release asset is empty: $($item.Name)"
    }
    Assert-ReleaseFilename -Filename $item.Name
    [PSCustomObject]@{
        Kind = $Kind
        GOOS = $GOOS
        GOARCH = $GOARCH
        Filename = $item.Name
        SHA256 = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        Size = [Int64]$item.Length
    }
}

function Read-ReleaseManifest {
    param([Parameter(Mandatory = $true)][string]$Path)
    $content = [System.IO.File]::ReadAllText((Resolve-Path -LiteralPath $Path).Path)
    $lines = @($content -split "\r?\n")
    if ($lines.Count -gt 0 -and $lines[$lines.Count - 1] -eq "") {
        $lines = @($lines[0..($lines.Count - 2)])
    }
    if ($lines.Count -lt 2 -or $lines[0] -ne $header) {
        throw "unsupported or empty release manifest"
    }
    $seenMeta = $false
    $seenChecksum = $false
    $seenPlatforms = @{}
    $seenFiles = @{}
    $records = New-Object System.Collections.Generic.List[object]
    $releaseVersion = ""
    $gitCommit = ""
    for ($index = 1; $index -lt $lines.Count; $index++) {
        $lineNumber = $index + 1
        $line = $lines[$index]
        if ([string]::IsNullOrEmpty($line)) {
            throw "line ${lineNumber}: blank lines are not allowed"
        }
        $fields = @($line.Split([char]9))
        switch ($fields[0]) {
            "meta" {
                if ($fields.Count -ne 4 -or $seenMeta -or $lineNumber -ne 2) {
                    throw "line ${lineNumber}: invalid or duplicate meta row"
                }
                Assert-Metadata -ReleaseVersion $fields[1] -GitCommit $fields[2] -Timestamp $fields[3]
                $releaseVersion = $fields[1]
                $gitCommit = $fields[2]
                $seenMeta = $true
                continue
            }
            "asset" {
                if (-not $seenMeta -or $seenChecksum -or $fields.Count -ne 6) {
                    throw "line ${lineNumber}: invalid asset row"
                }
                $platformKey = "$($fields[1])/$($fields[2])"
                $supported = $false
                foreach ($platform in $platforms) {
                    if ($platform[0] -eq $fields[1] -and $platform[1] -eq $fields[2]) {
                        $supported = $true
                    }
                }
                if (-not $supported -or $seenPlatforms.ContainsKey($platformKey)) {
                    throw "line ${lineNumber}: unsupported or duplicate platform"
                }
                $seenPlatforms[$platformKey] = $true
            }
            "checksum" {
                if (-not $seenMeta -or $seenChecksum -or $fields.Count -ne 6 -or
                    $fields[1] -ne "-" -or $fields[2] -ne "-" -or $fields[3] -ne "checksums.txt") {
                    throw "line ${lineNumber}: invalid or duplicate checksum row"
                }
                $seenChecksum = $true
            }
            "sbom" {
                if (-not $seenChecksum -or $fields.Count -ne 6 -or
                    $fields[1] -ne "-" -or $fields[2] -ne "-" -or
                    -not $fields[3].EndsWith(".spdx.json", [StringComparison]::Ordinal)) {
                    throw "line ${lineNumber}: invalid sbom row"
                }
            }
            default {
                throw "line ${lineNumber}: unknown row kind"
            }
        }
        if ($fields[0] -eq "meta") {
            continue
        }
        Assert-ReleaseFilename -Filename $fields[3]
        if ($seenFiles.ContainsKey($fields[3])) {
            throw "line ${lineNumber}: duplicate filename"
        }
        if ($fields[4] -notmatch '^[0-9a-f]{64}$' -or $fields[5] -notmatch '^[1-9][0-9]*$') {
            throw "line ${lineNumber}: invalid SHA-256 or size"
        }
        $size = [Int64]0
        if (-not [Int64]::TryParse($fields[5], [ref]$size) -or $size -le 0) {
            throw "line ${lineNumber}: asset size is out of range"
        }
        $seenFiles[$fields[3]] = $true
        $records.Add([PSCustomObject]@{
            Kind = $fields[0]
            GOOS = $fields[1]
            GOARCH = $fields[2]
            Filename = $fields[3]
            SHA256 = $fields[4]
            Size = $size
        })
    }
    $assetCount = @($records | Where-Object { $_.Kind -eq "asset" }).Count
    $sbomCount = @($records | Where-Object { $_.Kind -eq "sbom" }).Count
    if (-not $seenMeta -or -not $seenChecksum -or $assetCount -ne $platforms.Count -or $sbomCount -lt 1) {
        throw "release manifest is incomplete"
    }
    foreach ($platform in $platforms) {
        if (-not $seenPlatforms.ContainsKey("$($platform[0])/$($platform[1])")) {
            throw "release manifest is missing $($platform[0])/$($platform[1])"
        }
    }
    [PSCustomObject]@{
        Version = $releaseVersion
        Commit = $gitCommit
        Records = $records
    }
}

$resolvedDist = [System.IO.Path]::GetFullPath($DistDirectory)
if (-not (Test-Path -LiteralPath $resolvedDist -PathType Container)) {
    throw "release directory does not exist: $resolvedDist"
}
if ([string]::IsNullOrWhiteSpace($ManifestPath)) {
    $ManifestPath = Join-Path $resolvedDist "release-manifest.txt"
}
$resolvedManifest = [System.IO.Path]::GetFullPath($ManifestPath)

if ($Mode -eq "Generate") {
    if ([string]::IsNullOrWhiteSpace($CreatedUTC)) {
        $CreatedUTC = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ", [Globalization.CultureInfo]::InvariantCulture)
    }
    Assert-Metadata -ReleaseVersion $Version -GitCommit $Commit -Timestamp $CreatedUTC
    $records = New-Object System.Collections.Generic.List[object]
    foreach ($platform in $platforms) {
        $extension = ".tar.gz"
        if ($platform[0] -eq "windows") {
            $extension = ".zip"
        }
        $filename = "interviewcraft_${Version}_$($platform[0])_$($platform[1])${extension}"
        $records.Add((Get-ReleaseFileRecord -Kind "asset" -GOOS $platform[0] -GOARCH $platform[1] -Path (Join-Path $resolvedDist $filename)))
    }
    $records.Add((Get-ReleaseFileRecord -Kind "checksum" -GOOS "-" -GOARCH "-" -Path (Join-Path $resolvedDist "checksums.txt")))
    $sbomFiles = @(Get-ChildItem -LiteralPath $resolvedDist -File | Where-Object { $_.Name.EndsWith(".spdx.json", [StringComparison]::Ordinal) } | Sort-Object Name)
    if ($sbomFiles.Count -eq 0) {
        throw "no SPDX SBOM files were generated"
    }
    foreach ($sbomFile in $sbomFiles) {
        $records.Add((Get-ReleaseFileRecord -Kind "sbom" -GOOS "-" -GOARCH "-" -Path $sbomFile.FullName))
    }
    $builder = New-Object System.Text.StringBuilder
    [void]$builder.Append($header).Append("`n")
    [void]$builder.Append("meta`t${Version}`t${Commit}`t${CreatedUTC}`n")
    foreach ($record in $records) {
        [void]$builder.Append("$($record.Kind)`t$($record.GOOS)`t$($record.GOARCH)`t$($record.Filename)`t$($record.SHA256)`t$($record.Size)`n")
    }
    [System.IO.File]::WriteAllText($resolvedManifest, $builder.ToString(), (New-Object System.Text.UTF8Encoding($false)))
}

$manifest = Read-ReleaseManifest -Path $resolvedManifest
if (-not [string]::IsNullOrWhiteSpace($Version) -and $manifest.Version -ne $Version) {
    throw "manifest version does not match expected version"
}
if (-not [string]::IsNullOrWhiteSpace($Commit) -and $manifest.Commit -ne $Commit) {
    throw "manifest commit does not match expected commit"
}
foreach ($record in $manifest.Records) {
    $path = Join-Path $resolvedDist $record.Filename
    $actual = Get-ReleaseFileRecord -Kind $record.Kind -GOOS $record.GOOS -GOARCH $record.GOARCH -Path $path
    if ($actual.Size -ne $record.Size -or $actual.SHA256 -cne $record.SHA256) {
        throw "release asset does not match manifest: $($record.Filename)"
    }
}

Write-Output "Release manifest verified: $resolvedManifest"
