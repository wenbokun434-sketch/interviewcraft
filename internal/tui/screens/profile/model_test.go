package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/adapters/resume"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/training"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestKeyboardFormPathParseEditLockDeleteAndSave(t *testing.T) {
	t.Parallel()

	source := screenSource()
	commands := newStubProfileCommands()
	extractor := stubResumeExtractor{source: source}
	model := newScreenModel(t, extractor, commands, 160, 48, false)

	if err := model.UpdateActive(`C:\resumes\保留的文件路径.pdf`); err != nil {
		t.Fatalf("update file: %v", err)
	}
	model.HandleKey("tab")
	if err := model.UpdateActive(source.Text); err != nil {
		t.Fatalf("update paste: %v", err)
	}
	model.HandleKey("tab")
	if err := model.UpdateActive("Backend Engineer"); err != nil {
		t.Fatalf("update role: %v", err)
	}
	model.HandleKey("tab")
	model.HandleKey("down")
	model.HandleKey("tab")
	if err := model.UpdateActive("需要 Go 与 PostgreSQL，负责支付平台。"); err != nil {
		t.Fatalf("update JD: %v", err)
	}
	model.HandleKey("tab")
	model.HandleKey("down")
	if form := model.Form(); form.Level != "Mid" ||
		form.Language != "English" ||
		form.JD == "" ||
		form.FilePath == "" ||
		form.Paste != source.Text {
		t.Fatalf("keyboard form = %#v", form)
	}
	if action := model.HandleKey("x"); action.Intent != IntentParse {
		t.Fatalf("x action = %#v", action)
	}

	if err := model.Parse(context.Background(), nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, expected := range []string{
		"✓ confirmed",
		"? verify 60%",
		"payment service",
		"May have",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("parsed render missing %q", expected)
		}
	}

	model.HandleKey("tab")
	if model.focus.Active() != focusProfile {
		t.Fatalf("focus = %q, want profile", model.focus.Active())
	}
	if action := model.HandleKey("e"); action.Intent != IntentEdit {
		t.Fatalf("edit action = %#v", action)
	}
	if err := model.UpdateActive("Go"); err != nil {
		t.Fatalf("update inline edit: %v", err)
	}
	if action := model.HandleKey("enter"); action.Intent != IntentApplyEdit {
		t.Fatalf("apply action = %#v", action)
	}
	if err := model.ApplyEdit(context.Background()); err != nil {
		t.Fatalf("ApplyEdit: %v", err)
	}
	if action := model.HandleKey("l"); action.Intent != IntentToggleLock {
		t.Fatalf("lock action = %#v", action)
	}
	if err := model.ToggleSelectedLock(context.Background()); err != nil {
		t.Fatalf("ToggleSelectedLock: %v", err)
	}
	aggregate, _ := model.Aggregate()
	if !reflect.DeepEqual(
		aggregate.Metadata.LockedFactIDs,
		[]contracts.EvidenceID{"fact-payment"},
	) {
		t.Fatalf("fact locks = %#v", aggregate.Metadata.LockedFactIDs)
	}
	if err := model.BeginEdit(); !domainerr.IsCode(
		err,
		domainerr.CodePolicyDenied,
	) {
		t.Fatalf("locked BeginEdit error = %v", err)
	}

	model.HandleKey("down")
	if action := model.HandleKey("d"); action.Intent != IntentDelete {
		t.Fatalf("delete action = %#v", action)
	}
	if err := model.DeleteSelected(context.Background()); err != nil {
		t.Fatalf("DeleteSelected: %v", err)
	}
	aggregate, _ = model.Aggregate()
	if len(aggregate.Candidate.Inferences) != 0 {
		t.Fatalf("inferences after delete = %#v", aggregate.Candidate.Inferences)
	}

	if action := model.HandleKey("ctrl+enter"); action.Intent != IntentSave {
		t.Fatalf("save action = %#v", action)
	}
	destination, err := model.SaveAndContinue(context.Background(), nil)
	if err != nil || destination != DestinationScenario {
		t.Fatalf("SaveAndContinue: destination=%q err=%v", destination, err)
	}
	aggregate, _ = model.Aggregate()
	if aggregate.ConfirmedAt == nil {
		t.Fatal("confirmed profile has nil ConfirmedAt")
	}
}

func TestParseProgressCanCancelWithoutLosingInputOrCreatingProfile(t *testing.T) {
	t.Parallel()

	source := screenSource()
	model := newScreenModel(
		t,
		stubResumeExtractor{source: source},
		newStubProfileCommands(),
		120,
		36,
		false,
	)
	setPasteAndRole(t, model, source.Text, "Backend Engineer")
	var phases []async.Phase
	var loadingRender string
	var cancelAction Action

	err := model.Parse(context.Background(), func(state async.State[Progress]) {
		phases = append(phases, state.Phase)
		if state.Phase == async.Streaming {
			loadingRender, _ = model.Render()
			cancelAction = model.HandleKey("c")
		}
	})

	if !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
		t.Fatalf("Parse error = %v, want cancellation", err)
	}
	if len(phases) < 3 ||
		phases[0] != async.Pending ||
		phases[1] != async.Streaming ||
		phases[len(phases)-1] != async.Failed {
		t.Fatalf("phases = %#v", phases)
	}
	if _, found := model.Aggregate(); found {
		t.Fatal("cancelled parse created a partial profile")
	}
	if model.Form().Paste != source.Text {
		t.Fatal("cancelled parse lost pasted text")
	}
	if cancelAction.Intent != IntentCancel ||
		!strings.Contains(loadingRender, "正在读取简历") ||
		!strings.Contains(loadingRender, "[c] 取消") {
		t.Fatalf(
			"loading state/action = %#v output=%q",
			cancelAction,
			loadingRender,
		)
	}
}

func TestEmptyWorkbenchBlocksContinueAndShowsAction(t *testing.T) {
	t.Parallel()

	commands := newStubProfileCommands()
	model := newScreenModel(
		t,
		stubResumeExtractor{},
		commands,
		80,
		24,
		false,
	)
	model.HandleKey("tab")
	model.HandleKey("tab")
	if err := model.UpdateActive("Backend Engineer"); err != nil {
		t.Fatalf("update role: %v", err)
	}

	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "还没有加载简历") {
		t.Fatalf("empty render = %q", rendered)
	}
	if err := model.Parse(context.Background(), nil); !domainerr.IsCode(
		err,
		domainerr.CodeValidation,
	) {
		t.Fatalf("empty Parse error = %v", err)
	}
	if destination, err := model.SaveAndContinue(
		context.Background(),
		nil,
	); destination != DestinationNone ||
		!domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("empty save: destination=%q err=%v", destination, err)
	}
	if commands.createCalls != 0 || commands.confirmCalls != 0 {
		t.Fatalf(
			"empty flow called commands: create=%d confirm=%d",
			commands.createCalls,
			commands.confirmCalls,
		)
	}
}

func TestPathFormatAndSaveFailuresAreActionableAndPreserveDraft(t *testing.T) {
	t.Parallel()

	t.Run("invalid path", func(t *testing.T) {
		model := newScreenModel(
			t,
			resume.Extractor{},
			newStubProfileCommands(),
			120,
			36,
			false,
		)
		missing := `C:\missing-resume.pdf`
		setFileAndRole(t, model, missing, "Backend Engineer")
		model.HandleKey("shift+tab")
		if err := model.UpdateActive(screenSource().Text); err != nil {
			t.Fatalf("update paste fallback: %v", err)
		}
		if err := model.SelectSource(SourceFile); err != nil {
			t.Fatalf("select file: %v", err)
		}

		err := model.Parse(context.Background(), nil)

		var typed *domainerr.Error
		if !errors.As(err, &typed) ||
			!strings.Contains(typed.Message, missing) ||
			!strings.Contains(typed.RecoveryAction, "[p]") {
			t.Fatalf("path error = %#v", err)
		}
		if model.Form().FilePath != missing {
			t.Fatal("path failure lost file draft")
		}
		rendered, renderErr := model.Render()
		if renderErr != nil ||
			!strings.Contains(rendered, "[p]") {
			t.Fatalf("path failure render err=%v output=%q", renderErr, rendered)
		}
		if action := model.HandleKey("p"); action != (Action{}) {
			t.Fatalf("paste fallback action = %#v", action)
		}
		if model.focus.Active() != focusPaste {
			t.Fatalf("paste fallback focus = %q", model.focus.Active())
		}
		if err := model.Parse(context.Background(), nil); err != nil {
			t.Fatalf("paste fallback Parse: %v", err)
		}
		if _, found := model.Aggregate(); !found {
			t.Fatal("paste fallback did not create a profile")
		}
	})

	t.Run("format failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "broken-resume.pdf")
		if err := os.WriteFile(path, []byte("not a PDF"), 0o600); err != nil {
			t.Fatalf("write broken PDF: %v", err)
		}
		model := newScreenModel(
			t,
			resume.Extractor{},
			newStubProfileCommands(),
			120,
			36,
			false,
		)
		setFileAndRole(t, model, path, "Backend Engineer")

		err := model.Parse(context.Background(), nil)

		var typed *domainerr.Error
		if !errors.As(err, &typed) ||
			!strings.Contains(typed.Message, filepath.Base(path)) ||
			!strings.Contains(typed.RecoveryAction, "[p]") {
			t.Fatalf("format error = %#v", err)
		}
		if model.Form().FilePath != path {
			t.Fatal("format failure lost file draft")
		}
	})

	t.Run("save failure", func(t *testing.T) {
		source := screenSource()
		commands := newStubProfileCommands()
		model := newScreenModel(
			t,
			stubResumeExtractor{source: source},
			commands,
			120,
			36,
			false,
		)
		setPasteAndRole(t, model, source.Text, "Backend Engineer")
		if err := model.Parse(context.Background(), nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		commands.confirmErr = domainerr.New(
			domainerr.CodePersistenceFailed,
			"confirm profile",
			"SQLite 无法保存画像。",
			"检查数据库权限后重试。",
			true,
		)

		destination, err := model.SaveAndContinue(context.Background(), nil)

		if destination != DestinationNone ||
			!domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
			t.Fatalf("save failure: destination=%q err=%v", destination, err)
		}
		if _, found := model.Aggregate(); !found ||
			model.Form().Paste != source.Text {
			t.Fatal("save failure discarded profile or paste")
		}
		rendered, renderErr := model.Render()
		if renderErr != nil ||
			!strings.Contains(rendered, "SQLite 无法保存画像") ||
			!strings.Contains(rendered, "检查数据库权限") {
			t.Fatalf("save failure render err=%v output=%q", renderErr, rendered)
		}
	})
}

func TestFocusResizeAndInlineDraftArePreserved(t *testing.T) {
	t.Parallel()

	source := screenSource()
	model := newScreenModel(
		t,
		stubResumeExtractor{source: source},
		newStubProfileCommands(),
		160,
		48,
		false,
	)
	setPasteAndRole(t, model, source.Text, "后端平台工程师")
	model.HandleKey("tab")
	model.HandleKey("down")
	model.HandleKey("tab")
	if err := model.UpdateActive(
		"负责高并发支付平台；需要 Go、PostgreSQL 与跨团队沟通。",
	); err != nil {
		t.Fatalf("update JD: %v", err)
	}
	model.HandleKey("tab")
	model.HandleKey("down")
	formBefore := model.Form()
	focusBefore := model.focus.Active()
	model.HandleKey("?")
	model.Resize(80, 24)
	model.HandleKey("esc")
	if model.focus.Active() != focusBefore ||
		!reflect.DeepEqual(model.Form(), formBefore) {
		t.Fatalf(
			"resize/help changed state: focus=%q form=%#v",
			model.focus.Active(),
			model.Form(),
		)
	}

	if err := model.Parse(context.Background(), nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	model.HandleKey("tab")
	if err := model.BeginEdit(); err != nil {
		t.Fatalf("BeginEdit: %v", err)
	}
	if err := model.UpdateActive("保留中的行内编辑"); err != nil {
		t.Fatalf("UpdateActive edit: %v", err)
	}
	model.Resize(120, 36)
	if model.editBuffer != "保留中的行内编辑" ||
		model.Form().JD != formBefore.JD {
		t.Fatal("resize discarded edit or form draft")
	}
}

func TestResponsiveSnapshotsCJKLongPathAndASCII(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		width     int
		height    int
		ascii     bool
		required  []string
		forbidden []string
	}{
		{
			name: "wide_160x48", width: 160, height: 48,
			required: []string{
				"PROFILE / INPUT",
				"PROFILE / EDITABLE",
				"✓ confirmed",
				"? verify",
			},
		},
		{
			name: "split_120x36", width: 120, height: 36,
			required: []string{
				"PROFILE / INPUT",
				"PROFILE / EDITABLE",
				"后端平台工程师",
			},
		},
		{
			name: "narrow_80x24_ascii", width: 80, height: 24, ascii: true,
			required: []string{
				"+",
				"ok confirmed",
				"? verify",
			},
			forbidden: []string{"┌", "✓ confirmed"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := screenSource()
			model := newScreenModel(
				t,
				stubResumeExtractor{source: source},
				newStubProfileCommands(),
				test.width,
				test.height,
				test.ascii,
			)
			longPath := `C:\候选人资料\2026\非常长的目录名称\后端平台工程师最终版本简历.pdf`
			if err := model.UpdateActive(longPath); err != nil {
				t.Fatalf("update path: %v", err)
			}
			model.HandleKey("tab")
			if err := model.UpdateActive(source.Text); err != nil {
				t.Fatalf("update paste: %v", err)
			}
			model.HandleKey("tab")
			if err := model.UpdateActive("后端平台工程师"); err != nil {
				t.Fatalf("update role: %v", err)
			}
			if err := model.Parse(context.Background(), nil); err != nil {
				t.Fatalf("Parse: %v", err)
			}

			rendered, err := model.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertProfileGeometry(
				t,
				rendered,
				test.width,
				test.height,
			)
			for _, expected := range test.required {
				if !strings.Contains(rendered, expected) {
					t.Errorf("snapshot missing %q", expected)
				}
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(rendered, forbidden) {
					t.Errorf("snapshot contains forbidden %q", forbidden)
				}
			}
		})
	}
}

func TestTrainingToProfileSaveAndRestartRestore(t *testing.T) {
	t.Parallel()

	current := noColorProfileTheme(t, false)
	home, err := training.New(
		emptyTrainingQuery{},
		120,
		36,
		current,
	)
	if err != nil {
		t.Fatalf("training.New: %v", err)
	}
	home.Load(context.Background(), nil)
	if action := home.HandleKey("n"); action.Destination !=
		training.DestinationProfile {
		t.Fatalf("training action = %#v", action)
	}

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	coreService := coreprofile.NewService(
		store,
		resume.NewProfileStructurer(profileGenerator{}),
		func() time.Time {
			return time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
		},
	)
	workbench := newScreenModel(
		t,
		resume.Extractor{},
		coreService,
		120,
		36,
		false,
	)
	sourceText := "Built payment service with Go."
	setPasteAndRole(t, workbench, sourceText, "Backend Engineer")
	if err := workbench.Parse(ctx, nil); err != nil {
		_ = store.Close()
		t.Fatalf("Parse: %v", err)
	}
	destination, err := workbench.SaveAndContinue(ctx, nil)
	if err != nil || destination != DestinationScenario {
		_ = store.Close()
		t.Fatalf("SaveAndContinue: destination=%q err=%v", destination, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restored := newScreenModel(
		t,
		resume.Extractor{},
		coreprofile.NewService(reopened, nil, nil),
		120,
		36,
		false,
	)
	if err := restored.Load(ctx, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	aggregate, found := restored.Aggregate()
	if !found ||
		aggregate.ConfirmedAt == nil ||
		aggregate.Candidate.TargetRole != "Backend Engineer" ||
		restored.Form().Paste != sourceText {
		t.Fatalf(
			"restored aggregate=%#v found=%v form=%#v",
			aggregate,
			found,
			restored.Form(),
		)
	}
}

type stubResumeExtractor struct {
	source coreprofile.Source
	err    error
}

func (extractor stubResumeExtractor) Extract(
	ctx context.Context,
	input resume.Input,
	observer resume.Observer,
) (coreprofile.Source, error) {
	if observer != nil {
		observer(async.NewPending[resume.Progress]())
		progress := resume.Progress{
			Current:    int64(len(input.Text)),
			Total:      int64(len(input.Text)),
			Stage:      "正在读取简历",
			SourceName: extractor.source.Name,
		}
		observer(async.NewStreaming(&progress))
	}
	if err := ctx.Err(); err != nil {
		return coreprofile.Source{}, domainerr.Wrap(
			domainerr.CodeOperationCancelled,
			"extract resume",
			"resume extractor",
			"简历解析已取消。",
			"输入仍保留，可重新开始。",
			true,
			err,
		)
	}
	if extractor.err != nil {
		return coreprofile.Source{}, extractor.err
	}
	return extractor.source, nil
}

type stubProfileCommands struct {
	aggregate    coreprofile.Aggregate
	loadFound    bool
	loadErr      error
	createErr    error
	confirmErr   error
	createCalls  int
	confirmCalls int
}

func newStubProfileCommands() *stubProfileCommands {
	return &stubProfileCommands{}
}

func (commands *stubProfileCommands) Create(
	_ context.Context,
	id string,
	source coreprofile.Source,
	targetRole string,
	observer coreprofile.Observer,
) (coreprofile.Aggregate, error) {
	commands.createCalls++
	if commands.createErr != nil {
		return coreprofile.Aggregate{}, commands.createErr
	}
	if observer != nil {
		progress := coreprofile.Progress{Stage: "正在生成画像"}
		observer(async.NewStreaming(&progress))
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	aggregate := screenAggregate(source)
	aggregate.ID = id
	aggregate.Candidate.TargetRole = targetRole
	aggregate.Metadata.CreatedAt = now
	aggregate.Metadata.UpdatedAt = now
	commands.aggregate = cloneAggregate(aggregate)
	commands.loadFound = true
	return cloneAggregate(aggregate), nil
}

func (commands *stubProfileCommands) Load(
	context.Context,
	string,
) (coreprofile.Aggregate, bool, error) {
	return cloneAggregate(commands.aggregate), commands.loadFound, commands.loadErr
}

func (commands *stubProfileCommands) Confirm(
	context.Context,
	string,
) (coreprofile.Aggregate, error) {
	commands.confirmCalls++
	if commands.confirmErr != nil {
		return coreprofile.Aggregate{}, commands.confirmErr
	}
	value := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	commands.aggregate.ConfirmedAt = &value
	return cloneAggregate(commands.aggregate), nil
}

func (commands *stubProfileCommands) EditFact(
	_ context.Context,
	_ string,
	replacement contracts.ProfileFact,
) (coreprofile.Aggregate, error) {
	for index, fact := range commands.aggregate.Candidate.Facts {
		if fact.ID == replacement.ID {
			commands.aggregate.Candidate.Facts[index] = replacement
			commands.aggregate.ConfirmedAt = nil
			return cloneAggregate(commands.aggregate), nil
		}
	}
	return coreprofile.Aggregate{}, errors.New("fact not found")
}

func (commands *stubProfileCommands) EditInference(
	_ context.Context,
	_ string,
	replacement contracts.ProfileInference,
) (coreprofile.Aggregate, error) {
	for index, inference := range commands.aggregate.Candidate.Inferences {
		if inference.ID == replacement.ID {
			commands.aggregate.Candidate.Inferences[index] = replacement
			commands.aggregate.ConfirmedAt = nil
			return cloneAggregate(commands.aggregate), nil
		}
	}
	return coreprofile.Aggregate{}, errors.New("inference not found")
}

func (commands *stubProfileCommands) DeleteItem(
	_ context.Context,
	_ string,
	id string,
) (coreprofile.Aggregate, error) {
	for index, fact := range commands.aggregate.Candidate.Facts {
		if string(fact.ID) == id {
			commands.aggregate.Candidate.Facts = append(
				commands.aggregate.Candidate.Facts[:index],
				commands.aggregate.Candidate.Facts[index+1:]...,
			)
			return cloneAggregate(commands.aggregate), nil
		}
	}
	for index, inference := range commands.aggregate.Candidate.Inferences {
		if inference.ID == id {
			commands.aggregate.Candidate.Inferences = append(
				commands.aggregate.Candidate.Inferences[:index],
				commands.aggregate.Candidate.Inferences[index+1:]...,
			)
			return cloneAggregate(commands.aggregate), nil
		}
	}
	return coreprofile.Aggregate{}, errors.New("item not found")
}

func (commands *stubProfileCommands) SetLocked(
	_ context.Context,
	_ string,
	id string,
	locked bool,
) (coreprofile.Aggregate, error) {
	for _, fact := range commands.aggregate.Candidate.Facts {
		if string(fact.ID) != id {
			continue
		}
		commands.aggregate.Metadata.LockedFactIDs = updateEvidenceLock(
			commands.aggregate.Metadata.LockedFactIDs,
			fact.ID,
			locked,
		)
		return cloneAggregate(commands.aggregate), nil
	}
	for _, inference := range commands.aggregate.Candidate.Inferences {
		if inference.ID != id {
			continue
		}
		commands.aggregate.Metadata.LockedInferenceIDs = updateStringLock(
			commands.aggregate.Metadata.LockedInferenceIDs,
			id,
			locked,
		)
		return cloneAggregate(commands.aggregate), nil
	}
	return coreprofile.Aggregate{}, errors.New("item not found")
}

func screenSource() coreprofile.Source {
	return coreprofile.Source{
		Kind: coreprofile.SourcePaste,
		Name: "pasted-resume.txt",
		Text: "Built payment service with Go and PostgreSQL.",
	}
}

func screenAggregate(source coreprofile.Source) coreprofile.Aggregate {
	return coreprofile.Aggregate{
		ID: "default",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID:    "fact-payment",
				Field: "project",
				Value: "payment service",
				SourceSpan: contracts.SourceSpan{
					Start: 0,
					End:   len(source.Text),
					Text:  source.Text,
				},
			}},
			Inferences: []contracts.ProfileInference{{
				ID:                "inference-lead",
				Field:             "leadership",
				Value:             "May have led delivery",
				Confidence:        0.6,
				NeedsConfirmation: true,
			}},
			Projects: []string{"payment service"},
			Skills:   []string{"Go", "PostgreSQL"},
		},
		Metadata: coreprofile.Metadata{
			Source:             source,
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
		},
	}
}

func setPasteAndRole(
	t *testing.T,
	model *Model,
	paste string,
	role string,
) {
	t.Helper()
	model.HandleKey("tab")
	if err := model.UpdateActive(paste); err != nil {
		t.Fatalf("update paste: %v", err)
	}
	model.HandleKey("tab")
	if err := model.UpdateActive(role); err != nil {
		t.Fatalf("update role: %v", err)
	}
}

func setFileAndRole(
	t *testing.T,
	model *Model,
	path string,
	role string,
) {
	t.Helper()
	if err := model.UpdateActive(path); err != nil {
		t.Fatalf("update file: %v", err)
	}
	model.HandleKey("tab")
	model.HandleKey("tab")
	if err := model.UpdateActive(role); err != nil {
		t.Fatalf("update role: %v", err)
	}
}

func newScreenModel(
	t *testing.T,
	extractor ResumeExtractor,
	commands ProfileCommands,
	width int,
	height int,
	ascii bool,
) *Model {
	t.Helper()
	model, err := New(Options{
		ProfileID: "default",
		Extractor: extractor,
		Profiles:  commands,
		Width:     width,
		Height:    height,
		Theme:     noColorProfileTheme(t, ascii),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func noColorProfileTheme(t *testing.T, ascii bool) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode:         theme.Auto,
		ColorMode:    theme.NoColor,
		UseASCII:     ascii,
		ReduceMotion: true,
	})
	if err != nil {
		t.Fatalf("Resolve theme: %v", err)
	}
	return current
}

func assertProfileGeometry(
	t *testing.T,
	rendered string,
	width int,
	height int,
) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("rows = %d, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("row %d width=%d, want %d", index, got, width)
		}
	}
}

func updateEvidenceLock(
	values []contracts.EvidenceID,
	id contracts.EvidenceID,
	locked bool,
) []contracts.EvidenceID {
	index := -1
	for current, value := range values {
		if value == id {
			index = current
			break
		}
	}
	if locked && index < 0 {
		return append(values, id)
	}
	if !locked && index >= 0 {
		return append(values[:index], values[index+1:]...)
	}
	return values
}

func updateStringLock(values []string, id string, locked bool) []string {
	index := -1
	for current, value := range values {
		if value == id {
			index = current
			break
		}
	}
	if locked && index < 0 {
		return append(values, id)
	}
	if !locked && index >= 0 {
		return append(values[:index], values[index+1:]...)
	}
	return values
}

type emptyTrainingQuery struct{}

func (emptyTrainingQuery) LoadTrainingHome(
	context.Context,
	int,
) (db.TrainingHomeData, error) {
	return db.TrainingHomeData{}, nil
}

type profileGenerator struct{}

func (profileGenerator) Generate(
	context.Context,
	llm.Request,
) ([]byte, error) {
	return []byte(`{
		"target_role":"Backend Engineer",
		"facts":[{
			"id":"fact-payment",
			"field":"project",
			"value":"payment service",
			"source_span":{
				"start":6,
				"end":30,
				"text":"payment service with Go."
			}
		}],
		"inferences":[],
		"projects":["payment service"],
		"skills":["Go"]
	}`), nil
}
