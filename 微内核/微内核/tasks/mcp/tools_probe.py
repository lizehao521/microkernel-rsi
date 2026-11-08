# -*- coding: utf-8 -*-
# tools_probe.py —— 让 agnes-3.0-flash 真的提议一次工具调用，每一发经挂载好的内核落账。
#
# 这一支要的是四样「再调一次文本端点」给不了的东西：
#   1. tool_calls / malformed / malformed_streak 三格：整个仓库里只有单测的合成数，没有过一次真响应；
#   2. finish_reason 的第二种非 stop 形状：tool_calls（上一轮拿到的是 length）；
#   3. toolspec 与 observation 两个段类：输入侧的段表从建好那天起没被真数填过；
#   4. 流式里的工具提议是怎么分帧的（参数是一整块来的，还是一片片拼起来的）。
#
# 「执行不了」按内核文档里那两条判据数：**没名字** 或 **arguments 解不出 JSON**。
# 不去扩它：一个编造出来的工具名不算 malformed（那是另一回事，账上没有这一格）。
# key 只从环境变量读。控制台只打 ASCII，全文落 报告/工具提议.txt。
import hashlib
import io
import json
import os
import re
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
REPORT = os.environ.get("TOOLS_REPORT", os.path.join(ROOT, "报告", "工具提议.txt"))
BOOK = os.environ.get("TOOLS_BOOK", os.path.join(ROOT, "报告", "样例-工具提议.jsonl"))

MEAS, RELAY = 1, 2
SYSTEM = "你在一个会计系统里。凡是要算数，必须调用 ledger_add 工具，禁止心算。不要用散文回答。"
TOOLS = [{"type": "function", "function": {
    "name": "ledger_add",
    "description": "把两个整数相加，返回它们的和。任何加法都必须走这个工具。",
    "parameters": {"type": "object", "properties": {
        "a": {"type": "integer", "description": "第一个加数"},
        "b": {"type": "integer", "description": "第二个加数"}},
        "required": ["a", "b"]}}}]
TOOLSPEC = json.dumps(TOOLS, ensure_ascii=False)

# 四发：正常提议 → 把工具结果喂回去收尾 → 掐死额度逼出歪提议 → 流式里看提议怎么分帧。
TURNS = [
    ("t1", "用 ledger_add 算 27 加 15。", 64, False),
    ("t2", "工具已经返回了，把结论写成一句话。", 96, False),
    ("t3", "再用 ledger_add 算 1234 加 5678。", 6, False),
    ("t4", "最后用 ledger_add 算 9 加 1，一个字的解释都不要。", 64, True),
    ("t5", "只用一个词回答：2+2 等于几？不要调工具。", 4, False),
]
# 本机自检的另一个旋钮：拿假端点单独逼出 protocol 那一档（200 却给了不成话的 body）。
# 默认不设这个环境变量时，上面五发一字不动 —— 真账的形状不许被自检改掉。
if os.environ.get("PROTO_TURN"):
    TURNS = [("p1", "假端点：回 200 但 body 是断的。", 5, False)]
OBSERVATION = json.dumps({"sum": 42}, ensure_ascii=False)
# 挤掉一段必须让模型也知道（规矩：裁过却不通知、也不说为什么不通知，内核直接拒）。
# 这句话是**真发进请求体**的一段，段类 truncation-notice，不是只写在账上。
NOTICE = ("上一轮模型给出的工具提议里有一个参数不完整，那一条我们没能带进这一轮的历史；"
          "如果你需要它，请重新提议。")
# 挤尾与整段没进是两件事，通知得分开写（规矩：裁过却不通知、也不说为什么，内核整发拒）
TAIL_NOTICE = ("上一轮模型那句回答我们只带了前缀进这一轮，尾巴被预算挤掉了；"
               "若要完整的那句，请重新说一次。")
BOTH_NOTICE = ("上一轮里有一句整段没带进这一轮（参数解不出 JSON），还有一句被挤掉了尾巴只带了前缀；"
               "两件事都记在这轮的裁剪表上，需要完整内容请重新说。")


def pick_notice(kinds):
    """挤法 → 通知正文，只这一份定义。装配与判定都调它 —— 各写一遍就会漂成两套期望值。"""
    if len(kinds) > 1:
        return BOTH_NOTICE
    return TAIL_NOTICE if "tail-dropped" in kinds else NOTICE
FOLDED = []   # [(第几发模型说的, 原话, 发出去的前缀, 原段, 裁后段)] —— 判 (12) 用它跟账对
FOLD_DONE = set()  # 已经挤过一手的段文本：同一段不许被级联着挤第二次（内核会整发拒收）

log = []
note = log.append
check = Judge()

MSGS = []      # 真的带进下一发的 messages（不含 system，system 每发现搭）
SEGS = []      # 与 MSGS 同源的段表 [(kind, text)]，ContentBytes 就从这里加
HIST_LINKS = []  # [(第 N 发的号, 真发进下一发历史的那句模型话)] —— 走私测试用它跟账上指纹对


def post(body):
    raw_body = json.dumps(body, ensure_ascii=False).encode("utf-8")
    stream = bool(body.get("stream"))

    def req_of():
        rq = urllib.request.Request(BASE + "/chat/completions", data=raw_body, method="POST")
        rq.add_header("Authorization", "Bearer " + KEY)
        rq.add_header("Content-Type", "application/json")
        if stream:
            rq.add_header("Accept", "text/event-stream")
        return rq

    t0 = time.monotonic()
    try:
        r = urllib.request.urlopen(req_of(), timeout=120)
    except urllib.error.HTTPError as e:
        err = e.read() if hasattr(e, "read") else b""
        ra = e.headers.get("Retry-After") if e.headers else None
        retry_ms = None
        if ra:
            try:
                retry_ms = int(float(ra) * 1000)
            except ValueError:
                retry_ms = None
        return dict(http_error=True, status=e.code, sent=len(raw_body), got=len(err),
                    total_ms=int((time.monotonic() - t0) * 1000), first_ms=0, max_gap_ms=0,
                    frames=0, text="", finish="", model="", usage={}, calls=[],
                    detail=err.decode("utf-8", "replace")[:200].strip(), retry_after_ms=retry_ms)
    except Exception as e:
        return dict(http_error=True, status=0, sent=len(raw_body), got=0,
                    total_ms=int((time.monotonic() - t0) * 1000), first_ms=0, max_gap_ms=0,
                    frames=0, text="", finish="", model="", usage={}, calls=[],
                    detail=("%s: %s" % (type(e).__name__, str(e)))[:200].strip(), retry_after_ms=None)

    with r:
        status = r.getcode()
        if not stream:
            raw = r.read()
            total = int((time.monotonic() - t0) * 1000)
            try:
                j = json.loads(raw.decode("utf-8"))
                ch = j["choices"][0]
                msg = ch["message"]
            except (ValueError, KeyError, TypeError, IndexError, UnicodeDecodeError) as e:
                # HTTP 200、连接成了、字节也给了，话却不成话 —— 这是 protocol 那一档的真形状。
                # 此前这一支直接 traceback 出去（json.loads 裸调），所以内核表里的 protocol
                # 在任何调用方都**产不出来**：不是没开格，是没有采集路径。
                # Detail 只放**对端的原字节**（这一发有状态码，出处按规矩跟着 status 走 relayed，
                # 混进我们自己的判定句就等于把结论标成对端说的）。
                return dict(http_error=False, proto_broken=True, status=status,
                            sent=len(raw_body), got=len(raw),
                            total_ms=total, first_ms=total, max_gap_ms=total, frames=1,
                            text="", finish="", model="", usage={}, calls=[], obj="",
                            detail=raw.decode("utf-8", "replace")[:200],
                            why_unparsed=("%s" % e)[:120], retry_after_ms=None)
            return dict(http_error=False, status=status, sent=len(raw_body), got=len(raw),
                        total_ms=total, first_ms=total, max_gap_ms=total, frames=1,
                        text=msg.get("content") or "", finish=ch.get("finish_reason") or "",
                        model=j.get("model") or "", usage=j.get("usage") or {},
                        obj=j.get("object") or "",
                        calls=msg.get("tool_calls") or [], detail="", retry_after_ms=None)
        buf, deltas, gaps = b"", [], []
        obj_first = ""
        last, first = None, None
        tc_frames = 0            # 带工具提议碎片的帧数（这一支本轮新要的形状）
        tc_pieces = 0            # 收到的 delta.tool_calls 项数
        arg_chars = 0            # 拼起来的参数字符数
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
        blocks = [b for b in buf.split(b"\n\n") if b.strip()]
        finish, usage, model, calls = "", {}, "", []
        for b in blocks:
            saw_tc = False
            for line in b.split(b"\n"):
                if not line.startswith(b"data: ") or line == b"data: [DONE]":
                    continue
                try:
                    d = json.loads(line[6:].decode("utf-8"))
                except Exception:
                    continue
                if d.get("model"):
                    model = d["model"]
                if d.get("object") and not obj_first:
                    obj_first = d["object"]
                if d.get("usage"):
                    usage = d["usage"]
                for c in d.get("choices") or []:
                    dl = c.get("delta") or {}
                    if dl.get("content"):
                        deltas.append(dl["content"])
                    for tc in dl.get("tool_calls") or []:
                        saw_tc = True
                        tc_pieces += 1
                        arg_chars += len(((tc.get("function") or {}).get("arguments")) or "")
                        i = tc.get("index", len(calls))
                        while len(calls) <= i:
                            calls.append({"id": "", "type": "function",
                                          "function": {"name": "", "arguments": ""}})
                        if tc.get("id"):
                            calls[i]["id"] = tc["id"]
                        f = tc.get("function") or {}
                        calls[i]["function"]["name"] += f.get("name") or ""
                        calls[i]["function"]["arguments"] += f.get("arguments") or ""
                    if c.get("finish_reason"):
                        finish = c["finish_reason"]
            if saw_tc:
                tc_frames += 1
        return dict(http_error=False, status=status, sent=len(raw_body), got=len(buf),
                    total_ms=int((time.monotonic() - t0) * 1000),
                    first_ms=int((first - t0) * 1000) if first else 0,
                    max_gap_ms=max(gaps) if gaps else 0, frames=len(blocks),
                    text="".join(deltas), finish=finish, model=model, usage=usage,
                    obj=obj_first, calls=calls, tc_frames=tc_frames, tc_pieces=tc_pieces,
                    arg_chars=arg_chars, detail="", retry_after_ms=None)


def count_malformed(calls):
    """按内核文档里那两条判据数：没名字，或 arguments 解不出 JSON（空串也解不出）。"""
    bad = []
    for i, tc in enumerate(calls):
        fn = tc.get("function") or {}
        name = (fn.get("name") or "").strip()
        args = fn.get("arguments")
        if not isinstance(args, str):
            args = "" if args is None else json.dumps(args, ensure_ascii=False)
        if not name:
            bad.append("第 %d 个提议没有名字（参数 %r）" % (i + 1, args[:40]))
            continue
        try:
            json.loads(args)
        except Exception:
            bad.append("第 %d 个提议（%s）参数解不出 JSON：%r" % (i + 1, name, args[:40]))
    return bad


def meta_of(t, kind_segs, streak_prev, cuts=None):
    """把一发计量装配成两侧账。
    kind_segs 是**这一发真发出去的段**，顺序就是发送顺序；
    cuts 是**这一发没带进去**的那些段（发生在上一发的提议里，落账落在omit它的这一发）。"""
    cuts = cuts or []
    order = [k for k, _ in kind_segs]
    totals, seen = [], []
    for k, txt in kind_segs:
        if k in seen:
            for q in totals:
                if q["Kind"] == k:
                    q["Parts"] += 1
                    q["Bytes"] += len(txt.encode("utf-8"))
        else:
            seen.append(k)
            totals.append({"Kind": k, "Parts": 1, "Bytes": len(txt.encode("utf-8"))})
    total = sum(len(txt.encode("utf-8")) for _, txt in kind_segs)
    shares = {p["Kind"]: max(4, int(round(100.0 * p["Bytes"] / max(1, total)))) for p in totals}
    if sum(shares.values()) > 100:
        scale = 100.0 / sum(shares.values())
        shares = {k: max(1, int(v * scale)) for k, v in shares.items()}
        big = max(shares, key=lambda k: shares[k])
        shares[big] = max(1, shares[big] + 100 - sum(shares.values()))

    in_meta = json.loads(json.dumps(INPUT1))
    in_meta.update({"Call": t["call"], "Order": order, "Parts": totals, "ContentBytes": total})
    in_meta["Budget"]["Shares"] = shares
    in_meta["Budget"]["LeftoverPct"] = 100 - sum(shares.values())
    if "prompt_tokens" in t["usage"]:
        in_meta["Tokens"] = m(int(t["usage"]["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
    in_meta["Cuts"] = cuts
    # 通知这一格改成**从"真发出去的段"里读**，不再由裁剪表单方面宣称。
    # 上一版是 `if cuts: Sent=True`，于是我写出过"账上说发了、段里没有"的那一发（内核照收，缺口 14 的实例）。
    sent = [txt for k, txt in kind_segs if k == "truncation-notice"]
    # Sent 只由**这一发真发出去的段**决定，不由"这一发有没有裁剪"决定：
    # 那句通知一旦进了历史就跟着后面的每一发一起发出去 —— 说"这一发没发通知"是假的。
    # （内核第④条正是这么把它抓住的：段里有通知而账说没发 ⇒ 整发拒收。）
    if sent and cuts:
        ks = {c.get("Squeeze") for c in cuts}
        why = ("上一发那句回答被挤掉尾巴（留前缀发出去），这一发历史里它不再等于原话"
               if ks == {"tail-dropped"} else
               ("上一发既有一句整段没带进来、又有一句被挤掉尾巴，两件事都在这轮的裁剪表上"
                if len(ks) > 1 else
                "上一发的提议里有一个参数解不出 JSON，那条 assistant 消息整段没带进本轮历史"))
    elif sent:
        why = "这一发自己没挤任何东西；那段知情通知是之前那轮挤过之后留在历史里的，它仍随这一发发出去"
    elif cuts:
        why = "有裁剪记录，但这一发真发出去的段里没有通知段（发不出去就不算通知过）"
    else:
        why = "这一发没折叠、没裁尾、没整段丢弃；被截的是**输出**，由 finish_reason 那一格记"
    in_meta["Notice"] = {"Sent": bool(sent), "Trimmed": bool(cuts) and bool(sent),
                         "Why": why, "Text": sent[-1] if sent else ""}

    bad = t["bad"]
    nb = len(t["text"].encode("utf-8"))
    if t["http_error"]:
        # 「状态码 → 故障类别」的映射住在调用方。400/401/403/404/409/422 是 config
        # （密钥错、模型名不存在、参数非法），408/425/429/5xx 才是 http（限流/服务端）。
        # 分错的代价是实的：内核里 FailHTTP.RetrySame()=true、FailConfig=false，
        # 把 400 记成 http 等于在账上写「原样再发一次有用」，而它只会再吃一次同样的 400。
        kind = ("config" if t["status"] in (400, 401, 403, 404, 409, 422)
                else ("http" if t["status"] else "transport"))
        status = (m(t["status"], MEAS) if t["status"]
                  else missing("管路没通，压根没收到对端响应 —— 不是「状态码量到 0」"))
        detail = t["detail"] or ("状态 %d" % t["status"] if t["status"] else "管路没通")
        # 这里原来有一段绕行：内核不许 config 带状态码，于是把真 401 写进我们自己构造的说法、
        # 码归 0、对端原话留在调用方日志不进账。判据换成「收到过对端响应」那根轴之后删掉了 ——
        # 现在码照实记，原话照实抄，出处由内核那一格自己标成转述（见 报告/假key一发-401.md）。
        failure = {"Kind": kind, "Detail": detail.strip()[:200], "Status": status,
                   "RetryAfterMs": m(t["retry_after_ms"], RELAY, "响应的 Retry-After 头（秒换算成毫秒）")
                       if t["retry_after_ms"] is not None
                       else missing("端点没给 Retry-After，也没说要等多久 —— 不是「等 0 毫秒」"),
                   "Downgrade": False}
    elif t.get("proto_broken"):
        # 对端回了话（有状态码、有字节），只是我们解不出这套格式约定的形状。
        # 旧规矩下这里有两个装法（照实记被整发拒收 / 让步记写下 measured 0），
        # 那是一段实测记录，见 报告/协议档一发-200坏body.md；轴表落地后只剩这一种：码照实记，原话照实抄。
        failure = {"Kind": "protocol", "Detail": t["detail"].strip(), "Status": m(t["status"], MEAS),
                   "RetryAfterMs": missing("对端没给 Retry-After，它回的是一个解不出来的 body"),
                   "Downgrade": False}
    elif bad:
        failure = {"Kind": "malformed_action", "Detail": "；".join(bad)[:200],
                   "Status": m(t["status"] or 200, MEAS),
                   "RetryAfterMs": missing("原样重发不会变好：歪的是这一发的提议本身"),
                   "Downgrade": False}
    elif nb == 0 and not t["calls"]:
        # 端点回了 200，却一个字没吐、一个提议也没有。这一发确实发生过（有字节、有 usage、有收尾原因），
        # 但它没做成任何动作。内核明写拒「原话 0 字节、0 个提议却记成正常收尾」——
        # 那是它在替我兜谎，放宽它的不是办法；按「动作执行不了」记，说法里只放量到的数，不猜它为什么空。
        failure = {"Kind": "malformed_action",
                   "Detail": ("端点回了 %d 字节、finish=%r、原话 0 字节、0 个提议（补全 token %s）"
                              % (t["got"], t["finish"],
                                 t["usage"].get("completion_tokens", "没报")))[:200],
                   "Status": m(t["status"] or 200, MEAS),
                   "RetryAfterMs": missing("原样重发不会变好：同样的额度还会截在同样的地方"),
                   "Downgrade": False}
    else:
        failure = {"Kind": "", "Detail": "",
                   "Status": missing("这一发没故障，也就没有要记的状态码"),
                   "RetryAfterMs": missing("这一发没断，也就没有要等多久"), "Downgrade": False}

    streak = streak_prev + 1 if bad else 0
    streaming = bool(t.get("stream")) and not t["http_error"]
    out = {"Call": t["call"], "Streaming": streaming,
           "Finish": {"Reason": t["finish"], "Said": t["finish"] != ""},
           "Model": t["model"], "ModelSaid": t["model"] != "",
        "WireFormat": t.get("obj") or "", "WireFormatSaid": (t.get("obj") or "") != "",
           "Says": {"Bytes": nb, "Kept": nb,
                    "SHA256": hashlib.sha256(t["text"].encode("utf-8")).hexdigest()},
           "UsageIn": m(int(t["usage"]["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
               if "prompt_tokens" in t["usage"] else missing("这一发的响应里没有 usage.prompt_tokens"),
           "UsageOut": m(int(t["usage"]["completion_tokens"]), RELAY, "端点响应的 usage.completion_tokens")
               if "completion_tokens" in t["usage"] else missing("这一发的响应里没有 usage.completion_tokens"),
           "WireBytesOut": m(t["sent"], MEAS),
           "WireBytesIn": m(t["got"], MEAS),
           "ToolCalls": len(t["calls"]), "Malformed": len(bad), "MalformedStreak": streak,
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


def cell_line(text, key):
    for l in text.split("\n"):
        if l.startswith(key + " "):
            f = l.split()
            if len(f) >= 2:
                return f[1], " ".join(f[2:])
    return "", ""


K = Kernel("tools", sink=note)
# 一发该落几条**问内核**，不抄在自己的断言里：格位表长了两位（47→49），
# 写死 47 的那些行会整片红，而红的是我的抄数、不是内核的行为。
_ct, _, _ = K.call("kernel.celltable")
_m = re.search(r"当前 (\d+) 格", _ct or "")
NCELL = int(_m.group(1)) if _m else 0
EXPECT_LAND = "整发落账 %d 条" % NCELL if NCELL else "整发落账"
streak_prev = 0
pending_cuts = []
BOOKED = []
for i, (tid, qtext, mx, stream) in enumerate(TURNS):
    msg_snapshot, seg_snapshot = len(MSGS), len(SEGS)
    cuts_this = pending_cuts
    pending_cuts = []
    if tid == "t2" and BOOKED and BOOKED[-1][1]["calls"] and not BOOKED[-1][1]["bad"]:
        # 工具结果这一条是我们造的（27+15 就是 42），observation 段的原文就是它
        tc_id = (BOOKED[-1][1]["calls"][0] or {}).get("id") or ""
        MSGS.append({"role": "tool", "tool_call_id": tc_id or "call_without_id",
                     "name": "ledger_add", "content": OBSERVATION})
        SEGS.append(("observation", OBSERVATION))

    if os.environ.get("FOLD") and not any(c.get("Squeeze") == "tail-dropped" for c in cuts_this):
        # 挤历史这一条路（input.go 的 MayShrink 明确允许 tail-dropped）此前从没真跑过：
        # 在册账里只有 dropped-whole。这一支就是把它真做一次，然后问账能不能连回去。
        idx = None
        for j in range(len(SEGS) - 1, -1, -1):
            if SEGS[j][0] != "history" or SEGS[j][1] in FOLD_DONE:
                continue
            try:
                dd = json.loads(SEGS[j][1])
            except Exception:
                continue
            if dd.get("content"):
                idx = j
                break
        if idx is not None:
            kind, raw = SEGS[idx]
            dd = json.loads(raw)
            orig_content = dd["content"]
            keep = int(os.environ.get("FOLD_KEEP", "6"))
            dd["content"] = orig_content.encode("utf-8")[:keep].decode("utf-8", "ignore")
            new_raw = json.dumps(dd, ensure_ascii=False)
            SEGS[idx] = (kind, new_raw)
            for mm in reversed(MSGS):
                if mm.get("role") == "assistant" and mm.get("content"):
                    mm["content"] = dd["content"]
                    break
            cuts_this = list(cuts_this) + [{
                "Kind": "history", "RefCall": i,
                "Ref": "第 %d 发模型那句回答（挤尾留前缀）" % (i + 1 - 1),
                "Role": "assistant", "Squeeze": "tail-dropped",
                "FromBytes": len(raw.encode("utf-8")), "ToBytes": len(new_raw.encode("utf-8")),
                "SHA256": hashlib.sha256(raw.encode("utf-8")).hexdigest()}]
            FOLDED.append((i, orig_content, dd["content"], raw, new_raw))
            # 挤过的段记在一手名单里：再挤一次就是拿前缀当前缀 —— 上一轮真跑撞的就是这个，
            # 内核那句「留了 42 字节、原文 42 字节 —— 裁尾要留下一个非空的真前缀」拒得对，改的是调用方。
            FOLD_DONE.add(new_raw)
        else:
            note("  本轮没有可挤的历史段（要么没有、要么已经挤过一手）—— 不硬挤第二次")

    if cuts_this:
        # 知情通知是**真发出去的一段**，不是只写在账上的说明 —— 所以它必须附在裁剪表定下来之后。
        # 上一版附在折叠逻辑之前：那条"因为挤尾才产生的裁剪"没有对应的段发出去，
        # 而账上 Notice.Sent 已写成"是"，内核照收 —— README 缺口 14 的第一个真实例就是这么撞出来的。
        ntext = pick_notice({c.get("Squeeze") for c in cuts_this})
        MSGS.append({"role": "user", "content": ntext})
        SEGS.append(("truncation-notice", ntext))

    segs = [("system", SYSTEM), ("toolspec", TOOLSPEC)] + SEGS + [("user", qtext)]
    MSGS.append({"role": "user", "content": qtext})
    SEGS.append(("user", qtext))

    body = {"model": MODEL,
            "messages": [{"role": "system", "content": SYSTEM}] + MSGS,
            "tools": TOOLS, "max_tokens": mx, "stream": stream}
    if stream:
        body["stream_options"] = {"include_usage": True}
    t = post(body)
    t["call"] = i + 1
    t["stream"] = stream
    t["bad"] = [] if t["http_error"] else count_malformed(t["calls"])
    t["no_history"] = False
    if t["http_error"]:
        del MSGS[msg_snapshot:], SEGS[seg_snapshot:]
        pending_cuts = cuts_this        # 被拒这一发没做成新的挤，旧的留痕跟着回滚到下一发
    else:
        # 成功这一发要真的进下一发的历史，而且**说话人不能丢**：上一版只把问题接进历史、
        # 把模型的话丢了，于是它把上一题又答了一遍（真端点量到的，见报告）。
        am = {"role": "assistant"}
        if t["text"]:
            am["content"] = t["text"]
        if t["calls"]:
            am["tool_calls"] = t["calls"]
        if len(am) > 1:
            raw = json.dumps(am, ensure_ascii=False)
            if t["bad"]:
                # 带歪提议的 assistant 消息**对端自己不肯收**（上一轮就因此吃了 400：
                # 「Assistant tool call ***.arguments must be valid JSON」）。
                # 所以整段不带进下一发，而这件事要留在**省略它的那一发**的裁剪表上。
                pending_cuts = [{"Kind": "history",
                                 "Ref": "第 %d 发的 assistant 提议（%d 个里 %d 个执行不了）"
                                        % (t["call"], len(t["calls"]), len(t["bad"])),
                                 "RefCall": t["call"],
                                 "Role": "assistant", "Squeeze": "dropped-whole",
                                 "FromBytes": len(raw.encode("utf-8")), "ToBytes": 0,
                                 "SHA256": hashlib.sha256(raw.encode("utf-8")).hexdigest()}]
                t["dropped_msg"] = raw
            else:
                if os.environ.get("SMUGGLE") and am.get("content"):
                    # 走私测试（本机假端点，不花钱）：把这句改成"模型没说过的话"再进下一发历史。
                    # 账上第 N 发那条 model_says_sha256 仍是**真话**的指纹，而下一发发出去的是假的。
                    am["content"] = am["content"].replace("42", "43")
                    raw = json.dumps(am, ensure_ascii=False)
                if am.get("content"):
                    # 记的是**真发进下一发的那句**（改过就是改过的），不是账上那句
                    HIST_LINKS.append((t["call"], am["content"]))
                MSGS.append(am)
                SEGS.append(("history", raw))
        else:
            t["no_history"] = True
    in_meta, out = meta_of(t, segs, streak_prev, cuts_this)
    streak_prev = out["MalformedStreak"]
    BOOKED.append((in_meta, t, out))
    note("== %s 第 %d 发 stream=%s HTTP=%s 发=%d 收=%d 首帧=%dms 最大帧间=%dms 帧数=%d "
         "finish=%r 提议=%d 歪=%d usage=%s"
         % (tid, t["call"], stream, t["status"], t["sent"], t["got"], t["first_ms"],
            t["max_gap_ms"], t["frames"], t["finish"], len(t["calls"]), len(t["bad"]), t["usage"]))
    note("   题：%s" % qtext)
    note("   原话（%d 字节）：%r" % (len(t["text"].encode("utf-8")), t["text"][:160]))
    if t["calls"]:
        note("   提议：%s" % json.dumps(t["calls"], ensure_ascii=False)[:300])
    if t["bad"]:
        note("   执行不了的：%s" % "；".join(t["bad"]))
    if t["http_error"]:
        note("   这一发是**故障发**：kind=%s status=%r 说法=%r" %
             (out["Failure"]["Kind"], out["Failure"]["Status"], out["Failure"]["Detail"][:160]))
    elif out["Failure"]["Kind"] == "malformed_action" and not t["bad"]:
        note("   这一发**空手回来**（不是被拒）：kind=%s 说法=%r" %
             (out["Failure"]["Kind"], out["Failure"]["Detail"][:160]))
    if stream:
        note("   流式里的提议：带 tool_calls 碎片的帧=%s 项=%s 拼起来的参数长=%s"
             % (t.get("tc_frames"), t.get("tc_pieces"), t.get("arg_chars")))
    if t.get("dropped_msg"):
        note("   那条 assistant 消息整段不带进下一发（%d 字节，原文指纹 %s）；"
             "省略它这件事落在**下一发**的裁剪表上"
             % (len(t["dropped_msg"].encode("utf-8")),
                hashlib.sha256(t["dropped_msg"].encode("utf-8")).hexdigest()[:16]))
    if t["no_history"]:
        note("   这一发空手回来，下一发的历史里没有它的 assistant 消息（不是漏搭，是无内容可放）")
    if t["http_error"]:
        note("   这一发之后不再往下走：t2 要吃 t1 的提议、t3/t4 的历史要接得上，"
             "中间断一发后面就全是假形状。这一发本身已落账。")
        break

dump = {str(n): {k: v for k, v in t.items() if k != "bad"} for n, (_, t, _) in enumerate(BOOKED, 1)}
io.open(os.path.join(os.environ.get("TEMP", "/tmp"), "tools-raw.json"), "w", encoding="utf-8").write(
    json.dumps(dump, ensure_ascii=False, indent=1))

N = len(BOOKED)
for n, (in_meta, t, out) in enumerate(BOOKED, 1):
    rec, iserr, _ = K.call("kernel.record_call", {"session": "tools", "actor": "tools_probe",
                                                  "input": in_meta, "output": out})
    if iserr:
        note("!! 第 %d 发被拒：%s" % (n, rec))
    check("第 %d 发落账通过（格数由内核的表给，不抄在我脚本里）" % n, rec, [EXPECT_LAND])

ta, _, _ = K.call("kernel.tally")
note("tally：%s" % ta.split("\n")[0])
book_n = int(re.search(r"账 (\d+) 条", ta).group(1))
vf, _, _ = K.call("kernel.verify")
# 分母从账上取，不从我自己算的 47×发数 取：那一层曾经在被拒的那一发上报过一次假红。
check("%d 发之后链自洽（账上 %d 条）" % (N, book_n), vf,
      ["✓ 链自洽（共 %d 条，其中 %d 条比对过原像）" % (book_n, book_n)])
if book_n != 47 * N:
    note("注意：落账 %d 发 × %d 格 = %d，而账上 %d 条 —— 有发被拒了，差额就是它们"
         % (N, 47 * N, book_n))

# (1) finish_reason 的第二种非 stop 形状：tool_calls（上一轮拿到的是 length）
rb1, _, _ = K.call("kernel.read_back", {"session": "tools", "call": 1, "side": "output"})
b1_basis, b1_claim = cell_line(rb1, "finish")
note("(1) 第 1 发 finish 那一格：出处=%s 说法=%s" % (b1_basis, b1_claim))
if b1_claim == "tool_calls":
    check("(1) 拿到 tool_calls 这一档收尾，且出处是转述", "%s %s" % (b1_basis, b1_claim), ["relayed"])
else:
    note("(1) 这一发端点没按 tool_calls 收尾（说法=%r）—— 只记不判，判了就是我替端点说话" % b1_claim)

# (2) tool_calls 那一格第一次有真响应上的数（0 也要是**数出来的** 0，不是缺席）
_, tc_basis, tc_claim = ("", "", "")
for l in rb1.split("\n"):
    if l.startswith("tool_calls "):
        f = l.split()
        tc_basis, tc_claim = f[1], " ".join(f[2:])
note("(2) 账上 tool_calls 那一格：出处=%s 说法=%s" % (tc_basis, tc_claim))
check("(2) 提议个数这一格是实测的数（不是缺席）", "%s %s" % (tc_basis, tc_claim), ["measured"])

# (3) 三格在真响应上的分布：有几发歪了、链上连歪到第几
tally_bad = [(n, out["Malformed"], out["MalformedStreak"], out["ToolCalls"])
             for n, (_, _, out) in enumerate(BOOKED, 1)]
note("(3) 逐发（发号, 歪的个数, 链上连歪, 提议总数）：%s" % tally_bad)
any_bad = any(b > 0 for _, b, _, _ in tally_bad)
rb_bad = [cell_line(K.call("kernel.read_back", {"session": "tools", "call": n, "side": "output"})[0],
                    "malformed_actions") for n, b, _, _ in tally_bad if b > 0]
if any_bad:
    check("(3) 端点吐出了执行不了的提议，且那一格是实测的", " ".join(
        "%s %s" % x for x in rb_bad), ["measured"])
else:
    note("(3) 这一轮端点没吐歪提议：malformed 拿到的全是**数出来的 0**（与缺席不同），非零形状仍未见过")

# (4) 两个从没被真数填过的段类：toolspec 每一发都带，observation 只在真喂回工具结果那一发
if N >= 2:
    rb2, _, _ = K.call("kernel.read_back", {"session": "tools", "call": 2, "side": "input"})
    parts_claim = cell_line(rb2, "parts")[1]
    note("(4) 第 2 发输入侧 parts 那一格：%s" % parts_claim)
    check("(4a) 工具清单第一次作为独立一段上了段表", parts_claim, ["toolspec"])
    if "observation" in parts_claim:
        check("(4b) 工具读回来的正文也占一段，段类是 observation 而不是「拼进题面」",
              parts_claim, ["observation"])
    else:
        note("(4b) 第 2 发没带上 observation 段（t1 那一发没提议，也就没有结果可喂）—— 只记不判")
else:
    note("(4) 只落了一发就断了，observation 那一类本轮没上段表 —— 记着，不算判过")

# (5) 提议的**内容**（名字与参数）在账上有没有一处能复现
prop = [(n, t) for n, (_, t, _) in enumerate(BOOKED, 1) if t["calls"]]
hit = []
for n, t in prop:
    rb, _, _ = K.call("kernel.read_back", {"session": "tools", "call": n, "side": "output"})
    fn = (t["calls"][0].get("function") or {})
    name, args = fn.get("name") or "", fn.get("arguments") or ""
    hit.append((n, name in rb, args != "" and args in rb))
note("(5) 逐发（发号, 账上找得到工具名吗, 找得到参数原文吗）：%s" % hit)
note("(5) 这一格只描述形状：提议在账上是**三个数**（几个、歪几个、连歪几发），不是一段内容")

# (6) 流式那一发：提议是不是分片拼起来的（这一支本轮新要的形状）
for n, (_, t, out) in enumerate(BOOKED, 1):
    if out["Streaming"]:
        note("(6) 流式那一发：帧=%d 首帧=%d 最大帧间=%d 带提议碎片的帧=%s 碎片项=%s 参数长=%s finish=%r"
             % (t["frames"], t["first_ms"], t["max_gap_ms"], t.get("tc_frames"),
                t.get("tc_pieces"), t.get("arg_chars"), t["finish"]))
        trio = [cell_line(K.call("kernel.read_back", {"session": "tools", "call": n,
                                                      "side": "output"})[0], k)
                for k in ("chunks", "first_byte_ms", "max_gap_ms")]
        check("(6) 流式提议那一发的三格都是实测", " ".join("%s %s" % x for x in trio),
              ["measured", "measured", "measured"])

# (7) 空手那一发：内核第一次真的拒过一次装配，拒的是**调用方**的谎
empties = [(n, out) for n, (_, _, out) in enumerate(BOOKED, 1)
           if out["Failure"]["Kind"] == "malformed_action" and out["ToolCalls"] == 0]
ma_all = [n for n, (_, _, out) in enumerate(BOOKED, 1) if out["Failure"]["Kind"] == "malformed_action"]
if len(ma_all) > 1:
    note("(7) 同一个 malformed_action 档位本轮被两种成因用过（发号 %s）："
         "一种是提议歪了、一种是根本没提议。账上分得开它们的只有 fail_detail 那句人话" % ma_all)
if empties:
    n7 = empties[0][0]
    rb7, _, _ = K.call("kernel.read_back", {"session": "tools", "call": n7, "side": "output"})
    b7, c7 = cell_line(rb7, "fail_kind")
    note("(7) 第 %d 发 fail_kind 那一格：出处=%s 说法=%s" % (n7, b7, c7))
    check("(7) 空手那一发按「动作执行不了」落账，读回来还是这一档（内核没被放宽，是调用方改了口径）",
          "%s %s" % (b7, c7), ["malformed_action"])
else:
    note("(7) 这一轮没有空手的发：上一轮内核拒的那一形状本轮没重现 —— 判据还在，只是没撞上")

# (8) 裁剪表：整段没带进下一发的那条 assistant 消息，落在省略它的那一发上
cut_turns = [(n, in_meta) for n, (in_meta, _, _) in enumerate(BOOKED, 1) if in_meta["Cuts"]]
if cut_turns:
    n8 = cut_turns[0][0]
    rb8, _, _ = K.call("kernel.read_back", {"session": "tools", "call": n8, "side": "input"})
    d8, f8, c8, s8 = (cell_line(rb8, k) for k in ("dropped", "folded", "clipped", "notice_sent"))
    nt8 = cell_line(rb8, "notice_text")[1]
    # 挤法有两种（整段没进 / 裁尾留前缀），通知正文按挤法分开写 —— 所以期望值要跟这一发实际记的那一档一致，
    # 不能像上一版那样把 dropped-whole 的措辞写死：那一版在 tail-dropped 真发生时必红，且不是内核的错。
    # 期望正文要跟**这一发**的挤法一致 —— 上一版把所有发的挤法汇成一个集合去比对单发的通知，
    # 于是 dropped-whole 与 tail-dropped 分处两发时被拼成 BOTH，那是判定的错不是账的错。
    kinds = {c.get("Squeeze") for c in cut_turns[0][1]["Cuts"]}
    want_nt = pick_notice(kinds)
    want_bucket = "clipped" if "tail-dropped" in kinds else "dropped"
    note("(8) 第 %d 发：dropped=%s(%s) folded=%s(%s) clipped=%s(%s) notice_sent=%s 挤法=%s 通知正文=%s"
         % (n8, d8[1], d8[0], f8[1], f8[0], c8[1], c8[0], s8[1], sorted(kinds), nt8[:24]))
    other = [x for x in (d8[1] if want_bucket == "clipped" else c8[1], f8[1]) if "0 段" not in x]
    check("(8) 被挤的那一条落在它该有的档（%s），另两档不跟着变（三张名单没并成一个总数）" % want_bucket,
          "|".join([{"dropped": d8[1], "clipped": c8[1], "folded": f8[1]}[want_bucket]] + other),
          ["history"] + ["0 段"] * len(other))
    check("(8b) 挤掉一段的那一发确实把知情通知发了出去（账上是「是」，正文与挤法相配）",
          "%s %s" % (s8[1], nt8[:14]), ["是", want_nt[:14]])
    check("(8c) 通知那一句本身也占一段，段类是 truncation-notice（发出去的东西才许记账）",
          cell_line(rb8, "parts")[1], ["truncation-notice"])
else:
    note("(8) 本轮没有「整段没带进下一发」的段：裁剪表三档本轮全是空 —— 只记不判")
if pending_cuts:
    note("(8) 收尾时手里还有一条没落账的省略（歪在**最后一发**）：省略记在省略它的那一发上，"
         "而那一发本轮没发生 —— 这条留痕停在我的日志里，不在账上")

# (9) 故障那一发的**类别**：读回来还是不是装配时那一档（config 这一档在真端点上从没落过账）
faults = [(n, out) for n, (_, _, out) in enumerate(BOOKED, 1) if out["Failure"]["Kind"] != ""]
for n, out in faults:
    rb9, _, _ = K.call("kernel.read_back", {"session": "tools", "call": n, "side": "output"})
    b9, c9 = cell_line(rb9, "fail_kind")
    ra9 = cell_line(rb9, "retry_after_ms")
    want = out["Failure"]["Kind"]
    note("(9) 第 %d 发装配时记 %r，账上读回来 %r（出处=%s）；retry_after_ms 说法=%s 出处=%s；"
         "detail=%r" % (n, want, c9, b9, ra9[1], ra9[0], out["Failure"]["Detail"][:120]))
    if c9 != want:
        check("(9) 第 %d 发的故障类别落账前后不是同一档（装配=%s，账上=%s）" % (n, want, c9),
              "MISS", [want])
    else:
        check("(9) 第 %d 发按 %s 落账、读回来还是这一档（retry_after 那一格不许长成 0）" % (n, want),
              "%s|%s" % (c9, ra9[0]), [want])

# (10) config 那一档今天装不下对端的状态码（互核写着 kind≠http ⇒ status=0）。
# 这一条是**示踪弹**：它断言的是「我们宁可丢数也没有谎报类别」——
# 哪天判据放宽了、开始把码记进 fail_status、把对端原话抄进 fail_detail，它就该红，那时按新口径重写它。
cfg = [(n, t, out) for n, (_, t, out) in enumerate(BOOKED, 1) if out["Failure"]["Kind"] == "config"]
for n, t, out in cfg:
    rb10, _, _ = K.call("kernel.read_back", {"session": "tools", "call": n, "side": "output"})
    st10 = cell_line(rb10, "fail_status")
    dt10 = cell_line(rb10, "fail_detail")
    note("(10) 第 %d 发：对端回的是 %d，而账上 fail_status=%s（出处=%s）、fail_detail 出处=%s；"
         "那个码现在只住在 Detail 那句话里" % (n, t["status"], st10[1], st10[0], dt10[0]))
    check("(10) 第 %d 发没有为了留住状态码而谎报类别（fail_status 是 0、说法是我们自己构造的）" % n,
          "%s|%s" % (st10[1], dt10[0]), ["0", "measured"])

# (11) 走私测试（示踪弹）：第 N 发模型原话的指纹**就在账上**，而下一发历史里那句话是不是同一句？
# 这一条断言的是「发出去的历史与账对得上」。内核今天不比对这件事（它只核每一发自己填的 47 格），
# 所以 SMUGGLE=1 那一趟必红 —— 红的是「警卫员的原料都在、检查不存在」，不是脚本写坏了。
for n_prev, sent_text in HIST_LINKS:
    rb11, _, _ = K.call("kernel.read_back", {"session": "tools", "call": n_prev, "side": "output"})
    _, sha_on_book = cell_line(rb11, "model_says_sha256")
    sha_sent = hashlib.sha256(sent_text.encode("utf-8")).hexdigest()
    note("(11) 第 %d 发：账上指纹 %s…，真发进下一发的那句指纹 %s…，内容=%r"
         % (n_prev, sha_on_book[:12], sha_sent[:12], sent_text[:24]))
    check("(11) 第 %d 发发进下一发历史的那句对得上账上原话指纹（内核对这个只做不了：没人比对）" % n_prev,
          sha_sent[:16], [sha_on_book[:16]])

# (12) 挤过尾的那段历史，能不能**机器地**连回它是第几发的原话？
# 判的是形状不是措辞：裁剪表里那条 SHA256 与账上那一发的 model_says_sha256 必须是同一个量才谈得上比对。
# 规矩五：这条新判定必须真的红过一次 —— 跑 FOLD=1 就是为了让它有机会红，不跑就当没这回事。
for (i_prev, orig_content, kept, raw, new_raw) in FOLDED:
    n_prev = i_prev                      # 被挤的那句是第 i_prev 发说的（BOOKED 从 1 数起）
    rb12, _, _ = K.call("kernel.read_back", {"session": "tools", "call": n_prev, "side": "output"})
    _, sha_on_book = cell_line(rb12, "model_says_sha256")
    sha_cut = hashlib.sha256(raw.encode("utf-8")).hexdigest()
    sha_kept = hashlib.sha256(kept.encode("utf-8")).hexdigest()
    sha_orig = hashlib.sha256(orig_content.encode("utf-8")).hexdigest()
    note("(12) 第 %d 发原话 %r 挤成 %r；账上 model_says=%s…  裁剪表记=%s…  发出去那句=%s…"
         % (n_prev, orig_content[:16], kept[:16], sha_on_book[:10], sha_cut[:10], sha_kept[:10]))
    note("     （原话整句的指纹 %s… 与账上那条%s对得上；裁剪表记的是**段**（含 JSON 壳）的指纹）"
         % (sha_orig[:10], "✓" if sha_orig == sha_on_book else "✗"))
    check("(12) 裁剪表那条指纹能直接对上账上原话指纹（对不上=谁也连不回它是哪一句）",
          sha_cut[:16], [sha_on_book[:16]])

ok_turns = [(i, out) for i, (_, _, out) in enumerate(BOOKED) if out["Failure"]["Kind"] == ""]
bad_turns = [(i, out) for i, (_, _, out) in enumerate(BOOKED) if out["Failure"]["Kind"] != ""]
cost_in = sum(out["UsageIn"]["N"] for _, out in ok_turns if not out["UsageIn"]["Absent"])
cost_out = sum(out["UsageOut"]["N"] for _, out in ok_turns if not out["UsageOut"]["Absent"])
other_in = sum(out["UsageIn"]["N"] for _, out in bad_turns if not out["UsageIn"]["Absent"])
other_out = sum(out["UsageOut"]["N"] for _, out in bad_turns if not out["UsageOut"]["Absent"])
# 内核里那条「这一发的成本能不能进总额」的判据住在 CostCountable()，它不替人累加。
# 这里分两栏报，是把那条判据的结果**显式写出来**，而不是悄悄把空手那一发也加进去。
note("成本：正常那几发 %d 入 / %d 出；故障与空手的发（发号 %s）另计 %d 入 / %d 出"
     % (cost_in, cost_out, [i + 1 for i, _ in bad_turns], other_in, other_out))

ej, _, _ = K.call("kernel.encode_journal", {"payload": "included"})
book = ej.split("\n", 1)[1]
io.open(BOOK, "w", encoding="utf-8", newline="\n").write(book)
check("账落盘且不带 key", book, ["tools"], [KEY[:10], "sk-"])
note("账本：%s（落盘行数 %d，按空行切出来的条目数 %d）"
     % (BOOK, len(book.split("\n")), len([l for l in book.split("\n") if l.strip()])))

K.close()
fails = check.red
note("\n===== 判定 =====")
for name, st, why in check.rows:
    note("%s  %s  %s" % (st, name, why))
with io.open(REPORT, "w", encoding="utf-8") as f:
    f.write("\n".join(log) + "\n")
print("TOOLS PROBE status=%s checks=%d red=%d turns=%d tokens_in=%d tokens_out=%d" % (
    "OK" if not fails else "RED", len(check.rows), len(fails), N, cost_in, cost_out))
for name, st, why in fails:
    print("  RED %s | %s" % (name, why.encode("ascii", "replace").decode("ascii")))
sys.exit(0 if not fails else 1)
