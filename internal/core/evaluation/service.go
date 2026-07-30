package evaluation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

const transferWindow = 5 * time.Minute

var prohibitedEvaluationTerms = []string{
	"personality", "personality fit", "hire", "hiring decision",
	"人格", "性格", "录用", "招聘结论",
}

// Service orchestrates staged, evidence-first evaluation.
type Service struct {
	repository Repository
	provider   Provider
	reports    *report.Service
	now        func() time.Time
}

// NewService constructs an Evaluator and its durable report service.
func NewService(
	repository Repository,
	provider Provider,
	options Options,
) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		repository: repository,
		provider:   provider,
		reports: report.NewService(repository, report.Options{
			Now: now,
		}),
		now: now,
	}
}

// Generate creates, validates, persists, and completes one pending session.
func (service *Service) Generate(
	ctx context.Context,
	sessionID string,
	observer Observer,
) (Result, error) {
	notify(observer, async.NewPending[Progress]())
	if service == nil || service.repository == nil || service.reports == nil {
		return Result{}, fail(observer, evaluationError(
			domainerr.CodeDependencyUnavailable,
			"评估存储不可用。",
			"重新启动 InterviewCraft 后重试。",
			true,
		))
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Result{}, fail(observer, evaluationError(
			domainerr.CodeValidation,
			"会话 ID 不能为空。",
			"从已完成的训练会话打开报告。",
			false,
		))
	}
	session, found, err := service.repository.GetSession(ctx, sessionID)
	if err != nil {
		return Result{}, fail(observer, evaluationFailure(err))
	}
	if !found {
		return Result{}, fail(observer, noCompletedSession())
	}
	existing, reportFound, err := service.reports.Get(ctx, sessionID)
	if err != nil {
		return Result{}, fail(observer, evaluationFailure(err))
	}
	if reportFound {
		if session.Status == db.SessionActive {
			return Result{}, fail(observer, evaluationError(
				domainerr.CodeInvalidState,
				"活动会话不能使用旧报告。",
				"先完成或结束当前会话。",
				false,
			))
		}
		if session.Status == db.SessionEvaluationPending {
			if err := service.completeSession(ctx, sessionID); err != nil {
				return Result{}, fail(observer, err)
			}
		}
		notify(observer, async.NewSucceeded(Progress{
			Stage:   "ready",
			Message: "已恢复持久化报告",
		}))
		return Result{
			Report:     existing,
			Degraded:   existing.Degraded,
			Idempotent: true,
		}, nil
	}
	if session.Status != db.SessionEvaluationPending {
		return Result{}, fail(observer, noCompletedSession())
	}

	loaded, err := service.load(ctx, session)
	if err != nil {
		return Result{}, fail(observer, err)
	}
	progress(observer, "scoring_evidence", "正在校验并评分证据")
	degraded := false
	draft := emptyDraft()
	if service.provider == nil {
		return Result{}, fail(observer, evaluationError(
			domainerr.CodeDependencyUnavailable,
			"评估 Provider 不可用。",
			"检查模型设置后重试；会话证据已保留。",
			true,
		))
	}
	draft, err = service.provider.Evaluate(ctx, loaded.input)
	if err == nil {
		err = draft.Validate()
	}
	if err != nil {
		if domainerr.IsCode(err, domainerr.CodeInvalidModelOutput) ||
			domainerr.IsCode(err, domainerr.CodeValidation) {
			draft = emptyDraft()
			degraded = true
		} else {
			return Result{}, fail(observer, evaluationProviderFailure(ctx, err))
		}
	}

	progress(observer, "grouping_learning_gaps", "正在聚合 Coach 学习缺口")
	document, localDegraded := buildDocument(
		session,
		loaded,
		draft,
		service.now().UTC(),
	)
	document.Degraded = degraded || localDegraded
	progress(observer, "planning_next_run", "正在生成下一轮训练计划")
	if err := document.Validate(); err != nil {
		return Result{}, fail(observer, evaluationFailure(err))
	}
	progress(observer, "saving_report", "正在保存证据化报告")
	if err := service.reports.Save(ctx, document); err != nil {
		return Result{}, fail(observer, evaluationFailure(err))
	}
	if err := service.completeSession(ctx, sessionID); err != nil {
		return Result{}, fail(observer, err)
	}
	notify(observer, async.NewSucceeded(Progress{
		Stage:   "ready",
		Message: "证据化报告已保存",
	}))
	return Result{
		Report:   document,
		Degraded: document.Degraded,
	}, nil
}

type loadedEvaluation struct {
	input       Input
	scenario    contracts.Scenario
	events      []db.SessionEvent
	sidebar     []db.SidebarEvent
	code        []db.CodeSubmission
	evidence    []report.EvidenceLink
	evidenceSet map[contracts.EvidenceID]report.EvidenceLink
}

func (service *Service) load(
	ctx context.Context,
	session db.Session,
) (loadedEvaluation, error) {
	scenario, found, err := service.repository.GetScenario(
		ctx,
		session.ScenarioID,
	)
	if err != nil {
		return loadedEvaluation{}, evaluationFailure(err)
	}
	if !found {
		return loadedEvaluation{}, evaluationError(
			domainerr.CodeInvalidState,
			"已完成会话关联的场景不存在。",
			"保留会话数据并检查本地数据库。",
			false,
		)
	}
	profile, found, err := service.repository.GetSessionProfile(ctx, session.ID)
	if err != nil {
		return loadedEvaluation{}, evaluationFailure(err)
	}
	if !found || profile.ConfirmedAt == nil || profile.ConfirmedAt.IsZero() {
		return loadedEvaluation{}, evaluationError(
			domainerr.CodeInvalidState,
			"评估只能使用已确认画像。",
			"确认画像后重新完成一场训练。",
			false,
		)
	}
	events, err := service.repository.ListSessionEvents(ctx, session.ID)
	if err != nil {
		return loadedEvaluation{}, evaluationFailure(err)
	}
	sidebar, err := service.repository.ListSidebarEvents(ctx, session.ID)
	if err != nil {
		return loadedEvaluation{}, evaluationFailure(err)
	}
	codeRuns, err := service.repository.ListCodeSubmissions(ctx, session.ID)
	if err != nil {
		return loadedEvaluation{}, evaluationFailure(err)
	}
	hasSubmittedEvidence := len(codeRuns) > 0
	for _, event := range events {
		if event.Speaker == db.SpeakerUser &&
			strings.TrimSpace(event.Content) != "" {
			hasSubmittedEvidence = true
			break
		}
	}
	if !hasSubmittedEvidence {
		return loadedEvaluation{}, evaluationError(
			domainerr.CodeInvalidState,
			"已完成会话没有可评估的回答或代码证据。",
			"开始新的训练并至少提交一次回答。",
			false,
		)
	}
	loaded, err := makeLoaded(session, scenario, profile.Candidate.Facts,
		profile.Candidate.Skills, events, sidebar, codeRuns)
	if err != nil {
		return loadedEvaluation{}, err
	}
	return loaded, nil
}

func makeLoaded(
	session db.Session,
	scenario contracts.Scenario,
	facts []contracts.ProfileFact,
	skills []string,
	events []db.SessionEvent,
	sidebar []db.SidebarEvent,
	codeRuns []db.CodeSubmission,
) (loadedEvaluation, error) {
	links := make([]report.EvidenceLink, 0,
		len(scenario.Questions)+len(facts)+len(events)+
			len(sidebar)+len(codeRuns)*2)
	index := make(map[contracts.EvidenceID]report.EvidenceLink)
	add := func(link report.EvidenceLink) error {
		if existing, duplicate := index[link.ID]; duplicate {
			return evaluationError(
				domainerr.CodeInvalidState,
				fmt.Sprintf(
					"证据 ID %q 同时属于 %s 和 %s。",
					link.ID,
					existing.Kind,
					link.Kind,
				),
				"检查会话证据 ID 后重新生成报告。",
				false,
			)
		}
		index[link.ID] = link
		links = append(links, link)
		return nil
	}
	for _, question := range scenario.Questions {
		if err := add(report.EvidenceLink{
			ID:         contracts.EvidenceID("constraint:" + question.ID),
			Kind:       "question_constraint",
			QuestionID: question.ID,
			Label:      "题目约束 " + question.ID,
		}); err != nil {
			return loadedEvaluation{}, err
		}
	}
	for _, fact := range facts {
		if err := add(report.EvidenceLink{
			ID:    fact.ID,
			Kind:  "profile_fact",
			Label: "已确认画像事实 " + fact.Field,
		}); err != nil {
			return loadedEvaluation{}, err
		}
	}
	inputEvents := make([]EventEvidence, 0, len(events))
	for _, event := range events {
		id := contracts.EvidenceID(event.EventID)
		if err := add(report.EvidenceLink{
			ID:         id,
			Kind:       "session_" + string(event.Speaker),
			QuestionID: event.QuestionID,
			Label:      string(event.Speaker) + " " + event.QuestionID,
			OccurredAt: event.OccurredAt,
		}); err != nil {
			return loadedEvaluation{}, err
		}
		inputEvents = append(inputEvents, EventEvidence{
			ID:         id,
			Speaker:    event.Speaker,
			QuestionID: event.QuestionID,
			Content:    event.Content,
			OccurredAt: event.OccurredAt,
		})
	}
	inputCoach := make([]CoachEvidence, 0, len(sidebar))
	for _, event := range sidebar {
		id := contracts.EvidenceID(event.ID)
		if err := add(report.EvidenceLink{
			ID:         id,
			Kind:       "sidebar_event",
			QuestionID: event.QuestionID,
			Label:      "Coach " + event.QuestionID + " " + string(event.HelpLevel),
			OccurredAt: event.OccurredAt,
		}); err != nil {
			return loadedEvaluation{}, err
		}
		inputCoach = append(inputCoach, CoachEvidence{
			ID:          id,
			QuestionID:  event.QuestionID,
			Intent:      event.Intent,
			HelpLevel:   event.HelpLevel,
			Tags:        slices.Clone(event.Tags),
			Content:     event.Content,
			Outcome:     event.Outcome,
			PausedTimer: event.PausedTimer,
			OccurredAt:  event.OccurredAt,
		})
	}
	inputCode := make([]CodeEvidence, 0, len(codeRuns))
	for _, run := range codeRuns {
		submissionID := contracts.EvidenceID(run.ID)
		snapshotID := contracts.EvidenceID(run.SnapshotID)
		if err := add(report.EvidenceLink{
			ID:         submissionID,
			Kind:       "code_submission",
			QuestionID: run.QuestionID,
			Label:      "代码运行 " + run.QuestionID,
			OccurredAt: run.CreatedAt,
		}); err != nil {
			return loadedEvaluation{}, err
		}
		if err := add(report.EvidenceLink{
			ID:         snapshotID,
			Kind:       "code_snapshot",
			QuestionID: run.QuestionID,
			Label:      "代码快照 " + run.QuestionID,
			OccurredAt: run.CreatedAt,
		}); err != nil {
			return loadedEvaluation{}, err
		}
		inputCode = append(inputCode, CodeEvidence{
			SubmissionID: submissionID,
			SnapshotID:   snapshotID,
			QuestionID:   run.QuestionID,
			Language:     run.Language,
			Source:       run.Source,
			TestResult:   slices.Clone(run.TestResult),
			RuntimeStats: slices.Clone(run.RuntimeStats),
			OccurredAt:   run.CreatedAt,
		})
	}
	allowed := make([]contracts.EvidenceID, 0, len(links))
	for _, link := range links {
		allowed = append(allowed, link.ID)
	}
	return loadedEvaluation{
		input: Input{
			SessionID:          session.ID,
			Template:           scenario.Template,
			Mode:               scenario.Mode,
			StartedAt:          session.StartedAt,
			CompletedAt:        session.UpdatedAt,
			Questions:          slices.Clone(scenario.Questions),
			ConfirmedFacts:     slices.Clone(facts),
			ConfirmedSkills:    slices.Clone(skills),
			Events:             inputEvents,
			CoachEvents:        inputCoach,
			CodeRuns:           inputCode,
			AllowedEvidenceIDs: allowed,
		},
		scenario:    scenario,
		events:      slices.Clone(events),
		sidebar:     slices.Clone(sidebar),
		code:        slices.Clone(codeRuns),
		evidence:    links,
		evidenceSet: index,
	}, nil
}

func buildDocument(
	session db.Session,
	loaded loadedEvaluation,
	draft Draft,
	now time.Time,
) (report.Document, bool) {
	degraded := false
	reviews := make(map[string]DraftQuestionReview, len(draft.QuestionReviews))
	for _, value := range draft.QuestionReviews {
		reviews[value.QuestionID] = value
	}
	questionReviews := make([]report.QuestionReview, 0, len(loaded.scenario.Questions))
	for _, question := range loaded.scenario.Questions {
		value, found := reviews[question.ID]
		fallback := questionEvidence(loaded.events, loaded.code, question.ID)
		summary, summaryDegraded := resolvedInsight(
			value.Summary,
			found,
			"已记录本题作答；具体表现需结合题目量表复核。",
			fallback,
			loaded.evidenceSet,
		)
		nextAction, actionDegraded := resolvedInsight(
			value.NextAction,
			found,
			"按本题量表重答一次，并明确说明关键依据与取舍。",
			fallback,
			loaded.evidenceSet,
		)
		degraded = degraded || summaryDegraded || actionDegraded
		questionReviews = append(questionReviews, report.QuestionReview{
			QuestionID: question.ID,
			Prompt:     question.Prompt,
			Summary:    summary,
			NextAction: nextAction,
		})
	}
	scorecard, scoreDegraded := buildScorecard(
		draft.Findings,
		len(loaded.code) > 0,
		loaded.evidenceSet,
	)
	degraded = degraded || scoreDegraded
	learningMap := buildLearningMap(
		loaded.sidebar,
		loaded.input.ConfirmedSkills,
	)
	transfer := buildTransfer(
		loaded.sidebar,
		loaded.events,
		loaded.code,
	)
	crossInsights, crossDegraded := buildCrossInsights(
		draft.CrossInsights,
		loaded,
	)
	degraded = degraded || crossDegraded
	practice, practiceDegraded := buildPracticePlan(
		draft.PracticePlan,
		loaded,
		scorecard,
		learningMap,
	)
	degraded = degraded || practiceDegraded
	duration := int(session.UpdatedAt.Sub(session.StartedAt).Seconds())
	if duration < 0 {
		duration = 0
		degraded = true
	}
	document := report.Document{
		ID:            "report-" + session.ID,
		SchemaVersion: report.SchemaVersion,
		GeneratedAt:   now,
		Degraded:      degraded,
		Summary: report.SessionSummary{
			SessionID:        session.ID,
			ScenarioID:       session.ScenarioID,
			Template:         loaded.scenario.Template,
			Mode:             loaded.scenario.Mode,
			StartedAt:        session.StartedAt,
			CompletedAt:      session.UpdatedAt,
			DurationSeconds:  duration,
			QuestionCount:    len(loaded.scenario.Questions),
			CoachPromptCount: len(loaded.sidebar),
			CodeRunCount:     len(loaded.code),
		},
		Evidence:       slices.Clone(loaded.evidence),
		QuestionReview: questionReviews,
		Scorecard:      scorecard,
		LearningMap:    learningMap,
		Transfer:       transfer,
		CrossInsights:  crossInsights,
		PracticePlan:   practice,
	}
	return document, degraded
}

func resolvedInsight(
	value DraftInsight,
	provided bool,
	fallbackText string,
	fallbackEvidence []contracts.EvidenceID,
	evidence map[contracts.EvidenceID]report.EvidenceLink,
) (report.Insight, bool) {
	if provided && safeText(value.Text) &&
		validEvidence(value.EvidenceIDs, evidence) {
		return report.Insight{
			Text:        strings.TrimSpace(value.Text),
			Status:      report.StatusEvidenceBacked,
			EvidenceIDs: slices.Clone(value.EvidenceIDs),
			Confidence:  value.Confidence,
		}, false
	}
	if len(fallbackEvidence) > 0 {
		return report.Insight{
			Text:        fallbackText,
			Status:      report.StatusEvidenceBacked,
			EvidenceIDs: slices.Clone(fallbackEvidence),
			Confidence:  0,
		}, true
	}
	return insufficientInsight(), true
}

func buildScorecard(
	findings []contracts.EvaluationFinding,
	hasCode bool,
	evidence map[contracts.EvidenceID]report.EvidenceLink,
) ([]report.ScorecardItem, bool) {
	byDimension := make(map[contracts.EvaluationDimension]contracts.EvaluationFinding)
	for _, finding := range findings {
		byDimension[finding.Dimension] = finding
	}
	result := make([]report.ScorecardItem, 0, 8)
	degraded := false
	for _, dimension := range report.FixedDimensions() {
		if dimension == contracts.DimensionCodeQuality && !hasCode {
			result = append(result, report.ScorecardItem{
				Dimension:   dimension,
				Status:      report.StatusNotApplicable,
				EvidenceIDs: []contracts.EvidenceID{},
				NextAction:  "本场没有已运行代码，代码质量不适用。",
			})
			continue
		}
		finding, found := byDimension[dimension]
		if found && finding.Score != nil &&
			safeText(finding.NextAction) &&
			validEvidence(finding.EvidenceIDs, evidence) {
			score := *finding.Score
			result = append(result, report.ScorecardItem{
				Dimension:   dimension,
				Status:      report.StatusEvidenceBacked,
				Score:       &score,
				EvidenceIDs: slices.Clone(finding.EvidenceIDs),
				Confidence:  finding.Confidence,
				NextAction:  strings.TrimSpace(finding.NextAction),
			})
			continue
		}
		degraded = true
		result = append(result, report.ScorecardItem{
			Dimension:   dimension,
			Status:      report.StatusInsufficient,
			EvidenceIDs: []contracts.EvidenceID{},
			NextAction:  "不足以判断；需要更多已提交回答或已运行代码证据。",
		})
	}
	return result, degraded
}

func buildLearningMap(
	events []db.SidebarEvent,
	skills []string,
) []report.LearningGap {
	result := make([]report.LearningGap, 0)
	index := make(map[string]int)
	for _, event := range events {
		topic := "未分类主题"
		for _, tag := range event.Tags {
			if strings.TrimSpace(tag) != "" {
				topic = strings.TrimSpace(tag)
				break
			}
		}
		key := strings.ToLower(topic)
		position, found := index[key]
		if !found {
			position = len(result)
			index[key] = position
			result = append(result, report.LearningGap{
				Topic:          topic,
				MaxHelpLevel:   event.HelpLevel,
				QuestionIDs:    []string{},
				EvidenceIDs:    []contracts.EvidenceID{},
				RelatedSkills:  relatedSkills(topic, event.Tags, skills),
				RelatedJDNeeds: []string{},
			})
		}
		gap := &result[position]
		gap.AskCount++
		gap.EvidenceIDs = append(
			gap.EvidenceIDs,
			contracts.EvidenceID(event.ID),
		)
		if !slices.Contains(gap.QuestionIDs, event.QuestionID) {
			gap.QuestionIDs = append(gap.QuestionIDs, event.QuestionID)
		}
		if helpRank(event.HelpLevel) > helpRank(gap.MaxHelpLevel) {
			gap.MaxHelpLevel = event.HelpLevel
		}
		switch event.Outcome {
		case "understood":
			gap.UnderstoodCount++
		case "still_confused":
			gap.ConfusedCount++
		case "review":
			gap.ReviewCount++
		default:
			gap.UnmarkedCount++
		}
	}
	return result
}

func buildTransfer(
	sidebar []db.SidebarEvent,
	events []db.SessionEvent,
	code []db.CodeSubmission,
) []report.TransferEvidence {
	type candidate struct {
		id   contracts.EvidenceID
		when time.Time
	}
	result := make([]report.TransferEvidence, 0, len(sidebar))
	for _, coachEvent := range sidebar {
		candidates := make([]candidate, 0)
		deadline := coachEvent.OccurredAt.Add(transferWindow)
		for _, event := range events {
			if event.Speaker != db.SpeakerUser ||
				event.QuestionID != coachEvent.QuestionID ||
				!event.OccurredAt.After(coachEvent.OccurredAt) ||
				event.OccurredAt.After(deadline) {
				continue
			}
			candidates = append(candidates, candidate{
				id:   contracts.EvidenceID(event.EventID),
				when: event.OccurredAt,
			})
		}
		for _, run := range code {
			if run.QuestionID != coachEvent.QuestionID ||
				!run.CreatedAt.After(coachEvent.OccurredAt) ||
				run.CreatedAt.After(deadline) {
				continue
			}
			candidates = append(candidates, candidate{
				id:   contracts.EvidenceID(run.ID),
				when: run.CreatedAt,
			})
		}
		sort.SliceStable(candidates, func(left, right int) bool {
			if candidates[left].when.Equal(candidates[right].when) {
				return candidates[left].id < candidates[right].id
			}
			return candidates[left].when.Before(candidates[right].when)
		})
		item := report.TransferEvidence{
			SidebarEventID:     contracts.EvidenceID(coachEvent.ID),
			QuestionID:         coachEvent.QuestionID,
			Status:             report.TransferInsufficient,
			SubsequentEvidence: []contracts.EvidenceID{},
			Summary:            "不足以判断理解迁移；提示后 5 分钟内没有同题后续作答或代码事件。",
		}
		if len(candidates) > 0 {
			item.Status = report.TransferEvidenceObserved
			item.Summary = "提示后 5 分钟内记录了同题后续作答或代码；该事件不代表迁移成功或失败。"
			for _, value := range candidates {
				item.SubsequentEvidence = append(
					item.SubsequentEvidence,
					value.id,
				)
			}
		}
		result = append(result, item)
	}
	return result
}

func buildCrossInsights(
	values []DraftInsight,
	loaded loadedEvaluation,
) ([]report.Insight, bool) {
	result := make([]report.Insight, 0, len(values))
	degraded := false
	for _, value := range values {
		if !safeText(value.Text) ||
			!validEvidence(value.EvidenceIDs, loaded.evidenceSet) {
			degraded = true
			continue
		}
		result = append(result, report.Insight{
			Text:        strings.TrimSpace(value.Text),
			Status:      report.StatusEvidenceBacked,
			EvidenceIDs: slices.Clone(value.EvidenceIDs),
			Confidence:  value.Confidence,
		})
	}
	if len(result) > 0 {
		return result, degraded
	}
	crossEvidence := crossSourceEvidence(loaded)
	if len(crossEvidence) >= 2 {
		return []report.Insight{{
			Text:        "已记录多个来源的训练证据；具体能力变化需按证据逐项复核。",
			Status:      report.StatusEvidenceBacked,
			EvidenceIDs: crossEvidence,
		}}, true
	}
	return []report.Insight{insufficientInsight()}, true
}

func buildPracticePlan(
	values []DraftPracticeItem,
	loaded loadedEvaluation,
	scorecard []report.ScorecardItem,
	learningMap []report.LearningGap,
) ([]report.PracticeItem, bool) {
	result := make([]report.PracticeItem, 0, max(3, len(values)))
	seen := make(map[string]struct{})
	degraded := false
	add := func(item report.PracticeItem) {
		key := strings.ToLower(strings.TrimSpace(item.Topic))
		if key == "" {
			return
		}
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	for _, value := range values {
		if !safeText(value.Topic+" "+value.CompletionCriteria) ||
			value.DurationMinutes <= 0 ||
			!validDraftMode(value.Mode) ||
			!validEvidence(value.EvidenceIDs, loaded.evidenceSet) {
			degraded = true
			continue
		}
		add(report.PracticeItem{
			Topic:              strings.TrimSpace(value.Topic),
			Mode:               value.Mode,
			DurationMinutes:    value.DurationMinutes,
			CompletionCriteria: strings.TrimSpace(value.CompletionCriteria),
			Status:             report.StatusEvidenceBacked,
			EvidenceIDs:        slices.Clone(value.EvidenceIDs),
		})
	}
	for _, gap := range learningMap {
		add(report.PracticeItem{
			Topic:              gap.Topic,
			Mode:               contracts.ScenarioStrict,
			DurationMinutes:    15,
			CompletionCriteria: "在不新增 Coach 提示的情况下完成一题，并明确说明关键依据。",
			Status:             report.StatusEvidenceBacked,
			EvidenceIDs:        slices.Clone(gap.EvidenceIDs),
		})
	}
	scored := slices.Clone(scorecard)
	sort.SliceStable(scored, func(left, right int) bool {
		if scored[left].Score == nil {
			return false
		}
		if scored[right].Score == nil {
			return true
		}
		return *scored[left].Score < *scored[right].Score
	})
	for _, item := range scored {
		if item.Status != report.StatusEvidenceBacked {
			continue
		}
		add(report.PracticeItem{
			Topic:              dimensionLabel(item.Dimension),
			Mode:               contracts.ScenarioStrict,
			DurationMinutes:    15,
			CompletionCriteria: completionCriteria(item.Dimension),
			Status:             report.StatusEvidenceBacked,
			EvidenceIDs:        slices.Clone(item.EvidenceIDs),
		})
	}
	fallbackEvidence := firstSubmittedEvidence(loaded)
	fallbacks := []struct {
		topic    string
		criteria string
	}{
		{"回答结构", "用“结论—依据—结果”结构在 3 分钟内完成一次回答。"},
		{"技术深度", "说明一个实现取舍、一个失败模式和对应验证方法。"},
		{"问题澄清", "作答前提出至少两个可改变方案的约束问题。"},
		{"时间管理", "在设定时长内完成回答，并保留 30 秒总结。"},
	}
	for _, fallback := range fallbacks {
		if len(result) >= 3 {
			break
		}
		add(report.PracticeItem{
			Topic:              fallback.topic,
			Mode:               contracts.ScenarioStrict,
			DurationMinutes:    15,
			CompletionCriteria: fallback.criteria,
			Status:             report.StatusEvidenceBacked,
			EvidenceIDs:        slices.Clone(fallbackEvidence),
		})
		degraded = true
	}
	return result, degraded
}

func questionEvidence(
	events []db.SessionEvent,
	code []db.CodeSubmission,
	questionID string,
) []contracts.EvidenceID {
	result := make([]contracts.EvidenceID, 0)
	for _, event := range events {
		if event.QuestionID == questionID &&
			event.Speaker == db.SpeakerUser {
			result = append(result, contracts.EvidenceID(event.EventID))
		}
	}
	for _, run := range code {
		if run.QuestionID == questionID {
			result = append(result, contracts.EvidenceID(run.ID))
		}
	}
	return result
}

func crossSourceEvidence(loaded loadedEvaluation) []contracts.EvidenceID {
	result := make([]contracts.EvidenceID, 0, 3)
	if len(loaded.input.ConfirmedFacts) > 0 {
		result = append(result, loaded.input.ConfirmedFacts[0].ID)
	}
	if id := firstUserEvent(loaded.events); id != "" {
		result = append(result, id)
	} else if len(loaded.code) > 0 {
		result = append(result, contracts.EvidenceID(loaded.code[0].ID))
	}
	if len(loaded.sidebar) > 0 {
		result = append(
			result,
			contracts.EvidenceID(loaded.sidebar[0].ID),
		)
	}
	return result
}

func firstSubmittedEvidence(
	loaded loadedEvaluation,
) []contracts.EvidenceID {
	if id := firstUserEvent(loaded.events); id != "" {
		return []contracts.EvidenceID{id}
	}
	if len(loaded.code) > 0 {
		return []contracts.EvidenceID{
			contracts.EvidenceID(loaded.code[0].ID),
		}
	}
	return []contracts.EvidenceID{}
}

func firstUserEvent(events []db.SessionEvent) contracts.EvidenceID {
	for _, event := range events {
		if event.Speaker == db.SpeakerUser {
			return contracts.EvidenceID(event.EventID)
		}
	}
	return ""
}

func validEvidence(
	ids []contracts.EvidenceID,
	evidence map[contracts.EvidenceID]report.EvidenceLink,
) bool {
	if len(ids) == 0 {
		return false
	}
	seen := make(map[contracts.EvidenceID]struct{}, len(ids))
	for _, id := range ids {
		if _, found := evidence[id]; !found {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func safeText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	for _, term := range prohibitedEvaluationTerms {
		if strings.Contains(lower, strings.ToLower(term)) {
			return false
		}
	}
	return true
}

func relatedSkills(topic string, tags, skills []string) []string {
	result := make([]string, 0)
	needles := append([]string{topic}, tags...)
	for _, skill := range skills {
		lowerSkill := strings.ToLower(strings.TrimSpace(skill))
		for _, needle := range needles {
			lowerNeedle := strings.ToLower(strings.TrimSpace(needle))
			if lowerSkill != "" && lowerNeedle != "" &&
				(strings.Contains(lowerSkill, lowerNeedle) ||
					strings.Contains(lowerNeedle, lowerSkill)) {
				result = append(result, skill)
				break
			}
		}
	}
	return result
}

func helpRank(level contracts.HelpLevel) int {
	switch level {
	case contracts.HelpL1:
		return 1
	case contracts.HelpL2:
		return 2
	case contracts.HelpL3:
		return 3
	case contracts.HelpL4:
		return 4
	default:
		return 0
	}
}

func dimensionLabel(dimension contracts.EvaluationDimension) string {
	switch dimension {
	case contracts.DimensionAnswerStructure:
		return "回答结构"
	case contracts.DimensionExperienceCredibility:
		return "经历可信度"
	case contracts.DimensionTechnicalDepth:
		return "技术深度"
	case contracts.DimensionProblemClarification:
		return "问题澄清"
	case contracts.DimensionProblemSolving:
		return "解题过程"
	case contracts.DimensionCodeQuality:
		return "代码质量"
	case contracts.DimensionTimeManagement:
		return "时间管理"
	case contracts.DimensionIndependence:
		return "独立完成度"
	default:
		return string(dimension)
	}
}

func completionCriteria(dimension contracts.EvaluationDimension) string {
	switch dimension {
	case contracts.DimensionAnswerStructure:
		return "连续两次使用清晰结构完成回答，并给出可验证结果。"
	case contracts.DimensionExperienceCredibility:
		return "每项经历陈述都能回指已确认事实，并区分个人贡献。"
	case contracts.DimensionTechnicalDepth:
		return "说明至少一个取舍、失败模式和验证方法。"
	case contracts.DimensionProblemClarification:
		return "作答前提出至少两个会改变方案的约束问题。"
	case contracts.DimensionProblemSolving:
		return "完整说明假设、步骤、复杂度与验证过程。"
	case contracts.DimensionCodeQuality:
		return "代码通过全部公开测试，并解释一个边界条件。"
	case contracts.DimensionTimeManagement:
		return "在时限内完成主体回答并保留 30 秒总结。"
	case contracts.DimensionIndependence:
		return "在不新增 Coach 提示的情况下完成整题。"
	default:
		return "完成一次限时练习，并依据事件记录复核结果。"
	}
}

func insufficientInsight() report.Insight {
	return report.Insight{
		Text:        "不足以判断；当前没有可解析的已提交证据。",
		Status:      report.StatusInsufficient,
		EvidenceIDs: []contracts.EvidenceID{},
	}
}

func emptyDraft() Draft {
	return Draft{
		QuestionReviews: []DraftQuestionReview{},
		Findings:        []contracts.EvaluationFinding{},
		CrossInsights:   []DraftInsight{},
		PracticePlan:    []DraftPracticeItem{},
	}
}

func (service *Service) completeSession(
	ctx context.Context,
	sessionID string,
) error {
	updated, err := service.repository.UpdateSessionStatus(
		ctx,
		sessionID,
		db.SessionCompleted,
		service.now().UTC(),
	)
	if err != nil {
		return evaluationFailure(err)
	}
	if !updated {
		return evaluationError(
			domainerr.CodeInvalidState,
			"报告已保存，但无法更新会话状态。",
			"重新打开该会话以恢复持久化报告。",
			true,
		)
	}
	return nil
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func progress(observer Observer, stage, message string) {
	value := Progress{Stage: stage, Message: message}
	notify(observer, async.NewStreaming(&value))
}

func fail(observer Observer, err error) error {
	typed := evaluationFailure(err)
	notify(observer, async.NewFailed[Progress](typed))
	return typed
}

func noCompletedSession() *domainerr.Error {
	return evaluationError(
		domainerr.CodeInvalidState,
		"没有可生成报告的已完成会话。",
		"开始并完成一场训练后再生成报告。",
		false,
	)
}

func evaluationProviderFailure(
	ctx context.Context,
	err error,
) *domainerr.Error {
	if ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return domainerr.Wrap(
			domainerr.CodeOperationCancelled,
			"generate evaluation",
			"Evaluator Provider",
			"已停止生成报告。",
			"会话证据已保留，可稍后重试。",
			true,
			err,
		)
	}
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"generate evaluation",
		"Evaluator Provider",
		"评估 Provider 暂时不可用。",
		"会话证据已保留；检查模型设置后重试。",
		true,
		err,
	)
}

func evaluationError(
	code domainerr.Code,
	message string,
	recovery string,
	retryable bool,
) *domainerr.Error {
	return domainerr.New(
		code,
		"generate evaluation report",
		message,
		recovery,
		retryable,
	)
}

func evaluationFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"generate evaluation report",
		"evaluation storage",
		"无法读取或保存评估证据。",
		"会话数据已保留；检查本地数据库后重试。",
		true,
		err,
	)
}
