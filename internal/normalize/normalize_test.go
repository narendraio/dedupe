package normalize

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFingerprint(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"iso8601 utc", `2024-05-01T12:04:31.123Z ERROR db timeout after 30s`, `<ts> ERROR db timeout after #s`},
		{"iso8601 offset", `2024-05-01T12:04:31+02:00 WARN retrying`, `<ts> WARN retrying`},
		{"iso space comma millis", `2024-05-01 12:04:31,123 INFO [main] started`, `<ts> INFO [main] started`},
		{"python logging", `2024-05-01 12:04:31,991 - worker - ERROR - job 8812 failed`, `<ts> - worker - ERROR - job # failed`},
		{"common log format", `127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326`, `<ip> - frank [<ts>] "GET /apache_pb.gif HTTP/#" # #`},
		{"nginx error", `2024/05/01 12:04:31 [error] 31#31: *1 connect() failed (111: Connection refused) while connecting to upstream, client: 10.0.3.7, server: api`, `<ts> [error] ###: *# connect() failed (#: Connection refused) while connecting to upstream, client: <ip>, server: api`},
		{"syslog", `May  1 12:04:31 web-3 sshd[4412]: Failed password for root from 203.0.113.9 port 51234 ssh2`, `<ts> web-# sshd[#]: Failed password for root from <ip> port # ssh#`},
		{"rfc1123", `Wed, 01 May 2024 12:04:31 GMT request done`, `<ts> request done`},
		{"klog", `E0501 12:04:31.123456   12345 reflector.go:138] watch of *v1.Pod ended`, `<ts> # reflector.go:#] watch of *v#.Pod ended`},
		{"bare clock", `[12:04:31] tick`, `[<ts>] tick`},
		{"epoch seconds", `ts=1714565071 level=info msg=heartbeat`, `ts=<ts> level=info msg=heartbeat`},
		{"epoch millis", `{"t":1714565071123,"msg":"ok"}`, `{"t":<ts>,"msg":"ok"}`},
		{"uuid", `request 3fa85f64-5717-4562-b3fc-2c963f66afa6 completed`, `request <uuid> completed`},
		{"uuid upper", `id=3FA85F64-5717-4562-B3FC-2C963F66AFA6`, `id=<uuid>`},
		{"git sha", `deployed commit 9fceb02d0ae598e95dc970b74767f19372d61af8`, `deployed commit <hex>`},
		{"trace id", `trace_id=4bf92f3577b34da6a3ce929d0e0e4736 span=00f067aa0ba902b7`, `trace_id=<hex> span=<hex>`},
		{"0x pointer", `panic: nil deref at 0xc000123abc`, `panic: nil deref at <hex>`},
		{"short hex word stays", `cafe added bead`, `cafe added bead`},
		{"digits only long", `processed 12345678 rows`, `processed # rows`},
		{"ipv4 port", `dial tcp 10.0.0.12:5432: connect: connection refused`, `dial tcp <ip>: connect: connection refused`},
		{"ipv4 cidr", `allow 192.168.0.0/16`, `allow <ip>`},
		{"ipv6", `client 2001:db8:85a3::8a2e:370:7334 connected`, `client <ip> connected`},
		{"ipv6 loopback bracket", `listening on [::1]:8080`, `listening on <ip>`},
		{"rust path untouched", `thread 'main' panicked at std::io::Error`, `thread "…" panicked at std::io::Error`},
		{"numbers", `took 123.45ms, 3 retries, user42`, `took #ms, # retries, user#`},
		{"quoted double", `user "alice" logged in`, `user "…" logged in`},
		{"quoted single", `cannot open 'config.yaml'`, `cannot open "…"`},
		{"apostrophe kept", `can't connect, won't retry`, `can't connect, won't retry`},
		{"json keeps keys and msg", `{"level":"error","msg":"db timeout","user":"bob","latency_ms":120}`, `{"level":"error","msg":"db timeout","user":"…","latency_ms":#}`},
		{"logfmt keeps msg", `level=warn msg="slow query" query="SELECT 1" dur=2.1s`, `level=warn msg="slow query" query="…" dur=#s`},
		{"ansi stripped", "\x1b[31mERROR\x1b[0m boom 42", `ERROR boom #`},
		{"whitespace collapsed", "  a \t  b   ", `a b`},
		{"kubernetes", `I0501 12:04:31.000001       1 controller.go:116] "Observed a panic" pod="default/api-7d9f8b6c5-x2kqp"`, `<ts> # controller.go:#] "Observed a panic" pod="…"`},
	}
	var n Normalizer
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := n.Fingerprint(c.in); got != c.want {
				t.Errorf("Fingerprint(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}

func TestKeepQuotes(t *testing.T) {
	n := Normalizer{KeepQuotes: true}
	got := n.Fingerprint(`user "alice" logged in 3 times`)
	if want := `user "alice" logged in # times`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// Lines that should be the same event.
func TestSameGroup(t *testing.T) {
	pairs := [][2]string{
		{`2024-05-01T12:04:31Z ERROR payment 3fa85f64-5717-4562-b3fc-2c963f66afa6 failed: timeout after 30012ms`,
			`2024-05-01T12:09:02Z ERROR payment 7c9e6679-7425-40de-944b-e07fc1f90ae7 failed: timeout after 30001ms`},
		{`GET /api/users/1234 200 12ms`, `GET /api/users/98 200 7ms`},
		{`10.1.2.3 - - [01/May/2024:12:04:31 +0000] "GET /health HTTP/1.1" 200 2`,
			`10.9.8.7 - - [01/May/2024:12:04:32 +0000] "GET /health HTTP/1.1" 200 2`},
		{`{"time":"2024-05-01T12:04:31Z","level":"warn","msg":"cache miss","key":"user:1"}`,
			`{"time":"2024-05-01T12:04:33Z","level":"warn","msg":"cache miss","key":"user:77"}`},
	}
	var n Normalizer
	for _, p := range pairs {
		a, b := n.Fingerprint(p[0]), n.Fingerprint(p[1])
		if a != b {
			t.Errorf("expected same fingerprint:\n %q -> %q\n %q -> %q", p[0], a, p[1], b)
		}
	}
}

// Lines that must NOT collapse.
func TestDifferentGroup(t *testing.T) {
	pairs := [][2]string{
		{`ERROR db timeout`, `ERROR db connection refused`},
		{`{"level":"error","msg":"db timeout"}`, `{"level":"error","msg":"disk full"}`},
		{`level=info msg="started"`, `level=info msg="stopped"`},
		{`GET /api/users 200`, `POST /api/users 200`},
	}
	var n Normalizer
	for _, p := range pairs {
		if a, b := n.Fingerprint(p[0]), n.Fingerprint(p[1]); a == b {
			t.Errorf("expected different fingerprints, both %q", a)
		}
	}
}

func TestInvalidUTF8AndLongLines(t *testing.T) {
	var n Normalizer
	got := n.Fingerprint("bad \xff\xfe bytes 12")
	if want := "bad � bytes #"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	long := "ERROR " + strings.Repeat("x", 100_000)
	if fp := n.Fingerprint(long); len(fp) > MaxFingerprintInput {
		t.Fatalf("fingerprint too long: %d", len(fp))
	}
}

func BenchmarkFingerprint(b *testing.B) {
	var n Normalizer
	line := `2024-05-01T12:04:31.123Z ERROR [api] request 3fa85f64-5717-4562-b3fc-2c963f66afa6 from 10.0.0.12:5432 failed after 30012ms: "upstream timeout"`
	for i := 0; i < b.N; i++ {
		n.Fingerprint(line)
	}
}

func TestQuotedAfterMultibyteRune(t *testing.T) {
	got := Normalizer{}.Fingerprint("deploy 🚀'v1.2.3' failed")
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("fingerprint corrupted a multibyte rune: %q", got)
	}
	if !strings.Contains(got, "🚀") {
		t.Fatalf("emoji before quoted value was dropped: %q", got)
	}
}
