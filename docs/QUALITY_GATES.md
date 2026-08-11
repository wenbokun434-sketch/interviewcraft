# MVP quality gates

The release gate is executable, not a checklist inferred from past results.

## Commands

Docker-free Lite gate:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1 -SkipRunnerIsolation
```

Complete release gate:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1
```

The script checks `gofmt`, `git diff --check`, both Go modules, `go mod verify`, `go vet`, root coverage tests, migration tests, the nested Runner agent tests, a CGO-free host binary and Windows/Linux/macOS × amd64/arm64 release matrix, a fresh-install smoke, and—unless explicitly skipped—the real Docker isolation gate. Any failed native command terminates with a non-zero exit.

`go test ./...` at the repository root does not include `docker/runner/agent`, because it is a nested module. A release is incomplete if only the root module ran.

## Full journey and evidence coverage

`internal/e2e/TestLiteMVPJourneyFromFreshInitThroughTransfer` runs, with `RUNNER_MODE=disabled` and embedded SQLite only:

`init → doctor → run → pasted resume → confirmed/locked profile → confirmed scenario → text interview → Coach → optional in-process coding seam → evidence report → next practice session → export → empty Lite import → restored run`

The test asserts staged lifecycles for real asynchronous operations, the pre-run `LatestRun=nil` empty state, an 80×24 ASCII/reduce-motion frame, exclusion of runtime endpoint/secret values from transfer, and 100% conservative conclusion coverage. A conclusion counts as covered only when it is evidence-backed with at least one evidence ID or explicitly insufficient with no evidence ID. `Document.Validate` additionally resolves every referenced ID.

## One-command deployment four-state matrix

`scripts/test-deployment-e2e.ps1` is the auditable deployment entrypoint. It runs the real process lifecycle separately from the complete training journey and emits `interviewcraft-deployment-evidence-v1`. The CI workflow executes the README commands on Windows, Ubuntu, and macOS; Full Practice runs only on the dedicated Ubuntu Docker job.

| Deployment surface | Main path | Loading/cancel/retry | Empty state | Dependency/error and recovery |
| --- | --- | --- | --- | --- |
| Install and setup | `TestCleanDeploymentInstallSetupUpdateRollbackUninstall` covers Lite and Private Local install, same-version reinstall, setup, and doctor | Installer output must contain stages 1/7 through 7/7; `TestSetupProfilesMainEmptyAndProgress`, `TestSetupCancelResumesAtSafeCheckpoint`, and `TestSetupFullReportsRunnerStagesAndCancellation` assert ordered async progress and resumable cancellation | Fresh isolated home/data/install/PATH files; `TestNoProviderEmptyStateKeepsHistoryAndBlocksNewScenario` gives one Provider action | Installer fixtures cover missing platform, network/truncation, signature/hash, traversal, disk/PATH/setup failures; `TestSetupKeyringFailureRequiresEnvironment` and `TestSetupConfigFailureRollsBackCredential` preserve prior state and secrets |
| Training and restart | `TestLiteMVPJourneyFromFreshInitThroughTransfer` covers profile, scenario, interview, Coach, coding seam, report, next round, transfer, and restored run | Domain and TUI tests assert Pending/Streaming/Succeeded/Failed, keyboard cancellation, retry idempotency, stale-token discard, editable drafts, and no duplicate evidence | New training/profile/scenario/coding/report/settings tests each assert their single primary next action | Provider/schema/SQLite/terminal tests retain drafts/evidence and return redacted actionable errors; `TestSecretNeverAppearsInProviderError` prevents credential disclosure |
| Update and rollback | Both clean tiers run check, installer-delegated update, doctor, no-update check, explicit rollback, and version/data assertions | `TestRunUpdateAndRollbackCompleteData` asserts all eight update stages; post-switch cancellation enters automatic rollback; concurrent Store/updater is rejected | `TestRunNoUpdateCreatesNoBackup`, `TestRunCheckOnlyReportsAvailableWithoutBackup`, and the process no-update check create no false rollback point | The process test rejects an invalid bundle then succeeds on retry; pre/post-switch matrices cover Release API, signature, checksum, disk, backup, replacement, migration, doctor, cancellation, corrupt backup, and rollback validation while preserving matching binary/data |
| Full Runner | Full setup validates signed immutable metadata before enabling Docker; isolation runs Python, JavaScript, and Java | Full setup cancellation/resume and Docker execution cancellation have bounded cleanup | Lite/Private Local never contact Docker; missing daemon/image/manifest leaves Runner disabled | Registry, pull, signature, digest, label/user/protocol/smoke failures remain disabled; real isolation covers timeout, OOM, network/process attacks and requires zero residual containers |
| Uninstall | CLI/platform uninstall removes receipt-owned binary/PATH entries | Windows helper waits for the parent process; Unix removes atomically after checks | Missing rollback point and already-absent managed PATH entries are safe explicit results | Malformed receipt/PATH markers, credential-store purge failure, broad paths, symlinks, and install/data overlap fail closed; default uninstall retains configuration, credentials, SQLite data, reports, and backups |

The matrix is additive: it does not replace the package-level P-01 through P-07 states, installer fixture fault matrix, release metadata tamper tests, migration tests, six-target builds, or Docker isolation gate. Cross-compilation is build evidence only. A platform lifecycle is marked passed only by its native CI/local invocation and uploaded evidence.

## P-01–P-07 four-state matrix

| Product surface | Main path | Loading | Empty | Dependency/error | Responsive and keyboard evidence |
| --- | --- | --- | --- | --- | --- |
| P-01 Training | `training/TestMainNavigationResumesExactSessionAndOpensReportAndQueue` | `TestStreamingStateRendersWithoutBlockingNavigation` | `TestEmptyHomeHasSinglePrimaryPathToProfile` | `TestLoadCoversPendingSucceededAndFailedLifecycle` | `TestResponsiveSnapshotsAndAccessibilityModes`, `TestHelpRestoresExactFocusAndResizePreservesState` |
| P-02 Profile | `profile/TestKeyboardFormPathParseEditLockDeleteAndSave` | `TestParseProgressCanCancelWithoutLosingInputOrCreatingProfile` | `TestEmptyWorkbenchBlocksContinueAndShowsAction` | `TestPathFormatAndSaveFailuresAreActionableAndPreserveDraft` | `TestResponsiveSnapshotsCJKLongPathAndASCII`, `TestFocusResizeAndInlineDraftArePreserved` |
| P-03 Scenario | `scenario/TestGenerateEditDeleteRefreshAndStartLocksVersion` | `TestGenerationLoadingKeepsBackAvailableAndStartDisabled` | `TestEmptyPlanShowsActionAndBlocksDeleteAndStart` | `TestProviderAndSchemaFailuresAreActionableAndPreservePlan` | `TestFocusHelpResizeAndResponsiveSnapshots` |
| P-04 Interview + Coach | `interview/TestInterviewRoomCompleteKeyboardFlowAndTrace`, `TestCoachPaneQuickFreeAskOutcomePauseAndResponsiveFocus` | `TestThinkingCancelPreservesSubmittedAnswerAndRetry`, `TestCoachThinkingKeepsMainInputAvailableAndIsolated` | `TestEmptyRoomAndResponsiveDraftRecovery` | `TestInvalidModelOutputOffersRetryOrSafeEnd`, `TestCoachProviderErrorRetryAndQuotaRecovery` | Coach overlay/focus tests plus 160×48, 120×36, 80×24 snapshots in the same files |
| P-05 Coding | `coding/TestWorkbenchKeyboardThreeLanguagesDraftRestoreResetRunExplainAndReturn` | `TestRunStreamsElapsedPreventsDuplicateAndKeepsEditorWritable` | `TestRunSummaryFourStatesAndErrorsNeverLeakCause` (`not-run`) | same test covers disabled/failed/timeout/OOM/error | `TestResponsiveCJKASCIIHelpFocusAndLongSafeErrorSnapshots`, `TestBlockedTerminalShowsActionableMinimum` |
| P-06 Report | `report/TestReportMainFlowBrowsesEvidenceAndStartsNextPractice` | `TestReportLoadingGenerationEmptyAndFailureStates` | same test's empty case | same test plus `TestReportDeletionRequiresConfirmationAndHandlesFailure` | `TestReportRendersNormalAtAllBreakpointsWithoutHeroScore`, evidence-focus tests |
| P-07 Settings + Data | `settings/TestConnectionLifecycleAndReadyState`, `TestDataVaultMainLoadingEmptyAndFailureStates` | `TestLoadingStateAndFactoryError`, Data Vault loading case | `TestNoProviderEmptyStateKeepsHistoryAndBlocksNewScenario`, Data Vault empty case | authentication/model diagnostics, save failure, import/missing dependency, typed operation failure | `TestResponsiveSettingsSnapshotsAndFocus`, `TestDataVaultResponsivePrivacyChoiceAndExportProgress` |

Package paths are under `internal/tui/screens/`. The matrix intentionally points to test names rather than snapshot files so refactors fail at compile/test time instead of leaving a stale manual claim.

## Geometry and accessibility

Automated snapshots and geometry checks cover 160×48, 120×36, and 80×24 layouts; below 80×24 produces an actionable blocked state. Cross-screen evidence includes CJK width, long paths/model/error text, ASCII borders, no-color mode, reduce-motion static activity, focus restoration, overlays, rune cursor positions, and keyboard-only command paths.

Color is never the only state signal. Loading, ready, warning, error, evidence availability, Coach level, and run status all have text or symbols with ASCII alternatives.

## Security and AI quality

- Contract/schema tests reject unknown fields and invalid evidence IDs.
- Profile/scenario/interviewer/Coach/evaluator tests enforce their distinct context boundaries.
- Strict Coach adversarial tests reject 50 solution-extraction prompts and executable-code bypasses.
- Report validation permits only evidence-backed, not-applicable, or insufficient conclusions; the E2E coverage counter must remain 100%.
- Transfer tests reject corrupt/version-incompatible/relationally invalid packages and prove secrets and default Coach content are absent.
- Runner unit tests validate safe command construction and protocol redaction without Docker; the explicit isolation script performs real attack tests and container cleanup checks.

## Race testing

Run targeted `go test -race` where a supported CGO C toolchain exists. On Windows hosts without a C compiler this gate is recorded as N/A, never passed. Race availability does not replace deterministic state, idempotency, cancellation, and persistence tests.
