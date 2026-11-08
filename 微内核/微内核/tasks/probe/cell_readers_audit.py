# -*- coding: utf-8 -*-
"""每格有几个读者 —— 拿内核自报的 49 格当分母，数每一格的 Go 字段在源码里被读了几次。

为什么要这一支：#42「未堵那半」一直说的是「非 Measure 那 38 格没有出处档」，
但**没有出处档 ≠ 没有读者**：有的格虽然不能标"没量到"，它的值却被一条判据念到过（如 `Notice.Sent` 要和正文对质）。
这两件事混在一句「38 格不归这条」里，就把"内核到底管得到几格"说虚了。
⇒ 量出来才有资格说：49 格里，除了登记那一次之外**一个读者都没有**的有几格。

分母**由内核自印**（不是我 grep 出来的）：起一个实例调 `kernel.celltable`，
把 `input call ← Call` 那 49 行抠出来当名单 —— 这样新增一格忘了登记，这里当场少一行。

判据读数怎么算：每一格的 Field 路径取**末两段**（`Budget.CallBytesCeiling` → `CallBytesCeiling` 不够，留 `Budget.CallBytesCeiling` 也可选），
在**非测试** Go 文件里数它出现的次数，再**减掉登记那一处**（`cell("key","Field"` 那行）。
剩下 ≥1 才算"有读者"。

这条尺子不证明什么（写在读数里，别偷偷放大）：
  · 它数的是**文本出现**，不是"被读进判据" —— 出现在 `Errorf` 的格式参数里（只是印出来给人看）也算读者，
    所以"有读者 ≥1" **不等于**"这一格被某条闸核过"；反过来"0 读者"是硬的：那格除了登记没人碰。
  · 同名段可能撞（不同 struct 的同名字段），撞了会**高报**读者数 —— 所以 0 才是可信的那一端。
跑法：python tasks/probe/cell_readers_audit.py <mcphttp.exe> [输出.txt]
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
GO_FILES = ["cells.go", "input.go", "output.go", "record.go", "journal.go", "kernel.go", "measure.go",
            os.path.join("mcpsrv", "mcpsrv.go"), os.path.join("cmd", "mcphttp", "main.go")]


def call(url, tool, args):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": tool, "arguments": args}}).encode("utf-8")
    r = json.loads(urllib.request.urlopen(urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}), timeout=60).read().decode("utf-8"))
    res = r.get("result") or {}
    return "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "每格读者审计-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    port = 8803
    url = "http://127.0.0.1:%d/mcp" % port
    proc = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    try:
        time.sleep(1.2)
        ct = call(url, "kernel.celltable", {})
        if "唯一键名" not in ct:
            w("NOT_RUN：celltable 没回话或形状不对，这一轮一个数都不许读。")
            return 4
        w("== 每格读者审计 · 分母 = 内核自报 ==")
        w("内核抬头：" + ct.split("\n")[0][:120])
        cells = []
        for line in ct.split("\n")[1:]:
            m = re.match(r'^\s*(input|output)\s+(\S+)\s+←\s+(\S+)', line)
            if m:
                cells.append((m.group(1), m.group(2), m.group(3)))
        w("从那份回话里抠到的行 = %d（抬头说 49，两个数要一致才算数）" % len(cells))
        if len(cells) != 49:
            w("⇒ 抠出的行数与内核自报不符，是**我这支的解析错了**，不报任何结论。")
            return 5

        srcs = {}
        for f in GO_FILES:
            p = os.path.join(ROOT, f)
            if os.path.exists(p):
                srcs[f] = io.open(p, encoding="utf-8", errors="replace").read()
        w("读的 Go 文件 %d 个（非测试）：%s" % (len(srcs), " ".join(sorted(srcs))))
        w("")
        w("侧 ｜ 键名 ｜ Field ｜ 登记以外的读者处数 ｜ 出现在哪几个文件")
        w("-" * 78)
        zero, low = [], []
        for side, key, field in cells:
            raw = field.split("#")[-1].split(".")[-1]
            # 方法型 Field（`Sent()`、`Says.Clipped()`）末段带括号：`\b` 在 `)` 后面永远不成立，
            # 所以带括号时必须用「不带尾边界」的写法 —— 我第一版就是栽在这里，把 3 个有读者的格报成了 0 读者。
            is_method = raw.endswith("()")
            seg = raw[:-2] if is_method else raw
            pat = r'\b' + re.escape(seg) + (r'\(\)' if is_method else r'\b')
            n = 0
            files = set()
            for f, txt in srcs.items():
                for m in re.finditer(pat, txt):
                    line_start = txt.rfind("\n", 0, m.start()) + 1
                    line = txt[line_start:txt.find("\n", m.start())]
                    if re.search(r'(cell|measureCell|relayCell)\(\s*"', line):
                        continue                              # 登记那一处不算读者
                    n += 1
                    files.add(f)
            if n == 0:
                zero.append((side, key, field))
            elif n <= 2:
                low.append((side, key, field, n))
            w("%-6s ｜ %-22s ｜ %-26s ｜ %d ｜ %s" % (side, key, field, n, " ".join(sorted(files)) or "—"))
        w("")
        w("== 自印分母 ==")
        w("内核自报 49 格 ｜ 抠到 %d ｜ **登记以外 0 读者**的 = %d 格 ｜ 读者 ≤2 处的 = %d 格"
          % (len(cells), len(zero), len(low)))
        # 自核（这根针钉的是**我这支尺子自己**）：三格今天已知有读者，若这里数出 0，说明正则是坏的，
        # 那份"0 读者名单"就整体不可信 —— 第一版正是这么错的（`\b` 接在 `()` 后面永不成立，把 3 格有读者的报成 0）。
        controls = {"sent": "Notice.Sent（正文与发没发要对质，output/input 里都有它）",
                    "streaming": "Streaming（流式与否决定分帧那一格该不该有数）",
                    "tokens": "Tokens（token 那一格的口径白名单）"}
        cnt = {}
        for side, key, field in cells:
            raw = field.split("#")[-1].split(".")[-1]
            is_method = raw.endswith("()")
            seg = raw[:-2] if is_method else raw
            pat = r'\b' + re.escape(seg) + (r'\(\)' if is_method else r'\b')
            tot = 0
            for f, txt in srcs.items():
                for m in re.finditer(pat, txt):
                    ls = txt.rfind("\n", 0, m.start()) + 1
                    if re.search(r'(cell|measureCell|relayCell)\(\s*"', txt[ls:txt.find("\n", m.start())]):
                        continue
                    tot += 1
            cnt[key] = tot
        bad_ctrl = [k for k in controls if cnt.get(k, -1) <= 0]
        w("自核（钉尺子自己）：正对照 %s → %s ｜ 坏 = %s"
          % (" ".join(sorted(controls)), " ".join("%s=%d" % (k, cnt.get(k, -1)) for k in sorted(controls)),
             "无" if not bad_ctrl else " ".join(bad_ctrl)))
        if bad_ctrl:
            w("⇒ 正则坏了，上面那份 0 读者名单**整体不可信**，这一支退出码 6。")
        if zero:
            w("0 读者这些格（这一端是硬的：除了登记那一次，非测试 Go 源码里没有一处提到这个字段）：")
            for side, key, field in zero:
                w("   %-6s %-22s ← %s" % (side, key, field))
        w("不许这么读：'有读者 ≥1' ≠ '这一格被某条闸核过' —— 出现在 Errorf 的格式参数里（只是印给人看）也算读者；")
        w("            同名段会撞（不同 struct 同名字段），撞了只会**高报**读者数，所以可信的那一端是 0。")
        w("和 #42 的关系：#42 说非 Measure 那族写不出「没量到」的结构，这条不改那个结论；")
        w("            这条补的是另一半 —— 那 38 格里，哪些值其实**有判据在念**，哪些念都没人念。")
        return 6 if bad_ctrl else 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
