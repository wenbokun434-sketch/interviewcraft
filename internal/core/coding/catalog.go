package coding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	codingcontent "github.com/interviewcraft/interviewcraft/content/coding"
)

// LoadQuestions strictly decodes and validates the embedded catalog.
func LoadQuestions() ([]Question, error) {
	names := codingcontent.Names()
	questions := make([]Question, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		payload, err := codingcontent.Read(name)
		if err != nil {
			return nil, fmt.Errorf("read coding question %q: %w", name, err)
		}
		question, err := decodeQuestion(payload)
		if err != nil {
			return nil, fmt.Errorf("decode coding question %q: %w", name, err)
		}
		if question.ID != name {
			return nil, fmt.Errorf("coding question file %q declares id %q", name, question.ID)
		}
		if _, duplicate := seen[question.ID]; duplicate {
			return nil, fmt.Errorf("duplicate coding question %q", question.ID)
		}
		seen[question.ID] = struct{}{}
		questions = append(questions, question)
	}
	if len(questions) == 0 {
		return nil, errors.New("coding question catalog is empty")
	}
	return questions, nil
}

func decodeQuestion(payload []byte) (Question, error) {
	var question Question
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&question); err != nil {
		return Question{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Question{}, err
	}
	if err := question.Validate(); err != nil {
		return Question{}, err
	}
	return question, nil
}
