# InterviewCraft

InterviewCraft is a local-first terminal application for practicing interviews from a confirmed resume and target role.

The MVP is under active development. The product scope is defined in
[`docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md`](docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md),
and the terminal design contract is defined in [`docs/DESIGN.md`](docs/DESIGN.md).
Implementation order and acceptance gates live in [`TODO.md`](TODO.md).

## Current status

The Lite runtime can now initialize its local SQLite workspace and diagnose the
terminal, data directory, database, model endpoint, and optional Docker runner.
The `run`, `export`, and `import` commands remain visible placeholders until
their ordered TODO tasks are completed.

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

## Initialize and diagnose Lite

By default, InterviewCraft stores `config.json`, `interviewcraft.db`, uploads,
exports, and logs under `~/.interviewcraft`. Initialization is idempotent:

```powershell
.\interviewcraft.exe init
.\interviewcraft.exe doctor
```

`doctor` reports blocking checks with a non-zero exit code. A disabled or
unavailable optional Docker runner is reported as a warning and does not block
Lite mode.

Runtime settings can be supplied through environment variables. API keys are
referenced by environment-variable name and are never written to the config
file or diagnostic output.

| Variable | Purpose | Default |
| --- | --- | --- |
| `INTERVIEWCRAFT_DATA_DIR` | Local data and config directory | `~/.interviewcraft` |
| `INTERVIEWCRAFT_LLM_PROVIDER` | `openai-compatible` or `ollama` | unset |
| `INTERVIEWCRAFT_LLM_ENDPOINT` | Model service base URL | unset |
| `INTERVIEWCRAFT_LLM_MODEL` | Model name | unset |
| `INTERVIEWCRAFT_LLM_API_KEY_ENV` | Name of the environment variable containing the API key | `OPENAI_API_KEY` |
| `RUNNER_MODE` | `disabled` or `docker` | `disabled` |
| `AUDIO_PROVIDER` | Audio provider selector | `browser` |
