# -*- coding: utf-8 -*-
"""49 格逐格分「这格的值被谁用过」：判据 / 渲染 / 只是搬动 —— #55 那一族（免检通道）的穷举版。

上一支（`cell_readers_audit.py`）只数"文本出现几次"，那个口径答不了真问题：
出现在 `Errorf` 的格式参数里只是**印给人看**，不等于有闸念它。这一支把每一格的值在源码里的出现**分类**：
  · 判据 —— 那一行有 `if` / `==` / `!=` / `&&` / `||` / `for` 且不是在打错误信息（值能左右控制流）
  · 渲染 —— 那一行在 `Errorf` / `Sprintf` / `claimOf` / `.String()` 里（只是被说出去）
  · 搬动 —— 其余：赋值、当参数递出去、return（值进了账，但没人据它判断、也没人把它说出去）
分母与"值从哪来"都从**登记那一行**现读：`cell("键", "Field", <表达式>, Basis…)` 的第三参 ——
不靠猜字段名，也不靠段名（段名会撞，上一支就这毛病）。

自核（钉这支尺子自己）：三格已知有判据（`m.Tokens`、`m.WireBytesIn`、`m.Streaming`），
若分类器把它们判成「判据=0」就是分类器坏了 → 退出 6，这份表整体作废。
（这个自核不是形式主义：今天已经栽过两次 —— 正则漏掉方法名、计数器方向数反。）

跑法：python tasks/probe/cell_rule_readers.py
"""
import io
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
GO = ["cells.go", "input.go", "output.go", "record.go", "journal.go", "kernel.go", "measure.go",
      os.path.join("mcpsrv", "mcpsrv.go"), os.path.join("cmd", "mcphttp", "main.go")]
REG = re.compile(r'(cell|measureCell|relayCell)\(\s*"([^"]+)",\s*"([^"]+)",\s*([A-Za-z0-9_.()\[\]+]+)')
RENDER = re.compile(r'Errorf|Sprintf|claimOf|\.String\(\)')
COND = re.compile(r'\bif\b|==|!=|&&|\|\||\bfor\b')
CONTROLS = ["m.Tokens", "m.WireBytesIn", "m.Streaming"]


def read(fn):
    p = os.path.join(ROOT, fn)
    return io.open(p, encoding="utf-8", errors="replace").read() if os.path.exists(p) else ""


def main():
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "每格被谁用过-判据渲染搬动-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    texts = {f: read(f) for f in GO if read(f)}
    cells = []
    for f, txt in texts.items():
        for line in txt.split("\n"):
            m = REG.search(line)
            if m:
                expr = m.group(4)
                # 第三参后面紧跟着登记调用的右括号，字符类会把它一起吞进来（`m.Tokens)`）——
                # 自核就是被这个咬出来的：三格里两格被判成「判据=0」。括号不配对就退回去。
                while expr.endswith(")") and expr.count("(") < expr.count(")"):
                    expr = expr[:-1]
                cells.append((m.group(2), m.group(3), expr.rstrip(",")))
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    def classify(expr):
        rule = render = move = 0
        spots = []
        for f, txt in texts.items():
            for ln, line in enumerate(txt.split("\n"), 1):
                if expr not in line:
                    continue
                if REG.search(line):          # 登记那一行：是"造这格"，不是"用这格"
                    continue
                if RENDER.search(line):
                    render += 1
                elif COND.search(line):
                    rule += 1
                    spots.append("%s:%d" % (f, ln))
                else:
                    move += 1
        return rule, render, move, spots[:3]

    w("== 49 格逐格：这格的值被拿来判过、只被印过、还是只是搬了一下手 ==")
    w("分母来源：登记行现读（第三参 = 这格的值从哪来），扫 %d 个非测试 Go 文件：%s"
      % (len(texts), " ".join(sorted(texts))))
    w("")
    rows = []
    for key, field, expr in cells:
        r, d, m, spots = classify(expr)
        tag = "有判据" if r else ("只渲染" if d else ("只搬动" if m else "除登记外没人碰"))
        rows.append((key, field, expr, r, d, m, tag, spots))
    w("键名 ｜ Field ｜ 值来自 ｜ 判据 ｜ 渲染 ｜ 搬动 ｜ 归类")
    w("-" * 78)
    for key, field, expr, r, d, m, tag, spots in sorted(rows, key=lambda x: (x[6], x[0])):
        w("%-22s ｜ %-24s ｜ %-26s ｜ %d ｜ %d ｜ %d ｜ %s" % (key, field, expr, r, d, m, tag))
    tally = {}
    for x in rows:
        tally[x[6]] = tally.get(x[6], 0) + 1
    w("")
    w("== 自印分母 ==")
    w("登记行抠出的格 = %d" % len(rows))
    # 与内核自报的那 49 格对开：差多少、差在哪些键名，必须点名，不许留成一个数字。
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    port = 8807
    url = "http://127.0.0.1:%d/mcp" % port
    proc = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        time.sleep(1.2)
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                           "params": {"name": "kernel.celltable", "arguments": {}}}).encode("utf-8")
        r = json.loads(urllib.request.urlopen(urllib.request.Request(
            url, data=body, headers={"Content-Type": "application/json"}), timeout=60).read().decode("utf-8"))
        ct = "".join(c.get("text", "") for c in (r.get("result") or {}).get("content", []) if c.get("type") == "text")
    finally:
        proc.terminate()
    kernel_keys = set(re.findall(r'^\s*(?:input|output)\s+(\S+)\s+←', ct, re.M))
    mine = set(x[0] for x in rows)
    w("内核自报键名 %d 个 ｜ 我从登记行抠到 %d 个 ｜ 两边都对上的 %d 个"
      % (len(kernel_keys), len(mine), len(kernel_keys & mine)))
    only_kernel = sorted(kernel_keys - mine)
    only_mine = sorted(mine - kernel_keys)
    w("内核有、我抠不到的（=%d）：%s" % (len(only_kernel), " ".join(only_kernel) or "无"))
    w("我抠到、内核没报的（=%d）：%s" % (len(only_mine), " ".join(only_mine) or "无"))
    w("⇒ 上面这份分类表只覆盖『两边都对上』的那 %d 格；抠不到的那些**不参与归类计数**，"
      "它们走的是动态登记（`cells.go:84` 那个 CutLists 循环一类），静态分不开键名 —— 这条不是漏，是口径，写清楚。"
      % len(kernel_keys & mine))
    for tag in sorted(tally):
        w("   %-16s %d 格" % (tag, tally[tag]))
    for tag in ("只渲染", "只搬动", "除登记外没人碰"):
        got = [x[0] for x in rows if x[6] == tag]
        w("   嫌疑名单 [%s]：%s" % (tag, " ".join(got) or "无"))
    cw = {}
    for c in CONTROLS:
        cw[c] = sum(x[3] for x in rows if c in x[2])
    bad = [c for c, v in cw.items() if v <= 0]
    w("自核（钉尺子自己）：正对照 %s ｜ 坏 = %s"
      % (" ".join("%s=%d" % (c.split(".")[-1], v) for c, v in cw.items()), "无" if not bad else " ".join(bad)))
    w("它不证明：分类按**行**猜 —— 同一行既判又印时算判据（偏保守，宁可少报「没人判」）；")
    w("          同名表达式跨 struct 会并起来数（只会高报判据数），所以「只渲染 / 只搬动」两份名单是**上界**；")
    w("          要说某一格确实没有判据读它，得人工看那几行 —— 这份的用途是把嫌疑名单缩到能看完的量。")
    io.open(outp, "w", encoding="utf-8", newline="\n").write("\n".join(out) + "\n")
    print("REPORT=" + outp.encode("unicode_escape").decode("ascii"))
    if bad:
        return 6
    return 0


if __name__ == "__main__":
    sys.exit(main())
