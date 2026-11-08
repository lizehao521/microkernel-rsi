# -*- coding: utf-8 -*-
"""把输入集补全后新露出的 11 格（Measure 那一族）各推一条**真能到的第二判据**。

来由：`rule_hit_matrix.py` 的自核四补登了两份有效靶场报告，矩阵自己数出 `只被 1 条碰过的格 4 → 15`，
新增的 11 格全是 `measureCell` 那一族（tokens / usage_in / usage_out / fixed_overhead / wire_bytes_in /
wire_bytes_out / chunks / first_byte_ms / max_gap_ms / fail_status / retry_after_ms）。
这一轮就是看板 #62 的活。做法与前两轮同：**不猜哪条规则管这格，造值去撞，撞中哪条是哪条**，
期望那条事先写死源码行号，句子从源码现取（`contract_value_arms_v2.rule_template`）。

`Measure.Check` 自己就有 6 条（`measure.go:72/75/78/83/86/89`），这 11 格大多只被 `:89` 那一条碰过，
所以这一轮给它们分头去撞**别的**那几条；`fail_status`／`retry_after_ms` 两格撞的是失败族那几条
（`output.go:350/365`）—— 那两格 `Measure.Check` 根本不响（#55 的形状），所以只能从别条规则上找第二闸。

控制臂 A0 是合法装配，必须被收下（否则整批作废）。
跑法：python tasks/probe/thicken_round3.py [mcphttp.exe] [输出.txt]
"""
import hashlib
import io
import json
import os
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))
from contract_value_arms_v2 import rule_template, template_rx

DEMO = "演示·非真实流量"


def _set(d, **kw):
    d.update(kw)


ARMS = [
    ("tokens", "D1 有数却没说谁算的", ("measure.go", 86),
     lambda i, o: _set(i["Tokens"], Basis=0, Source="")),
    ("usage_in", "D2 缺席却带着出处", ("measure.go", 75),
     lambda i, o: _set(o["UsageIn"], Absent=True, N=0, Basis=2, Source="端点响应的 usage.prompt_tokens")),
    ("usage_out", "D3 被判不可信却没说为什么", ("measure.go", 78),
     lambda i, o: _set(o["UsageOut"], Absent=True, N=0, Basis=0, Refused=True, Why="")),
    ("wire_bytes_in", "D4 缺席却带着实测出处", ("measure.go", 75),
     lambda i, o: _set(o["WireBytesIn"], Absent=True, N=0, Basis=1)),
    ("wire_bytes_out", "D5 判不可信却不缺席", ("measure.go", 83),
     lambda i, o: _set(o["WireBytesOut"], Absent=False, Refused=True)),
    ("chunks", "D6 缺席却带着数", ("measure.go", 72),
     lambda i, o: _set(o["Chunks"], Absent=True, N=3, Basis=0)),
    ("first_byte_ms", "D7 缺席却带着出处", ("measure.go", 75),
     lambda i, o: _set(o["FirstByteMs"], Absent=True, N=0, Basis=1, Source=DEMO)),
    ("max_gap_ms", "D8 判不可信却没留理由", ("measure.go", 78),
     lambda i, o: _set(o["MaxGapMs"], Absent=True, N=0, Basis=0, Refused=True, Why="")),
    ("fixed_overhead", "D9 有数却没说谁算的", ("measure.go", 86),
     lambda i, o: _set(i["Budget"]["FixedOverhead"], Basis=0, Source="")),
    ("fail_status", "D10 没收到响应却记着状态码", ("output.go", 350),
     lambda i, o: (_set(o["Failure"], Kind="transport", Detail="演示：连接被重置"),
                   _set(o["Failure"]["Status"], Absent=False, N=404, Basis=1, Source=""))),
    ("retry_after_ms", "D11 没故障却记着要等 N 毫秒", ("output.go", 365),
     lambda i, o: _set(o["Failure"]["RetryAfterMs"], Absent=False, N=5000, Basis=1, Source="")),
]


def base(side_text):
    import attracta_probe as AP
    return json.loads(json.dumps(AP.legal_call(side_text)))


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "Measure那11格各推一条-11臂-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    port = 8815
    url = "http://127.0.0.1:%d/mcp" % port
    proc = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    def finish(code):
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
        return code

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
            return finish(4)
        w("== Measure 那 11 格各推一条第二判据 · %s ｜ 字节 sha256=%s ｜ 判据表照旧 %d 格 ==" % (
            time.strftime("%Y-%m-%d %H:%M:%S"),
            hashlib.sha256(io.open(exe, "rb").read()).hexdigest()[:16],
            len([1 for ln in head.splitlines() if "←" in ln])))
        tpl, bad = {}, []
        for k, name, ref, _fn in ARMS:
            tpl[name] = rule_template(ref[0], ref[1])
            if not tpl[name]:
                bad.append("%s(%s:%d)" % (name, ref[0], ref[1]))
        w("自核：模板取自源码 %d/%d 条 ｜ 取不到的：%s" % (len(ARMS) - len(bad), len(ARMS), " ".join(bad) or "无"))
        if bad:
            w("NOT_RUN：有臂的期望规则没从源码里取到句子，「撞对没撞对」无从判。")
            return finish(4)
        keys = set()
        for ln in head.splitlines()[1:]:
            if "←" not in ln:
                continue
            tok = ln.partition("←")[0].split()
            if len(tok) == 2:
                keys.add(tok[1])
        miss = [k for k, _n, _r, _f in ARMS if k not in keys]
        w("目标格都在格表里 = %s ｜ 不在的：%s" % ("是" if not miss else "**否**", " ".join(miss) or "无"))
        if miss:
            return finish(4)
        p0 = base("Measure 加厚的合法装配")
        _, c0 = call("kernel.check_call", {"session": "T3CTL", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        r0_txt, r0 = call("kernel.record_call", {"session": "T3CTL", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        w("A0 合法装配（必须被收下）：check=%s record=%s ｜ %s" % (c0, r0, r0_txt.replace("\n", " / ")[:110]))
        if r0:
            w("整批作废：合法装配都收不下，后面每条「被拒」都可能是在拒我的夹具。")
            return finish(5)
        w("")
        w("目标格 ｜ 臂 ｜ check ｜ record ｜ 同判 ｜ 撞对？ ｜ 原话尾巴")
        w("-" * 78)
        same_n = hit_n = 0
        accepted, off = [], []
        for idx, (k, name, ref, fn) in enumerate(ARMS):
            p = base("Measure 加厚的合法装配")
            fn(p["input"], p["output"])
            sid = "T3%02d" % idx
            c_txt, c_err = call("kernel.check_call", {"session": sid, "actor": "sweep", "input": p["input"], "output": p["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": sid, "actor": "sweep", "input": p["input"], "output": p["output"]})
            same_n += (c_err == r_err)
            txt = (r_txt or c_txt or "").replace("\n", " / ")
            hit = bool(template_rx(tpl[name]).search(txt))
            hit_n += hit
            if not r_err:
                accepted.append(name)
            elif not hit:
                off.append(name)
            tail = txt.split("kernel: ")[-1][:56] if r_err else ""
            w("%-16s ｜ %-22s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %-8s ｜ %s"
              % (k, name, c_err, r_err, "同判" if (c_err == r_err) else "**分歧**",
                 ("拒-撞对" if hit else ("**收下**" if not r_err else "拒-打偏")), tail))
            w("    └ 期望那条：%s:%d 模板「%s」" % (ref[0], ref[1], tpl[name][:62]))
        w("")
        w("== 自印分母 ==")
        w("臂 %d 条 ｜ 真打到内核 %d 条 ｜ A0 被收下 = 是 ｜ 预检与落账同判 %d/%d ｜ 撞对 %d ｜ 打偏 %d：%s ｜ 被收下 %d：%s"
          % (len(ARMS), len(ARMS), same_n, len(ARMS), hit_n, len(off), " ".join(off) or "无",
             len(accepted), " ".join(accepted) or "无"))
        w("读法：撞对的 11 格要从「1 条」升到「2 条」，得回写进矩阵才知道 —— 重跑 `rule_hit_matrix.py` 让它自己数；")
        w("      被收下 = 那条规则今天不咬这格（#55 那种「判据在、接线没接」的形状会在这里现形），那才是要报的。")
        return finish(0)
    finally:
        proc.terminate()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        import traceback
        d = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "Measure那11格-崩那跳-1017.txt")
        if os.path.exists(d):
            d = d + ".crash"
        io.open(d, "w", encoding="utf-8").write(traceback.format_exc())
        raise
