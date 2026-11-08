# -*- coding: utf-8 -*-
"""#56 的最后一批：`Cuts#folded / Cuts#clipped / Cuts#dropped` 这三格（覆盖账面里唯一"还没撞过"的可达格）。

账面那份说还剩 6 格：3 个方法格（载荷里根本没这条路）+ 这 3 个动态档（字段路径带 `#`）。
动态档不是"没值可改"，是**要构造 `Cuts` 表才撞得动** —— 这一支就构造它。

臂全部住在 `Cut` 那一族里：`input.go:545 Cut.check`、`:407-421` 跨发引用、`:426` 挤了段却不通知、
`:459 checkCutsAgainstParts`（裁剪表与段表两张对一遍）。每臂给期望规则的**文件:行号**，
那句话从源码现取 `fmt.Errorf` 模板再拿原话去撞（沿用 `contract_value_arms_v2` 的 `rule_template`/`template_rx`，一把尺子不抄第二份）。

A0 是**合法装配**，必须被收下 —— 收不下就是夹具不对，整批作废（exit 5），不许读成"内核太严"。
A7 是我读码时冒出的怀疑，专门留着验：`Squeeze` 填一个不在四种取值里的串收不收（`input.go:596` 有个 `default:`，
读码说该拒 —— 读码不算数，撞出来才算）。
A0 落账之后还要 `kernel.read_back` 把三格读回来贴进报告：**这一格被这一发算进去了没有要看得见**，不能只凭"这发被收了"。

改法用显式操作而不是散开的 lambda：
  ("cut", 字段, 值)      改 Cuts[0] 那一条
  ("set", 路径, 值)      改 input 里任意路径
  ("dropkind", 类名)     把段表/顺序/份额里这一类整行撤掉，并重算内容总字节

跑法：python tasks/probe/lie_cuts_arms.py [mcphttp.exe 路径] [输出.txt]
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


def hex64(s):
    return hashlib.sha256(s.encode("utf-8")).hexdigest()


def base_pack():
    """合法装配：system+user+history 三段加一句知情通知，一条「折成桩」的历史裁剪记录。"""
    import attracta_probe as AP
    p = json.loads(json.dumps(AP.legal_call("Cuts 族这一发的真 stdout")))
    i = p["input"]
    # 两侧发号必须一致（`record.go:71`「两侧的发号对不上」）—— 第一跳我就只改了输入侧，被这一条整批作废。
    i["Call"] = 2
    p["output"]["Call"] = 2
    i["Order"] = ["system", "user", "history", "truncation-notice"]
    i["Parts"] = [{"Kind": "system", "Parts": 1, "Bytes": 11},
                  {"Kind": "user", "Parts": 1, "Bytes": 9},
                  {"Kind": "history", "Parts": 1, "Bytes": 30},
                  {"Kind": "truncation-notice", "Parts": 1, "Bytes": 20}]
    i["ContentBytes"] = 11 + 9 + 30 + 20
    i["Budget"]["Shares"] = {"system": 35, "user": 25, "history": 30, "truncation-notice": 10}
    i["Budget"]["LeftoverPct"] = 0
    i["Cuts"] = [{"Kind": "history", "Ref": "第 1 发的历史段（演示值·非真实流量）", "RefCall": 1,
                  "Role": "assistant", "Squeeze": "folded", "FromBytes": 100, "ToBytes": 30,
                  "SHA256": hex64("一段被折掉的历史原文（演示）")}]
    i["Notice"] = {"Sent": True, "Trimmed": False, "Why": "演示：这一段历史被折成桩",
                   "Text": "本节被裁过：一段历史折成桩（演示值·非真实流量）"}
    return p


def apply_ops(inp, ops):
    for op in ops:
        if op[0] == "cut":
            inp["Cuts"][0][op[1]] = op[2]
        elif op[0] == "set":
            cur = inp
            for k in op[1][:-1]:
                cur = cur[k]
            cur[op[1][-1]] = op[2]
        elif op[0] == "dropkind":
            k = op[1]
            inp["Parts"] = [x for x in inp["Parts"] if x["Kind"] != k]
            while k in inp["Order"]:
                inp["Order"].remove(k)
            sh = dict(inp["Budget"]["Shares"])
            sh.pop(k, None)
            inp["Budget"]["Shares"] = sh
            # 撤掉一档就得重算「剩余额度」：v2 那跳我没算，5 条臂全被「剩余额度记的是 0%，而份额相加是 70」
            # 这条先拦下 —— 那就是打偏，不是它们各自期望的那条规则。
            inp["Budget"]["LeftoverPct"] = 100 - sum(sh.values())
            inp["ContentBytes"] = sum(x["Bytes"] for x in inp["Parts"])
    return inp


# (目标键名, 臂名, 操作, 期望那条规则 (文件, 行))
ARMS = [
    ("folded", "A0 合法装配（应被收下）", [], None),
    ("folded", "A1 折过之后比原文还大", [("cut", "ToBytes", 140)], ("input.go", 575)),
    ("folded", "A2 折过之后 0 字节", [("cut", "ToBytes", 0)], ("input.go", 572)),
    ("clipped", "A3 裁尾却没留下前缀", [("cut", "Squeeze", "tail-dropped"), ("cut", "ToBytes", 100)], ("input.go", 585)),
    ("dropped", "A4 说整段没进却记着字节", [("cut", "Squeeze", "dropped-whole"), ("cut", "ToBytes", 5),
                                          ("dropkind", "history"), ("set", ("Notice", "Sent"), False),
                                          ("set", ("Notice", "Why"), "演示：整段没进")], ("input.go", 592)),
    ("dropped", "A5 未知 squeeze 取值（读码说该拒）", [("cut", "Squeeze", "squeezed-其他（演示）"),
                                                  ("cut", "ToBytes", 0), ("dropkind", "history"),
                                                  ("set", ("Notice", "Sent"), False),
                                                  ("set", ("Notice", "Why"), "演示")], ("input.go", 597)),
    ("folded", "A6 squeeze 是空（没被挤）", [("cut", "Squeeze", "")], ("input.go", 547)),
    ("folded", "A7 说话人不在四种里", [("cut", "Role", "nobody")], ("input.go", 550)),
    ("folded", "A8 原文指纹不是 64 位", [("cut", "SHA256", "abc")], ("input.go", 607)),
    ("folded", "A9 Ref 是空的", [("cut", "Ref", "  ")], ("input.go", 560)),
    ("folded", "A10 system 这一类不许折", [("cut", "Kind", "system"), ("cut", "ToBytes", 5)], ("input.go", 565)),
    ("clipped", "A11 system 这一类是整段的（裁尾）", [("cut", "Kind", "system"), ("cut", "Squeeze", "tail-dropped"),
                                                  ("cut", "ToBytes", 5)], ("input.go", 579)),
    ("dropped", "A12 system 记成整段没进", [("cut", "Kind", "system"), ("cut", "Squeeze", "dropped-whole"),
                                          ("cut", "ToBytes", 0)], ("input.go", 590)),
    ("dropped", "A13 挤掉历史没说来自第几发", [("cut", "Squeeze", "dropped-whole"), ("cut", "ToBytes", 0),
                                             ("cut", "RefCall", 0), ("dropkind", "history"),
                                             ("set", ("Notice", "Sent"), False), ("set", ("Notice", "Why"), "演示")], ("input.go", 414)),
    ("folded", "A14 引用了未来那一发", [("cut", "RefCall", 9)], ("input.go", 417)),
    ("folded", "A15 留下字节比段表还多", [("cut", "ToBytes", 60)], ("input.go", 465)),
    ("folded", "A16 留下字节却段表没这行", [("cut", "ToBytes", 30), ("dropkind", "history"),
                                          ("set", ("Notice", "Why"), "演示：历史整类没留行")], ("input.go", 476)),
    ("dropped", "A17 挤了段却不通知也不说为什么", [("cut", "Squeeze", "dropped-whole"), ("cut", "ToBytes", 0),
                                                ("dropkind", "truncation-notice"),
                                                ("set", ("Notice", "Sent"), False), ("set", ("Notice", "Why"), ""),
                                                ("set", ("Notice", "Text"), "")], ("input.go", 426)),
]
TARGETS = ("folded", "clipped", "dropped")


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "动态档那三格-Cuts族撞一遍-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    port = 8812
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
            w("NOT_RUN：端点没答（这支尺子还没开始量）。")
            return finish(4)
        w("== 动态档那三格 · Cuts 族逐条撞 · %s ｜ 字节 sha256=%s ==" % (
            time.strftime("%Y-%m-%d %H:%M:%S"), hashlib.sha256(io.open(exe, "rb").read()).hexdigest()[:16]))
        tpl, bad = {}, []
        for _k, name, _ops, ref in ARMS:
            if ref is None:
                tpl[name] = ""
                continue
            tpl[name] = rule_template(ref[0], ref[1])
            if not tpl[name]:
                bad.append("%s(%s:%d)" % (name, ref[0], ref[1]))
        w("自核：模板取自源码 %d/%d 条 ｜ 取不到的：%s" % (len(ARMS) - 1 - len(bad), len(ARMS) - 1, " ".join(bad) or "无"))
        if bad:
            w("NOT_RUN：有臂的期望规则没从源码里取到句子，「撞对没撞对」无从判。")
            return finish(4)
        reg = set()
        for ln in head.splitlines()[1:]:
            if "←" not in ln:
                continue
            tok = ln.partition("←")[0].split()
            if len(tok) == 2:
                reg.add(tok[1])
        absent = [t for t in TARGETS if t not in reg]
        w("三格在格表里吗：%s ｜ 不在的：%s" % ("/".join(TARGETS), " ".join(absent) or "无"))
        if absent:
            w("NOT_RUN：目标格名与格表对不上，覆盖归因会全体错。")
            return finish(4)
        w("")
        p0 = base_pack()
        _, c0 = call("kernel.check_call", {"session": "CUTBASE", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        r0_txt, r0 = call("kernel.record_call", {"session": "CUTBASE", "actor": "sweep", "input": p0["input"], "output": p0["output"]})
        w("A0 合法装配：check=%s record=%s ｜ 底样 %s"
          % (c0, r0, json.dumps({k: p0["input"][k] for k in ("Call", "Order", "ContentBytes")}, ensure_ascii=False)))
        w("   内容字节相加 %d ｜ 段表 %s ｜ 份额 %s ｜ 裁剪 %s"
          % (sum(x["Bytes"] for x in p0["input"]["Parts"]),
             json.dumps(p0["input"]["Parts"], ensure_ascii=False),
             json.dumps(p0["input"]["Budget"]["Shares"], ensure_ascii=False),
             json.dumps(p0["input"]["Cuts"], ensure_ascii=False)[:150]))
        if r0:
            w("整批作废：合法装配都收不下 —— 原话：%s" % r0_txt.replace("\n", " / ")[:200])
            return finish(5)
        rb_txt, rb_err = call("kernel.read_back", {"session": "CUTBASE", "call": 2, "side": "input"})
        w("A0 落账后 read_back（isError=%s）里带这三档名的行：" % rb_err)
        got = 0
        for ln in rb_txt.splitlines():
            if any(t in ln for t in TARGETS):
                got += 1
                w("   %s" % ln.strip()[:130])
        w("   读到 %d 行（该有 3 行）｜ 读不到就别把「这三格被算进去了」当结论" % got)
        w("")
        w("目标键 ｜ 臂 ｜ check ｜ record ｜ 同判 ｜ 撞对？ ｜ 原话尾巴")
        w("-" * 78)
        same_n = hit_n = 0
        accepted, off = [], []
        for idx, (k, name, ops, ref) in enumerate(ARMS):
            if ref is None:
                continue
            p = apply_ops(base_pack()["input"], ops)
            pack = base_pack()
            pack["input"] = p
            sid = "CUT%02d" % idx
            c_txt, c_err = call("kernel.check_call", {"session": sid, "actor": "sweep", "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": sid, "actor": "sweep", "input": pack["input"], "output": pack["output"]})
            same_n += (c_err == r_err)
            txt = (r_txt or c_txt or "").replace("\n", " / ")
            hit = bool(template_rx(tpl[name]).search(txt))
            hit_n += hit
            if not r_err:
                accepted.append(name)
            elif not hit:
                off.append(name)
            tail = txt.split("kernel: ")[-1][:58] if r_err else ""
            state = "**收下**" if not r_err else ("拒-撞对" if hit else "拒-打偏")
            w("%-8s ｜ %-30s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %-8s ｜ %s"
              % (k, name, c_err, r_err, "同判" if (c_err == r_err) else "**分歧**", state, tail))
            w("    └ 期望那条：%s:%d 模板「%s」" % (ref[0], ref[1], tpl[name][:70]))
        w("")
        n = len(ARMS) - 1
        w("== 自印分母 ==")
        w("臂 %d 条（A0 合法装配 1 条 + 谎臂 %d 条）｜ A0 被收下 = %s ｜ 预检与落账同判 %d/%d"
          % (len(ARMS), n, "是", same_n, n))
        w("谎臂里：撞中对上期望那条规则 %d ｜ 打偏（拒了但不是这条拦的）%d：%s ｜ 被收下 %d：%s"
          % (hit_n, len(off), " ".join(off) or "无", len(accepted), " ".join(accepted) or "无"))
        w("覆盖归因：这一批打的目标键名 = %s ｜ 各臂数 %s"
          % ("/".join(TARGETS), " ".join("%s=%d" % (t, sum(1 for a in ARMS if a[0] == t)) for t in TARGETS)))
        w("读法：A0 收下证明夹具合法，后面的「被拒」才是判据在拒谎；")
        w("      「打偏」要回来改臂（我这批值同时踩了别的规则），不许记在期望那条规则头上；")
        w("      「被收下」= 这一族某个值域今天没人管。演示串都带「演示值·非真实流量」，别读成真流量。")
        return finish(0)
    finally:
        proc.terminate()


if __name__ == "__main__":
    sys.exit(main())
