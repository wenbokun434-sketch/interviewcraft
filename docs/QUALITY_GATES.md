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
