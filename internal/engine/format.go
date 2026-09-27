package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ANSI styles. Lime is the accent (#d6ff3f) in truecolor.
const (
	reset = "\x1b[0m"
	dim   = "\x1b[2m"
	bold  = "\x1b[1m"
	lime  = "\x1b[38;2;214;255;63m"
)

type palette struct{ on bool }

func (p palette) wrap(style, s string) string {
	if !p.on {
		return s
	}
	return style + s + reset
}

// Clean makes a line safe to print on one terminal row: ANSI sequences
// are removed, tabs expanded, other control characters and invalid
// UTF-8 replaced.
func Clean(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	var b strings.Builder
	b.Grow(len(s))
	col := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == 0x1b { // skip CSI / OSC escape sequences
			i += escapeLen(s[i:])
			continue
		}
		i += size
		switch {
		case r == '\t':
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case unicode.IsControl(r):
			b.WriteRune('�')
			col++
		default:
			b.WriteRune(r)
			col += RuneWidth(r)
		}
	}
	return b.String()
}

// escapeLen returns the byte length of the escape sequence at the start
// of s (which begins with ESC).
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[': // CSI: parameters, intermediates, final byte @-~
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']': // OSC: terminated by BEL or ESC \
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	return 2
}

// RuneWidth approximates the number of terminal columns r occupies.
func RuneWidth(r rune) int {
	switch {
	case r == 0, unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), r == 0x200B:
		return 0
	case r >= 0x1100 && (r <= 0x115F || r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1F64F) ||
		(r >= 0x1F900 && r <= 0x1FAFF) ||
		(r >= 0x20000 && r <= 0x3FFFD)):
		return 2
	}
	return 1
}

// Width is the display width of an already-cleaned string.
func Width(s string) int {
	w := 0
	for _, r := range s {
		w += RuneWidth(r)
	}
	return w
}

// Truncate cuts a cleaned string to at most max columns, ending with an
// ellipsis when something was removed.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if Width(s) <= max {
		return s
	}
	w := 0
	for i, r := range s {
		rw := RuneWidth(r)
		if w+rw > max-1 {
			return s[:i] + "…"
		}
		w += rw
	}
	return s
}

// Ago renders a duration as a short "last seen" phrase.
func Ago(d time.Duration) string {
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	default:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
}

// Clock formats a time as HH:MM:SS.
func Clock(t time.Time) string { return t.Format("15:04:05") }

// Commas formats n with thousands separators.
func Commas(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
