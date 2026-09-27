package engine

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"time"
)

// Pipe is the plain-stream renderer used when stdout is not a terminal
// (or with --no-live). The first line of every group is written
// immediately; once a group has been quiet for Window (or at EOF) a
// single "↳ repeated N more times" line is written for it.
type Pipe struct {
	Window time.Duration

	w       *bufio.Writer
	pal     palette
	active  map[string]*Group
	seq     int
	lastOut *Group // group whose first line is the most recent output
}

// NewPipe returns a Pipe renderer writing to w.
func NewPipe(w io.Writer, window time.Duration, color bool) *Pipe {
	return &Pipe{
		Window: window,
		w:      bufio.NewWriterSize(w, 64<<10),
		pal:    palette{color},
		active: make(map[string]*Group),
	}
}

// Line implements Renderer.
func (p *Pipe) Line(it Item, now time.Time) {
	if g := p.active[it.Key]; g != nil {
		g.Count++
		g.Last = now
		return
	}
	p.seq++
	g := &Group{Key: it.Key, Sample: it.Line, Count: 1, First: now, Last: now, seq: p.seq}
	p.active[it.Key] = g
	p.w.WriteString(it.Line)
	p.w.WriteByte('\n')
	p.lastOut = g
}

// Tick ends every group that has been quiet for at least Window.
func (p *Pipe) Tick(now time.Time) {
	var done []*Group
	for _, g := range p.active {
		if now.Sub(g.Last) >= p.Window {
			done = append(done, g)
		}
	}
	p.end(done)
	p.w.Flush()
}

// Flush writes buffered output.
func (p *Pipe) Flush() { p.w.Flush() }

// Close ends all groups and flushes.
func (p *Pipe) Close(time.Time) {
	done := make([]*Group, 0, len(p.active))
	for _, g := range p.active {
		done = append(done, g)
	}
	p.end(done)
	p.w.Flush()
}

func (p *Pipe) end(groups []*Group) {
	sort.Slice(groups, func(i, j int) bool { return groups[i].seq < groups[j].seq })
	for _, g := range groups {
		delete(p.active, g.Key)
		if g.Count < 2 {
			continue
		}
		more := g.Count - 1
		line := fmt.Sprintf("  %s repeated %s more %s %s",
			p.pal.wrap(dim, "↳"),
			p.pal.wrap(bold+lime, Commas(more)),
			plural(more, "time", "times"),
			p.pal.wrap(dim, "(last "+Clock(g.Last)+")"))
		if p.lastOut != g {
			// Not directly under its first line: say which group this is.
			line += p.pal.wrap(dim, " · "+Truncate(Clean(g.Sample), 72))
		}
		p.w.WriteString(line)
		p.w.WriteByte('\n')
		p.lastOut = nil
	}
}
