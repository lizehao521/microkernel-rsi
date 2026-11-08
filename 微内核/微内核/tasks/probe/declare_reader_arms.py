# -*- coding: utf-8 -*-
"""`kernel.declare_reader` 被拒的那几发，进没进任何一个计数 —— 打前提①（#44）的靶。

读码（本轮，`cells.go:408-429`）：这一共有 **3 条拒收路径**，每条都带一句人话理由：
  · 空 —— 「声明里键名与读者都不能空」
  · 不在键名表 —— 「拼错的声明会让一格死格看起来是活的」
  · 同一读者声明两遍 —— 「同一个读者声明两遍会把分母数大」
而 #44 立的是「门挡下的拒收必须进某个计数，否则三层合不出分母」：今天四个桶分别是
  内核自己拒的（`Ledger.Rejected()`，单位=发）、门口挡的（`doorRefused`，单位=一次写请求）、
  协议层判无效的（`badRequests`，单位=一条请求）、连协议层都没到的（`transportRefused`，单位=一条请求）。
`declare_reader` **既不是写请求、也不走 RecordCall** —— 所以它被拒的时候，四个桶该都不动。
这一支就是把这句"该都不动"**打成读数**：要么它进了某个桶（那桶的单位就破了），
要么它哪里都不进（那就是第五类拒收，今天没有数）。

跑法：python tasks/probe/declare_reader_arms.py <mcphttp.exe> [输出.txt]
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

ARMS = [
    ("D0 对照·合法声明", {"reader": "arm-reader", "key": "chunks", "side": "output"}, "该收"),
    ("D1 键名拼错", {"reader": "arm-reader", "key": "tokenz", "side": "input"}, "该拒：不在键名表"),
    ("D2 同一读者声明两遍", {"reader": "arm-reader", "key": "chunks", "side": "output"}, "该拒：重复"),
    ("D3 读者为空", {"reader": "  ", "key": "tokens", "side": "input"}, "该拒：空"),
    ("D4 键名对但侧错", {"reader": "arm-reader2", "key": "tokens", "side": "output"}, "该拒：output 侧没有 tokens"),
]


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "声明被拒-进没进计数-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    port = 8805
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
        if "error" in r and "result" not in r:
            return "协议层错误：%s" % json.dumps(r["error"], ensure_ascii=False), True
        res = r.get("result") or {}
        txt = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
        return txt, bool(res.get("isError"))

    def buckets(rep):
        """从 tally 那句里把四个数各抠出来（顺序：内核拒的／门口挡的／协议层无效的／连协议层都没到的）。"""
        nums = []
        m = re.search(r'被拒的写入 (\d+) 次', rep)
        nums.append(int(m.group(1)) if m else -1)
        m = re.search(r'另有 (\d+) 次写请求根本没走到内核', rep)
        nums.append(int(m.group(1)) if m else -1)
        m = re.search(r'(\d+) 条请求被协议层判成无效', rep)
        nums.append(int(m.group(1)) if m else -1)
        m = re.search(r'(\d+) 条请求连协议层都没到', rep)
        nums.append(int(m.group(1)) if m else -1)
        return nums

    try:
        time.sleep(1.2)
        head, _ = call("kernel.celltable", {})
        if "唯一键名" not in head:
            w("NOT_RUN：端点没答，一个数都不许读。")
            return 4
        b0_txt, _ = call("kernel.tally", {})
        b0 = buckets(b0_txt)
        w("== `kernel.declare_reader` 的 5 发臂 · %s ｜ 字节 sha256=%s =="
          % (time.strftime("%Y-%m-%d %H:%M:%S"),
             __import__("hashlib").sha256(io.open(exe, "rb").read()).hexdigest()[:16]))
        w("起手四桶（内核拒的, 门口挡的, 协议层无效, 连协议层都没到）= %s" % b0)
        w("  起手的 tally 原话：%s" % b0_txt.replace("\n", " / ")[:200])
        w("")
        w("臂 ｜ 期望 ｜ isError ｜ 回话")
        w("-" * 78)
        refused = 0
        for name, args, want in ARMS:
            t, e = call("kernel.declare_reader", args)
            if e:
                refused += 1
            w("%-22s ｜ %-22s ｜ %-6s ｜ %s" % (name, want, e, t.replace("\n", " / ")[:150]))
        b1_txt, _ = call("kernel.tally", {})
        b1 = buckets(b1_txt)
        w("")
        w("五发之后四桶 = %s ｜ 差值 = %s" % (b1, [y - x for x, y in zip(b0, b1)]))
        w("")
        w("== 自印分母 ==")
        w("臂 %d 条 ｜ 其中 isError=True 的 %d 条 ｜ 四桶逐桶差值（内核/门口/协议层/传输层）= %s"
          % (len(ARMS), refused, [y - x for x, y in zip(b0, b1)]))
        moved = [i for i in range(4) if b1[i] != b0[i]]
        if not moved:
            w("⇒ 读法（这一条是给前提①的）：**拒了 4 发，四个桶一个都没动** —— "
              "`declare_reader` 的拒既不是写请求也不走 RecordCall，所以它**今天不进任何一个计数**。")
            w("   这不是『没拒』（isError 与那四句原话都在上面，机器读得到），也不是『桶坏了』；")
            w("   它是**第五类拒收**：认领类。要不要给它一个数、还是明说它不该有数，是 #44 那一族的口径问题，等表态。")
        else:
            w("⇒ 有桶动了（第 %s 个）—— 那就得核它进的那个桶**单位对不对**（发？一次写请求？一条请求？）。" % moved)
        w("它不证明：不证明这四句理由足够好读（是否点名是哪一格的认领，见 #48 那一族：这里 `Declare` 的消息**自带键名**，"
          "比如『%s 已经被 … 声明读过一次』—— 这一族今天是带键名的，别和 #48 混成一条）。" % "chunks")
        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
