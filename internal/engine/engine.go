// Package engine groups log lines by fingerprint and renders the result,
// either as a live-updating terminal view or as a plain line stream.
package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"github.com/narendraio/dedupe/internal/normalize"
)

// MaxLine is the longest line kept in memory. Longer lines are cut and
// marked with the number of dropped bytes.
const MaxLine = 256 << 10

// Group is a set of lines sharing one fingerprint.
type Group struct {
	Key    string    // fingerprint
	Sample string    // first line seen, verbatim
	Count  int       // occurrences
	First  time.Time // first seen
	Last   time.Time // last seen

	seq    int
	inLive bool
}

// Item is one input line with its fingerprint.
type Item struct {
	Line string
	Key  string
}

// Renderer consumes lines and produces output.
type Renderer interface {
	// Line handles one input line.
	Line(it Item, now time.Time)
	// Tick is called periodically so the renderer can flush or redraw.
	Tick(now time.Time)
	// Close flushes everything; the renderer must not be used afterwards.
	Close(now time.Time)
}

// Read streams lines from each reader in order, fingerprints them (in
// parallel for big batches) and sends them to out in input order. It
// closes out when done.
func Read(inputs []io.Reader, norm normalize.Normalizer, out chan<- []Item) error {
	defer close(out)
	for _, r := range inputs {
		br := bufio.NewReaderSize(r, 64<<10)
		var batch []Item
		for {
			line, err := ReadLine(br)
			if err != nil {
				if len(batch) > 0 {
					fingerprint(batch, norm)
					out <- batch
				}
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
			batch = append(batch, Item{Line: line})
			// Ship the batch when it's big, or when the next read would
			// block (so live streams stay live).
			if len(batch) >= 2048 || br.Buffered() == 0 {
				fingerprint(batch, norm)
				out <- batch
				batch = nil
			}
		}
	}
	return nil
}

// ReadLine reads one line of any length, without the trailing newline
// (\n or \r\n). Lines longer than MaxLine are truncated with a marker.
func ReadLine(br *bufio.Reader) (string, error) {
	var buf, last []byte
	dropped := 0
	for {
		frag, err := br.ReadSlice('\n')
		last = frag
		if room := MaxLine - len(buf); len(frag) <= room {
			buf = append(buf, frag...)
		} else {
			buf = append(buf, frag[:room]...)
			dropped += len(frag) - room
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && len(buf) == 0 && dropped == 0 {
			return "", err
		}
		break
	}
	n := len(buf)
	if l := len(last); l > 0 && last[l-1] == '\n' {
		if dropped > 0 { // the line ending was cut off with the tail
			dropped--
			if l > 1 && last[l-2] == '\r' {
				dropped--
			}
		} else {
			n--
			if n > 0 && buf[n-1] == '\r' {
				n--
			}
		}
	}
	line := string(buf[:n])
	if dropped > 0 {
		line += fmt.Sprintf(" …[+%d bytes]", dropped)
	}
	return line, nil
}

func fingerprint(batch []Item, norm normalize.Normalizer) {
	workers := runtime.GOMAXPROCS(0)
	if len(batch) < 64 || workers == 1 {
		for i := range batch {
			batch[i].Key = norm.Fingerprint(batch[i].Line)
		}
		return
	}
	var wg sync.WaitGroup
	chunk := (len(batch) + workers - 1) / workers
	for start := 0; start < len(batch); start += chunk {
		end := min(start+chunk, len(batch))
		wg.Add(1)
		go func(part []Item) {
			defer wg.Done()
			for i := range part {
				part[i].Key = norm.Fingerprint(part[i].Line)
			}
		}(batch[start:end])
	}
	wg.Wait()
}
