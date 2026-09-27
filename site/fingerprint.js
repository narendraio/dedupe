/* dedupe's fingerprint rules, ported line for line from internal/normalize/normalize.go.
   Same regexes, same order, same prefilters. The only addition: every replacement is
   tracked as a segment, so the page can show which tokens were masked and what they were. */

export const MAX_INPUT = 8 << 10;
export const TS = "<ts>", UUID = "<uuid>", IP = "<ip>", HEX = "<hex>", NUM = "#", STR = '"…"';

const ansiRE = /\x1b\[[0-9;?]*[ -/]*[@-~]/g;
// A double-quoted string, or a single-quoted one that does not start mid-word (so "don't" survives).
const quotedRE = /"(?:[^"\\]|\\.)*"|(?:^|[^\p{L}\p{N}_])'(?:[^'\\\n]|\\.)*'/gu;
// Candidate IPv6 tokens; validated before replacing.
const ipv6RE = /\[?[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:(?:\.\d{1,3}){3}\.?\d{0,3})?\]?(?::\d{1,5})?/g;

const isHexLetter = (c) => (c >= "a" && c <= "f") || (c >= "A" && c <= "F");

// Order matters: the most specific patterns run first.
const rules = [
  { kind: "ts", re: /\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2})?(?:[.,]\d+)?(?:\s?(?:Z|[+-]\d{2}:?\d{2}|UTC)\b)?/g, repl: TS, need: "-:" },
  { kind: "ts", re: /\d{2}\/[A-Z][a-z]{2}\/\d{4}:\d{2}:\d{2}:\d{2}(?: [+-]\d{4})?/g, repl: TS, need: "/:" },
  { kind: "ts", re: /(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{1,2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2}(?: [A-Z]{2,5}\b| [+-]\d{4})?/g, repl: TS, need: ",:" },
  { kind: "ts", re: /\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) +\d{1,2} \d{2}:\d{2}:\d{2}(?:\.\d+)?/g, repl: TS, need: ":" },
  { kind: "ts", re: /\b\d{4}\/\d{2}\/\d{2}(?: \d{2}:\d{2}:\d{2}(?:[.,]\d+)?)?/g, repl: TS, need: "/" },
  { kind: "ts", re: /\b[IWEF]\d{4} \d{2}:\d{2}:\d{2}\.\d+/g, repl: TS, need: ":" },
  { kind: "ts", re: /\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b/g, repl: TS, need: ":" },
  { kind: "ts", re: /\b\d{4}-\d{2}-\d{2}\b/g, repl: TS, need: "-" },
  { kind: "ts", re: /\b1\d{9}(?:\d{3}|\d{6}|\d{9})?(?:\.\d+)?\b/g, repl: TS, need: "1" },
  { kind: "uuid", re: /\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b/g, repl: UUID, need: "-" },
  { kind: "ip", re: /\b(?:\d{1,3}\.){3}\d{1,3}(?:\/\d{1,2})?(?::\d{1,5})?\b/g, repl: IP, need: "." },
  { kind: "hex", re: /\b0[xX][0-9a-fA-F]+\b/g, repl: HEX, need: "0" },
  { kind: "hex", re: /\b[0-9a-fA-F]{8,}\b/g, fn: (s) => ([...s].some(isHexLetter) ? HEX : s) },
];
const ipv6Slot = rules.findIndex((r) => r.repl === IP);

const keptKeys = new Set(["msg", "message", "error", "err", "exception", "level", "lvl", "severity",
  "event", "reason", "logger", "component", "caller", "status"]);

const isWordChar = (c) => c === "_" || c.charCodeAt(0) >= 0x80 || /[0-9A-Za-z]/.test(c);

/* ── segments ───────────────────────────────────────────────
   A line is a list of segments: raw text { t, o } (t = current text, o = original)
   or a masked token { t: "<ts>", o: "2026-09-27T…", k: "ts" }. */
const joinT = (segs) => segs.map((s) => s.t).join("");

/** Replace the given [start, end, repl, kind] ranges (in the current text) with tokens. */
function splice(segs, hits) {
  if (!hits.length) return segs;
  const out = [];
  let pos = 0, h = 0, cur = null; // cur: token being collected
  for (const seg of segs) {
    const end = pos + seg.t.length;
    if (seg.k) { // an existing token is atomic: rules never match placeholder characters
      if (cur) cur.o += seg.o; else out.push(seg);
      pos = end;
      continue;
    }
    let i = 0;
    while (i < seg.t.length) {
      const at = pos + i;
      if (cur) {
        const stop = Math.min(hits[h][1], end) - pos;
        cur.o += seg.o.slice(i, stop);
        i = stop;
        if (pos + i >= hits[h][1]) { out.push(cur); cur = null; h++; }
        continue;
      }
      if (h < hits.length && hits[h][0] === at) {
        cur = { t: hits[h][2], o: "", k: hits[h][3] };
        if (hits[h][0] === hits[h][1]) { out.push(cur); cur = null; h++; }
        continue;
      }
      const next = h < hits.length ? Math.min(hits[h][0], end) - pos : seg.t.length;
      const piece = seg.t.slice(i, next);
      if (piece) out.push({ t: piece, o: seg.o.slice(i, next) });
      i = next;
    }
    pos = end;
  }
  if (cur) out.push(cur);
  return out;
}

function replaceQuoted(segs) {
  const s = joinT(segs);
  const hits = [];
  for (const m of s.matchAll(quotedRE)) {
    let start = m.index;
    const end = m.index + m[0].length;
    if (s[start] !== '"' && s[start] !== "'") start += m[0].codePointAt(0) > 0xffff ? 2 : 1; // leading char of the ' alternative
    if (isKey(s, end)) continue;
    const key = keyBefore(s, start);
    if (keptKeys.has(key.toLowerCase())) continue;
    // A free-standing quoted phrase ("Observed a panic") is prose, not a volatile value.
    if (key === "" && /[ \t]/.test(s.slice(start + 1, end - 1))) continue;
    hits.push([start, end, STR, "str"]);
  }
  return splice(segs, hits);
}

function isKey(s, end) {
  return s.slice(end).replace(/^[ \t]+/, "").startsWith(":");
}

function keyBefore(s, start) {
  let head = s.slice(0, start).replace(/[ \t]+$/, "");
  if (!head) return "";
  const c = head[head.length - 1];
  if (c !== "=" && c !== ":") return "";
  head = head.slice(0, -1).replace(/[ \t]+$/, "");
  if (head.endsWith('"')) head = head.slice(0, -1);
  let i = head.length;
  while (i > 0 && (isWordChar(head[i - 1]) || head[i - 1] === "." || head[i - 1] === "-")) i--;
  return head.slice(i);
}

/* net.ParseIP (netip.ParseAddr) for the IPv6 case: groups of 1–4 hex digits, at most one "::"
   standing for at least one zero group, an optional dotted IPv4 tail, no zones. */
function parseIPv4(s) {
  const p = s.split(".");
  return p.length === 4 && p.every((x) => /^\d{1,3}$/.test(x) && +x <= 255 && !(x.length > 1 && x[0] === "0"));
}
function validIPv6(tok) {
  if (tok.startsWith("[")) {
    const i = tok.indexOf("]");
    if (i < 0) return false;
    tok = tok.slice(1, i);
  }
  if (!tok.includes(":")) return false;
  let s = tok, ellipsis = -1, i = 0;
  if (s.startsWith("::")) { ellipsis = 0; s = s.slice(2); if (!s) return true; }
  while (i < 16) {
    const m = /^[0-9A-Fa-f]{1,4}/.exec(s);
    if (!m) return false;
    const c = m[0].length;
    if (c < s.length && s[c] === ".") { // trailing IPv4
      if (ellipsis < 0 && i !== 12) return false;
      if (i + 4 > 16) return false;
      if (!parseIPv4(s)) return false;
      i += 4; s = "";
      break;
    }
    if (c < s.length && /[0-9A-Fa-f]/.test(s[c])) return false; // a 5th hex digit
    i += 2;
    s = s.slice(c);
    if (!s) break;
    if (s[0] !== ":" || s.length === 1) return false;
    s = s.slice(1);
    if (s[0] === ":") {
      if (ellipsis >= 0) return false;
      ellipsis = i;
      s = s.slice(1);
      if (!s) break;
    }
  }
  if (s) return false;
  if (i < 16) return ellipsis >= 0;
  return ellipsis < 0;
}

function replaceIPv6(segs) {
  const s = joinT(segs);
  const hits = [];
  for (const m of s.matchAll(ipv6RE)) {
    const start = m.index, end = m.index + m[0].length;
    if (start === end) continue;
    if ((start > 0 && isWordChar(s[start - 1])) || (end < s.length && isWordChar(s[end]))) continue;
    if (!validIPv6(m[0])) continue;
    hits.push([start, end, IP, "ip"]);
  }
  return splice(segs, hits);
}

function applyRule(segs, r) {
  const s = joinT(segs);
  const hits = [];
  for (const m of s.matchAll(r.re)) {
    if (!m[0]) continue;
    const repl = r.fn ? r.fn(m[0]) : r.repl;
    if (repl === m[0]) continue;
    hits.push([m.index, m.index + m[0].length, repl, r.kind]);
  }
  return splice(segs, hits);
}

/* squash: every remaining number becomes "#", whitespace runs become one space,
   leading/trailing whitespace goes. Original whitespace is kept in `o` for display. */
const isDigit = (c) => c >= "0" && c <= "9";
const isSpace = (c) => c === " " || c === "\t" || c === "\n" || c === "\r" || c === "\v" || c === "\f";
function squash(segs) {
  // merge neighbouring raw segments first so a number is never split
  const merged = [];
  for (const s of segs) {
    const last = merged[merged.length - 1];
    if (!s.k && last && !last.k) { last.t += s.t; last.o += s.o; } else merged.push({ ...s });
  }
  const out = [];
  let len = 0, space = false, pendingWS = "";
  const emitSpace = () => {
    if (space && len > 0) { out.push({ t: " ", o: pendingWS }); len++; }
    else if (pendingWS) out.push({ t: "", o: pendingWS });
    space = false; pendingWS = "";
  };
  const raw = (t, o) => {
    const last = out[out.length - 1];
    if (last && !last.k) { last.t += t; last.o += o; } else out.push({ t, o });
    len += t.length;
  };
  for (const seg of merged) {
    if (seg.k) { emitSpace(); out.push(seg); len += seg.t.length; continue; }
    const s = seg.t;
    let i = 0;
    while (i < s.length) {
      const c = s[i];
      if (isDigit(c)) {
        const st = i;
        i++;
        while (i < s.length && isDigit(s[i])) i++;
        if (i + 1 < s.length && s[i] === "." && isDigit(s[i + 1])) {
          i++;
          while (i < s.length && isDigit(s[i])) i++;
        }
        emitSpace();
        out.push({ t: NUM, o: s.slice(st, i), k: "num" });
        len += 1;
        continue;
      }
      if (isSpace(c)) { space = true; pendingWS += c; i++; continue; }
      emitSpace();
      raw(c, c);
      i++;
    }
  }
  if (pendingWS) out.push({ t: "", o: pendingWS });
  // tidy: merge raw neighbours again
  const tidy = [];
  for (const s of out) {
    const last = tidy[tidy.length - 1];
    if (!s.k && last && !last.k) { last.t += s.t; last.o += s.o; } else tidy.push(s);
  }
  return tidy;
}

/** Fingerprint a line. Returns { fp, segs } where segs describe what was masked. */
export function analyze(line, { keepQuotes = false } = {}) {
  if (line.length > MAX_INPUT) line = line.slice(0, MAX_INPUT);
  if (line.includes("\x1b")) line = line.replace(ansiRE, "");
  let segs = [{ t: line, o: line }];
  if (!keepQuotes && /["']/.test(line)) segs = replaceQuoted(segs);
  const hasDigit = /[0-9]/.test(joinT(segs));
  rules.forEach((r, i) => {
    let s = joinT(segs);
    if (i === ipv6Slot && (s.match(/:/g) || []).length >= 2) { segs = replaceIPv6(segs); s = joinT(segs); }
    if (r.need && (!hasDigit || ![...r.need].every((c) => s.includes(c)))) return;
    segs = applyRule(segs, r);
  });
  segs = squash(segs);
  return { fp: joinT(segs), segs };
}

export const fingerprint = (line, opts) => analyze(line, opts).fp;

/** Group lines the way dedupe does: first-seen order, one group per fingerprint. */
export function group(lines, opts) {
  const map = new Map();
  const groups = [];
  let n = 0;
  for (const line of lines) {
    n++;
    const a = analyze(line, opts);
    let g = map.get(a.fp);
    if (!g) {
      g = { key: a.fp, sample: line, segs: a.segs, count: 0, seq: groups.length + 1, lastLine: n };
      map.set(a.fp, g);
      groups.push(g);
    }
    g.count++;
    g.lastLine = n;
  }
  return groups;
}

/** "99.7" — floored to one decimal, like stats.go, so a few groups never read as 100%. */
export function quieter(lines, groups) {
  if (!lines) return 0;
  return Math.floor(1000 * (1 - groups / lines)) / 10;
}

export const commas = (n) => n.toLocaleString("en-US");
