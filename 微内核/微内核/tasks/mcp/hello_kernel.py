# -*- coding: utf-8 -*-
"""hello_kernel.py —— 三分钟接上：一个最小可照抄的调用方，一次往来都不打。

跑法（只需要 Go 与 Python，不打任何端点、不花钱）：
    go build -o %TEMP%\\mcpkernel.exe ./cmd/mcpkernel
    python tasks/mcp/hello_kernel.py

它做的事就是别人接进去时要做的那五步：起进程 → 要格位表 → 先问（不落账）→ 落账 → 读回来。
控制台只打 ASCII（Windows GBK 会砸中文），中文全进 报告/三分钟接上.txt。
"""
import hashlib
import io
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client import Kernel  # noqa: E402  —— 一个客户端类，起进程 + 拆 MCP 的回话

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORT = os.path.join(ROOT, "报告", "三分钟接上.txt")
lines = []


def say(s):
    lines.append(s)
    sys.stdout.buffer.write(s.encode("ascii", "replace") + b"\n")
    sys.stdout.flush()


def measure(n, basis, source="", absent=False, why=""):
    """一格 Measure 的 JSON 形状。basis：0 未填 1 实测 2 转述 3 估算 4 模型自述 5 缺席。"""
    return {"N": n, "Basis": basis, "Source": source, "Absent": absent, "Refused": False, "Why": why}


ABSENT = lambda why: measure(0, 0, "", True, why)  # noqa: E731  缺席必须带理由，这是内核的三态

TEXT = "42"          # 假设模型这一发回了这两个字节
SHA = hashlib.sha256(TEXT.encode("utf-8")).hexdigest()

# 这份模板里那两条「schema 看不见的规矩」（10-16 两个陌生调用方各撞一轮撞出来的）：
#   ① 估算/转述那两档的说明要写在 **Source**，不是 Why —— `measure(7, 3, "…粗估")` 的第三个参数就是它；
#      写成 Why 的话，那一格与"模型自己说的"在证据上没区别。
#   ② `Absent=true` 时 **Basis 必须是 0、N 必须是 0**，理由写 Why —— 见下面 ABSENT()，
#      它就是这个形状的唯一一份写法；把缺席写成 `measured 0` 会被整发拒收（规矩一）。
# 还有第三件事要说明白：下面那块 Budget 是**手搭的**，数值等价于 `DeriveBudget(32768, 12, …, 估 300)` 的结果。
# 内核会重算比对（规矩三），所以抄错一个数就整发进不了门 —— 非 Go 调用方要么按公式自己复算，
# 要么等 `kernel.derive_budget`（工具 10→11，看板 #39 的那半个问题，还没拍）。
INPUT1 = {
    "Call": 1,
    "Order": ["system", "user"],
    "Parts": [{"Kind": "system", "Parts": 1, "Bytes": 11},
              {"Kind": "user", "Parts": 1, "Bytes": 9}],
    "ContentBytes": 20,
    "Tokens": measure(7, 3, "1 token ≈ 3 字节的粗估"),
    "Budget": {"CallBytesCeiling": 32768, "TransportSlackPct": 12, "ContentCeiling": 28835,
               "Shares": {"system": 55, "user": 45}, "LeftoverPct": 0,
               "FixedOverhead": measure(300, 3, "骨架+模型名粗估"),
               "EffectiveCeiling": 28835, "ClampRounds": 0},
    "Cuts": [],
    # 这一发没挤过任何东西，所以通知没发、段表里也没有那一段 —— 三处得一致（内核④会查）
    "Notice": {"Sent": False, "Trimmed": False, "Why": "这一发没折叠、没裁尾、没整段丢弃", "Text": ""},
}

OUTPUT1 = {
    "Call": 1, "Streaming": False,
    "Finish": {"Reason": "stop", "Said": True},
    "Model": "some-model", "ModelSaid": True,
    # 对端在响应体顶层自报的 object —— 只能转述，不许填我们自己那套接线常量
    "WireFormat": "chat.completion", "WireFormatSaid": True,
    "Says": {"Bytes": len(TEXT.encode("utf-8")), "Kept": len(TEXT.encode("utf-8")), "SHA256": SHA},
    "UsageIn": measure(5, 2, "端点响应的 usage.prompt_tokens"),
    "UsageOut": measure(2, 2, "端点响应的 usage.completion_tokens"),
    "WireBytesOut": measure(180, 1), "WireBytesIn": measure(96, 1),
    "Chunks": ABSENT("这一发不是流式的，结构上没有分帧"),
    "FirstByteMs": ABSENT("这一发不是流式的，没有首帧这件事"),
    "MaxGapMs": ABSENT("这一发不是流式的，没有帧间"),
    "ToolCalls": 0, "Malformed": 0, "MalformedStreak": 0,
    "Failure": {"Kind": "", "Detail": "",
                "Status": ABSENT("这一发没故障，也就没有要记的状态码"),
                "RetryAfterMs": ABSENT("这一发没断，也就没有要等多久"), "Downgrade": False},
}


def main():
    K = Kernel("hello")
    ct, _, _ = K.call("kernel.celltable")
    say("1) 要格位表（别照文档抄，照它填）：%s" % (ct or "").splitlines()[0])

    chk, _, _ = K.call("kernel.check_call", {"session": "hello", "actor": "hello",
                                             "input": INPUT1, "output": OUTPUT1})
    say("2) 先问内核这一份数它认不认（不落账）：%s" % (chk or "").splitlines()[0])

    rec, _, _ = K.call("kernel.record_call", {"session": "hello", "actor": "hello",
                                              "input": INPUT1, "output": OUTPUT1})
    say("3) 落账：%s" % (rec or "").splitlines()[0])

    dup, _, _ = K.call("kernel.record_call", {"session": "hello", "actor": "hello",
                                              "input": INPUT1, "output": OUTPUT1})
    say("4) 同一发再记一遍（去重门）：%s" % (dup or "").splitlines()[0])

    rb, _, _ = K.call("kernel.read_back", {"session": "hello", "call": 1, "side": "output"})
    heads = (rb or "").splitlines()
    say("5) 读回来第 1 发输出侧：%s" % (heads[0] if heads else ""))
    for l in heads[1:]:
        if l.strip().startswith(("wire_format", "fail_status", "model_says_sha256")):
            say("     " + l.strip()[:96])

    vf, _, _ = K.call("kernel.verify")
    say("6) 重算整条链：%s" % (vf or "").splitlines()[0])
    ta, _, _ = K.call("kernel.tally")
    say("7) 按出处分桶：%s" % (ta or "").splitlines()[0])
    rr, _, _ = K.call("kernel.readers_report")
    say("8) 谁读过哪些格（这一栏内核自己核不到）：%s" % (rr or "").splitlines()[0][:110])
    K.close()
    io.open(REPORT, "w", encoding="utf-8").write("\n".join(lines) + "\n")
    say("report written (utf-8).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
