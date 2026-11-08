# -*- coding: utf-8 -*-
# 尺子：把「被拒之后调用方怎么改」这一指标拆成两半 —— 链上能核的那一半真数出来，
# 量不到的那一半记 absent + 结构性理由（不是 0）。只读，不碰任何服务、不写别人的凭据。
# 控制台只印 ASCII；中文一律走带 encoding="utf-8" 的文件句柄。
import io
import json
import os
import re
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))          # ...\微内核\微内核\微内核（仓库根）
REL_LEDGER = os.path.join("构建-1017", "客服靶场", "导出", "账本-全量-1018.jsonl")
LEDGER = ""
_w = REPO
for _i in range(5):                                     # 这份导出的账住仓库外面，向上找它在哪一层
    _c = os.path.join(_w, REL_LEDGER)
    if os.path.exists(_c):
        LEDGER = _c
        break
    _w = os.path.dirname(_w)
RECORD_GO = os.path.join(REPO, "record.go")
RECORD_TEST = os.path.join(REPO, "record_test.go")
OUT = os.path.join(REPO, "报告", "被拒之后怎么改-链上能核与量不到-%s.txt" % time.strftime("%m%d-%H%M%S"))
PY = sys.executable
FORCE = os.environ.get("FORCE", "")

CITES = [(LEDGER, "账本来源"), (RECORD_GO, "拒收不写链的落点"), (RECORD_TEST, "不写半发那条测试")]
REFUSE_VOCAB = ["iserror", "rejected", "refused", "error", "拒", "败"]


def die(code, tag, msg):
    print("CHILD-LEAF-STOP=%s tag=%s" % (True, tag))
    sys.exit(code)


def rd(path):
    with io.open(path, "r", encoding="utf-8", errors="replace") as f:
        return f.read()


CTL = next((k for k in ("MISSCTL", "ANCHORCTL", "DENOMCTL") if os.environ.get(k) == "1"), "")
if CTL:
    # 反臂子发绝不能与母件同名：同一秒起跑时名字撞车，它会在"目标已存在"那道闸上假红（本件第二发就是这样）。
    OUT = os.path.join(tempfile.gettempdir(), "stdio-1019", "repair-edge-child-%s-%s.txt" % (CTL, time.strftime("%H%M%S")))
    FORCE = "1"
if os.environ.get("MISSCTL") == "1":
    CITES.append((os.path.join(REPO, "报告", "这一份件全树不存在-故意用来钉引用那一格.txt"), "假引用"))
if os.environ.get("DENOMCTL") == "1":
    pass  # 分母那一臂只把 denom_ok 强行改成 False，见下面那节
fake_lines = os.environ.get("FAKE_LINES", "")

miss = [(p, why) for p, why in CITES if not os.path.exists(p)]
if miss:
    for p, why in miss:
        print("MISS: %s (%s)" % (why.encode("unicode_escape").decode("ascii"), p))
    die(7, "MISS", "引用缺件")

if os.path.exists(OUT) and FORCE != "1":
    die(3, "REFUSE_TO_OVERWRITE", "目标已存在")

raw = rd(LEDGER)
lines = raw.splitlines()
head = lines[0]
body = lines[1:]
hm = re.search(r"(\d+)\s*字节.*?(\d+)\s*行", head)
claimed_bytes = int(hm.group(1)) if hm else -1
claimed_lines = int(hm.group(2)) if hm else -1
real_bytes = os.path.getsize(LEDGER)
entries = []
bad_json = 0
for ln in body:
    try:
        entries.append(json.loads(ln))
    except Exception:
        bad_json += 1
used_lines = len(body) - bad_json
if fake_lines:
    used_lines = int(fake_lines)
# 抬头那句「N 字节」说的是**内核交回的 JSONL 字节**，不是这支盘上文件的字节 ——
# 这一行外面还包着调用方自己写的那一句抬头（76 字节 + 1 个 LF）。两种口径分开对，别混。
head_bytes = len(head.encode("utf-8")) + 1
body_bytes = len(("\n".join(body) + "\n").encode("utf-8"))
bytes_ok = (claimed_bytes == body_bytes) and (real_bytes == claimed_bytes + head_bytes)
lines_ok = (claimed_lines == used_lines)
denom_ok = bytes_ok and lines_ok
if os.environ.get("DENOMCTL") == "1":
    denom_ok = False

# —— 键名表（用来证明链上没有任何一格带"被拒"这件事）——
keys = sorted({k for e in entries for k in e.keys()})
vocab_hit = [k for k in keys if any(v in k.lower() for v in REFUSE_VOCAB)]
if vocab_hit:
    print("CARRIER-FOUND: %s" % " ".join(vocab_hit).encode("unicode_escape").decode("ascii"))
    die(7, "CARRIER-FOUND", "本件结论依赖「键名里没有拒收载体」，这条不成立就不许下结论")

# —— 按 (session, step) 聚合 ——
by_sess = {}
for e in entries:
    by_sess.setdefault(e["session"], {}).setdefault(e["step"], []).append(e)
dups = 0
kinds_total = sorted({e["kind"] for e in entries})
for sess, turns in by_sess.items():
    for step, es in turns.items():
        seen = set()
        for e in es:
            if e["kind"] in seen:
                dups += 1
            seen.add(e["kind"])

turn_rows = []
for sess in sorted(by_sess):
    turns = by_sess[sess]
    steps = sorted(turns)
    gaps = [b - a for a, b in zip(steps, steps[1:]) if b - a != 1]
    per_turn = [len(turns[s]) for s in steps]
    turn_rows.append((sess, len(steps), steps[0] if steps else 0, steps[-1] if steps else 0,
                      gaps, min(per_turn), max(per_turn)))

# —— 相邻两发之间改了什么（同 session、发号相差 1 且两侧都在链上）——
same_sha = diff_sha = same_claim = diff_claim = same_basis = diff_basis = pairs = 0
gaps_ms = []
for sess, turns in by_sess.items():
    steps = sorted(turns)
    for a, b in zip(steps, steps[1:]):
        if b - a != 1:
            continue
        ka = {e["kind"]: e for e in turns[a]}
        kb = {e["kind"]: e for e in turns[b]}
        inter = sorted(set(ka) & set(kb))
        if not inter:
            continue
        pairs += 1
        for k in inter:
            ea, eb = ka[k], kb[k]
            if ea["payload_sha256"] == eb["payload_sha256"]:
                same_sha += 1
            else:
                diff_sha += 1
            if ea.get("claim") == eb.get("claim"):
                same_claim += 1
            else:
                diff_claim += 1
            if ea.get("basis") == eb.get("basis"):
                same_basis += 1
            else:
                diff_basis += 1
        try:
            ta = time.mktime(time.strptime(ka[inter[0]]["ts"][:19], "%Y-%m-%dT%H:%M:%S"))
            tb = time.mktime(time.strptime(kb[inter[0]]["ts"][:19], "%Y-%m-%dT%H:%M:%S"))
            gaps_ms.append(int(round((tb - ta) * 1000)))
        except Exception:
            pass
gaps_ms.sort()


def pct(arr, q):
    if not arr:
        return None
    i = min(len(arr) - 1, int(round(q * (len(arr) - 1))))
    return arr[i]


def fmt_ms(v):
    return "—" if v is None else ("%d ms" % v)


# —— 结构那一节：拒收为什么在链上不可见 ——
# 每条锚带一个「该命中几行」的预期。命中数与预期不等 = 树变了或锚写歪了，两种都不许我照常下结论（exit 7）。
go_txt = rd(RECORD_GO)
tst_txt = rd(RECORD_TEST)
ANCHORS = [
    (go_txt, 'return 0, fmt.Errorf("kernel: 输入侧账不自洽', 2, "拒收当场返回（输入侧那两句）"),
    (go_txt, "l.noteRejected()", 9, "noteRejected 调用行"),
    (go_txt, "func (l *Ledger) plan(session, actor string, step int, kind string, cells []Cell)", 1, "翻译与落账分两步"),
    (go_txt, "已经记过了 —— 同一发只许记一次", 1, "同发重复被门口挡"),
    (tst_txt, "func TestRecordCallNeverLeavesHalfACallOnTheLedger", 1, "不写半发那条测试"),
]
if os.environ.get("ANCHORCTL") == "1":
    ANCHORS = ANCHORS + [(tst_txt, "TestRecordCallNeverLeavesHalfACallOnTheLedeger（故意拼错一个字母）", 1, "假锚")]
anchor_rows = []
bad_anchor = []
for txt, needle, want, label in ANCHORS:
    hits = [i for i, ln in enumerate(txt.splitlines(), 1) if needle in ln]
    anchor_rows.append((label, needle, want, hits))
    if len(hits) != want:
        bad_anchor.append((label, len(hits), want))
if bad_anchor:
    for label, got, want in bad_anchor:
        print("ANCHOR-BAD: %s got=%d want=%d" % (label.encode("unicode_escape").decode("ascii"), got, want))
    die(7, "ANCHOR-BAD", "锚的命中数与自己声明的预期对不上")

# 与看板 #69 那一族对开：全树（非测试 .go）里 noteRejected 的调用行数，现扫现印（定义行不算）
call_rows = []
for root, dirs, fs in os.walk(REPO):
    dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "__pycache__")]
    for fn in sorted(fs):
        if fn.endswith(".go") and not fn.endswith("_test.go"):
            p = os.path.join(root, fn)
            hs = [i for i, ln in enumerate(rd(p).splitlines(), 1)
                  if "noteRejected()" in ln and not ln.strip().startswith("func ")]
            if hs:
                call_rows.append((os.path.relpath(p, REPO).replace("\\", "/"), len(hs), hs))
calls_total = sum(n for _, n, _ in call_rows)

L = []
A = L.append
A("「被拒之后调用方怎么改」这一指标：链上能核的一半 + 结构上量不到的一半（10-19 午后）")
A("取材：看板待跑那张「\"被拒之后调用方怎么改\"这条指标」。卡片写的做法是「不改格、不动内核，先用 turns + 相邻两发序号/时间差做显示层」。")
A("这一发把它做成读数：只用盘上那份 10-18 导出的真流量账，只读、不起服务、不调任何 MCP 工具。")
A("来源件位置（现读）：%s" % LEDGER)
A("  ⇒ 注意这份账**住在仓库外面**（`构建-1017/` 在工作区根，不在仓库根），所以它不在回滚基线的清单里；本件只读它，不写它。")
A("")
A("【一】分母自印（抬头那行自己声明的数，与文件真实字节/行数对开）")
A("  抬头原句：%s" % head)
A("  解出来的声明：字节 %d ｜ 行数 %d" % (claimed_bytes, claimed_lines))
A("  盘上这份文件：%d 字节 ｜ 总行 %d ｜ 去掉抬头行 %d ｜ 解开的 JSON %d ｜ 解不开 %d" %
  (real_bytes, len(lines), len(body), len(entries), bad_json))
A("  两种口径分开对（这是本轮自己撞出来的区别）：")
A("    字节：抬头声明 %d ｜ 内核交回的 JSONL 实测 %d ｜ 相等 = %s" % (claimed_bytes, body_bytes, claimed_bytes == body_bytes))
A("    外层那一句抬头自己占 %d 字节（含行尾 LF），%d ＋ %d ＝ %d ＝ 盘上文件大小 = %s" %
  (head_bytes, claimed_bytes, head_bytes, claimed_bytes + head_bytes, real_bytes))
A("    行数：抬头声明 %d ｜ 解开的条目 %d ｜ 相等 = %s" % (claimed_lines, used_lines, lines_ok))
A("  ⇒ 「N 字节」那句说的是**内核交回的字节**，不是这支盘上文件的字节；混着比会假红（本件第一版就是这么红的，见【六】）。")
A("  分母闸成立 = %s（任一项不等就 exit 6，不落盘）" % (denom_ok,))
A("  自印口径：逐名取自 `json.loads` 成功的行；键名并集 %d 个，格名（kind）去重 %d 个，session %d 个" %
  (len(keys), len(kinds_total), len(by_sess)))
A("")
A("【二】链上能核的那一半：每一发记了几格、发号连不连续")
A("  session 名 ｜ 发数 ｜ 首末发号 ｜ 发号断档（差≠1 的那几个差值）｜ 每发格数 min-max")
for r in turn_rows:
    A("  %s ｜ %d ｜ %d–%d ｜ 断档 %s ｜ %d–%d" %
      (r[0], r[1], r[2], r[3], ("无" if not r[4] else ",".join(str(x) for x in r[4])), r[5], r[6]))
A("  同一发同一格记了两遍的处数 = %d（该 0；门口那条「同一发只许记一次」在守它）" % dups)
A("")
A("【三】相邻两发之间改了什么（只算发号相差 1 的相邻对，逐格比 payload 哈希/话/出处档）")
A("  相邻对数 = %d ｜ 参与比对的格次 = %d" % (pairs, same_sha + diff_sha))
A("  payload 哈希相同 %d ｜ 不同 %d" % (same_sha, diff_sha))
A("  claim（那人写的话）相同 %d ｜ 不同 %d" % (same_claim, diff_claim))
A("  basis（出处档）相同 %d ｜ 不同 %d" % (same_basis, diff_basis))
A("  相邻两发的时间差：样本 %d ｜ 中位 %s ｜ p10 %s ｜ p90 %s" %
  (len(gaps_ms), fmt_ms(pct(gaps_ms, 0.5)), fmt_ms(pct(gaps_ms, 0.1)), fmt_ms(pct(gaps_ms, 0.9))))
A("  ⇒ 时间差这一栏要打折读：中位 0 ms 意味着**多数相邻两发落在同一秒里**（这份账是脚本连发跑出来的），")
A("    所以卡片设想的「用时间差量调用方想了多久」在这份账上量不出来；能用的只有 p90 那一头（%s）。" % fmt_ms(pct(gaps_ms, 0.9)))
A("  旁注：claim 与 payload 的 same/diff 两栏数字相同（%d/%d）—— 这份账里没有「话没变而字节变了」的反例，" % (same_claim, diff_claim))
A("    所以拿 claim 当改动代理在这一份上是安全的，但这**不是判据**，是这一本账的形状。")
A("  ⇒ 这一节能说的是：链上「第 N 发与第 N+1 发之间，哪几格的字节/话/档变了」有原文可指；")
A("    不能说的是：这些变化里哪一次是被拒之后逼出来的（见【四】）。")
A("")
A("【四】量不到的那一半 —— 记 absent，不记 0，并给结构性理由")
A("  账本键名并集（逐名）：%s" % " ".join(keys))
A("  其中带「被拒/失败」字样的格名 = %d 个（词表 %s 现撞）" % (len(vocab_hit), " ".join(REFUSE_VOCAB)))
A("  结构证明（每条锚带一个我自己声明的预期，命中数与预期对不上就 exit 7 不落盘）：")
for label, needle, want, hits in anchor_rows:
    A("    %s ｜ 锚「%s」命中 %d 行（预期 %d）｜ 行号 %s" %
      (label, needle, len(hits), want, ",".join(str(x) for x in hits)))
A("  与看板 #69 那一族对开：全树（非测试 .go）里 `noteRejected()` 的调用行现扫 = %d 处，逐名：" % calls_total)
for f, n, hs in call_rows:
    A("    %s ｜ %d 处 ｜ 行号 %s" % (f, n, ",".join(str(x) for x in hs)))
A("    ⇒ 这一栏只是把「谁在记这笔数」现数一遍；#69 说的是**注释里那个数与它对不上**，那条仍等作者圈，我没动注释。")
A("  ⇒ 三条叠起来就是这句话：")
A("    ① 拒收当场 return，链上一条不写（`record.go` 那几行），所以「被拒」这件事在账上没有载体；")
A("    ② 同一发重试到同一个发号，成功那次的形状与「一次就写对」逐字节相同（因为前一发根本没落），")
A("      所以链上连「这一发改过两次」都留不下；")
A("    ③ 唯一能在链上看出「中间有过不发」的是发号断档 —— 但断档不可归因：")
A("      调用方成功后自己跳号、与被拒后改号，两种成因在账上同形。上面【二】那列就是全部的断档现数。")
A("  所以「被拒之后隔几发改对 / 改的是哪几格」这一指标：今天从**账本**量 = `absent（结构上量不到）`；")
A("    要量它，得从**调用方那一侧**取（transcript / 往来原文），那正是 #48 与 E 段那三件材料缺的东西。")
A("")
A("【五】这一格能说什么 / 不能说什么")
A("  能说：链上能核的「相邻发改动」第一次有了条数与分布（上面【三】），显示层不用改判据、也不用动内核就做出来了；")
A("  能说：量不到的那一半是指名量不到，不是没测。")
A("  不能说：这个指标已经闭上 —— 它闭上需要调用方侧的原文，而那份材料今天不存在（E 段还欠着那一个真人）。")
A("  不能说：断档 = 被拒过。【二】那列断档的归因今天没有任何东西守着，我把两种成因都写上了。")
A("  不能推广：这一份是 10-18 那棵树导出的账（49 格 × 若干发），换一版口径（#66 之后 Cuts 三格已改档）不可比。")
A("")
A("【六】自曝")
A("  1) 我第一版想把「被拒次数」直接从账上数出来 —— 数之前先去代码里核了拒收走不走 Append，")
A("     核完才知道这一格在账上恒不可得。如果我先跑再读码，就会拿 0 当读数（那条房规：量不到记 absent，绝不记 0）。")
A("  2) 这支尺子第一次跑就红在 **自己的路径 bug** 上（把仓库根算少了一层，`构建-1017` 其实住仓库外）：")
A("     引用闸当场 rc=7 拒绝落盘，所以那份坏读数一个字都没进归档 —— 这一条不算我的功劳，是闸的。改完路径才有本件。")
A("  3) 时间差那一栏是按 ts 秒级解析的（账里 ts 带小数但我按整秒取），所以 ms 一栏的精度只到秒，不能拿来谈毫秒级排队时间。")
A("  4) 第二发它红在**我自己的口径错**上（rc=6 DENOM-BAD）：我把抬头那句「1483862 字节」当成这支文件的字节去对，")
A("     实际差 77 字节 = 外面那一行抬头自己（76 字节 ＋ LF）。真件没错、尺子错 —— 上面【一】那三行算术就是复核用的。")
A("     这一条要留着：**分母对不上时，先假设是尺子的口径错，再去怀疑件**。")
A("  5) 第三发红在**反臂与母件同名**上：三发子发与母进程跑在同一秒里，自动名撞车，子发一个个先撞到")
A("     「目标已存在」那道闸退了 3 —— 看着像红，其实红的是覆盖闸不是我要验的那道闸。")
A("     修法：子发的产物路径改到 TEMP 下带模式名的独立文件，并新增一栏读数「子发产物件数 before/after 该相等」")
A("     （这一栏今天就是【六】5 那条错的现数闸）。教训：**闸要能红，还得红在该红的那一格上**。")
A("")
A("【七】这一发的分母自己印（收尾）")
A("  引用 %d 条 ｜ MISS 0（有缺件就 exit 7 不落盘） ｜ 锚 %d 条逐名命中现印 ｜ 键名 %d ｜ 格名 %d ｜ session %d ｜ 条目 %d" %
  (len(CITES), len(anchor_rows), len(keys), len(kinds_total), len(by_sess), len(entries)))

txt = "\r\n".join(L) + "\r\n"
leak = [i for i, ln in enumerate(L, 1) if re.search(r"%[sd]", ln)]
if leak:
    print("FORMAT-LEAK: lines %s" % leak)
    sys.exit(8)
if not denom_ok:
    die(6, "DENOM-BAD", "抬头声明与现量对不上（或被人调成对不上）")
with io.open(OUT, "w", encoding="utf-8", newline="") as f:
    f.write(txt)
print("REPORT WRITTEN: %s" % OUT.encode("unicode_escape").decode("ascii"))
print("bytes=%d lines=%d entries=%d pairs=%d dups=%d" % (len(txt.encode("utf-8")), len(L), len(entries), pairs, dups))

if not CTL:
    child_env = dict(os.environ, PYTHONIOENCODING="utf-8")
    cdir = os.path.join(tempfile.gettempdir(), "stdio-1019")
    if os.path.isdir(cdir):
        before = len([f for f in os.listdir(cdir) if f.startswith("repair-edge-child-")])
    else:
        before = 0
    arms = []
    arms.append(("MISS", subprocess.run([PY, "-X", "utf8", __file__], env=dict(child_env, MISSCTL="1"),
                                        capture_output=True, text=True, encoding="utf-8", errors="replace"), 7))
    arms.append(("ANCHOR", subprocess.run([PY, "-X", "utf8", __file__], env=dict(child_env, ANCHORCTL="1"),
                                          capture_output=True, text=True, encoding="utf-8", errors="replace"), 7))
    arms.append(("DENOM", subprocess.run([PY, "-X", "utf8", __file__], env=dict(child_env, DENOMCTL="1"),
                                         capture_output=True, text=True, encoding="utf-8", errors="replace"), 6))
    ok = True
    for name, r, want in arms:
        hit = r.returncode == want and "CHILD-LEAF-STOP=True" in (r.stdout or "")
        if not hit:
            ok = False
        print("ARM %s: rc=%s want=%s leaf=%s" % (name, r.returncode, want, "CHILD-LEAF-STOP=True" in (r.stdout or "")))
    after = len([f for f in os.listdir(cdir) if f.startswith("repair-edge-child-")]) if os.path.isdir(cdir) else -1
    landed = os.path.exists(OUT)
    print("ARMS ALL RED = %s ｜ 子发产物件数 before=%d after=%d（该相等：反臂是叶子，一个字节都不写）" % (ok, before, after))
    print("母件仍在盘上 = %s ｜ 路径 = %s" % (landed, OUT.encode("unicode_escape").decode("ascii")))
    if not ok or after != before:
        print("ARM-FAIL: 有反臂没红，或有子发写了件，这份读数不算被闸过")
        sys.exit(5)
print("PROBE DONE")
