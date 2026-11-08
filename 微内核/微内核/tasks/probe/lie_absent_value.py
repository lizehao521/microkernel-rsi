# -*- coding: utf-8 -*-
"""说谎者靶场 · 第二种谎形状：「说这一格没有，却带着一个数」。

为什么要单独一支：`mcpsrv.go:235` 那条不变量写的是「Absent=true 时 **Basis 必须是 0、N 必须是 0**，理由写在 Why」。
读码（本轮，两遍才读对）：
  ① Basis 那一半**由构造保证** —— `cells.go:44-50 measureCell()` 见 Absent 就把 Basis 覆写成 unset，
     调用方谎不出「缺席+实测」，所以 `lie_sweep_all.py` 那一支对 Basis 已经覆盖了；
  ② N 那一半**判据是有的**（`measure.go:68-80 Measure.Check()`：`Absent ⇒ N 必须 0`），
     我第一遍写成"全树找不到这条判据"是**读漏了** —— 真正的洞在**调用点**：
     全树对 `Measure.Check` 的调用只有 usage 族(`output.go:187`)、WireBytesIn(`:265`)、WireBytesOut(`:273`)、
     流式三格(`:294`)、`RetryAfterMs`(`:357`) —— **`Failure.Status` 一个都没有**，
     而失败那一族三条规则只看 `f.Status.Decidable()`（`:332/346/349`），`Absent=true` 就整条跳过。
     ⇒ 所以这一支实测的是"有判据、看它接到几格上"，不是"有没有判据"。
这一支就测②：把 11 个带出处档的格逐格改成 `Absent=true` **且 N 给一个非零数**，看
  · 预检与落账的 `isError` 同不同判（#20 的不变量在第二种谎形状上成立吗）；
  · 收下的话，账上**还能不能看出这格带来过一个数**（读回来给人看，不替它判定）。


分母**不许是我挑的清单**：这一支现场从 `cells.go`/`input.go`/`output.go` 里把 `measureCell("键", …)` 的调用
抠出来当分母，并与脚本里写死的那份对照；两份不一致就报出来（不是修一边，是印两边）。

跑法（不自己编译，先 go build 好 mcphttp）：
    python tasks/probe/lie_absent_value.py <mcphttp.exe> <输出文件.txt>
"""
import json
import os
import re
import subprocess
import sys
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "mcp"))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

DECLARED = ["tokens", "fixed_overhead", "usage_in", "usage_out", "wire_bytes_out",
             "wire_bytes_in", "chunks", "first_byte_ms", "max_gap_ms",
             "fail_status", "retry_after_ms"]
PATHS = {"tokens": ("input", "Tokens"), "fixed_overhead": ("input", "Budget", "FixedOverhead")}
OUT_PATHS = {"usage_in": "UsageIn", "usage_out": "UsageOut", "wire_bytes_out": "WireBytesOut",
             "wire_bytes_in": "WireBytesIn", "chunks": "Chunks", "first_byte_ms": "FirstByteMs",
             "max_gap_ms": "MaxGapMs", "fail_status": ("Failure", "Status"),
             "retry_after_ms": ("Failure", "RetryAfterMs")}
# 缺席却带数：N 给 7（任何非零都一样），Basis 顺手写 measured —— 明知它会被构造覆写，留着是为了看覆写发生没
LIE = {"N": 7, "Basis": 1, "Source": "", "Absent": True, "Refused": False, "Why": ""}


def measured_cells_from_source():
    """分母现场从源码抠：measureCell("键名", …) 的调用点。两个来源并排印，不合成一份。"""
    found, order = set(), []
    for fn in ("cells.go", "input.go", "output.go", "record.go", "measure.go"):
        p = os.path.join(ROOT, fn)
        if not os.path.exists(p):
            continue
        for m in re.finditer(r'measureCell\(\s*"([^"]+)"', io_open(p)):
            if m.group(1) not in found:
                found.add(m.group(1))
                order.append(m.group(1))
    return order


def io_open(p):
    import io
    return io.open(p, encoding="utf-8", errors="replace").read()


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
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "说谎者靶场-缺席带数-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在，那是上一轮的凭据。换个名字给我。" % outp)
        return 3
    import attracta_probe as AP
    src_cells = measured_cells_from_source()
    port = 8794
    url = "http://127.0.0.1:%d/mcp" % port
    proc = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write(s.encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
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
        # 预检闸门：端点没答就别印任何读数（这一族我犯过一次假绿）
        probe0, _ = call("kernel.celltable", {})
        if not probe0.strip():
            w("NOT_RUN：拿不到 kernel.celltable 的回话 —— 这一轮的数一个都不许读。")
            import io
            io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
            return 4
        w("== 说谎者靶场 · 第二种谎形状：Absent=true 却带着 N=7 · %s ==" % __import__("time").strftime("%Y-%m-%d %H:%M:%S"))
        w("字节 %s" % os.path.basename(exe))
        w("分母两份并排：源码现场抠出 %d 个 %s" % (len(src_cells), " ".join(src_cells)))
        w("              脚本写死     %d 个 %s" % (len(DECLARED), " ".join(DECLARED)))
        w("两份一致 = %s（不一致就照实印，不许修一边凑齐）" % (sorted(src_cells) == sorted(DECLARED)))
        cells = src_cells or DECLARED
        # 对照臂先跑一发合法的：整批发不起，后面每个"被拒"都不能算数（VACUOUS 那一族）
        base = json.loads(json.dumps(AP.legal_call("缺席带数这一支的底样")))
        c0, e0 = call("kernel.check_call", {"session": "ctl-legal", "actor": "sweep",
                                           "input": base["input"], "output": base["output"]})
        r0, er0 = call("kernel.record_call", {"session": "ctl-legal", "actor": "sweep",
                                             "input": base["input"], "output": base["output"]})
        w("对照臂（这份装配没被动过）：check isError=%s ｜ record isError=%s ｜ %s"
          % (e0, er0, (r0 or c0).replace("\n", " / ")[:100]))
        if er0:
            w("⇒ 对照臂就没落进去，这一支所有读数作废（底样不合法，不是内核拦了谎）。")
            import io
            io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
            return 5
        w("")
        w("格 ｜ 预检isError ｜ 落账isError ｜ 同判? ｜ 账上读回来的人话")
        w("-" * 78)
        ran = agree = accepted = empty_reply = 0
        for name in cells:
            pack = json.loads(json.dumps(AP.legal_call("缺席带数这一发的真 stdout")))
            path = cell_path(name)
            try:
                setcell(pack, path, dict(LIE))
            except Exception as ex:
                w("%-16s ｜ 装配不到这一格：%s（路径 %s）｜ — ｜ —" % (name, ex, path))
                continue
            ran += 1
            c_txt, c_err = call("kernel.check_call", {"session": "av-" + name, "actor": "sweep",
                                                     "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": "av-" + name, "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
            if not c_txt or not r_txt:
                empty_reply += 1
            agree += (c_err == r_err)
            if not r_err:
                accepted += 1
            rb, rbe = call("kernel.read_back", {"session": "av-" + name, "call": 1, "side": path[0]})
            line = next((l for l in (rb or "").split("\n") if name in l or (isinstance(path[-1], str) and path[-1] in l)), "")
            w("%-16s ｜ check=%-5s ｜ record=%-5s ｜ %s ｜ %s"
              % (name, c_err, r_err, "同判" if c_err == r_err else "**分歧**",
                 (line or rb or "（读不回）").replace("\n", " / ")[:110]))
            if r_err:
                w("     拒收原话：%s" % (r_txt or c_txt).replace("\n", " / ")[:140])
        w("")
        w("== 自印分母 ==")
        w("源码抠出的分母=%d ｜ 写死那份=%d ｜ 本轮跑到=%d ｜ 落账被收下=%d ｜ 预检与落账同判=%d ｜ 回话为空的臂=%d"
          % (len(src_cells), len(DECLARED), ran, accepted, agree, empty_reply))
        w("一句实话：『收下』= 内核没拒，不代表账上留下了痕迹 —— 上面那一列是**读回来的人话**，")
        w("        N=7 到底还看得见吗，看那一列里有没有 7；我在这份里不替它判定。")
        w("        这条不变量的另一半（Basis 不许是 measured）由构造保证：cells.go:44-50 见 Absent 就覆写 Basis，")
        w("        所以那一半谎不出来，不需要针 —— 这是本轮读码读出来的，不是我推的。")
        return 0
    finally:
        proc.terminate()
        import io
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
