# ADR-0001: Single-binary Lite runtime with optional adapters

- Status: Accepted
- Date: 2026-08-03

## Context

InterviewCraft must run the complete text-practice loop on a candidate's machine without requiring infrastructure administration. The product also needs model access and optional untrusted-code execution, but those concerns have different trust and dependency profiles from local training state.

## Decision

The MVP application is a statically buildable Go command with embedded SQLite and a local data directory. Runtime configuration is loaded from a local config file plus environment overrides. The application talks to one OpenAI-compatible or Ollama model Provider through an adapter.

`RUNNER_MODE=disabled` is the default. Code execution is an optional Docker adapter and does not participate in application startup, ordinary tests, text interviews, Coach requests, or report generation. The Runner image contains Python, JavaScript, and Java; the host application does not depend on those runtimes.

Durable training state remains local. Model inputs are constructed at typed core boundaries and include only the confirmed or submitted evidence allowed for that Agent. Secrets remain process environment values; the config stores only an environment-variable name.

## Consequences

- A release consists of one platform binary plus documentation and checksums.
- Fresh Lite installation needs no external database, Node.js, Java, Python, or Docker.
- SQLite migrations are owned by the binary and apply on open.
- Optional dependency failures disable only their feature and remain visible through `doctor` and Settings.
- Transfer packages provide explicit migration between Lite instances and exclude Provider configuration and secrets.
- Docker daemon access is a separate trusted-administrator decision and is covered by a dedicated isolation gate.

## Alternatives rejected for the MVP

- A mandatory service stack with PostgreSQL, Redis, queues, or object storage adds operational cost without improving the single-user practice loop.
- Running submitted code in the main process or against host language runtimes cannot provide the required isolation boundary.
- Persisting API key values in SQLite or config would expand the breach and export surface.

## Non-goals

This decision does not introduce cloud accounts, multi-user tenancy, collaboration, payments, ATS integrations, covert real-interview assistance, video/personality analysis, or hiring predictions. Voice is optional and is not part of the release gate.
