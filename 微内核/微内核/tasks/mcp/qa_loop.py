# -*- coding: utf-8 -*-
# qa_loop.py —— 出几道文本题让 agnes-3.0-flash 答，每一发都经挂载好的内核落账。
#
# 这一支要拿的东西有四样，都不是「再调一次端点」能给的：
#   1. 真多轮：第 N 发的历史里带着前 N-1 发的原话，并且**从账本上回头验它抄没抄对**（读侧被用来做判定）；
#   2. 流式那一支：chunks / first_byte_ms / max_gap_ms 三格第一次拿到**对话端点**的真数
#      （Deep Research 那个端点给过事件块数，但不是这一路的帧）；
#   3. finish_reason 的非 stop 形状：故意要一段 200 字的回答却只给 8 个 token 的额度；
#   4. 429：限流是真会发生的（这一版第一次跑就撞上了），而**撞上了该怎么记账**比它发生本身重要 ——
#      崩掉是一个框架最坏的写法：它把「这一发发过、被拒了、要等多久」三件事全丢了。
#      现在它记成一发 http 故障：retry_after_ms 有 Retry-After 就转述、没有就如实缺席，
#      然后按那个间隔重发**新的一发**（发号不覆盖被拒那一发）。
#
# key 只从环境变量读，不进仓库、不进账本。控制台只打 ASCII，全文落 报告/答题循环.txt。
import hashlib
import io
import json
import os
import sys
import time
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client import INPUT1, Judge, Kernel, m, missing

BASE = os.environ.get("AGNES_BASE", "https://api.agnes-ai.cn/v1")
MODEL = os.environ.get("AGNES_MODEL", "agnes-3.0-flash")
KEY = os.environ.get("AGNES_KEY", "")
if not KEY:
    raise SystemExit("NO KEY IN ENV (set AGNES_KEY)")

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
# 路径可以外用：拿本地假端点做管线自检时，别让假账覆盖了真账。
REPORT = os.environ.get("QA_REPORT", os.path.join(ROOT, "报告", "答题循环.txt"))
BOOK = os.environ.get("QA_BOOK", os.path.join(ROOT, "报告", "样例-答题循环.jsonl"))

MEAS, RELAY = 1, 2
SYSTEM = "你在接受一组文本题测试。按题面要求回答，不要反问，不要加解释。"

QUESTIONS = [
    ("q1", "一句话解释什么叫仅追加日志（append-only log）。", 64, False),
    ("q2", "哈希链能证明什么、不能证明什么？分两点，每点不超过 20 字。", 64, False),
    ("q3", "把这句翻成英文，只给译文：红不了的承诺是散文。", 48, False),
    ("q4", "写一段 200 字的话讲仅追加日志的好处，不要提前收尾。", 8, False),
    ("q5", "把你在第三题给出的译文原样再抄一遍，只给译文。", 48, False),
    ("q6", "用两三句话解释：为什么「没量到」不能写成 0。", 96, True),
]
MAX_ATTEMPTS = 3      # 同一道题最多发三发（每发都是独立一发，各自有号）
BACKOFF_MS = 20000    # 端点没说等多久时，自己定的间隔

log = []
note = log.append
check = Judge()

ANSWER = {}     # qid -> 成功那一发的原话，供事后逐题核对
HISTORY = []    # 真的带进下一发的问答对（只有成功的那一发才进历史）
TURNS = []      # (发号, qid, 输入侧账, 输出侧账, 计量)


def ask(content, max_tokens, stream):
    body = {"model": MODEL, "messages": [{"role": "system", "content": SYSTEM}] + HISTORY +
            [{"role": "user", "content": content}], "max_tokens": max_tokens, "stream": stream}
    if stream:
        # 不带这一项，端点的流式响应**根本不吐 usage 帧**（实测：带了才有那一块）。
        body["stream_options"] = {"include_usage": True}
    raw_body = json.dumps(body, ensure_ascii=False).encode("utf-8")

    def req_of():
        rq = urllib.request.Request(BASE + "/chat/completions", data=raw_body, method="POST")
        rq.add_header("Authorization", "Bearer " + KEY)
        rq.add_header("Content-Type", "application/json")
        if stream:
            rq.add_header("Accept", "text/event-stream")
        return rq

    fail = dict(sent=len(raw_body), got=0, total_ms=0, first_ms=0, max_gap_ms=0, frames=0,
                text="", finish="", model="", usage={}, http_error=True, detail="", retry_after_ms=None)
    t0 = time.monotonic()
    try:
        r = urllib.request.urlopen(req_of(), timeout=120)
    except urllib.error.HTTPError as e:
        # 429/5xx 也是一发**发生过的**请求：它过了网线、有状态码、也许带 Retry-After。
        # 这一支把它读成一次计量，而不是一个异常。
        err = e.read() if hasattr(e, "read") else b""
        ra = e.headers.get("Retry-After") if e.headers else None
        retry_ms = None
        if ra:
            try:
                retry_ms = int(float(ra) * 1000)   # 只认「秒」这一种写法（HTTP-date 这轮没遇到）
            except ValueError:
                retry_ms = None
        return dict(fail, status=e.code, got=len(err),
                    total_ms=int((time.monotonic() - t0) * 1000),
                    detail=(err.decode("utf-8", "replace")[:200]).strip())
    except Exception as e:
        return dict(fail, status=0, transport=type(e).__name__,
                    total_ms=int((time.monotonic() - t0) * 1000),
                    detail=("%s: %s" % (type(e).__name__, str(e)))[:200].strip())

    with r:
        status = r.getcode()
        if not stream:
            raw = r.read()
            total = int((time.monotonic() - t0) * 1000)
            j = json.loads(raw.decode("utf-8"))
            ch = j["choices"][0]
            return dict(status=status, sent=len(raw_body), got=len(raw), total_ms=total,
                        first_ms=total, max_gap_ms=total, frames=1,
                        text=ch["message"]["content"] or "", finish=ch.get("finish_reason") or "",
                        model=j.get("model") or "", usage=j.get("usage") or {},
                        http_error=False, detail="", retry_after_ms=None)
        buf, deltas, gaps = b"", [], []
        last, first = None, None
        while True:
            chunk = r.read(512)
            if not chunk:
                break
            now = time.monotonic()
            if first is None:
                first = now
            elif last is not None:
                gaps.append(int((now - last) * 1000))
            last = now
            buf += chunk
    # 分帧按**空行**数，不按 read 的次数数：一次 read(512) 可能跨来三帧，也可能只来半帧。
    # （实测这个端点每帧都以 \n\n 结尾，收尾帧与 usage 帧各占一块，data: [DONE] 单独一块。）
    blocks = [b for b in buf.split(b"\n\n") if b.strip()]
    finish, usage, model = "", {}, ""
    for b in blocks:
        for line in b.split(b"\n"):
            if not line.startswith(b"data: ") or line == b"data: [DONE]":
                continue
            try:
                d = json.loads(line[6:].decode("utf-8"))
            except Exception:
                continue
            if d.get("model"):
                model = d["model"]
            if d.get("usage"):
                usage = d["usage"]
            for c in d.get("choices") or []:
                piece = (c.get("delta") or {}).get("content")
                if piece:
                    deltas.append(piece)
                if c.get("finish_reason"):
                    finish = c["finish_reason"]
    return dict(status=status, sent=len(raw_body), got=len(buf),
                total_ms=int((time.monotonic() - t0) * 1000),
                first_ms=int((first - t0) * 1000) if first else 0,
                max_gap_ms=max(gaps) if gaps else 0, frames=len(blocks),
                text="".join(deltas), finish=finish, model=model, usage=usage,
                http_error=False, detail="", retry_after_ms=None)


def build_meta(call_no, qtext, t, stream, prior):
    """把一发计量装配成两侧账。`prior` 是之前**成功的**问答对（被拒那一发不进历史）。"""
    sbytes = len(SYSTEM.encode("utf-8"))
    ubytes = len(qtext.encode("utf-8"))
    pbytes = sum(len(q.encode("utf-8")) for q, _ in prior)
    hbytes = sum(len(a.encode("utf-8")) for _, a in prior)
    # 发送顺序表按**每一段**列一项（顺序就是花钱顺序），段表按**类**聚合，两数必须同源。
    order = ["system"]
    for _ in prior:
        order += ["user", "history"]
    order.append("user")
    parts = [{"Kind": "system", "Parts": 1, "Bytes": sbytes}]
    # user 一类只许一行（两行会被数两遍）：本轮的问题与历史里的问题并成同一类的一行
    parts.append({"Kind": "user", "Parts": len(prior) + 1, "Bytes": pbytes + ubytes})
    if prior:
        parts.append({"Kind": "history", "Parts": len(prior), "Bytes": hbytes})
    total = sbytes + pbytes + hbytes + ubytes
    shares = {p["Kind"]: max(5, int(round(100.0 * p["Bytes"] / max(1, total)))) for p in parts}
    if sum(shares.values()) > 100:
        scale = 100.0 / sum(shares.values())
        shares = {k: max(1, int(v * scale)) for k, v in shares.items()}
        shares[parts[0]["Kind"]] += 100 - sum(shares.values())

    in_meta = json.loads(json.dumps(INPUT1))
    in_meta.update({"Call": call_no, "Order": order, "Parts": parts, "ContentBytes": total})
    in_meta["Budget"]["Shares"] = shares
    in_meta["Budget"]["LeftoverPct"] = 100 - sum(shares.values())
    if "prompt_tokens" in t["usage"]:
        in_meta["Tokens"] = m(int(t["usage"]["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
    in_meta["Notice"] = {"Sent": False, "Trimmed": False,
                         "Why": "这一发没折叠、没裁尾、没整段丢弃；被截的是**输出**，由 finish_reason 那一格记",
                         "Text": ""}

    nb = len(t["text"].encode("utf-8"))
    if t["http_error"]:
        kind = "http" if t["status"] else "transport"
        detail = t["detail"] or ("状态 %d" % t["status"] if t["status"] else "管路没通")
        failure = {"Kind": kind, "Detail": detail,
                   "Status": (m(t["status"], MEAS) if t["status"]
                              else missing("管路没通，压根没收到对端响应 —— 不是「状态码量到 0」")),
                   "RetryAfterMs": m(t["retry_after_ms"], RELAY, "响应的 Retry-After 头（秒换算成毫秒）")
                       if t["retry_after_ms"] is not None
                       else missing("端点没给 Retry-After，也没说要等多久 —— 不是「等 0 毫秒」"),
                   "Downgrade": False}
    else:
        failure = {"Kind": "", "Detail": "",
                   "Status": missing("这一发没故障，也就没有要记的状态码"),
                   "RetryAfterMs": missing("这一发没断，也就没有要等多久"), "Downgrade": False}

    streaming = stream and not t["http_error"]
    out = {"Call": call_no, "Streaming": streaming,
           "Finish": {"Reason": t["finish"], "Said": t["finish"] != ""},
           "Model": t["model"], "ModelSaid": t["model"] != "",
           "WireFormat": "", "WireFormatSaid": False,
           "Says": {"Bytes": nb, "Kept": nb, "SHA256": hashlib.sha256(t["text"].encode("utf-8")).hexdigest()},
           "UsageIn": m(int(t["usage"]["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
               if "prompt_tokens" in t["usage"] else missing("这一发的响应里没有 usage.prompt_tokens"),
           "UsageOut": m(int(t["usage"]["completion_tokens"]), RELAY, "端点响应的 usage.completion_tokens")
               if "completion_tokens" in t["usage"] else missing("这一发的响应里没有 usage.completion_tokens"),
           "WireBytesOut": m(t["sent"], MEAS),
           "WireBytesIn": m(t["got"], MEAS),
           "ToolCalls": 0, "Malformed": 0, "MalformedStreak": 0,
           "Failure": failure}
    if streaming:
        out["Chunks"] = m(t["frames"], MEAS)
        out["FirstByteMs"] = m(t["first_ms"], MEAS)
        out["MaxGapMs"] = m(t["max_gap_ms"], MEAS)
    else:
        why = "这一发被拒在收到内容之前，结构上没有分帧" if t["http_error"] else "这一发不是流式的，结构上没有分帧"
        out["Chunks"] = missing(why)
        out["FirstByteMs"] = missing(why.replace("分帧", "首帧这件事"))
        out["MaxGapMs"] = missing(why.replace("分帧", "帧间"))
    return in_meta, out


K = Kernel("qa", sink=note)
# 一发几条从内核的表里读，不抄在这里（表长了两位时，红的该是我的抄数）。
import re as _re
_ct0, _, _ = K.call("kernel.celltable")
_m0 = _re.search(r"当前 (\d+) 格", _ct0 or "")
NC = int(_m0.group(1)) if _m0 else 49
call_no = 0
pending = [(qid, qtext, mx, stream, 0) for qid, qtext, mx, stream in QUESTIONS]
while pending:
    qid, qtext, mx, stream, tries = pending.pop(0)
    call_no += 1
    prior = [(HISTORY[i]["content"], HISTORY[i + 1]["content"]) for i in range(0, len(HISTORY), 2)]
    t = ask(qtext, mx, stream)
    in_meta, out = build_meta(call_no, qtext, t, stream, prior)
    TURNS.append((call_no, qid, in_meta, out, t))
    note("== 第 %d 发 %s stream=%s HTTP=%s 发=%d 收=%d 首帧=%dms 最大帧间=%dms 帧数=%d finish=%r usage=%s"
         % (call_no, qid, stream, t["status"], t["sent"], t["got"], t["first_ms"],
            t["max_gap_ms"], t["frames"], t["finish"], t["usage"]))
    note("   题：%s" % qtext)
    note("   答（%d 字节）：%s" % (len(t["text"].encode("utf-8")), t["text"].replace("\n", " ")[:220]))
    if t["http_error"]:
        note("   这一发是**故障发**：kind=%s status=%s retry_after=%s 说法=%r"
             % (out["Failure"]["Kind"],
                "缺席" if out["Failure"]["Status"]["Absent"] else out["Failure"]["Status"]["N"],
                "缺席" if out["Failure"]["RetryAfterMs"]["Absent"] else out["Failure"]["RetryAfterMs"]["N"],
                out["Failure"]["Detail"][:140]))
        if tries + 1 < MAX_ATTEMPTS:
            wait_ms = t["retry_after_ms"] if t["retry_after_ms"] else BACKOFF_MS
            note("   按账上那一格等 %d 毫秒再发下一发（重试是**新的一发**，不覆盖被拒那一发）" % wait_ms)
            time.sleep(min(wait_ms, 120000) / 1000.0)
            pending.insert(0, (qid, qtext, mx, stream, tries + 1))
        continue
    HISTORY = HISTORY + [{"role": "user", "content": qtext}, {"role": "assistant", "content": t["text"]}]
    ANSWER[qid] = t["text"]

# 量到就先落盘：这一支上一次跑是在判定阶段崩的，几发真调用全丢了。
io.open(os.path.join(os.environ.get("TEMP", "/tmp"), "qa-raw.json"), "w", encoding="utf-8").write(
    json.dumps({str(n): {k: v for k, v in t.items() if k != "raw"} for n, _, _, _, t in TURNS},
               ensure_ascii=False, indent=1))

# ---- 逐发过门口 ----
N = len(TURNS)
for n, qid, in_meta, out, _ in TURNS:
    rec, iserr, _ = K.call("kernel.record_call", {"session": "qa", "actor": "qa_loop",
                                                  "input": in_meta, "output": out})
    if iserr:
        note("!! 第 %d 发（%s）被拒：%s" % (n, qid, rec))
    check("第 %d 发 %s 落账通过" % (n, qid), rec, ["整发落账 %d 条" % NC])

ta, _, _ = K.call("kernel.tally")
note("tally：%s" % ta.split("\n")[0])
vf, _, _ = K.call("kernel.verify")
check("%d 发之后链自洽" % N, vf, ["✓ 链自洽（共 %d 条，其中 %d 条比对过原像）" % (NC * N, NC * N)])


def cell_line(text, key):
    """从 read_back 那段里取某一格，返回 (出处档, 说法)。读不到就返回 ("","")。"""
    for l in text.split("\n"):
        if l.startswith(key + " "):
            f = l.split()
            if len(f) >= 2:
                return f[1], " ".join(f[2:])
    return "", ""


# (1) finish_reason 有没有非 stop 的真形状
trunc = next((n for n, _, _, o, _ in TURNS if o["Finish"]["Reason"] not in ("stop", "")), None)
if trunc:
    rb, _, _ = K.call("kernel.read_back", {"session": "qa", "call": trunc, "side": "output"})
    b, claim = cell_line(rb, "finish")
    note("第 %d 发 finish 那一格：出处=%s 说法=%s" % (trunc, b, claim))
    check("(1) 拿到一个非 stop 的收尾原因，且出处是转述（端点自己说的）", "%s %s" % (b, claim), ["relayed"])
else:
    # 这一条**不写成判定**：一个只能记「本轮没出现」的 check 永远不会红，
    # 而慢闸那一族专门盯着这种空针（判据只能报「过」的时候，它就不是判据）。
    note("(1) 未覆盖：这一轮每一发都自然答完了，没拿到非 stop 的收尾原因 —— 不算绿，也不假装的绿。")

# (2) 流式那一发的三格必须是量到的数
stream_turn = next((n for n, _, _, o, _ in TURNS if o["Streaming"]), None)
rb_s, _, _ = K.call("kernel.read_back", {"session": "qa", "call": stream_turn or 1, "side": "output"})
b6 = [(k,) + cell_line(rb_s, k) for k in ("chunks", "first_byte_ms", "max_gap_ms")]
note("流式那一发（第 %s 发）三格：%s" % (stream_turn, b6))
ok_b = stream_turn is not None and all(x[1] == "measured" for x in b6) and int(b6[0][2] or 0) >= 1
check("(2) 流式那一发量到了帧数、首帧与最大帧间（三格都是实测，帧数至少 1）",
      "OK" if ok_b else "BAD %s" % b6, ["OK"])

# (3) 故障发：429 那一发在账上是什么样
fails_turn = [(n, o) for n, _, _, o, _ in TURNS if o["Failure"]["Kind"] != ""]
if fails_turn:
    nfail = fails_turn[0][0]
    rb_f, _, _ = K.call("kernel.read_back", {"session": "qa", "call": nfail, "side": "output"})
    bk, bc = cell_line(rb_f, "fail_kind")
    rk, rc = cell_line(rb_f, "retry_after_ms")
    note("故障发第 %d 发：fail_kind=%s/%s，retry_after=%s/%s" % (nfail, bk, bc, rk, rc))
    check("(3) 真限流被记成一发故障（有类别、有状态、有说法），不是崩、也不是没发生",
          rb_f, ["http", "429"])
else:
    # 同 (1)：没撞上限流就没有真故障数据，这一条只记未覆盖，不写成一条永远不会红的判定。
    note("(3) 未覆盖：这一轮没撞上 429/5xx，故障那一支仍然只有合成数据（假端点那轮量过，见报告）。")

# (4) 读侧拿账做判定：考历史那一发抄的译文，对不对得上**账上**那条指纹
n3 = next((n for n, q, _, _, _ in TURNS if q == "q3"), None)
if n3 and "q5" in ANSWER:
    rb3, _, _ = K.call("kernel.read_back", {"session": "qa", "call": n3, "side": "output"})
    _, sha_on_book = cell_line(rb3, "model_says_sha256")
    recomputed = hashlib.sha256(ANSWER["q3"].encode("utf-8")).hexdigest()
    note("账上第 %d 发指纹=%s 本地重算=%s 相符=%s" % (n3, sha_on_book[:16], recomputed[:16], sha_on_book == recomputed))
    check("(4a) 读回来的指纹与本地重算一致（第三方按同一规则能复算）",
          "MATCH" if sha_on_book == recomputed else "MISS %s" % sha_on_book, ["MATCH"])
    t3 = ANSWER["q3"].strip().strip("“”\"").lower()
    t5 = ANSWER["q5"].strip().strip("“”\"").lower()
    check("(4b) 模型真的抄回了它上一轮的译文（历史在走，不是各发独立）",
          "SAME" if (t3 and t3 == t5) else "DIFF t3=%s t5=%s" % (t3, t5), ["SAME"])

# (5) 两格同量那个旧洞：每发的 tokens 与 usage_in 是否一致
bad_pair = [n for n, _, inm, o, _ in TURNS
            if not inm["Tokens"]["Absent"] and not o["UsageIn"]["Absent"] and inm["Tokens"]["N"] != o["UsageIn"]["N"]]
note("tokens 与 usage_in 不相等的发：%s（今天仍无判据拦这一对）" % (bad_pair or "无"))
cost_in = sum(o["UsageIn"]["N"] for _, _, _, o, _ in TURNS if not o["UsageIn"]["Absent"])
cost_out = sum(o["UsageOut"]["N"] for _, _, _, o, _ in TURNS if not o["UsageOut"]["Absent"])
note("按 usage_in 累加=%d，按 usage_out 累加=%d" % (cost_in, cost_out))

ej, _, _ = K.call("kernel.encode_journal", {"payload": "included"})
book = ej.split("\n", 1)[1]
io.open(BOOK, "w", encoding="utf-8", newline="\n").write(book)
check("账落盘且不带 key", book, ["qa"], [KEY[:10], "sk-"])
note("账本：%s（%d 条）" % (BOOK, NC * N))

# 换进程重启后再记第 1 发必须被拒
K2 = Kernel("qa2", sink=note)
ld, _, _ = K2.call("kernel.load_journal", {"text": book})
again, _, _ = K2.call("kernel.record_call", {"session": "qa", "actor": "qa_loop",
                                             "input": TURNS[0][2], "output": TURNS[0][3]})
check("换进程读回后，第 1 发再记被拒", ld + again, ["读回 %d 条" % (NC * N), "整发拒收", "已经记过了"])
K2.close()
K.close()

fails = check.red
note("\n===== 判定 =====")
for name, st, why in check.rows:
    note("%s  %s  %s" % (st, name, why))
with io.open(REPORT, "w", encoding="utf-8") as f:
    f.write("\n".join(log) + "\n")
print("QA LOOP status=%s checks=%d red=%d turns=%d tokens_in=%d tokens_out=%d fail_turns=%d" % (
    "OK" if not fails else "RED", len(check.rows), len(fails), N, cost_in, cost_out, len(fails_turn)))
for name, st, why in fails:
    print("  RED %s | %s" % (name, why.encode("ascii", "replace").decode("ascii")))
sys.exit(0 if not fails else 1)
