package layout

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// VisibleWidth returns terminal columns after ignoring ANSI control sequences.
// It treats combining marks as zero-width and East Asian wide runes as two
// columns instead of assuming one rune always occupies one column.
func VisibleWidth(value string) int {
	width := 0
	for _, cluster := range clusters(StripANSI(value)) {
		width += cluster.width
	}
	return width
}

// StripANSI removes CSI escape sequences used by the semantic theme.
func StripANSI(value string) string {
	var output strings.Builder
	for index := 0; index < len(value); {
		if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '[' {
			index += 2
			for index < len(value) {
				current := value[index]
				index++
				if current >= 0x40 && current <= 0x7e {
					break
				}
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		output.WriteRune(r)
		index += size
	}
	return output.String()
}

// ClipRight clips a value to width terminal columns.
func ClipRight(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if VisibleWidth(value) <= width {
		return value
	}
	if strings.Contains(value, "\x1b[") {
		// A truncated styled fragment must never leak a partial escape sequence.
		// Dropping styling preserves the complete, readable content.
		value = StripANSI(value)
	}
	var output strings.Builder
	used := 0
	for _, cluster := range clusters(value) {
		if used+cluster.width > width {
			break
		}
		output.WriteString(cluster.text)
		used += cluster.width
	}
	return output.String()
}

// TruncateRight preserves the beginning of labels and appends a visible
// truncation marker instead of silently clipping content.
func TruncateRight(value string, width int, ascii bool) string {
	if width <= 0 {
		return ""
	}
	if VisibleWidth(value) <= width {
		return value
	}
	marker := "…"
	if ascii {
		marker = "..."
	}
	markerWidth := VisibleWidth(marker)
	if markerWidth >= width {
		return ClipRight(marker, width)
	}
	return ClipRight(value, width-markerWidth) + marker
}

// TruncateLeft preserves the useful tail of long paths and model names.
func TruncateLeft(value string, width int, ascii bool) string {
	if width <= 0 {
		return ""
	}
	if VisibleWidth(value) <= width {
		return value
	}
	marker := "…"
	if ascii {
		marker = "..."
	}
	markerWidth := VisibleWidth(marker)
	if markerWidth >= width {
		return ClipRight(marker, width)
	}

	all := clusters(value)
	keptWidth := 0
	start := len(all)
	for start > 0 {
		next := all[start-1]
		if keptWidth+next.width > width-markerWidth {
			break
		}
		start--
		keptWidth += next.width
	}
	var output strings.Builder
	output.WriteString(marker)
	for _, cluster := range all[start:] {
		output.WriteString(cluster.text)
	}
	return output.String()
}

// Fit clips and right-pads a value to exactly width visible columns.
func Fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = ClipRight(value, width)
	return value + strings.Repeat(" ", width-VisibleWidth(value))
}

// Wrap wraps text without breaking a wide rune across the target width.
func Wrap(value string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	for _, sourceLine := range strings.Split(value, "\n") {
		if sourceLine == "" {
			lines = append(lines, "")
			continue
		}
		var line strings.Builder
		lineWidth := 0
		for _, cluster := range clusters(sourceLine) {
			if lineWidth > 0 && lineWidth+cluster.width > width {
				lines = append(lines, line.String())
				line.Reset()
				lineWidth = 0
			}
			if cluster.width > width {
				continue
			}
			line.WriteString(cluster.text)
			lineWidth += cluster.width
		}
		lines = append(lines, line.String())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

type textCluster struct {
	text  string
	width int
}

func clusters(value string) []textCluster {
	result := make([]textCluster, 0, len(value))
	for _, current := range value {
		width := runeWidth(current)
		if width == 0 && len(result) > 0 {
			result[len(result)-1].text += string(current)
			continue
		}
		result = append(result, textCluster{text: string(current), width: width})
	}
	return result
}

func runeWidth(current rune) int {
	switch {
	case current == '\t':
		return 4
	case current == '\n' || current == '\r':
		return 0
	case unicode.IsControl(current):
		return 0
	case unicode.Is(unicode.Mn, current),
		unicode.Is(unicode.Me, current),
		unicode.Is(unicode.Cf, current):
		return 0
	case isWide(current):
		return 2
	default:
		return 1
	}
}

// isWide follows the stable East Asian and emoji ranges relevant to terminal
// UIs. Ambiguous-width punctuation intentionally remains one column.
func isWide(current rune) bool {
	return current >= 0x1100 && (current <= 0x115f ||
		current == 0x2329 ||
		current == 0x232a ||
		(current >= 0x2e80 && current <= 0xa4cf && current != 0x303f) ||
		(current >= 0xac00 && current <= 0xd7a3) ||
		(current >= 0xf900 && current <= 0xfaff) ||
		(current >= 0xfe10 && current <= 0xfe19) ||
		(current >= 0xfe30 && current <= 0xfe6f) ||
		(current >= 0xff00 && current <= 0xff60) ||
		(current >= 0xffe0 && current <= 0xffe6) ||
		(current >= 0x1f300 && current <= 0x1faff) ||
		(current >= 0x20000 && current <= 0x3fffd))
}
