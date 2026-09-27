package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `2026-09-27T12:04:00.001Z INFO  server listening on :8080
2026-09-27T12:04:01.120Z ERROR db: query timeout after 3001ms (req=3fa85f64-5717-4562-b3fc-2c963f66afa6)
2026-09-27T12:04:01.480Z ERROR db: query timeout after 3017ms (req=7c9e6679-7425-40de-944b-e07fc1f90ae7)
2026-09-27T12:04:02.002Z WARN  cache miss key="user:42" node=10.0.3.7
2026-09-27T12:04:02.310Z ERROR db: query timeout after 2998ms (req=16fd2706-8baf-433b-82eb-8c7fada847da)
2026-09-27T12:04:02.900Z WARN  cache miss key="user:7" node=10.0.1.12
2026-09-27T12:04:03.000Z INFO  shutting down
`

func writeSample(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunPipeMode(t *testing.T) {
	path := writeSample(t)
	var out, errb bytes.Buffer
	// flags after the file name must still work
	code := run([]string{path, "--summary", "--color", "never"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 4 first lines + 2 summaries, got %d:\n%s", len(lines), out.String())
	}
	for i, prefix := range []string{
		"2026-09-27T12:04:00.001Z INFO  server listening",
		"2026-09-27T12:04:01.120Z ERROR db: query timeout",
		"2026-09-27T12:04:02.002Z WARN  cache miss",
		"2026-09-27T12:04:03.000Z INFO  shutting down",
		"  ↳ repeated 2 more times",
		"  ↳ repeated 1 more time ",
	} {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], prefix)
		}
	}
	if !strings.Contains(errb.String(), "7 lines → 4 groups") {
		t.Errorf("summary missing:\n%s", errb.String())
	}
}

func TestRunStdin(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--keep-quotes"}, strings.NewReader("hit \"a\" 1\nhit \"a\" 2\nhit \"b\" 3\n"), &out, &errb)
	if code != 0 {
		t.Fatal(errb.String())
	}
	want := "hit \"a\" 1\nhit \"b\" 3\n  ↳ repeated 1 more time (last "
	if !strings.HasPrefix(out.String(), want) {
		t.Fatalf("got %q", out.String())
	}
}

func TestRunErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--color", "rainbow"}, nil, &out, &errb); code != 2 {
		t.Errorf("bad --color: exit %d", code)
	}
	if code := run([]string{"/definitely/not/here.log"}, nil, &out, &errb); code != 1 {
		t.Errorf("missing file: exit %d", code)
	}
	out.Reset()
	if code := run([]string{"--version"}, nil, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "dedupe ") {
		t.Errorf("--version: exit %d, %q", code, out.String())
	}
}
