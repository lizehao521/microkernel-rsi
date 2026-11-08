# -*- coding: utf-8 -*-
"""#55 的**杀伤半径**：`fail_status` 这一格到底能塞进多少种形状。

看板 #55 现在只说了一件事（缺席却带数被收下）。但没接线的不是"一条判据"，是**整个 `Measure.Check` 那一族**
（`measure.go:68-92` 里那 5 条规则全在同一个函数里）。所以真正要说清的是：**这 5 条里有几条在这格上从来不响**。

方法：同一格（`output.Failure.Status`）逐种形状试，另外**并排两格对照组**（`tokens`、`chunks`）——
同一种形状在对照组响、在 `fail_status` 不响，才能说"是这一格漏了"，而不是"这形状本来合法"。
每种形状都跑两遍：预检 `kernel.check_call` 与落账 `kernel.record_call`，看 #20 的同判在这一族上成不成立。

形状表（都在 `Measure.Check` 里，行号是本轮读码定的）：
  S1 `Absent=true` ＋ `N=7`              → `:71-73`「报的是缺席，却带着数」
  S2 `Refused=true` ＋ `Absent=true` ＋ `Why=""` → `:77-79`「说是被判成不可信的，却没说为什么」
  S3 `Refused=true` ＋ `Absent=false`           → `:81-83`「说是被判成不可信的，却又不是缺席」
  S4 `Basis=unset` ＋ `N=999` ＋ `Absent=false`  → `:85-87`「有数却没说谁算的（basis 空）—— 会被下游当成实测」
  S5 `Basis=relayed` ＋ `Source=""`             → `:88-90`「说是 relayed 的，却没说来源」

跑法：python tasks/probe/lie_status_blast.py <mcphttp.exe> <输出.txt>
"""
import json
import os
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "mcp"))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

CELLS = [("fail_status", ("output", "Failure", "Status")), ("tokens", ("input", "Tokens")),
         ("chunks", ("output", "Chunks"))]
SHAPES = [
    ("S1 缺席却带数", {"N": 7, "Basis": 1, "Source": "", "Absent": True, "Refused": False, "Why": ""},
     "measure.go:71-73"),
    ("S2 被拒却不留理由", {"N": 0, "Basis": 0, "Source": "", "Absent": True, "Refused": True, "Why": ""},
     "measure.go:77-79"),
    ("S3 被拒却不是缺席", {"N": 5, "Basis": 5, "Source": "", "Absent": False, "Refused": True, "Why": ""},
     "measure.go:81-83"),
    ("S4 有数却不说谁算的", {"N": 999, "Basis": 0, "Source": "", "Absent": False, "Refused": False, "Why": ""},
     "measure.go:85-87"),
    ("S5 转述却不给出处", {"N": 5, "Basis": 2, "Source": "", "Absent": False, "Refused": False, "Why": ""},
     "measure.go:88-90"),
]


def setcell(d, path, val):
    cur = d
    for k in path[:-1]:
        cur = cur[k]
    cur[path[-1]] = val


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "说谎者靶场-fail_status杀伤半径-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    import attracta_probe as AP
    import io
    port = 8802
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
        return "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text"), bool(res.get("isError"))

    try:
        time.sleep(1.2)
        p0, _ = call("kernel.celltable", {})
        if not p0.strip():
            w("NOT_RUN：端点没答，一个数都不许读。")
            return 4
        base = json.loads(json.dumps(AP.legal_call("杀伤半径的控制臂")))
        _, ce = call("kernel.check_call", {"session": "ctlB", "actor": "sweep", "input": base["input"], "output": base["output"]})
        _, re_ = call("kernel.record_call", {"session": "ctlB", "actor": "sweep", "input": base["input"], "output": base["output"]})
        w("== 说谎者靶场 · #55 的杀伤半径：同一格试 5 种形状 × 3 格对照 · %s ==" % time.strftime("%Y-%m-%d %H:%M:%S"))
        w("控制臂（没动过）：check=%s record=%s —— 这一发收不下就整轮作废" % (ce, re_))
        if re_:
            return 5
        w("")
        w("形状 ｜ 规则在哪 ｜ fail_status 拒? ｜ tokens 拒? ｜ chunks 拒? ｜ 同判?")
        w("-" * 78)
        slips = agree = total = 0
        for label, lie, where in SHAPES:
            row = {}
            same = True
            for cname, cpath in CELLS:
                pack = json.loads(json.dumps(AP.legal_call("杀伤半径这一发的真 stdout")))
                setcell(pack, cpath, dict(lie))
                c_txt, c_err = call("kernel.check_call", {"session": "B-" + label[:2] + cname, "actor": "sweep",
                                                         "input": pack["input"], "output": pack["output"]})
                r_txt, r_err = call("kernel.record_call", {"session": "B-" + label[:2] + cname, "actor": "sweep",
                                                          "input": pack["input"], "output": pack["output"]})
                row[cname] = r_err
                same = same and (c_err == r_err)
                if r_err and cname == "fail_status":
                    row["_msg"] = (r_txt or "").replace("\n", " / ")[:130]
            total += 1
            agree += same
            if not row["fail_status"]:
                slips += 1
            w("%-18s ｜ %-16s ｜ %-10s ｜ %-8s ｜ %-8s ｜ %s"
              % (label, where, "收下了" if not row["fail_status"] else "拒",
                 "拒" if row["tokens"] else "收下", "拒" if row["chunks"] else "收下",
                 "同判" if same else "**分歧**"))
            # 拒了也得说是**哪条规则**拒的 —— 不然"这一格被守住了"可能是别处顺手拦的，归因就假了
            w("%s 这一格收下/拒的原话：%s" % ("      ↳" if row["fail_status"] else "      ↳（收下，无拒收原话）",
                                    row.get("_msg", "")))
        w("")
        w("== 自印分母 ==")
        w("形状 %d 种 ｜ fail_status 被收下 = %d 种（这一族规则在它身上从来不响的条数）｜ 对照组里两格全拒的形状 = 见上表"
          % (total, slips))
        w("预检与落账同判的形状数 = %d/%d" % (agree, total))
        w("读法：对照组（tokens/chunks）同一形状若拒、而 fail_status 收下 —— 差别的来源是**这一格没接进 Measure.Check**，")
        w("      不是这形状本来合法。反之若对照组也收下，那是这形状本来就允许，不算漏。")
        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
