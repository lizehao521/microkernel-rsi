# -*- coding: utf-8 -*-
# client.py —— 挂载后的内核的**唯一**一个驱动客户端。
#
# 为什么要有这个文件：drive.py（单进程 23 条判定）和 fork.py（跨进程分叉实验）
# 都要做同一件事——起进程、发换行分隔的 JSON-RPC、把 MCP 的 content[0].text 拆出来。
# 两处各写一遍，就会有两套「我以为服务端长什么样」：服务端改了协议，
# 一个脚本先红、另一个继续绿，那比没有测试更糟。
import json
import os
import shutil
import subprocess
import time

# go 装在哪每台机器不一样（README §1 那句）。写死一个绝对路径的代价是实测过的：
# 10-17 换了一台机器（用户目录不叫 Administrator），drive.py 直接 FileNotFoundError ——
# 那既不是绿也不是红，是"没跑成"。顺序：环境变量 → PATH 上的 go → 文档里那个默认值。
def _gobin():
    p = os.environ.get("AGENTKERNEL_GOBIN")
    if p:
        return p
    w = shutil.which("go")
    if w:
        return os.path.dirname(os.path.realpath(w))
    return r"C:\Users\Admin\sdk\go\bin"


GOBIN = _gobin()
_BUILT = False


class Kernel:
    """一个挂载实例 = 一个进程 = 一本活在它记忆里的账。

    同时起两个 Kernel 就是在测「两个进程各写各的」那一半 —— 内核自己拦不住，
    这一条不是缺陷清单里的新项，是 README 缺口第 6 条与第 8 条剩下的那一半。
    """

    def __init__(self, tag, sink=None):
        self.tag = tag
        self.sink = sink or (lambda s: None)
        self.exe = os.path.join(os.environ.get("TEMP", "/tmp"), "mcpkernel.exe")
        self.root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
        self._id = 0
        self._build()
        self.proc = subprocess.Popen([self.exe], stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, bufsize=0, cwd=self.root)
        self.rpc("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                                "clientInfo": {"name": tag, "version": "1"}})
        self.rpc("notifications/initialized")

    def _build(self):
        # 每个 Python 进程只编一次：一是 Windows 不允许覆盖正在运行的 exe
        # （第二个 Kernel 起不来就是因为这个），二是**不许拿上一次留下的旧二进制当本轮的结果**。
        global _BUILT
        if _BUILT:
            return
        env = dict(os.environ)
        env["PATH"] = env.get("PATH", "") + os.pathsep + GOBIN
        r = subprocess.run([os.path.join(GOBIN, "go.exe"), "build", "-o", self.exe, "./cmd/mcpkernel"],
                           cwd=self.root, capture_output=True, env=env)
        if r.returncode != 0:
            raise SystemExit("BUILD FAILED: " + r.stderr.decode("utf-8", "replace"))
        _BUILT = True

    def rpc(self, method, params=None):
        self._id += 1
        rid = self._id
        msg = {"jsonrpc": "2.0", "id": rid, "method": method}
        if params is not None:
            msg["params"] = params
        raw = (json.dumps(msg, ensure_ascii=False) + "\n").encode("utf-8")
        t0 = time.monotonic()
        self.proc.stdin.write(raw)
        self.proc.stdin.flush()
        line = self.proc.stdout.readline()
        ms = int((time.monotonic() - t0) * 1000)
        if not line:
            raise SystemExit("SERVER DIED at request %d (%s)" % (rid, method))
        rep = json.loads(line.decode("utf-8"))
        if "result" in rep:
            c = rep["result"].get("content")
            txt = c[0]["text"] if c else json.dumps(rep["result"], ensure_ascii=False)
            iserr = bool(rep["result"].get("isError"))
        else:
            txt = json.dumps(rep.get("error", {}), ensure_ascii=False)
            iserr = True
        self.sink(">>> [%s] #%d %s %s" % (self.tag, rid, method,
                                    json.dumps(params or {}, ensure_ascii=False)))
        self.sink("<<< %dms isError=%s\n%s\n" % (ms, iserr, txt))
        return txt, iserr, ms

    def call(self, tool, args=None):
        return self.rpc("tools/call", {"name": tool, "arguments": args or {}})

    def close(self):
        try:
            self.proc.stdin.close()
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()


# ---- 「第 1 发」的共用样本 ----
# 两个脚本（drive.py 单进程判定、fork.py 跨进程分叉）断言的是同一个内核，
# 所以这一发的形状只许有一份：各写一遍，就会漂成两套「我以为内核收得下什么」。
# 数值全部取自 kresearch 那一趟真端点（报告/样例-真接口两发.jsonl 的第 1 发）。

UNSET = 0    # 没填 —— 缺席那一格必须带它，不许顺手塞一个出处
MEAS = 1     # BasisMeasured
EST = 3      # BasisEstimated


def m(n, basis, source="", absent=False, why=""):
    return {"N": n, "Basis": basis, "Source": source, "Absent": absent, "Refused": False, "Why": why}


def missing(why):
    """缺席的一格：N 必须是 0、出处必须是「没填」，理由单独带 —— 这是内核的三态。"""
    return m(0, UNSET, "", True, why)


INPUT1 = {
    "Call": 1,
    "Order": ["user", "truncation-notice"],
    # 内核的④：Notice.Sent=true 必须能在段表里数出那一段 —— 上一版这两处自相矛盾
    # （账说通知发了、Order 里只有 user），第一次真跑就把 drive/fork 整片拦下。
    "Parts": [{"Kind": "user", "Parts": 1, "Bytes": 42},
              {"Kind": "truncation-notice", "Parts": 1, "Bytes": 15}],
    "ContentBytes": 57,
    "Tokens": m(19, EST, "1 token ≈ 3 字节的粗估"),
    "Budget": {
        "CallBytesCeiling": 32768,
        "TransportSlackPct": 12,
        "ContentCeiling": 28835,
        "Shares": {"user": 74, "truncation-notice": 26},
        "LeftoverPct": 0,
        "FixedOverhead": m(300, EST, "骨架+模型名+工具清单的粗估"),
        "EffectiveCeiling": 28835,
        "ClampRounds": 0,
    },
    "Cuts": [],
    "Notice": {"Sent": True, "Trimmed": False, "Why": "", "Text": "本节为节选"},
}

OUTPUT1 = {
    "Call": 1,
    "Streaming": False,
    "Finish": {"Reason": "", "Said": False},
    "Model": "", "ModelSaid": False,
    "WireFormat": "", "WireFormatSaid": False,
    "Says": {"Bytes": 49, "Kept": 49, "SHA256": "a465c216022f7a4d57de3e76e128ad39cceeef1b09fef4169f72c3c487f2d482"},
    "UsageIn": missing("端点响应里没有 usage 字段，不由我们这端的分词"),
    "UsageOut": missing("端点响应里没有 usage 字段，不由我们这端的分词"),
    "WireBytesOut": m(54, MEAS),
    "WireBytesIn": m(49, MEAS),
    "Chunks": missing("这一发不是流式的，结构上没有分帧"),
    "FirstByteMs": missing("这一发不是流式的，没有首帧这件事"),
    "MaxGapMs": missing("这一发不是流式的，没有帧间"),
    "ToolCalls": 0, "Malformed": 0, "MalformedStreak": 0,
    "Failure": {"Kind": "", "Detail": "",
             "Status": missing("这一发没故障，也就没有要记的状态码"),
                "RetryAfterMs": missing("这一发没断，也就没有要等多久"), "Downgrade": False},
}


class Judge:
    """判据：每一行是「我期望内核说什么」与「它实际说了什么」的对照，不是读着顺眼就算过。"""

    def __init__(self):
        self.rows = []

    def __call__(self, name, txt, needles, forbidden=()):
        ok = all(n in txt for n in needles) and not any(f in txt for f in forbidden)
        self.rows.append((name, "PASS" if ok else "FAIL",
                          "" if ok else "期望 %r 去掉 %r，实际：%s" % (needles, forbidden, txt[:400])))
        return ok

    @property
    def red(self):
        return [r for r in self.rows if r[1] == "FAIL"]

