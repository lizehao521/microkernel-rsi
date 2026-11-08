# -*- coding: utf-8 -*-
# fork.py —— 跨进程实验：同一份盘上账本，两个进程各自往下长，会发生什么。
#
# drive.py 测的是「一个进程里，判据拦得住什么」。这一支测的是它测不了的那一半：
# README 缺口第 6 条（Ledger 没有锁）与第 8 条剩下的那一半（两个进程各写各的），
# 那句注释在 journal.go 里写着「这一条不许被写成已验证」—— 所以把它跑一遍，量出个形状。
#
# 三个进程、同一份起点（报告/样例-真接口两发.jsonl：kresearch 那一趟真端点留下的 94 条，
# 每一条都带原像）。A 与 B 各自记一发**自造**的第 3 发（session=fork-demo，故意不与真数据混），
# 两边记的数不一样。然后问内核：哪本是正史？
import io
import json
import re
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client import INPUT1, MEAS, OUTPUT1, Judge, Kernel, m

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BASE = os.path.join(ROOT, "报告", "样例-真接口两发.jsonl")
TRANSCRIPT = os.path.join(ROOT, "报告", "挂载-跨进程分叉.txt")
BOOK_A = os.path.join(ROOT, "报告", "实验-分叉A.jsonl")
BOOK_B = os.path.join(ROOT, "报告", "实验-分叉B.jsonl")

log = []


def note(s):
    log.append(s)


check = Judge()
base_text = io.open(BASE, encoding="utf-8").read()

# ---- 两个进程，同一个起点 ----
A = Kernel("A", sink=note)
B = Kernel("B", sink=note)

BASE = len([x for x in base_text.splitlines() if x.strip()])
la, _, _ = A.call("kernel.load_journal", {"text": base_text})
lb, _, _ = B.call("kernel.load_journal", {"text": base_text})
check("F01 进程 A 从盘上读回 94 条", la, ["读回 %d 条" % BASE, "原像 %d 条在手" % BASE])
check("F02 进程 B 从同一份字节读回同样的 94 条", lb, ["读回 %d 条" % BASE, "原像 %d 条在手" % BASE])

va, _, _ = A.call("kernel.verify")
vb, _, _ = B.call("kernel.verify")
# 这是这一支最有分量的一条：一个**进程外**的读者，只拿 JSON-RPC 和盘上字节，
# 把两天前那趟真端点的账逐格重算了一遍，而且是第二个进程做第一件事。
check("F03 独立进程能把真端点那 94 条重算成绿", va, ["✓ 链自洽（共 %d 条，其中 %d 条比对过原像）" % (BASE, BASE)])
check("F04 第二个进程独立重算，结果一模一样", vb, ["✓ 链自洽（共 %d 条，其中 %d 条比对过原像）" % (BASE, BASE)])

dup, _, _ = A.call("kernel.record_call", {"session": "kresearch", "actor": "A",
                                          "input": INPUT1, "output": OUTPUT1})
check("F05 跨进程重启去重：真端点第 1 发再记一遍被拒", dup, ["整发拒收", "已经记过了"])

# ---- 分叉：两本账各自往下长同一发，数不一样 ----
IN3 = dict(INPUT1, Call=3)
OUT3 = dict(OUTPUT1, Call=3)
OUT3_B = json.loads(json.dumps(OUT3))
OUT3_B["WireBytesIn"] = m(50, MEAS)  # 与 A 差 1 字节：通道上收到的数不一样

ra, _, _ = A.call("kernel.record_call", {"session": "fork-demo", "actor": "A", "input": IN3, "output": OUT3})
rb, _, _ = B.call("kernel.record_call", {"session": "fork-demo", "actor": "B", "input": IN3, "output": OUT3_B})
# 同上：条数从内核的表派生（这一支按 3 发算，所以是 3*NC）。
_ct0, _, _ = A.call("kernel.celltable")
_m0 = re.search(r"当前 (\d+) 格", _ct0 or "")
NC = int(_m0.group(1)) if _m0 else 49
AFTER = BASE + NC  # 底本那几发是历史文件（每发当时的格数），自造这一发用现在的格数
check("F06 A 收下自造的第 3 发（底本 %d + 一发 %d）" % (BASE, NC), ra,
      ["整发落账 %d 条" % NC, "链上共 %d 条" % AFTER])
check("F07 B 也收下同一个发号、另一个数（同样 %d 条）" % AFTER, rb,
      ["整发落账 %d 条" % NC, "链上共 %d 条" % AFTER])

va2, _, _ = A.call("kernel.verify")
vb2, _, _ = B.call("kernel.verify")
check("F08 分叉之后 A 仍然报绿", va2, ["✓ 链自洽（共 %d 条" % AFTER])
check("F09 分叉之后 B 也报绿 —— 两条绿互不知情", vb2, ["✓ 链自洽（共 %d 条" % AFTER])

# 同一发、同一个发号，两本账给出的答案不一样。这不是「谁的链断了」，
# 而是两本都自洽的账对同一个 (session, step) 各说各话。
ba, _, _ = A.call("kernel.read_back", {"session": "fork-demo", "call": 3, "side": "output"})
bb, _, _ = B.call("kernel.read_back", {"session": "fork-demo", "call": 3, "side": "output"})
line_a = [l for l in ba.split("\n") if "wire_bytes_in" in l]
line_b = [l for l in bb.split("\n") if "wire_bytes_in" in l]
note("A 的第 3 发 wire_bytes_in 那格：%s" % (line_a[0] if line_a else "（读不到）"))
note("B 的第 3 发 wire_bytes_in 那格：%s" % (line_b[0] if line_b else "（读不到）"))
check("F10a A 那一格说的是 49", line_a[0] if line_a else "", ["wire_bytes_in", "49"])
check("F10b B 那一格说的是 50", line_b[0] if line_b else "", ["wire_bytes_in", "50"])

ea, _, _ = A.call("kernel.encode_journal", {"payload": "included"})
eb, _, _ = B.call("kernel.encode_journal", {"payload": "included"})
book_a = ea.split("\n", 1)[1]
book_b = eb.split("\n", 1)[1]
io.open(BOOK_A, "w", encoding="utf-8", newline="\n").write(book_a)
io.open(BOOK_B, "w", encoding="utf-8", newline="\n").write(book_b)

ha = [json.loads(l)["hash"] for l in book_a.rstrip("\n").split("\n")]
hb = [json.loads(l)["hash"] for l in book_b.rstrip("\n").split("\n")]
same_prefix = 0
for x, y in zip(ha, hb):
    if x != y:
        break
    same_prefix += 1
div = [i + 1 for i, (x, y) in enumerate(zip(ha, hb)) if x != y]
note("A 链头 %s\\nB 链头 %s\\n共同前缀 %d 条，第一处分叉在第 %s 条" %
     (ha[-1], hb[-1], same_prefix, div[0] if div else "无"))
check("F11 前 94 条逐字节相同，分叉从第 95 条开始",
      "共同前缀 %d 条｜第一处分叉在第 %s 条" % (same_prefix, div[0] if div else "无"),
      ["共同前缀 %d 条" % BASE, "第 %d 条" % (BASE + 1)])
heads = "SAME" if ha[-1] == hb[-1] else "DIFFERENT"
check("F12 两本的链头是两个不同的数（各自 commit 了自己那一条第 95 格）",
      "A=%s B=%s %s" % (ha[-1], hb[-1], heads), ["DIFFERENT"])

# ---- 第三个进程：它能不能判哪本是正史？ ----
C = Kernel("C", sink=note)
ca, _, _ = C.call("kernel.load_journal", {"text": book_a})
cv, _, _ = C.call("kernel.verify")
check("F13 进程 C 收 A 这本，重算为绿", ca + cv, ["读回 %d 条" % AFTER, "✓ 链自洽（共 %d 条" % AFTER])
# C 手上已经有一本 141 条的账了，换另一本必须先签字 —— 不然「换账」这件事会静悄悄发生。
swap, _, _ = C.call("kernel.load_journal", {"text": book_b})
check("F13b C 没签字就换第二本被拒（会顶掉手里那 141 条）", swap, ["载入被拒", "现在有 %d 条" % AFTER])
cb, _, _ = C.call("kernel.load_journal", {"text": book_b, "acknowledge": "replace"})
cv2, _, _ = C.call("kernel.verify")
check("F14 同一个 C 签了字换 B 这本，也重算为绿 —— 正史判不出来", cb + cv2,
      ["读回 %d 条" % AFTER, "✓ 链自洽（共 %d 条" % AFTER])

# C 有没有任何一个工具能同时拿着两本比？把 tools/list 摊开数一遍。
tl, _, _ = C.rpc("tools/list")
names = [t["name"] for t in json.loads(tl)["tools"]]
C.close()
A.close()
B.close()

note("内核交出去的工具：%s" % " ".join(sorted(names)))
check("F15 内核没有任何一格/一个工具能判定哪本是正史",
      " ".join(sorted(names)), ["kernel.verify"], ["kernel.compare", "kernel.merge", "kernel.anchor"])

# 锚的代价：量出来是一个多长的串。
note("一个数（%d 个十六进制字符，%d 字节）就 commit 住了整本 141 条 —— A=%s，B=%s" %
     (len(ha[-1]), len(ha[-1]), ha[-1][:16], hb[-1][:16]))

fails = check.red
note("\n===== 判定 =====")
for name, st, why in check.rows:
    note("%s  %s  %s" % (st, name, why))
note("合计 %d 条，红 %d 条" % (len(check.rows), len(fails)))

with io.open(TRANSCRIPT, "w", encoding="utf-8") as f:
    f.write("\n".join(log) + "\n")

print("MCP FORK status=%s checks=%d red=%d prefix=%d first_diverge=%s head_a=%s head_b=%s" % (
    "OK" if not fails else "RED", len(check.rows), len(fails), same_prefix,
    div[0] if div else "none", ha[-1][:12], hb[-1][:12]))
for name, st, why in fails:
    print("  RED %s | %s" % (name, why.encode("ascii", "replace").decode("ascii")))
sys.exit(0 if not fails else 1)
