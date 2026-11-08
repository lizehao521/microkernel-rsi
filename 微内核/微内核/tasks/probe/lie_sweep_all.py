# -*- coding: utf-8 -*-
"""lie_sweep_all.py —— 把「把没量到的写成量到的」这一族谎**逐格**种一遍，不再靠我列 7 条。

分母不是我挑的，是代码自己登记的：`cells.go` 里用 `measureCell(...)` 登记的格才有出处档
（`Measure.Basis`），一共 11 格；其余 38 格没有这一档（那是 #42 未堵那半的结构原因，不在这条的账上）。
所以对这一族谎来说，「完整名单」= 这 11 格 × 一种谎（把 `Basis` 写成 measured=1 并谎称是自己量的），
而不是我手挑的几条臂。

每一格记三件事：预检 `isError`、落账 `isError`、拒收有没有点名这一格。
   #20 要的不变量：同一格上 **预检与落账必须同判**（一个说会拒、一个却收下 = 洞）。
   #48 说的那半：这一版不再印"点得到/点不到"（那要我先造一份中文名单，等于把尺子换成我的话），
   改成印三个不需要造词的数：拒收原话互不相同的条数、snake 键名原样出现的格数、JSON 字段名原样出现的格数。

跑法：python tasks/probe/lie_sweep_all.py <mcphttp.exe> <输出读数路径>
"""
import hashlib
import io
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "mcp"))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

MEASURE_CELLS = ["tokens", "fixed_overhead", "usage_in", "usage_out", "wire_bytes_out",
                 "wire_bytes_in", "chunks", "first_byte_ms", "max_gap_ms",
                 "fail_status", "retry_after_ms"]
# 每格谎成"我们自己量到的"：Basis=1(measured)、Absent=false，理由清空 —— 这是最常见的一种谎形状
LIE = {"N": 0, "Basis": 1, "Source": "", "Absent": False, "Refused": False, "Why": ""}
# 这些格谎成 measured 之外还得给个像样的数，否则会被别的判据先拦下（会混淆归因）
NONZERO = {"wire_bytes_out": 180, "wire_bytes_in": 96, "chunks": 5,
           "first_byte_ms": 700, "max_gap_ms": 180, "usage_in": 48, "usage_out": 12,
           "tokens": 25, "fixed_overhead": 300, "fail_status": 200, "retry_after_ms": 1500}

PATHS = {"tokens": ("input", "Tokens"), "fixed_overhead": ("input", "Budget", "FixedOverhead")}
OUT_PATHS = {"usage_in": "UsageIn", "usage_out": "UsageOut", "wire_bytes_out": "WireBytesOut",
             "wire_bytes_in": "WireBytesIn", "chunks": "Chunks", "first_byte_ms": "FirstByteMs",
             "max_gap_ms": "MaxGapMs", "fail_status": ("Failure", "Status"),
             "retry_after_ms": ("Failure", "RetryAfterMs")}


def setcell(d, path, val):
    cur = d
    for k in path[:-1]:
        cur = cur[k]
    cur[path[-1]] = val


def cell_path(name):
    """格 → 在那份装配里的真路径。输出侧必须带 "output" 这一段：
    上一版漏了它，谎被写进 pack 顶层（pack["UsageIn"]=…），而发出去时只发 input/output ——
    于是那 8 格**根本没被种上谎**，"全都收了"是个假结果（10-17 自查发现）。"""
    if name in PATHS:
        return PATHS[name]
    tail = OUT_PATHS[name]
    return ("output", tail) if isinstance(tail, str) else ("output",) + tuple(tail)


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    out = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "说谎者靶场-全格-1017.txt")
    if os.path.exists(out):
        print("REFUSE_TO_OVERWRITE: %s 已存在（那是上一轮的凭据）。换个输出路径给我。" % out)
        return 3
    import attracta_probe as AP  # 49 格能落账的那份底样只有一处，不自己拼
    port = 8793
    url = "http://127.0.0.1:%d/mcp" % port
    srv = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def call(n, a):
        b = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                        "params": {"name": n, "arguments": a}}).encode("utf-8")
        r = json.loads(urllib.request.urlopen(urllib.request.Request(
            url, data=b, headers={"Content-Type": "application/json"}), timeout=60).read().decode("utf-8"))
        res = r.get("result", {})
        return res.get("content", [{}])[0].get("text", ""), bool(res.get("isError"))

    lines = []
    w = lines.append
    try:
        for _ in range(30):
            try:
                call("kernel.celltable", {})
                break
            except Exception:
                time.sleep(1)
        else:
            w("NOT_RUN: 实例没起来（%s）—— 不产出空表冒充跑过" % exe)
            return 4
        ct, _ = call("kernel.celltable", {})
        rows = [l for l in ct.splitlines() if l.strip().startswith(("input", "output"))]
        w("== 说谎者靶场 · 全格逐种种谎 · %s · 字节 %s ==" % (time.strftime("%F %T"), os.path.basename(exe)))
        w("内核自报：`%s`" % ct.splitlines()[0][:96])
        w("表里行数（工具自印，不是我数的）=%d；带出处档的格（measureCell 登记）=%d 格 = 本次分母"
          % (len(rows), len(MEASURE_CELLS)))
        w("谎的形状只一种：把该格的 `Basis` 写成 measured(1)、`Absent=false`、理由清空 ——"
          "即「把没量到的写成自己量到的」。其余 38 格没有这一档，不归这条（见 #42 未堵那半）。")
        w("")
        w("格 ｜ 预检isError ｜ 落账isError ｜ 同判? ｜ 内核那句拒收原话（已抠掉重复的「kernel: 」前缀）")
        w("-" * 78)
        empty_reply = disagree = ran = 0
        refused_n = key_hits = field_hits = clause_len = 0
        clauses = {}
        for name in MEASURE_CELLS:
            pack = json.loads(json.dumps(AP.legal_call("全格靶场这一发的真 stdout")))
            path = cell_path(name)
            lie = dict(LIE)
            lie["N"] = NONZERO.get(name, 0)
            try:
                setcell(pack, path, lie)
            except Exception as ex:
                w("%-16s ｜ 装配不到这一格：%s（路径 %s）｜ — ｜ — ｜ —" % (name, ex, path))
                continue
            ran += 1
            side = "输入侧" if path[0] == "input" else "输出侧"
            if path[0] not in ("input", "output"):
                w("%-16s ｜ 路径不落在 input/output 任何一侧，这一格的谎没种上（%s）—— 记为没跑到" % (name, path))
                continue
            c_txt, c_err = call("kernel.check_call", {"session": "sweep-" + name, "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": "sweep-" + name, "actor": "sweep",
                                                       "input": pack["input"], "output": pack["output"]})
            if not c_txt or not r_txt:
                empty_reply += 1
            same = "同判" if c_err == r_err else "**分歧**"
            if c_err != r_err:
                disagree += 1
            txt = (c_txt or "") + " ⏎ " + (r_txt or "")
            # 「点名」这一栏以前只拿 snake 键名当尺子（`tokens` vs 内核原话「token 那一格」——差一个 s 就记成没点到），
            # 于是 8 条拒收全被印成"没点到这一格"，把 #48 报得比实际更窄。改成印三个**不需要我造中文名单**的数：
            #   内核那句话的原样（抠掉外壳的重复前缀）｜ 这句话在 8 臂里独不独特 ｜ 键名/JSON 字段名有没有原样出现过。
            inner = txt
            while "kernel: " in inner:
                inner = inner.split("kernel: ", 1)[1]
            inner = inner.split(" ⏎ ")[0].strip()
            if name in txt:
                key_hits += 1
            fld = path[-1] if isinstance(path[-1], str) else ""
            if fld and fld in txt:
                field_hits += 1
            if r_err:
                refused_n += 1
                clauses[name] = inner[:120]
                clause_len = max(clause_len, len(inner))
            w("%-16s(%s) ｜ check=%-5s ｜ record=%-5s ｜ %s ｜ 内核那句：%s"
              % (name, side, c_err, r_err, same, inner[:120] or "（回话为空）"))
        w("")
        w("== 自印分母 ==")
        w("分母=%d 格（带出处档的格，来自 cells.go 的 measureCell 登记）；这一轮真跑到=%d 格；装配不到=%d 格"
          % (len(MEASURE_CELLS), ran, len(MEASURE_CELLS) - ran))
        w("预检与落账**分歧**的格 = %d（#20 要的正是这个数为 0）" % disagree)
        w("回话为空的臂 = %d（要 0；非 0 则上面所有读数不可信）" % empty_reply)
        w("被拒的格 = %d；这 %d 条拒收原话互不相同的有 %d 条；"
          "回话里原样出现 snake 键名的 = %d 格；原样出现 JSON 字段名的 = %d 格；最长那句 %d 字"
          % (refused_n, refused_n, len(set(clauses.values())), key_hits, field_hits, clause_len))
        w("一句实话（这一栏测的是内核原话的形状，与 #20 那次改动无关）：")
        w("  · 「两两不同」只证明**每格各说一句**，不证明那句话能把格对上 —— 把「分帧数」认成 chunks 需要人（或一张表），")
        w("    而内核里没有这张注册表：拒收文案是逐条规则手写在各 check 里的（如 input.go:339「token 那一格」），"
          "没有 per-cell 的中文名字段；")
        w("  · 所以我**不印**「点名 N/8」这种数 —— 那要我先造一份中文名单，等于把尺子换成我的话（#20 那轮立的规矩：分母不许是我挑的清单）；")
        w("  · 结构化归因那一半仍开着 = **#48**：回话里没有机器可读的格键名（snake 键名命中 %d、字段名命中 %d，两个数就是这一句的读数）。"
          % (key_hits, field_hits))
        return 0
    finally:
        srv.terminate()
        try:
            srv.wait(timeout=10)
        except Exception:
            srv.kill()
        io.open(out, "w", encoding="utf-8").write("\n".join(lines) + "\n")
        print("REPORT=" + out.encode("unicode_escape", "backslashreplace").decode("ascii"))


if __name__ == "__main__":
    sys.exit(main())
