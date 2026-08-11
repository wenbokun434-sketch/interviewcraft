# Security model

InterviewCraft handles private resume text, interview answers, model traffic, and optionally untrusted source code. The default boundary is a single local user and a user-selected model Provider.

## Local data and secrets

- Profiles, drafts, events, code evidence, and reports are stored in the selected local SQLite data directory.
- Resume facts retain exact source spans. Unconfirmed inferences cannot be used as confirmed evidence.
- API key values are read from process environment variables. Config stores only the variable name; diagnostics, UI, reports, and exports must not render the value.
- Transfer packages exclude runtime Provider configuration and secrets. Coach transcript text is opt-in.
- Deletion is explicit and transactional. Report conclusions resolve to surviving evidence or degrade to insufficient evidence.

Protect the data directory with operating-system permissions appropriate for private resume material. InterviewCraft does not encrypt the local database at rest; use full-disk or directory encryption when the device threat model requires it.

## Agent context isolation

- Profile structuring distinguishes source-backed facts from unconfirmed inferences.
- Scenario planning receives confirmed facts only.
- Interviewer receives submitted answers and executed code snapshots, but not answer drafts, unconfirmed inferences, or Coach response text.
- Coach receives confirmed facts, submitted answers, and executed code snapshots, but not unsubmitted answer/code drafts or previous Coach response text.
- Evaluator conclusions must reference durable evidence or be marked insufficient. Personality and hiring judgments are rejected.
- Strict Coach mode rejects complete solutions and executable answer bypasses.

## Optional Docker Runner

`RUNNER_MODE` defaults to `disabled`. It is enabled only by Full setup after verifying the signed release manifest and exact immutable image digest. The accepted certificate identity is the tagged repository release workflow and the issuer is GitHub Actions OIDC. Doctor and runtime startup re-check the signature, official repository digest, linux/amd64 or linux/arm64 architecture, application version, protocol label, `io.interviewcraft.runner=true`, and default user `65532:65532`.

Every submitted program runs in a newly created container with:

- `--network none` and `--ipc none`;
- `--read-only` root filesystem and no host mounts;
- non-root UID/GID `65532:65532`;
- `--cap-drop ALL` and `no-new-privileges=true`;
- CPU `0.50`, memory and swap `256m`, PID limit `64`;
- `nproc` and `nofile` ulimits, including file-descriptor limit `64`;
- no-execute temporary filesystems and bounded tmpfs capacity;
- no host environment forwarding and Docker log driver `none`;
- an application wall-clock deadline and forced `docker rm --force --volumes` on success, failure, timeout, OOM, cancellation, and protocol errors.

The host sends only a versioned question ID, language, and submitted source over stdin. Test suites stay in the image. The public response schema can expose only:

- public test name and passed/failed/error status;
- hidden-test passed and failed counts;
- an enumerated error kind;
- duration and peak-memory counters.

Hidden inputs, expected outputs, test source, raw stderr, submitted source, environment values, secrets, and host/container paths have no public protocol field. Invalid or oversized protocol data is rejected.

Release images are built with `docker/runner` as the complete build context. Do not change the context to the repository root. The dedicated gate validates Python, JavaScript, and Java happy/failing cases plus infinite-loop, network, memory, and process-fork attacks, then requires zero residual integration containers:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-runner-isolation.ps1
```

Do not remove an isolation flag to make a language test pass. A failed Registry request, signature, digest, architecture, label, user, protocol, daemon, smoke, or cancellation gate removes a newly introduced image reference and leaves the persisted mode disabled. Lite and Private Local never contact Docker or the Runner registry.

## Trust boundary and limitations

The Docker daemon is privileged infrastructure and must be administered as trusted software. The Runner reduces the impact of an untrusted candidate program but is not a multi-tenant cloud sandbox or a defense against a compromised daemon/kernel. Do not expose the local Docker socket or InterviewCraft data directory to untrusted users.

Model Providers receive the minimum typed context required for their role, but they remain external processors unless a loopback local model is used. Review a Provider's retention and privacy policy before sending resume data.

InterviewCraft is a practice tool. It is not designed for covert live-interview assistance, employment decisions, personality assessment, or safety-critical evaluation.

## Deployment fixture boundary

Deployment acceptance never relaxes production verification. Its release and Provider server binds only to `127.0.0.1`, and its deterministic Cosign stand-in is accepted only when the explicit installer/updater test-mode variables are set. The executable must live below the operating-system temporary directory and match the SHA-256 supplied by the test harness. The fixture package is not included in GoReleaser archives.

The process E2E first proves an invalid bundle cannot change the installed version or user data, then restores the fixture bundle and exercises the normal updater. Production installers and updaters continue to require Cosign v3.1.3, the exact tagged release workflow identity, the GitHub Actions OIDC issuer, strict manifest parsing, archive hash/size, embedded version, and safe extraction. CI evidence contains no Provider key, credential, resume, database, or machine-specific data directory.

## Reporting a vulnerability

Do not publish an exploit, resume sample, secret, hidden test, or container-escape detail in a public issue. Use the repository host's private security advisory channel and include the affected commit, platform, Runner mode, minimal reproduction, and impact. If no private channel is available, contact the maintainers before disclosing technical details publicly.
