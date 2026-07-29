# InterviewCraft

InterviewCraft is a local-first terminal application for practicing interviews from a confirmed resume and target role.

The MVP is under active development. The product scope is defined in
[`docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md`](docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md),
and the terminal design contract is defined in [`docs/DESIGN.md`](docs/DESIGN.md).
Implementation order and acceptance gates live in [`TODO.md`](TODO.md).

## Current status

T-001 provides the Go single-binary command skeleton. The `init`, `run`,
`doctor`, `export`, and `import` commands are visible in help but intentionally
remain unavailable until their ordered TODO tasks are completed.

Lite mode will not require Node.js, Docker, PostgreSQL, Redis, a message queue,
or any other resident service. Docker remains an optional code-runner
dependency.

## Build from source

Requirements:

- Go 1.26 or newer

Run the current checks and build the command:

```powershell
go test ./...
go build ./cmd/interviewcraft
.\interviewcraft.exe --help
```

No API key, configuration file, network connection, or Docker installation is
needed to show the command help.
