# -*- coding: utf-8 -*-
# llm_stream.py —— 真端点的**流式**那一发：在**外部第三方端点**上把 Chunks / FirstByteMs / MaxGapMs 量成 measured。
#
# 更正（10-17 01:50 现场重数，别把这一支读成"史上第一次量到帧"）：这三格在本仓并不新鲜——
# 21 本 `.jsonl` 里它们共 198 行 = absent 159 ＋ measured 39，11 本作者旧账各有 3 行 measured（他自己的流式那几发）。
# 这一支的新意只在两处：① 数帧的是**外部端点**而不是自建假端点；② 同一发上出现
# 「端点自报 usage 全 0」与「我们数到 28264 字节 / 56 帧」并排落账，而今天没有判据拦这一族矛盾（#47）。
#
# 为什么要这一支：作者旧账里那 11 本的流式三格是他**自建/自家端点**上量的；
# 外部端点的帧数、首帧与最大帧间到今天为止没人量过，而它才是内核将来要审计的那种形状。
#
# 10-17 撞上的一件事（这一支存在的理由）：推理型模型把输出放在 `delta.reasoning_content`，
# `delta.content` 可以是空的 —— 上一支（llm_probe.py）只读 content，于是把「0 字节原话」记成正常收尾，
# 内核整发拒收（拒得对）。这一支**两个字段都数**，并让账说实话：
# 原话住哪一格、内核认不认，都由落账那一刻定，不替内核圆场。
#
# key 只从环境变量读；不落进仓库、不落进账本；控制台只打 ASCII，中文读数落文件。
import hashlib
import io
import json
import os
import subprocess
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client import INPUT1, Judge, Kernel, m, missing

BASE = os.environ.get("AGNES_BASE", "")
MODEL = os.environ.get("AGNES_MODEL", "")
KEY = os.environ.get("AGNES_KEY", "")
if not KEY or not BASE:
    raise SystemExit("NO KEY/BASE IN ENV (set AGNES_KEY/AGNES_BASE) —— 不要把 key 写进文件")

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORT = os.path.join(ROOT, "报告", "真流量-流式第一发-1017.txt")
LEDGER = os.path.join(os.environ.get("TEMP", "/tmp"), "账本-流式第一发.jsonl")
MEASURED, RELAY, EST = 1, 2, 3
PROMPT = "用中文说一句话，说明你是谁。"
SYSTEM = "你是被测方，按用户要求回答即可。"

log = []
note = log.append
check = Judge()


def sse(body_obj):
    """发一次流式调用，返回（拼回来的原文, 推理原文, 帧数, 首帧 ms, 最大帧间 ms, 收到的总字节, usage, finish）。

    帧数与帧间是**我们在通道上数出来的** ⇒ 出处只能是实测；
    usage 与 finish_reason 是端点自报 ⇒ 转述 / model_says，永远是线索不是事实。
    """
    body = json.dumps(body_obj, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(BASE + "/chat/completions", data=body, method="POST")
    req.add_header("Authorization", "Bearer " + KEY)
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "text/event-stream")
    t0 = time.monotonic()
    raw = b""
    ans, rea = [], []
    frames = 0
    gaps = 0
    first = None
    last = None
    usage = {}
    finish = ""
    err = ""
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            status = r.getcode()
            ctype = r.headers.get("Content-Type", "")
            buf = b""
            for chunk in iter(lambda: r.read(256), b""):
                now = time.monotonic()
                if first is None:
                    first = now
                elif last is not None:
                    gaps = max(gaps, int((now - last) * 1000))
                last = now
                raw += chunk
                buf += chunk
                while b"\n" in buf:
                    line, buf = buf.split(b"\n", 1)
                    line = line.strip()
                    if not line.startswith(b"data:"):
                        continue
                    payload = line[5:].strip()
                    if payload == b"[DONE]":
                        continue
                    frames += 1
                    try:
                        d = json.loads(payload.decode("utf-8"))
                    except Exception:
                        continue
                    if d.get("usage"):
                        usage = d["usage"]
                    for ch in (d.get("choices") or []):
                        dl = ch.get("delta") or {}
                        if dl.get("content"):
                            ans.append(dl["content"])
                        if dl.get("reasoning_content"):
                            rea.append(dl["reasoning_content"])
                        if ch.get("finish_reason"):
                            finish = ch["finish_reason"]
    except urllib.error.HTTPError as e:
        status = e.code
        ctype = ""
        err = e.read().decode("utf-8", "replace")[:300]
    return ("".join(ans), "".join(rea), frames,
            int((first - t0) * 1000) if first else 0, gaps, len(raw),
            status, ctype, usage, finish, err)


def main():
    # include_usage 是 OpenAI 兼容端点的常见开关，但不是每家都认：先按标准流式发，
    # usage 拿不到就照实记 absent —— 不许为了让某格有数而猜一个。
    payload = {"model": MODEL, "messages": [{"role": "system", "content": SYSTEM},
                                            {"role": "user", "content": PROMPT}],
               "max_tokens": 256, "stream": True}
    ans, rea, frames, fb, gap, nbytes, status, ctype, usage, finish, err = sse(payload)
    n_out = len(json.dumps(payload, ensure_ascii=False).encode("utf-8"))
    note("== 真端点流式第一发 · %s ==" % time.strftime("%F %T"))
    note("端点=%s 模型=%s" % (BASE, MODEL))
    note("HTTP=%s Content-Type=%r 收到=%d 字节 发出=%d 字节" % (status, ctype, nbytes, n_out))
    if err:
        note("端点错误正文（截 300）：%s" % err)
    note("帧数（data: 行，去掉 [DONE]）=%d 首帧=%dms 最大帧间=%dms" % (frames, fb, gap))
    note("finish_reason=%r usage=%s" % (finish, usage))
    note("delta.content 拼回来 %d 字节：%r" % (len(ans.encode("utf-8")), ans[:200]))
    note("delta.reasoning_content 拼回来 %d 字节：%r" % (len(rea.encode("utf-8")), rea[:200]))
    note("一句实话：这一发的『原话』住在哪一格，是这支驱动定的还是内核定的 —— 下面两条臂各答一次。")

    K = Kernel("stream", sink=note)
    NC_note = []

    def build(text, which):
        """装配一发：输出侧原话 = text；其余格子按真量到的填。"""
        tb = len(text.encode("utf-8"))
        sha = hashlib.sha256(text.encode("utf-8")).hexdigest()
        IN = json.loads(json.dumps(INPUT1))
        IN["Order"] = ["system", "user"]
        IN["Parts"] = [{"Kind": "system", "Parts": 1, "Bytes": len(SYSTEM.encode("utf-8"))},
                       {"Kind": "user", "Parts": 1, "Bytes": len(PROMPT.encode("utf-8"))}]
        IN["ContentBytes"] = sum(p["Bytes"] for p in IN["Parts"])
        IN["Budget"]["Shares"] = {"system": 10, "user": 80}
        IN["Budget"]["LeftoverPct"] = 10
        IN["Notice"] = {"Sent": False, "Trimmed": False,
                        "Why": "这一发没折叠、没裁尾、没整段丢弃，没有通知要发", "Text": ""}
        if "prompt_tokens" in usage:
            IN["Tokens"] = m(int(usage["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
        OUT = {
            "Call": 1, "Streaming": True,
            "Finish": {"Reason": finish, "Said": bool(finish)},
            "Model": MODEL, "ModelSaid": True,
            # 10-17 真端点教的一课：`Content-Type: text/event-stream` 是**我们在通道上观察到的**，
            # 不是对端自报的格式名。填进 WireFormat 又把 WireFormatSaid 写成 false，内核当场拒：
            # 「对端没报与报了个空串是两件事」。今天没有一格放「我们观察到的介质类型」，
            # 所以这里照实留空 + false，观察值只写在读数的抬头那几行里。
            "WireFormat": "", "WireFormatSaid": False,
            "Says": {"Bytes": tb, "Kept": tb, "SHA256": sha},
            "UsageIn": m(int(usage.get("prompt_tokens", 0)), RELAY, "端点流式最后一帧的 usage.prompt_tokens")
                if "prompt_tokens" in usage else missing("流式响应里没有回 usage 这一帧"),
            "UsageOut": m(int(usage.get("completion_tokens", 0)), RELAY, "端点流式最后一帧的 usage.completion_tokens")
                if "completion_tokens" in usage else missing("流式响应里没有回 usage 这一帧"),
            "WireBytesOut": m(n_out, MEASURED),
            "WireBytesIn": m(nbytes, MEASURED),
            "Chunks": m(frames, MEASURED),
            "FirstByteMs": m(fb, MEASURED),
            "MaxGapMs": m(gap, MEASURED),
            "ToolCalls": 0, "Malformed": 0, "MalformedStreak": 0,
            "Failure": {"Kind": "", "Detail": "",
                        "Status": missing("这一发没故障，也就没有要记的状态码"),
                        "RetryAfterMs": missing("这一发没断，也就没有要等多久"),
                        "Downgrade": False},
        }
        return IN, OUT

    # 臂 A：把 content 那一段当原话（上一支的做法，可能还是 0 字节）
    for which, text in (("content", ans), ("reasoning", rea)):
        IN, OUT = build(text, which)
        # 两臂各用**自己的 session**：同一 session 同一发第 1 次落上之后，第二次必被去重门拦，
        # 那样"臂 B 被拒"就分不清是格式的问题还是去重的问题 —— 归因要干净（上一版两臂共用
        # session="realsse"，两条都拒时我就分不清是哪一道拦的）。
        sess = "realsse-" + which
        cc, iserr_cc, _ = K.call("kernel.check_call", {"session": sess, "actor": "llm_stream",
                                                       "input": IN, "output": OUT})
        note("\n---- 臂 %s：原话取 delta.%s（%d 字节）----" % (which, which, len(text.encode("utf-8"))))
        note("预检（isError=%s）：%s" % (iserr_cc, cc.replace("\n", " / ")[:520]))
        rec, iserr_rc, _ = K.call("kernel.record_call", {"session": sess, "actor": "llm_stream",
                                                         "input": IN, "output": OUT})
        note("落账（isError=%s）：%s" % (iserr_rc, rec.replace("\n", " / ")[:520]))
        NC_note.append((which, len(text.encode("utf-8")), iserr_cc, iserr_rc))
        if not iserr_rc:
            # 落上了就把链和读侧一起看：三格今天第一次有真数
            rb, _, _ = K.call("kernel.read_back", {"session": sess, "call": 1, "side": "output"})
            io.open(os.path.join(os.environ.get("TEMP", "/tmp"), "readback-stream-%s.txt" % which),
                    "w", encoding="utf-8").write(rb)
            hit = [l for l in rb.splitlines() if any(k in l for k in ("chunks", "first_byte_ms", "max_gap_ms"))]
            note("读回这三格：%s" % (" ｜ ".join(h.strip() for h in hit) if hit else "读回里没有这三格的名字（别读成没记）"))
        note("这一臂的结论：%s（原话 %d 字节）—— 收还是拒由内核定，这支脚本不替它判，所以不进分母。"
             % ("拒" if iserr_rc else "收", len(text.encode("utf-8"))))

    ta, _, _ = K.call("kernel.tally")
    note("\ntally：%s" % ta.replace("\n", " / ")[:600])
    vf, _, _ = K.call("kernel.verify")
    note("verify：%s" % vf.splitlines()[0][:300])
    ej, _, _ = K.call("kernel.encode_journal", {"payload": "included"})
    book = ej.split("\n", 1)[1] if "\n" in ej else ""
    lines = len([l for l in book.splitlines() if l.strip()])
    io.open(LEDGER, "w", encoding="utf-8", newline="\n").write(book)
    note("交出去的字节：自印 %d 行、%d 字节（**这两个数由本脚本现场数，不抄**）；账落 %s"
         % (lines, len(book.encode("utf-8")), LEDGER))
    leak = [pat for pat in (KEY[:12], "ms-", "sk-", "Bearer") if pat and pat in book]
    note("泄露扫查：交出的字节里命中 %s（要 0 次）；session 名 'realsse' 命中=%s"
         % (leak or "无", "realsse" in book))
    if book.strip():
        check("交出的账本里没有口令的影子", "无" if not leak else str(leak), ["无"])
    else:
        note("  —— 账本是空的（一发都没落上），这一条今天**判不出结论**，不许读成「扫过了、干净」。")
    K.close()

    note("\n===== 判定 =====")
    for name, st, why in check.rows:
        note("%s  %s  %s" % (st, name, why))
    note("两臂收/拒：%s" % NC_note)
    io.open(REPORT, "w", encoding="utf-8").write("\n".join(log) + "\n")
    print("STREAM PROBE checks=%d red=%d arms=%s" % (len(check.rows), len(check.red), NC_note))
    return 0


if __name__ == "__main__":
    sys.exit(main())
