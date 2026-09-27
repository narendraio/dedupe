// Package normalize turns a raw log line into a stable "fingerprint" by
// replacing the parts of a line that change on every occurrence
// (timestamps, ids, addresses, numbers, ...) with placeholders.
//
// Two lines with the same fingerprint are considered the same event.
package normalize

import (
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxFingerprintInput caps how many bytes of a line take part in the
// fingerprint. Anything past this is assumed to be payload noise.
const MaxFingerprintInput = 8 << 10

// Placeholders used in fingerprints.
const (
	TS   = "<ts>"
	UUID = "<uuid>"
	IP   = "<ip>"
	HEX  = "<hex>"
	NUM  = "#"
	STR  = `"…"`
)

type rule struct {
	re   *regexp.Regexp
	repl string
	fn   func(string) string // optional: used instead of repl
	need string              // every byte here must occur in the line, else skip (cheap prefilter)
}

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

	// A double-quoted string, or a single-quoted one that does not start
	// in the middle of a word (so apostrophes in "don't" are left alone).
	quotedRE = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|(?:^|[^\p{L}\p{N}_])'(?:[^'\\\n]|\\.)*'`)

	// Candidate IPv6 tokens; validated with net.ParseIP before replacing.
	ipv6RE = regexp.MustCompile(`\[?[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:(?:\.\d{1,3}){3}\.?\d{0,3})?\]?(?::\d{1,5})?`)

	// Order matters: the most specific patterns run first so that, for
	// example, the digits of a timestamp are not eaten by the number rule.
	rules = []rule{
		// ISO-8601 / RFC 3339 and friends: 2024-05-01T12:04:31.123Z, 2024-05-01 12:04:31,123 +0200
		{re: regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2})?(?:[.,]\d+)?(?:\s?(?:Z|[+-]\d{2}:?\d{2}|UTC)\b)?`), repl: TS, need: "-:"},
		// Apache / nginx common log format: 10/Oct/2000:13:55:36 -0700
		{re: regexp.MustCompile(`\d{2}/[A-Z][a-z]{2}/\d{4}:\d{2}:\d{2}:\d{2}(?: [+-]\d{4})?`), repl: TS, need: "/:"},
		// RFC 1123-ish: Mon, 02 Jan 2006 15:04:05 MST
		{re: regexp.MustCompile(`(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{1,2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2}(?: [A-Z]{2,5}\b| [+-]\d{4})?`), repl: TS, need: ",:"},
		// syslog: Jan  2 15:04:05
		{re: regexp.MustCompile(`\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) +\d{1,2} \d{2}:\d{2}:\d{2}(?:\.\d+)?`), repl: TS, need: ":"},
		// Go log / slashed dates: 2006/01/02 15:04:05.000000
		{re: regexp.MustCompile(`\b\d{4}/\d{2}/\d{2}(?: \d{2}:\d{2}:\d{2}(?:[.,]\d+)?)?`), repl: TS, need: "/"},
		// klog / glog: I0502 12:04:31.123456
		{re: regexp.MustCompile(`\b[IWEF]\d{4} \d{2}:\d{2}:\d{2}\.\d+`), repl: TS, need: ":"},
		// bare clock time: 12:04:31 or 12:04:31.123
		{re: regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`), repl: TS, need: ":"},
		// bare date: 2024-05-01
		{re: regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`), repl: TS, need: "-"},
		// unix epoch seconds / millis / micros / nanos (2001..2286), optional fraction
		{re: regexp.MustCompile(`\b1\d{9}(?:\d{3}|\d{6}|\d{9})?(?:\.\d+)?\b`), repl: TS, need: "1"},

		// UUIDs
		{re: regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`), repl: UUID, need: "-"},

		// IPv4 with optional CIDR and port
		{re: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2})?(?::\d{1,5})?\b`), repl: IP, need: "."},

		// 0x-prefixed hex of any length, then bare hex runs of 8+ chars that
		// contain at least one letter (commit SHAs, trace ids, pointers).
		// Pure digit runs fall through to the number rule.
		{re: regexp.MustCompile(`\b0[xX][0-9a-fA-F]+\b`), repl: HEX, need: "0"},
		{re: regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`), fn: func(s string) string {
			if strings.IndexFunc(s, isHexLetter) < 0 {
				return s
			}
			return HEX
		}},
	}
)

// keptKeys are keys whose quoted values carry the meaning of a log line.
// Their values are never replaced, even without --keep-quotes, otherwise
// every JSON / logfmt line would collapse into one group.
var keptKeys = map[string]bool{
	"msg": true, "message": true, "error": true, "err": true, "exception": true,
	"level": true, "lvl": true, "severity": true, "event": true, "reason": true,
	"logger": true, "component": true, "caller": true, "status": true,
}

// Normalizer computes fingerprints. The zero value is ready to use.
type Normalizer struct {
	// KeepQuotes disables replacing quoted strings with a placeholder.
	KeepQuotes bool
}

// Fingerprint returns the normalized form of line. Lines that differ
// only in volatile tokens produce identical fingerprints.
func (n Normalizer) Fingerprint(line string) string {
	if len(line) > MaxFingerprintInput {
		line = line[:MaxFingerprintInput]
	}
	if !utf8.ValidString(line) {
		line = strings.ToValidUTF8(line, "�")
	}
	if strings.IndexByte(line, 0x1b) >= 0 {
		line = ansiRE.ReplaceAllString(line, "")
	}
	if !n.KeepQuotes && strings.ContainsAny(line, `"'`) {
		line = replaceQuoted(line)
	}
	hasDigit := strings.ContainsAny(line, "0123456789")
	for i, r := range rules {
		if i == ipv6Slot && strings.Count(line, ":") >= 2 {
			line = replaceIPv6(line)
		}
		if r.need != "" && (!hasDigit || !containsAll(line, r.need)) {
			continue
		}
		if r.fn != nil {
			line = r.re.ReplaceAllStringFunc(line, r.fn)
		} else {
			line = r.re.ReplaceAllLiteralString(line, r.repl)
		}
	}
	return squash(line)
}

func containsAll(s, chars string) bool {
	for i := 0; i < len(chars); i++ {
		if strings.IndexByte(s, chars[i]) < 0 {
			return false
		}
	}
	return true
}

// squash replaces every remaining number (ints, decimals, the digits in
// "user42" or "12ms") with NUM and collapses whitespace runs, in one pass.
func squash(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			i++
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			if i+1 < len(s) && s[i] == '.' && s[i+1] >= '0' && s[i+1] <= '9' {
				i++
				for i < len(s) && s[i] >= '0' && s[i] <= '9' {
					i++
				}
			}
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteString(NUM)
			continue
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			space = true
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteByte(c)
		}
		i++
	}
	return b.String()
}

// ipv6Slot is the index of the rule before which IPv6 replacement runs
// (after timestamps, so "12:04:31" is gone, and before hex/number rules).
var ipv6Slot = func() int {
	for i, r := range rules {
		if r.repl == IP {
			return i
		}
	}
	return len(rules) - 1
}()

func isHexLetter(r rune) bool { return (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') }

func isWordByte(b byte) bool {
	return b == '_' || b >= 0x80 || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// replaceQuoted swaps quoted values for a placeholder, keeping JSON keys
// and the values of meaningful keys (msg, error, level, ...).
func replaceQuoted(s string) string {
	locs := quotedRE.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, l := range locs {
		start, end := l[0], l[1]
		if s[start] != '"' && s[start] != '\'' {
			// the single-quote alternative includes one leading char; skip the whole rune
			_, w := utf8.DecodeRuneInString(s[start:])
			start += w
		}
		if isKey(s, end) {
			continue
		}
		key := keyBefore(s, start)
		if keptKeys[strings.ToLower(key)] {
			continue
		}
		// A free-standing quoted phrase ("Observed a panic") is prose, not
		// a volatile value: keep it.
		if key == "" && strings.ContainsAny(s[start+1:end-1], " \t") {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(STR)
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// isKey reports whether the quoted string ending at end is followed by a
// colon, i.e. it is a JSON object key.
func isKey(s string, end int) bool {
	rest := strings.TrimLeft(s[end:], " \t")
	return strings.HasPrefix(rest, ":")
}

// keyBefore returns the key name for a value starting at start, for
// shapes like `key="v"`, `key: "v"` and `"key":"v"`.
func keyBefore(s string, start int) string {
	head := strings.TrimRight(s[:start], " \t")
	if head == "" {
		return ""
	}
	if c := head[len(head)-1]; c != '=' && c != ':' {
		return ""
	}
	head = strings.TrimRight(head[:len(head)-1], " \t")
	head = strings.TrimSuffix(head, `"`)
	i := len(head)
	for i > 0 && (isWordByte(head[i-1]) || head[i-1] == '.' || head[i-1] == '-') {
		i--
	}
	return head[i:]
}

// replaceIPv6 replaces valid IPv6 addresses (optionally bracketed and
// with a port) that stand on their own, not glued to identifiers such
// as Rust / C++ paths ("std::io").
func replaceIPv6(s string) string {
	locs := ipv6RE.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, l := range locs {
		start, end := l[0], l[1]
		if (start > 0 && isWordByte(s[start-1])) || (end < len(s) && isWordByte(s[end])) {
			continue
		}
		if !validIPv6(s[start:end]) {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(IP)
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func validIPv6(tok string) bool {
	if strings.HasPrefix(tok, "[") {
		i := strings.IndexByte(tok, ']')
		if i < 0 {
			return false
		}
		tok = tok[1:i]
	}
	ip := net.ParseIP(tok)
	return ip != nil && strings.Contains(tok, ":")
}
