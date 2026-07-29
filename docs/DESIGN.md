# InterviewCraft TUI Design System

> Version: 1.0  
> Applies to: `interviewcraft` terminal UI (TUI)  
> Scope: color, layout, reusable components, interaction feedback, loading/error/empty states

## 1. Design intent

### Product and audience

InterviewCraft is a local-first terminal application for candidates practicing interviews. Its primary job is to keep a candidate thinking, answering, coding, and reviewing without making them manage a complex interface.

### Visual thesis: **The calm practice ledger**

The UI should feel like a trustworthy terminal record of a training session: precise, quiet, and accountable. It borrows terminal-first discipline from OpenCode-style TUI tools, but it is not a fake terminal in a browser and is not a retro arcade interface.

The one distinctive visual device is **Answer Trace**: a concise, timestamped event rail that records question, answer, Coach help, code run, and evaluation events. It is functional evidence for the final report, not decorative activity.

### Non-negotiable rules

- Information beats decoration. Every line either helps a user act, orient, or understand state.
- Color reinforces state; it must never be the only way to communicate state.
- No gradients, shadows, glass effects, mascots, illustrations, or fake “neon hacker” styling.
- No mouse-only interaction. Every action must have a documented keyboard path.
- The Coach pane is visibly secondary to the main answer/code pane. It must not look like a second interviewer.
- The main question, active text composer, and current keyboard action are always visible at the same time on supported terminal sizes.

---

## 2. Terminal compatibility and layout contract

### 2.1 Supported sizes

| Terminal size | Layout | Required behavior |
|---|---|---|
| ≥160 columns × 48 rows | `Trace + Main + Coach` | All three panes visible. Main pane receives initial focus. |
| 110–159 columns × 36 rows | `Main + Coach` | Trace collapses into a one-line activity summary in the status bar. |
| 80–109 columns × 24 rows | `Main only + Coach overlay` | `c` opens Coach as a full-screen overlay; `Esc` returns to the preserved main draft. |
| <80 columns or <24 rows | Blocked state | Do not render a clipped work surface; show required/current size and `Resize terminal` instruction. |

### 2.2 Stable chrome

Every interactive screen follows the same vertical structure:

```text
┌ InterviewCraft ─ [screen] ─ [provider status] ─────────────────────────┐
│ screen-specific content                                                  │
├─────────────────────────────────────────────────────────────────────────┤
│ [key] action · [key] action · Tab next pane · ? shortcuts               │
└─────────────────────────────────────────────────────────────────────────┘
```

- **Status bar**: exactly one row; contains product name, screen, provider health, and time-sensitive state.
- **Content area**: panes use one-character box drawing borders; never nest more than two border levels.
- **Command bar**: exactly one row; lists only actions usable in the current focus context.
- **Modal/overlay**: covers content only, not the status bar; `Esc` always returns to the preceding focus target.

### 2.3 Pane width priorities

1. Main interview or code editor: flexible remainder, minimum 52 columns.
2. Coach pane: 30–38 columns; never wider than 35% of available width.
3. Answer Trace: 20 columns fixed when visible.

If there is insufficient width, collapse in this order: Trace → Coach → secondary metadata. Never collapse the main composer or code editor.

---

## 3. Color system

The TUI must work in both true-color terminals and ANSI-16 terminals. Code uses semantic tokens, never raw terminal color indices in feature code.

### 3.1 Semantic tokens

| Token | True-color value | ANSI-16 fallback | Use |
|---|---:|---|---|
| `bg.canvas` | `#10110E` | default background | App canvas; do not force if terminal has a custom default unless theme opts in. |
| `bg.panel` | `#181A15` | default background | Selected pane or overlay background; optional in ANSI compatibility mode. |
| `fg.primary` | `#E8E7DF` | bright white | Main body text and content. |
| `fg.muted` | `#A3A69C` | bright black | Metadata, timestamps, inactive labels. |
| `line.rule` | `#3C4035` | bright black | Borders and dividers. |
| `state.focus` | `#D7FF54` | bright green | Focus ring, selected row, primary action. |
| `state.info` | `#77DDF5` | bright cyan | Informational labels, linked evidence. |
| `state.success` | `#9EE493` | green | Passed test, saved state, completed action. |
| `state.warning` | `#FFC857` | yellow | Time pressure, limited hints, recoverable warning. |
| `state.error` | `#FF6B5B` | bright red | Blocking errors and failed test summary. |
| `state.coach` | `#C7B7FF` | bright magenta | Coach identity and learning-map tags only. |

### 3.2 Color application rules

- Use `state.focus` only for one focused control or one active primary action per screen.
- Use `state.coach` only for Coach headers, Coach events, and learning-map labels. Do not use it for errors or primary buttons.
- `state.error` must be paired with `!`, `failed`, or a concrete error verb. `state.success` must be paired with `✓`, `saved`, `passed`, or an explicit completion label.
- A pane may use `bg.panel` only when active, selected, or displayed as an overlay. Default panes remain visually quiet.
- Avoid full-line color fills. Prefer colored labels, left markers, or inverse video on the focused row.
- Support `--theme auto|dark|light` and `--ascii`. In `auto`, preserve the user terminal’s default canvas and map semantic foreground/status tokens only.

### 3.3 Light theme

Light theme is an explicit theme, not an inverted dark theme. It uses `#F2F1EA` canvas, `#20221C` primary ink, `#73776A` muted ink, and retains the same semantic status meanings. Contrast must be at least 4.5:1 for primary text and 3:1 for focus indicators.

---

## 4. Typography, spacing, and terminal primitives

### 4.1 Typography

- Let the terminal decide the font. Recommended fonts: `JetBrains Mono`, `Maple Mono`, or `Iosevka`; Chinese fallback: `Noto Sans Mono CJK SC`.
- Do not rely on Unicode glyph width for layout-critical alignment. Test CJK and emoji-free strings at all supported sizes.
- Use sentence case and concise, concrete verbs: `Start session`, `Run public tests`, `Save profile`, `Open Coach`.
- Use title case only for product/screen names. Avoid exclamation marks except errors requiring immediate attention.

### 4.2 Spacing rhythm

| Token | Terminal units | Use |
|---|---:|---|
| `space.0` | 0 | Dense key/value pairs only. |
| `space.1` | 1 column or 1 row | Inside lists and command bar separators. |
| `space.2` | 2 columns or 1 blank row | Between distinct data groups. |
| `space.3` | 4 columns or 2 blank rows | Between screen regions; use sparingly. |

- One blank row is preferable to extra borders.
- Never create a decorative empty panel. Empty space should mean “available for the next answer” or “no data yet.”

### 4.3 Box characters and ASCII fallback

Use `┌ ─ ┐ │ └ ┘ ├ ┤` for pane frames only. In `--ascii`, map to `+ - |`.

```text
┌ QUESTION 02/03 ───────────────────────────────────────────────┐
│ Explain how you handled cache invalidation in the project.     │
└────────────────────────────────────────────────────────────────┘
```

Do not use repeated hyphens as separators outside ASCII fallback. Do not use emojis as icons.

---

## 5. Reusable component specification

All components are terminal rendering primitives. Feature screens compose them; they do not invent one-off border, status, or focus treatments.

### 5.1 Core primitives

| Component | Purpose | Required states | Reuse rules |
|---|---|---|---|
| `AppShell` | Status bar, content region, command bar | normal, provider-warning, terminal-too-small | Exactly one per screen. |
| `Pane` | Bordered content region | inactive, focused, collapsed, overlay | Use `title`, optional `status`, and child content; no nested `Pane` deeper than two levels. |
| `SectionLabel` | Compact region heading | default, info, coach, warning | Uppercase ASCII label; max 18 visible columns. |
| `KeyHint` | Keyboard affordance | enabled, disabled | Use `[Enter] Start`, not “Click Start”. Must appear in the command bar or next to destructive confirmation. |
| `SelectableList` | Navigate rows | default, focused, selected, disabled, empty | Uses one cursor marker `›`; no checkbox unless multi-select is required. |
| `TextComposer` | Multi-line user input | empty, typing, validation-error, disabled, draft-restored | Shows line count and `Ctrl+Enter submit`; preserves local draft. |
| `StatusBadge` | Small state summary | ready, warning, error, disabled | Always includes text, e.g. `● ready`, `! runner disabled`. |
| `ConfirmPrompt` | Destructive or session-ending confirmation | confirm, cancel | Always maps confirm/cancel to explicit keys and defaults to cancel. |
| `InlineNotice` | Recoverable guidance | info, warning, error, success | One action maximum; use plain language. |
| `ProgressLine` | Determinate task progress | running, complete, failed | Use only with measurable progress. Otherwise use `ActivityLine`. |
| `ActivityLine` | Non-determinate model/parse activity | pending, streaming, failed | Uses text status, not an endless decorative spinner. |

### 5.2 Interview-specific components

| Component | Purpose | Required states | Reuse rules |
|---|---|---|---|
| `AnswerTrace` | Timestamped training evidence rail | idle, appended, collapsed | Only show events that affect the session: question, answer, Coach, code run, pause, report. |
| `QuestionCard` | Current question and constraints | active, timed, code, closed | Exactly one active question at a time. |
| `Timer` | Remaining session/question time | normal, warning (<20%), paused, expired | Always shows text state in addition to color: `12:14 left`, `paused`, `time ended`. |
| `CoachPane` | Separate learning channel | idle, thinking, response, quota-reached, overlay | Visually secondary; Coach text is never rendered inside the interviewer transcript. |
| `HintMeter` | Remaining help budget | available, last hint, exhausted | Shows `1/2 hints used`; strict mode never uses hidden quotas. |
| `CodeEditor` | Code buffer with line numbers | editing, draft-restored, readonly, runner-disabled | Keyboard-first; selected line uses inverse video or focus token. |
| `RunSummary` | Test result summary | not-run, running, passed, failed, timeout | Shows passed/total, time, memory, and safe diagnostic text. |
| `EvidenceLink` | Jump from report finding to source event | normal, missing | Label includes source type and timestamp/question, e.g. `→ answer Q2 14:07`. |
| `LearningGapRow` | One report learning gap | high, medium, low, resolved | Never labels the person; label the skill/topic, e.g. `Redis consistency: review`. |

### 5.3 Component state rules

- A selected row uses `›` plus `state.focus`; an active pane uses a focused title rule. Do not use both inverse video and full background fill unless in `--theme auto` fallback.
- Disabled actions remain visible only when knowing the capability exists is useful. State the reason and recovery action: `Run tests unavailable — enable runner in Settings [s]`.
- A destructive action is always shown in `state.error` only after focus/confirmation, not in its resting state.
- Code/test output is monospaced by nature but must wrap at pane width and preserve line numbers. Truncate long paths from the left, preserving the useful file name and line number.

---

## 6. Key interaction behaviors

### 6.1 Keyboard model

| Key | Meaning | Constraint |
|---|---|---|
| `Tab` / `Shift+Tab` | Move focus between visible panes | Never loses an active draft. |
| `↑` / `↓` | Move selection within a list | Does not scroll unrelated panes. |
| `Enter` | Confirm selected item / activate focused action | Never submits a multi-line answer. |
| `Ctrl+Enter` | Submit current answer or Coach question | Disabled with visible reason when input is invalid. |
| `c` | Open/focus Coach | In narrow mode opens Coach overlay. |
| `r` | Run public tests | Present only in code screen; runner-disabled state explains why. |
| `?` | Open shortcut help | Help is non-destructive overlay; `Esc` closes it. |
| `Esc` | Close overlay, cancel confirmation, or blur composer | Never exits the app directly. |
| `q` | Request quit | If session/draft changed, show `ConfirmPrompt`. |

### 6.2 Focus contract

- Initial focus on a new interview question is `TextComposer`, except code questions where it is `CodeEditor`.
- Returning from Coach overlay restores focus to the exact prior composer/editor cursor position.
- Every focused control has a non-color marker: `›`, bracketed key hint, or inverse text.
- There is no focus trap except modal/overlay content; overlays provide `Esc Back` in the command bar.

### 6.3 Key interaction motion

Motion is deliberately minimal. Terminal refresh must communicate progress without flicker.

| Event | Visual treatment | Duration / limit |
|---|---|---|
| New Answer Trace event | Insert one line, briefly render the timestamp and event label in `state.info`, then return to normal | ≤150 ms; disable with `--reduce-motion` |
| LLM stream | Render received text progressively under `assistant:`; show a static block cursor `▌` at the end | No spinner while text is streaming |
| Unknown-duration work | `ActivityLine`: `· generating question`, then `··`, then `···` | Update at 400 ms; switch to static `· working` with `--reduce-motion` |
| Determinate parsing/import | `ProgressLine`: `[████░░░░] 50% extracting text` | Update only when percentage changes by ≥5% |
| Test run | `running` label + elapsed seconds | No frame animation; replace atomically with `passed`/`failed` |
| Focus change | Move cursor marker and title focus color | Immediate; no easing or fade |

Never animate page transitions, decorative cursors, gradients, or background effects.

---

## 7. Loading, error, and empty states

### 7.1 General state grammar

```text
[symbol] What happened
        What the user can do next. [key] Action
```

Use a concrete verb and the failed dependency. Do not write `Something went wrong`, `Oops`, or `Error 500` as the primary message.

### 7.2 Loading states

| Context | Required visual | Copy | Behavior |
|---|---|---|---|
| Resume parsing | `ProgressLine` inside Profile pane | `Extracting projects and skills — 45%` | Preserve pasted text and allow `Esc Cancel`; cancel leaves no partial profile. |
| Scenario generation | `ActivityLine` in Run Plan | `· creating questions from confirmed profile` | Disable `Start`, keep `Back` available. |
| Interviewer thinking | Transcript placeholder | `interviewer: ▌` | Composer is disabled only after submit; show `Esc stop waiting` if provider supports cancellation. |
| Coach thinking | Coach pane placeholder | `coach: · preparing a L2 hint` | Main composer remains usable and timer continues unless explicitly paused. |
| Code run | RunSummary | `running public tests · 2.4s` | Disable repeated Run; keep editor writable. |
| Report generation | Report header + staged ActivityLine | `scoring evidence → grouping learning gaps → planning next run` | Show stage names; report is not shown until evidence validation completes. |

### 7.3 Error states

| Context | Required visual | Exact message pattern | Recovery |
|---|---|---|---|
| Invalid resume path | Inline error beneath path input | `! Cannot read ~/resume.pdf — file does not exist.` | `[e] Edit path` and `[p] Paste text instead` |
| Resume parse failure | Profile pane notice | `! Could not extract text from this PDF.` | Preserve file name; offer `[p] Paste text` and `[l] View log` |
| LLM unavailable | Status badge + transcript notice | `! Model unavailable — Ollama at localhost:11434 did not respond.` | `[s] Settings`, `[t] Retry`; never discard submitted answer |
| Invalid model output | Inline notice | `! The model returned an invalid interview action.` | Retry once automatically; then show `[t] Retry` / `[x] End question` |
| Coach quota reached | Coach pane warning | `! Hint limit reached for this strict-mode question (1/1).` | `[a] Answer independently` or `[x] End question`; no upsell or shame language |
| Runner disabled | RunSummary warning | `! Public tests are unavailable — Docker runner is disabled.` | `[s] Open settings` |
| Test timeout | RunSummary error | `! Test stopped after 5s — execution time limit reached.` | `[e] Return to editor`; include safe diagnostic only |
| SQLite write error | Blocking AppShell notice | `! Cannot save session — ~/.interviewcraft is not writable.` | `[l] View path`, `[q] Quit safely`; do not continue as if saved |
| Terminal too small | Blocking layout message | `Terminal is 72×22. InterviewCraft needs at least 80×24.` | `Resize terminal, then press [r] Retry` |

### 7.4 Empty states

| Screen | Required visual | Primary action |
|---|---|---|
| Training home, no sessions | `-- no training sessions yet --` with one explanatory line | `[n] Create your first session` |
| Profile, no resume | `-- no resume loaded --` | `[f] Enter file path` or `[p] Paste resume text` |
| Scenario queue, no plan | `-- no scenario plan --` | `[g] Generate plan` |
| Coach, no messages | `-- Coach is ready when you need it --` | Show the 3 most useful shortcuts, not an empty bordered chat history |
| Code output, not run | `-- public tests have not run --` | `[r] Run public tests` or runner setup action |
| Report, no completed session | `-- no report available --` | `[t] Start a training session` |
| Learning map, no Coach use | `-- no Coach questions in this session --` | State that this is neutral, not a missing requirement |

### 7.5 Success feedback

Success feedback is quiet and short-lived. It is recorded in Answer Trace when relevant.

| Event | Copy | Presentation |
|---|---|---|
| Profile saved | `✓ Profile saved locally` | `InlineNotice`, disappears after 2 s unless focused |
| Answer submitted | `✓ Answer recorded` | One Trace event; do not show a toast over the transcript |
| Tests pass | `✓ 4/4 public tests passed · 124ms · 32MB` | Persistent RunSummary until next run |
| Report exported | `✓ Exported to ~/Downloads/interview-report.md` | Persistent until navigation, with `[o] Open folder` only if platform supports it |
| Data deleted | `✓ Session and derived report deleted` | Persistent confirmation on the destination screen |

---

## 8. Screen composition rules

### 8.1 Training screen

- One primary action only: `Start new session` or `Resume session`.
- Recent sessions and practice queue share the same `SelectableList` row structure.
- A score never appears without a label such as `technical 3/5`; do not show a single “candidate score” as the hero.

### 8.2 Profile screen

- File path, pasted resume, target role, level, and JD are inputs; derived projects/skills are reviewable data.
- Facts use `✓ confirmed`; inferences use `? verify`. Never distinguish them by color alone.
- Editing a fact opens an inline composer rather than a separate full-screen form unless terminal width is below 80 columns.

### 8.3 Scenario screen

- The selected template, mode, and duration stay in the top summary row while Run Plan is scrolled.
- Each question row shows sequence, intent, time budget, and evidence tag; no question is an opaque model output.
- Strict/Standard/Coach policy must show both quota and the highest permitted help level.

### 8.4 Interview and Coach screen

- Transcript order is immutable; corrections are appended as a user event, never silently edit past messages.
- Interviewer labels use `state.info`; user labels use `fg.primary`; Coach labels use `state.coach`.
- Coach replies render with an explicit `L1`–`L4` marker and `1/2 hints used` meter.
- The command bar depends on focus. In the main composer, show submit/end/Coach. In Coach, show ask/mark-understood/return.

### 8.5 Code screen

- Problem specification and editor are separate panes; RunSummary occupies the bottom row/region and is always visible after the first run.
- Syntax highlighting is optional and must not be required to distinguish errors, selected line, or focus.
- Errors from code execution use user-safe messages. Never render container paths, host paths, stack traces with secrets, or raw hidden-test input.

### 8.6 Report screen

- The report begins with session facts, not celebratory prose: scenario, duration, mode, questions, hints, code-run state.
- Findings are grouped by `Keep / Improve / Practice next`, with no more than three action items in each group.
- Every score/finding provides an `EvidenceLink`. A missing link renders `evidence unavailable`, not a fabricated explanation.
- Learning gaps name a topic or behavior, never a trait: use `Clarifying constraints: practice`, not `Poor communicator`.

---

## 9. Accessibility and quality gates

### 9.1 Accessibility requirements

- All essential controls are keyboard operable and discoverable through `?` help.
- Focus is visible using a cursor marker or inverse treatment in addition to color.
- `--reduce-motion` makes all non-determinate activity static and disables the Answer Trace flash.
- `--ascii` replaces Unicode borders/symbols; strings must remain understandable.
- No color-only status. Every status includes a glyph and text.
- All layout decisions must be tested with CJK content, long file paths, long model names, and 80-column terminals.

### 9.2 Design acceptance checklist

Before merging a TUI screen or component, verify:

- [ ] Uses semantic color tokens and no feature-local raw color code.
- [ ] Uses an existing primitive or documents why a new primitive is necessary.
- [ ] Has focus, disabled, loading, error, and empty behavior where applicable.
- [ ] Has a keyboard path and appears in context-sensitive `?` help.
- [ ] Does not rely on Unicode width assumptions; passes `--ascii` smoke test.
- [ ] Does not show an error without a human-readable cause and recovery action.
- [ ] Preserves user drafts through focus changes, Coach overlay, provider retry, and terminal resize.
- [ ] Renders correctly at 160×48, 120×36, and 80×24.
- [ ] Adds only motion that communicates state; passes `--reduce-motion` test.

---

## 10. Implementation handoff

### Theme interface

The rendering layer should expose semantic roles, not terminal library colors:

```go
type Theme struct {
    Canvas, Panel, Primary, Muted, Rule Color
    Focus, Info, Success, Warning, Error, Coach Color
    UseASCII bool
    ReduceMotion bool
}
```

### Component interface conventions

- Components receive data and a `UIState`; they do not call LLMs, mutate SQLite, or perform Docker work directly.
- Async operations publish typed state changes (`Pending`, `Streaming`, `Succeeded`, `Failed`) to the screen model.
- Screen models own focus order and command-bar bindings; primitives only render the passed state.
- All error copy comes from typed domain errors, not ad-hoc strings in renderer code.

### Copy voice

Use calm, direct, non-judgmental Chinese. Name the operation and next action.

| Avoid | Use instead |
|---|---|
| `出错了，请重试` | `无法连接 Ollama。检查服务是否运行后按 [t] 重试。` |
| `你的答案不够好` | `这段回答缺少量化结果。可补充影响范围或指标。` |
| `暂无数据` | `还没有训练记录。按 [n] 创建第一场模拟。` |
| `处理中…` | `正在生成项目深挖问题…` |

