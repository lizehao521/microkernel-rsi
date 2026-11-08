# -*- coding: utf-8 -*-
"""读者声明活不活得过一次重建 —— 打 `kernel.declare_reader` 那句「这一格从此不再是死格」的靶。

读码先立怀疑（三处，都是本轮现场读的）：
  · `cells.go:400-404`：`Readers` 就是一个 `map[string][]string`，没有落盘；
  · `journal.go` 全文 `grep -i reader` = **0 命中** ⇒ 读者声明**不进 JSONL、不进链、没有指纹**；
  · `mcpsrv.go:418` 成功回话却写着 `✓ … 声明读 … （这一格从此不再是死格）` —— 「从此」这两个字是要能被红的。
所以这一支跑四步：声明 → 报"有人读" → `kernel.load_journal`（工具自己说这「相当于进程重启了一次」）→ 再报一次。
中间用 `kernel.verify` 钉住账没变，免得把"链坏了"混进来当结论。

跑法：python tasks/probe/readers_persist_probe.py <mcphttp.exe> <一本归档账本.jsonl> [输出.txt]
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


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    ledger = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "样例-真流量流式-1017.jsonl")
    outp = sys.argv[3] if len(sys.argv) > 3 else os.path.join(ROOT, "报告", "读者声明-活过一次重建-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    if not os.path.exists(ledger):
        print("NOT_RUN：账本不在（%s）" % ledger.encode("unicode_escape"))
        return 4
    port = 8804
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
            url, data=body, headers={"Content-Type": "application/json"}), timeout=90).read().decode("utf-8"))
        res = r.get("result") or {}
        txt = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
        if not txt:
            err = r.get("error") or {}
            txt = "协议层错误：%s" % json.dumps(err, ensure_ascii=False)
        return txt, bool(res.get("isError"))

    try:
        time.sleep(1.2)
        head, _ = call("kernel.celltable", {})
        if "唯一键名" not in head:
            w("NOT_RUN：端点没答，一个数都不许读。")
            return 5
        w("== 读者声明能不能活过一次重建 · %s ｜ 字节 sha256=%s ｜ 账本=%s =="
          % (time.strftime("%Y-%m-%d %H:%M:%S"),
             __import__("hashlib").sha256(io.open(exe, "rb").read()).hexdigest()[:16],
             os.path.basename(ledger)))
        w("")
        KEYS = [("output", "cost_countable"), ("output", "chunks"), ("input", "tokens")]

        def undeclared(rep):
            """从 report 那句「其中 N 格没有任何声明者读」里抠出 N（两侧各一个）。"""
            return [int(m) for m in re.findall(r'其中 (\d+) 格没有任何声明者读', rep)]

        b0, _ = call("kernel.readers_report", {})
        base_cnt = undeclared(b0)
        w("声明之前 readers_report 的「没有任何声明者读」= %s（输入侧, 输出侧）" % base_cnt)
        declared = 0
        for side, k in KEYS:
            t, e = call("kernel.declare_reader", {"reader": "probe--reader", "key": k, "side": side})
            w("声明 %s 侧的 %-16s → isError=%s ｜ %s" % (side, k, e, t.replace("\n", " / ")[:120]))
            declared += 0 if e else 1
        before, _ = call("kernel.readers_report", {})
        w("")
        w("── 重建之前 readers_report ──")
        for line in before.split("\n"):
            w("   " + line[:170])

        txt = io.open(ledger, encoding="utf-8").read()
        lt, le = call("kernel.load_journal", {"text": txt})
        w("")
        w("── kernel.load_journal（工具自己说这相当于进程重启一次）──")
        w("   isError=%s ｜ %s" % (le, lt.replace("\n", " / ")[:170]))
        vt, ve = call("kernel.verify", {})
        w("   链还是那条链吗：isError=%s ｜ %s" % (ve, vt.replace("\n", " / ")[:140]))
        after, _ = call("kernel.readers_report", {})
        w("── 重建之后 readers_report ──")
        for line in after.split("\n"):
            w("   " + line[:170])

        probed = [k for _, k in KEYS]
        hits_before = sum(1 for k in probed if k in before)
        hits_after = sum(1 for k in probed if k in after)
        b_cnt = undeclared(before)
        a_cnt = undeclared(after)
        w("")
        w("== 自印分母 ==")
        w("声明成功 %d 条（探针试了 3 格：%s）" % (declared, " ".join(probed)))
        w("没有任何声明者读的格数（输入侧, 输出侧）：声明之前=%s ｜ 3 条声明之后=%s ｜ 顶账之后=%s"
          % (base_cnt, b_cnt, a_cnt))
        w("顶账这一步动了账吗：isError=%s，链=%s（上面 verify 那一行）" % (le, "自洽" if not ve else "不符"))
        w("⇒ 读清楚这一条的方向：**声明不进账（`journal.go` 全文无 reader），但顶账也不清它**。")
        w("   于是同一进程里可以出现「账已经是别人的 98 条历史账，而『这一格有人读过』还挂着探针那一刻的声明」——")
        w("   声明与它所描述的账**不同源、无指纹绑定**，`readers_report` 报的『有人读过』没法被账本核。")
        # 第三臂：换一个进程（真正的重启）—— 声明是不是跟着没了
        proc.terminate()
        proc = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(1.2)
        lt2, le2 = call("kernel.load_journal", {"text": txt})
        fresh, fe = call("kernel.readers_report", {})
        f_cnt = undeclared(fresh)
        w("")
        w("── 第三臂：换一个进程再顶同一本账（工具说的『相当于进程重启』的真实版本）──")
        w("   load_journal isError=%s ｜ readers_report 没有任何声明者读：%s（对照顶账后那一栏 %s）"
          % (le2, f_cnt, a_cnt))
        w("   ⇒ 如果这一栏回到 (20, 29)，那 `mcpsrv.go:418` 那句「这一格**从此**不再是死格」的『从此』")
        w("     = 到**本进程结束**为止；而它旁边那本账是可以带走的 —— 两者寿命不一样长。")

        return 0
    finally:
        proc.terminate()
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
