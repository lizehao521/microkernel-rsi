# -*- coding: utf-8 -*-
"""把"只被一条规则接住"的几格推到 ≥2 条 —— 五臂，每臂的期望规则给到源码行号。

来由：`rule_hit_matrix.py` 数出 16 格只被 1 条接住。我先试了一支**静态扫候选**的尺子
（`rule_candidates_for_thin_cells.py`），它自核就没过（条件列会把上 8 行里别人的 `if` 认领过来，
`fail_kind` 那种词还会扫出 32 条噪声）—— 那份表当场被判「不许引用」。
所以换成现在这支：**不猜哪条规则管这格，直接造值去撞，撞中哪条是哪条**，期望由源码行号事先写死。

五臂（格 ｜ 期望那条规则的源码行 ｜ 造的值）：
  B1 `downgrade`   ｜ output.go:234 ｜ `Streaming=false` 而 `Failure.Downgrade=true`
  B2 `streaming`   ｜ output.go:303 ｜ 非流式那一发把 `Chunks` 报成 measured 3
  B3 `tool_calls`  ｜ output.go:323 ｜ 原话 0 字节、留下 0 字节、0 个提议，却记成正常收尾
  B4 `notice_sent` ｜ input.go:447  ｜ 通知说"发出去了"（正文非空，避开 :434），但段表里没有 truncation-notice 那一段
  B5 `parts`       ｜ input.go:358  ｜ 同一类段在段表里占两行
控制臂 A0 是合法装配，必须被收下（否则整批作废：那说明后面每条「被拒」可能都在拒我的夹具）。
模板匹配复用 `contract_value_arms_v2` 的 `rule_template`/`template_rx`：那句话从源码现取，我一个字都不抄。

跑法：python tasks/probe/thicken_thin_cells.py [mcphttp.exe] [输出.txt]
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

ARMS = [
    ("downgrade", "B1 不是流式却建议换非流式", ("output",), {"Failure": {"Kind": "transport", "Detail": "演示：连接被重置", "Downgrade": True}}, ("output.go", 234)),
    ("streaming", "B2 非流式却报了分帧数", ("output",),
     {"Chunks": {"N": 3, "Basis": 1, "Source": "\u6f14\u793a\x20\xb7\xe9\x9d\x9e\xe7\x9c\x9f\xe5\xae\x9e\xe6\xb5\x81\xe9\x87\x8f", "Absent": False, "Refused": False, "Why": ""}}, ("output.go", 303)),
    ("tool_calls", "B3 什么都没回却记正常收尾", ("output",),
     {"Says": {"Bytes": 0, "Kept": 0, "SHA256": None}, "ToolCalls": 0}, ("output.go", 323)),
    ("notice_sent", "B4 说发出去却段表没那段", ("input",),
     {"Notice": {"Sent": True, "Trimmed": False, "Why": "", "Text": "\u6f14\u793a\xb7\xe9\x9d\x9e\u771f\u5b9e\u6d41\u91cf：本节被裁过"}}, ("input.go", 447)),
    ("parts", "B5 同一类段占两行", ("input",),
     {"Parts": [{"Kind": "system", "Parts": 1, "Bytes": 11}, {"Kind": "user", "Parts": 1, "Bytes": 9},
                {"Kind": "system", "Parts": 1, "Bytes": 11}]}, ("input.go", 358)),
]


def base(side_text):
    import attracta_probe as AP
    return json.loads(json.dumps(AP.legal_call(side_text)))


def merge(dst, patch, sha_text):
    for k, v in patch.items():
        if isinstance(v, dict):
            sub = dict(dst[k])
            for kk, vv in v.items():
                if kk == "SHA256" and vv is None:
                    continue
                sub[kk] = vv
            dst[k] = sub
        else:
            dst[k] = v


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "薄那几格推到两条-五臂-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    port = 8813
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
        w("== 把「只被一条接住」的格推到 ≥2 条 · %s ｜ 字节 sha256=%s ==" % (
            time.strftime("%Y-%m-%d %H:%M:%S"), hashlib.sha256(io.open(exe, "rb").read()).hexdigest()[:16]))
        tpl, bad = {}, []
        for k, name, _s, _p, ref in ARMS:
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
        miss = [k for k, _n, _s, _p, _r in ARMS if k not in keys]
        w("目标格都在格表里 = %s ｜ 不在的：%s" % ("是" if not miss else "**否**", " ".join(miss) or "无"))
        if miss:
            return finish(4)
        p0 = base("加厚这批的合法装配")
        _, c0 = call("kernel.check_call", {"session": "THCTL", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        r0_txt, r0 = call("kernel.record_call", {"session": "THCTL", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        w("A0 合法装配（必须被收下）：check=%s record=%s ｜ %s" % (c0, r0, r0_txt.replace("\n", " / ")[:120]))
        if r0:
            w("整批作废：合法装配都收不下，后面每条「被拒」都可能是在拒我的夹具。")
            return finish(5)
        w("")
        w("目标格 ｜ 臂 ｜ check ｜ record ｜ 同判 ｜ 撞对？ ｜ 原话尾巴")
        w("-" * 78)
        same_n = hit_n = 0
        accepted, off = [], []
        for idx, (k, name, side, patch, ref) in enumerate(ARMS):
            p = base("加厚这批的合法装配")
            merge(p["input"] if side[0] == "input" else p["output"], patch, None)
            sid = "TH%02d" % idx
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
            tail = txt.split("kernel: ")[-1][:58] if r_err else ""
            w("%-14s ｜ %-24s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %-8s ｜ %s"
              % (k, name, c_err, r_err, "同判" if (c_err == r_err) else "**分歧**",
                 ("拒-撞对" if hit else ("**收下**" if not r_err else "拒-打偏")), tail))
            w("    └ 期望那条：%s:%d 模板「%s」" % (ref[0], ref[1], tpl[name][:64]))
        w("")
        w("== 自印分母 ==")
        w("臂 %d 条 ｜ A0 被收下 = 是 ｜ 预检与落账同判 %d/%d ｜ 撞对 %d ｜ 打偏 %d：%s ｜ 被收下 %d：%s"
          % (len(ARMS), same_n, len(ARMS), hit_n, len(off), " ".join(off) or "无",
             len(accepted), " ".join(accepted) or "无"))
        w("读法：撞对的那几格从「1 条」升到「2 条」，要回写进矩阵才知道 —— 重跑一次 `rule_hit_matrix.py` 让它自己数；")
        w("      打偏 = 我这批值同时踩了别的规则（回来改臂，不许记在期望那条头上）；被收下 = 这条规则今天不咬这格，那才是要报的。")
        return finish(0)
    finally:
        proc.terminate()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        import traceback
        d = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "薄那几格推到两条-崩那跳-1017.txt")
        if os.path.exists(d):
            d = d + ".crash"
        io.open(d, "w", encoding="utf-8").write(traceback.format_exc())
        raise
