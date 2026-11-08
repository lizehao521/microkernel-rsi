# -*- coding: utf-8 -*-
# llm_probe.py —— 用挂载好的内核，给一个**真 LLM 对话端点**记第一发账。
#
# 为什么这一发值得单独跑：contract.md 第 4 节说，`usage_in`/`usage_out`/`endpoint_model`
# 那几格在 Deep Research 那个端点上填不上，理由是「端点不给」。换一个 OpenAI 兼容的
# chat/completions 端点就能分辨这两种解释到底哪个成立：
#   - 端点给了，而我们没登记   ⇒ 是接线的洞；
#   - 端点给了、登记上了       ⇒ 那一格的「缺席」从此变成「有来源」，
#     而 usage 的出处**只能是转述**（分词器在对端），不能记成实测。
#
# 这一版**不加 cmd/**：慢闸 62 针正在跑，它每根针各自拷贝一次源码树，
# 往树里加新包会让跑到一半的轮次前后不一致。所以这里只经 stdio 驱动已经编译好的服务，
# 账落在临时目录。等探针落地再把它固化成 cmd/kloop。
#
# key 只从环境变量读，不落进仓库、不落进账本、控制台只打 ASCII。
import io
import json
import os
import subprocess
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client import INPUT1, Judge, Kernel, m, missing

BASE = os.environ.get("AGNES_BASE", "https://api.agnes-ai.cn/v1")
MODEL = os.environ.get("AGNES_MODEL", "agnes-3.0-flash")
KEY = os.environ.get("AGNES_KEY", "")
if not KEY:
    raise SystemExit("NO KEY IN ENV (set AGNES_KEY) —— 不要把 key 写进文件")

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORT = os.path.join(ROOT, "报告", "接入LLM-第一发.txt")
LEDGER = os.path.join(os.environ.get("TEMP", "/tmp"), "账本-llm第一发.jsonl")

log = []
note = log.append
check = Judge()

# 上一发模型的**原话**要进下一发的历史，这才是真循环而不是两次独立调用。
# 所以这里不发 tools、不发 system，只发一段 user —— 每一类段都单独计量，
# 与键名表里 parts / part_totals / content_bytes 三个不同的量对上。
PROMPT1 = "用中文说一句话，说明你是谁。"
SYSTEM1 = "你是被测方，按用户要求回答即可。"

MEASURED, RELAY, EST = 1, 2, 3


def chat(messages, stream=False):
    """发一次真调用，返回（响应正文, 收到的字节, 首帧 ms, 最大帧间 ms, 帧数, HTTP 状态）。

    字节是**我们在通道上数出来的**，不是端点说的 —— 出处只能记实测。
    """
    body = json.dumps({"model": MODEL, "messages": messages,
                       "max_tokens": 64, "stream": stream}, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(BASE + "/chat/completions", data=body, method="POST")
    req.add_header("Authorization", "Bearer " + KEY)
    req.add_header("Content-Type", "application/json")
    t0 = time.monotonic()
    raw, frames, gaps = b"", 0, 0
    with urllib.request.urlopen(req, timeout=90) as r:
        status = r.getcode()
        if not stream:
            raw = r.read()
            frames, gaps = 1, int((time.monotonic() - t0) * 1000)
        else:
            buf, last, first = b"", None, None
            for chunk in iter(lambda: r.read(256), b""):
                now = time.monotonic()
                if first is None:
                    first = now
                else:
                    gaps = max(gaps, int((now - last) * 1000))
                last = now
                buf += chunk
                raw += chunk
                frames = buf.count(b"\n\n")
    return raw, status, len(raw), int((time.monotonic() - t0) * 1000), gaps, frames


K = Kernel("llm", sink=note)

# ---- 第一发：非流式 ----
raw, status, n_in, ms_all, _, _ = chat([{"role": "system", "content": SYSTEM1},
                                          {"role": "user", "content": PROMPT1}])
resp = json.loads(raw.decode("utf-8"))
choice = resp["choices"][0]
text = choice["message"]["content"]
usage = resp.get("usage") or {}
finish = choice.get("finish_reason") or ""
model_said = resp.get("model") or ""
n_out = len(json.dumps({"model": MODEL, "messages": [{"role": "system", "content": SYSTEM1},
                                                       {"role": "user", "content": PROMPT1}],
                        "max_tokens": 64, "stream": False}, ensure_ascii=False).encode("utf-8"))
note("HTTP=%s 收到=%d 字节 发出=%d 字节 首包到全部=%dms" % (status, n_in, n_out, ms_all))
note("端点自报模型=%r finish_reason=%r usage=%s" % (model_said, finish, usage))
note("模型原话（%d 字节）：%s" % (len(text.encode("utf-8")), text))

# 出处一格一格对上：这是内核最在意的那件事。
# usage 是端点给的 ⇒ 转述（relayed），绝不能记成实测；
# 字节是我们自己在通道上数的 ⇒ 只能实测；
# 模型自报的 id ⇒ model_says，永远是线索不是事实。
OUT1 = {
    "Call": 1,
    "Streaming": False,
    "Finish": {"Reason": finish, "Said": finish != ""},
    "Model": model_said, "ModelSaid": model_said != "",
    "WireFormat": "", "WireFormatSaid": False,
    "Says": {"Bytes": len(text.encode("utf-8")), "Kept": len(text.encode("utf-8")),
             "SHA256": __import__("hashlib").sha256(text.encode("utf-8")).hexdigest()},
    "UsageIn": m(int(usage.get("prompt_tokens", 0)), RELAY, "端点响应的 usage.prompt_tokens")
        if "prompt_tokens" in usage else missing("端点响应里没有 usage.prompt_tokens"),
    "UsageOut": m(int(usage.get("completion_tokens", 0)), RELAY, "端点响应的 usage.completion_tokens")
        if "completion_tokens" in usage else missing("端点响应里没有 usage.completion_tokens"),
    "WireBytesOut": m(n_out, MEASURED),
    "WireBytesIn": m(n_in, MEASURED),
    "Chunks": missing("这一发不是流式的，结构上没有分帧"),
    "FirstByteMs": missing("这一发不是流式的，没有首帧这件事"),
    "MaxGapMs": missing("这一发不是流式的，没有帧间"),
    "ToolCalls": 0, "Malformed": 0, "MalformedStreak": 0,
    "Failure": {"Kind": "", "Detail": "", "Status": missing("这一发没故障，也就没有要记的状态码"),
                "RetryAfterMs": missing("这一发没断，也就没有要等多久"), "Downgrade": False},
}
IN1 = json.loads(json.dumps(INPUT1))
IN1["Order"] = ["system", "user"]
IN1["Parts"] = [
    {"Kind": "system", "Parts": 1, "Bytes": len(SYSTEM1.encode("utf-8"))},
    {"Kind": "user", "Parts": 1, "Bytes": len(PROMPT1.encode("utf-8"))},
]
IN1["ContentBytes"] = sum(p["Bytes"] for p in IN1["Parts"])
IN1["Budget"]["Shares"] = {"system": 10, "user": 80}
IN1["Budget"]["LeftoverPct"] = 10
# 端点报了 prompt_tokens ⇒ 输入侧那一格从「我们的粗估」升级成「转述端点给的数」。
# 升级的是出处档（估算 → 转述），不是「我们自己量到了」——分词器始终在对端。
if "prompt_tokens" in usage:
    IN1["Tokens"] = m(int(usage["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
# 这一发没裁过任何东西 —— 所以那句「本节被裁过」的知情通知**不该**被记成发出去了。
# 照 INPUT1 那份夹具把 Notice 写成 Sent=true 就是编数：内核收得下，但账上是假的。
IN1["Notice"] = {"Sent": False, "Trimmed": False,
                 "Why": "这一发没有折叠、没有裁尾、没有整段丢弃，没有通知要发", "Text": ""}

cc, _, _ = K.call("kernel.check_call", {"session": "agnes", "actor": "llm_probe",
                                         "input": IN1, "output": OUT1})
note("内核先说什么：%s" % cc)
check("01 真 LLM 那一发两侧都自洽", cc, ["输入侧：自洽", "输出侧：自洽"])

rec, _, _ = K.call("kernel.record_call", {"session": "agnes", "actor": "llm_probe",
                                          "input": IN1, "output": OUT1})
# 一发几条从内核的表里读，不抄在这里（表长了两位时，红的该是我的抄数）。
_ct0, _, _ = K.call("kernel.celltable")
import re as _re
_m0 = _re.search(r"当前 (\d+) 格", _ct0 or "")
NC = int(_m0.group(1)) if _m0 else 49
check("02 落账 %d 条" % NC, rec, ["整发落账 %d 条" % NC])

# ---- 第二发：把第一发的原话真放进历史（这是循环，不是重复调用）----
P2 = "把你刚才那句话再短一半。"
raw2, st2, n_in2, ms2, _, _ = chat([{"role": "system", "content": SYSTEM1},
                                      {"role": "user", "content": PROMPT1},
                                      {"role": "assistant", "content": text},
                                      {"role": "user", "content": P2}])
r2 = json.loads(raw2.decode("utf-8"))
c2 = r2["choices"][0]
t2 = c2["message"]["content"]
u2 = r2.get("usage") or {}
n_out2 = len(json.dumps({"model": MODEL, "messages": [
    {"role": "system", "content": SYSTEM1}, {"role": "user", "content": PROMPT1},
    {"role": "assistant", "content": text}, {"role": "user", "content": P2}],
    "max_tokens": 64, "stream": False}, ensure_ascii=False).encode("utf-8"))
note("第 2 发 HTTP=%s 收到=%d 发出=%d 原话=%s" % (st2, n_in2, n_out2, t2))

IN2 = json.loads(json.dumps(IN1))
IN2["Call"] = 2
IN2["Order"] = ["system", "user", "history", "user"]
hbytes = len(text.encode("utf-8"))
IN2["Parts"] = [
    {"Kind": "system", "Parts": 1, "Bytes": len(SYSTEM1.encode("utf-8"))},
    {"Kind": "user", "Parts": 2, "Bytes": len(PROMPT1.encode("utf-8")) + len(P2.encode("utf-8"))},
    {"Kind": "history", "Parts": 1, "Bytes": hbytes},
]
IN2["ContentBytes"] = sum(p["Bytes"] for p in IN2["Parts"])
IN2["Budget"]["Shares"] = {"system": 10, "user": 60, "history": 20}
IN2["Budget"]["LeftoverPct"] = 10
# 端点对第 2 发报的是 127（prompt 变长是因为历史里真带了第 1 发的原话）。
# 上一版这里忘了更新，把第 1 发的 93 沿用了过来 —— 而**内核收得下那个错**：
# tokens（输入侧）与 usage_in（输出侧）是同一个物理量的两格，差 34 也没人拦。
# 那条缺陷已经记进 contract.md 第 4 节与报告，这里补上正确的数，不再靠重复跑错来留证据。
if "prompt_tokens" in u2:
    IN2["Tokens"] = m(int(u2["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")

OUT2 = json.loads(json.dumps(OUT1))
OUT2.update({"Call": 2,
             "Finish": {"Reason": c2.get("finish_reason") or "", "Said": bool(c2.get("finish_reason"))},
             "Model": r2.get("model") or "", "ModelSaid": bool(r2.get("model")),
             "Says": {"Bytes": len(t2.encode("utf-8")), "Kept": len(t2.encode("utf-8")),
                      "SHA256": __import__("hashlib").sha256(t2.encode("utf-8")).hexdigest()},
             "WireBytesIn": m(n_in2, MEASURED), "WireBytesOut": m(n_out2, MEASURED)})
if "prompt_tokens" in u2:
    OUT2["UsageIn"] = m(int(u2["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
if "completion_tokens" in u2:
    OUT2["UsageOut"] = m(int(u2["completion_tokens"]), RELAY, "端点响应的 usage.completion_tokens")

rec2, _, _ = K.call("kernel.record_call", {"session": "agnes", "actor": "llm_probe",
                                           "input": IN2, "output": OUT2})
check("03 第二发落账，链上 %d 条" % (2 * NC), rec2, ["整发落账 %d 条" % NC, "链上共 %d 条" % (2 * NC)])

# 读侧：这两发的 usage 两格现在是什么出处？（这是缺口第 8 条要的那个「生产调用方」动作）
rb, _, _ = K.call("kernel.read_back", {"session": "agnes", "call": 1, "side": "output"})
io.open(os.path.join(os.environ.get("TEMP", "/tmp"), "readback1.txt"), "w", encoding="utf-8").write(rb)
check("04 读回第 1 发输出侧，usage 两格不再是缺席", rb, ["usage_in", "relayed"],
      ["usage_in            absent", "usage_out           absent"])

ta, _, _ = K.call("kernel.tally")
check("05 合计里 relayed 这一桶第一次有真来源", ta, ["账 %d 条" % (2 * NC)])
note("tally：%s" % ta.split("\n")[0])

vf, _, _ = K.call("kernel.verify")
check("06 两发之后链自洽", vf, ["✓ 链自洽（共 %d 条，其中 %d 条比对过原像）" % (2 * NC, 2 * NC)])

ej, _, _ = K.call("kernel.encode_journal", {"payload": "included"})
book = ej.split("\n", 1)[1]
io.open(LEDGER, "w", encoding="utf-8", newline="\n").write(book)
check("07 交出去的字节里没有 key 的影子", book, ["agnes"], [KEY[:12]])
note("账本落在临时目录：%s（%d 字节，94 行）" % (LEDGER, len(book.encode("utf-8"))))

# 重启一次：从盘上读回，再记第 1 发必须被拒。
ld, _, _ = K.call("kernel.load_journal", {"text": book, "acknowledge": "replace"})
again, _, _ = K.call("kernel.record_call", {"session": "agnes", "actor": "llm_probe",
                                            "input": IN1, "output": OUT1})
check("08 读回后再记第 1 发被拒", ld + again, ["读回 %d 条" % (2 * NC), "整发拒收", "已经记过了"])

K.close()

fails = check.red
note("\n===== 判定 =====")
for name, st, why in check.rows:
    note("%s  %s  %s" % (st, name, why))
with io.open(REPORT, "w", encoding="utf-8") as f:
    f.write("\n".join(log) + "\n")
print("LLM PROBE status=%s checks=%d red=%d" % ("OK" if not fails else "RED", len(check.rows), len(fails)))
for name, st, why in fails:
    print("  RED %s | %s" % (name, why.encode("ascii", "replace").decode("ascii")))
sys.exit(0 if not fails else 1)
