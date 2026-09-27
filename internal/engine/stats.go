package engine

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// Stats accumulates totals for the end-of-run summary.
type Stats struct {
	Lines  int
	groups map[string]*Group
	seen   int  // distinct fingerprints ever seen
	pruned bool // true once rare groups were dropped to bound memory
}

// NewStats returns an empty Stats.
func NewStats() *Stats { return &Stats{groups: make(map[string]*Group)} }

// Add records one line.
func (s *Stats) Add(it Item, now time.Time) {
	s.Lines++
	g := s.groups[it.Key]
	if g == nil {
		if len(s.groups) >= maxTracked {
			for k, g := range s.groups {
				if g.Count == 1 {
					delete(s.groups, k)
				}
			}
			s.pruned = true
		}
		s.seen++
		g = &Group{Key: it.Key, Sample: it.Line, First: now, seq: s.seen}
		s.groups[it.Key] = g
	}
	g.Count++
	g.Last = now
}

// Groups returns the number of distinct fingerprints seen.
func (s *Stats) Groups() int { return s.seen }

// Top returns the n largest groups, biggest first (ties: first seen).
func (s *Stats) Top(n int) []*Group {
	all := make([]*Group, 0, len(s.groups))
	for _, g := range s.groups {
		all = append(all, g)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		return all[i].seq < all[j].seq
	})
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// WriteSummary prints the top groups as a small table.
func (s *Stats) WriteSummary(w io.Writer, top int, color bool, cols int) {
	p := palette{color}
	if cols <= 0 {
		cols = 100
	}
	groups := s.Groups()
	quieter := 0.0
	if s.Lines > 0 {
		// floor, so a few distinct groups never read as "100.0%"
		quieter = math.Floor(1000*(1-float64(groups)/float64(s.Lines))) / 10
	}
	approx := ""
	if s.pruned {
		approx = "~"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s  %s %s → %s%s %s  %s\n\n",
		p.wrap(bold+lime, "dedupe"),
		Commas(s.Lines), plural(s.Lines, "line", "lines"),
		approx, Commas(groups), plural(groups, "group", "groups"),
		p.wrap(dim, fmt.Sprintf("· %.1f%% quieter", quieter)))

	rows := s.Top(top)
	if len(rows) == 0 {
		b.WriteString("  (no input)\n\n")
		io.WriteString(w, b.String())
		return
	}
	cw := len("COUNT")
	for _, g := range rows {
		cw = max(cw, len(Commas(g.Count))+1)
	}
	fmt.Fprintf(&b, "  %s\n", p.wrap(dim, fmt.Sprintf("%*s  %-8s  %s", cw, "COUNT", "LAST", "LINE")))
	room := cols - 2 - cw - 2 - 8 - 2 - 1
	for _, g := range rows {
		count := fmt.Sprintf("%*s", cw, "×"+Commas(g.Count))
		fmt.Fprintf(&b, "  %s  %s  %s\n",
			p.wrap(bold+lime, count), p.wrap(dim, Clock(g.Last)), Truncate(Clean(g.Sample), max(room, 20)))
	}
	b.WriteByte('\n')
	io.WriteString(w, b.String())
}
