// Package theme defines the semantic terminal palette and capability switches.
// Rendering code depends on roles instead of feature-local color values.
package theme

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Mode selects the terminal-aware palette.
type Mode string

const (
	Auto  Mode = "auto"
	Dark  Mode = "dark"
	Light Mode = "light"
)

// ColorMode selects the escape sequence capability of the terminal.
type ColorMode string

const (
	TrueColor ColorMode = "truecolor"
	ANSI16    ColorMode = "ansi16"
	NoColor   ColorMode = "none"
)

// Role identifies a semantic visual purpose.
type Role string

const (
	Canvas  Role = "bg.canvas"
	Panel   Role = "bg.panel"
	Primary Role = "fg.primary"
	Muted   Role = "fg.muted"
	Rule    Role = "line.rule"
	Focus   Role = "state.focus"
	Info    Role = "state.info"
	Success Role = "state.success"
	Warning Role = "state.warning"
	Error   Role = "state.error"
	Coach   Role = "state.coach"
)

// Color stores a true-color value and its ANSI-16 fallback.
type Color struct {
	Hex       string
	ANSI      int
	IsDefault bool
}

// Options are the user-selectable terminal rendering capabilities.
type Options struct {
	Mode         Mode
	ColorMode    ColorMode
	UseASCII     bool
	ReduceMotion bool
}

// Glyphs contains every non-content symbol used by foundation components.
type Glyphs struct {
	TopLeft     string
	Horizontal  string
	TopRight    string
	Vertical    string
	BottomLeft  string
	BottomRight string
	TeeLeft     string
	TeeRight    string
	Cursor      string
	Success     string
	Warning     string
	Error       string
	Info        string
	Activity    string
	Stream      string
}

// Theme is the resolved semantic palette and terminal capability set.
type Theme struct {
	Mode         Mode
	ColorMode    ColorMode
	UseASCII     bool
	ReduceMotion bool

	Canvas  Color
	Panel   Color
	Primary Color
	Muted   Color
	Rule    Color
	Focus   Color
	Info    Color
	Success Color
	Warning Color
	Error   Color
	Coach   Color

	Glyphs Glyphs
}

// Resolve validates options and creates a complete semantic theme.
func Resolve(options Options) (Theme, error) {
	if options.Mode == "" {
		options.Mode = Auto
	}
	if options.ColorMode == "" {
		options.ColorMode = TrueColor
	}
	if options.Mode != Auto && options.Mode != Dark && options.Mode != Light {
		return Theme{}, fmt.Errorf("unsupported theme %q", options.Mode)
	}
	if options.ColorMode != TrueColor &&
		options.ColorMode != ANSI16 &&
		options.ColorMode != NoColor {
		return Theme{}, fmt.Errorf("unsupported color mode %q", options.ColorMode)
	}

	resolved := darkTheme()
	switch options.Mode {
	case Auto:
		resolved.Mode = Auto
		resolved.Canvas.IsDefault = true
		resolved.Panel.IsDefault = true
	case Dark:
		resolved.Mode = Dark
	case Light:
		resolved = lightTheme()
	}
	resolved.ColorMode = options.ColorMode
	resolved.UseASCII = options.UseASCII
	resolved.ReduceMotion = options.ReduceMotion
	resolved.Glyphs = glyphs(options.UseASCII)
	return resolved, nil
}

// ParseOptions recognizes the foundation rendering flags without coupling them
// to the command package before the run command is implemented.
func ParseOptions(args []string) (Options, error) {
	options := Options{Mode: Auto, ColorMode: TrueColor}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--ascii":
			options.UseASCII = true
		case arg == "--reduce-motion":
			options.ReduceMotion = true
		case arg == "--ansi-16":
			options.ColorMode = ANSI16
		case arg == "--no-color":
			options.ColorMode = NoColor
		case arg == "--theme":
			if index+1 >= len(args) {
				return Options{}, errors.New("--theme requires auto, dark, or light")
			}
			index++
			options.Mode = Mode(args[index])
		case strings.HasPrefix(arg, "--theme="):
			options.Mode = Mode(strings.TrimPrefix(arg, "--theme="))
		default:
			return Options{}, fmt.Errorf("unknown TUI option %q", arg)
		}
	}
	if _, err := Resolve(options); err != nil {
		return Options{}, err
	}
	return options, nil
}

// Paint applies one semantic foreground role. No-color snapshots receive the
// original text without escape sequences.
func (theme Theme) Paint(role Role, text string) string {
	if text == "" || theme.ColorMode == NoColor {
		return text
	}
	color, ok := theme.color(role)
	if !ok || color.IsDefault {
		return text
	}

	var sequence string
	switch theme.ColorMode {
	case ANSI16:
		sequence = strconv.Itoa(color.ANSI)
	case TrueColor:
		red, green, blue, err := parseHex(color.Hex)
		if err != nil {
			return text
		}
		channel := 38
		if role == Canvas || role == Panel {
			channel = 48
		}
		sequence = fmt.Sprintf("%d;2;%d;%d;%d", channel, red, green, blue)
	default:
		return text
	}
	return "\x1b[" + sequence + "m" + text + "\x1b[0m"
}

// Inverse renders a focused value with both a non-color marker supplied by the
// component and inverse video when color output is enabled.
func (theme Theme) Inverse(text string) string {
	if text == "" || theme.ColorMode == NoColor {
		return text
	}
	return "\x1b[7m" + text + "\x1b[0m"
}

func (theme Theme) color(role Role) (Color, bool) {
	switch role {
	case Canvas:
		return theme.Canvas, true
	case Panel:
		return theme.Panel, true
	case Primary:
		return theme.Primary, true
	case Muted:
		return theme.Muted, true
	case Rule:
		return theme.Rule, true
	case Focus:
		return theme.Focus, true
	case Info:
		return theme.Info, true
	case Success:
		return theme.Success, true
	case Warning:
		return theme.Warning, true
	case Error:
		return theme.Error, true
	case Coach:
		return theme.Coach, true
	default:
		return Color{}, false
	}
}

func darkTheme() Theme {
	return Theme{
		Canvas:  Color{Hex: "#10110E", ANSI: 40},
		Panel:   Color{Hex: "#181A15", ANSI: 40},
		Primary: Color{Hex: "#E8E7DF", ANSI: 97},
		Muted:   Color{Hex: "#A3A69C", ANSI: 90},
		Rule:    Color{Hex: "#3C4035", ANSI: 90},
		Focus:   Color{Hex: "#D7FF54", ANSI: 92},
		Info:    Color{Hex: "#77DDF5", ANSI: 96},
		Success: Color{Hex: "#9EE493", ANSI: 32},
		Warning: Color{Hex: "#FFC857", ANSI: 33},
		Error:   Color{Hex: "#FF6B5B", ANSI: 91},
		Coach:   Color{Hex: "#C7B7FF", ANSI: 95},
	}
}

func lightTheme() Theme {
	return Theme{
		Mode:    Light,
		Canvas:  Color{Hex: "#F2F1EA", ANSI: 47},
		Panel:   Color{Hex: "#E7E5DC", ANSI: 47},
		Primary: Color{Hex: "#20221C", ANSI: 30},
		Muted:   Color{Hex: "#73776A", ANSI: 90},
		Rule:    Color{Hex: "#B9BBAF", ANSI: 90},
		Focus:   Color{Hex: "#526600", ANSI: 32},
		Info:    Color{Hex: "#006B80", ANSI: 36},
		Success: Color{Hex: "#327A37", ANSI: 32},
		Warning: Color{Hex: "#8A5700", ANSI: 33},
		Error:   Color{Hex: "#B42318", ANSI: 31},
		Coach:   Color{Hex: "#6550A3", ANSI: 35},
	}
}

func glyphs(ascii bool) Glyphs {
	if ascii {
		return Glyphs{
			TopLeft:     "+",
			Horizontal:  "-",
			TopRight:    "+",
			Vertical:    "|",
			BottomLeft:  "+",
			BottomRight: "+",
			TeeLeft:     "+",
			TeeRight:    "+",
			Cursor:      ">",
			Success:     "ok",
			Warning:     "!",
			Error:       "!",
			Info:        "i",
			Activity:    ".",
			Stream:      "|",
		}
	}
	return Glyphs{
		TopLeft:     "┌",
		Horizontal:  "─",
		TopRight:    "┐",
		Vertical:    "│",
		BottomLeft:  "└",
		BottomRight: "┘",
		TeeLeft:     "├",
		TeeRight:    "┤",
		Cursor:      "›",
		Success:     "✓",
		Warning:     "!",
		Error:       "!",
		Info:        "i",
		Activity:    "·",
		Stream:      "▌",
	}
}

func parseHex(value string) (int64, int64, int64, error) {
	if len(value) != 7 || value[0] != '#' {
		return 0, 0, 0, fmt.Errorf("invalid color %q", value)
	}
	red, err := strconv.ParseInt(value[1:3], 16, 16)
	if err != nil {
		return 0, 0, 0, err
	}
	green, err := strconv.ParseInt(value[3:5], 16, 16)
	if err != nil {
		return 0, 0, 0, err
	}
	blue, err := strconv.ParseInt(value[5:7], 16, 16)
	if err != nil {
		return 0, 0, 0, err
	}
	return red, green, blue, nil
}
