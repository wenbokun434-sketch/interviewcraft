[CmdletBinding()]
param(
    [ValidateSet("Generate", "Verify")]
    [string]$Mode = "Verify",
    [string]$DistDirectory = "dist",
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$Commit,
    [string]$CreatedUTC = ([DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")),
    [string]$AMD64Digest,
    [string]$ARM64Digest
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$header = "interviewcraft-runner-release-v1"
$repository = "ghcr.io/wenbokun434-sketch/interviewcraft-runner"
$protocol = "interviewcraft-runner-response-v1"
$digestPattern = '^sha256:[0-9a-f]{64}$'
$versionPattern = '^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$'
$commitPattern = '^[0-9a-f]{40}([0-9a-f]{24})?$'
$manifestPath = Join-Path $DistDirectory "runner-manifest.txt"

function Assert-Common {
    if ($Version -cnotmatch $versionPattern) { throw "Runner manifest version is invalid" }
    if ($Commit -cnotmatch $commitPattern) { throw "Runner manifest commit is invalid" }
}

function Read-StrictRunnerManifest {
    param([string]$Path)
    $content = [IO.File]::ReadAllText($Path)
    if (-not $content.EndsWith("`n")) { throw "Runner manifest must end with LF" }
    $lines = @($content.TrimEnd("`r", "`n") -split "`n")
    if ($lines.Count -ne 4 -or $lines[0] -cne $header) { throw "Runner manifest header or row count is invalid" }
    $meta = @($lines[1].TrimEnd("`r") -split "`t", -1)
    if ($meta.Count -ne 4 -or $meta[0] -cne "meta" -or $meta[1] -cne $Version -or $meta[2] -cne $Commit) {
        throw "Runner manifest metadata is invalid"
    }
    $parsedTime = [DateTime]::MinValue
    if (-not [DateTime]::TryParseExact($meta[3], "yyyy-MM-ddTHH:mm:ssZ", [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal, [ref]$parsedTime)) {
        throw "Runner manifest creation time is invalid"
    }
    $seen = @{}
    foreach ($line in $lines[2..3]) {
        $fields = @($line.TrimEnd("`r") -split "`t", -1)
        if ($fields.Count -ne 7 -or $fields[0] -cne "image" -or $fields[1] -cne "linux" -or
            @("amd64", "arm64") -cnotcontains $fields[2] -or $fields[3] -cne $repository -or
            $fields[4] -cnotmatch $digestPattern -or $fields[5] -cne $protocol -or $fields[6] -cne "65532:65532") {
            throw "Runner manifest image row is invalid"
        }
        if ($seen.ContainsKey($fields[2])) { throw "Runner manifest platform is duplicated" }
        $seen[$fields[2]] = $fields[4]
    }
    if (-not $seen.ContainsKey("amd64") -or -not $seen.ContainsKey("arm64")) { throw "Runner manifest platform is missing" }
    $seen
}

Assert-Common
if ($Mode -eq "Generate") {
    if ($AMD64Digest -cnotmatch $digestPattern -or $ARM64Digest -cnotmatch $digestPattern) {
        throw "Both immutable Runner digests are required"
    }
    [IO.Directory]::CreateDirectory((Resolve-Path $DistDirectory).Path) | Out-Null
    $rows = @(
        $header,
        ("meta`t{0}`t{1}`t{2}" -f $Version, $Commit, $CreatedUTC),
        ("image`tlinux`tamd64`t{0}`t{1}`t{2}`t65532:65532" -f $repository, $AMD64Digest, $protocol),
        ("image`tlinux`tarm64`t{0}`t{1}`t{2}`t65532:65532" -f $repository, $ARM64Digest, $protocol)
    )
    [IO.File]::WriteAllText($manifestPath, (($rows -join "`n") + "`n"), [Text.UTF8Encoding]::new($false))
}
$result = Read-StrictRunnerManifest -Path $manifestPath
Write-Host "Runner manifest verified: amd64=$($result.amd64) arm64=$($result.arm64)"
