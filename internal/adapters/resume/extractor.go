// Package resume extracts local resume text without network services or
// external processes.
package resume

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
)

// MaxFileBytes is the hard resume input limit.
const MaxFileBytes int64 = profile.MaxSourceBytes

// Input selects a local path or pasted text.
type Input struct {
	Kind profile.SourceKind
	Path string
	Text string
}

// Progress reports deterministic read and parse stages.
type Progress struct {
	Current    int64
	Total      int64
	Stage      string
	SourceName string
}

// Observer receives typed extraction lifecycle states.
type Observer func(async.State[Progress])

// Extractor reads supported inputs using only the local process.
type Extractor struct{}

// Extract returns normalized text or a typed, actionable failure.
func (Extractor) Extract(
	ctx context.Context,
	input Input,
	observer Observer,
) (profile.Source, error) {
	notify(observer, async.NewPending[Progress]())
	if input.Kind == profile.SourcePaste {
		return extractPaste(ctx, input.Text, observer)
	}

	path := strings.TrimSpace(input.Path)
	if path == "" {
		failure := resumeError(
			domainerr.CodeValidation,
			"read resume",
			"简历路径不能为空。",
			"输入 PDF、DOCX、TXT 路径，或按 [p] 粘贴文本。",
			false,
			nil,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}
	kind := input.Kind
	if kind == "" {
		kind = kindFromExtension(filepath.Ext(path))
	}
	if kind != profile.SourcePDF &&
		kind != profile.SourceDOCX &&
		kind != profile.SourceTXT {
		failure := resumeError(
			domainerr.CodeValidation,
			"read resume",
			"不支持该简历格式："+filepath.Base(path)+"。",
			"使用 PDF、DOCX、TXT，或按 [p] 粘贴文本。",
			false,
			nil,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}
	if expected := kindFromExtension(filepath.Ext(path)); expected != "" &&
		expected != kind {
		failure := resumeError(
			domainerr.CodeValidation,
			"read resume",
			"简历扩展名与输入类型不一致："+filepath.Base(path)+"。",
			"选择正确格式，或按 [p] 粘贴文本。",
			false,
			nil,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}

	payload, name, err := readFile(ctx, path, observer)
	if err != nil {
		failure := resumeFailure(err)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}
	parsing := Progress{
		Current:    int64(len(payload)),
		Total:      int64(len(payload)),
		Stage:      "正在提取简历文本",
		SourceName: name,
	}
	notify(observer, async.NewStreaming(&parsing))
	if err := cancelled(ctx, name); err != nil {
		notify(observer, async.NewFailed[Progress](err))
		return profile.Source{}, err
	}

	var text string
	switch kind {
	case profile.SourceTXT:
		text, err = extractTXT(payload)
	case profile.SourceDOCX:
		text, err = extractDOCX(payload)
	case profile.SourcePDF:
		text, err = extractPDF(payload)
	}
	if err != nil {
		failure := resumeError(
			domainerr.CodeValidation,
			"extract resume text",
			"无法从 "+name+" 提取可用文本。",
			"保留文件名并按 [p] 粘贴文本继续。",
			true,
			err,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}
	text, err = normalizeText(text)
	if err != nil {
		failure := resumeError(
			domainerr.CodeValidation,
			"normalize resume text",
			"简历文本编码无效："+name+"。",
			"将内容保存为 UTF-8 TXT，或按 [p] 粘贴文本。",
			true,
			err,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}
	source := profile.Source{Kind: kind, Name: name, Text: text}
	notify(observer, async.NewSucceeded(Progress{
		Current:    int64(len(payload)),
		Total:      int64(len(payload)),
		Stage:      "简历文本已提取",
		SourceName: name,
	}))
	return source, nil
}

func extractPaste(
	ctx context.Context,
	text string,
	observer Observer,
) (profile.Source, error) {
	name := "pasted-resume.txt"
	if int64(len(text)) > MaxFileBytes {
		failure := resumeError(
			domainerr.CodeValidation,
			"read pasted resume",
			"粘贴的简历文本超过 10MB。",
			"缩短文本后重试。",
			false,
			nil,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}
	if err := cancelled(ctx, name); err != nil {
		notify(observer, async.NewFailed[Progress](err))
		return profile.Source{}, err
	}
	normalized, err := normalizeText(text)
	if err != nil {
		failure := resumeError(
			domainerr.CodeValidation,
			"read pasted resume",
			"粘贴的简历文本为空或编码无效。",
			"粘贴有效的纯文本简历后重试。",
			false,
			err,
		)
		notify(observer, async.NewFailed[Progress](failure))
		return profile.Source{}, failure
	}
	source := profile.Source{
		Kind: profile.SourcePaste,
		Name: name,
		Text: normalized,
	}
	notify(observer, async.NewSucceeded(Progress{
		Current:    int64(len(text)),
		Total:      int64(len(text)),
		Stage:      "粘贴文本已就绪",
		SourceName: name,
	}))
	return source, nil
}

func readFile(
	ctx context.Context,
	path string,
	observer Observer,
) ([]byte, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, filepath.Base(path), resumeError(
			domainerr.CodeDependencyUnavailable,
			"open resume",
			"无法读取简历路径："+path+"。",
			"检查路径与权限，或按 [p] 粘贴文本。",
			true,
			err,
		)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, filepath.Base(path), err
	}
	name := info.Name()
	if !info.Mode().IsRegular() {
		return nil, name, resumeError(
			domainerr.CodeValidation,
			"read resume",
			"简历路径不是普通文件："+path+"。",
			"选择 PDF、DOCX、TXT 文件，或按 [p] 粘贴文本。",
			false,
			nil,
		)
	}
	if info.Size() <= 0 {
		return nil, name, resumeError(
			domainerr.CodeValidation,
			"read resume",
			"简历文件为空："+name+"。",
			"选择包含文本的文件，或按 [p] 粘贴文本。",
			false,
			nil,
		)
	}
	if info.Size() > MaxFileBytes {
		return nil, name, resumeError(
			domainerr.CodeValidation,
			"read resume",
			"简历文件超过 10MB："+name+"。",
			"压缩文件或按 [p] 粘贴文本。",
			false,
			nil,
		)
	}

	payload := make([]byte, 0, info.Size())
	buffer := make([]byte, 64<<10)
	var current int64
	lastPercent := -5
	for {
		if err := cancelled(ctx, name); err != nil {
			return nil, name, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			payload = append(payload, buffer[:count]...)
			current += int64(count)
			percent := int(current * 100 / info.Size())
			if percent-lastPercent >= 5 || current == info.Size() {
				progress := Progress{
					Current:    current,
					Total:      info.Size(),
					Stage:      "正在读取简历文件",
					SourceName: name,
				}
				notify(observer, async.NewStreaming(&progress))
				lastPercent = percent
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, name, readErr
		}
	}
	return payload, name, nil
}

func extractTXT(payload []byte) (string, error) {
	if !utf8.Valid(payload) {
		return "", errors.New("TXT is not UTF-8")
	}
	return string(payload), nil
}

func normalizeText(value string) (string, error) {
	value = strings.TrimPrefix(value, "\ufeff")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\x00", "")
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !blank && len(result) > 0 {
				result = append(result, "")
			}
			blank = true
			continue
		}
		result = append(result, line)
		blank = false
	}
	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	normalized := strings.Join(result, "\n")
	if strings.TrimSpace(normalized) == "" || !utf8.ValidString(normalized) {
		return "", errors.New("resume text is empty or invalid UTF-8")
	}
	return normalized, nil
}

func kindFromExtension(extension string) profile.SourceKind {
	switch strings.ToLower(extension) {
	case ".pdf":
		return profile.SourcePDF
	case ".docx":
		return profile.SourceDOCX
	case ".txt":
		return profile.SourceTXT
	default:
		return ""
	}
}

func cancelled(ctx context.Context, name string) *domainerr.Error {
	if err := ctx.Err(); err != nil {
		return resumeError(
			domainerr.CodeOperationCancelled,
			"extract resume",
			"已取消解析 "+name+"，未创建半成品画像。",
			"文件名和输入方式已保留，可重新开始或粘贴文本。",
			true,
			err,
		)
	}
	return nil
}

func resumeFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return resumeError(
		domainerr.CodeDependencyUnavailable,
		"read resume",
		"无法读取简历文件。",
		"检查路径与权限，或按 [p] 粘贴文本。",
		true,
		err,
	)
}

func resumeError(
	code domainerr.Code,
	operation string,
	message string,
	recovery string,
	retryable bool,
	cause error,
) *domainerr.Error {
	return domainerr.Wrap(
		code,
		operation,
		"resume extractor",
		message,
		recovery,
		retryable,
		cause,
	)
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}
