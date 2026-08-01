package coding

import (
	"context"
	"strings"
	"testing"
)

func TestCatalogQuestionsExposeCompleteThreeLanguageContract(t *testing.T) {
	questions, err := LoadQuestions()
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}
	if len(questions) == 0 {
		t.Fatal("catalog is empty")
	}
	for _, question := range questions {
		if err := question.Validate(); err != nil {
			t.Fatalf("Validate %s: %v", question.ID, err)
		}
		if len(question.Examples) < 2 || len(question.Rubric) < 3 ||
			len(question.Constraints) == 0 ||
			question.TargetComplexity.Time == "" ||
			question.TargetComplexity.Space == "" {
			t.Fatalf("incomplete question=%#v", question)
		}
		for _, language := range Languages() {
			if strings.TrimSpace(question.Templates[language]) == "" {
				t.Errorf("%s template %s is empty", question.ID, language)
			}
		}
	}

	questions[0].Templates[LanguagePython] = "changed"
	reloaded, err := LoadQuestions()
	if err != nil || reloaded[0].Templates[LanguagePython] == "changed" {
		t.Fatalf("catalog mutation leaked: err=%v question=%#v", err, reloaded[0])
	}
}

func TestQuestionDecoderAndValidationRejectIncompleteOrUnknownFields(t *testing.T) {
	if _, err := decodeQuestion([]byte(`{"id":"bad","unknown":true}`)); err == nil {
		t.Fatal("decodeQuestion accepted unknown field")
	}
	question := Question{
		ID: "bad", Title: "Bad", Description: "Bad question",
		InputFormat: "input", OutputFormat: "output",
		Constraints:      []string{"n > 0"},
		Examples:         []Example{{Input: "1", Output: "1", Explanation: "one"}},
		TargetComplexity: Complexity{Time: "O(n)", Space: "O(1)"},
		Rubric: []RubricItem{
			{Dimension: "one", Description: "one"},
			{Dimension: "two", Description: "two"},
			{Dimension: "three", Description: "three"},
		},
		Templates: map[Language]string{
			LanguagePython: "pass", LanguageJavaScript: "return;", LanguageJava: "class X {}",
		},
	}
	if err := question.Validate(); err == nil {
		t.Fatal("Question.Validate accepted fewer than two examples")
	}
}

func TestBasicFormatterSupportsThreeLanguagesAndCancellation(t *testing.T) {
	formatter := BasicFormatter{}
	for _, language := range Languages() {
		formatted, err := formatter.Format(
			context.Background(),
			language,
			"line one  \r\nline two\t\r\n",
		)
		if err != nil || formatted != "line one\nline two\n" {
			t.Fatalf("Format %s=%q err=%v", language, formatted, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := formatter.Format(ctx, LanguagePython, "pass"); err == nil {
		t.Fatal("Format accepted cancelled context")
	}
	if _, err := formatter.Format(context.Background(), Language("go"), "package main"); err == nil {
		t.Fatal("Format accepted unsupported language")
	}
}

func TestStoredCodingPayloadMustBeStrictJSONObject(t *testing.T) {
	var runtime RuntimeStats
	if err := strictDecode([]byte(`null`), &runtime); err == nil {
		t.Fatal("strictDecode accepted null runtime payload")
	}
	if err := strictDecode(
		[]byte(`{"duration_ms":1,"peak_memory_kb":2,"extra":true}`),
		&runtime,
	); err == nil {
		t.Fatal("strictDecode accepted unknown runtime field")
	}
}
