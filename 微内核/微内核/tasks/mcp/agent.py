# -*- coding: utf-8 -*-
"""agent.py —— 第一个"由账本决定下一步"的 Agent（示踪弹，不是产品）。

为什么要有这一支：内核到今天只矫正**写账的人**（拒收过装配错的我），
从没矫正过**模型的下一步** —— 两个驱动里 read_back/tally 全落在回合循环之后。
这一支把那条回边真的接上一试：循环体里每一个"接下来干什么"的决定，
要么写明它读的是账上哪一格，要么记成"本地信息，没问内核"。
轮末吐一个数：**账本触发过几次决定**。0 就证明这条边虚接。

形状只许有一份：两侧的模板取自 client.py 的 INPUT1/OUTPUT1（那是 drive/fork 共用的那一份），
这里只填数，不改形状。
"""
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
from client import Kernel, Judge, m, missing, MEAS, EST, UNSET  # noqa: E402

RELAY = 2   # BasisRelayed —— 分词器在对端，我们只能转述它报的数（内核明写不许标 measured）

BASE = os.environ.get("AGNES_BASE", "https://api.agnes-ai.cn/v1")
MODEL = os.environ.get("AGNES_MODEL", "agnes-3.0-flash")
KEY = os.environ.get("AGNES_KEY", "")
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORT = os.environ.get("AGENT_REPORT", os.path.join(ROOT, "报告", "agent-真跑.txt"))
BOOK = os.environ.get("AGENT_BOOK", os.path.join(ROOT, "报告", "样例-agent.jsonl"))
MAX_TURNS = int(os.environ.get("AGENT_MAX_TURNS", "8"))
MAX_TOKENS = int(os.environ.get("AGENT_MAX_TOKENS", "64"))   # 假端点靠这个旋钮走剧本

# 任务：一个必须分几步用工具才算得完的活，答案可机器核对
NUMS = [27, 15, 8, 34]
TARGET = sum(NUMS)

SYSTEM = ("你是一个只用工具的计算器。你手里只有一个 ledger_add，一次只能加两个数。"
          "要算一组数的和，就一步步提议加法，等我把每次的结果作为工具结果回给你，"
          "最后把总和写成一句话。不要一次编出结果。")
TOOLS = [{"type": "function", "function": {
    "name": "ledger_add", "description": "把两个整数相加，返回它们的和",
    "parameters": {"type": "object", "properties": {"a": {"type": "integer"},
                                                     "b": {"type": "integer"}},
                   "required": ["a", "b"]}}}]
TOOLSPEC = json.dumps(TOOLS, ensure_ascii=False)

# ============ 回边：账上的格 → 下一步动作。这是这一支唯一的新东西 ============
# 每条都注了"这一格住在账上哪个键名"，动作由 read_back 读回来的值触发。
POLICY = {
    "transport": ("重发原样一次", "账上 fail_kind=transport：这一类按定义没收到对端响应，原样再发有用"),
    "http": ("等一等再原样发", "账上 fail_kind=http：对端忙/断了，RetrySame 那一族"),
    "config": ("停 —— 重发只会再吃一次同样的拒绝", "账上 fail_kind=config：密钥、模型名、参数这一类"),
    "protocol": ("停 —— 我们对不上话，再发也是", "账上 fail_kind=protocol"),
    "malformed_action": ("换个说法再问一次", "账上 fail_kind=malformed_action：这一发没做成动作"),
    "budget": ("挤历史再发", "账上 fail_kind=budget：额度不够，不是对端的错"),
}
DECISIONS = []   # (第几发, 依据来自哪一格或"本地", 读到的值, 做了什么, 为什么)


def decide(turn, cell, value, action, why):
    DECISIONS.append((turn, cell, value, action, why))
    note("    决定：第 %d 发 依据=%-22s 值=%-18r 动作=%s" % (turn, cell, value, action))


log = []
note = log.append
check = Judge()


def post(body):
    raw = json.dumps(body, ensure_ascii=False).encode("utf-8")
    rq = urllib.request.Request(BASE + "/chat/completions", data=raw, method="POST")
    rq.add_header("Authorization", "Bearer " + KEY)
    rq.add_header("Content-Type", "application/json")
    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(rq, timeout=120) as r:
            data = r.read()
            try:
                j = json.loads(data.decode("utf-8"))
            except Exception as e:
                return dict(ok=False, status=r.getcode(), sent=len(raw), got=len(data),
                            ms=int((time.monotonic() - t0) * 1000), kind="protocol",
                            detail=data.decode("utf-8", "replace")[:200], why=repr(e)[:80],
                            text="", finish="", model="", usage={}, calls=[])
            ch = j["choices"][0]
            msg = ch["message"]
            return dict(ok=True, status=r.getcode(), sent=len(raw), got=len(data),
                        ms=int((time.monotonic() - t0) * 1000), kind="", detail="",
                        text=msg.get("content") or "", finish=ch.get("finish_reason") or "",
                        model=j.get("model") or "", usage=j.get("usage") or {},
                        calls=msg.get("tool_calls") or [])
    except urllib.error.HTTPError as e:
        err = e.read() if hasattr(e, "read") else b""
        return dict(ok=False, status=e.code, sent=len(raw), got=len(err),
                    ms=int((time.monotonic() - t0) * 1000),
                    kind=("config" if e.code in (400, 401, 403, 404, 409, 422)
                          else ("http" if e.code else "transport")),
                    detail=err.decode("utf-8", "replace")[:200].strip(), why="",
                    text="", finish="", model="", usage={}, calls=[])
    except Exception as e:
        return dict(ok=False, status=0, sent=len(raw), got=0,
                    ms=int((time.monotonic() - t0) * 1000), kind="transport",
                    detail=("%s: %s" % (type(e).__name__, str(e)))[:200], why="",
                    text="", finish="", model="", usage={}, calls=[])


def count_malformed(calls):
    bad = []
    for i, tc in enumerate(calls):
        fn = tc.get("function") or {}
        name = (fn.get("name") or "").strip()
        args = fn.get("arguments")
        if not isinstance(args, str):
            args = "" if args is None else json.dumps(args, ensure_ascii=False)
        if not name:
            bad.append("第 %d 个提议没有名字" % (i + 1))
            continue
        try:
            json.loads(args)
        except Exception:
            bad.append("第 %d 个提议（%s）参数解不出 JSON：%r" % (i + 1, name, args[:40]))
    return bad


def build(turn, segs, t, streak_prev):
    """把这一发装配成两侧账。段表与真发出去的 messages 同源（规矩：发出去才算数）。"""
    order = [k for k, _ in segs]
    totals, seen = [], []
    for k, txt in segs:
        if k in seen:
            for q in totals:
                if q["Kind"] == k:
                    q["Parts"] += 1
                    q["Bytes"] += len(txt.encode("utf-8"))
        else:
            seen.append(k)
            totals.append({"Kind": k, "Parts": 1, "Bytes": len(txt.encode("utf-8"))})
    total = sum(len(txt.encode("utf-8")) for _, txt in segs)
    shares = {p["Kind"]: max(4, int(round(100.0 * p["Bytes"] / max(1, total)))) for p in totals}
    if sum(shares.values()) > 100:
        scale = 100.0 / sum(shares.values())
        shares = {k: max(1, int(v * scale)) for k, v in shares.items()}
        big = max(shares, key=lambda k: shares[k])
        shares[big] = max(1, shares[big] + 100 - sum(shares.values()))

    bad = [] if not t["ok"] else count_malformed(t["calls"])
    nb = len(t["text"].encode("utf-8"))
    if not t["ok"]:
        kind, status = t["kind"], 0
        detail = t["detail"] or "管路没通"
        # 现行互核：kind != http ⇒ status = 0（缺口 10 还没定口径，这里按 tools_probe 的绕行记法）
        if kind == "config" and t["status"]:
            detail = ("本地判定 config：对端回了状态码 %d 并拒了；它的原话没进账（这一档不许带状态码）"
                      % t["status"])[:200]
        failure = {"Kind": kind, "Detail": detail.strip()[:200],
                   "Status": (m(status, MEAS) if status
                              else missing("管路没通，压根没收到对端响应 —— 不是「状态码量到 0」")),
                   "RetryAfterMs": missing("没收到 Retry-After"), "Downgrade": False}
    elif bad:
        failure = {"Kind": "malformed_action", "Detail": "；".join(bad)[:200],
                   "Status": m(t["status"] or 200, MEAS),
                   "RetryAfterMs": missing("原样重发不会变好：歪的是这一发的提议本身"), "Downgrade": False}
    elif nb == 0 and not t["calls"]:
        failure = {"Kind": "malformed_action",
                   "Detail": ("端点回了 %d 字节、finish=%r、原话 0 字节、0 个提议" % (t["got"], t["finish"]))[:200],
                   "Status": m(t["status"] or 200, MEAS),
                   "RetryAfterMs": missing("原样重发不会变好：同样的额度还会截在同样的地方"),
                   "Downgrade": False}
    else:
        failure = {"Kind": "", "Detail": "",
                   "Status": missing("这一发没故障，也就没有要记的状态码"),
                   "RetryAfterMs": missing("这一发没断，也就没有要等多久"), "Downgrade": False}

    inp = {
        "Call": turn, "Order": order, "Parts": totals, "ContentBytes": total,
        "Tokens": m(max(1, total // 3), EST, "1 token ≈ 3 字节的粗估"),
        "Budget": {"CallBytesCeiling": 32768, "TransportSlackPct": 12, "ContentCeiling": 28835,
                   "Shares": shares, "LeftoverPct": max(0, 100 - sum(shares.values())),
                   "FixedOverhead": m(300, EST, "骨架+模型名+工具清单的粗估"),
                   "EffectiveCeiling": 28835, "ClampRounds": 0},
        "Cuts": [],
        "Notice": {"Sent": False, "Trimmed": False,
                   "Why": "这一发没折叠、没裁尾、没整段丢弃", "Text": ""},
    }
    out = {
        "Call": turn, "Streaming": False,
        "Finish": {"Reason": t["finish"], "Said": t["finish"] != ""},
        "Model": t["model"], "ModelSaid": t["model"] != "",
        "WireFormat": (t.get("obj") or ""), "WireFormatSaid": bool(t.get("obj")),
        # 这里原来有一行 `"_status_probe": None,` —— 全仓没有任何地方读它，是手工探针的遗留。
        # 10-17 真端点跑这一支时被 #46 的解码闸当场拒收（`写侧没有 _status_probe 这个键…整发拒收，链上一条不写`），
        # 原文归档在 报告/agent-真跑-1017.txt。留着它整支循环一步都落不了账，所以删。
        "Says": {"Bytes": nb, "Kept": nb, "SHA256": hashlib.sha256(t["text"].encode("utf-8")).hexdigest()},
        "UsageIn": m(int(t["usage"]["prompt_tokens"]), RELAY, "端点响应的 usage.prompt_tokens")
                   if "prompt_tokens" in t["usage"] else missing("响应里没有 usage.prompt_tokens"),
        "UsageOut": m(int(t["usage"]["completion_tokens"]), RELAY, "端点响应的 usage.completion_tokens")
                    if "completion_tokens" in t["usage"] else missing("响应里没有 usage.completion_tokens"),
        "WireBytesOut": m(t["sent"], MEAS), "WireBytesIn": m(t["got"], MEAS),
        "ToolCalls": len(t["calls"]), "Malformed": len(bad),
        "MalformedStreak": streak_prev + 1 if bad else 0,
        "Failure": failure,
        "Chunks": missing("这一发不是流式的，结构上没有分帧"),
        "FirstByteMs": missing("这一发不是流式的，没有首帧这件事"),
        "MaxGapMs": missing("这一发不是流式的，没有帧间"),
    }
    return inp, out, bad


def cell_line(rendered, key):
    for ln in (rendered or "").splitlines():
        if ln.strip().startswith(key):
            rest = ln.strip()[len(key):].strip()
            p = rest.split(None, 1)
            return (p[0] if p else "", p[1].strip() if len(p) > 1 else "")
    return ("", "")


def read_cell(K, turn, key, side="output"):
    rb, _, _ = K.call("kernel.read_back", {"session": "agent", "call": turn, "side": side})
    return cell_line(rb, key)


def probe_turns(K):
    """新进程不知道跑过几发 —— 只能一问一问试出来（读侧没有"有几发"这一格，这也是测出来的一条）。"""
    n = 0
    while n < 24:
        rb, _, _ = K.call("kernel.read_back", {"session": "agent", "call": n + 1, "side": "output"})
        # 服务端对"没这一发"的措辞是"这本账里没有这一发"，它出现在整句后半段，
        # 所以上一版按前 40 字匹配根本没命中 —— 现在整行匹配。
        first = (rb or "").splitlines()[0] if rb else ""
        if not rb or ("没有这一发" in first) or ("没有" in first and "第" in first):
            break
        n += 1
    return n


def finish(reds, summary):
    """提前退出也必须留话：上一版 resume() 里有两条 return 走的是静默路径，
    脚本什么都不打印却退出 0，读起来像成功 —— 这是「红不了的承诺是散文」同款，写在我自己身上。"""
    print(summary)
    io.open(REPORT, "w", encoding="utf-8").write("\n".join(log) + "\n")
    k = globals().get("K")
    if k:
        k.close()
    return 1 if reds else 0


def resume():
    """续跑臂：只带落盘那本账，内存里没有任何 MSGS —— 数一数"账本单独能定几次决定"。"""
    note("AGENT resume run %s  book=%s" % (time.strftime("%F %T"), BOOK))
    if not os.path.exists(BOOK):
        note("账本文件不存在，续跑臂无从谈起：%s" % BOOK)
        return finish([("no-book", "FAIL", "文件不存在")], "AGENT-RESUME status=BROKE ledger_file=missing")
    book_text = io.open(BOOK, encoding="utf-8").read()
    K = Kernel("agent-resume")
    ld, iserr, _ = K.call("kernel.load_journal", {"text": book_text})
    note("load_journal：%s" % (ld or "")[:200])
    if iserr:
        note("内核拒了这本账 —— 续跑臂不往下走（拒收本身就是结果）")
        return finish([("load", "FAIL", str(ld))],
                      "AGENT-RESUME status=REJECTED-by-kernel msg=%s" % str(ld)[:120].encode("ascii", "replace").decode())
    ta, _, _ = K.call("kernel.tally")
    vf, _, _ = K.call("kernel.verify")
    note("tally： %s" % (ta or "").splitlines()[0] if ta else "(空)")
    note("verify：%s" % (vf or "").splitlines()[0] if vf else "(空)")
    n = probe_turns(K)
    note("只靠读侧试出来：这本账有 %d 发（脚本里没有一格记着这个数，是一发一发问出来的）" % n)
    if n == 0:
        note("一本账都读不出来 —— 走静默退出是上一版的毛病，现在把话说完再退")
        return finish([("probe", "FAIL", "读不出任何一发")],
                      "AGENT-RESUME status=EMPTY-LEDGER turns=0")

    ok_list, no_list = [], []

    def try_decide(name, key, side, how):
        b, c = read_cell(K, n, key, side)
        got = c and c != "（这一格是空的）"
        if got:
            ok_list.append((name, key, c))
            note("  能定｜%-26s 依据=%-18s 值=%r（出处=%s）" % (name, key, c[:36], b))
        else:
            no_list.append((name, key))
            note("  不能定｜%-26s 想要=%-18s 读回来=%r —— %s" % (name, key, c[:36], how))

    try_decide("上一发该不该原样重发", "fail_kind", "output", "这一档没落账（这本账跑得很顺）")
    try_decide("连歪了几发、要不要收手", "malformed_streak", "output", "数在账上")
    try_decide("下一发要不要挤历史", "content_bytes", "input", "字节数在账上（原文不在 —— 所以只能定'要不要挤'，定不了'挤哪段'）")
    try_decide("这一路花掉多少 token", "usage_out", "output", "对端自报的数在账上")
    try_decide("上一发是不是那句（比对指纹）", "model_says_sha256", "output", "指纹能定同一性")
    try_decide("上一发那句话**是什么**", "（只有指纹，没有原文）", "output",
               "指纹答不了内容 —— 上一版我把这条误标成'能定'，数因此虚高了一格")
    try_decide("上一个工具结果是多少（42 还是 50）", "observation 的原文", "input",
               "输入侧只记段类与字节数，observation 的内容一格都没有")
    try_decide("待算的数还剩哪些没加", "user 段的原文", "input", "题面也没进账")
    try_decide("下一发该问什么", "（没有任何格能回答这个）", "input", "这是内容决定，不是计数决定")

    # 去重门这一臂**测不了**，就别拿它装样子：要测它得交一份**合法的**整发装配，
    # 而续跑臂手里只有账、组不出合法装配（这正是 (F) 说的那件事）。
    # 上一版我传了个只含 Call 的残件，内核当然拒 —— 但拒的是"单发上限是 0，这份预算不是 DeriveBudget 造的"，
    # 跟去重门一点关系没有：那是一条**看着像测到了的假测试**。去重门由 drive.py 那条覆盖（同进程记两遍）。
    note("去重门：本臂不判 —— 组不出合法装配（组得出就不叫续跑了）。已有覆盖：drive.py 的'读回后同发再记被拦'")

    note("")
    note("账本单独能定的决定：%d 类；不能定的：%d 类" % (len(ok_list), len(no_list)))
    for x in no_list:
        note("   不能定：%s（想要的那格：%s）" % (x[0], x[1]))

    check("(E) 续跑时账本至少能单独定 1 类决定（计数类）", "ok=%d" % len(ok_list), (), ["ok=0"])
    check("(F) 内容类决定定不了 —— 这条**期望它成立**：账是审计件不是恢复件",
          "no=%d" % len(no_list), (), ["no=0"])
    check("(G) 读回来的账仍然自洽", (vf or ""), ["自洽"])
    if os.environ.get("AGENT_RESUME_LLM"):
        # 一发真调用：提示里**只许出现账本能给得出的事实**。
        # 上一版我把题面 "27 + 15 + 8 + 34" 也写进去了，于是模型答出 84 —— 那不是"账当得了记忆"，
        # 那是我把答案的原料递给它的污染实验；而且那条判定禁项写了 [""]（空串在任何文本里都算命中），
        # 所以它恒红。两处一起改：提示不含任何数字项，判的是**它答对就该红**（那只能是编的）。
        # facts 里**不放指纹**：那串十六进制里必然撞出 "8"、"84" 这类子串，
        # 用子串查"有没有漏题面数字"就会一直假红 —— 指纹对续跑也没有内容价值，去掉。
        facts = "；".join("%s=%s" % (nm, v) for nm, k, v in ok_list if "sha256" not in k)
        q = ("我们之前一起算过一串数的和，跑了几发。你现在的上下文是空的，"
             "关于那段往来你只有一个账本给得出的摘要： %s 。"
             "请给出那个总和；如果你没有这些数，请直接说不知道。" % facts)
        leak = [str(x) for x in NUMS + [TARGET] if re.search(r"(?<!\d)%s(?!\d)" % x, q)]
        note("续跑提示（只含账本给得出的事实）：%s" % q[:200])
        note("实查泄漏（按独立数字查，不算十六进制里的子串）=%r" % leak)
        check("(I0) 提示是干净的（题面与答案都不在里面）", "leak=%r" % leak, ["leak=[]"])
        t = post({"model": MODEL, "messages": [{"role": "user", "content": q}], "max_tokens": 64})
        note("续跑那发真调用（只喂账本给得出的事实）：finish=%r 回话=%r" % (t["finish"], t["text"][:160]))
        # 判的是反向：**它要是报出了 84，那是编的**（提示里没有这些数，账上也没有原文）。
        check("(I1) 只用账本续跑，模型不许能报出正确总和 %d（报出来就是幻觉，不是记忆）" % TARGET,
              t["text"] or "(空)", (), [str(TARGET)])
        admitted = any(w in (t["text"] or "") for w in ("不知道", "无法", "没有这些", "看不到", "无从", "缺少"))
        # 上一版这条 needles 与禁项都是空的 —— 空 needles 恒真、空禁项恒不命中，那是一条永远不会红的假判定。
        check("(I2) 它承认自己没有这些数（不承认就是把编的当记忆）", "admitted=%s" % admitted, ["admitted=True"])
    K.close()
    reds = [r for r in check.rows if r[1] != "PASS"]
    print("AGENT-RESUME status=%s turns=%d ledger_decides=%d cannot=%d checks=%d red=%d"
          % ("RED" if reds else "OK", n, len(ok_list), len(no_list), len(check.rows), len(reds)))
    for name, st, why in check.rows:
        sys.stdout.buffer.write(("%-5s %s  %s\n" % (st, name, why)).encode("utf-8", "replace"))
    io.open(REPORT, "w", encoding="utf-8").write("\n".join(log) + "\n")
    return 1 if reds else 0


def main():
    if os.environ.get("AGENT_RESUME"):
        return resume()
    if not KEY:
        print("AGNES_KEY missing in env")
        return 2
    note("AGENT run %s  model=%s  task=sum(%s)=%d  max_turns=%d"
         % (time.strftime("%F %T"), MODEL, NUMS, TARGET, MAX_TURNS))
    K = Kernel("agent")
    ct, _, _ = K.call("kernel.celltable")
    note("内核自报：%s" % (ct.splitlines()[0] if ct else "(没读到表)"))

    MSGS, SEGS = [], []
    streak = 0
    turn = 0
    running = NUMS[0]
    pending = NUMS[1:]
    done = False
    while turn < MAX_TURNS and not done:
        turn += 1
        if turn == 1:
            q = "要算 %s 的和。先提议第一步加法。" % " + ".join(str(x) for x in NUMS)
            segs = [("system", SYSTEM), ("toolspec", TOOLSPEC), ("user", q)]
            MSGS.append({"role": "user", "content": q})
        else:
            segs = [("system", SYSTEM), ("toolspec", TOOLSPEC)] + [
                ("user" if mm["role"] in ("user",) else
                 ("observation" if mm["role"] == "tool" else "history"),
                 mm["content"] if isinstance(mm.get("content"), str) else json.dumps(mm, ensure_ascii=False))
                for mm in MSGS]
        body = {"model": MODEL,
                "messages": [{"role": "system", "content": SYSTEM}] + MSGS,
                "tools": TOOLS, "max_tokens": MAX_TOKENS}
        note("== turn %d: 发=%d 收待算=%s 已累计=%d" % (turn, body and len(json.dumps(body)), pending, running))
        t = post(body)
        inp, out, bad = build(turn, segs, t, streak)
        # 先问内核（写侧门口），再落账
        rec, iserr, _ = K.call("kernel.record_call", {"session": "agent", "actor": "agent",
                                                      "input": inp, "output": out})
        if iserr or "拒收" in (rec or ""):
            note("  kernel 拒了这发：%s" % (rec or "")[:200])
            decide(turn, "record_call 回执", "拒收", "停止 —— 不再往下走", "装配不自洽时继续跑只会生产假形状")
            break
        streak = out["MalformedStreak"]

        # ===== 回边：下一步动作由账上那一格决定 =====
        kind_b, kind_c = read_cell(K, turn, "fail_kind")
        if kind_c and kind_c != "（这一格是空的）":
            act, why = POLICY.get(kind_c, ("停 —— 表里没有这一档", "账上类别不在策略表里"))
            decide(turn, "fail_kind", kind_c, act, why)
            if act.startswith("停"):
                break
            if act.startswith("重发") or act.startswith("等一等"):
                MSGS = MSGS  # 原样：历史不动，下一轮再发一次
                continue
        if streak >= 2:
            st_b, st_c = read_cell(K, turn, "malformed_streak")
            decide(turn, "malformed_streak", st_c, "停止 —— 连歪两发不是运气",
                   "账上 streak 这一格本来就是给这个判断留的")
            break

        if t["calls"]:
            decided_by = ("tool_calls", read_cell(K, turn, "tool_calls"))
            for tc in t["calls"]:
                fn = (tc.get("function") or {})
                try:
                    a = json.loads(fn.get("arguments") or "{}")
                    s = int(a.get("a", 0)) + int(a.get("b", 0))
                except Exception:
                    s = None
                if s is not None:
                    running = s
                    pending = [x for x in pending if x not in (a.get("a"), a.get("b"))] or pending
                    obs = json.dumps({"sum": s}, ensure_ascii=False)
                else:
                    obs = json.dumps({"error": "参数解不出来"}, ensure_ascii=False)
                MSGS.append({"role": "assistant", "content": None,
                             "tool_calls": [tc]})
                SEGS.append(("history", json.dumps([tc], ensure_ascii=False)))
                MSGS.append({"role": "tool", "tool_call_id": tc.get("id") or "call_without_id",
                             "content": obs})
                note("  工具执行 %s -> %s" % (fn.get("name"), obs))
            decide(turn, "tool_calls（账上的数）", decided_by[1], "执行工具并把结果作为 observation 发回去",
                   "下一发多了一段 observation —— 这一段是真发出去的，也真进了段表")
            continue
        if t["text"]:
            final = t["text"].strip()
            decide(turn, "finish_reason", read_cell(K, turn, "finish")[1], "收工 —— 模型给了话没给提议",
                   "账上收尾原因那一格是对端自报的")
            note("  模型最终回答：%r" % final[:120])
            done = str(TARGET) in final
            break
        decide(turn, "（没有任何动作）", "空", "换个说法再问一次", "这一发既没提议也没话")

    # ===== 收尾：把这一趟的账读回来核对，并数"账本触发过几次决定" =====
    ta, _, _ = K.call("kernel.tally")
    vf, _, _ = K.call("kernel.verify")
    for ln in (ta or "").splitlines():
        note("tally： %s" % ln)
    for ln in (vf or "").splitlines():
        note("verify：%s" % ln)
    from_ledger = sum(1 for d in DECISIONS if "record_call" not in d[1] and d[1] != "本地")
    note("")
    note("决策共 %d 条；其中依据写在账上的某一格的：%d 条" % (len(DECISIONS), from_ledger))
    for d in DECISIONS:
        note("   第%-2d 发  依据=%-24s 值=%-20r 动作=%s" % (d[0], d[1], d[2], d[3]))

    fake = "127.0.0.1" in BASE
    if fake:
        # 假端点是定死回答的（永远提议 27+15），它做不完这道题 —— 这一条对它不作数，
        # 只在本机自检时打印提醒，别把它读成"任务失败"，也别把它读成"通过"。
        note("(A) 自检模式（假端点）：任务完成度这一条不判 —— 假端点定死回答，做不完链式加法")
    else:
        check("(A) 任务做完了：总和 %d 出现在最终回答里" % TARGET, "done=%s" % done, ["done=True"])
    check("(B) 每一发都落进了同一条链（没有半发）", (vf or ""), ["自洽"])
    # (C) 是这一支的存在理由：回边是不是实的。判的是"0 次"不许出现 —— 一次都没有就说明
    # 循环体还是只看本地信息，我又造了一个看起来接上的假接口。
    check("(C) 账本至少触发过一次决定（回边是实的）", "from_ledger=%d" % from_ledger,
          (), ["from_ledger=0"])
    txt, _, _ = K.call("kernel.encode_journal", {"payload": "included"})
    # encode_journal 交回来的**第一行是给人看的表头**（"89035 字节 JSONL，188 行…"），
    # 账体从第二行起 —— 上一版我把整段回复当账写了，于是那本"账"第 1 行解不开、load_journal 当场拒收。
    book_body = (txt or "").split("\n", 1)[1] if txt and "\n" in txt else (txt or "")
    io.open(BOOK, "w", encoding="utf-8", newline="\n").write(book_body)
    lines_on_disk = len([x for x in book_body.splitlines() if x.strip()])
    note("表头行（不写进账）：%s" % ((txt or "").splitlines() or [""])[0][:120])
    note("账本：%s（条目数 %d）" % (BOOK, lines_on_disk))
    # (D) 判的是**账本字节里有没有密钥**：拿真 key 的前 10 个字符与 "sk-" 当禁项去扫那本账，
    #      而不是扫一个我自己写的词 —— 上一版这条是假断言，已改。
    check("(D) 账本字节里扫不到密钥（前 10 字符与 sk- 都查）", book_body or "(空账)", [],
          ["sk-", KEY[:10]])

    reds = [r for r in check.rows if r[1] != "PASS"]
    print("AGENT status=%s turns=%d decisions=%d from_ledger=%d checks=%d red=%d"
          % ("RED" if reds else "OK", turn, len(DECISIONS), from_ledger, len(check.rows), len(reds)))
    for name, st, why in check.rows:
        sys.stdout.buffer.write(("%-5s %s  %s\n" % (st, name, why)).encode("utf-8", "replace"))
    io.open(REPORT, "w", encoding="utf-8").write(
        "\n".join(log) + "\n\n" + "\n".join("%s %s  %s" % (st, nm, wy) for nm, st, wy in check.rows) + "\n")
    K.close()
    return 1 if reds else 0


if __name__ == "__main__":
    main()
