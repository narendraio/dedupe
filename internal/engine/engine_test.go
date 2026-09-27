package engine

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/narendraio/dedupe/internal/normalize"
)

var t0 = time.Date(2026, 9, 27, 12, 4, 0, 0, time.Local)

func items(lines ...string) []Item {
	var n normalize.Normalizer
	out := make([]Item, len(lines))
	for i, l := range lines {
		out[i] = Item{Line: l, Key: n.Fingerprint(l)}
	}
	return out
}

func TestReadLine(t *testing.T) {
	long := strings.Repeat("a", MaxLine+100)
	in := "one\r\ntwo\n\nlast-no-newline"
	in = long + "\n" + in
	br := bufio.NewReaderSize(strings.NewReader(in), 16)
	var got []string
	for {
		l, err := ReadLine(br)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}
	if len(got) != 5 {
		t.Fatalf("got %d lines: %q", len(got), got)
	}
	if want := strings.Repeat("a", MaxLine) + " …[+100 bytes]"; got[0] != want {
		t.Errorf("long line: got %d bytes, suffix %q", len(got[0]), got[0][len(got[0])-20:])
	}
	for i, want := range []string{"one", "two", "", "last-no-newline"} {
		if got[i+1] != want {
			t.Errorf("line %d: got %q want %q", i+1, got[i+1], want)
		}
	}
}

func TestReadKeepsOrderAcrossParallelBatches(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 10_000; i++ {
		fmt.Fprintf(&buf, "line %d id=%x\n", i, i*7919)
	}
	out := make(chan []Item, 8)
	errc := make(chan error, 1)
	go func() { errc <- Read([]io.Reader{&buf}, normalize.Normalizer{}, out) }()
	n := 0
	for batch := range out {
		for _, it := range batch {
			if want := fmt.Sprintf("line %d ", n); !strings.HasPrefix(it.Line, want) {
				t.Fatalf("out of order at %d: %q", n, it.Line)
			}
			if !strings.HasPrefix(it.Key, "line # id=") {
				t.Fatalf("bad key %q", it.Key)
			}
			n++
		}
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if n != 10_000 {
		t.Fatalf("got %d lines", n)
	}
}

func TestPipeGroupsAndSummaries(t *testing.T) {
	var out bytes.Buffer
	p := NewPipe(&out, 5*time.Second, false)
	in := items(
		"2026-09-27T12:04:00Z ERROR db timeout after 3001ms",
		"2026-09-27T12:04:01Z ERROR db timeout after 3017ms",
		"2026-09-27T12:04:02Z ERROR db timeout after 2999ms",
		"2026-09-27T12:04:03Z INFO request ok",
	)
	for i, it := range in {
		p.Line(it, t0.Add(time.Duration(i)*time.Second))
	}
	p.Tick(t0.Add(4 * time.Second)) // nothing quiet for 5s yet
	p.Flush()
	want := "2026-09-27T12:04:00Z ERROR db timeout after 3001ms\n2026-09-27T12:04:03Z INFO request ok\n"
	if out.String() != want {
		t.Fatalf("before window:\n%s", out.String())
	}
	// ERROR group last seen at +2s; at +7s it has been quiet for 5s.
	p.Tick(t0.Add(7 * time.Second))
	want += "  ↳ repeated 2 more times (last 12:04:02) · 2026-09-27T12:04:00Z ERROR db timeout after 3001ms\n"
	if out.String() != want {
		t.Fatalf("after window:\n%s\nwant:\n%s", out.String(), want)
	}
	// The same event after its group ended starts a new group.
	p.Line(items("2026-09-27T12:05:00Z ERROR db timeout after 1ms")[0], t0.Add(60*time.Second))
	p.Line(items("2026-09-27T12:05:00Z ERROR db timeout after 2ms")[0], t0.Add(61*time.Second))
	p.Close(t0.Add(62 * time.Second))
	want += "2026-09-27T12:05:00Z ERROR db timeout after 1ms\n  ↳ repeated 1 more time (last 12:05:01)\n"
	if out.String() != want {
		t.Fatalf("after close:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestPipeCloseOrder(t *testing.T) {
	var out bytes.Buffer
	p := NewPipe(&out, time.Hour, false)
	for i, it := range items("a 1", "b 1", "a 2", "c 1", "b 2", "b 3") {
		p.Line(it, t0.Add(time.Duration(i)*time.Second))
	}
	p.Close(t0)
	want := "a 1\nb 1\nc 1\n" +
		"  ↳ repeated 1 more time (last 12:04:02) · a 1\n" +
		"  ↳ repeated 2 more times (last 12:04:05) · b 1\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestPipeColor(t *testing.T) {
	var out bytes.Buffer
	p := NewPipe(&out, time.Second, true)
	for _, it := range items("x 1", "x 2") {
		p.Line(it, t0)
	}
	p.Close(t0)
	if !strings.Contains(out.String(), lime) {
		t.Fatalf("expected color codes: %q", out.String())
	}
}

func TestLiveWindowAndFreeze(t *testing.T) {
	var out bytes.Buffer
	l := NewLive(&out, 2, false, func() (int, int) { return 60, 20 })
	now := t0
	for _, it := range items("A 1", "A 2", "A 3") {
		l.Line(it, now)
	}
	l.Tick(now.Add(time.Second))
	if s := out.String(); !strings.Contains(s, "A 1  ×3 · 1s ago\n") {
		t.Fatalf("frame 1: %q", s)
	}
	out.Reset()
	// Two new groups push A out of the 2-row window: it is frozen.
	for _, it := range items("B", "C") {
		l.Line(it, now.Add(2*time.Second))
	}
	l.Tick(now.Add(3 * time.Second))
	s := out.String()
	if !strings.HasPrefix(s, "\x1b[1A\r") {
		t.Fatalf("expected cursor-up over the 1 drawn row, got %q", s)
	}
	if !strings.Contains(s, "A 1  ×3 · last 12:04:00\n") || !strings.Contains(s, "B\n") || !strings.Contains(s, "C\n") {
		t.Fatalf("frame 2: %q", s)
	}
	// A repeat of a frozen group brings it back with its running total.
	out.Reset()
	l.Line(items("A 99")[0], now.Add(4*time.Second))
	l.Close(now.Add(4 * time.Second))
	s = out.String()
	if !strings.HasPrefix(s, "\x1b[2A\r") || !strings.Contains(s, "A 1  ×4 · last 12:04:04\n") {
		t.Fatalf("final: %q", s)
	}
}

func TestLiveThrottle(t *testing.T) {
	var out bytes.Buffer
	l := NewLive(&out, 5, false, nil)
	l.Line(items("x")[0], t0)
	l.Tick(t0)
	n := out.Len()
	l.Line(items("y")[0], t0.Add(10*time.Millisecond))
	l.Tick(t0.Add(10 * time.Millisecond)) // within 50ms: no frame
	if out.Len() != n {
		t.Fatal("redrew faster than the frame interval")
	}
	l.Tick(t0.Add(60 * time.Millisecond))
	if out.Len() == n {
		t.Fatal("expected a frame after the interval")
	}
}

func TestLiveTruncatesToWidth(t *testing.T) {
	var out bytes.Buffer
	l := NewLive(&out, 5, false, func() (int, int) { return 30, 10 })
	for _, it := range items(strings.Repeat("word ", 40)+"1", strings.Repeat("word ", 40)+"2") {
		l.Line(it, t0)
	}
	l.Close(t0)
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimPrefix(line, "\x1b[2K")
		if i := strings.LastIndex(line, "\x1b[2K"); i >= 0 {
			line = line[i+4:]
		}
		if w := Width(line); w > 29 {
			t.Fatalf("row wider than terminal (%d): %q", w, line)
		}
	}
}

func TestStatsTop(t *testing.T) {
	s := NewStats()
	for _, it := range items("a 1", "b 1", "b 2", "c", "b 3", "a 2") {
		s.Add(it, t0)
	}
	top := s.Top(2)
	if s.Lines != 6 || s.Groups() != 3 || len(top) != 2 || top[0].Sample != "b 1" || top[0].Count != 3 || top[1].Sample != "a 1" {
		t.Fatalf("unexpected stats: lines=%d groups=%d top=%+v", s.Lines, s.Groups(), top)
	}
	var buf bytes.Buffer
	s.WriteSummary(&buf, 10, false, 80)
	for _, want := range []string{"6 lines → 3 groups", "50.0% quieter", "×3  12:04:00  b 1"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("summary missing %q:\n%s", want, buf.String())
		}
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := Clean("a\tb\x1b[31mred\x1b[0m\x07\xff"); got != "a       bred\uFFFD\uFFFD" {
		t.Errorf("Clean: %q", got)
	}
	if got := Truncate("hello world", 8); got != "hello w…" {
		t.Errorf("Truncate: %q", got)
	}
	if got := Truncate("日本語日本語", 5); got != "日本…" {
		t.Errorf("Truncate wide: %q", got)
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567"} {
		if got := Commas(n); got != want {
			t.Errorf("Commas(%d)=%q", n, got)
		}
	}
	for d, want := range map[time.Duration]string{0: "just now", 2 * time.Second: "2s ago", 3 * time.Minute: "3m ago", 2 * time.Hour: "2h ago"} {
		if got := Ago(d); got != want {
			t.Errorf("Ago(%v)=%q", d, got)
		}
	}
}
