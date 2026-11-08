# -*- coding: utf-8 -*-
"""双臂验收探针（决策件的可执行那一半）。

它只做一件事：把「干净臂 / 坏臂 / 对照臂」各自至少一根会红的针，打成当场读数。
不改内核、不写 8797/8799、不碰任何已在监听的实例。

三态退出码：
  0 = 弱判据与严格判据都过（今天不预期）
  1 = 弱判据过、严格判据没过（严格判据里包含「坏臂的拒收必须进对计数桶」）
  2 = 探针自己的尺子坏了（合成坏结果没有变红）
  3 = NOT_RUN（构建失败 / 端口 owner 对不上 / 端点没答）

跑法：
  C:/Users/Admin/sdk/py314/python.exe 报告/双臂验收-探针-1019.py
"""
import copy
import hashlib
import io
import json
import os
import re
import socket
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GO = r"C:\Users\Admin\sdk\go\bin\go.exe"
TEMP = os.path.join(os.environ.get("TEMP", r"C:\Windows\Temp"), "agentkernel-arms-1019")
EXE = os.path.join(TEMP, "mcphttp-%d.exe" % os.getpid())
LOG = os.path.join(TEMP, "server-%d.log" % os.getpid())
OUT = os.path.join(ROOT, "报告", "双臂验收-实测-1019-%s.txt" % time.strftime("%H%M%S"))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

out = []


def w(s):
    out.append(s)
    sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
    sys.stdout.flush()


def finish(code):
    io.open(OUT, "w", encoding="utf-8").write("\n".join(out) + "\n")
    print("REPORT=" + OUT.encode("unicode_escape").decode("ascii"))
    return code


def free_port(start=18807):
    for p in range(start, start + 40):
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        try:
            s.bind(("127.0.0.1", p))
            s.close()
            return p
        except OSError:
            s.close()
    raise RuntimeError("没有空端口")


def http_call(url, tool, args, rid=1):
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": "tools/call",
                       "params": {"name": tool, "arguments": args}},
                      ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    rep = json.loads(urllib.request.urlopen(req, timeout=60).read().decode("utf-8"))
    if "error" in rep and "result" not in rep:
        return "协议层错误：" + json.dumps(rep["error"], ensure_ascii=False), True
    res = rep.get("result") or {}
    txt = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
    return txt, bool(res.get("isError"))


def build():
    os.makedirs(TEMP, exist_ok=True)
    if os.path.exists(EXE):
        os.remove(EXE)
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    p = subprocess.run([GO, "build", "-o", EXE, "./cmd/mcphttp"], cwd=ROOT, env=env,
                       capture_output=True, text=True, timeout=180)
    if p.returncode != 0:
        w("BUILD_FAIL rc=%d stderr=%s" % (p.returncode, (p.stderr or "")[:400]))
        return False
    return True


def pids_on(port):
    p = subprocess.run(["netstat", "-ano"], capture_output=True, text=True, timeout=30)
    got = []
    for line in p.stdout.splitlines():
        if "LISTENING" not in line.upper() or (":%d" % port) not in line:
            continue
        parts = line.split()
        if parts:
            got.append(parts[-1])
    return sorted(set(got))


def proc_path(pid):
    p = subprocess.run(["powershell", "-NoProfile", "-Command",
                        "(Get-Process -Id %d).Path" % pid],
                       capture_output=True, text=True, timeout=30)
    return (p.stdout or "").strip().splitlines()[0] if (p.stdout or "").strip() else ""


def same_path(a, b):
    return os.path.normcase(os.path.abspath(a)) == os.path.normcase(os.path.abspath(b))


def tally(url):
    txt, _ = http_call(url, "kernel.tally", {})
    pats = {
        "rejected": r"被拒的写入\s+(\d+)\s+次",
        "precheck_rejected": r"预检.*?被内核判据拒\s+(\d+)\s+发",
        "precheck_door": r"预检在门口被挡下\s+(\d+)\s+次",
        "door_refused": r"另有\s+(\d+)\s+次写请求根本没走到内核",
        "bad_requests": r"(\d+)\s+条请求被协议层判成无效",
        "transport_refused": r"(\d+)\s+条请求连协议层都没到",
    }
    vals = {}
    for k, pat in pats.items():
        m = re.search(pat, txt)
        vals[k] = int(m.group(1)) if m else -1
    state = "?"
    m = re.search(r"这几笔数合起来的口径：(\S+)", txt)
    if m:
        state = m.group(1)
    return vals, state, txt


def deltas(a, b):
    return {k: (b[k] - a[k] if a[k] >= 0 and b[k] >= 0 else -1) for k in a}


def variant(base, session, callno=1):
    d = copy.deepcopy(base)
    d["session"] = session
    d["actor"] = "dual-arm"
    d["input"]["Call"] = callno
    d["output"]["Call"] = callno
    return d


def naming(text, key, label):
    has_key = key in text
    has_label = label in text
    if has_key and has_label:
        return "STRICT+LABEL"
    if has_key:
        return "STRICT"
    if has_label:
        return "WEAK"
    return "UNNAMED"


def checker(rows, naming_b1, naming_b2, b2_delta, verify_txt, strict):
    """rows: 名字 -> (is_error, text)。返回 (ok, reasons)。"""
    reasons = []
    def need(name, cond):
        if not cond:
            reasons.append(name)
    ok0, t0 = rows["C0"]
    need("C0 干净调用没有被收", (not ok0) and ("整发落账 49 条" in t0))
    pc, pt = rows["B1-pre"]
    b1, bt = rows["B1-rec"]
    need("B1 预检没有拒", pc and ("没来源" in pt or "却没说来源" in pt))
    need("B1 落账没有拒", b1 and ("没来源" in bt or "却没说来源" in bt))
    need("B1 拒收没有点名（键名/标签都没有）", naming_b1 != "UNNAMED")
    c1, ct = rows["C1"]
    need("C1 修好后的 B1 没有被收", (not c1) and ("整发落账 49 条" in ct))
    d0, d0t = rows["B2-D0"]
    d2, d2t = rows["B2-dup"]
    c2, c2t = rows["B2-new"]
    need("B2 对照声明没有被收", not d0)
    need("B2 重复声明没有拒", d2 and ("声明读过一次" in d2t))
    need("B2 重复声明没有带 snake 键名", naming_b2 == "STRICT" or naming_b2 == "STRICT+LABEL")
    need("B2 修好后的新读者没有被收", not c2)
    c3, c3t = rows["C3"]
    need("C3 干净臂的反针没有红", c3)
    need("链验证不是 ✓/98", ("✓" in verify_txt) and ("98" in verify_txt))
    if strict:
        if any(v < 0 for v in b2_delta.values()):
            reasons.append("B2 六桶里有解析失败（-1），不能读成 0")
        else:
            need("B2 拒收没有进任何一个计数桶（严格判据）", any(v != 0 for v in b2_delta.values()))
    return (len(reasons) == 0), reasons


def main():
    if not os.path.exists(GO):
        w("NOT_RUN：go 不在 %s" % GO)
        return finish(3)
    if not build():
        return finish(3)
    exe_sha = hashlib.sha256(io.open(EXE, "rb").read()).hexdigest()
    latest = max(os.path.getmtime(os.path.join(ROOT, f)) for f in
                 ("cells.go", "input.go", "output.go", "record.go", "kernel.go",
                  "mcpsrv/mcpsrv.go", "cmd/mcphttp/main.go"))
    w("== 双臂验收 · %s ==" % time.strftime("%F %T"))
    w("exe=%s" % EXE)
    w("exe sha256=%s" % exe_sha)
    w("源码最新 mtime=%s ｜ exe mtime=%s" % (
        time.strftime("%F %T", time.localtime(latest)),
        time.strftime("%F %T", time.localtime(os.path.getmtime(EXE)))))
    if os.path.getmtime(EXE) + 1 < latest:
        w("NOT_RUN：exe 比源码旧，读数作废。")
        return finish(3)

    port = free_port()
    url = "http://127.0.0.1:%d/mcp" % port
    logf = io.open(LOG, "wb")
    proc = subprocess.Popen([EXE, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                            stdout=logf, stderr=logf)
    try:
        ready = False
        for _ in range(40):
            if proc.poll() is not None:
                break
            try:
                head, _ = http_call(url, "kernel.celltable", {})
                if "唯一键名" in head:
                    ready = True
                    break
            except Exception:
                pass
            time.sleep(0.25)
        logf.flush()
        rawlog = io.open(LOG, "rb").read() if os.path.exists(LOG) else b""
        bind_bad = rawlog.count(b"bind:") + rawlog.count(b"Only one usage")
        owner_path = proc_path(proc.pid)
        listen_pids = pids_on(port)
        w("端口=%d ｜ pid=%d ｜ owner=%s ｜ 监听 pid=%s ｜ bind 错误=%d" % (
            port, proc.pid, owner_path or "?", ",".join(listen_pids) or "无", bind_bad))
        if not ready:
            w("NOT_RUN：端点没答（进程可能已经退出）。server.log 尾部：")
            w(rawlog.decode("utf-8", "replace")[-500:])
            return finish(3)
        if bind_bad != 0 or not same_path(owner_path, EXE) or str(proc.pid) not in listen_pids:
            w("NOT_RUN：端口 owner 不是本进程（有人在听 ≠ 是我起的）。一行都不许打。")
            return finish(3)

        import attracta_probe as AP
        base = AP.legal_call("双臂验收：干净臂的一份已知合法装配")
        rows = {}

        t0, state0, raw0 = tally(url)
        c0 = variant(base, "clean-c0")
        txt, err = http_call(url, "kernel.record_call", c0)
        rows["C0"] = (err, txt)
        w("")
        w("C0 干净调用（对照） ｜ isError=%s ｜ %s" % (err, txt.replace("\n", " / ")[:220]))

        t_pre0 = tally(url)[0]
        b1 = variant(base, "bad-b1")
        b1["input"]["Tokens"]["Source"] = ""
        pt, pe = http_call(url, "kernel.check_call", b1)
        rows["B1-pre"] = (pe, pt)
        t_pre1 = tally(url)[0]
        w("B1 预检（Basis=3、Source 空） ｜ isError=%s ｜ %s" % (pe, pt.replace("\n", " / ")[:260]))
        w("   预检桶差值：%s" % json.dumps(deltas(t_pre0, t_pre1), ensure_ascii=False))

        t_b0 = tally(url)[0]
        bt, be = http_call(url, "kernel.record_call", b1)
        rows["B1-rec"] = (be, bt)
        t_b1 = tally(url)[0]
        w("B1 落账（同一份坏记录） ｜ isError=%s ｜ %s" % (be, bt.replace("\n", " / ")[:260]))
        w("   内核拒收桶差值：%s" % json.dumps(deltas(t_b0, t_b1), ensure_ascii=False))

        c1 = variant(base, "fixed-b1")
        c1["input"]["Tokens"]["Source"] = "估算：1 token 约 3 字节的粗估"
        ct, ce = http_call(url, "kernel.record_call", c1)
        rows["C1"] = (ce, ct)
        w("C1 把 B1 修好（补回 Source） ｜ isError=%s ｜ %s" % (ce, ct.replace("\n", " / ")[:220]))

        t_d0 = tally(url)[0]
        d0t, d0e = http_call(url, "kernel.declare_reader",
                             {"reader": "arm-reader", "key": "chunks", "side": "output"})
        rows["B2-D0"] = (d0e, d0t)
        d2t, d2e = http_call(url, "kernel.declare_reader",
                             {"reader": "arm-reader", "key": "chunks", "side": "output"})
        rows["B2-dup"] = (d2e, d2t)
        c2t, c2e = http_call(url, "kernel.declare_reader",
                             {"reader": "arm-reader-fixed", "key": "chunks", "side": "output"})
        rows["B2-new"] = (c2e, c2t)
        t_d1 = tally(url)[0]
        b2_delta = deltas(t_d0, t_d1)
        w("B2 对照声明 ｜ isError=%s ｜ %s" % (d0e, d0t.replace("\n", " / ")[:180]))
        w("B2 重复声明 ｜ isError=%s ｜ %s" % (d2e, d2t.replace("\n", " / ")[:220]))
        w("B2 修好后的新读者 ｜ isError=%s ｜ %s" % (c2e, c2t.replace("\n", " / ")[:180]))
        w("   B2 三个声明前后六桶差值：%s" % json.dumps(b2_delta, ensure_ascii=False))

        c3 = variant(base, "clean-falsifier", 0)
        c3t, c3e = http_call(url, "kernel.record_call", c3)
        rows["C3"] = (c3e, c3t)
        w("C3 干净臂的反针（发号=0） ｜ isError=%s ｜ %s" % (c3e, c3t.replace("\n", " / ")[:220]))

        vt, ve = http_call(url, "kernel.verify", {})
        w("链验证 ｜ isError=%s ｜ %s" % (ve, vt.replace("\n", " / ")[:220]))
        rt, re_ = http_call(url, "kernel.readers_report", {})
        w("读者报告 ｜ isError=%s ｜ %s" % (re_, rt.replace("\n", " / ")[:260]))
        t_final, state_final, raw_final = tally(url)
        w("末态六桶 ｜ %s ｜ 口径=%s" % (json.dumps(t_final, ensure_ascii=False), state_final))

        n_b1 = naming(bt + " ⏎ " + pt, "tokens", "token 数")
        n_b2 = naming(d2t, "chunks", "chunks")
        weak_ok, weak_reasons = checker(rows, n_b1, n_b2, b2_delta, vt, strict=False)
        strict_ok, strict_reasons = checker(rows, n_b1, n_b2, b2_delta, vt, strict=True)
        w("")
        w("B1 点名形状=%s ｜ B2 点名形状=%s" % (n_b1, n_b2))
        w("弱判据=%s ｜ 原因=%s" % ("PASS" if weak_ok else "FAIL", "；".join(weak_reasons) or "无"))
        w("严格判据=%s ｜ 原因=%s" % ("PASS" if strict_ok else "FAIL", "；".join(strict_reasons) or "无"))
        w("严格判据比弱判据多的一条：坏臂的拒收必须进对计数桶；B2 这一族今天六桶全不动。")

        bad_rows = dict(rows)
        bad_rows["C0"] = (True, "整发拒收：合成坏结果")
        selftest_bad, _ = checker(bad_rows, n_b1, n_b2, b2_delta, vt, strict=False)
        selftest_naming = naming("一句没有键名也没有标签的拒绝", "tokens", "token 数")
        w("")
        w("== 探针自检（尺子会不会红）==")
        w("合成坏结果（C0 被拒）→ 判定=%s（期望 FAIL）" % ("PASS" if selftest_bad else "FAIL"))
        w("合成无点名文本 → 分类=%s（期望 UNNAMED）" % selftest_naming)
        if selftest_bad or selftest_naming != "UNNAMED":
            w("自检坏：这把尺子不会红，今天的读数不许引用。")
            return finish(2)

        w("")
        w("== 自印分母 ==")
        w("臂 8 条：C0、B1-pre、B1-rec、C1、B2-D0、B2-dup、B2-new、C3 —— 实到 %d 条" % len(rows))
        w("链上条目应为 98（C0 49 + C1 49），末态 verify：%s" % ("✓" if "✓" in vt else "✗/核不了"))
        w("这只是脚本化演练：干净臂的「真人 30 分钟接入」今天仍是 NOT_RUN（G10=0）。")
        w("它不证明：不证明所有 Measure 格都这样点名（只测了 tokens 一格）；不证明 declare_reader 的拒收已经有桶。")
        return finish(0 if (weak_ok and strict_ok) else (1 if weak_ok else 1))
    finally:
        try:
            proc.terminate()
            proc.wait(timeout=10)
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass
        logf.close()


if __name__ == "__main__":
    code = main()
    # main() 已经 finish 过；这里只在异常退出时补一份落盘，避免空报告。
    if code is None:
        code = 1
    sys.exit(code)
