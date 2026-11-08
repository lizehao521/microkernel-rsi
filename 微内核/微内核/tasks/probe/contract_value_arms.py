# -*- coding: utf-8 -*-
"""#56 剩下的那半：非 Measure 那批格，逐格拿运行时撞一遍「这格到底有没有闸」。

为什么必须是运行时：同日 04:06 已经判定静态分类器不可引用（撞过的 13 条静态结论 0 条判对，
机制是表驱动传参与换指称物）。所以这一支不问代码，问内核：**给每一格造一个"按契约/按代码注释应当被拒"的值**，
看拒不拒、`isError` 对不对（前提②）、拒的那句带不带机器可读键名（#48 的数据点）。

每一臂的"该拒"都有出处，不靠我编：
  N1 `Notice.Sent=true` 而 `Notice.Text=""`      → `input.go:434`「通知报的是「发出去了」，却没有正文」
  N2 `ToolCalls=5` 而 `Malformed=9`              → `output.go:310`「两个数不是从同一个集合数出来的」
  N3 `Malformed=3` 而 `MalformedStreak=0`        → `output.go:318`
  N4 `WireFormat` 非空而 `WireFormatSaid=false`  → 真流量那轮实测过（`报告/真流量-第一发被内核拒收-1017.txt`）
  N5 `Says.SHA256="abc"`（非 64 位十六进制）      → `contract.md §2.4`「必须 64 位十六进制，唯一判据函数 `checkFingerprint`」
  N6 `ContentBytes=-1`                           → 值域里没这条，**期望写"无依据"**，只记录行为（不许事后改期望凑结论）
控制臂必须先绿；每一臂跑预检 + 落账两遍。

键名也不许我抄：跑前先拿 `kernel.celltable` 的 49 行现取「字段路径 → 键名」，
行数对不上表头、或某臂改的路径在格表里没有，都 `NOT_RUN` 退出（不给自己留"键名写错所以没指名"的假读数）。

跑法：python tasks/probe/contract_value_arms.py <mcphttp.exe> [输出.txt]
"""
import io
import json
import os
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "mcp"))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

# (臂名, 改哪儿(侧, 路径…), 值, 期望, 期望的出处)
ARMS = [
    ("N1 说发了却没正文", ("input", "Notice", "Sent"), True, "该拒", "input.go:434"),
    ("N2 执行不了比总数多", ("output", "Malformed"), 9, "该拒", "output.go:310"),
    ("N3 有执行不了却连0", ("output", "MalformedStreak"), 0, "该拒", "output.go:318"),
    ("N4 通道档没报却说得出", ("output", "WireFormatSaid"), False, "该拒", "真流量实测（报告/真流量-第一发被内核拒收-1017.txt）"),
    ("N5 指纹不是 64 位", ("output", "Says", "SHA256"), "abc", "该拒", "contract.md §2.4 checkFingerprint"),
    ("N6 内容字节给负数", ("input", "ContentBytes"), -1, "无依据，只记录", "值域 §2.4 没这条"),
]
# N2/N3 需要连带动 ToolCalls / Malformed，让臂本身是"应当被拒"的那种不一致
NEED = {
    "N2 执行不了比总数多": [(("output", "ToolCalls"), 5)],
    "N3 有执行不了却连0": [(("output", "Malformed"), 3)],
    "N4 通道档没报却说得出": [(("output", "WireFormat"), "text/event-stream")],
}


def setcell(d, path, val):
    cur = d
    for k in path[:-1]:
        cur = cur[k]
    cur[path[-1]] = val


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "非Measure那批-运行时撞一遍-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    import attracta_probe as AP
    port = 8809
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
            w("NOT_RUN：端点没答。")
            return 4
        # 键名不许我抄：从内核自己印的格表里取（`侧 键名 ← 字段路径`）。
        # 每一臂先把"我改的那条字段路径"对上表里的键名 —— 对不上说明这一臂改的根本不是一格，
        # 那这条臂的读数就不该进分母，得先修臂。
        rows = {}
        for ln in head.splitlines()[1:]:
            if "←" not in ln:
                continue
            side_key, _, path = ln.partition("←")
            tok = side_key.split()
            if len(tok) == 2:
                rows[(tok[0], path.strip())] = tok[1]
        w("格表抠到 %d 行（表头钉 49）｜ 唯一键名 %d 个 ｜ 分隔符 %s"
          % (len(rows), len(set(rows.values())), "←" if "←" in head else "**没有 ←**"))
        if len(rows) != 49:
            w("NOT_RUN：格表行数和表头对不上，这一批臂的键名会全体错。")
            return 4
        for name, path, _v, _w2, _y in ARMS:
            if (path[0], ".".join(path[1:])) not in rows:
                w("NOT_RUN：臂「%s」改的 %s.%s 在格表里没有这一格 —— 先修臂再跑。"
                  % (name, path[0], ".".join(path[1:])))
                return 4
        w("")
        b = json.loads(json.dumps(AP.legal_call("值域臂的控制臂")))
        _, ce = call("kernel.check_call", {"session": "ctlV", "actor": "sweep", "input": b["input"], "output": b["output"]})
        _, re0 = call("kernel.record_call", {"session": "ctlV", "actor": "sweep", "input": b["input"], "output": b["output"]})
        w("== 非 Measure 那批格 · 运行时逐格撞 · %s ｜ 字节 sha256=%s =="
          % (time.strftime("%Y-%m-%d %H:%M:%S"),
             __import__("hashlib").sha256(io.open(exe, "rb").read()).hexdigest()[:16]))
        w("底样相关几格原值：ToolCalls=%s Malformed=%s MalformedStreak=%s Notice=%s WireFormat=%r WireFormatSaid=%s Says=%s"
          % (b["output"]["ToolCalls"], b["output"]["Malformed"], b["output"]["MalformedStreak"],
             b["input"]["Notice"], b["output"]["WireFormat"], b["output"]["WireFormatSaid"], b["output"]["Says"]))
        w("控制臂（什么都没改）：check=%s record=%s —— 收不下就整批作废" % (ce, re0))
        if re0:
            return 5
        w("")
        w("臂 ｜ 这一格键名 ｜ 期望 ｜ check ｜ record ｜ 同判 ｜ 拒收原话（有没有指名这一格）")
        w("-" * 78)
        key_named = 0
        accepted = []
        same_n = 0
        for name, path, val, want, why in ARMS:
            pack = json.loads(json.dumps(AP.legal_call("值域臂这一发的真 stdout")))
            for (p2, v2) in NEED.get(name, []):
                setcell(pack, p2, v2)
            setcell(pack, path, val)
            mykey = rows[(path[0], ".".join(path[1:]))]
            c_txt, c_err = call("kernel.check_call", {"session": "V" + name[:2], "actor": "sweep",
                                                     "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": "V" + name[:2], "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
            same = (c_err == r_err)
            same_n += same
            txt = (r_txt or c_txt or "")
            named = mykey in txt
            key_named += named
            if not r_err:
                accepted.append(name)
            w("%-22s ｜ %-20s ｜ %-14s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %s"
              % (name, mykey, want, c_err, r_err, "同判" if same else "**分歧**",
                 txt.replace("\n", " / ")[:110] if r_err else "**收下了**"))
            w("    └ 期望的出处：%s ｜ 这一臂把 %s 改成了 %r"
              % (why, ".".join(path[1:]), val))
        w("")
        w("== 自印分母 ==")
        w("臂 %d 条 ｜ 落账被拒 %d 条 ｜ 被收下 %d 条：%s ｜ 预检与落账同判 %d/%d ｜ 拒收原话里指名了那一格（键名由格表现取）%d 条"
          % (len(ARMS), len(ARMS) - len(accepted), len(accepted), " ".join(accepted) or "无", same_n, len(ARMS), key_named))
        w("读法：`N6 内容字节给负数` 的期望本来就是「无依据，只记录」—— 它收下/拒都不算契约被违反；")
        w("      其余各臂若出现「期望该拒、实际收下」，那是**判据与说法对不上**（契约/代码注释写了那条却没接上），")
        w("      那才是要立缺陷的情形；若全部按期望拒了，则 #56 对这批格的怀疑不成立。")
        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
