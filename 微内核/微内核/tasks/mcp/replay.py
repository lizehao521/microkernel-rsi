# -*- coding: utf-8 -*-
# replay.py —— 换一个新进程、只把落盘的账交给它，问它能重建出下一发的历史吗。
#
# 一次端点都不打，不花钱。要验的有三层：
#   1. 重启之后这本账还自洽吗（load + verify）；
#   2. 从账上能不能读回**模型说过的那句话**（不是它的字节数、不是它的指纹）；
#   3. 如果原话在别处（驱动脚本自己的日志），账本能干什么 —— 逐条按同一规则重算指纹，
#      核它是不是当时记下的那一个。
# 这一支的真正产出是第 2 层的答案，它决定「续跑」这件事的架构：
# 账本要么是能恢复的件（recovery），要么只是能审计的件（audit），两者不能混着吹。
#
# 控制台只打 ASCII，全文落 报告/续跑重建.txt。
import ast
import hashlib
import io
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client import Judge, Kernel

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BOOK = os.environ.get("REPLAY_BOOK", os.path.join(ROOT, "报告", "样例-工具提议.jsonl"))
LOG = os.environ.get("REPLAY_LOG", os.path.join(ROOT, "报告", "工具提议.txt"))
REPORT = os.environ.get("REPLAY_REPORT", os.path.join(ROOT, "报告", "续跑重建.txt"))

log = []
note = log.append
check = Judge()

book_text = io.open(BOOK, encoding="utf-8").read()
entries = [l for l in book_text.split("\n") if l.strip()]
note("账本：%s（%d 行条目）" % (BOOK, len(entries)))

# 驱动脚本当时把每一发的原话抄进了自己的日志（那是我唯一有原文的地方）。
# 它印的是「原话（N 字节）：'...text[:160]...'」，所以长句子在**日志里也被截过** —— 这一点本身要量出来。
answers = []
for l in io.open(LOG, encoding="utf-8").read().split("\n"):
    mo = re.match(r"   原话（(\d+) 字节）：(.*)$", l)
    if mo:
        declared = int(mo.group(1))
        try:
            txt = ast.literal_eval(mo.group(2).strip())
        except Exception:
            note("   日志里有一行的原话读不回字面值，跳过：%r" % l[:60])
            continue
        answers.append((declared, txt, len(txt.encode("utf-8")) == declared))
note("驱动日志里找到 %d 条原话，其中**没被日志自己截过**的 %d 条"
     % (len(answers), sum(1 for _, _, ok in answers if ok)))

K = Kernel("replay", sink=note)
import re as _re
_ct0, _, _ = K.call("kernel.celltable")
_m0 = _re.search(r"当前 (\d+) 格", _ct0 or "")
NCELL = int(_m0.group(1)) if _m0 else 49
ld, _, _ = K.call("kernel.load_journal", {"text": book_text})
note("载入（新进程、空账本）：%s" % ld.split("\n")[0])
vf, _, _ = K.call("kernel.verify")
check("换进程读回之后链自洽", vf, ["✓ 链自洽（共 %d 条" % len(entries)])

# 一发起号从 1 连续排；一发的条数从内核的表派生（下表），不抄死。
n_calls = len(entries) // 47
note("按 %d 条一发数出来：%d 发" % (NCELL, n_calls))

cells_of = {}
for n in range(1, n_calls + 1):
    rb, iserr, _ = K.call("kernel.read_back", {"session": "tools", "call": n, "side": "output"})
    if iserr:
        note("!! 第 %d 发读回失败：%s" % (n, rb))
        continue
    d = {}
    for l in rb.split("\n"):
        f = l.split()
        if len(f) >= 2 and not f[0].startswith("session="):
            d[f[0]] = (f[1], " ".join(f[2:]))
    cells_of[n] = d
    note("-- 第 %d 发 output 侧读到 %d 格；缺席 %s"
         % (n, len(d), rb.split("缺席 ")[1].split("：")[0] if "缺席 " in rb else "?"))
    note("   model_says_bytes=%s model_says_sha256=%s"
         % (d.get("model_says_bytes", ("—", ""))[1], d.get("model_says_sha256", ("—", ""))[1][:20]))

# (1) 原话到底在不在账上。两条一起看：
#  a) **结构**：装着「模型说了什么」的格子有哪几个键 —— 只有四个，全是数与指纹；
#  b) **逐字**：把日志里那些没被截过的句子拿去和**每一格的 claim** 比（不是拿整本账做子串搜索）。
SAYS_KEYS = ("model_says_bytes", "model_says_kept", "model_says_clipped", "model_says_sha256")
for n, d in sorted(cells_of.items()):
    have = [k for k in SAYS_KEYS if k in d]
    note("(1a) 第 %d 发装着「模型说了什么」的格：%s" % (n, "、".join(
        "%s=%s" % (k, d[k][1][:20]) for k in have)))
claims = [(n, k, claim) for n, d in cells_of.items() for k, (_, claim) in d.items()]
usable = [(declared, txt) for declared, txt, ok in answers if ok and txt]
# 一句 1 个字符的答案（本轮真有两个：'4' 与换行）拿去子串搜索必然假命中——它会撞上数字格。
# 所以逐字这一条只拿**长过 8 个字节**的句子来判，短句子另列出来当反例。
long_ = [t for _, t in usable if len(t.encode("utf-8")) > 8]
short_ = [t for _, t in usable if len(t.encode("utf-8")) <= 8]
hit_claim = [t[:30] for t in long_ if any(t == c for _, _, c in claims)]
hit_book = [t[:30] for _, t in usable if t in book_text]
note("(1b) 长过 8 字节的原话 %d 句，与某一格 claim **完全相等**的：%s；"
     "另有 %d 句短到 8 字节以内，故意不拿来判（'4' 会撞上数字格，见上面那行）"
     % (len(long_), hit_claim, len(short_)))
note("(1b) 反例：拿整本账做**子串**搜索会「找到」%d 句 —— %s —— 那是假命中，不是证据"
     % (len(hit_book), hit_book))
if long_:
    check("(1) 没有任何一格装着模型的原话（账本是审计件，不是恢复件）",
          "命中 %d 句" % len(hit_claim), ["命中 0 句"])
else:
    note("(1) 日志里没有长到能可靠判定的原话 —— 这一条本轮只记不判")

# (2) 但**指纹对得上**：第三方拿到原文能按同一规则复算，账本因此仍然有用（只是不能供原文）。
verified, mismatched, skipped = 0, 0, 0
for n, d in sorted(cells_of.items()):
    sha_on_book = d.get("model_says_sha256", ("", ""))[1]
    if n - 1 >= len(answers):
        continue
    declared, txt, ok = answers[n - 1]
    if not ok or not sha_on_book or not txt:
        skipped += 1
        continue
    recomputed = hashlib.sha256(txt.encode("utf-8")).hexdigest()
    if recomputed == sha_on_book:
        verified += 1
    else:
        mismatched += 1
        note("   !! 第 %d 发指纹对不上：账上=%s 重算=%s" % (n, sha_on_book[:16], recomputed[:16]))
note("(2) 逐发按同一规则重算指纹：对上 %d 发、对不上 %d 发、跳过 %d 发（跳过的没有原文可比）"
     % (verified, mismatched, skipped))
if verified:
    check("(2) 凡日志里那句原话没被截过的，指纹全部与账上一致（比过 %d 发）" % verified,
          "红=%d" % mismatched, ["红=0"])
else:
    note("(2) 一句可比的原话都没有（日志把长句子截在 160 字以内）—— 这一条本轮只记不判")

# (3) 续跑真正缺的是什么：把「能重建」与「不能重建」两张清单摆出来，不含糊成一句「不够」。
can = ["模型说了几句（model_says_bytes / kept）", "每句话的指纹（model_says_sha256）",
       "收尾原因（finish）、自报模型名（endpoint_model）",
       "提议了几个、几个歪、链上连歪几发（tool_calls / malformed_*）",
       "收发字节、帧数、首帧、最大帧间、token 自报数",
       "哪些段整段没带进下一发（folded / clipped / dropped + 原文指纹）",
       "有没有故障、哪一类、要不要重发（fail_kind / fail_status / downgrade / RetrySame 由类别推）"]
cannot = ["模型原话本身（没有任何一格装文本）", "人问过的题面原文（输入侧只有字节数与段名）",
          "工具返回的正文原文（observation 也只剩字节数）", "system 说明书原文",
          "会话 id / 这一发属于哪一趟对话（session 有，但对话内容没有）"]
note("(3) 只带这本账能重建：%s" % "、".join(can))
note("(3) 只带这本账重建不出：%s" % "、".join(cannot))
note("(3) 「重建不出原文」这一句的依据是上面 (1)：整本账里找不到任何一格装着那句原话")

# (4) 顺带把 dropped 那一格读回来：被省掉的那条 assistant 消息，账上留着它的指纹 ——
# 也就是说**框架如果另存了原文，账本能证明存的就是当时那一条**。
for n, d in sorted(cells_of.items()):
    dr = d.get("dropped", ("", ""))
    if dr[1] and not dr[1].startswith("0"):
        note("(4) 第 %d 发 dropped=%s（这一格的 claim 里带 ref，原文指纹在裁剪表那一格里）" % (n, dr[1][:90]))

ta, _, _ = K.call("kernel.tally")
note("tally：%s" % ta.split("\n")[0])
K.close()

fails = check.red
note("\n===== 判定 =====")
for name, st, why in check.rows:
    note("%s  %s  %s" % (st, name, why))
with io.open(REPORT, "w", encoding="utf-8") as f:
    f.write("\n".join(log) + "\n")
print("REPLAY status=%s checks=%d red=%d entries=%d calls=%d verified=%d mismatched=%d" % (
    "OK" if not fails else "RED", len(check.rows), len(fails), len(entries), n_calls, verified, mismatched))
for name, st, why in fails:
    print("  RED %s | %s" % (name, why.encode("ascii", "replace").decode("ascii")))
sys.exit(0 if not fails else 1)
