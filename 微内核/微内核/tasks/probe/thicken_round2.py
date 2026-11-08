# -*- coding: utf-8 -*-
"""把「只被一条规则接住」剩下的 11 格再推一轮 —— 八臂 + 三格明说到不了。

来由：`thicken_thin_cells.py` 把 16 格里能推的 5 格推到了 ≥2 条，矩阵自己数出剩 11 格只被 1 条接住。
这一轮接着推剩下的。做法与上一轮同：**不猜哪条规则管这格，直接造值去撞，撞中哪条是哪条**，
期望那条事先按源码行号写死，句子从源码现取（复用 `contract_value_arms_v2.rule_template`）。

八臂（格 ｜ 期望那条规则的源码行 ｜ 造的值）：
  C1 `fail_detail`         ｜ output.go:341 ｜ 说法末尾多敲一个空格（:336 要它非空，:341 要它不带首尾空白）
  C2 `fail_kind`           ｜ output.go:347 ｜ 类别记 `http`（按对端响应分的那几类）却不带状态码
  C3 `malformed_actions`   ｜ output.go:307 ｜ 执行不了的提议数写成负
  C4 `malformed_streak`    ｜ output.go:315 ｜ 这一发 0 个歪提议，链上连续计数写 2
  C5 `model_says_bytes`    ｜ output.go:382 ｜ 把原话字节数改小到低于「留下」那格
  C6 `model_says_kept`     ｜ output.go:382 ｜ 把「留下」那格改到高于原话
  C7 `model_says_sha256`   ｜ input.go:610  ｜ 64 位但全是非十六进制字符（:607 那条只量长度，量不到这个形状）
  C8 `wire_format_said`    ｜ output.go:251 ｜ 说「对端自报了格式」，可类别记的是压根没收到响应那一类
控制臂 A0 是合法装配，必须被收下（否则整批作废）。

到不了那 3 格（`notice_trimmed` / `finish_said` / `endpoint_model_said`）不硬造臂：
这一支把「源码里读这个字段名的每一行」全列出来并机械分类，让**处数**自印 ——
只有一条判据行时，第二闸要等新判据，不是我造个值就能变出来的。那是静态提示，不作覆盖结论。

跑法：python tasks/probe/thicken_round2.py [mcphttp.exe] [输出.txt]
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
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))
from contract_value_arms_v2 import rule_template, template_rx


def _fail(o, kind, detail):
    o["Failure"]["Kind"] = kind
    o["Failure"]["Detail"] = detail


ARMS = [
    ("fail_detail", "C1 说法末尾多敲一个空格", lambda o: _fail(o, "transport", "演示：说法末尾多一个空格 "), ("output.go", 341)),
    ("fail_kind", "C2 按响应分的类别却没有状态码", lambda o: _fail(o, "http", "演示：对端回了个 500"), ("output.go", 347)),
    ("malformed_actions", "C3 提议数写成负", lambda o: o.__setitem__("Malformed", -1), ("output.go", 307)),
    ("malformed_streak", "C4 这发没歪链上却记 2", lambda o: o.__setitem__("MalformedStreak", 2), ("output.go", 315)),
    ("model_says_bytes", "C5 原话改短到低于留下", lambda o: o["Says"].__setitem__("Bytes", 1), ("output.go", 382)),
    ("model_says_kept", "C6 留下改长到高于原话", lambda o: o["Says"].__setitem__("Kept", o["Says"]["Bytes"] + 5), ("output.go", 382)),
    ("model_says_sha256", "C7 64 位但不是十六进制", lambda o: o["Says"].__setitem__("SHA256", "g" * 64), ("input.go", 610)),
    ("wire_format_said", "C8 说自报格式却没收到响应", lambda o: (_fail(o, "transport", "演示：连接被重置"), o.__setitem__("WireFormatSaid", True), o.__setitem__("WireFormat", "chat.completion")), ("output.go", 251)),
]

UNREACHABLE = [("notice_trimmed", "Notice.Trimmed"),
               ("finish_said", "Finish.Said"),
               ("endpoint_model_said", "ModelSaid")]


def base(side_text):
    import attracta_probe as AP
    return json.loads(json.dumps(AP.legal_call(side_text)))


def go_lines(relpath):
    return io.open(os.path.join(ROOT, relpath), encoding="utf-8").read().splitlines()


def field_occurrences(selector):
    """全仓非测试 .go 里出现这个字段名的每一行，机械分类后原行一起交出来（谁都能复核我的处数）。"""
    hits = []
    for dirp, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in (".git", ".hermess-snapshots", "node_modules", "报告", "visualizations")]
        for fn in filenames:
            if not fn.endswith(".go") or fn.endswith("_test.go"):
                continue
            rel = os.path.relpath(os.path.join(dirp, fn), ROOT).replace("\\", "/")
            for i, l in enumerate(go_lines(rel), 1):
                if selector not in l:
                    continue
                s = l.strip()
                if s.startswith("//"):
                    tag = "注释"
                elif "cell(" in s:
                    tag = "记账行"
                elif re.search(r"\bif\b", l.split(selector)[0]):
                    tag = "if 条件"
                elif re.match(r"^[A-Za-z_]\w*\s+(bool|string|int|int64)\b", s):
                    tag = "字段声明"
                elif tag_is_err(s):
                    tag = "报错句"
                else:
                    tag = "其他"
                hits.append((rel, i, tag, s[:76]))
    return hits


def tag_is_err(s):
    return "fmt.Errorf" in s or "errorf" in s.lower()


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "薄那批再推一轮-八臂-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    port = 8814
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
        w("== 剩下的薄格再推一轮 · %s ｜ 字节 sha256=%s ｜ 判据表照旧 %d 格 ==" % (
            time.strftime("%Y-%m-%d %H:%M:%S"),
            hashlib.sha256(io.open(exe, "rb").read()).hexdigest()[:16],
            len([1 for ln in head.splitlines() if "←" in ln])))
        tpl, bad, wrapped = {}, [], set()
        for k, name, _fn, ref in ARMS:
            tpl[name] = rule_template(ref[0], ref[1])
            if not tpl[name]:
                bad.append("%s(%s:%d)" % (name, ref[0], ref[1]))
            elif tpl[name].rstrip().endswith("%v"):
                wrapped.add(name)
        w("自核：模板取自源码 %d/%d 条 ｜ 取不到的：%s" % (len(ARMS) - len(bad), len(ARMS), " ".join(bad) or "无"))
        if bad:
            w("NOT_RUN：有臂的期望规则没从源码里取到句子，「撞对没撞对」无从判。")
            return finish(4)
        w("自核：期望那条被 `rule_hit_matrix.py` 判成包装层（句尾 %%v）的臂 = %d：%s"
          % (len(wrapped), " ".join(sorted(wrapped)) or "无"))
        w("      —— 这类臂就算撞对，矩阵那本账也不计入闸数（是尺子的口径，不是这格的实情）。")
        keys = set()
        for ln in head.splitlines()[1:]:
            if "←" not in ln:
                continue
            tok = ln.partition("←")[0].split()
            if len(tok) == 2:
                keys.add(tok[1])
        miss = [k for k, _n, _f, _r in ARMS if k not in keys] + [k for k, _s in UNREACHABLE if k not in keys]
        w("目标格都在格表里 = %s ｜ 不在的：%s" % ("是" if not miss else "**否**", " ".join(miss) or "无"))
        if miss:
            return finish(4)
        p0 = base("再推一轮的合法装配")
        _, c0 = call("kernel.check_call", {"session": "TH2CTL", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        r0_txt, r0 = call("kernel.record_call", {"session": "TH2CTL", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        w("A0 合法装配（必须被收下）：check=%s record=%s ｜ %s" % (c0, r0, r0_txt.replace("\n", " / ")[:120]))
        if r0:
            w("整批作废：合法装配都收不下，后面每条「被拒」都可能是在拒我的夹具。")
            return finish(5)
        w("")
        w("目标格 ｜ 臂 ｜ check ｜ record ｜ 同判 ｜ 撞对？ ｜ 原话尾巴")
        w("-" * 78)
        same_n = hit_n = 0
        accepted, off, skipped = [], [], []
        for idx, (k, name, patch, ref) in enumerate(ARMS):
            p = base("再推一轮的合法装配")
            o = p["output"]
            if k == "model_says_bytes" and o["Says"]["Kept"] <= 1:
                skipped.append(name)
                w("%-18s ｜ %-24s ｜ 造不出值：夹具里「留下」那格本来就只有 %d 字节，改短原话撞不出「裁不会把话裁长」" % (k, name, o["Says"]["Kept"]))
                continue
            patch(o)
            sid = "T2%02d" % idx
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
            w("%-18s ｜ %-24s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %-8s ｜ %s"
              % (k, name, c_err, r_err, "同判" if (c_err == r_err) else "**分歧**",
                 ("拒-撞对" if hit else ("**收下**" if not r_err else "拒-打偏")), tail))
            w("    └ 期望那条：%s:%d 模板「%s」" % (ref[0], ref[1], tpl[name][:64]))
        w("")
        w("== 到不了那 3 格：源码里读这个字段名的每一行（机械分类，原始行一并印出供复核）==")
        for keyname, selector in UNREACHABLE:
            hits = field_occurrences(selector)
            njudge = len([1 for _r, _i, tag, _s in hits if tag == "if 条件"])
            w("   %-20s ｜ 字段名 %s ｜ 出现 %d 处，其中落在 if 条件里 %d 处" % (keyname, selector, len(hits), njudge))
            for rel, i, tag, s in hits:
                w("        %s:%d ｜ %s ｜ %s" % (rel, i, tag, s))
            if njudge == 1:
                w("        → 只有一条判据读它：第二闸得等新判据，不是我造个值就能多出来的。（静态提示，不作覆盖结论）")
        w("")
        w("== 自印分母 ==")
        w("臂 %d 条 ｜ 真打到内核 %d 条 ｜ 造不出值 %d：%s ｜ A0 被收下 = 是 ｜ 预检与落账同判 %d/%d ｜ 撞对 %d ｜ 打偏 %d：%s ｜ 被收下 %d：%s"
          % (len(ARMS), len(ARMS) - len(skipped), len(skipped), " ".join(skipped) or "无",
             same_n, len(ARMS) - len(skipped), hit_n, len(off), " ".join(off) or "无",
             len(accepted), " ".join(accepted) or "无"))
        w("到不了那 %d 格：只把处数印出来，不记成覆盖 —— 它们仍算「只被 1 条接住」，要报给作者。" % len(UNREACHABLE))
        w("读法：撞对的那几格要从「1 条」升到「2 条」，得回写进矩阵才知道 —— 重跑 `rule_hit_matrix.py`（把这份报告加进它的 REPORTS）让它自己数；")
        w("      打偏 = 我这批值同时踩了别的规则（回来改臂，不许记在期望那条头上）；被收下 = 这条规则今天不咬这格，那才是要报的。")
        return finish(0)
    finally:
        proc.terminate()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        import traceback
        d = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "薄那批再推一轮-崩那跳-1017.txt")
        if os.path.exists(d):
            d = d + ".crash"
        io.open(d, "w", encoding="utf-8").write(traceback.format_exc())
        raise
