# -*- coding: utf-8 -*-
"""拿契约自己的话打内核：`contract.md §1.4` 说预算「`InputMeta.Check()` 会把公式**重算一遍**再比对」——
那就逐格手搭，看哪几格真进不了门。

为什么挑这七个：它们正是静态分类器判成「只搬动 / 除登记外没人碰」的那几格
（`content_ceiling`、`effective_ceiling`、`clamp_rounds`、`leftover_pct`、`transport_slack_pct`、
`call_bytes_ceiling`、`shares`）—— 上一份分析件已经判定静态不可信（撞了的 5 条全是假阴性），
所以这次不问代码，问运行时：**契约说会被拒的，到底拒不拒**。

每一臂都跑预检 + 落账两遍（顺带再数一次 #20 的同判），并原样留回话。
控制臂（没动过的那发）必须先被收下，否则整批作废。

跑法：python tasks/probe/contract_budget_arms.py <mcphttp.exe> [输出.txt]
"""
import io
import json
import os
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "mcp"))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

# (臂名, Budget 里改哪个字段, 改成什么, 契约里哪句话要求它被拒)
ARMS = [
    ("A1 手搭内容额度", "ContentCeiling", 999999, "§1.4「公式的唯一出口…手搭一份 ContentCeiling=999999 的预算进不了门」"),
    ("A2 手搭生效额度", "EffectiveCeiling", 888888, "§1.4 同一句（生效额度也是推出来的量）"),
    ("A3 手搭回夹刀数", "ClampRounds", 99, "§1.4 同一句（回夹几刀是过程量，也该对得上）"),
    ("A4 剩余百分比不符", "LeftoverPct", 7, "§1「一格一量」+ DeriveBudget 注释「100 减去份额之和」"),
    ("A5 传输余量手搭", "TransportSlackPct", 3, "§1.4（额度由 slack 推，改了 slack 不改额度就对不上）"),
    ("A6 上限为零", "CallBytesCeiling", 0, "§1.4（人给的量也得自洽：0 就是什么都装不下）"),
    ("A7 份额加总超 100", "Shares", {"system": 90, "history": 90}, "DeriveBudget 注释「可以小于 100，但不许大于」"),
]


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "契约说进不了门-到底拒不拒-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    import attracta_probe as AP
    port = 8808
    url = "http://127.0.0.1:%d/mcp" % port
    proc = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    def call(tool, args):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                           "params": {"name": tool, "arguments": args}}).encode("utf-8")
        r = json.loads(urllib.request.urlopen(urllib.request.Request(
            url, data=body, headers={"Content-Type": "application/json"}), timeout=60).read().decode("utf-8"))
        res = r.get("result") or {}
        txt = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
        return txt, bool(res.get("isError"))

    try:
        time.sleep(1.2)
        head, _ = call("kernel.celltable", {})
        if "唯一键名" not in head:
            w("NOT_RUN：端点没答。")
            return 4
        base = json.loads(json.dumps(AP.legal_call("契约对表这一发的底样")))
        _, ce = call("kernel.check_call", {"session": "ctlC", "actor": "sweep", "input": base["input"], "output": base["output"]})
        _, re_ = call("kernel.record_call", {"session": "ctlC", "actor": "sweep", "input": base["input"], "output": base["output"]})
        w("== 契约 §1.4「预算重算一遍再比对」逐字段对表 · %s ｜ 字节 sha256=%s =="
          % (time.strftime("%Y-%m-%d %H:%M:%S"),
             __import__("hashlib").sha256(io.open(exe, "rb").read()).hexdigest()[:16]))
        w("底样先读一遍这发的预算几格（不动它）：")
        for f in ("CallBytesCeiling", "TransportSlackPct", "ContentCeiling", "LeftoverPct", "EffectiveCeiling", "ClampRounds"):
            w("   %-20s = %s" % (f, base["input"]["Budget"].get(f)))
        w("   %-20s = %s" % ("Shares", base["input"]["Budget"].get("Shares")))
        w("控制臂（什么都没改）：check=%s record=%s —— 收不下就整批作废" % (ce, re_))
        if re_:
            return 5
        w("")
        w("臂 ｜ 改哪个字段 ｜ 契约出处 ｜ check ｜ record ｜ 同判 ｜ 拒收原话")
        w("-" * 78)
        refused_n = same_n = 0
        holes = []
        for name, field, val, why in ARMS:
            pack = json.loads(json.dumps(AP.legal_call("契约对表这一发的真 stdout")))
            pack["input"]["Budget"][field] = val
            c_txt, c_err = call("kernel.check_call", {"session": "C" + name[:2], "actor": "sweep",
                                                     "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": "C" + name[:2], "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
            same = (c_err == r_err)
            same_n += same
            if r_err:
                refused_n += 1
            else:
                holes.append(name)
            w("%-18s ｜ %-18s ｜ %-40s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %s"
              % (name, field, why[:38], c_err, r_err, "同判" if same else "**分歧**",
                 ((r_txt or c_txt).replace("\n", " / ")[:110] if r_err else "**收下了**")))
        w("")
        w("== 自印分母 ==")
        w("臂 %d 条 ｜ 落账被拒 %d 条 ｜ **收下（契约说该拒而没拒）%d 条：%s"
          % (len(ARMS), refused_n, len(holes), " ".join(holes) or "无"))
        w("预检与落账同判 %d/%d" % (same_n, len(ARMS)))
        w("读法：收下不等于缺陷 —— 得先确认那个值**真的与公式不符**（比如 `TransportSlackPct` 改了，")
        w("      若公式核的是 ContentCeiling 与 slack 的关系，那这一臂可能本来就该收）。")
        w("      所以这份只负责把『契约写了话、判据没反应』的**候选**列出来，逐条再由人判；")
        w("      真要判'漏没漏'，得把 DeriveBudget 的公式独立重算一遍再比 —— 那是下一支探针的活。")
        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
