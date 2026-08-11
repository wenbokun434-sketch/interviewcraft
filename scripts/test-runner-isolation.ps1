[CmdletBinding()]
param(
    [string]$GoBinary = "go",
    [string]$BuildProxy = $env:INTERVIEWCRAFT_RUNNER_BUILD_PROXY,
    [string]$AlpineMirror = $env:INTERVIEWCRAFT_RUNNER_ALPINE_MIRROR
)

$ErrorActionPreference = "Stop"
$runnerImage = "interviewcraft-runner:local"
$protocolImage = "interviewcraft-runner-protocol-test:local"

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
}

function Get-IntegrationContainerIds {
    $ids = @(& docker ps -aq --filter "name=interviewcraft-runner-integration-")
    if ($LASTEXITCODE -ne 0) {
        throw "docker container query failed"
    }
    return @($ids | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
}

$existing = @(Get-IntegrationContainerIds)
if ($existing.Count -ne 0) {
    throw "Runner integration containers already exist; inspect them before retrying"
}

$gatePassed = $false
try {
    $buildArguments = @(
        "build", "--progress=plain",
        "--build-arg", "APPLICATION_VERSION=0.0.0-test",
        "--build-arg", "GIT_COMMIT=0000000000000000000000000000000000000000",
        "--build-arg", "RUNNER_PROTOCOL=interviewcraft-runner-response-v1"
    )
    if (-not [string]::IsNullOrWhiteSpace($BuildProxy)) {
        foreach ($name in @("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy")) {
            $buildArguments += @("--build-arg", "${name}=$BuildProxy")
        }
    }
    if (-not [string]::IsNullOrWhiteSpace($AlpineMirror)) {
        if (-not $AlpineMirror.StartsWith("https://", [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "AlpineMirror must use HTTPS"
        }
        $buildArguments += @("--build-arg", "ALPINE_MIRROR=$AlpineMirror")
    }
    $buildArguments += @("-t", $runnerImage, "docker/runner")
    Invoke-Native -FilePath "docker" -Arguments $buildArguments

    $inspect = @(& docker image inspect $runnerImage | ConvertFrom-Json)[0]
    if ($LASTEXITCODE -ne 0) {
        throw "Runner image inspection failed"
    }
    if ($inspect.Config.Labels."io.interviewcraft.runner" -ne "true") {
        throw "Runner image label is invalid"
    }
    if ($inspect.Config.Labels."io.interviewcraft.version" -ne "0.0.0-test" -or
        $inspect.Config.Labels."io.interviewcraft.protocol" -ne "interviewcraft-runner-response-v1") {
        throw "Runner image version or protocol label is invalid"
    }
    if ($inspect.Config.User -ne "65532:65532") {
        throw "Runner image default user is invalid"
    }

    if ([string]::IsNullOrWhiteSpace($env:GOCACHE)) {
        $env:GOCACHE = Join-Path ([IO.Path]::GetTempPath()) "interviewcraft-runner-go-cache"
    }

    Push-Location "docker/runner/agent"
    try {
        Invoke-Native -FilePath $GoBinary -Arguments @("test", "-count=1", "./...")
    }
    finally {
        Pop-Location
    }

    $env:INTERVIEWCRAFT_RUNNER_INTEGRATION = "1"
    Invoke-Native -FilePath $GoBinary -Arguments @(
        "test",
        "-count=1",
        "./internal/adapters/runner",
        "-run",
        "^TestDockerIntegration"
    )

    $remaining = @(Get-IntegrationContainerIds)
    if ($remaining.Count -ne 0) {
        throw "Runner integration container cleanup was incomplete"
    }

    $gatePassed = $true
}
finally {
    $remaining = @(Get-IntegrationContainerIds)
    if ($remaining.Count -ne 0) {
        & docker rm --force --volumes @remaining | Out-Null
    }
    $protocolImageIds = @(& docker images -q --filter "reference=$protocolImage")
    if ($LASTEXITCODE -eq 0) {
        $protocolImageIds = @($protocolImageIds | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
        if ($protocolImageIds.Count -ne 0) {
            & docker image rm --force @protocolImageIds | Out-Null
        }
    }
}

if ($gatePassed) {
    Write-Output "Runner isolation gate passed with zero residual containers."
}
