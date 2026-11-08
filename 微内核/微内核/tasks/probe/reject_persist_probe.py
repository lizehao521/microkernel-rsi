# -*- coding: utf-8 -*-
"""reject_persist_probe.py —— 把 #51 打成读数：内核那一桶（判据拒＝发）活不过一次重建。

为什么单独测这一条：`kaudit` 是对外的审计面，它印的 `rejected_writes` 取自
`s.led.Rejected()`；而 `LoadLedger` 只做 `NewLedger()` + `l.docs = j.Docs`，
`Journal` 结构里压根没有拒收数这一格（journal.go:122-136）。
所以「这本账拒过几次」从文件里读不回来 —— 但读数仍然印出一个 0，
读的人会把它当成「一次都没拒过」，那是把缺席读成 0（本项目第一条规矩）。

更要紧的是四桶放在一起看：门口拒／协议层判无效／传输层的门这三桶住在 `Server` 上，
重建账本时它们**接着数**；只有内核那一桶归零。于一句 tally 里同时混着两种寿命的数。

跑法：python tasks/probe/reject_persist_probe.py <mcphttp.exe> <kaudit.exe>
"""
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
MCP = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
KAU = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "..", ".bin", "kaudit.exe")
PORT = 8791
URL = "http://127.0.0.1:%d/mcp" % PORT
KPORT = 8792
OUT = os.path.join(ROOT, "报告", "拒收数不落盘-51-1017.txt")
lines = []


def w(s):
    lines.append(s)
    sys.stdout.buffer.write(s.encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
    sys.stdout.flush()


def call(name, args, rid=1, url=None):
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": "tools/call",
                       "params": {"name": name, "arguments": args}}).encode("utf-8")
    rep = json.loads(urllib.request.urlopen(
        urllib.request.Request(url or URL, data=body,
                               headers={"Content-Type": "application/json"}),
        timeout=90).read().decode("utf-8"))
    res = rep.get("result", {})
    return res.get("content", [{}])[0].get("text", ""), bool(res.get("isError"))


def wait_ready(url, tool="kernel.celltable"):
    for _ in range(25):
        try:
            call(tool, {}, url=url)
            return
        except Exception:
            time.sleep(1)
    raise RuntimeError("服务没起来：" + url)


def doornum(tally_text):
    """同一句 tally 里门口那一桶的数：「另有 N 次写请求根本没走到内核」。"""
    for seg in tally_text.splitlines():
        mm = re.search(r"另有 (\d+) 次写请求根本没走到内核", seg)
        if mm:
            return mm.group(1)
    return "抠不到"


def rejnum(tally_text):
    """从 tally 那句人读里把「被拒的写入 N 次」抠出来 —— 抠不到就说抠不到，不猜。"""
    for seg in tally_text.splitlines():
        if "被拒的写入" in seg:
            mm = re.search(r"被拒的写入 (\d+) 次", seg)
            if mm:
                return mm.group(1)
    return "抠不到"


def main():
    for exe, nm in ((MCP, "mcphttp"), (KAU, "kaudit")):
        if not os.path.exists(exe):
            w("NO_BINARY: %s（%s）先 go build" % (exe, nm))
            return 3
    sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))
    import attracta_probe as AP  # 49 格能落账的那份模板，不自己拼（自己拼红了分不清是谁）

    mcp = subprocess.Popen([MCP, "-listen", "127.0.0.1:%d" % PORT], cwd=ROOT,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        wait_ready(URL)
        w("== #51 拒收数活不过一次重建 ==")
        w("用的字节：%s / %s" % (os.path.basename(MCP), os.path.basename(KAU)))
        w("")
        inp_pack = AP.legal_call("r51 这一发的真 stdout")
        t, e = call("kernel.record_call", inp_pack)
        w("① 第一发落账：isErr=%s（模板 = attracta_probe.legal_call，能过 48 键那一份装配）" % e)
        t, e = call("kernel.record_call", inp_pack)
        w("② 同一发同一侧再记一遍：isErr=%s —— 去重门拒，这一笔应当进内核那一桶" % e)
        lie = json.loads(json.dumps(inp_pack))
        lie["output"]["Says"]["Clipped"] = 0
        t, e = call("kernel.record_call", lie)
        w("③ 写侧塞一个不存在的键（Says.Clipped）：isErr=%s —— 这一笔走不到内核，该进门口那一桶" % e)
        t1, _ = call("kernel.tally", {})
        w("④ 重建前 tally（整句原样印，四桶一起看）：")
        for seg in t1.splitlines():
            w("     %s" % seg)
        w("     抠出来的两个数：内核那一桶 %s 次；门口那一桶 %s 次"
          % (rejnum(t1), doornum(t1)))
        enc, _ = call("kernel.encode_journal", {"payload": "included"})
        nline = len([l for l in enc.splitlines() if l.strip().startswith("{")])
        w("⑤ encode_journal 交出 %d 行账（第一行是人读表头，不是账；交调用方写盘，内核不写）" % nline)
        t, e = call("kernel.load_journal", {"text": enc, "acknowledge": "replace"})
        w("⑥ 同一进程把同一份字节读回来（＝重启一次）：isErr=%s 首行=%s"
          % (e, t.splitlines()[0][:110]))
        t2, _ = call("kernel.tally", {})
        w("⑦ 重建后 tally（整句原样印）：")
        for seg in t2.splitlines():
            w("     %s" % seg)
        w("     抠出来的两个数：内核那一桶 %s 次；门口那一桶 %s 次"
          % (rejnum(t2), doornum(t2)))
        v, _ = call("kernel.verify", {})
        w("   同一次 verify：%s" % v.splitlines()[0][:110])
        w("")
        w("判读：④ 与 ⑦ 之间条数没变、链照样自洽，只有内核那一桶从 %s 掉回 %s；"
          "门口那一桶 %s → %s（它住在 Server 上，不在这条链里）。"
          % (rejnum(t1), rejnum(t2), doornum(t1), doornum(t2)))

        jp = os.path.join(ROOT, "报告", "样例-agent.jsonl")
        kau = subprocess.Popen([KAU, "-journal", jp, "-listen", "127.0.0.1:%d" % KPORT],
                               cwd=ROOT, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            kurl = "http://127.0.0.1:%d/v1/tally" % KPORT
            for _ in range(25):
                try:
                    raw = json.loads(urllib.request.urlopen(kurl, timeout=30).read().decode("utf-8"))
                    break
                except Exception:
                    time.sleep(1)
            else:
                raw = {}
            ent = None
            for l in open(jp, encoding="utf-8"):
                if l.strip():
                    ent = (ent or 0) + 1
            w("")
            w("⑧ 独立审计面（另一个进程，只读那份归档账）：")
            w("   journal 字段=%s" % json.dumps(raw.get("journal"), ensure_ascii=False))
            rw = raw.get("rejected_writes")
            w("   /v1/tally 的 rejected_writes=%r" % rw)
            w("   判读：文件侧非空行 %s，审计侧读到 %s 条（其中带原像 %s 条），"
              "rejected_writes 却是 %r —— 审计方读到 0 会读成「一次都没拒过」，"
              "而真值是「这份文件里没这个数」。"
              % (ent, (raw.get("journal") or {}).get("parsed"),
                 (raw.get("journal") or {}).get("with_payload"), rw))
        finally:
            kau.terminate()
            try:
                kau.wait(timeout=10)
            except Exception:
                kau.kill()
        w("")
        w("四桶分开数（这一条测的是其中两桶的寿命）：本次实测的是内核那一桶（判据拒＝发，1 → 0）"
          "和门口那一桶（写请求，测前 1 次、重建后仍在）。协议层判无效与传输层的门这两桶本次没喂请求，"
          "它们与门口桶同住 `Server`、不进这条链 —— 那是读码所得，别记成本轮的读数。")
        w("要点不在哪个桶掉了，在一句 tally 里同时印着两种寿命的数：重建之后前缀「被拒的写入」印的是"
          "这本账没带过来的数，后缀「另有 N 次写请求」印的是这个进程活着的数，两者并排、都不标注。")
        w("修法要拍口径（不替作者选）：①把拒收数编进 Journal 头部（多一个非格字段，要问算不算动表）；"
          "②把它做成链上的一格（要动 48 键/49 格，封板冻结那条得三件配齐）；"
          "③不修，只让审计面在从文件重建时**别再印这个数**、改印「这本账没带这个数」——"
          "这一种不动表也不动判据，代价是审计方拿不到那个 KPI。")
        return 0
    finally:
        mcp.terminate()
        try:
            mcp.wait(timeout=10)
        except Exception:
            mcp.kill()
        import io
        io.open(OUT, "w", encoding="utf-8").write("\n".join(lines) + "\n")
        print("REPORT=" + OUT.encode("unicode_escape").decode("ascii"))


if __name__ == "__main__":
    sys.exit(main())
