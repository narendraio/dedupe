/* Realistic noisy logs for the try-it pad and the hero stream.
   Generated from templates with a seeded RNG, so the sample text is stable between visits. */

export function rng(seed = 7) {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const pad = (n, w = 2) => String(n).padStart(w, "0");
const hex = (r, n) => Array.from({ length: n }, () => "0123456789abcdef"[Math.floor(r() * 16)]).join("");
const uuid = (r) => `${hex(r, 8)}-${hex(r, 4)}-4${hex(r, 3)}-${"89ab"[Math.floor(r() * 4)]}${hex(r, 3)}-${hex(r, 12)}`;
const int = (r, a, b) => a + Math.floor(r() * (b - a + 1));
const pick = (r, a) => a[Math.floor(r() * a.length)];

/** Weighted picker: [[weight, fn], …] */
function mix(r, table) {
  const total = table.reduce((s, [w]) => s + w, 0);
  return (force) => {
    if (force) return table[table.length - 1][1];
    let x = r() * total;
    for (const [w, fn] of table) if ((x -= w) < 0) return fn;
    return table[table.length - 1][1];
  };
}

/* A clock that moves forward a little on every line. */
function clock(r, start = 12 * 3600 + 4 * 60) {
  let t = start * 1000;
  return () => {
    t += int(r, 20, 900);
    const s = Math.floor(t / 1000);
    return { h: pad(Math.floor(s / 3600) % 24), m: pad(Math.floor(s / 60) % 60), s: pad(s % 60), ms: pad(t % 1000, 3), us: pad(int(r, 0, 999999), 6), epoch: 1790510400 + s };
  };
}

/* ── nginx: access + error log ───────────────────────────── */
function nginxLine(r, now, force) {
  const ip = () => `10.0.${int(r, 1, 6)}.${int(r, 2, 250)}`;
  const t = () => { const c = now(); return `27/Sep/2026:${c.h}:${c.m}:${c.s} +0000`; };
  const et = () => { const c = now(); return `2026/09/27 ${c.h}:${c.m}:${c.s}`; };
  const ua = ["Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15", "kube-probe/1.30", "curl/8.7.1", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X)"];
  return mix(r, [
    [30, () => `${ip()} - - [${t()}] "GET /healthz HTTP/1.1" 200 2 "-" "kube-probe/1.30"`],
    [16, () => `${ip()} - - [${t()}] "GET /api/products/${int(r, 100, 9999)} HTTP/1.1" 200 ${int(r, 800, 4200)} "-" "${pick(r, ua)}"`],
    [7, () => `${ip()} - - [${t()}] "POST /api/cart HTTP/1.1" 201 ${int(r, 90, 140)} "https://shop.example.com/" "${ua[0]}"`],
    [12, () => `${et()} [error] 31#31: *${int(r, 1000, 99999)} connect() failed (111: Connection refused) while connecting to upstream, client: ${ip()}, server: shop, request: "GET /api/cart HTTP/1.1", upstream: "http://10.0.5.12:8080/api/cart", host: "shop.example.com"`],
    [6, () => `${ip()} - - [${t()}] "GET /api/cart HTTP/1.1" 502 157 "-" "${ua[0]}"`],
    [5, () => `${et()} [warn] 31#31: *${int(r, 1000, 99999)} an upstream response is buffered to a temporary file /var/cache/nginx/proxy_temp/${int(r, 1, 9)}/${pad(int(r, 0, 99))}/00000${int(r, 10000, 99999)} while reading upstream`],
    [1, () => `${et()} [crit] 31#31: *${int(r, 1000, 99999)} SSL_do_handshake() failed (SSL: error:0A00006C:SSL routines::bad key share) while SSL handshaking, client: ${ip()}, server: 0.0.0.0:443`],
  ])(force)();
}

/* ── kubernetes: kubectl logs + klog ─────────────────────── */
function k8sLine(r, now, force) {
  const pod = () => `api-7d9f8b6c5-${pick(r, ["x2kqp", "m8zlw", "q4tnc"])}`;
  const iso = () => { const c = now(); return `2026-09-27T${c.h}:${c.m}:${c.s}.${c.ms}Z`; };
  const klog = (lvl) => { const c = now(); return `${lvl}0927 ${c.h}:${c.m}:${c.s}.${c.us}       1`; };
  return mix(r, [
    [26, () => `${iso()} ERROR db: query timeout after ${int(r, 3000, 3200)}ms (conn=${int(r, 1, 48)})`],
    [16, () => `${iso()} INFO  GET /api/orders/${int(r, 1000, 9999)} 200 ${int(r, 4, 90)}ms`],
    [10, () => `${iso()} WARN  cache miss key="user:${int(r, 10, 999)}" node=10.0.0.${int(r, 2, 90)}`],
    [9, () => `${klog("I")} reflector.go:${pick(r, [138, 255])}] watch of *v1.Pod ended with: too old resource version: ${int(r, 1000000, 9999999)} (${int(r, 1000000, 9999999)})`],
    [8, () => `${klog("E")} controller.go:116] "Observed a panic" pod="default/${pod()}" panic="runtime error: invalid memory address or nil pointer dereference"`],
    [6, () => `${iso()} INFO  healthcheck ok`],
    [4, () => `${klog("W")} prober.go:104] Readiness probe failed: Get "http://10.244.${int(r, 0, 3)}.${int(r, 2, 250)}:8080/ready": context deadline exceeded`],
    [2, () => `${iso()} ERROR payment provider returned 503 for charge ch_3Nf8Qz`],
    [1, () => `${klog("E")} kuberuntime_manager.go:1256] "Back-off restarting failed container" container="api" pod="default/${pod()}" podUID=${uuid(r)}`],
  ])(force)();
}

/* ── JSON app (structured logs) ──────────────────────────── */
function jsonLine(r, now, force) {
  const time = () => { const c = now(); return `2026-09-27T${c.h}:${c.m}:${c.s}.${c.ms}Z`; };
  return mix(r, [
    [24, () => `{"time":"${time()}","level":"info","msg":"request completed","method":"GET","path":"/api/users/${int(r, 1, 9999)}","status":200,"duration_ms":${int(r, 2, 80)},"request_id":"${uuid(r)}"}`],
    [16, () => `{"time":"${time()}","level":"error","msg":"db timeout","query":"SELECT * FROM orders WHERE user_id = ${int(r, 1, 9999)}","elapsed_ms":${int(r, 5000, 5100)},"trace_id":"${hex(r, 32)}"}`],
    [10, () => `{"time":"${time()}","level":"warn","msg":"cache miss","key":"session:${hex(r, 12)}","node":"10.0.0.${int(r, 2, 90)}"}`],
    [7, () => `{"time":"${time()}","level":"info","msg":"job finished","job":"email-digest","batch":${int(r, 100, 999)},"sent":${int(r, 10, 500)}}`],
    [5, () => `{"time":"${time()}","level":"error","msg":"disk full","path":"/var/lib/app/uploads","free_bytes":${int(r, 0, 4096)}}`],
    [3, () => `{"time":"${time()}","level":"warn","msg":"rate limited","client":"${pick(r, ["ios", "android", "web"])}","retry_after":${int(r, 1, 30)}}`],
    [1, () => `{"time":"${time()}","level":"error","msg":"payment declined","error":"card_expired","customer":"cus_${hex(r, 10)}","amount":${int(r, 500, 90000)}}`],
  ])(force)();
}

const makers = { nginx: nginxLine, k8s: k8sLine, json: jsonLine };

/** A fixed sample log of n lines. */
export function sample(kind, n = 60, seed = 11) {
  const r = rng(seed + kind.length * 101);
  const now = clock(r);
  const make = makers[kind];
  // the rare, important line always shows up once, about two thirds of the way down
  const rare = Math.floor(n * 0.64);
  return Array.from({ length: n }, (_, i) => make(r, now, i === rare)).join("\n");
}

/** An endless stream for the hero: returns a function that yields the next line. */
export function stream(kind = "k8s", seed = Date.now() % 100000) {
  const r = rng(seed);
  const now = clock(r);
  const make = makers[kind];
  return () => make(r, now);
}
