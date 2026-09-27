package engine

import (
	"bytes"
	"fmt"
	"io"
	"time"
)

// FrameInterval is the minimum time between redraws (20 fps).
const FrameInterval = 50 * time.Millisecond

// maxTracked bounds memory on endless streams of unique lines.
const maxTracked = 100_000

// Live is the terminal renderer. Every new group is printed once; the
// most recent Rows groups form a "live window" at the bottom of the
// screen whose counters are redrawn in place. Groups that scroll out of
// the window are frozen with their final count.
type Live struct {
	Rows int
	// Size returns the terminal size (columns, rows).
	Size func() (int, int)

	out    io.Writer
	pal    palette
	groups map[string]*Group
	rows   []*Group // the live window, oldest first
	frozen []Group  // snapshots evicted since the last frame
	drawn  int      // screen lines currently owned by the live window
	dirty  bool
	last   time.Time // last frame
	seq    int
	buf    bytes.Buffer
}

// NewLive returns a live renderer.
func NewLive(out io.Writer, rows int, color bool, size func() (int, int)) *Live {
	if rows < 1 {
		rows = 1
	}
	return &Live{Rows: rows, Size: size, out: out, pal: palette{color}, groups: make(map[string]*Group)}
}

// Line implements Renderer. Nothing is drawn here; drawing happens on
// Tick so fast streams are coalesced into at most 20 frames a second.
func (l *Live) Line(it Item, now time.Time) {
	g := l.groups[it.Key]
	if g == nil {
		if len(l.groups) >= maxTracked {
			l.prune()
		}
		l.seq++
		g = &Group{Key: it.Key, Sample: it.Line, First: now, seq: l.seq}
		l.groups[it.Key] = g
	}
	g.Count++
	g.Last = now
	if !g.inLive {
		l.push(g)
	}
	l.dirty = true
}

func (l *Live) push(g *Group) {
	g.inLive = true
	l.rows = append(l.rows, g)
	l.evict(l.Rows)
}

// evict freezes the oldest rows until at most max remain.
func (l *Live) evict(max int) {
	for len(l.rows) > max {
		old := l.rows[0]
		l.rows[0] = nil
		l.rows = l.rows[1:]
		old.inLive = false
		l.frozen = append(l.frozen, *old)
	}
}

// prune forgets groups that are no longer on the live window.
func (l *Live) prune() {
	for k, g := range l.groups {
		if !g.inLive {
			delete(l.groups, k)
		}
	}
}

// Tick redraws when something changed, or once a second so the
// "last seen" labels stay fresh.
func (l *Live) Tick(now time.Time) {
	if now.Sub(l.last) < FrameInterval {
		return
	}
	if !l.dirty && now.Unix() == l.last.Unix() {
		return
	}
	l.frame(now, false)
}

// Close draws a final frame with every row frozen.
func (l *Live) Close(now time.Time) { l.frame(now, true) }

func (l *Live) frame(now time.Time, final bool) {
	cols, lines := 80, 24
	if l.Size != nil {
		if c, r := l.Size(); c > 0 && r > 0 {
			cols, lines = c, r
		}
	}
	// Never own more lines than fit on screen, or cursor-up would clamp
	// at the top and the redraw would smear.
	l.evict(max(1, min(l.Rows, lines-1)))

	b := &l.buf
	b.Reset()
	if l.drawn > 0 {
		fmt.Fprintf(b, "\x1b[%dA\r", l.drawn)
	}
	for i := range l.frozen {
		l.row(b, &l.frozen[i], now, cols, true)
	}
	l.frozen = l.frozen[:0]
	for _, g := range l.rows {
		l.row(b, g, now, cols, final)
	}
	b.WriteString("\x1b[J") // clear anything left below
	l.drawn = len(l.rows)
	if final {
		l.drawn = 0
		for _, g := range l.rows {
			g.inLive = false
		}
		l.rows = nil
	}
	l.out.Write(b.Bytes())
	l.dirty = false
	l.last = now
}

// row writes one group as a single terminal line.
func (l *Live) row(b *bytes.Buffer, g *Group, now time.Time, cols int, frozen bool) {
	b.WriteString("\x1b[2K")
	text := Clean(g.Sample)
	if g.Count < 2 {
		b.WriteString(Truncate(text, cols-1))
		b.WriteByte('\n')
		return
	}
	badge := "×" + Commas(g.Count)
	when := Ago(now.Sub(g.Last))
	if frozen {
		when = "last " + Clock(g.Last)
	}
	suffix := "  " + badge + " · " + when
	room := cols - 1 - Width(suffix)
	if room < 8 { // very narrow terminal: drop the time
		suffix = "  " + badge
		room = cols - 1 - Width(suffix)
	}
	b.WriteString(Truncate(text, room))
	b.WriteString("  ")
	b.WriteString(l.pal.wrap(bold+lime, badge))
	if len(suffix) > len(badge)+2 {
		b.WriteString(l.pal.wrap(dim, " · "+when))
	}
	b.WriteByte('\n')
}
