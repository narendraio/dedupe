/* dedupe — the site, with a working copy of the fingerprint engine. */
import { analyze, group, quieter, commas } from "./fingerprint.js";
import { sample, stream } from "./samples.js";

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const plural = (n, one, many) => (n === 1 ? one : many);
const clockNow = () => new Date().toTimeString().slice(0, 8);
const KIND = { ts: "timestamp", uuid: "uuid", ip: "ip address", hex: "hex id", num: "number", str: "quoted value" };

/** Render segments: the original text, with masked tokens wrapped. */
function segsHTML(segs, useFP) {
  return segs.map((s) => {
    const text = useFP ? s.t : s.o;
    if (!s.k) return esc(text);
    return `<span class="tok" data-k="${s.k}" title="${esc(KIND[s.k] + ": " + s.o + " → " + s.t)}">${esc(text)}</span>`;
  }).join("");
}

/* ═══════════════════════ hero — the live stream ═══════════════════════ */
(() => {
  const fig = $(".stage");
  if (!fig) return;
  const box = $(".stream", fig), barEl = $(".st-bar i", fig), track = $(".st-in-track", fig), outEl = $(".st-out", fig);
  const rateEl = $(".st-rate", fig), groupsEl = $(".st-groups", fig), sumEl = $(".st-sum", fig), qEl = $(".st-q", fig);
  const btn = $(".fig-btn", fig);

  const next = stream("k8s", 20260927);
  const VITAL = [
    "ERROR payment provider returned 503 for charge ch_3Nf8Qz",
    "WARN  disk /var/lib/postgresql at 91% capacity",
    "ERROR tls: certificate for api.internal expired",
    "FATAL migration add_orders_index failed: lock timeout",
    "ERROR stripe webhook signature mismatch",
  ];
  const groups = new Map();
  let live = []; // groups in the live window, oldest first
  let lines = 0, seen = 0, vi = 0, sinceVital = 0, running = !reduce, visible = true, timer = 0;

  const display = (line) => line.replace(/^2026-09-27T/, "").replace(/ {2,}/g, " ");
  const levelHTML = (html) => html.replace(/\b(ERROR|FATAL|E0927)\b/, '<span class="lv-e">$1</span>').replace(/\b(WARN|W0927)\b/, '<span class="lv-w">$1</span>');

  function capacity() { return Math.max(4, Math.floor((outEl.clientHeight - 10) / 34)); }

  function makeRow(g) {
    const row = document.createElement("div");
    row.className = "st-row new" + (g.vital ? " vital" : "");
    row.innerHTML = `<span class="t">${levelHTML(esc(display(g.sample)))}</span><span class="c"></span><span class="ago">just now</span>`;
    outEl.appendChild(row);
    g.row = row;
    setCount(g);
    // keep the panel bottom-anchored: drop rows that scrolled off the top
    const rows = outEl.children;
    while (rows.length > capacity()) rows[0].remove();
    return row;
  }
  function setCount(g) {
    const c = $(".c", g.row);
    c.textContent = "×" + commas(g.count);
    c.classList.toggle("one", g.count === 1);
  }
  function freeze(g) {
    g.inLive = false;
    if (!g.row) return;
    g.row.classList.add("frozen");
    $(".ago", g.row).textContent = clockNow();
    g.row = null;
  }

  function ghost(fromEl, toEl, text) {
    if (reduce || !fromEl || !toEl || document.hidden) return;
    const b = box.getBoundingClientRect(), f = fromEl.getBoundingClientRect(), t = toEl.getBoundingClientRect();
    const el = document.createElement("div");
    el.className = "ghost";
    el.textContent = text.slice(0, 38);
    box.appendChild(el);
    const x0 = f.left - b.left + 14, y0 = f.top - b.top, x1 = t.left - b.left + 14, y1 = t.top - b.top + 6;
    el.animate(
      [
        { transform: `translate(${x0}px, ${y0}px) scale(1)`, opacity: 0.95 },
        { transform: `translate(${x1}px, ${y1}px) scale(0.92)`, opacity: 0 },
      ],
      { duration: 520, easing: "cubic-bezier(.5,0,.2,1)" },
    ).onfinish = () => el.remove();
  }

  function push() {
    let line;
    if (++sinceVital > 38 + Math.floor(Math.random() * 30)) {
      sinceVital = 0;
      line = "2026-09-27T12:0" + (4 + (vi % 5)) + ":" + String(10 + ((vi * 17) % 49)).padStart(2, "0") + "." + String(100 + vi * 7).slice(-3) + "Z " + VITAL[vi++ % VITAL.length];
    } else line = next();
    lines++;

    // stdin band: newest line at the bottom, masked tokens tinted
    const a = analyze(line);
    const div = document.createElement("div");
    div.className = "st-line";
    div.innerHTML = a.segs.map((s) => (s.k ? `<span class="tk">${esc(display(s.o))}</span>` : esc(s.o))).join("").replace(/^2026-09-27T/, "");
    track.appendChild(div);
    while (track.children.length > 16) track.firstChild.remove();

    // group it
    let g = groups.get(a.fp);
    if (!g) {
      g = { key: a.fp, sample: line, count: 0, inLive: false, row: null, vital: /ERROR payment|WARN  disk|tls:|FATAL|stripe/.test(line) };
      groups.set(a.fp, g);
      seen++;
    }
    g.count++;
    g.last = performance.now();
    if (!g.inLive) {
      g.inLive = true;
      live.push(g);
      makeRow(g);
      // the live window: the newest groups keep updating in place, older ones freeze
      const LIVE = Math.max(2, Math.min(6, capacity() - 2));
      while (live.length > LIVE) freeze(live.shift());
    } else {
      setCount(g);
      g.row.classList.remove("hit", "new");
      void g.row.offsetWidth;
      g.row.classList.add("hit");
      setTimeout(() => g.row && g.row.classList.remove("hit"), 60);
      if (lines % 2 === 0) ghost(div, g.row, display(line));
    }

    rateEl.textContent = commas(lines) + " " + plural(lines, "line", "lines");
    groupsEl.textContent = commas(seen) + " " + plural(seen, "group", "groups");
    sumEl.textContent = `${commas(lines)} lines → ${commas(seen)} groups`;
    const q = quieter(lines, seen);
    qEl.textContent = q.toFixed(1) + "% quieter";
    barEl.style.width = q + "%";
  }

  function agoTick() {
    const now = performance.now();
    for (const g of live) {
      if (!g.row) continue;
      const s = Math.floor((now - g.last) / 1000);
      $(".ago", g.row).textContent = s < 1 ? "just now" : s < 60 ? s + "s ago" : Math.floor(s / 60) + "m ago";
    }
  }
  setInterval(agoTick, 1000);

  function loop() {
    clearTimeout(timer);
    if (!running || !visible || document.hidden) return;
    push();
    // bursty, like a real service: mostly fast, sometimes a breather
    const r = Math.random();
    const wait = reduce ? 700 : r < 0.1 ? 380 : r < 0.5 ? 70 : 130;
    timer = setTimeout(loop, wait);
  }

  // warm up so the panel is never empty
  for (let i = 0; i < 26; i++) push();
  $$(".st-row", outEl).forEach((r) => r.classList.remove("new"));

  new IntersectionObserver(([e]) => { visible = e.isIntersecting; loop(); }).observe(fig);
  document.addEventListener("visibilitychange", loop);
  btn.addEventListener("click", () => {
    running = !running;
    btn.textContent = running ? "pause" : "play";
    btn.setAttribute("aria-pressed", String(!running));
    loop();
  });
  if (reduce) { btn.textContent = "play"; btn.setAttribute("aria-pressed", "true"); }
  loop();
})();

/* ═══════════════════════ demo — paste your worst log ═══════════════════════ */
(() => {
  const pad = $("#pad"), out = $(".out"), keep = $("#keepq");
  if (!pad) return;
  const meter = $(".meter"), mLines = $(".m-lines"), mGroups = $(".m-groups"), mQ = $(".m-q"), bar = $(".meter-bar i");
  const chips = $$(".chips .chip");
  let mode = "live", timer = 0;

  const truncate = (s, max) => ([...s].length <= max ? s : [...s].slice(0, max - 1).join("") + "…");

  // count a number up/down to its new value
  const shown = new Map();
  function tween(el, to, fmt) {
    const from = shown.get(el) ?? 0;
    shown.set(el, to);
    cancelAnimationFrame(el._raf);
    if (reduce || from === to) { el.textContent = fmt(to); return; }
    const t0 = performance.now(), dur = 700;
    const step = (now) => {
      const k = Math.min(1, (now - t0) / dur), e = 1 - Math.pow(1 - k, 3);
      el.textContent = fmt(from + (to - from) * e);
      if (k < 1) el._raf = requestAnimationFrame(step);
    };
    el._raf = requestAnimationFrame(step);
  }
  const whole = (v) => commas(Math.round(v));
  const pct = (v) => v.toFixed(1);

  function readLines() {
    const v = pad.value;
    if (!v) return [];
    const ls = v.split("\n").map((l) => (l.endsWith("\r") ? l.slice(0, -1) : l));
    if (ls[ls.length - 1] === "") ls.pop(); // a trailing newline is not an extra line
    return ls;
  }

  function render(animate = false) {
    const lines = readLines();
    const groups = group(lines, { keepQuotes: keep.checked });
    const q = quieter(lines.length, groups.length);
    tween(mLines, lines.length, whole);
    tween(mGroups, groups.length, whole);
    tween(mQ, q, pct);
    bar.style.width = q + "%";
    if (animate && !reduce) { meter.classList.remove("pop"); void meter.offsetWidth; meter.classList.add("pop"); }

    if (!lines.length) { out.innerHTML = `<p class="empty">paste some log lines on the left, or pick a sample above.</p>`; return; }

    if (mode === "live") {
      const list = groups.slice(0, 500);
      out.innerHTML = list.map((g, i) =>
        `<div class="g" tabindex="0" style="animation-delay:${animate ? Math.min(i, 14) * 35 : 0}ms${animate ? "" : ";animation:none"}"><span class="cnt${g.count === 1 ? " one" : ""}">×${commas(g.count)}</span>` +
        `<span class="line">${segsHTML(g.segs, false)}</span>` +
        `<span class="fp">${segsHTML(g.segs, true)}</span></div>`).join("") +
        (groups.length > list.length ? `<p class="empty">…and ${commas(groups.length - list.length)} more groups</p>` : "");
      return;
    }

    if (mode === "pipe") {
      // pipe mode at end of input: every first line as it arrived, then one ↳ line per repeated group
      const outLines = [];
      let lastOut = null;
      const firstSeen = new Set();
      const byKey = new Map(groups.map((g) => [g.key, g]));
      for (const line of lines) {
        const g = byKey.get(analyze(line, { keepQuotes: keep.checked }).fp);
        if (firstSeen.has(g)) continue;
        firstSeen.add(g);
        outLines.push(esc(line));
        lastOut = g;
      }
      const t = clockNow();
      for (const g of groups) {
        if (g.count < 2) continue;
        const more = g.count - 1;
        let s = `<i>  ↳</i> repeated <b>${commas(more)}</b> more ${plural(more, "time", "times")} <i>(last ${t})`;
        if (lastOut !== g) s += " · " + esc(truncate(g.sample, 72));
        outLines.push(s + "</i>");
        lastOut = null;
      }
      out.innerHTML = `<pre>${outLines.join("\n")}</pre>`;
      return;
    }

    // --summary: the top 10 groups, as printed to stderr
    const top = [...groups].sort((a, b) => b.count - a.count || a.seq - b.seq).slice(0, 10);
    const cw = Math.max(5, ...top.map((g) => commas(g.count).length + 1));
    const room = Math.max(100 - 2 - cw - 2 - 8 - 2 - 1, 20);
    const t = clockNow();
    let s = `\n  <b>dedupe</b>  ${commas(lines.length)} ${plural(lines.length, "line", "lines")} → ${commas(groups.length)} ${plural(groups.length, "group", "groups")}  <i>· ${q.toFixed(1)}% quieter</i>\n\n`;
    s += `  <i>${"COUNT".padStart(cw)}  ${"LAST".padEnd(8)}  LINE</i>\n`;
    for (const g of top) s += `  <b>${("×" + commas(g.count)).padStart(cw)}</b>  <i>${t}</i>  ${esc(truncate(g.sample, room))}\n`;
    out.innerHTML = `<pre>${s}</pre>`;
  }

  const schedule = () => { clearTimeout(timer); timer = setTimeout(() => render(false), pad.value.length > 200000 ? 250 : 60); };
  pad.addEventListener("input", () => { chips.forEach((b) => b.setAttribute("aria-pressed", "false")); schedule(); });
  keep.addEventListener("change", () => render(true));

  chips.forEach((b) => b.addEventListener("click", () => {
    pad.value = b.dataset.sample ? sample(b.dataset.sample, 60) : "";
    chips.forEach((x) => x.setAttribute("aria-pressed", String(x === b && !!b.dataset.sample)));
    pad.scrollTop = 0; pad.scrollLeft = 0; out.scrollTop = 0;
    render(true);
    if (!b.dataset.sample) pad.focus();
  }));

  const tabs = $$(".tabs [role=tab]");
  tabs.forEach((b, i) => {
    b.addEventListener("click", () => {
      mode = b.dataset.mode;
      tabs.forEach((x) => { x.setAttribute("aria-selected", String(x === b)); x.tabIndex = x === b ? 0 : -1; });
      render(true);
    });
    b.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
      const n = tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
      n.focus(); n.click();
    });
    b.tabIndex = i === 0 ? 0 : -1;
  });

  // tap / Enter to pin a row open (touch screens, keyboards)
  out.addEventListener("click", (e) => {
    const g = e.target.closest(".g");
    if (g) g.classList.toggle("is-open");
  });
  out.addEventListener("keydown", (e) => {
    const g = e.target.closest(".g");
    if (g && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); g.classList.toggle("is-open"); }
  });

  pad.value = sample("k8s", 60);
  // count up the first time the demo scrolls into view
  let first = true;
  render(false);
  new IntersectionObserver(([e], io) => {
    if (!e.isIntersecting || !first) return;
    first = false; io.disconnect();
    shown.clear();
    render(true);
  }, { threshold: 0.25 }).observe(meter);
})();

/* ═══════════════════════ install tabs ═══════════════════════ */
(() => {
  const tabs = $$(".itabs [role=tab]");
  tabs.forEach((b, i) => {
    const select = () => tabs.forEach((x) => {
      const on = x === b;
      x.setAttribute("aria-selected", String(on));
      x.tabIndex = on ? 0 : -1;
      $("#" + x.getAttribute("aria-controls")).hidden = !on;
    });
    b.addEventListener("click", select);
    b.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
      const n = tabs[(i + 1) % tabs.length];
      n.focus(); n.click();
    });
  });
})();

/* ═══════════════════════ copy ═══════════════════════ */
$$(".copy").forEach((b) => b.addEventListener("click", async () => {
  const text = $(".copy-src", b.parentElement).textContent.trim();
  try { await navigator.clipboard.writeText(text); }
  catch {
    const t = document.createElement("textarea");
    t.value = text; document.body.appendChild(t); t.select();
    try { document.execCommand("copy"); } catch { /* nothing else to try */ }
    t.remove();
  }
  b.textContent = "copied ✓";
  b.classList.add("done");
  clearTimeout(b._t);
  b._t = setTimeout(() => { b.textContent = "copy"; b.classList.remove("done"); }, 1600);
}));

/* ═══════════════════════ small things that move ═══════════════════════ */
// top bar hairline once scrolled
(() => {
  const bar = $(".bar");
  const on = () => bar.classList.toggle("scrolled", scrollY > 8);
  addEventListener("scroll", on, { passive: true });
  on();
})();

// scroll reveal
(() => {
  const els = $$(".reveal");
  if (reduce || !("IntersectionObserver" in window)) { els.forEach((e) => e.classList.add("in")); return; }
  const io = new IntersectionObserver((es) => es.forEach((e) => {
    if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
  }), { rootMargin: "0px 0px -8% 0px" });
  els.forEach((e) => io.observe(e));
})();

// counters that won't stop: the tile badge and the footer wordmark
function runaway(el, target, loop) {
  if (!el) return;
  if (reduce) { el.textContent = commas(target); return; }
  let v = 1, on = false, t = 0;
  const box = el.closest(".cnt");
  const step = () => {
    if (!on) return;
    v += v < target ? Math.ceil((target - v) / 14) : 1;
    if (loop && v > target + 6) v = 1;
    el.textContent = commas(v);
    if (box && v >= target) { box.classList.remove("bump"); void box.offsetWidth; box.classList.add("bump"); }
    t = setTimeout(step, v < target ? 40 : 900);
  };
  new IntersectionObserver(([e]) => { on = e.isIntersecting; clearTimeout(t); if (on) step(); }).observe(el);
}
runaway($(".ill-count b[data-to]"), 248, true);
runaway($(".fb-n"), 248, false);
