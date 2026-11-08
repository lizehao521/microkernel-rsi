# -*- coding: utf-8 -*-
"""ledger_sweep.py —— 把 报告/ 里所有归档账本逐一过 `kernel.load_journal` + `kernel.verify`。

为什么要跑：10-17 动了解码这一步（#46 不许未知键）与 tally 话术（#44/#49）。
判据表没动，但"历史落下来的账还能不能被今天这颗内核读回来"是另一件事——
读不回来就是**向后兼容断了**，而这条今天没有任何东西在守。

它不判定"该不该读回来"，只把每本的形状摆出来：条数、载入是否被拒、verify 的三态是哪一态。
跑法（自己起一个实例，收进程按监听 owner 收）：
    python tasks/probe/ledger_sweep.py <mcphttp.exe>
"""
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
EXE = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
PORT = 8790
URL = "http://127.0.0.1:%d/mcp" % PORT
OUT = os.path.join(ROOT, "报告", "归档账本载入扫查-1017.txt")
lines = []


def w(s):
    lines.append(s)
    sys.stdout.buffer.write(s.encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
    sys.stdout.flush()


def call(name, args):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": name, "arguments": args}}).encode("utf-8")
    r = json.loads(urllib.request.urlopen(
        urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json"}),
        timeout=120).read().decode("utf-8"))
    res = r.get("result", {})
    return res.get("content", [{}])[0].get("text", ""), bool(res.get("isError"))


def main():
    global OUT
    pos = [a for a in sys.argv[1:] if not a.startswith("--")]
    if len(pos) > 1:
        OUT = pos[1] if os.path.isabs(pos[1]) else os.path.join(ROOT, pos[1])
    # 扫查每跑一次就要留一份：`报告/` 里的读数都是凭据，同名重写等于把上一轮的数销毁
    # （10-17 我在别的脚本上犯过两次，这次把规矩写进脚本自己）。要覆盖得显式给 --force。
    if os.path.exists(OUT) and "--force" not in sys.argv:
        print("REFUSE_TO_OVERWRITE: %s 已存在。这一轮请给第二个参数指一个新文件名（要覆盖再加 --force）。"
              % OUT.encode("unicode_escape", "backslashreplace").decode("ascii"))
        return 3
    files = sorted(f for f in os.listdir(os.path.join(ROOT, "报告")) if f.endswith(".jsonl"))
    if not os.path.exists(EXE):
        w("NO_BINARY：%s（先 go build -o ../.bin/mcphttp.exe ./cmd/mcphttp）" % EXE)
        return 3
    srv = subprocess.Popen([EXE, "-listen", "127.0.0.1:%d" % PORT], cwd=ROOT,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    ok = refused = undecidable = empty = broken = unknown = skipped = 0
    try:
        for _ in range(25):
            try:
                call("kernel.celltable", {}); break
            except Exception:
                time.sleep(1)
        w("== 归档账本载入扫查 · %s · 用的字节 %s" % (time.strftime("%F %T"), os.path.basename(EXE)))
        w("每本走 load_journal(acknowledge=replace)；只有载入被收的那本才接着 verify。")
        w("为什么不能无条件 verify：载入被拒时内核那本账**保持上一本的**（mcpsrv.go 里 err 分支直接 return，"
          "不换 s.ledger）—— 若照抄一句 verify，报的是上一本的链，却标在这一本名下，归因就错了。")
        w("verify 的『—』有两种，分开数：『— 尚无账可核（共 0 条）』是空账，"
          "『— 字段级自洽…核不了：N 条没有原像』才是缺原像那一种（journal.go 的 String 分两支）。")
        w("")
        for f in files:
            p = os.path.join(ROOT, "报告", f)
            text = open(p, encoding="utf-8", errors="replace").read()
            if not text.strip():
                skipped += 1
                w("%-34s 空文件 —— 跳过（0 字节不是账）" % f)
                continue
            if not text.endswith("\n"):
                text += "\n"
            n = len([l for l in text.splitlines() if l.strip().startswith("{")])
            t1, e1 = call("kernel.load_journal", {"text": text, "acknowledge": "replace"})
            if e1:
                refused += 1
                w("%-34s 条数=%-4d 载入=拒   verify=（没做，这本没换上）" % (f, n))
                w("      拒因：%s" % t1.splitlines()[0][:170])
                continue
            t2, _ = call("kernel.verify", {})
            head = t2.splitlines()[0] if t2 else ""
            if head.startswith("\u2713"):
                state = "过（比过原像的链自洽）"; ok += 1
            elif head.startswith("\u2717"):
                state = "不符（红）"; broken += 1
            elif head.startswith("\u2014 \u5c1a\u65e0\u8d26\u53ef\u6838"):
                state = "空（共 0 条，别读成通过）"; empty += 1
            elif head.startswith("\u2014 \u5b57\u6bb5\u7ea7\u81ea\u6d3d"):
                state = "核不了（缺原像）"; undecidable += 1
            else:
                state = "读不懂这句分类（不猜）"; unknown += 1
            w("%-34s 条数=%-4d 载入=收   verify=%s" % (f, n, head[:96]))
            w("      这一本归到：%s" % state)
        w("")
        w("== 自印分母 ==")
        w("报告/ 里 .jsonl 共 %d 本；空文件跳过 %d；载入被拒 %d；载入成功 %d"
          % (len(files), skipped, refused, ok + undecidable + empty + broken + unknown))
        w("载入成功里：链自洽 %d、核不了（缺原像）%d、空账 %d、不符（红）%d、读不懂 %d"
          % (ok, undecidable, empty, broken, unknown))
        w("两个不许塌成一格：『载入被拒』是这颗内核读不动这份历史（向后兼容断了）；"
          "『核不了』是账本身在手但原像不在，那是缺口 5 的形状，不是兼容问题。")
        w("这一条不判「该不该读回来」：它只回答「今天这颗内核还读不读得动历史上落下来的账」。")
        return 0
    finally:
        srv.terminate()
        try:
            srv.wait(timeout=10)
        except Exception:
            srv.kill()
        import io
        io.open(OUT, "w", encoding="utf-8").write("\n".join(lines) + "\n")
        print("REPORT=" + OUT.encode("unicode_escape").decode("ascii"))


if __name__ == "__main__":
    sys.exit(main())
