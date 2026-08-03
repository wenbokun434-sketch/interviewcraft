# InterviewCraft

InterviewCraft is a local-first terminal application for evidence-based interview practice. It turns a confirmed resume into a scenario, text interview, policy-bound Coach session, optional coding exercise, evidence-linked report, and next practice run.

Lite is a single Go binary backed by embedded SQLite and a model Provider. Docker is optional and `RUNNER_MODE` defaults to `disabled`; Node.js, Java, Python, PostgreSQL, Redis, queues, object storage, and vector databases are not Lite dependencies.

## Install a release binary

Download the archive and `checksums.txt` from [GitHub Releases](https://github.com/interviewcraft/interviewcraft/releases). Release archives are named `interviewcraft_<version>_<os>_<arch>`.

Windows PowerShell:

```powershell
Expand-Archive .\interviewcraft_<version>_windows_amd64.zip -DestinationPath .\interviewcraft
Get-FileHash .\interviewcraft_<version>_windows_amd64.zip -Algorithm SHA256
.\interviewcraft\interviewcraft.exe --help
```

Linux or macOS:

```sh
tar -xzf interviewcraft_<version>_<os>_<arch>.tar.gz
sha256sum interviewcraft_<version>_<os>_<arch>.tar.gz
./interviewcraft --help
```

Compare the printed hash with `checksums.txt` before running the binary. With Go 1.26 or newer, source installation is also available:

```sh
go install github.com/interviewcraft/interviewcraft/cmd/interviewcraft@latest
```

## Quick start

The supported terminal minimum is 80 columns by 24 rows. Initialization is idempotent and creates `config.json`, `interviewcraft.db`, `uploads/`, `exports/`, and `logs/` under `~/.interviewcraft` by default.

```powershell
interviewcraft init
interviewcraft doctor
interviewcraft run
```

`doctor` checks the data directory, embedded SQLite migrations, terminal size, model Provider, and optional Runner. Blocking failures return a non-zero exit code. A disabled or unavailable optional Docker Runner is a warning and does not block the Lite text-interview, Coach, or report path.

Terminal capability flags can be combined when needed:

```powershell
interviewcraft run --ascii --reduce-motion --no-color
```

The primary workflow is keyboard-operated. Run `interviewcraft --help` for commands and use `?` on a screen for its current shortcuts.

## Configure a model Provider

InterviewCraft supports OpenAI-compatible endpoints and Ollama. API key values remain in process environment variables: configuration stores only the name of the variable to read.

OpenAI-compatible example:

```powershell
$env:INTERVIEWCRAFT_LLM_PROVIDER = "openai-compatible"
$env:INTERVIEWCRAFT_LLM_ENDPOINT = "https://provider.example/v1"
$env:INTERVIEWCRAFT_LLM_MODEL = "model-name"
$env:INTERVIEWCRAFT_LLM_API_KEY_ENV = "INTERVIEWCRAFT_API_KEY"
$env:INTERVIEWCRAFT_API_KEY = "<secret>"
interviewcraft doctor
```

Local Ollama example:

```powershell
$env:INTERVIEWCRAFT_LLM_PROVIDER = "ollama"
$env:INTERVIEWCRAFT_LLM_ENDPOINT = "http://127.0.0.1:11434"
$env:INTERVIEWCRAFT_LLM_MODEL = "<installed-model>"
interviewcraft doctor
```

| Variable | Purpose | Default |
| --- | --- | --- |
| `INTERVIEWCRAFT_DATA_DIR` | Local configuration and data directory | `~/.interviewcraft` |
| `INTERVIEWCRAFT_LLM_PROVIDER` | `openai-compatible` or `ollama` | unset |
| `INTERVIEWCRAFT_LLM_ENDPOINT` | Model service base URL | unset |
| `INTERVIEWCRAFT_LLM_MODEL` | Model name | unset |
| `INTERVIEWCRAFT_LLM_API_KEY_ENV` | Name of the environment variable containing the API key | `OPENAI_API_KEY` |
| `RUNNER_MODE` | `disabled` or `docker` | `disabled` |
| `AUDIO_PROVIDER` | Reserved audio provider selector | `browser` |

## Deployment tiers

| Tier | Components | Data boundary | Code execution |
| --- | --- | --- | --- |
| Lite | Binary, embedded SQLite, one OpenAI-compatible Provider or Ollama | Training data stays in the local data directory; Provider receives only policy-selected context | Disabled by default |
| Private Local | Lite plus a loopback Ollama endpoint | Training and model traffic stay on the machine | Disabled by default |
| Full Practice | Lite or Private Local plus the InterviewCraft Docker Runner image | Same model boundary; submitted code enters a short-lived isolated container | Explicit `RUNNER_MODE=docker` |

See [Deployment](docs/DEPLOYMENT.md) for platform setup, upgrades, backups, and tier changes. Full Practice does not change the Lite application dependencies: language runtimes live inside the optional Runner image.

## Export, migrate, and restore

Create a migration package. Provider configuration and secrets are never included; Coach transcript text is excluded unless explicitly requested.

```powershell
interviewcraft export --format package --output .\interviewcraft-transfer.json
interviewcraft export --format package --include-coach --output .\with-coach.json
```

Export one completed report:

```powershell
interviewcraft export --format markdown --session <session-id> --output .\report.md
interviewcraft export --format json --session <session-id> --output .\report.json
```

Restore only into an initialized instance with no training data:

```powershell
$env:INTERVIEWCRAFT_DATA_DIR = "D:\InterviewCraft"
interviewcraft init
interviewcraft import --input .\interviewcraft-transfer.json
interviewcraft run
```

Import strictly validates the package version, IDs, report evidence, and foreign-key graph before one atomic commit. Export never overwrites an existing file. For in-place upgrades, stop InterviewCraft, back up the complete data directory, install the new binary, and run `interviewcraft doctor`; embedded migrations apply automatically. Detailed recovery guidance is in [Deployment](docs/DEPLOYMENT.md#migration-backup-and-restore).

## Optional Runner security

Enable Docker execution only after building the dedicated image and passing the isolation gate:

```powershell
docker build -t interviewcraft-runner:local docker/runner
$env:RUNNER_MODE = "docker"
interviewcraft doctor
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-runner-isolation.ps1
```

Each run is network-disabled, non-root, capability-free, read-only, `no-new-privileges`, resource-limited, wall-clock-limited, and force-removed on every exit path. No host directory is mounted and no host environment is forwarded. Public output contains only public test names/statuses, hidden pass/fail counts, enumerated errors, duration, and peak memory. See [Security](docs/SECURITY.md) for the complete threat boundary and safe protocol.

## Build and verify from source

Requirements:

- Go 1.26 or newer
- PowerShell 7 in CI, or Windows PowerShell with `-ExecutionPolicy Bypass`
- Docker only for Runner changes and the complete release gate

Lite checks without Docker:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1 -SkipRunnerIsolation
```

Complete release gate, including real Runner isolation attacks:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1
```

The root `go test ./...` does not enter the nested `docker/runner/agent` module; the quality script tests it separately. See [Quality gates](docs/QUALITY_GATES.md) for the E2E and P-01–P-07 evidence matrix.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing contracts, migrations, AI policies, or Runner isolation. Security issues should follow the private reporting guidance in [SECURITY.md](docs/SECURITY.md#reporting-a-vulnerability).

Product scope is defined in the [MVP PRD](docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md), the terminal contract in [DESIGN.md](docs/DESIGN.md), and ordered acceptance work in [TODO.md](TODO.md).

## MVP non-goals

- No real-interview covert assistance, stealth mode, screen evasion, or automatic answering.
- No video, face, emotion, personality, hiring, or employment-decision scoring.
- No required vector database, queue, microservices, complex permissions, or cloud observability stack.
- No multi-user collaboration, mentor review, billing, automated applications, or ATS integration.
- Voice ASR/TTS is not an MVP release blocker; the complete text path is the baseline.
