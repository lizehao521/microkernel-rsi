# -*- coding: utf-8 -*-
"""acceptance_probe —— 把 `产品定位.md` §唯一验收从散文变成**能红的判定**。

验收原文三条（一字不替自己改写）：
  1. 账上 `被拒的写入 ≥ 1`，且每一次都能指名是哪一格拦的、内核原话是什么；
  2. 全程链自洽、`sk-` 0 次、任何一格都不许出现"没量到却写成 0"；
  3. 接入耗时 ≤30 分钟，调用方模型代码零改动。
再加一条本项目自己立的边界：**这一条只在"不是我写的"调用方身上算数**，
所以本脚本**拒绝**为作者本人或本项目的 agent 替身打勾（见 BLOCKLIST 与 --attest-caller）。
**但别把这句读反**：躲开 BLOCKLIST 不等于能拿到 PASS。今天段3 对**任何**调用方都拿不到 PASS，
因为验收第①条的后半「每一次都指名是哪一格」还没有可机械核的形状（看板 #48，未落）——
外部人照本脚本跑出一行 FAIL 时，那是判定器的实话缺一半，不是他没做对。

三段各自印分母，跑不成就是 NOT_RUN —— 不许塌成 PASS（三态不塌一态）：
  段 1  两面 `tools/list` 的**字节**对照（stdio 与 streamable-HTTP 是同一个 owner 吗？这是看板 #37）
  段 2  把调用方交来的账本喂给 `kernel.load_journal`，让**内核自己**说收不收；再统计（统计不是判定）
  段 3  三条验收逐条判，判定材料是调用方自己存的 transcript；没有 transcript → 整段 NOT_RUN

跑法（两个 exe 都先用 go 编好，脚本不自己编译）：
    python tasks/probe/acceptance_probe.py <mcpkernel.exe> <mcphttp.exe> [账本.jsonl] [--attest-caller "名字/来历"] [--transcript t.jsonl]
"""
import hashlib
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORT = os.path.join(ROOT, "报告", "唯一验收-可执行形式-1017.txt")
# 输出路径以前是写死的，重跑一次就把上一轮的归档覆盖了（10-17 一天里我犯过两次，
# 见看板「报告名一轮一个不复用」那条）。现在：要写进一个已存在的文件必须显式 --force-overwrite，
# 否则拒绝起跑 —— 覆盖归档这种事不该靠人记得住。
# 内置名单：命中就**不算**验收通过 —— 这一条不能靠调用方自觉，也不能靠脚本的解释权。
BLOCKLIST = ("作者", "author", "agent", "替身", "cold-call", "attracta", "a0-", "probe")
# 段3 那张判定表应有的条数（拦名／①后半可核／①前半被拒数／段2／③耗时）。
# 增删一行必须同时改这里，并在归档里说清多出来算哪一条 —— 否则自核报「坏」、退出 2。
GATE_N = 5
out = []


def w(s):
    out.append(s)
    sys.stdout.buffer.write(s.encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
    sys.stdout.flush()


def http_call(url, name, args, rid=1):
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": "tools/call",
                       "params": {"name": name, "arguments": args}}).encode("utf-8")
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    return urllib.request.urlopen(req, timeout=60).read()


def http_raw(url, payload):
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
    return urllib.request.urlopen(req, timeout=60).read()


def stdio_raw(exe, payload):
    p = subprocess.run([exe], input=payload + b"\n", capture_output=True, timeout=60)
    line = [l for l in p.stdout.split(b"\n") if l.strip()][-1] if p.stdout.strip() else b""
    return line


def result_of(raw):
    try:
        return json.loads(raw.decode("utf-8")).get("result")
    except Exception:
        return None


def canon(obj):
    return json.dumps(obj, sort_keys=True, ensure_ascii=False).encode("utf-8")


def main():
    global REPORT
    argv = sys.argv[1:]
    attest, transcript, ledger, exe_stdio, exe_http = "", "", None, None, None
    force = False
    i = 0
    rest = []
    while i < len(argv):
        if argv[i] == "--attest-caller":
            attest = argv[i + 1]; i += 2
        elif argv[i] == "--transcript":
            transcript = argv[i + 1]; i += 2
        elif argv[i] == "--out":
            REPORT = argv[i + 1]
            if not os.path.isabs(REPORT):
                REPORT = os.path.join(ROOT, REPORT)
            i += 2
        elif argv[i] == "--force-overwrite":
            force = True; i += 1
        else:
            rest.append(argv[i]); i += 1
    # 起跑前就挡：归档是凭据，覆盖等于销毁上一轮。默认拒绝，显式 --out 到新名或 --force-overwrite 才继续。
    if os.path.exists(REPORT) and not force:
        print("REFUSE_TO_OVERWRITE: %s 已存在（那是上一轮的凭据）。"
              "要留新一轮就加 --out 一个新文件名；确实要覆盖再加 --force-overwrite。"
              % REPORT.encode("unicode_escape", "backslashreplace").decode("ascii"))
        return 3
    if len(rest) >= 2:
        exe_stdio, exe_http = rest[0], rest[1]
    if len(rest) >= 3:
        ledger = rest[2]
    if not ledger:
        ledger = os.path.join(ROOT, "报告", "样例-agent.jsonl")

    w("== 唯一验收·可执行形式 · %s · 判据表由现场 celltable 要，不抄 ==" % time.strftime("%F %T"))
    verdicts = {}

    # ———— 段 1：两面 tools/list 的字节对照 ————
    st = "NOT_RUN"
    if not (os.path.exists(exe_stdio or "") and os.path.exists(exe_http or "")):
        w("段1 NOT_RUN：两个 exe 不齐（stdio=%r http=%r）—— 先 go build，别把没跑成读成过"
          % (exe_stdio, exe_http))
    else:
        req = b'{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
        a_raw = stdio_raw(exe_stdio, req)
        port = 8794
        srv = subprocess.Popen([exe_http, "-listen", "127.0.0.1:%d" % port],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        url = "http://127.0.0.1:%d/mcp" % port
        for _ in range(25):
            try:
                http_call(url, "kernel.celltable", {}); break
            except Exception:
                time.sleep(1)
        h_raw = http_raw(url, req)
        a_res, h_res = result_of(a_raw), result_of(h_raw)
        md5 = lambda b: hashlib.md5(b).hexdigest()[:12]
        same_bytes, same_result = a_raw == h_raw, a_res is not None and canon(a_res) == canon(h_res)
        n_a = len((a_res or {}).get("tools", [])); n_h = len((h_res or {}).get("tools", []))
        st = "PASS" if same_result else "FAIL"
        w("段1 [%s] tools/list 对照：工具数 stdio=%d http=%d；result 规范化后逐字节 %s；原始回话字节 %s（%d vs %d B）"
          % (st, n_a, n_h, "相同" if same_result else "不同", "相同" if same_bytes else "不同",
             len(a_raw), len(h_raw)))
        if not same_result:
            keys = lambda r: sorted([t.get("name", "?") for t in (r or {}).get("tools", [])])
            w("   不同在哪：stdio 工具名=%s" % keys(a_res))
            w("             http  工具名=%s" % keys(h_res))
        else:
            w("   md5(result) 两面都是 %s —— 「协议只有一个 owner」这一句今天有字节撑着（#37）" % md5(canon(a_res)))
        srv.terminate()
        try:
            srv.wait(timeout=10)
        except Exception:
            srv.kill()
    verdicts["段1 两面同一份协议"] = st

    # ———— 段 2：账本让内核自己收；统计不是判定 ————
    port2 = 8793
    st2 = "NOT_RUN"
    if not os.path.exists(ledger):
        w("段2 NOT_RUN：账本文件不在（%s）" % ledger)
    else:
        srv2 = subprocess.Popen([exe_http, "-listen", "127.0.0.1:%d" % port2],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        url2 = "http://127.0.0.1:%d/mcp" % port2
        for _ in range(25):
            try:
                http_call(url2, "kernel.celltable", {}); break
            except Exception:
                time.sleep(1)
        text = open(ledger, encoding="utf-8").read()
        if not text.endswith("\n"):
            text += "\n"
        raw = http_call(url2, "kernel.load_journal", {"text": text, "acknowledge": "replace"})
        loaded = json.loads(raw.decode())
        ltxt = loaded["result"]["content"][0]["text"]
        lerr = bool(loaded["result"].get("isError"))
        vraw = http_call(url2, "kernel.verify", {})
        vtxt = json.loads(vraw.decode())["result"]["content"][0]["text"]
        lines = [l for l in text.splitlines() if l.strip().startswith("{")]
        entries = [json.loads(l) for l in lines]
        empty_claim = sum(1 for e in entries if not (e.get("claim") or "").strip())
        unset = sum(1 for e in entries if e.get("basis") == 0)
        nosrc = sum(1 for e in entries if e.get("basis") in (2, 3) and not (e.get("source") or "").strip())
        sk = text.lower().count("sk-")
        sess = sorted({e.get("session", "") for e in entries})
        steps = len({(e.get("session"), e.get("step")) for e in entries})
        # 条件 2 里那句「没量到却写成 0」：不在这里重搭一份判据（那会是第二个 owner），
        # 而是看内核收不收 + 数出「measured 且 payload 是裸 0」的格数，**只当统计、不当判定**。
        meas_zero = sum(1 for e in entries if e.get("basis") == 1 and (e.get("claim") or "").strip() == "0")
        st2 = "PASS" if (not lerr and "✓" in vtxt and empty_claim == 0 and unset == 0 and sk == 0) else "FAIL"
        w("段2 [%s] 内核自己怎么说：载入 isError=%s ｜ verify=%s" % (st2, lerr, vtxt[:110]))
        w("   统计（不是判定）：条目 %d、发 %d、session %s ｜ claim 空 %d ｜ basis=unset %d ｜ "
          "转述/估算没写 Source %d ｜ 裸 0 的 measured %d 条 ｜ sk- %d 次"
          % (len(entries), steps, sess, empty_claim, unset, nosrc, meas_zero, sk))
        if meas_zero:
            w("   「没量到却写成 0」这一半**不由本脚本判定**（判据只有一份，住在内核）；"
              "这里只是把候选数摆出来，要它变成红的一栏得等 #42 那半（字符串/计数格写不出缺席）修完")
        srv2.terminate()
        try:
            srv2.wait(timeout=10)
        except Exception:
            srv2.kill()
    verdicts["段2 账本被内核收下"] = st2

    # ———— 段 3：三条验收逐条判，材料是调用方自己存的 transcript ————
    st3 = "NOT_RUN"
    gates = []
    selfcheck = "未跑（段3 没材料，那张表没建起来）"
    if not transcript or not os.path.exists(transcript):
        w("段3 NOT_RUN：没有调用方的 transcript（--transcript 没给或文件不在）—— "
          "这一条**不许**因为前两段绿就算通过：它要的是「一个不是我写的调用方」的现场")
    else:
        rows = [json.loads(l) for l in open(transcript, encoding="utf-8") if l.strip()]
        refused = [r for r in rows if r.get("isError")]
        times = [r.get("t") for r in rows if r.get("t")]
        mins = 0.0
        if len(times) >= 2:
            f = "%Y-%m-%d %H:%M:%S"
            t0 = time.mktime(time.strptime(times[0], f)); t1 = time.mktime(time.strptime(times[-1], f))
            mins = (t1 - t0) / 60.0
        who = (attest or "").strip()
        blocked = any(b.lower() in who.lower() for b in BLOCKLIST) or who == ""
        # ① 拆两半，因为今天只有一半能机械核：
        #    「被拒写入 ≥1」= 读 tally 里那个**数**（不嗅正文）；
        #    「每一次都指名是哪一格」= 内核今天指名的方式是**人话**（"通知报的是「发出去了」，却没有正文"、
        #    "通道上收到的字节"），回话里没有结构化的格名 —— 所以这一半**不可机械核**，
        #    列原话给人判，不冒充判定（红不了的东西不该叫判据；把 snake 键名当尺子是我第一版用错了）。
        tally_row = next((r for r in rows if r.get("tool") == "kernel.tally"), None)
        n_txt = (tally_row or {}).get("text", "")
        m_kernel = re.search(r"被拒的写入 (\d+) 次", n_txt)
        m_door = re.search(r"累计 (\d+) 次|(\d+) 次写请求根本没走到内核", n_txt)
        refused_kernel = sum(1 for r in refused if r.get("tool") == "kernel.record_call"
                             and "整发拒收" in (r.get("text") or ""))
        c1 = bool(m_kernel) and int(m_kernel.group(1)) >= 1
        c1_named = "NOT_CHECKABLE"
        w("段3 ① 被拒记录 %d 条（其中落账拒收 %d 条）｜ tally 自印 内核拒的=%s 次 → %s"
          % (len(refused), refused_kernel, (m_kernel.group(1) if m_kernel else "?"), "成" if c1 else "不成"))
        w("   ① 的后半「每一次都指名是哪一格」= %s —— 内核的指名是人话，回话里没有结构化格名；"
          "下面逐条列原话供人判，不冒充判定" % c1_named)
        for r in refused:
            w("     %s ｜ %s ｜ %s" % (r.get("t"), r.get("tool"), (r.get("text") or "").replace("\n", " ⏎ ")[:150]))
        w("   ② 由段 2 那一行决定（内核说收 + sk- 0 次）；「没量到写成 0」那一半今天还不能由脚本判定")
        w("   ③ 首末时间戳差 = %.1f 分钟（门槛 30）→ %s ｜ 注意：这一支替身是脚本连跑，"
          "不含人类读文档的时间，所以它**不能**替 §唯一验收的那一条计时" % (mins, "成" if mins <= 30 else "不成"))
        w("   attest 的调用方来历 = %r ｜ BLOCKLIST 命中 = %s" % (who, blocked))
        # 判定与理由**同源**：以前 st3 自己一套表达式、理由另列一套，于是"只印一条理由"能带着
        # "实际有两条独立原因"一起成立 —— 外部读者就被那一行 FAIL 骗去以为问题出在自己名字上。
        # 现在这张表既是判定也是理由：(名字, 这条成立吗)。条数由下面的自核钉住。
        gates = [
            ("调用方来历命中 BLOCKLIST 或没给", not blocked),
            ("①的后半「每一次都指名是哪一格」不可机械核 —— 这一条与你是谁无关", c1_named != "NOT_CHECKABLE"),
            ("①的前半：tally 自印的被拒写入 <1", c1),
            ("段2 没通过", verdicts["段2 账本被内核收下"] == "PASS"),
            ("③接入耗时 >30 分钟", mins <= 30),
        ]
        reasons = [name for (name, ok) in gates if not ok]
        st3 = "FAIL" if reasons else "PASS"
        w("   FAIL 的全部理由（%d 条，逐条独立）：%s"
          % (len(reasons), "；".join(reasons) if reasons else "无"))
        w("   段3 拿 PASS 今天可达吗 = %s —— 不可达的原因是上面那条「与你是谁无关」的，"
          "不是因为你名字没写好；换真人来跑也一样 FAIL"
          % ("能" if c1_named != "NOT_CHECKABLE" else "不能"))
        w("段3 [%s] —— %s" % (st3, "；".join(reasons) if reasons
                          else "三条都可机械核，且调用方不在本项目名单上"))
        # 自核钉的是**条数**：这张表增删一行而不同步改 GATE_N，就是这支脚本自己坏了（报「坏」、退出 2）。
        # 说清它不钉什么：表里某个 ok 写反它抓不到（那会直接改判据结论，由两臂读档与复跑管）。
        selfcheck = "OK" if len(gates) == GATE_N else "坏：表里 %d 条，抬头钉的是 %d 条" % (len(gates), GATE_N)
    verdicts["段3 三条验收"] = st3

    w("")
    w("== 自印分母 ==")
    w("段 3 段；判定 %s" % "；".join("%s=%s" % (k, v) for k, v in verdicts.items()))
    w("段3 判定表实到 %d 条（抬头钉 %d 条） ｜ 自核（这支脚本自己的尺子）= %s" % (len(gates), GATE_N, selfcheck))
    w("NOT_RUN 不是 PASS，也不是 FAIL —— 是「这一条今天没跑成」。")
    w("本脚本不重搭判据：段2 的收拒由 `kernel.load_journal`/`kernel.verify` 说，段1 只比字节。")
    import io
    io.open(REPORT, "w", encoding="utf-8").write("\n".join(out) + "\n")
    print("REPORT=" + REPORT.encode("unicode_escape").decode("ascii"))
    # 退出码分三档，别让"尺子坏了"混进"判据没过"：2=自核坏（脚本自己不对），1=判据没过，0=三段全 PASS。
    if selfcheck.startswith("坏"):
        return 2
    return 0 if all(v == "PASS" for v in verdicts.values()) else 1


if __name__ == "__main__":
    sys.exit(main())
