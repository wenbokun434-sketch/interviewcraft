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

The installer trust sequence is fixed: verify the downloaded Cosign v3.1.3 executable against `scripts/cosign-v3.1.3-sha256.txt`; verify the manifest bundle against the exact tagged release workflow identity and GitHub Actions OIDC issuer; strictly parse the manifest; verify archive SHA-256 and size; reject traversal, links, and unexpected executables; execute the staged binary's `version --json`; then atomically place it. A secret-free receipt at `~/.interviewcraft/install-receipt.txt` records the exact binary, canonical data directory, and PATH files for update, rollback, and uninstall.

Reinstalling the same version is idempotent. A different newer version is delegated to `interviewcraft update`; the installer never overwrites the installed binary directly. `scripts/uninstall.ps1` and `scripts/uninstall.sh` remove only receipt-owned binary/PATH entries and preserve configuration, credentials, SQLite data, reports, and rollback backups by default.

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

## Verified update, backup, and restore

Use the installed receipt-owned binary:

```text
interviewcraft update --check
interviewcraft update
interviewcraft rollback
```

The updater verifies the exact tagged-workflow Sigstore identity, strict release manifest, archive hash/size, platform, embedded version, available disk, installation receipt, and executable identity before it requests the maintenance lock. The cross-process lock waits for every open Store to close and prevents a second updater or ordinary SQLite process from entering. It then creates a new immutable backup under the data directory's sibling `.<name>-backups` directory. The backup manifest hashes every regular file, preserves empty directories and modes, and includes the currently working binary; symbolic links and special files are rejected.

After an atomic binary switch, the new binary runs embedded migrations and `doctor` while holding a token-bound maintenance guard. Any switch, migration, doctor, Runner verification, receipt commit, cancellation, or interrupted-state failure restores both the prior binary and the complete prior data directory. The failed directory and bounded command diagnostics are retained under the backup root. Windows performs the live executable replacement from a copied helper after the parent exits; Linux and macOS use same-directory atomic rename. Do not copy `interviewcraft.db` independently, edit `_schema_migrations`, or modify a backup in place.

`rollback` verifies the stored backup before modifying the installation and first creates a forward recovery backup of the current version. If the restored version fails migration/doctor, the forward snapshot is restored. No update is a successful empty state and creates neither a backup nor a rollback point.

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

## Safe uninstall and purge

`interviewcraft uninstall` and the platform uninstall scripts remove only receipt-owned PATH blocks and the installed binary. Configuration, SQLite data, reports, rollback backups, and system credentials remain available for reinstall.

Data purge is deliberately separate and double-confirmed:

```text
interviewcraft uninstall --purge-data --confirm-purge "/exact/canonical/.interviewcraft"
```

The confirmation must equal the canonical data directory stored in the installation receipt. Purge refuses volume roots, the user home, temporary/work directories, symbolic links, and any target overlapping the install directory. The corresponding `InterviewCraft` keyring account is removed before filesystem deletion; an unavailable credential store fails closed instead of falling back to plaintext or broad deletion.

## Release automation

`.goreleaser.yaml` builds CGO-free Windows, Linux, and macOS archives for amd64 and arm64 and emits `checksums.txt`. The quality script independently cross-compiles all six OS/architecture targets before publication. Tagged releases run the complete quality gate, build linux/amd64 and linux/arm64 Runner images, sign each digest keylessly, generate/sign `runner-manifest.txt`, and re-download and verify all assets while the release is still Draft. Ordinary CI separates the Docker-free Lite job from the explicit Runner isolation job.

## Clean deployment certification

The `deployment-e2e` workflow runs the repository's documented command on `windows-latest`, `ubuntu-latest`, and `macos-latest`. Each job creates an isolated home, loopback-only Provider/release fixture, two real versioned application binaries, and a user-owned install directory. Lite and Private Local both perform install, idempotent reinstall, setup, doctor, 80x24 run, process restart, signed update, tamper rejection without mutation, no-update empty state, rollback, and uninstall while retaining configuration and a data marker. The same job separately executes the complete Lite training/evidence journey.

An Ubuntu Docker job adds Full setup state/cancellation/recovery tests, signed Runner release metadata, all three language paths, resource attacks, and the zero-residual-container assertion. It never makes Docker, Go, Node.js, Python, Java, an external database, or elevation a Lite runtime prerequisite; build tools belong only to repository acceptance jobs.

Every successful or failed invocation writes `interviewcraft-deployment-evidence-v1` JSON with the platform, architecture, tested application versions, Go version, commit SHA, worktree-dirty flag, UTC timestamps, duration, and named gate results. CI uploads one artifact per platform. A local report may claim only the host/container combinations actually executed; cross-compiling the six release targets validates buildability but does not certify a macOS lifecycle.
