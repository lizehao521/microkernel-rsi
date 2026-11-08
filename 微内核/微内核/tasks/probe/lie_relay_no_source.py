# -*- coding: utf-8 -*-
"""说谎者靶场 · 第三种谎形状：「说是转述的，却不说转述谁」（Basis=relayed ＋ Source 空）。

为什么要这第三种：前两支测的都是 `Measure.Check` 那一族（Basis 谎、缺席带数），
而**同一件事在树上还有第二个 owner**：`cells.go:163-165 CheckCells` 也有一条 `needsSource ⇒ 必须有 Source`，
它的消息模板是 `kernel: %s的 %s 那一格说是 %s，却没说出处` —— **第二个 %s 是 `c.Key`，也就是 snake 键名**。
⇒ 这一支真正要量的不是"拦不拦得住"，而是 **#48 那半边到底有没有救**：
   同一个内核里，有的判据族开口就带机器可读的键名，有的只给人话标签。
   如果这一族 11/11 都带键名，那 #48 的修法就有了现成的先例（把归因统一到 CheckCells 那种消息上）。

分母仍然由代码自印：从 `cells.go`/`input.go`/`output.go` 抠 `measureCell("键", …)` 的调用点。
控制臂先跑一发没动过的：整批落不进去，后面每个"被拒"都不算数（VACUOUS 那一族我犯过）。

跑法：python tasks/probe/lie_relay_no_source.py <mcphttp.exe> <输出.txt>
"""
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

PATHS = {"tokens": ("input", "Tokens"), "fixed_overhead": ("input", "Budget", "FixedOverhead")}
OUT_PATHS = {"usage_in": "UsageIn", "usage_out": "UsageOut", "wire_bytes_out": "WireBytesOut",
             "wire_bytes_in": "WireBytesIn", "chunks": "Chunks", "first_byte_ms": "FirstByteMs",
             "max_gap_ms": "MaxGapMs", "fail_status": ("Failure", "Status"),
             "retry_after_ms": ("Failure", "RetryAfterMs")}
LIE = {"N": 5, "Basis": 2, "Source": "", "Absent": False, "Refused": False, "Why": ""}


def measure_keys():
    ks, seen = [], set()
    for fn in ("cells.go", "input.go", "output.go", "record.go", "measure.go"):
        p = os.path.join(ROOT, fn)
        if not os.path.exists(p):
            continue
        txt = __import__("io").open(p, encoding="utf-8", errors="replace").read()
        for m in re.finditer(r'measureCell\(\s*"([^"]+)"', txt):
            if m.group(1) not in seen:
                seen.add(m.group(1))
                ks.append(m.group(1))
    return ks


def cell_path(name):
    if name in PATHS:
        return PATHS[name]
    tail = OUT_PATHS[name]
    return ("output", tail) if isinstance(tail, str) else ("output",) + tuple(tail)


def setcell(d, path, val):
    cur = d
    for k in path[:-1]:
        cur = cur[k]
    cur[path[-1]] = val


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "说谎者靶场-转述不给出处-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: 换名给我，%s 已存在" % outp.encode("unicode_escape"))
        return 3
    import attracta_probe as AP
    import io
    cells = measure_keys()
    port = 8801
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
            w("NOT_RUN：端点没答，这一轮一个数都不许读。")
            return 4
        w("== 说谎者靶场 · 第三种谎形状：Basis=relayed(2) ＋ Source 空 · %s ==" % time.strftime("%Y-%m-%d %H:%M:%S"))
        w("分母现场从源码抠：%d 个 measureCell 键 -> %s" % (len(cells), " ".join(cells)))
        base = json.loads(json.dumps(AP.legal_call("第三种形状的控制臂")))
        _, ce0 = call("kernel.check_call", {"session": "ctl3", "actor": "sweep", "input": base["input"], "output": base["output"]})
        _, re0 = call("kernel.record_call", {"session": "ctl3", "actor": "sweep", "input": base["input"], "output": base["output"]})
        w("控制臂（没动过）：check=%s record=%s —— 这一发必须能被收下，否则后面全是假数" % (ce0, re0))
        if re0:
            w("⇒ 控制臂就红了，底样不合法，这一轮读数作废。")
            return 5
        w("")
        w("格 ｜ 预检isError ｜ 落账isError ｜ 同判? ｜ 消息里带的是键名还是人话标签")
        w("-" * 78)
        ran = agree = refused = key_hits = label_hits = empty_reply = 0
        for name in cells:
            pack = json.loads(json.dumps(AP.legal_call("第三种形状这一发的真 stdout")))
            path = cell_path(name)
            try:
                setcell(pack, path, dict(LIE))
            except Exception as ex:
                w("%-16s ｜ 装配不到：%s ｜ — ｜ — ｜ —" % (name, ex))
                continue
            ran += 1
            c_txt, c_err = call("kernel.check_call", {"session": "rs-" + name, "actor": "sweep",
                                                     "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": "rs-" + name, "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
            if not c_txt or not r_txt:
                empty_reply += 1
            agree += (c_err == r_err)
            txt = (c_txt or "") + " ⏎ " + (r_txt or "")
            inner = txt
            while "kernel: " in inner:
                inner = inner.split("kernel: ", 1)[1]
            inner = inner.split(" ⏎ ")[0].strip()
            k = name in txt
            lab = bool(re.search(r'[一-鿿][^，。\s]{0,10}\s*那一格|说是 [^\s]+ 的，却没说来源', inner)) and not k
            key_hits += k
            label_hits += lab
            if r_err:
                refused += 1
            w("%-16s ｜ check=%-5s ｜ record=%-5s ｜ %s ｜ %s ｜ %s"
              % (name, c_err, r_err, "同判" if c_err == r_err else "**分歧**",
                 "带键名" if k else ("只带人话标签" if lab else "两样都不带"), inner[:96]))
        w("")
        w("== 自印分母 ==")
        w("分母=%d ｜ 跑到=%d ｜ 落账被拒=%d ｜ 预检与落账同判=%d ｜ 回话为空的臂=%d" % (len(cells), ran, refused, agree, empty_reply))
        w("消息里**带 snake 键名**的=%d ｜ **只带人话标签**的=%d ｜ 两样都不带的=%d"
          % (key_hits, label_hits, ran - key_hits - label_hits))
        w("读法：这一族如果键名命中 = 分母，说明内核**已经有一种带机器可读格名的拒收消息**"
          "（`cells.go:163-165` 那句用 c.Key），#48 就不是「从无到有」，而是「把另一族也接到这个先例上」。")
        w("它不证明：不证明所有判据族都带键名 —— 前两支量的 `Measure.Check` 那一族实测 0/8 带键名"
          "（`报告/说谎者靶场-全格-改后-原话三数-1017.txt:30`）。两个数分开数，不许相加。")
        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
