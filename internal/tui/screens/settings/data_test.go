package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/transfer"
)

type dataManagerStub struct {
	inventory    transfer.Inventory
	inventoryErr error
	export       func(transfer.ExportOptions, transfer.Observer) (transfer.ExportResult, error)
	importData   func(string, transfer.Observer) (transfer.ImportResult, error)
	deleteData   func(transfer.Confirmation, transfer.Observer) (int64, error)
}

func (stub *dataManagerStub) Inventory(context.Context) (transfer.Inventory, error) {
	return stub.inventory, stub.inventoryErr
}

func (stub *dataManagerStub) Export(
	_ context.Context,
	options transfer.ExportOptions,
	observer transfer.Observer,
) (transfer.ExportResult, error) {
	if stub.export == nil {
		return transfer.ExportResult{}, errors.New("export unavailable")
	}
	return stub.export(options, observer)
}

func (stub *dataManagerStub) Import(
	_ context.Context,
	path string,
	observer transfer.Observer,
) (transfer.ImportResult, error) {
	if stub.importData == nil {
		return transfer.ImportResult{}, errors.New("import unavailable")
	}
	return stub.importData(path, observer)
}

func (stub *dataManagerStub) Delete(
	_ context.Context,
	confirmation transfer.Confirmation,
	observer transfer.Observer,
) (int64, error) {
	if stub.deleteData == nil {
		return 0, errors.New("delete unavailable")
	}
	return stub.deleteData(confirmation, observer)
}

func TestDataVaultMainLoadingEmptyAndFailureStates(t *testing.T) {
	normal := &dataManagerStub{inventory: transfer.Inventory{
		Profiles: 1, Scenarios: 1, Sessions: 2, Reports: 1, CoachItems: 3,
		SessionIDs: []string{"session-new", "session-old"},
	}}
	model := newDataSettingsModel(t, normal, 120, 36, false, false)
	var phases []async.Phase
	model.LoadData(context.Background(), func(state async.State[transfer.Inventory]) {
		phases = append(phases, state.Phase)
	})
	if len(phases) != 2 || phases[0] != async.Pending || phases[1] != async.Succeeded {
		t.Fatalf("normal phases=%v", phases)
	}
	focusDataVault(model)
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("normal Render: %v", err)
	}
	for _, expected := range []string{
		"DATA VAULT", "画像 1", "会话 2", "报告 1", "session-new",
		"Coach transcript  excluded (default)", "Provider secrets  never exported",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("normal data vault missing %q", expected)
		}
	}

	model.dataState = async.NewPending[transfer.Inventory]()
	rendered, err = model.Render()
	if err != nil || !strings.Contains(rendered, "正在读取本地数据清单") {
		t.Fatalf("loading err=%v output=%q", err, rendered)
	}

	empty := newDataSettingsModel(t, &dataManagerStub{
		inventory: transfer.Inventory{SessionIDs: []string{}},
	}, 80, 24, true, true)
	empty.LoadData(context.Background(), nil)
	focusDataVault(empty)
	if destination := empty.HandleKey("e"); destination != DestinationNone {
		t.Fatalf("empty export destination=%q, want none", destination)
	}
	if destination := empty.HandleKey("i"); destination != DestinationDataImport {
		t.Fatalf("empty import destination=%q", destination)
	}
	rendered, err = empty.Render()
	if err != nil || !strings.Contains(rendered, "还没有本地训练数据") ||
		!strings.Contains(rendered, "[i] 从迁移包恢复") {
		t.Fatalf("empty err=%v output=%q", err, rendered)
	}

	failed := newDataSettingsModel(t, &dataManagerStub{
		inventoryErr: errors.New("secret database detail"),
	}, 120, 36, false, false)
	failed.LoadData(context.Background(), nil)
	focusDataVault(failed)
	rendered, err = failed.Render()
	if err != nil || !strings.Contains(rendered, "无法完成本地数据操作") ||
		strings.Contains(rendered, "secret database detail") {
		t.Fatalf("failure err=%v output=%q", err, rendered)
	}
}

func TestDataVaultResponsivePrivacyChoiceAndExportProgress(t *testing.T) {
	manager := &dataManagerStub{inventory: transfer.Inventory{
		Profiles: 1, Scenarios: 1, Sessions: 1, Reports: 1, CoachItems: 1,
		SessionIDs: []string{"session-data"},
	}}
	var exported transfer.ExportOptions
	manager.export = func(
		options transfer.ExportOptions,
		observer transfer.Observer,
	) (transfer.ExportResult, error) {
		exported = options
		observer(async.NewPending[transfer.Progress]())
		progress := transfer.Progress{
			Stage: "writing_artifact", Current: 3, Total: 4,
			Message: "正在写入迁移包",
		}
		observer(async.NewStreaming(&progress))
		completed := transfer.Progress{Stage: "completed", Current: 4, Total: 4}
		observer(async.NewSucceeded(completed))
		return transfer.ExportResult{Path: options.OutputPath, Format: options.Format}, nil
	}

	for _, size := range []struct {
		width, height int
		ascii         bool
		reduced       bool
	}{
		{160, 48, false, false},
		{120, 36, false, false},
		{80, 24, true, true},
	} {
		model := newDataSettingsModel(
			t, manager, size.width, size.height, size.ascii, size.reduced,
		)
		model.LoadData(context.Background(), nil)
		focusDataVault(model)
		rendered, err := model.Render()
		if err != nil {
			t.Fatalf("Render %dx%d: %v", size.width, size.height, err)
		}
		assertGeometry(t, rendered, size.width, size.height)
		for _, expected := range []string{"DATA VAULT", "session-data", "never exported"} {
			if !strings.Contains(rendered, expected) {
				t.Errorf("%dx%d missing %q: %q", size.width, size.height, expected, rendered)
			}
		}
	}

	model := newDataSettingsModel(t, manager, 120, 36, false, false)
	model.LoadData(context.Background(), nil)
	focusDataVault(model)
	if destination := model.HandleKey("i"); destination != DestinationNone {
		t.Fatalf("non-empty import destination=%q, want none", destination)
	}
	if destination := model.HandleKey("c"); destination != DestinationNone ||
		!model.IncludeCoachContent() {
		t.Fatalf("privacy destination=%q include=%v", destination, model.IncludeCoachContent())
	}
	if destination := model.HandleKey("e"); destination != DestinationDataExport {
		t.Fatalf("export destination=%q", destination)
	}
	var phases []async.Phase
	_, err := model.ExportData(
		context.Background(), transfer.FormatPackage, "vault.json",
		func(state async.State[transfer.Progress]) { phases = append(phases, state.Phase) },
	)
	if err != nil {
		t.Fatalf("ExportData: %v", err)
	}
	if exported.SessionID != "session-data" || !exported.IncludeCoachContent ||
		exported.OutputPath != "vault.json" {
		t.Fatalf("exported options=%#v", exported)
	}
	if len(phases) != 3 || phases[0] != async.Pending ||
		phases[1] != async.Streaming || phases[2] != async.Succeeded {
		t.Fatalf("operation phases=%v", phases)
	}
}

func TestDataVaultDeletionRequiresUIConfirmationForSessionAndAll(t *testing.T) {
	manager := &dataManagerStub{inventory: transfer.Inventory{
		Profiles: 1, Scenarios: 1, Sessions: 1, Reports: 1,
		SessionIDs: []string{"session-delete"},
	}}
	var confirmations []transfer.Confirmation
	manager.deleteData = func(
		confirmation transfer.Confirmation,
		observer transfer.Observer,
	) (int64, error) {
		confirmations = append(confirmations, confirmation)
		observer(async.NewPending[transfer.Progress]())
		completed := transfer.Progress{Stage: "completed", Current: 2, Total: 2}
		observer(async.NewSucceeded(completed))
		if confirmation.Scope == transfer.DeleteAll {
			manager.inventory = transfer.Inventory{SessionIDs: []string{}}
		} else {
			manager.inventory.Sessions = 0
			manager.inventory.Reports = 0
			manager.inventory.SessionIDs = []string{}
		}
		return 1, nil
	}
	model := newDataSettingsModel(t, manager, 120, 36, false, false)
	model.LoadData(context.Background(), nil)
	focusDataVault(model)

	if _, err := model.DeleteData(context.Background(), nil); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("direct delete err=%#v", err)
	}
	model.HandleKey("d")
	if model.focus.Active() != focusDataDelete {
		t.Fatalf("confirm focus=%q", model.focus.Active())
	}
	model.HandleKey("enter")
	if model.focus.Active() != focusData || len(confirmations) != 0 {
		t.Fatalf("default cancel focus=%q confirmations=%v", model.focus.Active(), confirmations)
	}
	model.HandleKey("d")
	if destination := model.HandleKey("y"); destination != DestinationDataDelete {
		t.Fatalf("delete destination=%q", destination)
	}
	if _, err := model.DeleteData(context.Background(), nil); err != nil {
		t.Fatalf("DeleteData session: %v", err)
	}
	if len(confirmations) != 1 ||
		confirmations[0].Phrase != transfer.SessionDeletePhrase("session-delete") {
		t.Fatalf("session confirmations=%#v", confirmations)
	}

	manager.inventory = transfer.Inventory{
		Profiles: 1, Scenarios: 1, Sessions: 1,
		SessionIDs: []string{"session-all"},
	}
	model.LoadData(context.Background(), nil)
	model.HandleKey("x")
	if destination := model.HandleKey("y"); destination != DestinationDataDelete {
		t.Fatalf("all delete destination=%q", destination)
	}
	if _, err := model.DeleteData(context.Background(), nil); err != nil {
		t.Fatalf("DeleteData all: %v", err)
	}
	if len(confirmations) != 2 || confirmations[1].Scope != transfer.DeleteAll ||
		confirmations[1].Phrase != transfer.AllDeletePhrase() {
		t.Fatalf("all confirmations=%#v", confirmations)
	}
}

func TestDataVaultOperationFailureIsTypedAndPreservesInventory(t *testing.T) {
	manager := &dataManagerStub{inventory: transfer.Inventory{
		Profiles: 1, Sessions: 1, Reports: 1,
		SessionIDs: []string{"session-safe"},
	}}
	manager.export = func(
		transfer.ExportOptions,
		transfer.Observer,
	) (transfer.ExportResult, error) {
		return transfer.ExportResult{}, errors.New("raw write failure")
	}
	model := newDataSettingsModel(t, manager, 120, 36, false, false)
	model.LoadData(context.Background(), nil)
	focusDataVault(model)
	if _, err := model.ExportData(
		context.Background(), transfer.FormatPackage, "blocked.json", nil,
	); err == nil {
		t.Fatal("ExportData unexpectedly succeeded")
	}
	if model.DataOperationState().Phase != async.Failed ||
		model.DataState().Value == nil || model.DataState().Value.Sessions != 1 {
		t.Fatalf("operation=%#v inventory=%#v", model.DataOperationState(), model.DataState())
	}
	rendered, err := model.Render()
	if err != nil || !strings.Contains(rendered, "无法完成本地数据操作") ||
		strings.Contains(rendered, "raw write failure") {
		t.Fatalf("failure err=%v output=%q", err, rendered)
	}
}

func TestDataVaultImportRefreshAndMissingDependency(t *testing.T) {
	manager := &dataManagerStub{inventory: transfer.Inventory{SessionIDs: []string{}}}
	manager.importData = func(
		path string,
		observer transfer.Observer,
	) (transfer.ImportResult, error) {
		if path != "transfer.json" {
			t.Fatalf("import path=%q", path)
		}
		observer(async.NewPending[transfer.Progress]())
		progress := transfer.Progress{
			Stage: "restoring_sessions", Current: 4, Total: 6,
			Message: "正在恢复会话",
		}
		observer(async.NewStreaming(&progress))
		completed := transfer.Progress{Stage: "completed", Current: 6, Total: 6}
		observer(async.NewSucceeded(completed))
		manager.inventory = transfer.Inventory{
			Profiles: 1, Scenarios: 1, Sessions: 1, Reports: 1,
			SessionIDs: []string{"session-imported"},
		}
		return transfer.ImportResult{Profiles: 1, Sessions: 1, Reports: 1}, nil
	}
	model := newDataSettingsModel(t, manager, 80, 24, true, true)
	model.LoadData(context.Background(), nil)
	focusDataVault(model)
	if destination := model.HandleKey("i"); destination != DestinationDataImport {
		t.Fatalf("import destination=%q", destination)
	}
	result, err := model.ImportData(context.Background(), "transfer.json", nil)
	if err != nil || result.Sessions != 1 {
		t.Fatalf("ImportData result=%#v err=%v", result, err)
	}
	if model.DataState().Value == nil || model.DataState().Value.SessionIDs[0] != "session-imported" {
		t.Fatalf("refreshed state=%#v", model.DataState())
	}

	withoutManager := newDataSettingsModel(t, nil, 80, 24, true, true)
	withoutManager.LoadData(context.Background(), nil)
	if withoutManager.DataState().Phase != async.Failed ||
		!domainerr.IsCode(withoutManager.DataState().Err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("missing manager data state=%#v", withoutManager.DataState())
	}
	if _, err := withoutManager.ExportData(
		context.Background(), transfer.FormatPackage, "missing.json", nil,
	); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("missing manager export err=%#v", err)
	}
}

func newDataSettingsModel(
	t *testing.T,
	manager DataManager,
	width int,
	height int,
	ascii bool,
	reduced bool,
) *Model {
	t.Helper()
	model, err := New(Options{
		Runtime: healthyRuntime(t, true), Data: manager,
		Width: width, Height: height, Theme: noColorTheme(t, ascii, reduced),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func focusDataVault(model *Model) {
	model.HandleKey("tab")
	model.HandleKey("tab")
}
