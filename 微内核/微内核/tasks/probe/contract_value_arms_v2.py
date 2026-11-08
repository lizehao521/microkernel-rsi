# -*- coding: utf-8 -*-
"""#56 剩下的那半：非 Measure 那批格，逐格拿运行时撞一遍「这格到底有没有闸」。

为什么必须是运行时：同日 04:06 已经判定静态分类器不可引用（撞过的 13 条静态结论 0 条判对，
机制是表驱动传参与换指称物）。所以这一支不问代码，问内核：**给每一格造一个「按契约/按代码注释应当被拒」的值**，
看拒不拒、`isError` 对不对（前提②）。

v2 相对 v1（`报告/非Measure那批-运行时撞一遍-1017.txt`）改了两件事，都是我自己那把尺子的问题：
 1. **v1 的 N3 打偏**。那一臂只把 `MalformedStreak` 钉成 0、把 `Malformed` 抬到 3，而底样 `ToolCalls` 本来就是 0，
    于是先撞上 `output.go:310`（3 比 0 多），根本没走到 `:318`。撞出去的原话印的是「比提议总数 0 还多」，
    我当时把「被拒了」当成「那条规则有闸」——**臂名与撞中的规则不是一回事**。
 2. v2 因此不再只看「拒没拒」：每一臂的规则由**源码里的行号**现取 `fmt.Errorf` 模板，把 `%d/%s/%q/%v` 当通配，
    再去拒收原话里找这条模板。取到才算臂，撞中对上才算闸。模板一个字都不许我手抄。

每一臂改哪一格、期望哪条规则，出处都是行号（跑时现读源码）：
  N1 `Notice.Sent=true` 而 `Notice.Text=""`   → input.go:434     N2 `Malformed>ToolCalls`        → output.go:310
  N3 `Malformed>0` 而 `MalformedStreak<1`（v2 补齐 ToolCalls 让 :310 不抢跑）→ output.go:318
  N4 `WireFormat` 非空而 `WireFormatSaid=false` → output.go:245   N5 指纹非 64 位 → input.go:607（经 output.go:384）
  N6 `ContentBytes=-1` → 期望 input.go:396 那条**相加核账**接住（值域里没有「不许为负」这条，所以它是被间接拦的）
  N7 段表里也放负数（让相加核账无法抢跑）→ 期望 input.go:355 那条接住
  N8 `Says.Kept` 负数 → 期望 output.go:378；这一臂 v1 没有，是把「负数到底在哪几格有闸」铺全。
各臂该撞的那一句原话不抄在这里，跑时从源码那行现取并印进报告（样例短语我手抄错过一次）。
键名也不许我抄：跑前先拿 `kernel.celltable` 的 49 行现取「字段路径 → 键名」，行数对不上表头、
或某臂改的路径在格表里没有，都 `NOT_RUN` 退出（不给自己留「键名写错所以没指名」的假读数）。
控制臂（什么都没改）必须先被收下，否则整批作废。

跑法：python tasks/probe/contract_value_arms_v2.py <mcphttp.exe> [输出.txt]
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

# (臂名, 改哪儿(侧, 字段…), 值, 期望那条规则的源码位置(文件, 行), 附带要一起改的格)
ARMS = [
    ("N1 说发了却没正文", ("input", "Notice", "Sent"), True, ("input.go", 434), []),
    ("N2 执行不了比总数多", ("output", "Malformed"), 9, ("output.go", 310), [(("output", "ToolCalls"), 5)]),
    ("N3 有执行不了却连0", ("output", "MalformedStreak"), 0, ("output.go", 318),
     [(("output", "ToolCalls"), 3), (("output", "Malformed"), 3)]),
    ("N4 通道档没报却说得出", ("output", "WireFormatSaid"), False, ("output.go", 245),
     [(("output", "WireFormat"), "text/event-stream")]),
    ("N5 指纹不是64位", ("output", "Says", "SHA256"), "abc", ("input.go", 607), []),
    ("N6 内容字节给负数", ("input", "ContentBytes"), -1, ("input.go", 396), []),
    ("N7 段表也放负数", ("input", "ContentBytes"), -1, ("input.go", 355), [(("input", "Parts"), None)]),
    ("N8 留下字节给负数", ("output", "Says", "Kept"), -1, ("output.go", 378), []),
]


def setcell(d, path, val):
    cur = d
    for k in path[:-1]:
        cur = cur[k]
    cur[path[-1]] = val


def rule_template(fname, line):
    """从源码第 line 行（含后续续行）现取 fmt.Errorf 的第一段含 `kernel:` 的字符串，不手抄。

    注意：判据行常写成 `if strings.TrimSpace(x) == "" {`，那一行里就有**空字面量**，
    取"第一个字符串"会永远取到那个空的、于是报"取不到"（10-17 我这两支臂就是这么被挡下的）。
    所以要取**第一段含 `kernel:` 的**字面量，而不是第一段。
    """
    ls = io.open(os.path.join(ROOT, fname), encoding="utf-8").read().splitlines()
    buf = ""
    for i in range(line - 1, min(line + 4, len(ls))):
        buf += ls[i] + " "
        for m in re.finditer(r'"((?:[^"\\]|\\.)*)"', buf):
            if "kernel:" in m.group(1):
                return m.group(1)
    return ""


def template_rx(msg):
    """模板里的格式动词当通配；其余逐字。%q 打出带引号的值，所以通配不锚引号。"""
    parts = re.split(r"%[dsqvf]|%%", msg)
    return re.compile(".*?".join(re.escape(p) for p in parts))


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "非Measure那批-运行时撞一遍-v2-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    import attracta_probe as AP
    port = 8810
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
        # 先把 8 条模板全取出来；取不到或取成空串，是这支尺子坏了，不是内核没闸。
        tpl = {}
        bad = []
        for name, _p, _v, (f, ln), _n in ARMS:
            t = rule_template(f, ln)
            tpl[name] = t
            if not t:
                bad.append("%s(%s:%d)" % (name, f, ln))
        w("== 非 Measure 那批格 · 运行时逐格撞（v2）· %s ｜ 字节 sha256=%s =="
          % (time.strftime("%Y-%m-%d %H:%M:%S"),
             __import__("hashlib").sha256(io.open(exe, "rb").read()).hexdigest()[:16]))
        w("自核（钉我这把尺子）：模板取自源码 %d/%d 条 ｜ 取不到的：%s"
          % (len(ARMS) - len(bad), len(ARMS), " ".join(bad) or "无"))
        # 正对照（钉我这把匹配尺子）：把每条模板的动词各自填成「只有这一条臂会有的」假值，
        # 要求它能匹配自己、且匹配不到别的臂 —— 我手抄的样例短语错过一次（:355 真话是「那一行报了 %d 字节」），
        # 所以这里一律不抄，全部由源码模板现填。
        synth = {}
        ctl = []
        for i, (name, _p, _v, (f, ln), _n) in enumerate(ARMS):
            filled = re.sub(r"%[dsqvf]", lambda _m, k=i: "Q%dZ" % k, tpl[name])
            filled = filled.replace("%%", "%")
            synth[name] = filled
            others = [o for o in synth.values() if o != filled]
            ok_self = bool(template_rx(tpl[name]).search(filled))
            cross = any(bool(template_rx(tpl[name]).search(o)) for o in others)
            ctl.append((name, ok_self, cross))
        broke = [n for n, s, c in ctl if not s or c]
        w("正对照（模板自填假值）：自配 %d/%d 条 ｜ 会串到别的臂的：%s"
          % (len(ARMS) - len(broke), len(ARMS), " ".join(broke) or "无"))
        if broke:
            w("坏：这几条模板彼此分不开或自配不上，「撞对」这一列就不成立 —— 尺子坏，不算内核的读数。")
            return 6
        if bad:
            w("NOT_RUN：有臂的规则模板没从源码里取到，「撞对没撞对」就无从判。")
            return 4
        if len(set(tpl.values())) != len(ARMS):
            w("NOT_RUN：有两条臂取到同一段模板（%d 段去重后 %d 段），短语没区分开。"
              % (len(ARMS), len(set(tpl.values()))))
            return 4
        head, _ = call("kernel.celltable", {})
        if "唯一键名" not in head:
            w("NOT_RUN：端点没答。")
            return 4
        rows = {}
        for ln in head.splitlines()[1:]:
            if "←" not in ln:
                continue
            side_key, _, path = ln.partition("←")
            tok = side_key.split()
            if len(tok) == 2:
                rows[(tok[0], path.strip())] = tok[1]
        w("格表抠到 %d 行（表头钉 49）｜ 唯一键名 %d 个" % (len(rows), len(set(rows.values()))))
        if len(rows) != 49:
            w("NOT_RUN：格表行数和表头对不上，这一批臂的键名会全体错。")
            return 4
        for name, path, _v, _r, need in ARMS:
            for (p2, _v2) in need:
                if (p2[0], ".".join(p2[1:])) not in rows:
                    w("NOT_RUN：臂「%s」连带改的 %s.%s 在格表里没有这一格。" % (name, p2[0], ".".join(p2[1:])))
                    return 4
            if (path[0], ".".join(path[1:])) not in rows:
                w("NOT_RUN：臂「%s」改的 %s.%s 在格表里没有这一格 —— 先修臂再跑。"
                  % (name, path[0], ".".join(path[1:])))
                return 4
        w("")
        b = json.loads(json.dumps(AP.legal_call("值域臂 v2 这一发的真 stdout")))
        _, ce = call("kernel.check_call", {"session": "ctlV2", "actor": "sweep", "input": b["input"], "output": b["output"]})
        _, re0 = call("kernel.record_call", {"session": "ctlV2", "actor": "sweep", "input": b["input"], "output": b["output"]})
        w("底样相关几格原值：ToolCalls=%s Malformed=%s MalformedStreak=%s ContentBytes=%s Notice=%s "
          "WireFormat=%r WireFormatSaid=%s Says=%s Parts=%s"
          % (b["output"]["ToolCalls"], b["output"]["Malformed"], b["output"]["MalformedStreak"],
             b["input"]["ContentBytes"], b["input"]["Notice"], b["output"]["WireFormat"],
             b["output"]["WireFormatSaid"], b["output"]["Says"], b["input"]["Parts"]))
        w("控制臂（什么都没改）：check=%s record=%s —— 收不下就整批作废" % (ce, re0))
        if re0:
            return 5
        w("")
        w("臂 ｜ 那一格键名 ｜ check ｜ record ｜ 同判 ｜ 撞中对上这条规则？ ｜ 拒收原话")
        w("-" * 78)
        named = 0
        accepted = []
        same_n = 0
        missed = []
        for name, path, val, (f, ln), need in ARMS:
            pack = json.loads(json.dumps(AP.legal_call("值域臂 v2 这一发的真 stdout")))
            for (p2, v2) in need:
                if v2 is None:
                    pack["input"]["Parts"] = [{"Kind": "system", "Parts": 1, "Bytes": -1},
                                              {"Kind": "user", "Parts": 1, "Bytes": 9}]
                    pack["input"]["Order"] = ["system", "user"]
                    pack["input"]["Budget"]["Shares"] = {"system": 55, "user": 45}
                else:
                    setcell(pack, p2, v2)
            setcell(pack, path, val)
            mykey = rows[(path[0], ".".join(path[1:]))]
            c_txt, c_err = call("kernel.check_call", {"session": "W" + name[:2], "actor": "sweep",
                                                     "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": "W" + name[:2], "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
            same = (c_err == r_err)
            same_n += same
            txt = (r_txt or c_txt or "")
            hit = bool(template_rx(tpl[name]).search(txt.replace("\n", " ")))
            if mykey in txt:
                named += 1
            if not r_err:
                accepted.append(name)
            elif not hit:
                missed.append(name)
            w("%-20s ｜ %-20s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %-6s ｜ %s"
              % (name, mykey, c_err, r_err, "同判" if same else "**分歧**",
                 "撞对" if hit else ("**收下**" if not r_err else "**打偏**"),
                 txt.replace("\n", " / ")[:100]))
            w("    └ 期望那条：%s:%d 模板「%s」" % (f, ln, tpl[name][:64]))
        w("")
        w("== 自印分母 ==")
        w("臂 %d 条 ｜ 落账被拒 %d 条 ｜ 被收下 %d 条：%s ｜ 预检与落账同判 %d/%d"
          % (len(ARMS), len(ARMS) - len(accepted), len(accepted), " ".join(accepted) or "无", same_n, len(ARMS)))
        w("被拒的里面：撞中对上期望那条规则 %d 条 ｜ 打偏（拒了，但不是这条规则拦的）%d 条：%s"
          % (len(ARMS) - len(accepted) - len(missed), len(missed), " ".join(missed) or "无"))
        w("拒收原话里指名了那一格（键名由格表现取）%d/%d 条" % (named, len(ARMS)))
        w("读法：「打偏」不是内核的错，是**我这批臂造的值同时踩了别条规则**——v1 的 N3 就属于这种，")
        w("      它印的是 `:310` 那句，我却按 `:318` 记了一功。这一列就是为了不再犯这个错才加的。")
        w("      「被收下」才是 #56 要的情形：契约/注释写了那条，运行时却不拦。")
        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
