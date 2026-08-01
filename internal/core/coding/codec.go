package coding

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
)

func encodeDraft(document DraftDocument) ([]byte, error) {
	if err := document.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}

func decodeDraft(payload []byte) (DraftDocument, error) {
	var document DraftDocument
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return DraftDocument{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return DraftDocument{}, err
	}
	if err := document.validate(); err != nil {
		return DraftDocument{}, err
	}
	return document, nil
}

func cloneQuestion(question Question) Question {
	question.Constraints = slices.Clone(question.Constraints)
	question.Examples = slices.Clone(question.Examples)
	question.Rubric = slices.Clone(question.Rubric)
	question.Templates = maps.Clone(question.Templates)
	return question
}

func cloneDraft(document DraftDocument) DraftDocument {
	document.Sources = maps.Clone(document.Sources)
	return document
}

func cloneSnapshot(snapshot *RunSnapshot) *RunSnapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	copy.Result.PublicTests = slices.Clone(snapshot.Result.PublicTests)
	return &copy
}

func cloneWorkspace(workspace Workspace) Workspace {
	workspace.Question = cloneQuestion(workspace.Question)
	workspace.Draft = cloneDraft(workspace.Draft)
	workspace.LatestRun = cloneSnapshot(workspace.LatestRun)
	return workspace
}
