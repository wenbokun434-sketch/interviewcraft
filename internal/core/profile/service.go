package profile

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// Progress identifies the current create/save stage.
type Progress struct {
	Stage string
}

// Observer receives typed profile creation states.
type Observer func(async.State[Progress])

// Service validates evidence and coordinates atomic persistence.
type Service struct {
	repository Repository
	structurer Structurer
	now        func() time.Time
}

// NewService constructs a Profile service without starting external work.
func NewService(
	repository Repository,
	structurer Structurer,
	now func() time.Time,
) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		repository: repository,
		structurer: structurer,
		now:        now,
	}
}

// Create structures, validates, and atomically saves one profile. No
// persistence call occurs before every evidence invariant passes.
func (service *Service) Create(
	ctx context.Context,
	id string,
	source Source,
	targetRole string,
	observer Observer,
) (Aggregate, error) {
	notify(observer, async.NewPending[Progress]())
	if service == nil || service.repository == nil || service.structurer == nil {
		failure := domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"create CandidateProfile",
			"画像服务依赖不可用。",
			"重新初始化画像服务后重试。",
			true,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return Aggregate{}, failure
	}
	id = strings.TrimSpace(id)
	targetRole = strings.TrimSpace(targetRole)
	if id == "" || targetRole == "" {
		failure := validationError(
			"create CandidateProfile",
			"画像 ID 和目标岗位不能为空。",
		)
		notify(observer, async.NewFailed[Progress](failure))
		return Aggregate{}, failure
	}
	if err := source.Validate(); err != nil {
		failure := profileFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return Aggregate{}, failure
	}
	if err := contextFailure(ctx, "解析已取消，未保存画像。"); err != nil {
		notify(observer, async.NewFailed[Progress](err))
		return Aggregate{}, err
	}

	structuring := Progress{Stage: "正在从简历文本生成可追溯画像"}
	notify(observer, async.NewStreaming(&structuring))
	candidate, err := service.structurer.Structure(ctx, source, targetRole)
	if err != nil {
		failure := structurerFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return Aggregate{}, failure
	}
	if candidate.TargetRole != targetRole {
		failure := validationError(
			"validate CandidateProfile target",
			"画像目标岗位与用户输入不一致。",
		)
		notify(observer, async.NewFailed[Progress](failure))
		return Aggregate{}, failure
	}

	validating := Progress{Stage: "正在校验事实来源与待确认推断"}
	notify(observer, async.NewStreaming(&validating))
	if err := ValidateTrace(candidate, source.Text); err != nil {
		failure := profileFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return Aggregate{}, failure
	}
	if err := contextFailure(ctx, "解析已取消，未保存画像。"); err != nil {
		notify(observer, async.NewFailed[Progress](err))
		return Aggregate{}, err
	}

	now := service.now().UTC()
	aggregate := Aggregate{
		ID:        id,
		Candidate: candidate,
		Metadata: Metadata{
			Source:             source,
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}
	saving := Progress{Stage: "正在保存本地画像"}
	notify(observer, async.NewStreaming(&saving))
	if err := service.repository.SaveProfileAggregate(ctx, aggregate); err != nil {
		failure := profileFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return Aggregate{}, failure
	}
	notify(observer, async.NewSucceeded(Progress{Stage: "画像已保存"}))
	return aggregate, nil
}

// Load restores profile text and exact lock state after restart.
func (service *Service) Load(
	ctx context.Context,
	profileID string,
) (Aggregate, bool, error) {
	if service == nil || service.repository == nil {
		return Aggregate{}, false, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"load CandidateProfile",
			"画像存储不可用。",
			"重新启动后重试。",
			true,
		)
	}
	return service.repository.GetProfileAggregate(ctx, profileID)
}

// Confirm records that the user reviewed the current facts and pending
// inferences. Pending inferences remain inferences and are never promoted to
// facts by this command.
func (service *Service) Confirm(
	ctx context.Context,
	profileID string,
) (Aggregate, error) {
	return service.mutate(ctx, profileID, func(aggregate *Aggregate) error {
		confirmedAt := service.now().UTC()
		aggregate.ConfirmedAt = &confirmedAt
		return nil
	})
}

// EditFact replaces one unlocked fact while retaining trace validation.
func (service *Service) EditFact(
	ctx context.Context,
	profileID string,
	replacement contracts.ProfileFact,
) (Aggregate, error) {
	return service.mutate(ctx, profileID, func(aggregate *Aggregate) error {
		if isFactLocked(aggregate.Metadata, replacement.ID) {
			return lockedError("事实")
		}
		index := slices.IndexFunc(
			aggregate.Candidate.Facts,
			func(item contracts.ProfileFact) bool { return item.ID == replacement.ID },
		)
		if index < 0 {
			return validationError("edit profile fact", "找不到要编辑的事实。")
		}
		aggregate.Candidate.Facts[index] = replacement
		aggregate.ConfirmedAt = nil
		return nil
	})
}

// EditInference replaces one unlocked inference and preserves its unconfirmed
// boundary.
func (service *Service) EditInference(
	ctx context.Context,
	profileID string,
	replacement contracts.ProfileInference,
) (Aggregate, error) {
	return service.mutate(ctx, profileID, func(aggregate *Aggregate) error {
		if isInferenceLocked(aggregate.Metadata, replacement.ID) {
			return lockedError("推断")
		}
		if !replacement.NeedsConfirmation {
			return validationError(
				"edit profile inference",
				"未确认推断不能直接变成既定事实。",
			)
		}
		index := slices.IndexFunc(
			aggregate.Candidate.Inferences,
			func(item contracts.ProfileInference) bool {
				return item.ID == replacement.ID
			},
		)
		if index < 0 {
			return validationError("edit profile inference", "找不到要编辑的推断。")
		}
		aggregate.Candidate.Inferences[index] = replacement
		aggregate.ConfirmedAt = nil
		return nil
	})
}

// DeleteItem removes one unlocked fact or inference.
func (service *Service) DeleteItem(
	ctx context.Context,
	profileID string,
	itemID string,
) (Aggregate, error) {
	return service.mutate(ctx, profileID, func(aggregate *Aggregate) error {
		evidenceID := contracts.EvidenceID(itemID)
		if isFactLocked(aggregate.Metadata, evidenceID) ||
			isInferenceLocked(aggregate.Metadata, itemID) {
			return lockedError("画像字段")
		}
		factIndex := slices.IndexFunc(
			aggregate.Candidate.Facts,
			func(item contracts.ProfileFact) bool { return item.ID == evidenceID },
		)
		if factIndex >= 0 {
			aggregate.Candidate.Facts = slices.Delete(
				aggregate.Candidate.Facts,
				factIndex,
				factIndex+1,
			)
			aggregate.ConfirmedAt = nil
			return nil
		}
		inferenceIndex := slices.IndexFunc(
			aggregate.Candidate.Inferences,
			func(item contracts.ProfileInference) bool { return item.ID == itemID },
		)
		if inferenceIndex >= 0 {
			aggregate.Candidate.Inferences = slices.Delete(
				aggregate.Candidate.Inferences,
				inferenceIndex,
				inferenceIndex+1,
			)
			aggregate.ConfirmedAt = nil
			return nil
		}
		return validationError("delete profile item", "找不到要删除的画像字段。")
	})
}

// SetLocked changes a fact or inference lock and persists it.
func (service *Service) SetLocked(
	ctx context.Context,
	profileID string,
	itemID string,
	locked bool,
) (Aggregate, error) {
	return service.mutate(ctx, profileID, func(aggregate *Aggregate) error {
		evidenceID := contracts.EvidenceID(itemID)
		if slices.ContainsFunc(
			aggregate.Candidate.Facts,
			func(item contracts.ProfileFact) bool { return item.ID == evidenceID },
		) {
			aggregate.Metadata.LockedFactIDs = setEvidenceLock(
				aggregate.Metadata.LockedFactIDs,
				evidenceID,
				locked,
			)
			return nil
		}
		if slices.ContainsFunc(
			aggregate.Candidate.Inferences,
			func(item contracts.ProfileInference) bool { return item.ID == itemID },
		) {
			aggregate.Metadata.LockedInferenceIDs = setStringLock(
				aggregate.Metadata.LockedInferenceIDs,
				itemID,
				locked,
			)
			return nil
		}
		return validationError("lock profile item", "找不到要锁定的画像字段。")
	})
}

// Delete removes the profile and all database-derived rows transactionally.
func (service *Service) Delete(
	ctx context.Context,
	profileID string,
) (bool, error) {
	if service == nil || service.repository == nil {
		return false, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"delete CandidateProfile",
			"画像存储不可用。",
			"重新启动后重试。",
			true,
		)
	}
	return service.repository.DeleteProfile(ctx, profileID)
}

func (service *Service) mutate(
	ctx context.Context,
	profileID string,
	change func(*Aggregate) error,
) (Aggregate, error) {
	aggregate, found, err := service.Load(ctx, profileID)
	if err != nil {
		return Aggregate{}, err
	}
	if !found {
		return Aggregate{}, validationError(
			"edit CandidateProfile",
			"找不到要编辑的画像。",
		)
	}
	aggregate = cloneAggregate(aggregate)
	if err := change(&aggregate); err != nil {
		return Aggregate{}, err
	}
	if err := contextFailure(ctx, "画像编辑已取消，现有画像未改变。"); err != nil {
		return Aggregate{}, err
	}
	aggregate.Metadata.UpdatedAt = service.now().UTC()
	if err := ValidateTrace(
		aggregate.Candidate,
		aggregate.Metadata.Source.Text,
	); err != nil {
		return Aggregate{}, err
	}
	if err := service.repository.SaveProfileAggregate(ctx, aggregate); err != nil {
		return Aggregate{}, profileFailure(err)
	}
	return aggregate, nil
}

// ValidateTrace ensures every fact, project, and skill is supported by the
// normalized resume text while keeping all inferences unconfirmed.
func ValidateTrace(candidate contracts.CandidateProfile, source string) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	factIDs := make(map[contracts.EvidenceID]struct{}, len(candidate.Facts))
	for _, fact := range candidate.Facts {
		if _, exists := factIDs[fact.ID]; exists {
			return validationError(
				"validate CandidateProfile evidence",
				"画像事实 ID 不能重复。",
			)
		}
		factIDs[fact.ID] = struct{}{}
		span := fact.SourceSpan
		if span.Start < 0 || span.End > len(source) || span.Start >= span.End ||
			source[span.Start:span.End] != span.Text {
			return validationError(
				"validate CandidateProfile evidence",
				"画像事实的 source_span 与简历原文不一致。",
			)
		}
		if !containsFold(span.Text, fact.Value) {
			return validationError(
				"validate CandidateProfile evidence",
				"画像事实没有被 source_span 原文直接支持。",
			)
		}
	}
	inferenceIDs := make(map[string]struct{}, len(candidate.Inferences))
	for _, inference := range candidate.Inferences {
		if _, exists := inferenceIDs[inference.ID]; exists {
			return validationError(
				"validate CandidateProfile inference",
				"画像推断 ID 不能重复。",
			)
		}
		inferenceIDs[inference.ID] = struct{}{}
		if _, conflicts := factIDs[contracts.EvidenceID(inference.ID)]; conflicts {
			return validationError(
				"validate CandidateProfile inference",
				"事实与推断不能使用相同 ID。",
			)
		}
	}
	for _, project := range candidate.Projects {
		if !containsFold(source, project) {
			return validationError(
				"validate CandidateProfile projects",
				"画像项目没有简历原文证据。",
			)
		}
	}
	for _, skill := range candidate.Skills {
		if !containsFold(source, skill) {
			return validationError(
				"validate CandidateProfile skills",
				"画像技能没有简历原文证据。",
			)
		}
	}
	return nil
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(
		strings.ToLower(strings.TrimSpace(haystack)),
		strings.ToLower(strings.TrimSpace(needle)),
	)
}

func contextFailure(
	ctx context.Context,
	message string,
) *domainerr.Error {
	if err := ctx.Err(); err != nil {
		return domainerr.Wrap(
			domainerr.CodeOperationCancelled,
			"create CandidateProfile",
			"profile service",
			message,
			"输入仍保留，可重新开始解析。",
			true,
			err,
		)
	}
	return nil
}

func profileFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"save CandidateProfile",
		"profile repository",
		"无法保存本地画像。",
		"简历输入仍保留；检查数据库后重试。",
		true,
		err,
	)
}

func structurerFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"structure CandidateProfile",
		"profile structurer",
		"无法从简历生成画像。",
		"保留简历输入；检查模型连接后重试。",
		true,
		err,
	)
}

func cloneAggregate(value Aggregate) Aggregate {
	value.Candidate.Facts = slices.Clone(value.Candidate.Facts)
	value.Candidate.Inferences = slices.Clone(value.Candidate.Inferences)
	value.Candidate.Projects = slices.Clone(value.Candidate.Projects)
	value.Candidate.Skills = slices.Clone(value.Candidate.Skills)
	value.Metadata.LockedFactIDs = slices.Clone(value.Metadata.LockedFactIDs)
	value.Metadata.LockedInferenceIDs = slices.Clone(
		value.Metadata.LockedInferenceIDs,
	)
	if value.ConfirmedAt != nil {
		confirmedAt := *value.ConfirmedAt
		value.ConfirmedAt = &confirmedAt
	}
	return value
}

func lockedError(label string) *domainerr.Error {
	return domainerr.New(
		domainerr.CodePolicyDenied,
		"edit locked profile item",
		fmt.Sprintf("%s已锁定，不能编辑或删除。", label),
		"先解锁该字段后重试。",
		false,
	)
}

func isFactLocked(metadata Metadata, id contracts.EvidenceID) bool {
	return slices.Contains(metadata.LockedFactIDs, id)
}

func isInferenceLocked(metadata Metadata, id string) bool {
	return slices.Contains(metadata.LockedInferenceIDs, id)
}

func setEvidenceLock(
	items []contracts.EvidenceID,
	id contracts.EvidenceID,
	locked bool,
) []contracts.EvidenceID {
	index := slices.Index(items, id)
	if locked && index < 0 {
		return append(items, id)
	}
	if !locked && index >= 0 {
		return slices.Delete(items, index, index+1)
	}
	return items
}

func setStringLock(items []string, id string, locked bool) []string {
	index := slices.Index(items, id)
	if locked && index < 0 {
		return append(items, id)
	}
	if !locked && index >= 0 {
		return slices.Delete(items, index, index+1)
	}
	return items
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}
