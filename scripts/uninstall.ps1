[CmdletBinding()]
param(
    [string]$ReceiptPath = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$header = "interviewcraft-install-receipt-v1"
$testMode = $env:INTERVIEWCRAFT_INSTALL_TEST_MODE -eq "1"

if ([string]::IsNullOrWhiteSpace($ReceiptPath)) {
    if ($testMode -and -not [string]::IsNullOrWhiteSpace($env:INTERVIEWCRAFT_INSTALL_TEST_RECEIPT)) {
        $ReceiptPath = $env:INTERVIEWCRAFT_INSTALL_TEST_RECEIPT
    }
    else {
        $ReceiptPath = Join-Path $HOME ".interviewcraft\install-receipt.txt"
    }
}
$ReceiptPath = [IO.Path]::GetFullPath($ReceiptPath)
if (-not (Test-Path -LiteralPath $ReceiptPath -PathType Leaf)) {
    throw "InterviewCraft install receipt was not found: $ReceiptPath"
}
$lines = @(Get-Content -LiteralPath $ReceiptPath)
if ($lines.Count -lt 5 -or $lines[0] -ne $header) { throw "install receipt is invalid" }
$values = @{}
foreach ($line in $lines[1..($lines.Count - 1)]) {
    $fields = @($line.Split([char]9))
    if ($fields.Count -ne 2 -or $values.ContainsKey($fields[0])) { throw "install receipt is malformed" }
    $values[$fields[0]] = $fields[1]
}
foreach ($required in @("version", "install_dir", "binary_path", "path_target")) {
    if (-not $values.ContainsKey($required)) { throw "install receipt is missing $required" }
}
$installDir = [IO.Path]::GetFullPath($values.install_dir)
$binaryPath = [IO.Path]::GetFullPath($values.binary_path)
if ([IO.Path]::GetDirectoryName($binaryPath) -ine $installDir -or [IO.Path]::GetFileName($binaryPath) -ine "interviewcraft.exe") {
    throw "install receipt binary path is outside the recorded install directory"
}

$target = $values.path_target
if ($target -eq "HKCU\Environment\Path") {
    $entries = @([Environment]::GetEnvironmentVariable("Path", "User") -split ';' |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) -and $_.TrimEnd('\') -ine $installDir.TrimEnd('\') })
    [Environment]::SetEnvironmentVariable("Path", ($entries -join ';'), "User")
}
elseif (Test-Path -LiteralPath $target) {
    $content = [IO.File]::ReadAllText($target)
    $pattern = '(?ms)^# >>> InterviewCraft PATH >>>\r?\n.*?^# <<< InterviewCraft PATH <<<\r?\n?'
    [IO.File]::WriteAllText($target, [regex]::Replace($content, $pattern, ""), (New-Object Text.UTF8Encoding($false)))
}

Remove-Item -LiteralPath $binaryPath -Force -ErrorAction Stop
if ((Test-Path -LiteralPath $installDir -PathType Container) -and @(Get-ChildItem -LiteralPath $installDir -Force).Count -eq 0) {
    Remove-Item -LiteralPath $installDir -Force
}
Remove-Item -LiteralPath $ReceiptPath -Force
Write-Host "InterviewCraft $($values.version) was uninstalled."
Write-Host "Configuration, credentials, and $HOME\.interviewcraft data were preserved."
