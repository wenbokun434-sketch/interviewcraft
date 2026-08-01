package coding

import (
	"context"
	"strings"
)

// BasicFormatter provides a deterministic, dependency-free safe baseline for
// all supported languages. It normalizes line endings and trailing whitespace
// without rewriting syntax or changing indentation semantics.
type BasicFormatter struct{}

// Format normalizes one source buffer.
func (BasicFormatter) Format(
	ctx context.Context,
	language Language,
	source string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !supportedLanguage(language) {
		return "", validationError("format code draft", "不支持该代码语言。")
	}
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")
	lines := strings.Split(source, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	formatted := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if formatted == "" {
		return "", nil
	}
	return formatted + "\n", nil
}
