# Deployment and operations

## Choose a tier

### Lite

Use the release binary, embedded SQLite, and one reachable model Provider. Keep `RUNNER_MODE=disabled`. This is the default and supports resume extraction, profile confirmation, scenario planning, text interview, Coach, evidence-based reports, and transfer packages without Docker or an external database.

### Private Local

Use the same binary and SQLite layout, but point `INTERVIEWCRAFT_LLM_PROVIDER=ollama` at a loopback Ollama endpoint. Confirm the model is installed before running `interviewcraft doctor`. No model traffic should leave the machine when the endpoint remains loopback and Ollama itself has no remote forwarding configured.

### Full Practice

Start from either Lite tier and run `interviewcraft setup --profile full --restart`. Setup resolves the signed Runner manifest for the installed application version, selects linux/amd64 or linux/arm64, pulls the immutable GHCR digest, verifies the exact GitHub Actions identity and OIDC issuer, inspects labels/default user/platform, and runs an isolated smoke test before atomically enabling `RUNNER_MODE=docker`. Setup never installs or starts Docker. Python, Node.js, and Java remain inside the Runner image.

## Platform installation

Release artifacts cover Windows, Linux, and macOS on amd64 and arm64. The supported one-command installers are `scripts/install.ps1` for Windows PowerShell 5.1/7 and `scripts/install.sh` for POSIX shells. They install only into user-writable locations, manage marked user PATH entries, run `setup` and `doctor` by default, and never request elevation.

The installer trust sequence is fixed: verify the downloaded Cosign v3.1.3 executable against `scripts/cosign-v3.1.3-sha256.txt`; verify the manifest bundle against the exact tagged release workflow identity and GitHub Actions OIDC issuer; strictly parse the manifest; verify archive SHA-256 and size; reject traversal, links, and unexpected executables; execute the staged binary's `version --json`; then atomically place it. A secret-free receipt at `~/.interviewcraft/install-receipt.txt` records the exact binary and PATH files for uninstall.

Reinstalling the same version is idempotent. A different installed version is not overwritten because verified upgrades and rollback belong to T-028. `scripts/uninstall.ps1` and `scripts/uninstall.sh` remove only receipt-owned binary/PATH entries and preserve configuration, credentials, SQLite data, and the rest of `~/.interviewcraft`.

The supported terminal minimum is 80×24. Use `--ascii`, `--no-color`, or `--reduce-motion` for limited terminal capabilities. At smaller dimensions the application deliberately renders an actionable blocked state instead of a clipped workspace.

## First start

```powershell
interviewcraft init
interviewcraft doctor
interviewcraft run
```

`init` is idempotent. The default directory is `~/.interviewcraft`; set `INTERVIEWCRAFT_DATA_DIR` before `init` to choose another location. The selected user must be able to create and replace files in that directory.

`doctor` returns non-zero when the data directory, SQLite, terminal, or configured model Provider blocks training. Runner diagnostics are non-blocking while disabled. When enabled, `doctor` re-verifies the image signature, repository digest, architecture, version/protocol labels, default user, and Docker daemon before reporting it ready.

## Configuration

Environment variables override the local runtime configuration. Do not put credentials in endpoint URLs or command history. Set `INTERVIEWCRAFT_LLM_API_KEY_ENV` to the name of a separate secret variable and inject that secret through the operating system, service manager, or CI secret store.

The full variable table and examples are in the [README](../README.md#configure-a-model-provider).

## Migration, backup, and restore

For an in-place binary upgrade:

1. Stop all InterviewCraft processes using the data directory.
2. Copy the complete data directory, including `interviewcraft.db`, to a protected backup location.
3. Replace the binary, leaving the data directory unchanged.
4. Run `interviewcraft doctor`. Opening SQLite applies ordered embedded migrations transactionally.
5. Start with `interviewcraft run` and verify recent sessions and reports.

Do not edit `_schema_migrations` or apply SQL manually. If an upgrade fails, preserve the failed directory for diagnosis, restore the complete pre-upgrade directory, and run the previous binary. A database file copied while a writer is active is not a supported backup.

For migration to another machine or directory, prefer the strict transfer package:

```powershell
interviewcraft export --format package --output .\transfer.json
$env:INTERVIEWCRAFT_DATA_DIR = "D:\InterviewCraft"
interviewcraft init
interviewcraft import --input .\transfer.json
```

The target must contain no training data. Import is atomic and does not overwrite the target's Provider configuration. API keys are never transferred; Coach transcript content is transferred only when `--include-coach` was explicitly used at export.

## Tier changes and rollback

Moving from Lite to Full Practice writes immutable, non-secret Runner metadata and changes `RUNNER_MODE` only after every provisioning gate passes. Do not enable the mode with an environment variable alone. Moving back is immediate: set `RUNNER_MODE=disabled`, rerun `doctor`, and continue the text path. Existing code evidence remains part of completed sessions, but no new code process starts.

Changing model Providers does not rewrite prior evidence. Diagnose the new endpoint before starting a new scenario, and retain the old model setting until the new configuration passes.

## Release automation

`.goreleaser.yaml` builds CGO-free Windows, Linux, and macOS archives for amd64 and arm64 and emits `checksums.txt`. The quality script independently cross-compiles all six OS/architecture targets before publication. Tagged releases run the complete quality gate, build linux/amd64 and linux/arm64 Runner images, sign each digest keylessly, generate/sign `runner-manifest.txt`, and re-download and verify all assets while the release is still Draft. Ordinary CI separates the Docker-free Lite job from the explicit Runner isolation job.
