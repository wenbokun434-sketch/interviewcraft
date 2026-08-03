# Contributing to InterviewCraft

## Before you start

Read the [MVP PRD](docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md), [design system](docs/DESIGN.md), [Lite runtime ADR](docs/ADR-0001-lite-runtime.md), and the current ordered item in [TODO.md](TODO.md). Keep changes inside that item's declared scope and do not implement the next task early.

Go 1.26 or newer is required. Docker is optional and is needed only for Runner image/integration work and the complete release gate. Lite development must not require Node.js, Java, Python, an external database, or a running Docker daemon.

## Local workflow

1. Create a focused branch and inspect `git status`, the current diff, and recent commits.
2. Add or update a failing test that expresses the requested contract.
3. Make the smallest scoped implementation change.
4. Run the affected package tests, then the Docker-free quality gate.
5. If Runner code, image, protocol, or resource policy changed, run the complete isolation gate.
6. Update user/security/quality documentation when behavior or an operational boundary changed.
7. Review the staged diff and commit only files belonging to the task.

Windows PowerShell:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1 -SkipRunnerIsolation
```

Runner changes:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-runner-isolation.ps1
```

The root Go module and `docker/runner/agent` are separate modules. Do not report the agent as tested from a root-only `go test ./...`.

## Architecture rules

- Core packages own typed domain policy and must not invoke TUI or Docker directly.
- Adapters translate external Provider, resume, and Runner protocols into safe core types.
- Screens render typed state and call services; they do not construct hidden model or Docker contexts.
- SQLite events and executed code snapshots are durable evidence. Do not mutate them to simplify UI behavior.
- Facts must resolve to source spans; unconfirmed inferences never become facts implicitly.
- Interviewer cannot read Coach response text. Coach cannot read unsubmitted answer/code drafts or prior Coach response text.
- Evaluator conclusions reference durable evidence or become explicitly insufficient. Do not add personality or hiring judgments.
- `RUNNER_MODE` stays disabled by default. Never remove an isolation flag or expose raw stderr/paths/hidden cases to make a test pass.

## Tests and snapshots

Every P-01–P-07 behavior change should preserve main, loading, empty, and dependency/error states. Interactive changes also cover keyboard focus, resize, 160×48, 120×36, 80×24, blocked small terminals, CJK/long content, ASCII, no-color, and reduce-motion where applicable.

Use stable semantic assertions. Snapshot changes must be reviewed for information hierarchy, clipping, focus, and safe error content—not accepted mechanically.

For migrations, test fresh creation, repeat open, upgrade, invalid/renamed versions, and rollback. Never edit an applied migration in place.

## Pull request checklist

- The change matches one declared task or one focused bug.
- Ordinary tests pass with Docker unavailable and `RUNNER_MODE=disabled`.
- `gofmt`, `git diff --check`, module verification, vet, tests, and binary build pass.
- The nested Runner agent was tested separately when relevant.
- No secret, raw Provider error, path, hidden test, or unconfirmed inference entered logs/UI/export/report output.
- New conclusions have evidence or an insufficient state.
- README and operational docs match the shipped command behavior.
- No generated binaries, databases, transfer packages, Docker logs, or temporary caches are committed.

## Security reports

Follow [the private vulnerability-reporting process](docs/SECURITY.md#reporting-a-vulnerability). Do not include live secrets, private resumes, hidden tests, or working container-escape details in a public issue.
