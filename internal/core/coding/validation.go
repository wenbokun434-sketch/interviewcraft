package coding

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Validate checks the complete visible question contract.
func (question Question) Validate() error {
	for field, value := range map[string]string{
		"question id":   question.ID,
		"title":         question.Title,
		"description":   question.Description,
		"input format":  question.InputFormat,
		"output format": question.OutputFormat,
		"time target":   question.TargetComplexity.Time,
		"space target":  question.TargetComplexity.Space,
	} {
		if strings.TrimSpace(value) == "" {
			return validationError("validate coding question", field+" 不能为空。")
		}
	}
	if !identifierPattern.MatchString(question.ID) {
		return validationError("validate coding question", "代码题 ID 格式无效。")
	}
	if question.Constraints == nil || len(question.Constraints) == 0 {
		return validationError("validate coding question", "代码题必须包含显式约束。")
	}
	for _, constraint := range question.Constraints {
		if strings.TrimSpace(constraint) == "" {
			return validationError("validate coding question", "代码题约束不能包含空项。")
		}
	}
	if question.Examples == nil || len(question.Examples) < 2 {
		return validationError("validate coding question", "代码题至少需要 2 个公开示例。")
	}
	for _, example := range question.Examples {
		if strings.TrimSpace(example.Input) == "" ||
			strings.TrimSpace(example.Output) == "" ||
			strings.TrimSpace(example.Explanation) == "" {
			return validationError("validate coding question", "公开示例必须包含输入、输出和解释。")
		}
	}
	if question.Rubric == nil || len(question.Rubric) < 3 {
		return validationError("validate coding question", "代码题至少需要 3 个评分维度。")
	}
	dimensions := make(map[string]struct{}, len(question.Rubric))
	for _, item := range question.Rubric {
		dimension := strings.ToLower(strings.TrimSpace(item.Dimension))
		if dimension == "" || strings.TrimSpace(item.Description) == "" {
			return validationError("validate coding question", "评分维度与说明不能为空。")
		}
		if _, duplicate := dimensions[dimension]; duplicate {
			return validationError("validate coding question", "代码题评分维度不能重复。")
		}
		dimensions[dimension] = struct{}{}
	}
	if question.Templates == nil || len(question.Templates) != len(languages) {
		return validationError("validate coding question", "代码题必须精确包含 Python、JavaScript、Java 模板。")
	}
	for _, language := range languages {
		if strings.TrimSpace(question.Templates[language]) == "" {
			return validationError("validate coding question", fmt.Sprintf("%s 模板不能为空。", language))
		}
	}
	return nil
}

func (document DraftDocument) validate() error {
	if document.Version != DraftVersion ||
		strings.TrimSpace(document.QuestionID) == "" ||
		!supportedLanguage(document.ActiveLanguage) ||
		document.Sources == nil || len(document.Sources) != len(languages) {
		return validationError("validate code draft", "代码草稿版本、题目或语言集合无效。")
	}
	for _, language := range languages {
		if _, found := document.Sources[language]; !found {
			return validationError("validate code draft", "代码草稿缺少三语言缓冲区。")
		}
	}
	return nil
}

func (result ExecutionResult) validate() error {
	if result.Result.Version != ResultVersion ||
		result.Result.PublicTests == nil ||
		result.Result.HiddenTests.Passed < 0 ||
		result.Result.HiddenTests.Failed < 0 ||
		result.Runtime.DurationMilliseconds < 0 ||
		result.Runtime.PeakMemoryKB < 0 {
		return invalidRunnerResult()
	}
	seen := make(map[string]struct{}, len(result.Result.PublicTests))
	failed := result.Result.HiddenTests.Failed
	for _, item := range result.Result.PublicTests {
		name := strings.TrimSpace(item.Name)
		if name == "" || !validTestStatus(item.Status) {
			return invalidRunnerResult()
		}
		if _, duplicate := seen[name]; duplicate {
			return invalidRunnerResult()
		}
		seen[name] = struct{}{}
		if item.Status != TestPassed {
			failed++
		}
	}
	if !validErrorKind(result.Result.ErrorKind) {
		return invalidRunnerResult()
	}
	switch result.Result.Status {
	case RunPassed:
		if failed != 0 || result.Result.ErrorKind != ErrorNone {
			return invalidRunnerResult()
		}
	case RunFailed:
		if failed == 0 || result.Result.ErrorKind != ErrorNone {
			return invalidRunnerResult()
		}
	case RunError:
		if result.Result.ErrorKind == ErrorNone {
			return invalidRunnerResult()
		}
	default:
		return invalidRunnerResult()
	}
	return nil
}

func supportedLanguage(language Language) bool {
	return language == LanguagePython ||
		language == LanguageJavaScript ||
		language == LanguageJava
}

func validTestStatus(status TestStatus) bool {
	return status == TestPassed || status == TestFailed || status == TestError
}

func validErrorKind(kind ErrorKind) bool {
	switch kind {
	case ErrorNone, ErrorCompile, ErrorRuntime, ErrorTimeout,
		ErrorOutOfMemory, ErrorPolicyDenied, ErrorRunnerUnhealthy:
		return true
	default:
		return false
	}
}

func validateIdentifier(operation, field, value string) error {
	if !identifierPattern.MatchString(strings.TrimSpace(value)) {
		return validationError(operation, field+" 格式无效。")
	}
	return nil
}

func validationError(operation, message string) *domainerr.Error {
	return domainerr.New(
		domainerr.CodeValidation,
		operation,
		message,
		"修正代码题、语言或操作标识后重试。",
		false,
	)
}

func invalidRunnerResult() *domainerr.Error {
	return domainerr.New(
		domainerr.CodeInvalidState,
		"validate runner result",
		"代码执行器返回了无效或不安全的结果。",
		"检查 Runner 健康状态后重试；当前草稿已保留。",
		true,
	)
}
