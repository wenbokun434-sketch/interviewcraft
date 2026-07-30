package resume

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var pdfStreamPattern = regexp.MustCompile(
	`(?s)stream(?:\r\n|\n|\r)(.*?)(?:\r\n|\n|\r)endstream`,
)

func extractPDF(payload []byte) (string, error) {
	if !bytes.HasPrefix(payload, []byte("%PDF-")) {
		return "", errors.New("invalid PDF header")
	}
	if bytes.Contains(payload, []byte("/Encrypt")) {
		return "", errors.New("encrypted PDF is not supported")
	}
	matches := pdfStreamPattern.FindAllSubmatchIndex(payload, -1)
	if len(matches) == 0 {
		return "", errors.New("PDF has no readable content streams")
	}
	var result strings.Builder
	for _, match := range matches {
		start := match[2]
		end := match[3]
		stream := payload[start:end]
		dictionaryStart := max(0, match[0]-1024)
		dictionary := payload[dictionaryStart:match[0]]
		if bytes.Contains(dictionary, []byte("/FlateDecode")) {
			reader, err := zlib.NewReader(bytes.NewReader(stream))
			if err != nil {
				continue
			}
			decoded, readErr := io.ReadAll(io.LimitReader(reader, MaxFileBytes+1))
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil ||
				len(decoded) > int(MaxFileBytes) {
				continue
			}
			stream = decoded
		}
		text := pdfTextFromContent(stream)
		if strings.TrimSpace(text) != "" {
			if result.Len() > 0 {
				result.WriteByte('\n')
			}
			result.WriteString(text)
		}
	}
	if strings.TrimSpace(result.String()) == "" {
		return "", errors.New("PDF contains no extractable text")
	}
	return result.String(), nil
}

func pdfTextFromContent(content []byte) string {
	var result strings.Builder
	for offset := 0; offset < len(content); {
		begin := findPDFToken(content, offset, "BT")
		if begin < 0 {
			break
		}
		end := findPDFToken(content, begin+2, "ET")
		if end < 0 {
			end = len(content)
		}
		extractPDFTextObjects(&result, content[begin+2:end])
		offset = min(len(content), end+2)
	}
	return strings.TrimSpace(result.String())
}

func extractPDFTextObjects(result *strings.Builder, content []byte) {
	for index := 0; index < len(content); {
		switch content[index] {
		case '(':
			value, next, ok := parsePDFLiteral(content, index)
			if ok {
				appendPDFText(result, decodePDFBytes(value))
				index = next
				continue
			}
		case '<':
			if index+1 < len(content) && content[index+1] != '<' {
				end := bytes.IndexByte(content[index+1:], '>')
				if end >= 0 {
					raw := bytes.Map(
						func(current rune) rune {
							if current == ' ' || current == '\n' ||
								current == '\r' || current == '\t' {
								return -1
							}
							return current
						},
						content[index+1:index+1+end],
					)
					if len(raw)%2 != 0 {
						raw = append(raw, '0')
					}
					decoded := make([]byte, hex.DecodedLen(len(raw)))
					if _, err := hex.Decode(decoded, raw); err == nil {
						appendPDFText(result, decodePDFBytes(decoded))
					}
					index += end + 2
					continue
				}
			}
		}
		index++
	}
}

func parsePDFLiteral(
	content []byte,
	start int,
) ([]byte, int, bool) {
	var result []byte
	depth := 1
	for index := start + 1; index < len(content); index++ {
		current := content[index]
		if current == '\\' {
			if index+1 >= len(content) {
				break
			}
			index++
			escaped := content[index]
			switch escaped {
			case 'n':
				result = append(result, '\n')
			case 'r':
				result = append(result, '\r')
			case 't':
				result = append(result, '\t')
			case 'b':
				result = append(result, '\b')
			case 'f':
				result = append(result, '\f')
			case '\n':
			case '\r':
				if index+1 < len(content) && content[index+1] == '\n' {
					index++
				}
			default:
				if escaped >= '0' && escaped <= '7' {
					digits := []byte{escaped}
					for len(digits) < 3 && index+1 < len(content) &&
						content[index+1] >= '0' && content[index+1] <= '7' {
						index++
						digits = append(digits, content[index])
					}
					value, _ := strconv.ParseUint(string(digits), 8, 8)
					result = append(result, byte(value))
				} else {
					result = append(result, escaped)
				}
			}
			continue
		}
		switch current {
		case '(':
			depth++
			result = append(result, current)
		case ')':
			depth--
			if depth == 0 {
				return result, index + 1, true
			}
			result = append(result, current)
		default:
			result = append(result, current)
		}
	}
	return nil, start + 1, false
}

func decodePDFBytes(value []byte) string {
	if len(value) >= 2 && value[0] == 0xfe && value[1] == 0xff {
		words := make([]uint16, 0, (len(value)-2)/2)
		for index := 2; index+1 < len(value); index += 2 {
			words = append(words, uint16(value[index])<<8|uint16(value[index+1]))
		}
		return string(utf16.Decode(words))
	}
	if utf8.Valid(value) {
		return string(value)
	}
	runes := make([]rune, len(value))
	for index, current := range value {
		runes[index] = rune(current)
	}
	return string(runes)
}

func appendPDFText(result *strings.Builder, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if result.Len() > 0 {
		result.WriteByte(' ')
	}
	result.WriteString(value)
}

func findPDFToken(content []byte, offset int, token string) int {
	for {
		index := bytes.Index(content[offset:], []byte(token))
		if index < 0 {
			return -1
		}
		index += offset
		before := index == 0 || isPDFDelimiter(content[index-1])
		afterIndex := index + len(token)
		after := afterIndex >= len(content) || isPDFDelimiter(content[afterIndex])
		if before && after {
			return index
		}
		offset = index + len(token)
		if offset >= len(content) {
			return -1
		}
	}
}

func isPDFDelimiter(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' ||
		value == '\n' || value == '[' || value == ']' ||
		value == '<' || value == '>' || value == '(' || value == ')'
}
