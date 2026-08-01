// Package report implements the evidence-first P-06 report screen.
package report

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/evaluation"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

const (
	focusScorecard      = "scorecard"
	focusReviews        = "question-reviews"
	focusLearning       = "learning-map"
	focusPractice       = "practice-next"
	focusEvidence       = "evidence"
	focusHelp           = "help"
	focusDelete         = "delete-report"
	focusEvidenceDetail = "evidence-detail"
)

// Loader restores a persisted, validated report.
type Loader interface {
	Get(context.Context, string) (corereport.Document, bool, error)
}

// Generator creates a report for an evaluation-pending session.
type Generator interface {
	Generate(context.Context, string, evaluation.Observer) (evaluation.Result, error)
}

// Deleter removes one report and all data derived from its payload.
type Deleter interface {
	DeleteReport(context.Context, string, string) (bool, error)
}

// Data is the report screen's typed lifecycle payload.
type Data struct {
	Report *corereport.Document
	Stage  string
}

// StateObserver receives load, generation, and deletion lifecycle states.
type StateObserver func(async.State[Data])

// Destination identifies an app-level navigation target.
type Destination string

const (
	DestinationNone     Destination = ""
	DestinationTraining Destination = "training"
	DestinationScenario Destination = "scenario"
	DestinationEvidence Destination = "evidence"
	DestinationQuit     Destination = "quit"
)

// Intent distinguishes navigation from a confirmed destructive request.
type Intent string

const (
	IntentNone          Intent = ""
	IntentOpenEvidence  Intent = "open-evidence"
	IntentStartPractice Intent = "start-practice"
	IntentDeleteReport  Intent = "delete-report"
)

// PracticeSeed carries the report recommendation into scenario creation.
type PracticeSeed struct {
	ID                 string
	SessionID          string
	ReportID           string
	Topic              string
	Mode               contracts.ScenarioMode
	DurationMinutes    int
	CompletionCriteria string
}

// Action is returned to the application controller for navigation or work.
type Action struct {
	Intent      Intent
	Destination Destination
	SessionID   string
	ReportID    string
	EvidenceID  contracts.EvidenceID
	QuestionID  string
	Practice    *PracticeSeed
}

// Options wires the report screen without coupling it to SQLite or a Provider.
type Options struct {
	SessionID string
	ReportID  string
	Reports   Loader
	Evaluator Generator
	Deleter   Deleter
	Width     int
	Height    int
	Theme     theme.Theme
}

// Model owns report loading, keyboard focus, overlays, and safe deletion.
type Model struct {
	sessionID string
	reportID  string
	reports   Loader
	evaluator Generator
	deleter   Deleter
	state     async.State[Data]
	focus     *layout.FocusModel

	selectedScorecard int
	selectedReview    int
	selectedLearning  int
	selectedPractice  int
	selectedEvidence  int
	helpOpen          bool
	deleteConfirmOpen bool
	deleteAuthorized  bool
	evidenceOpen      bool
	missingEvidence   bool
	operationErr      *domainerr.Error

	Width    int
	Height   int
	Theme    theme.Theme
	Provider components.StatusBadge
}

// New creates a pending report screen with the evidence-backed scorecard in focus.
func New(options Options) (*Model, error) {
	focus, err := layout.NewFocusModel(
		focusScorecard,
		focusReviews,
		focusLearning,
		focusPractice,
		focusEvidence,
	)
	if err != nil {
		return nil, err
	}
	return &Model{
		sessionID: strings.TrimSpace(options.SessionID),
		reportID:  strings.TrimSpace(options.ReportID),
		reports:   options.Reports,
		evaluator: options.Evaluator,
		deleter:   options.Deleter,
		state:     async.NewPending[Data](),
		focus:     focus,
		Width:     options.Width,
		Height:    options.Height,
		Theme:     options.Theme,
		Provider: components.StatusBadge{
			State: components.BadgeReady,
			Text:  "local report",
		},
	}, nil
}

// State returns the current typed report lifecycle.
func (model *Model) State() async.State[Data] {
	if model == nil {
		return async.State[Data]{}
	}
	return model.state
}

// ActiveFocus exposes the logical focus for controller and regression tests.
func (model *Model) ActiveFocus() string {
	if model == nil || model.focus == nil {
		return ""
	}
	return model.focus.Active()
}

// Load restores an existing report, generates a missing pending report when an
// evaluator is available, or returns an actionable empty state.
func (model *Model) Load(ctx context.Context, observer StateObserver) {
	if model == nil {
		return
	}
	model.operationErr = nil
	model.state = async.NewPending[Data]()
	notifyState(observer, model.state)
	if model.sessionID == "" {
		model.succeed(nil, observer)
		return
	}
	if model.reports == nil {
		model.fail(reportDependencyFailure(), observer)
		return
	}
	document, found, err := model.reports.Get(ctx, model.sessionID)
	if err != nil {
		model.fail(reportFailure("load report", err), observer)
		return
	}
	if found {
		if model.reportID != "" && document.ID != model.reportID {
			model.fail(domainerr.New(
				domainerr.CodeInvalidState,
				"load report",
				"所选报告与会话中的报告不一致。",
				"返回训练主页并重新打开报告。",
				false,
			), observer)
			return
		}
		model.succeed(&document, observer)
		return
	}
	if model.evaluator == nil {
		model.succeed(nil, observer)
		return
	}
	result, err := model.evaluator.Generate(
		ctx,
		model.sessionID,
		func(progressState async.State[evaluation.Progress]) {
			if progressState.Phase != async.Streaming || progressState.Value == nil {
				return
			}
			model.state = async.NewStreaming(&Data{
				Stage: progressState.Value.Message,
			})
			notifyState(observer, model.state)
		},
	)
	if err != nil {
		model.fail(reportFailure("generate report", err), observer)
		return
	}
	model.succeed(&result.Report, observer)
}

// Delete executes only after HandleKey returned a confirmed delete intent.
func (model *Model) Delete(ctx context.Context, observer StateObserver) {
	if model == nil {
		return
	}
	if !model.deleteAuthorized {
		model.operationErr = domainerr.New(
			domainerr.CodePolicyDenied,
			"delete report",
			"删除报告前必须再次确认。",
			"按 [d] 打开确认提示，再按 [y] 确认删除。",
			false,
		)
		return
	}
	model.deleteAuthorized = false
	document := model.document()
	if document == nil {
		model.operationErr = domainerr.New(
			domainerr.CodeInvalidState,
			"delete report",
			"当前没有可删除的报告。",
			"返回训练主页选择一份报告。",
			false,
		)
		return
	}
	if model.deleter == nil {
		model.operationErr = domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"delete report",
			"报告删除接口不可用。",
			"重新启动 InterviewCraft 后重试。",
			true,
		)
		return
	}
	copyDocument := *document
	model.state = async.NewStreaming(&Data{
		Report: &copyDocument,
		Stage:  "正在删除报告与派生训练计划",
	})
	notifyState(observer, model.state)
	deleted, err := model.deleter.DeleteReport(ctx, model.sessionID, document.ID)
	if err != nil {
		model.state = async.NewSucceeded(Data{Report: &copyDocument})
		model.operationErr = reportFailure("delete report", err)
		notifyState(observer, model.state)
		return
	}
	if !deleted {
		model.state = async.NewSucceeded(Data{Report: &copyDocument})
		model.operationErr = domainerr.New(
			domainerr.CodeInvalidState,
			"delete report",
			"报告已不存在，未删除任何数据。",
			"按 [t] 返回训练主页刷新记录。",
			false,
		)
		notifyState(observer, model.state)
		return
	}
	model.reportID = ""
	model.operationErr = nil
	model.resetSelections()
	model.succeed(nil, observer)
}

// Resize changes terminal geometry without clearing state, selection, or focus.
func (model *Model) Resize(width, height int) {
	if model == nil {
		return
	}
	model.Width = width
	model.Height = height
}

// HandleKey applies focus, evidence jumps, practice creation, and confirmation.
func (model *Model) HandleKey(key string) Action {
	if model == nil {
		return Action{}
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if model.helpOpen {
		if isEscape(key) || key == "?" {
			model.closeOverlay()
		}
		return Action{}
	}
	if model.evidenceOpen {
		if isEscape(key) {
			model.closeOverlay()
			return Action{}
		}
		if key == "enter" && !model.missingEvidence {
			return model.evidenceNavigation()
		}
		return Action{}
	}
	if model.deleteConfirmOpen {
		switch key {
		case "y":
			model.closeOverlay()
			model.deleteAuthorized = true
			return Action{
				Intent:    IntentDeleteReport,
				SessionID: model.sessionID,
				ReportID:  model.currentReportID(),
			}
		case "n", "escape", "esc", "enter":
			model.closeOverlay()
		}
		return Action{}
	}

	switch key {
	case "?":
		if model.focus.OpenOverlay(focusHelp) == nil {
			model.helpOpen = true
		}
	case "tab":
		model.focus.Handle(layout.KeyTab)
	case "shift+tab":
		model.focus.Handle(layout.KeyShiftTab)
	case "up", "k":
		model.moveSelection(-1)
	case "down", "j":
		model.moveSelection(1)
	case "e":
		model.openSelectedEvidence()
	case "n":
		return model.practiceNavigation()
	case "d":
		if model.document() != nil && model.focus.OpenOverlay(focusDelete) == nil {
			model.deleteAuthorized = false
			model.deleteConfirmOpen = true
		}
	case "t":
		return Action{Destination: DestinationTraining}
	case "q":
		return Action{Destination: DestinationQuit}
	case "enter":
		return model.activateFocused()
	}
	return Action{}
}

func (model *Model) activateFocused() Action {
	if model.document() == nil {
		return Action{Destination: DestinationTraining}
	}
	switch model.focus.Active() {
	case focusPractice:
		return model.practiceNavigation()
	default:
		model.openSelectedEvidence()
	}
	return Action{}
}

func (model *Model) practiceNavigation() Action {
	document := model.document()
	if document == nil || len(document.PracticePlan) == 0 {
		return Action{Destination: DestinationTraining}
	}
	index := clampSelection(model.selectedPractice, len(document.PracticePlan))
	item := document.PracticePlan[index]
	return Action{
		Intent:      IntentStartPractice,
		Destination: DestinationScenario,
		SessionID:   model.sessionID,
		ReportID:    document.ID,
		Practice: &PracticeSeed{
			ID:                 fmt.Sprintf("%s-practice-%d", document.ID, index+1),
			SessionID:          model.sessionID,
			ReportID:           document.ID,
			Topic:              item.Topic,
			Mode:               item.Mode,
			DurationMinutes:    item.DurationMinutes,
			CompletionCriteria: item.CompletionCriteria,
		},
	}
}

func (model *Model) openSelectedEvidence() {
	document := model.document()
	if document == nil {
		return
	}
	references := model.selectedReferences()
	model.missingEvidence = len(references) == 0
	if len(references) > 0 {
		index := evidenceIndex(document.Evidence, references[0])
		if index < 0 {
			model.missingEvidence = true
		} else {
			model.selectedEvidence = index
		}
	}
	if model.focus.OpenOverlay(focusEvidenceDetail) == nil {
		model.evidenceOpen = true
	}
}

func (model *Model) selectedReferences() []contracts.EvidenceID {
	document := model.document()
	if document == nil {
		return nil
	}
	switch model.focus.Active() {
	case focusScorecard:
		if len(document.Scorecard) > 0 {
			return slices.Clone(document.Scorecard[clampSelection(
				model.selectedScorecard, len(document.Scorecard),
			)].EvidenceIDs)
		}
	case focusReviews:
		if len(document.QuestionReview) > 0 {
			item := document.QuestionReview[clampSelection(
				model.selectedReview, len(document.QuestionReview),
			)]
			return uniqueEvidence(append(
				slices.Clone(item.Summary.EvidenceIDs),
				item.NextAction.EvidenceIDs...,
			))
		}
	case focusLearning:
		if len(document.LearningMap) > 0 {
			return slices.Clone(document.LearningMap[clampSelection(
				model.selectedLearning, len(document.LearningMap),
			)].EvidenceIDs)
		}
	case focusPractice:
		if len(document.PracticePlan) > 0 {
			return slices.Clone(document.PracticePlan[clampSelection(
				model.selectedPractice, len(document.PracticePlan),
			)].EvidenceIDs)
		}
	case focusEvidence:
		if len(document.Evidence) > 0 {
			return []contracts.EvidenceID{document.Evidence[clampSelection(
				model.selectedEvidence, len(document.Evidence),
			)].ID}
		}
	}
	return nil
}

func (model *Model) evidenceNavigation() Action {
	document := model.document()
	if document == nil || len(document.Evidence) == 0 {
		return Action{}
	}
	item := document.Evidence[clampSelection(
		model.selectedEvidence, len(document.Evidence),
	)]
	return Action{
		Intent:      IntentOpenEvidence,
		Destination: DestinationEvidence,
		SessionID:   model.sessionID,
		ReportID:    document.ID,
		EvidenceID:  item.ID,
		QuestionID:  item.QuestionID,
	}
}

func (model *Model) moveSelection(delta int) {
	document := model.document()
	if document == nil {
		return
	}
	switch model.focus.Active() {
	case focusScorecard:
		model.selectedScorecard = wrapSelection(
			model.selectedScorecard, delta, len(document.Scorecard),
		)
	case focusReviews:
		model.selectedReview = wrapSelection(
			model.selectedReview, delta, len(document.QuestionReview),
		)
	case focusLearning:
		model.selectedLearning = wrapSelection(
			model.selectedLearning, delta, len(document.LearningMap),
		)
	case focusPractice:
		model.selectedPractice = wrapSelection(
			model.selectedPractice, delta, len(document.PracticePlan),
		)
	case focusEvidence:
		model.selectedEvidence = wrapSelection(
			model.selectedEvidence, delta, len(document.Evidence),
		)
	}
}

func (model *Model) closeOverlay() {
	model.helpOpen = false
	model.deleteConfirmOpen = false
	model.evidenceOpen = false
	model.missingEvidence = false
	model.focus.CloseOverlay()
}

func (model *Model) document() *corereport.Document {
	if model == nil || model.state.Value == nil {
		return nil
	}
	if model.state.Phase != async.Succeeded && model.state.Phase != async.Streaming {
		return nil
	}
	return model.state.Value.Report
}

func (model *Model) currentReportID() string {
	if document := model.document(); document != nil {
		return document.ID
	}
	return model.reportID
}

func (model *Model) succeed(document *corereport.Document, observer StateObserver) {
	if document != nil {
		model.reportID = document.ID
		model.clampSelections(document)
	}
	model.state = async.NewSucceeded(Data{Report: document})
	notifyState(observer, model.state)
}

func (model *Model) fail(failure *domainerr.Error, observer StateObserver) {
	model.state = async.NewFailed[Data](failure)
	notifyState(observer, model.state)
}

func (model *Model) clampSelections(document *corereport.Document) {
	model.selectedScorecard = clampSelection(model.selectedScorecard, len(document.Scorecard))
	model.selectedReview = clampSelection(model.selectedReview, len(document.QuestionReview))
	model.selectedLearning = clampSelection(model.selectedLearning, len(document.LearningMap))
	model.selectedPractice = clampSelection(model.selectedPractice, len(document.PracticePlan))
	model.selectedEvidence = clampSelection(model.selectedEvidence, len(document.Evidence))
}

func (model *Model) resetSelections() {
	model.selectedScorecard = 0
	model.selectedReview = 0
	model.selectedLearning = 0
	model.selectedPractice = 0
	model.selectedEvidence = 0
}

func reportDependencyFailure() *domainerr.Error {
	return domainerr.New(
		domainerr.CodeDependencyUnavailable,
		"load report",
		"报告读取接口不可用。",
		"重新启动 InterviewCraft 后按 [t] 重试。",
		true,
	)
}

func reportFailure(operation string, err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"report storage",
		"无法读取或更新报告。",
		"会话证据仍然保留；检查本地数据库后按 [t] 重试。",
		true,
		err,
	)
}

func notifyState(observer StateObserver, state async.State[Data]) {
	if observer != nil {
		observer(state)
	}
}

func evidenceIndex(values []corereport.EvidenceLink, id contracts.EvidenceID) int {
	for index, value := range values {
		if value.ID == id {
			return index
		}
	}
	return -1
}

func uniqueEvidence(values []contracts.EvidenceID) []contracts.EvidenceID {
	result := make([]contracts.EvidenceID, 0, len(values))
	seen := make(map[contracts.EvidenceID]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func isEscape(key string) bool {
	return key == "escape" || key == "esc"
}

func clampSelection(selected, count int) int {
	if count <= 0 {
		return 0
	}
	if selected < 0 {
		return 0
	}
	if selected >= count {
		return count - 1
	}
	return selected
}

func wrapSelection(selected, delta, count int) int {
	if count <= 0 {
		return 0
	}
	return (selected + delta%count + count) % count
}
