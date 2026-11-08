# -*- coding: utf-8 -*-
"""standin_transcript.py —— 给 §唯一验收的判定脚本喂一份**真跑出来的** transcript（替身）。

它不是"不是我写的调用方"，它是本项目的 agent 写的脚本：所以 `acceptance_probe.py` 拿它跑出来的
段 3 必须是 **FAIL（BLOCKLIST 命中）**，而不是绿。这一支存在的意义就是把那条边界**跑成读数**：
替身能把闸门推动、能让 ①③ 的形状成立，但**不许**被读成验收通过。

跑法：
    python tasks/probe/standin_transcript.py <mcphttp.exe> [输出 transcript.jsonl]
"""
import json
import os
import subprocess
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import attracta_probe as AP  # noqa: E402  合法的 49 格底样只有一份

PORT = 8792
URL = "http://127.0.0.1:%d/mcp" % PORT


def call(url, name, args):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": name, "arguments": args}}).encode("utf-8")
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    r = json.loads(urllib.request.urlopen(req, timeout=60).read().decode("utf-8"))
    res = r.get("result", {})
    return res.get("content", [{}])[0].get("text", ""), bool(res.get("isError"))


def main():
    exe = sys.argv[1]
    dst = sys.argv[2] if len(sys.argv) > 2 else os.path.join(os.environ.get("TEMP", "."), "standin-transcript.jsonl")
    srv = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % PORT], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    rows = []

    def rec(tool, args):
        t0 = time.strftime("%Y-%m-%d %H:%M:%S")
        txt, err = call(URL, tool, args)
        rows.append({"t": t0, "tool": tool, "isError": err, "text": txt})

    try:
        for _ in range(25):
            try:
                call(URL, "kernel.celltable", {}); break
            except Exception:
                time.sleep(1)
        rec("kernel.celltable", {})
        base = AP.legal_call("2318")
        lie = json.loads(json.dumps(base))
        lie["session"] = "standin-1"
        lie["input"]["Notice"]["Sent"] = True          # 谎：声称通知发了却没正文
        rec("kernel.record_call", lie)
        lie2 = json.loads(json.dumps(base))
        lie2["session"] = "standin-2"
        lie2["output"]["UsageIn"]["Basis"] = 1          # 谎：把转述标成实测
        rec("kernel.check_call", lie2)
        rec("kernel.check_call", lie2)                  # 上面那条谎的预检形状（同参数再问一次）
        good = json.loads(json.dumps(base)); good["session"] = "standin-0"
        rec("kernel.record_call", good)
        rec("kernel.tally", {})
        rec("kernel.verify", {})
        with open(dst, "w", encoding="utf-8") as f:
            for r in rows:
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
        print("TRANSCRIPT=%s rows=%d refused=%d" % (
            dst.encode("unicode_escape").decode(), len(rows), sum(1 for r in rows if r["isError"])))
        return 0
    finally:
        srv.terminate()
        try:
            srv.wait(timeout=10)
        except Exception:
            srv.kill()


if __name__ == "__main__":
    sys.exit(main())
