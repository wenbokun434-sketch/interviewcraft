package scenario

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
)

var planIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Service coordinates generation, evidence policy, local edits, locking, and
// persistence of immutable Scenario versions.
type Service struct {
	repository Repository
	generator  Generator
	templates  []Template
	now        func() time.Time
}

// NewService loads the embedded template catalog.
func NewService(
	repository Repository,
	generator Generator,
	now func() time.Time,
) (*Service, error) {
	templates, err := LoadTemplates()
	if err != nil {
		return nil, domainerr.Wrap(
			domainerr.CodeDependencyUnavailable,
			"load Scenario templates",
			"embedded scenario catalog",
			"无法加载场景模板。",
			"重新安装或更新 InterviewCraft 后重试。",
			false,
			err,
		)
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		repository: repository,
		generator:  generator,
		templates:  templates,
		now:        now,
	}, nil
}

// Templates returns a defensive copy in stable product order.
func (service *Service) Templates() []Template {
	if service == nil {
		return nil
	}
	result := make([]Template, len(service.templates))
	for index, template := range service.templates {
		result[index] = cloneTemplate(template)
	}
	return result
}

// Generate builds an editable plan from confirmed facts only. A failed refresh
// returns the prior plan unchanged.
func (service *Service) Generate(
	ctx context.Context,
	request Request,
	previous *Plan,
	observer Observer,
) (Plan, error) {
	notify(observer, async.NewPending[Progress]())
	fallback := Plan{}
	if previous != nil {
		fallback = clonePlan(*previous)
	}
	if service == nil || service.generator == nil {
		failure := domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"generate Scenario plan",
			"Scenario Planner Provider 不可用。",
			"配置并测试模型 Provider 后重试。",
			true,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return fallback, failure
	}
	template, err := service.validateRequest(request, previous)
	if err != nil {
		failure := plannerFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return fallback, failure
	}
	if err := contextFailure(ctx, "场景生成已取消。"); err != nil {
		notify(observer, async.NewFailed[Progress](err))
		return fallback, err
	}

	mode := request.Mode
	timeBudget := request.TimeBudgetSeconds
	if previous != nil && previous.ManualRules {
		mode = previous.Scenario.Mode
		timeBudget = previous.Scenario.TimeBudgetSeconds
	}
	preparing := Progress{Stage: "正在准备已确认履历与场景模板"}
	notify(observer, async.NewStreaming(&preparing))
	input := GenerationInput{
		Template:      cloneTemplate(template),
		Mode:          mode,
		TimeBudget:    timeBudget,
		TargetRole:    request.Profile.Candidate.TargetRole,
		Facts:         slices.Clone(request.Profile.Candidate.Facts),
		Projects:      slices.Clone(request.Profile.Candidate.Projects),
		Skills:        slices.Clone(request.Profile.Candidate.Skills),
		JD:            strings.TrimSpace(request.JD),
		PromptVersion: PromptVersionBase,
	}
	generated, err := service.generator.Generate(ctx, input)
	if err != nil {
		failure := plannerFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return fallback, failure
	}
	if err := contextFailure(ctx, "场景生成已取消。"); err != nil {
		notify(observer, async.NewFailed[Progress](err))
		return fallback, err
	}
	if err := generated.Validate(); err != nil {
		failure := plannerFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return fallback, failure
	}
	if generated.Scenario.Mode != mode ||
		generated.Scenario.TimeBudgetSeconds != timeBudget {
		failure := validationError(
			"validate Scenario rules",
			"模型改变了用户选择的场景模式或时长。",
			"保留当前设置并重试生成。",
		)
		notify(observer, async.NewFailed[Progress](failure))
		return fallback, failure
	}

	revision := 1
	manualIDs := []string{}
	manualRules := false
	if previous != nil {
		revision = previous.Revision + 1
		manualIDs = slices.Clone(previous.ManualQuestionIDs)
		manualRules = previous.ManualRules
		generated.Scenario.Questions = mergeManualQuestions(
			previous.Scenario.Questions,
			manualIDs,
			generated.Scenario.Questions,
		)
		if manualRules {
			generated.Scenario.Mode = previous.Scenario.Mode
			generated.Scenario.TimeBudgetSeconds =
				previous.Scenario.TimeBudgetSeconds
		}
	}
	generated.Scenario.PromptVersion = versionString(revision)
	evidenceIDs := factIDs(request.Profile.Candidate.Facts)
	plan := Plan{
		ID:                versionID(request.PlanID, revision),
		BaseID:            request.PlanID,
		ProfileID:         request.Profile.ID,
		Scenario:          cloneScenario(generated.Scenario),
		JDMappings:        cloneMappings(generated.JDMappings),
		EvidenceIDs:       evidenceIDs,
		Revision:          revision,
		ManualQuestionIDs: manualIDs,
		ManualRules:       manualRules,
		JDProvided:        strings.TrimSpace(request.JD) != "",
	}
	validating := Progress{Stage: "正在校验题目证据与 JD 映射"}
	notify(observer, async.NewStreaming(&validating))
	if err := validatePlan(plan, template); err != nil {
		failure := plannerFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return fallback, failure
	}
	notify(observer, async.NewSucceeded(Progress{
		Stage: fmt.Sprintf("场景计划 v%d 已生成", revision),
	}))
	return plan, nil
}

// EditQuestion replaces one question and marks it as manual so regeneration
// cannot overwrite it.
func (service *Service) EditQuestion(
	plan Plan,
	replacement contracts.ScenarioQuestion,
) (Plan, error) {
	if plan.Locked {
		return clonePlan(plan), policyError(
			"已确认场景不能再编辑题目。",
			"创建新场景版本后再修改。",
		)
	}
	index := slices.IndexFunc(
		plan.Scenario.Questions,
		func(question contracts.ScenarioQuestion) bool {
			return question.ID == replacement.ID
		},
	)
	if index < 0 {
		return clonePlan(plan), validationError(
			"edit Scenario question",
			"找不到要编辑的场景题目。",
			"重新选择题目后重试。",
		)
	}
	if err := validateQuestion(replacement, plan.EvidenceIDs); err != nil {
		return clonePlan(plan), err
	}
	result := clonePlan(plan)
	result.Scenario.Questions[index] = cloneQuestion(replacement)
	result.ManualQuestionIDs = addManualID(
		result.ManualQuestionIDs,
		replacement.ID,
	)
	bumpVersion(&result)
	template, found := service.template(result.Scenario.Template)
	if !found {
		return clonePlan(plan), validationError(
			"edit Scenario question",
			"场景模板不存在。",
			"重新选择内置模板。",
		)
	}
	if err := validatePlan(result, template); err != nil {
		return clonePlan(plan), err
	}
	return result, nil
}

// UpdateRules edits the Coach mode and time budget and marks both as manual.
func (service *Service) UpdateRules(
	plan Plan,
	mode contracts.ScenarioMode,
	timeBudgetSeconds int,
) (Plan, error) {
	if plan.Locked {
		return clonePlan(plan), policyError(
			"已确认场景的 Coach 规则已锁定。",
			"创建新场景版本后再修改模式或时长。",
		)
	}
	if !validMode(mode) || timeBudgetSeconds <= 0 {
		return clonePlan(plan), validationError(
			"edit Scenario rules",
			"场景模式或时长无效。",
			"选择 strict、standard、coach 和正数时长。",
		)
	}
	result := clonePlan(plan)
	result.Scenario.Mode = mode
	result.Scenario.TimeBudgetSeconds = timeBudgetSeconds
	result.ManualRules = true
	bumpVersion(&result)
	template, found := service.template(result.Scenario.Template)
	if !found {
		return clonePlan(plan), validationError(
			"edit Scenario rules",
			"场景模板不存在。",
			"重新选择内置模板。",
		)
	}
	if err := validatePlan(result, template); err != nil {
		return clonePlan(plan), err
	}
	return result, nil
}

// Confirm locks and atomically persists one immutable Scenario version.
func (service *Service) Confirm(
	ctx context.Context,
	plan Plan,
) (Plan, error) {
	if service == nil || service.repository == nil {
		return clonePlan(plan), domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"confirm Scenario plan",
			"场景存储不可用。",
			"保留当前计划并重新打开场景页。",
			true,
		)
	}
	if plan.Locked {
		return clonePlan(plan), policyError(
			"场景版本已经确认并锁定。",
			"直接开始训练，或创建新版本。",
		)
	}
	template, found := service.template(plan.Scenario.Template)
	if !found {
		return clonePlan(plan), validationError(
			"confirm Scenario plan",
			"场景模板不存在。",
			"重新选择内置模板。",
		)
	}
	persistedProfile, found, err := service.repository.GetProfileAggregate(
		ctx,
		plan.ProfileID,
	)
	if err != nil {
		return clonePlan(plan), persistenceFailure(err)
	}
	if !found || persistedProfile.ConfirmedAt == nil ||
		persistedProfile.ConfirmedAt.IsZero() {
		return clonePlan(plan), validationError(
			"confirm Scenario plan",
			"已确认画像不存在或已失效。",
			"返回画像页重新确认后再保存场景。",
		)
	}
	if err := persistedProfile.Metadata.Source.Validate(); err != nil {
		return clonePlan(plan), err
	}
	if err := coreprofile.ValidateTrace(
		persistedProfile.Candidate,
		persistedProfile.Metadata.Source.Text,
	); err != nil {
		return clonePlan(plan), err
	}
	confirmed := clonePlan(plan)
	confirmed.EvidenceIDs = factIDs(persistedProfile.Candidate.Facts)
	confirmedAt := service.now().UTC()
	confirmed.Locked = true
	confirmed.ConfirmedAt = &confirmedAt
	if err := validatePlan(confirmed, template); err != nil {
		return clonePlan(plan), err
	}
	if err := service.repository.SaveScenario(
		ctx,
		confirmed.ID,
		confirmed.ProfileID,
		confirmed.Scenario,
		confirmedAt,
	); err != nil {
		return clonePlan(plan), persistenceFailure(err)
	}
	return confirmed, nil
}

// ControlsFor ensures generation never traps Back and Start only enables for
// a confirmed immutable plan.
func ControlsFor(
	state async.State[Progress],
	plan *Plan,
) Controls {
	busy := state.Phase == async.Pending || state.Phase == async.Streaming
	return Controls{
		GenerateEnabled: !busy,
		StartEnabled: !busy &&
			plan != nil &&
			plan.Locked &&
			plan.ConfirmedAt != nil,
		BackEnabled: true,
	}
}

func (service *Service) validateRequest(
	request Request,
	previous *Plan,
) (Template, error) {
	if !planIDPattern.MatchString(strings.TrimSpace(request.PlanID)) {
		return Template{}, validationError(
			"validate Scenario request",
			"场景计划 ID 无效。",
			"使用 1–64 位字母、数字、连字符或下划线。",
		)
	}
	if strings.TrimSpace(request.Profile.ID) == "" ||
		request.Profile.ConfirmedAt == nil ||
		request.Profile.ConfirmedAt.IsZero() {
		return Template{}, validationError(
			"validate confirmed CandidateProfile",
			"必须先确认候选人画像。",
			"返回画像页确认事实后再生成场景。",
		)
	}
	if err := request.Profile.Metadata.Source.Validate(); err != nil {
		return Template{}, err
	}
	if err := coreprofile.ValidateTrace(
		request.Profile.Candidate,
		request.Profile.Metadata.Source.Text,
	); err != nil {
		return Template{}, err
	}
	template, found := service.template(request.TemplateID)
	if !found {
		return Template{}, validationError(
			"validate Scenario template",
			"选择的场景模板不存在。",
			"选择行为面、项目深挖、基础技术、算法编码、系统设计或综合面。",
		)
	}
	if !validMode(request.Mode) || request.TimeBudgetSeconds <= 0 {
		return Template{}, validationError(
			"validate Scenario request",
			"场景模式或时长无效。",
			"选择有效模式和正数时长。",
		)
	}
	if previous != nil {
		if previous.Locked {
			return Template{}, policyError(
				"已确认场景不能直接刷新。",
				"创建新场景版本后再生成。",
			)
		}
		if previous.BaseID != request.PlanID ||
			previous.ProfileID != request.Profile.ID ||
			previous.Scenario.Template != request.TemplateID {
			return Template{}, validationError(
				"refresh Scenario plan",
				"刷新请求与当前计划不一致。",
				"保留当前计划，或明确创建另一份计划。",
			)
		}
	}
	return template, nil
}

func (service *Service) template(id string) (Template, bool) {
	for _, template := range service.templates {
		if template.ID == id {
			return cloneTemplate(template), true
		}
	}
	return Template{}, false
}

func mergeManualQuestions(
	previous []contracts.ScenarioQuestion,
	manualIDs []string,
	generated []contracts.ScenarioQuestion,
) []contracts.ScenarioQuestion {
	result := cloneQuestions(generated)
	for _, old := range previous {
		if !slices.Contains(manualIDs, old.ID) {
			continue
		}
		index := slices.IndexFunc(
			result,
			func(question contracts.ScenarioQuestion) bool {
				return question.ID == old.ID
			},
		)
		if index >= 0 {
			result[index] = cloneQuestion(old)
		} else {
			result = append(result, cloneQuestion(old))
		}
	}
	return result
}

func bumpVersion(plan *Plan) {
	plan.Revision++
	plan.ID = versionID(plan.BaseID, plan.Revision)
	plan.Scenario.PromptVersion = versionString(plan.Revision)
	plan.Locked = false
	plan.ConfirmedAt = nil
}

func factIDs(facts []contracts.ProfileFact) []contracts.EvidenceID {
	result := make([]contracts.EvidenceID, len(facts))
	for index, fact := range facts {
		result[index] = fact.ID
	}
	return result
}

func cloneTemplate(value Template) Template {
	value.QuestionGuidance = slices.Clone(value.QuestionGuidance)
	value.RubricGuidance = slices.Clone(value.RubricGuidance)
	return value
}

func clonePlan(value Plan) Plan {
	value.Scenario = cloneScenario(value.Scenario)
	value.JDMappings = cloneMappings(value.JDMappings)
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	value.ManualQuestionIDs = slices.Clone(value.ManualQuestionIDs)
	if value.ConfirmedAt != nil {
		confirmedAt := *value.ConfirmedAt
		value.ConfirmedAt = &confirmedAt
	}
	return value
}

func cloneScenario(value contracts.Scenario) contracts.Scenario {
	value.Questions = cloneQuestions(value.Questions)
	return value
}

func cloneQuestions(
	values []contracts.ScenarioQuestion,
) []contracts.ScenarioQuestion {
	result := make([]contracts.ScenarioQuestion, len(values))
	for index, value := range values {
		result[index] = cloneQuestion(value)
	}
	return result
}

func cloneQuestion(
	value contracts.ScenarioQuestion,
) contracts.ScenarioQuestion {
	value.Rubric = slices.Clone(value.Rubric)
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	return value
}

func cloneMappings(values []JDMapping) []JDMapping {
	result := make([]JDMapping, len(values))
	for index, value := range values {
		value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
		result[index] = value
	}
	return result
}

func plannerFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"generate Scenario plan",
		"Scenario Planner Provider",
		"无法生成场景计划。",
		"当前计划和手工编辑已保留；检查 Provider 后重试。",
		true,
		err,
	)
}

func persistenceFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"save Scenario version",
		"SQLite",
		"无法保存确认后的场景版本。",
		"当前计划仍未锁定；检查数据库后重试。",
		true,
		err,
	)
}

func contextFailure(
	ctx context.Context,
	message string,
) *domainerr.Error {
	if err := ctx.Err(); err != nil {
		return domainerr.Wrap(
			domainerr.CodeOperationCancelled,
			"generate Scenario plan",
			"Scenario Planner",
			message,
			"当前计划和手工编辑已保留，可重新生成。",
			true,
			err,
		)
	}
	return nil
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}
