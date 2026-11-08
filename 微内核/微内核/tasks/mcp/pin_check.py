# -*- coding: utf-8 -*-
# pin_check.py —— 验"将要被挂上去的那一颗二进制"是不是当前那颗内核，而不是上一次留下的。
#
# 为什么要有它：会话里挂的是 `.bin\mcpkernel.exe` 这个**路径**，而路径不会自己说明它是哪天编的。
# 10-15 那天配置里的 exe 是 10-13 03:31 编的，比封板后的内核旧两天 —— 挂上去就用
# 一套过期判据记账，而所有读数看着都正常。`tasks/mcp/client.py` 每次自己重编到 %TEMP%，
# 正好盖不住这个洞：它验的是"我刚编的那颗"，不是"配置指的那颗"。
#
# 用法：py -3 tasks/mcp/pin_check.py <exe 完整路径>
import json
import os
import subprocess
import sys
import time

EXE = sys.argv[1] if len(sys.argv) > 1 else r"C:\Users\Admin\Desktop\微内核\微内核\.bin\mcpkernel.exe"
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, "报告", "挂载前-pin_check.txt")
p = subprocess.Popen([EXE], stdin=subprocess.PIPE, stdout=subprocess.PIPE, bufsize=0, cwd=ROOT)


def rpc(method, params=None, notif=False):
    msg = {"jsonrpc": "2.0", "method": method}
    if not notif:
        msg["id"] = rpc.i
        rpc.i += 1
    if params is not None:
        msg["params"] = params
    p.stdin.write((json.dumps(msg, ensure_ascii=False) + "\n").encode("utf-8"))
    p.stdin.flush()
    if notif:
        return None
    line = p.stdout.readline()
    if not line:
        raise SystemExit("SERVER DIED at %s" % method)
    return json.loads(line.decode("utf-8"))


rpc.i = 1
init = rpc("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                          "clientInfo": {"name": "pin_check", "version": "1"}})
rpc("notifications/initialized", notif=True)
name = init["result"]["serverInfo"]["name"]
ver = init["result"]["serverInfo"].get("version", "?")
tools = rpc("tools/list")["result"]["tools"]
print("PIN server=%s version=%s tools=%d" % (name, ver, len(tools)))

ct = rpc("tools/call", {"name": "kernel.celltable", "arguments": {}})
txt = ct["result"]["content"][0]["text"]
first = txt.splitlines()[0] if txt else "<空>"
print("PIN celltable first line (utf-8 in file, ascii here): len=%d head=%r" % (len(txt), first[:60]))

# 分母必须自己数出来，不许抄：格位表里每个键都要有 side 与 basis，缺一个就是编了一颗半截内核。
rows = [ln for ln in txt.splitlines() if ln.strip() and ("input" in ln or "output" in ln)]
print("PIN rows_with_side=%d" % len(rows))
bad = rpc("tools/call", {"name": "kernel.check_call", "arguments": {"session": "pin", "call": 1}})
print("PIN check_call_isError=%s" % bool(bad["result"].get("isError")))
out = []
for r in (init, {"tools": len(tools)}, {"celltable": txt}, {"check_call": bad}):
    out.append(json.dumps(r, ensure_ascii=False)[:400])
open(OUT, "w", encoding="utf-8").write("\n".join(out))
p.stdin.close()
p.wait(timeout=5)
print("PIN exited=%s" % p.returncode)
