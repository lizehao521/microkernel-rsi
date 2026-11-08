# -*- coding: utf-8 -*-
"""免检通道普查（两口径对开）—— 回答看板 #55 卡片末尾那句「还有没有别的免检通道，没数」。

背景（为什么要另起一支，而不是引用 10-17 那份）：
  `cell_rule_readers.py` 按**登记行第三参那个表达式**（`m.Budget.ContentCeiling`）在源码里找出现处，
  于是数出 19 格嫌疑名单（7 只搬动 ＋ 12 除登记外没人碰）。那份表自己在末尾写了它的边界：
  「同名表达式跨 struct 会并起来数……要说某一格确实没有判据读它，得人工看那几行」。
  这一支把那「人工几行」做成机械判据：**口径二 = 字段名 + 形状**，认三种它不认的形状：
    · 换接收者写（`b.ContentCeiling` / `f.Detail`）——登记式全文匹配看不见；
    · 表驱动接线（`[]struct{name string; m Measure}{{"分帧数", m.Chunks}, …}` 后面那个循环里的 `s.m.Check(s.name)`）；
    · `return` 里直调校验器（`return checkFingerprint("原话指纹", s.SHA256)`）——那一行没有 if，被旧口径记成「搬动」。
  校验器名单不许我手抄：从源码 `func` 声明现取（名字以 check 开头/结尾、或就叫 Check/check），
  且每个取到的名字在全仓必须命中 ≥1 次，取到 0 个就 exit 6 —— 名字漂了尺子当场红，不会静默把「有闸」报成「没闸」。

三态不塌：派生格（值来自方法，调用方无法独立填）单独一态，不混进「免检」；
静态分不开键名那批（动态登记 `CutLists` 一族）记 `absent（静态分不开）`，不记 0。

反臂（子发写 TEMP，绝不与母件同名）：
  EXPECTCTL=1 → 塞一条假的逐名预期（把 cost_countable 写成「有判据」）→ 该 exit 6；
  VALIDCTL=1 → 校验器词表换成一个全仓不存在的名字 → 该 exit 6（词表锚不成立）；
  MISSCTL=1  → 必需源件换成不存在的文件名 → 该 exit 7。
正对照（钉尺子，任一不符 exit 6）：`m.Tokens`/`m.WireBytesIn`/`m.Streaming` 三格必须判成「有判据」。

跑法：python tasks/probe/exempt_channel_census.py http://127.0.0.1:<我的端口> [输出件路径]
      只读源码 + 一发 kernel.celltable（只读取件），不写内核账、不动树。
"""
import io
import json
import os
import re
import sys
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
CTL = next((k for k in ("EXPECTCTL", "VALIDCTL", "MISSCTL") if os.environ.get(k) == "1"), "")
EXPECTCTL = CTL == "EXPECTCTL"
VALIDCTL = CTL == "VALIDCTL"
MISSCTL = CTL == "MISSCTL"

if CTL:
    OUT = os.path.join(tempfile.gettempdir(), "stdio-1019",
                       "exempt-child-%s-%s.txt" % (CTL, time.strftime("%H%M%S")))
    FORCE = "1"
else:
    OUT = sys.argv[2] if len(sys.argv) > 2 else os.path.join(
        REPO, "报告", "免检通道普查-两口径对开-1019-%s.txt" % time.strftime("%H%M%S"))
    FORCE = os.environ.get("FORCE", "")

REG = re.compile(r'(cell|measureCell|relayCell)\(\s*"([^"]+)",\s*"([^"]+)",\s*([A-Za-z0-9_.()\[\]+, ]+?)[,)]')
RENDER = re.compile(r'Errorf|Sprintf|claimOf')
COND = re.compile(r'\bif\b|==|!=|&&|\|\||\bfor\b|<=|>=|<|>')
VALID_CALL = re.compile(r'\.(Check|check)\(|\b(check[A-Za-z_]*)\(')
TABLE_ROW = re.compile(r'\{\s*"[^"]+"\s*,\s*[A-Za-z0-9_.()]+')


def die(code, tag, msg):
    sys.stdout.buffer.write((("%s: %s" % (tag, msg))).encode("utf-8", "replace"))
    sys.stdout.buffer.write(b"\n")
    if CTL:
        # 反臂子发死在自己那道上就收口，绝不回头写母件（叶子停 = 这一发不产归档）
        sys.stdout.buffer.write(b"CHILD-LEAF-STOP=True\n")
    sys.exit(code)


def rel(p):
    return os.path.relpath(p, REPO).replace(os.sep, "/")


def scan_go():
    files = []
    for dp, dn, fn in os.walk(REPO):
        dn[:] = [d for d in dn if d not in (".hermess-snapshots", ".qoder", "node_modules")]
        for f in fn:
            if f.endswith(".go") and not f.endswith("_test.go"):
                files.append(os.path.join(dp, f))
    return dict((rel(p), io.open(p, encoding="utf-8", errors="replace").read()) for p in files)


TEXTS = scan_go()
REQUIRE = ["cells.go", "input.go", "output.go", "record.go", "measure.go", "kernel.go"]
if MISSCTL:
    REQUIRE = REQUIRE + ["tasks/probe/__不存在的必需源件__.go"]
miss = [f for f in REQUIRE if f not in TEXTS]
if miss:
    die(7, "MISS", "必需源件缺位（分母不成立，不落盘）：%s" % " ".join(miss))

# —— 校验器词表：从源码 func 声明现取，不许手抄 ——
#    认的是「这个函数能造出拒收」= 声明里返回 error；按名字形状取会漏掉 `usageOK` 那一族（10-19 这一发实测漏的）。
FUNC_DECL = re.compile(r'^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(')
VALIDATORS = set()
for f, t in TEXTS.items():
    for line in t.split("\n"):
        m = FUNC_DECL.match(line.strip())
        if m and re.search(r'\berror\b', line) and not m.group(1).startswith("new"):
            VALIDATORS.add(m.group(1))
VALIDATORS = sorted(VALIDATORS)
if VALIDCTL:
    VALIDATORS.append("checkZzzNeverInTheDocument")
if not VALIDATORS:
    die(6, "VOCAB-BAD", "校验器词表取到 0 个")
vocab_rows = dict((v, sum(1 for t in TEXTS.values() for ln in t.split("\n")
                          if re.search(r'\b' + v + r'\b', ln))) for v in VALIDATORS)
zero_vocab = [v for v, n in vocab_rows.items() if n == 0]
if zero_vocab:
    die(6, "VOCAB-BAD", "词表里这些名字全仓命中 0 次（名字漂了）：%s" % " ".join(zero_vocab))

# —— 表驱动接线：一行是 `{"标签", m.X}` 这种表字面量，且它下面 12 行内真有把循环变量交给校验器的调用 ——
TABLE_LOOPS = {}
for f, t in TEXTS.items():
    lines = t.split("\n")
    for ln, line in enumerate(lines, 1):
        if not TABLE_ROW.search(line):
            continue
        ahead = "\n".join(lines[ln:min(len(lines), ln + 12)])
        if any(re.search(r'\b' + v + r'\s*\(', ahead) for v in VALIDATORS):
            TABLE_LOOPS.setdefault(f, set()).add(ln)

# —— 分母一：登记行现读 ——
rows = []
for f, t in sorted(TEXTS.items()):
    for ln, line in enumerate(t.split("\n"), 1):
        m = REG.search(line)
        if m:
            expr = m.group(4).strip()
            while expr.endswith(")") and expr.count("(") < expr.count(")"):
                expr = expr[:-1]
            rows.append((m.group(2), m.group(3), expr, f, ln))
KEYS = sorted(set(k for k, _f, _e, _fn, _ln in rows))
if not KEYS:
    die(7, "DENOM-BAD", "登记行抠出 0 格")

# —— 分母二：内核自报键名（只读取件，端点必须 http 开头）——
ENDPOINT = sys.argv[1] if len(sys.argv) > 1 else ""
if not ENDPOINT.startswith("http"):
    die(4, "BAD_ARGV", "第一个位置参数是端点 URL（要 http 开头），拿到的是 %r" % ENDPOINT[:60])
body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                   "params": {"name": "kernel.celltable", "arguments": {}}}).encode("utf-8")
try:
    r = json.loads(urllib.request.urlopen(urllib.request.Request(
        ENDPOINT.rstrip("/") + "/mcp", data=body,
        headers={"Content-Type": "application/json"}), timeout=60).read().decode("utf-8"))
except Exception as e:
    die(7, "ENDPOINT-BAD", "kernel.celltable 这一发没答：%r" % (e,))
ct = "".join(c.get("text", "") for c in (r.get("result") or {}).get("content", []) if c.get("type") == "text")
kernel_keys = sorted(set(re.findall(r'^\s*(?:input|output)\s+(\S+)\s+←', ct, re.M)))
if len(kernel_keys) < 40:
    die(7, "CELLTABLE-BAD", "端点交回的键名只有 %d 个（该 ≥40）" % len(kernel_keys))


def field_ident(field):
    return re.sub(r"\(\)$", "", field).split(".")[-1]


def is_derived(expr, field):
    return bool(re.search(r'\.\w+\(\)', expr)) or bool(re.search(r'\(\)$', field))


# —— 函数归属表：条件读只有落在**校验器函数体内**才算闸（其余是派生式或搬运，另立一栏不静默丢）——
FUNC_RX = re.compile(r'^func\s+\(\w*\s*\*?([A-Za-z_][A-Za-z0-9_]*)\)\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(')
FUNC_ANY = re.compile(r'^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(')
TYPE_RX = re.compile(r'^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+struct\s*\{')
FIELD_RX = re.compile(r'^([A-Z][A-Za-z0-9_]*)\s+[^\s].*(//|$)')


def func_map(t):
    """返回 {行号: (函数名, 接收者类型)}（大括号配对粗算，够用于「这行在哪个 func 里、谁的」这一问）。"""
    out = {}
    name, rt = "<文件作用域>", ""
    depth = 0
    started = False
    for ln, line in enumerate(t.split("\n"), 1):
        s = line.strip()
        m = FUNC_RX.match(s)
        if m:
            rt, name = m.group(1), m.group(2)
        elif FUNC_ANY.match(s) and depth == 0:
            rt, name = "", FUNC_ANY.match(s).group(1)
        out[ln] = (name, rt)
        if depth == 0 and started and s == "}":
            name, rt = "<文件作用域>", ""
            started = False
            continue
        if s.startswith("//"):
            continue
        opens = line.count("{") - line.count("}")
        if opens:
            depth += opens
            started = True
            if depth <= 0:
                depth = 0
                name, rt = "<文件作用域>", ""
                started = False
    return out


def owner_types(t):
    """{标识名: {拥有它的 struct 类型}} —— 字段来自 struct 声明，方法来自 func 接收者。"""
    own = {}
    cur = None
    for line in t.split("\n"):
        s = line.strip()
        mt = TYPE_RX.match(s)
        if mt:
            cur = mt.group(1)
            continue
        if cur:
            if s.startswith("}"):
                cur = None
                continue
            fm = FIELD_RX.match(s)
            if fm:
                own.setdefault(fm.group(1), set()).add(cur)
        mm = FUNC_RX.match(s)
        if mm:
            own.setdefault(mm.group(2), set()).add(mm.group(1))
    return own


OWN = {}
for _f, _t in TEXTS.items():
    for k, v in owner_types(_t).items():
        OWN.setdefault(k, set()).update(v)
FM = dict((f, func_map(t)) for f, t in TEXTS.items())
VSET = set(VALIDATORS)


def classify(key, field, expr, gates=None):
    """命中只认两种写法：`<父路径>.字段`（如 m.Notice.Sent），或那一行所在函数的接收者类型正好拥有这个字段。

    为什么要这一道：10-17 那份表按裸名数会串味 —— `Sent` 既是 OutputMeta 的方法（格 sent），
    又是 `Notice.Sent`（格 notice_sent）；上一版我按裸名把 notice_sent 那五条闸读成了 sent 的。

    gates = 哪些函数名算「闸」。宽的那一把凡返回 error 的函数都算（连 RecordCall 那种搬运的也算，会高报「有判据」）；
    严的那一把只认名字以 Check/check 起的那些。两把都跑，逐格对开 —— 「免检 = 0」这句只有在两把都成立时才说得出。
    """
    G = VSET if gates is None else set(gates)
    ident = field_ident(field)
    parent = re.sub(r"\(\)$", "", field).split(".")
    ppath = re.compile(r'\b' + r'\.'.join(re.escape(x) for x in parent[:-1]) + r'\.' + re.escape(ident) + r'\b') \
        if len(parent) > 1 else None
    rx = re.compile(r'\b' + re.escape(ident) + r'\b')
    owners = OWN.get(ident, set())
    reg_hits, val_hits, cond_hits, tbl_hits, rend_hits, move_hits, outside = [], [], [], [], [], [], []
    for f, t in sorted(TEXTS.items()):
        fm = FM[f]
        for ln, line in enumerate(t.split("\n"), 1):
            if not rx.search(line):
                continue
            s = line.strip()
            if REG.search(line) and '"%s"' % key in line:
                reg_hits.append("%s:%d" % (f, ln))
                continue
            if s.startswith("//"):
                continue
            fname, frt = fm[ln]
            if fname == ident:
                outside.append("%s:%d[定义体 %s]" % (f, ln, fname))
                continue
            tied = frt in owners or bool(ppath and ppath.search(line))
            if not tied:
                continue
            called = any(re.search(re.escape(v) + r'\s*\(', line) for v in VALIDATORS)
            in_gate = fname in G
            if called and in_gate:
                val_hits.append("%s:%d[%s]" % (f, ln, fname))
            elif ln in TABLE_LOOPS.get(f, ()) and TABLE_ROW.search(line) and in_gate:
                tbl_hits.append("%s:%d[%s]" % (f, ln, fname))
            elif COND.search(s) and in_gate:
                cond_hits.append("%s:%d[%s]" % (f, ln, fname))
            elif RENDER.search(s):
                rend_hits.append("%s:%d[%s]" % (f, ln, fname))
            elif in_gate:
                move_hits.append("%s:%d[%s]" % (f, ln, fname))
            else:
                outside.append("%s:%d[闸外 %s]" % (f, ln, fname))
    judged = len(val_hits) + len(cond_hits) + len(tbl_hits)
    if judged:
        tag = "有判据"
    elif is_derived(expr, field):
        tag = "派生格（无判据读，但调用方填不了它）"
    elif rend_hits:
        tag = "只渲染"
    else:
        tag = "免检通道（无判据读、可独立填）"
    ev = (val_hits + tbl_hits + cond_hits + rend_hits + move_hits + reg_hits)[:4]
    return tag, judged, len(val_hits), len(cond_hits), len(tbl_hits), len(rend_hits), len(move_hits), ev, outside, outside


CENSUS = {}
for key, field, expr in [(k, f, e) for k, f, e, _fn, _ln in rows]:
    if key in CENSUS:
        continue
    CENSUS[key] = classify(key, field, expr) + (field, expr)

STRICT = [v for v in VALIDATORS if v == "Check" or v == "check" or v.lower().startswith("check")]
CENSUS_STRICT = {}
for key, field, expr in [(k, f, e) for k, f, e, _fn, _ln in rows]:
    if key in CENSUS_STRICT:
        continue
    CENSUS_STRICT[key] = classify(key, field, expr, gates=STRICT) + (field, expr)

# —— 正对照 + 逐名预期（写死我自己这一轮人工看过的那 12 条；不符就 exit 6，不落盘）——
CONTROLS = [("tokens", "有判据"), ("wire_bytes_in", "有判据"), ("streaming", "有判据"),
            ("chunks", "有判据"), ("first_byte_ms", "有判据"), ("max_gap_ms", "有判据"),
            ("fail_detail", "有判据"), ("fail_kind", "有判据"), ("retry_after_ms", "有判据"),
            ("model_says_sha256", "有判据"), ("fixed_overhead", "有判据"),
            ("leftover_pct", "有判据"), ("content_ceiling", "有判据"), ("clamp_rounds", "有判据"),
            ("call_bytes_ceiling", "有判据"), ("transport_slack_pct", "有判据"),
            ("shares", "有判据"), ("model_says_kept", "有判据"),
            ("fail_status", "有判据"),
            ("usage_in", "有判据"), ("usage_out", "有判据"),
            ("cost_countable", "派生格（无判据读，但调用方填不了它）"),
            ("sent", "派生格（无判据读，但调用方填不了它）"),
            ("model_says_clipped", "派生格（无判据读，但调用方填不了它）")]
if EXPECTCTL:
    CONTROLS = CONTROLS + [("cost_countable", "有判据")]
bad = []
for key, want in CONTROLS:
    got = CENSUS.get(key)
    if got is None:
        bad.append("%s 抠不到（登记行里没有这一格）" % key)
    elif got[0] != want:
        bad.append("%s 判成「%s」，我写的预期是「%s」" % (key, got[0], want))
if bad:
    die(6, "EXPECT-BAD", "逐名预期不成立：%s" % " ｜ ".join(bad))

# fail_status 那一格要的两个数分开看（#55 的准确口径靠这两个数）
fs = CENSUS["fail_status"]
if not (fs[1] > 0 and fs[2] == 0):
    die(6, "K55-BAD", "fail_status 的「条件读 >0 且校验器接线 =0」这一形状今天不成立：判据行 %d / 校验器 %d" % (fs[1], fs[2]))
fs_s = CENSUS_STRICT["fail_status"]
if not (fs_s[1] > 0 and fs_s[2] == 0):
    die(6, "K55-BAD", "严口径下 fail_status 那一形状也须成立：判据行 %d / 校验器 %d" % (fs_s[1], fs_s[2]))
# 表驱动那一族在严口径下必须仍然算「有判据」——上一版就是在这里判成免检的（usageOK 不在按名字取的词表里）
for k in ("usage_in", "usage_out", "chunks", "first_byte_ms", "max_gap_ms"):
    if CENSUS_STRICT[k][0] != "有判据":
        die(6, "TABLE-BAD", "严口径把 %s 判成「%s」，该是「有判据」（表驱动接线那条没认出来）" % (k, CENSUS_STRICT[k][0]))

L = []
A = L.append
ts = time.strftime("%Y-%m-%d %H:%M:%S")
A("免检通道普查 · 两口径对开（10-19 午后）")
A("口径一 = 旧尺子（登记行第三参那个表达式全文匹配，`cell_rule_readers.py`）；口径二 = 这一支（字段名 + 三种接线形状）。")
A("两端点分开报，分歧逐名归因 —— 这一支的用途就是把 10-17 那份表末尾「得人工看那几行」那句换成机械判据。")
A("")
A("【一】分母自印")
A("  全仓非测试 .go = %d 份（旧尺子只扫 9 份）｜ 逐名：%s" % (len(TEXTS), " ".join(sorted(TEXTS))))
A("  校验器词表从源码 func 声明现取 = %d 个 ｜ 逐名：%s" % (len(VALIDATORS), " ".join(VALIDATORS)))
A("  词表锚（每个名字全仓命中次数，任一为 0 我就不落盘）：%s"
  % " ".join("%s=%d" % (v, vocab_rows[v]) for v in VALIDATORS))
A("  登记行抠出的格 = %d ｜ 键名去重 = %d ｜ 内核自报键名 = %d ｜ 两边都对上 = %d"
  % (len(rows), len(KEYS), len(kernel_keys), len(set(KEYS) & set(kernel_keys))))
only_k = sorted(set(kernel_keys) - set(KEYS))
only_m = sorted(set(KEYS) - set(kernel_keys))
A("  内核有、静态抠不到的（=%d）：%s" % (len(only_k), " ".join(only_k) or "无"))
A("    ⇒ 这一批走动态登记（`cells.go` 的 CutLists 循环那一族），逐格分类记 `absent（静态分不开）`，不记 0、也不参与下面的归类计数。")
A("  静态抠到、内核没报的（=%d）：%s" % (len(only_m), " ".join(only_m) or "无"))
_anchor = " ".join("%s:%s" % (f, "、".join(str(x) for x in sorted(v))) for f, v in sorted(TABLE_LOOPS.items()))
A("  表驱动 Check 的锚：%s" % (_anchor or "无"))
A("")
A("【二】19 格嫌疑名单逐格定论（10-17 那份表的嫌疑名单，逐名在这里翻案或坐实）")
SUSPECT = ["content_ceiling", "clamp_rounds", "chunks", "first_byte_ms", "max_gap_ms", "fail_kind",
           "fail_detail", "call_bytes_ceiling", "transport_slack_pct", "leftover_pct", "fixed_overhead",
           "shares", "model_says_kept", "model_says_clipped", "model_says_sha256", "fail_status",
           "retry_after_ms", "sent", "cost_countable"]
A("  键名 ｜ 旧口径归类 ｜ 新口径判定 ｜ 校验器接线 ｜ 条件读 ｜ 表驱动 ｜ 闸外读 ｜ 证据（file:line[所属函数]）")
OLD = {"content_ceiling": "只搬动", "clamp_rounds": "只搬动", "chunks": "只搬动", "first_byte_ms": "只搬动",
       "max_gap_ms": "只搬动", "fail_kind": "只搬动", "fail_detail": "只搬动",
       "call_bytes_ceiling": "除登记外没人碰", "transport_slack_pct": "除登记外没人碰",
       "leftover_pct": "除登记外没人碰", "fixed_overhead": "除登记外没人碰", "shares": "除登记外没人碰",
       "model_says_kept": "除登记外没人碰", "model_says_clipped": "除登记外没人碰",
       "model_says_sha256": "除登记外没人碰", "fail_status": "除登记外没人碰",
       "retry_after_ms": "除登记外没人碰", "sent": "除登记外没人碰", "cost_countable": "除登记外没人碰"}
翻案 = []
for k in SUSPECT:
    g = CENSUS[k]
    A("  %-22s ｜ %-16s ｜ %-34s ｜ %d ｜ %d ｜ %d ｜ %d ｜ %s"
      % (k, OLD[k], g[0], g[2], g[3], g[4], len(g[8]), " ".join(g[7])))
    if g[8]:
        A("       闸外读（值被谁搬/算，不算闸）：%s" % " ｜ ".join(g[8][:4]))
    if OLD[k] != "有判据" and g[1] > 0:
        翻案.append(k)
A("  ⇒ 旧表那 19 格里，被新口径翻成「其实有判据读」的 = %d 格 ｜ 逐名：%s" % (len(翻案), " ".join(翻案)))
A("")
A("【三】全表归类（两口径都跑，不只嫌疑名单）")
buck = {}
for k in sorted(CENSUS):
    buck.setdefault(CENSUS[k][0], []).append(k)
for tag in sorted(buck):
    A("  %-36s %d 格 ｜ %s" % (tag, len(buck[tag]), " ".join(buck[tag])))
A("  对开：归类计数合计 %d ＋ 静态分不开 %d = %d（该等于 %d）"
  % (sum(len(v) for v in buck.values()), len(only_k), sum(len(v) for v in buck.values()) + len(only_k), len(kernel_keys)))
sbuck = {}
for k in sorted(CENSUS_STRICT):
    sbuck.setdefault(CENSUS_STRICT[k][0], []).append(k)
A("  严口径（只认名字以 Check/check 起的 %d 个函数算闸；宽的那一把连搬运 error 的函数也算，会高报）：%s"
  % (len(STRICT), " ".join(STRICT)))
for tag in sorted(sbuck):
    A("    %-36s %d 格 ｜ %s" % (tag, len(sbuck[tag]), " ".join(sbuck[tag])))
分 = [k for k in CENSUS if CENSUS[k][0] != CENSUS_STRICT[k][0]]
A("    两把尺子判定不一致的格 = %d ｜ 逐名：%s" % (len(分), " ".join(sorted(分)) or "无"))
for k in sorted(分):
    A("      %s ｜ 宽=%s ｜ 严=%s ｜ 宽边证据 %s ｜ 严边证据 %s"
      % (k, CENSUS[k][0], CENSUS_STRICT[k][0], " ".join(CENSUS[k][7]), " ".join(CENSUS_STRICT[k][7])))
A("")
A("【四】这一格能说什么 / 不能说什么")
免 = buck.get("免检通道（无判据读、可独立填）", [])
派 = [t for t in buck if t.startswith("派生格")]
A("  能说：静态穷举到全仓 %d 份非测试 .go，「无判据读、且调用方能自己往里写」这一族现在有名有名数 = %d（逐名：%s）。"
  % (len(TEXTS), len(免), " ".join(免) or "无"))
A("  能说：旧表那 19 格嫌疑名单**是口径产物**，不是 19 个洞 —— 引用它的人要连这句一起引。")
A("  能说：`fail_status` 那一格的准确形状是「条件读 %d 处、校验器接线 %d 处」——" % (fs[3], fs[2]))
A("        #55 说的不是「这一格没人碰」，是「碰它的几条都只看 Decidable()，没有一条走 Check() 那道『缺席却带数』的闸」。")
A("  不能说：这些判定在**运行时**也拦得住 —— 这一支只读源码，没起靶场；运行时那一侧另有 `lie_uncovered_sweep.py`。")
A("  不能说：静态分不开那 %d 格没有闸 —— 它们只是这一支按登记行分不出键名，读数记 absent，不记 0。" % len(only_k))
A("  不能推广：派生格「调用方填不了」这句的依据是格值由方法当场算出（`cells.go` 登记行的第三参就是那个方法调用），" )
A("        它不等于「派生格的输入也没人判」—— 输入格的判定看上面【三】那一行，不在这一句里。")
A("")
A("【五】这一发的分母自己印（收尾）")
A("  源件 %d ｜ 校验器名 %d ｜ 登记格 %d ｜ 键名对上 %d/%d ｜ 翻案 %d ｜ 免检（可独立填）%d ｜ 逐名预期 %d 条全中 ｜ 退出码 0"
  % (len(TEXTS), len(VALIDATORS), len(KEYS), len(set(KEYS) & set(kernel_keys)), len(kernel_keys),
     len(翻案), len(免), len(CONTROLS)))

leak = [i for i, ln in enumerate(L, 1) if re.search(r"%[sd]", ln)]
if leak:
    die(8, "FORMAT-LEAK", "正文有未格式化的占位符，行号 %s" % leak)

txt = "\n".join(L) + "\n"
if os.path.exists(OUT) and not FORCE:
    die(3, "REFUSE_TO_OVERWRITE", OUT)
io.open(OUT, "w", encoding="utf-8", newline="\n").write(txt)
print(txt.encode("unicode_escape", "backslashreplace").decode("ascii"))
print("WROTE %s ｜ %d 行 ｜ %d 字节" % (OUT, len(L) + 1, len(txt.encode("utf-8"))))
if CTL:
    print("CHILD-LEAF-STOP=True")
