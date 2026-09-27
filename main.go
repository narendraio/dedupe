// dedupe collapses repeated and near-duplicate log lines in a live stream.
//
//	tail -f app.log | dedupe
//	kubectl logs -f deploy/api | dedupe --summary
//	dedupe app.log
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/narendraio/dedupe/internal/engine"
	"github.com/narendraio/dedupe/internal/normalize"
)

// version is set with -ldflags "-X main.version=..." for release builds.
var version = ""

const usage = `dedupe — pipe noisy logs in, get signal out.

Usage:
  <command> | dedupe [flags]
  dedupe [flags] [file ...]

Examples:
  tail -f app.log | dedupe
  kubectl logs -f deploy/api | dedupe --summary
  dedupe --no-live --window 10s app.log > quiet.log

Flags:
`

type config struct {
	window     time.Duration
	liveRows   int
	noLive     bool
	summary    bool
	keepQuotes bool
	color      string
	version    bool
	files      []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func parseArgs(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{}
	fs := flag.NewFlagSet("dedupe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.DurationVar(&cfg.window, "window", 5*time.Second, "a group ends after this long without repeats (pipe mode)")
	fs.IntVar(&cfg.liveRows, "live-rows", 20, "number of recent groups kept live-updating on screen")
	fs.BoolVar(&cfg.noLive, "no-live", false, "never redraw in place; print a plain stream even on a terminal")
	fs.BoolVar(&cfg.summary, "summary", false, "print the top 10 groups to stderr at exit (also on Ctrl-C)")
	fs.BoolVar(&cfg.keepQuotes, "keep-quotes", false, `treat quoted strings as significant instead of replacing them with "…"`)
	fs.StringVar(&cfg.color, "color", "auto", "colorize output: auto, always or never")
	fs.BoolVar(&cfg.version, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.VisitAll(func(f *flag.Flag) {
			arg, help := flag.UnquoteUsage(f)
			name := "--" + f.Name
			if arg != "" {
				name += " " + arg
			}
			if f.DefValue != "" && f.DefValue != "false" {
				help += " (default " + f.DefValue + ")"
			}
			fmt.Fprintf(stderr, "  %-20s %s\n", name, help)
		})
	}
	// Allow flags after file names: dedupe app.log --summary
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		cfg.files = append(cfg.files, args[0])
		args = args[1:]
	}
	switch cfg.color {
	case "auto", "always", "never":
	default:
		return nil, fmt.Errorf("invalid --color %q (want auto, always or never)", cfg.color)
	}
	if cfg.window <= 0 {
		return nil, errors.New("--window must be positive")
	}
	if cfg.liveRows < 1 {
		return nil, errors.New("--live-rows must be at least 1")
	}
	return cfg, nil
}

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func isTTY(f any) bool {
	if f, ok := f.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

func useColor(mode string, f any) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	return isTTY(f) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "dedupe:", err)
		return 2
	}
	if cfg.version {
		fmt.Fprintln(stdout, "dedupe", versionString())
		return 0
	}

	var inputs []io.Reader
	for _, name := range cfg.files {
		if name == "-" {
			inputs = append(inputs, stdin)
			continue
		}
		f, err := os.Open(name)
		if err != nil {
			fmt.Fprintln(stderr, "dedupe:", err)
			return 1
		}
		defer f.Close()
		inputs = append(inputs, f)
	}
	if len(inputs) == 0 {
		if isTTY(stdin) {
			fmt.Fprint(stderr, "dedupe: reading from the terminal (pipe something in, or pass a file; --help for usage)\n")
		}
		inputs = append(inputs, stdin)
	}

	size := func() (int, int) {
		if f, ok := stdout.(*os.File); ok {
			if c, r, err := term.GetSize(int(f.Fd())); err == nil {
				return c, r
			}
		}
		return 80, 24
	}

	live := !cfg.noLive && isTTY(stdout) && os.Getenv("TERM") != "dumb"
	color := useColor(cfg.color, stdout)

	var r engine.Renderer
	tick := FrameTick(cfg.window)
	if live {
		r = engine.NewLive(stdout, cfg.liveRows, color, size)
		tick = engine.FrameInterval
	} else {
		r = engine.NewPipe(stdout, cfg.window, color)
	}

	var stats *engine.Stats
	if cfg.summary {
		stats = engine.NewStats()
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	lines := make(chan []engine.Item, 64)
	readErr := make(chan error, 1)
	go func() {
		readErr <- engine.Read(inputs, normalize.Normalizer{KeepQuotes: cfg.keepQuotes}, lines)
	}()

	code := consume(lines, sigs, r, stats, tick)

	r.Close(time.Now())
	if stats != nil {
		cols, _ := sizeOf(stderr)
		stats.WriteSummary(stderr, 10, useColor(cfg.color, stderr), cols)
	}
	if code == 0 {
		select {
		case err := <-readErr:
			if err != nil {
				fmt.Fprintln(stderr, "dedupe:", err)
				code = 1
			}
		default:
		}
	}
	return code
}

// consume runs the main loop until input ends (0) or a signal arrives (130).
func consume(lines <-chan []engine.Item, sigs <-chan os.Signal, r engine.Renderer, stats *engine.Stats, tick time.Duration) int {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	flusher, _ := r.(interface{ Flush() })
	for {
		select {
		case batch, ok := <-lines:
			if !ok {
				return 0
			}
			now := time.Now()
			for _, it := range batch {
				if stats != nil {
					stats.Add(it, now)
				}
				r.Line(it, now)
			}
			if flusher != nil && len(lines) == 0 {
				flusher.Flush()
			}
		case now := <-ticker.C:
			r.Tick(now)
		case <-sigs:
			return 130
		}
	}
}

// FrameTick picks how often pipe mode checks for ended groups.
func FrameTick(window time.Duration) time.Duration {
	return min(max(window/10, 10*time.Millisecond), 250*time.Millisecond)
}

func sizeOf(w io.Writer) (int, int) {
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if c, r, err := term.GetSize(int(f.Fd())); err == nil {
			return c, r
		}
	}
	return 0, 0
}
