# -*- coding: utf-8 -*-
# drive.py —— 把挂成 MCP 的内核当真客户端跑一遍。
#
# 为什么要这么麻烦地「挂」而不是在 Go 里直接调：
# contract.md 第 0 节那条批评是「本 module 之外没有任何调用方」。
# 挂成 MCP 之后，调用方是一个**独立进程**，它只能通过 JSON-RPC 说话 ——
# 这一层拿不到的东西（未导出的字段、包内的约定）就再也骗不了我们了。
#
# 客户端管道、判据器与「第 1 发」的样本住在 client.py（drive 与 fork 共用一份）：
# 两处各写一遍，就会漂成两套「我以为内核收得下什么」。
# 全文落到 报告/挂载-MCP-往来.txt，控制台只打 ASCII ——
# Windows 的 GBK 控制台会把中文打崩，这是 README 第 1 节警告过的那个坑。
import io
import json
import re
import os
import sys

# 本机是 embeddable Python：它带着 ._pth，脚本所在目录不会自动进 sys.path，
# 所以同目录的 client.py 要自己递进去一次。
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from client import INPUT1, MEAS, OUTPUT1, Judge, Kernel, m, missing

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORT = os.path.join(ROOT, "报告", "挂载-MCP-往来.txt")

log = []          # 全文（UTF-8 落盘）


def note(s):
    log.append(s)


K = Kernel("drive", sink=note)
check = Judge()

# 坏形状：非流式却把分帧数记成实测的 0 —— 这正是 runloop 今天在线上写下的那个形状
BAD_STREAM = json.loads(json.dumps(OUTPUT1))
BAD_STREAM["Chunks"] = m(0, MEAS)

# 坏形状：报预算挡下，过网线字节却记成「没数到」
BAD_BUDGET = json.loads(json.dumps(OUTPUT1))
BAD_BUDGET["Failure"] = {"Kind": "budget", "Detail": "额度到顶，一发都没该发",
                         "Status": missing("是我们自己掐的额度，压根没走到对端 —— 没有状态码可记"),
                         "RetryAfterMs": missing("没发出去，对端没说"), "Downgrade": False}
BAD_BUDGET["WireBytesOut"] = missing("没数到")

# ---- 跑（initialize 由 Kernel 的构造函数代做）----
tl, _, _ = K.rpc("tools/list")
try:
    names = [t["name"] for t in json.loads(tl)["tools"]]
except Exception as e:
    names = []
    note("tools/list 解不开：%s" % e)
check("01 tools/list 交出 10 个工具（含 load_journal，不含绕过判据的裸写入）",
      " ".join(names),
      ["kernel.celltable", "kernel.record_call", "kernel.load_journal", "kernel.encode_journal",
       "kernel.check_call", "kernel.verify", "kernel.tally", "kernel.read_back",
       "kernel.declare_reader", "kernel.readers_report"],
      ["kernel.record_input", "kernel.append"])
note("tools/list 报出的工具：%s" % " ".join(sorted(names)))

ct, _, _ = K.call("kernel.celltable")
# 一发几条、表上几格：全部**从内核的 celltable 派生**，不抄在脚本里。
# 上一版把 47/46/141 这些数写死，格位表一长两位（47→49）就整片红 —— 红的该是我的抄数，不是内核。
_ct0, _, _ = K.call("kernel.celltable")
_m0 = re.search(r"当前 (\d+) 格（输入侧 (\d+) \+ 输出侧 (\d+)）；唯一键名 (\d+) 个", _ct0 or "")
NC, NI, NO, NK = (int(_m0.group(i)) for i in range(1, 5)) if _m0 else (0, 0, 0, 0)
check("02 格位表的三个分母与代码一致", ct, ["当前 %d 格（输入侧 %d + 输出侧 %d）" % (NC, NI, NO), "唯一键名 %d 个" % NK])

cc, _, _ = K.call("kernel.check_call", {"session": "kresearch", "actor": "drive",
                                      "input": INPUT1, "output": OUTPUT1})
check("03 真端点那一发两侧都自洽", cc, ["输入侧：自洽", "输出侧：自洽"])

# 拼错的键名：声明必须当场被拒，否则一格死格看起来是活的
bad, _, _ = K.call("kernel.declare_reader", {"reader": "drive", "key": "wire_bytes_outt", "side": "output"})
check("04 拼错的键名被拒", bad, ["声明被拒", "wire_bytes_outt"])

okdecl, _, _ = K.call("kernel.declare_reader", {"reader": "drive", "key": "wire_bytes_in", "side": "output"})
check("05 正确键名的声明被收下", okdecl, ["✓", "wire_bytes_in"])

dupdecl, _, _ = K.call("kernel.declare_reader", {"reader": "drive", "key": "wire_bytes_in", "side": "output"})
check("06 同一读者声明两遍被拒", dupdecl, ["声明被拒"])

rr0, _, _ = K.call("kernel.readers_report")
# 声明过的那一格必须从未读名单里掉出去：输出侧 27 格里 26 格没人读。
# 分母全部由内核的表给（NI/NO/NK 在文件头上从 celltable 解析）：
# 上一版把 20/27/26 抄在这里，表长两格（输出侧 27→29）就整片红 —— 红的该是抄数不是内核。
check("07 读者报告带分母，声明过的那一格掉出未读名单", rr0,
      ["键名表 %d 格" % NI, "键名表 %d 格" % NO, "%d 格没有任何声明者读" % (NO - 1)], ["wire_bytes_in"])

rec, _, _ = K.call("kernel.record_call", {"session": "kresearch", "actor": "drive",
                                        "input": INPUT1, "output": OUTPUT1})
check("08 整发落账 %d 条" % NC, rec, ["整发落账 %d 条" % NC, "链上共 %d 条" % NC])

rec2, _, _ = K.call("kernel.record_call", {"session": "kresearch", "actor": "drive",
                                         "input": INPUT1, "output": OUTPUT1})
check("09 同一发记两遍被拒", rec2, ["整发拒收", "已经记过了"])

bad1, _, _ = K.call("kernel.record_call", {"session": "kresearch", "actor": "drive",
                                         "input": INPUT1, "output": BAD_STREAM})
check("10 非流式写成分帧 0 被拒", bad1, ["整发拒收", "非流式没有这一格"])

bad2, _, _ = K.call("kernel.record_call", {"session": "kresearch", "actor": "drive",
                                         "input": INPUT1, "output": BAD_BUDGET})
check("11 预算挡下却报字节没数到 被拒", bad2, ["整发拒收", "过网线字节却记成没数到"])

# 两侧发号不一致：外壳不该有能力造出「半发」
half, _, _ = K.call("kernel.record_call", {"session": "kresearch", "actor": "drive",
                                         "input": INPUT1,
                                         "output": dict(OUTPUT1, Call=9)})
check("12 两侧发号对不上时整发拒收（不写半发）", half, ["整发拒收", "两侧的发号对不上"])

ta, _, _ = K.call("kernel.tally")
check("13 被拒的次数进了合计（拒了 4 次）", ta, ["被拒的写入 4 次"])

vf, _, _ = K.call("kernel.verify")
check("14 链自洽，且四次拒收没留下半条", vf, ["✓ 链自洽（共 %d 条，其中 %d 条比对过原像）" % (NC, NC)])

rb, _, _ = K.call("kernel.read_back", {"session": "kresearch", "call": 1, "side": "output"})
check("15 读侧能把某一发某一侧读回来", rb, ["session=kresearch 的第 1 发 output 侧共 %d 格" % NO, "wire_bytes_in", "缺席"])

ej, _, _ = K.call("kernel.encode_journal", {"payload": "omitted"})
# 不带原像 = payload_b64 这一格根本不出现在行里？不：它出现但为 null。
# 「键不存在」「空串」「有值」三态必须长得不一样，这里核的是第一种。
check("16 编成 JSONL：不带原像（payload_b64 为 null，指纹仍在）", ej,
      ["JSONL", "payload_sha256", '"payload_b64":null'])

ej2, _, _ = K.call("kernel.encode_journal", {"payload": "maybe"})
check("17 带不带原像说「maybe」被拒（不许默认值替调用方决定）", ej2, ["编码被拒", "只认 included / omitted"])

eji, _, _ = K.call("kernel.encode_journal", {"payload": "included"})
check("18 带原像时 payload_b64 有值", eji, ['"payload_b64":"'])

# 重启：把 16 号那条不带原像的字节读回来 —— 链必须说「核不了」而不是「被动过」
body = ej.split("\n", 1)[1]
ld, _, _ = K.call("kernel.load_journal", {"text": body, "acknowledge": "replace"})
check("19 从字节读回整本账（一发 %d 条）" % NC, ld, ["读回 %d 条" % NC, "链上现在 %d 条" % NC, "原像 0 条在手"])

vf2, _, _ = K.call("kernel.verify")
check("20 没有原像时不喊篡改、也不打勾", vf2, ["字段级自洽", "核不了"], ["✓", "被动过"])

rec3, _, _ = K.call("kernel.record_call", {"session": "kresearch", "actor": "drive",
                                         "input": INPUT1, "output": OUTPUT1})
check("21 重启后同一发仍记不进去第二遍（去重门跟着账一起回来）", rec3, ["整发拒收", "已经记过了"])

# 真不符：改一条的 claim 再载入，必须被点名（证明 20 号那条不是「一律不报」）
tampered = []
for i, line in enumerate(body.rstrip("\n").split("\n")):
    d = json.loads(line)
    if i == 3:
        d["claim"] = "篡改过的说法"
    tampered.append(json.dumps(d, ensure_ascii=False))
ld2, _, _ = K.call("kernel.load_journal", {"text": "\n".join(tampered) + "\n", "acknowledge": "replace"})
# 点名要的是**指纹重算不符**那一句：如果它是因为没签字被拒，这一条就会假绿。
check("22 载入时逐格比对，改过的 claim 被点名拒收", ld2, ["载入被拒", "第 4 条整格指纹重算不符"],
      ["读回 %d 条" % NC, "acknowledge=replace"])

vf3, _, _ = K.call("kernel.verify")
check("23 上一次失败的载入没有污染当前这本账", vf3, ["核不了"], ["✗"])

# 写侧的「一发」由三个轴定身份（session、发号、侧 —— 见 alreadyRecorded），
# 读侧曾经只认两个。挂成 MCP 后两个 session 各记一次第 1 发，读侧当场回了 40 格。
# 这一条现在钉在 Go 测试里（TestCellsOfKeepsTwoSessionsApart），这里再从进程外走一遍。
lost, _, _ = K.call("kernel.record_call", {"session": "unsaved", "actor": "drive",
                                           "input": INPUT1, "output": OUTPUT1})
note("第二个 session 的第 1 发被收下了：%s" % lost.split("\n")[0])
mixA, _, _ = K.call("kernel.read_back", {"session": "unsaved", "call": 1, "side": "input"})
mixB, _, _ = K.call("kernel.read_back", {"session": "kresearch", "call": 1, "side": "input"})
check("24 两个 session 同号同侧各读各的，不再被并成 40 格", mixA + "||" + mixB,
      ["session=unsaved 的第 1 发 input 侧共 20 格", "session=kresearch 的第 1 发 input 侧共 20 格"])

# 载入是**替换**而不是合并：当前这本里还没被 encode 出去的条目会跟着没了，所以要签字。
ld3, _, _ = K.call("kernel.load_journal", {"text": body})
check("25 没签字就顶掉一本有货的账被拒（并报出会丢几条）", ld3,
      ["载入被拒", "现在有 %d 条" % (2 * NC), "acknowledge=replace"])

ld4, _, _ = K.call("kernel.load_journal", {"text": body, "acknowledge": "replace"})
gone, _, _ = K.call("kernel.read_back", {"session": "unsaved", "call": 1, "side": "input"})
back, _, _ = K.call("kernel.read_back", {"session": "kresearch", "call": 1, "side": "input"})
check("26 签了 acknowledge=replace 才真顶", ld4, ["读回 %d 条" % NC])
check("27 顶完了前一本题掉的记录当场读不回来（替换就是替换）", gone + "||" + back,
      ["一条都没有", "session=kresearch 的第 1 发 input 侧共 20 格"])

K.close()

# ---- 收口 ----
fails = check.red
note("\n===== 判定 =====")
for name, st, why in check.rows:
    note("%s  %s  %s" % (st, name, why))
note("合计 %d 条，红 %d 条" % (len(check.rows), len(fails)))

os.makedirs(os.path.dirname(REPORT), exist_ok=True)
with io.open(REPORT, "w", encoding="utf-8") as f:
    f.write("\n".join(log) + "\n")

print("MCP DRIVE status=%s checks=%d red=%d" % (
    "OK" if not fails else "RED", len(check.rows), len(fails)))
for name, st, why in fails:
    # 只往控制台打 ASCII：GBK 连 U+FFFD 都打不出，中文全文在报告文件里。
    print("  RED %s | %s" % (name, why.encode("ascii", "replace").decode("ascii")))
sys.exit(0 if not fails else 1)
