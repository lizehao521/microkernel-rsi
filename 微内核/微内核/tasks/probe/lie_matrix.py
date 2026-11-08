# -*- coding: utf-8 -*-
"""#55 的范围到底多大：把 5 种形状 × 全部 11 格铺成一张矩阵跑。

为什么要铺满：`lie_status_blast.py` 只试了 3 格（`fail_status` + 两格对照），
结论是「`Measure.Check` 5 条里有 3 条在这格从来不响」。但**只有这一格漏**这句话还没核过 ——
万一别的格也漏一条，#55 就不是接线问题而是范围问题。

矩阵：分母（格）由源码现场抠 `measureCell("键", …)`；分子（形状）是 `Measure.Check` 里那 5 条规则各自的破坏形状。
每一格 × 每一形状都跑**预检 + 落账两遍**，所以顺带又把 #20 的不变量数了一遍（同判率）。

读法（钉死的三件事）：
  · 一格一形状：`收下` 而它在别的格上是 `拒` ⇒ 那一格在这一条判据上没接线（#55 的形状）；
  · 满盘皆`收下` ⇒ 那这条判据本身没实现，不是接线问题（今天不该出现这种列）；
  · 满盘皆`拒`   ⇒ 这条判据与格子无关，由 `CheckCells` 那一层兜住（也可能出现，见 `fail_status` 的 S4/S5）。
跑法：python tasks/probe/lie_matrix.py <mcphttp.exe> [输出.txt]
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
sys.path.insert(0, os.path.join(ROOT, "tasks", "mcp"))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

PATHS = {"tokens": ("input", "Tokens"), "fixed_overhead": ("input", "Budget", "FixedOverhead")}
OUT_PATHS = {"usage_in": "UsageIn", "usage_out": "UsageOut", "wire_bytes_out": "WireBytesOut",
             "wire_bytes_in": "WireBytesIn", "chunks": "Chunks", "first_byte_ms": "FirstByteMs",
             "max_gap_ms": "MaxGapMs", "fail_status": ("Failure", "Status"),
             "retry_after_ms": ("Failure", "RetryAfterMs")}
SHAPES = [
    ("S1 缺席带数", {"N": 7, "Basis": 1, "Source": "", "Absent": True, "Refused": False, "Why": ""}, "measure.go:71-73"),
    ("S2 被拒无理由", {"N": 0, "Basis": 0, "Source": "", "Absent": True, "Refused": True, "Why": ""}, "measure.go:77-79"),
    ("S3 被拒非缺席", {"N": 5, "Basis": 5, "Source": "", "Absent": False, "Refused": True, "Why": ""}, "measure.go:81-83"),
    ("S4 有数无出处", {"N": 999, "Basis": 0, "Source": "", "Absent": False, "Refused": False, "Why": ""}, "measure.go:85-87"),
    ("S5 转述无来源", {"N": 5, "Basis": 2, "Source": "", "Absent": False, "Refused": False, "Why": ""}, "measure.go:88-90"),
]


def measure_keys():
    ks, seen = [], set()
    for fn in ("cells.go", "input.go", "output.go", "record.go", "measure.go"):
        p = os.path.join(ROOT, fn)
        if not os.path.exists(p):
            continue
        txt = io.open(p, encoding="utf-8", errors="replace").read()
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
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "说谎者靶场-五形状乘全格矩阵-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    import attracta_probe as AP
    cells = measure_keys()
    port = 8806
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
            w("NOT_RUN：端点没答，一个数都不许读。")
            return 4
        base = json.loads(json.dumps(AP.legal_call("矩阵的控制臂")))
        _, ce = call("kernel.check_call", {"session": "ctlM", "actor": "sweep", "input": base["input"], "output": base["output"]})
        _, re_ = call("kernel.record_call", {"session": "ctlM", "actor": "sweep", "input": base["input"], "output": base["output"]})
        w("== 说谎者靶场 · `Measure.Check` 5 种形状 × 全部 %d 格 · %s ==" % (len(cells), time.strftime("%Y-%m-%d %H:%M:%S")))
        w("分母由源码现场抠出 measureCell 键 = %d 个 ｜ 控制臂 check=%s record=%s（必须收得下，否则整盘作废）"
          % (len(cells), ce, re_))
        if re_:
            w("⇒ 控制臂红，整盘作废（VACUOUS 那一族）。")
            return 5
        w("")
        w("格 ＼ 形状   ｜ " + " ｜ ".join(n for n, _, _ in SHAPES))
        w("-" * 78)
        slips, agree, total = {}, 0, 0
        col_accept = {n: 0 for n, _, _ in SHAPES}
        for name in cells:
            row = []
            for sname, lie, _where in SHAPES:
                pack = json.loads(json.dumps(AP.legal_call("矩阵这一发的真 stdout")))
                try:
                    setcell(pack, cell_path(name), dict(lie))
                except Exception:
                    row.append("装配不到")
                    continue
                total += 1
                _, c_err = call("kernel.check_call", {"session": "M-%s-%s" % (name, sname[:2]), "actor": "sweep",
                                                     "input": pack["input"], "output": pack["output"]})
                _, r_err = call("kernel.record_call", {"session": "M-%s-%s" % (name, sname[:2]), "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
                agree += (c_err == r_err)
                row.append("拒" if r_err else "收下")
                if not r_err:
                    col_accept[sname] += 1
                    slips.setdefault(name, []).append(sname)
            w("%-14s ｜ " % name + " ｜ ".join("%-10s" % x for x in row))
        w("")
        w("== 自印分母 ==")
        w("格子 %d ｜ 形状 %d ｜ 跑了 %d 发（预检+落账各一次，共 %d 次调用）｜ 预检与落账同判 %d/%d"
          % (len(cells), len(SHAPES), total, total * 2, agree, total))
        w("每一形状被**收下**的格数（=该条判据的覆盖面，越少越说明它接线接得全）：")
        for sname, _lie, where in SHAPES:
            w("   %-14s %-16s 收下 %d/%d 格" % (sname, where, col_accept[sname], len(cells)))
        if slips:
            w("漏的格（该格在这一条判据上没接线）：")
            for k in sorted(slips):
                w("   %-16s 漏 %d 条：%s" % (k, len(slips[k]), " ".join(slips[k])))
        else:
            w("没有任何一格漏：5 条判据在全部 %d 格上都响。" % len(cells))
        w("不许读成的两件事：①「某形状全部收下」也不等于判据没实现（可能由 `CheckCells` 那层兜住，看是否满盘皆拒）；")
        w("              ②这张矩阵测的是**判据接没接到格**，不测判据本身对不对 —— 后者是判据表的事，48 键/49 格这一轮没动。")
        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
