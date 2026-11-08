# -*- coding: utf-8 -*-
"""attracta_probe —— #32 方案 1：产物原像住在链外（内容寻址），账里那一格只留指纹，对质时三方比。

为什么是这一支：三臂跑到 C 臂卡住的形状是实测过的（`报告/缺口-账里没有产物原像.txt`）——
账里每格的 `payload_b64` 存的是"那一格自己的值"，产物的**原话从来没进过这本账**，
于是"改了内容、指纹没重算"这种坏法在账里没有落点。加一格装原文要破 §8 的封板标记，
而它救不了"已有那格指纹与内容之间没有判据"这件事（还顺手把脱敏契约从"我们写的句子"扩到"模型说的一切"）。

方案 1 不加格：
    文件（原像，文件名 = 它的 sha256）  ←→  manifest（把摘要与「哪一格」连起来）  ←→  账（`model_output:model_says_sha256`）
对质 = 三方各比一次，缺哪一方就说哪一句。借的是 in-toto/SLSA provenance 的语义
（按摘要寻址，不按路径寻址），不自己发明一套。

判分线（**跑之前**写死在这里，不许跑完再定）：
  P0  三方一致                      → 必须报「对质过」，且核到的格数由脚本自己印；
  C① 改文件内容、文件名与 manifest 都不动 → 必须报「文件与它的摘要不符」，不许读成过；
  C② 改账上那一格的指纹（连 payload 的 sha 一起改，让它"看起来自洽"）
                                   → 必须被拒，并点名是被**链/整格 seal** 拒的还是被**三方比**拒的；
  C③ 原像文件不在场（删掉）         → 必须报「核不了」—— 既不许喊「被人改过」，也不许打 ✓。
任何一条不成立 → 这一族还是"没副手铐"，不许把跑完当过。

跑法（不需要编译，只要一个能打的 MCP-HTTP 面；它自己起进程、自己收）：
    python tasks/probe/attracta_probe.py [go 编好的 mcphttp.exe 路径]
"""
import base64
import hashlib
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
EXE = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
PORT = 8795
URL = "http://127.0.0.1:%d/mcp" % PORT
WORK = os.path.join(os.environ.get("TEMP", "/tmp"), "attracta")
CELL = "model_output:model_says_sha256"   # 那一格就是手铐的另一头
KEY = "model_says_sha256"

# 题面：唯一客观答案由**真运行**给出，两份自述冲突时只能由运行终结（与 artifact_probe 同一族）。
CODE = "print(sum([i for i in range(100) if i % 3 == 0 or i % 5 == 0]))"
CLAIM_WRONG = "4711"

out = []


def w(s):
    out.append(s)
    sys.stdout.buffer.write(s.encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
    sys.stdout.flush()


def call(name, args, rid=1):
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": "tools/call",
                       "params": {"name": name, "arguments": args}}).encode("utf-8")
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json"})
    rep = json.loads(urllib.request.urlopen(req, timeout=60).read().decode("utf-8"))
    res = rep.get("result", {})
    return (res.get("content") or [{}])[0].get("text", ""), bool(res.get("isError"))


def hex64(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def legal_call(side_stdout_text):
    """一份已知能落账 49 条的装配（照 hello_kernel 的形状，不自己造 49 格）。"""
    n = len(side_stdout_text.encode("utf-8"))
    parts = [{"Kind": "system", "Parts": 1, "Bytes": 11}, {"Kind": "user", "Parts": 1, "Bytes": 9}]
    return {
        "session": "attracta", "actor": "probe",
        "input": {"Call": 1, "Order": ["system", "user"], "Parts": parts, "ContentBytes": 20,
                  "Tokens": {"N": 7, "Basis": 3, "Source": "1 token 约 3 字节的粗估", "Absent": False,
                             "Refused": False, "Why": ""},
                  "Budget": {"CallBytesCeiling": 32768, "TransportSlackPct": 12, "ContentCeiling": 28835,
                             "Shares": {"system": 55, "user": 45}, "LeftoverPct": 0,
                             "FixedOverhead": {"N": 300, "Basis": 3, "Source": "骨架+模型名粗估",
                                               "Absent": False, "Refused": False, "Why": ""},
                             "EffectiveCeiling": 28835, "ClampRounds": 0},
                  "Cuts": [],
                  "Notice": {"Sent": False, "Trimmed": False, "Why": "这一发没折叠、没裁尾、没整段丢弃",
                             "Text": ""}},
        "output": {"Call": 1, "Streaming": False, "Finish": {"Reason": "stop", "Said": True},
                   "Model": "attracta-probe", "ModelSaid": True,
                   "WireFormat": "chat.completion", "WireFormatSaid": True,
                   # `Says` 没有 Clipped 这个字段（`Says.Clipped()` 是个方法）—— 10-17 #46 落地之后
                   # 写进来会被当场拒收；在那之前它被静默丢掉，我自己这支靶场也中过这一枪。
                   "Says": {"Bytes": n, "Kept": n, "SHA256": hex64(side_stdout_text)},
                   "UsageIn": {"N": 5, "Basis": 2, "Source": "端点响应的 usage.prompt_tokens",
                               "Absent": False, "Refused": False, "Why": ""},
                   "UsageOut": {"N": 2, "Basis": 2, "Source": "端点响应的 usage.completion_tokens",
                                "Absent": False, "Refused": False, "Why": ""},
                   "WireBytesOut": {"N": 180, "Basis": 1, "Source": "", "Absent": False, "Refused": False, "Why": ""},
                   "WireBytesIn": {"N": 96, "Basis": 1, "Source": "", "Absent": False, "Refused": False, "Why": ""},
                   "Chunks": {"N": 0, "Basis": 0, "Source": "", "Absent": True, "Refused": False, "Why": "这一发不分帧"},
                   "FirstByteMs": {"N": 0, "Basis": 0, "Source": "", "Absent": True, "Refused": False, "Why": "这一发不分帧"},
                   "MaxGapMs": {"N": 0, "Basis": 0, "Source": "", "Absent": True, "Refused": False, "Why": "这一发不分帧"},
                   "ToolCalls": 0, "Malformed": 0, "MalformedStreak": 0,
                   "Failure": {"Kind": "", "Detail": "",
                               "Status": {"N": 0, "Basis": 0, "Source": "", "Absent": True, "Refused": False,
                                          "Why": "这一发没故障，也就没有要记的状态码"},
                               "RetryAfterMs": {"N": 0, "Basis": 0, "Source": "", "Absent": True, "Refused": False,
                                                "Why": "这一发没断，也就没有要等多久"},
                               "Downgrade": False}},
    }


def ledger_digest():
    """从账上把那一格的指纹读回来（读侧第一次被拿来下判定，而不是只被写）。"""
    txt, is_err = call("kernel.read_back", {"session": "attracta", "call": 1, "side": "output"})
    if is_err:
        return None, "read_back 被拒：" + txt[:120]
    for line in txt.splitlines():
        if line.strip().startswith(KEY):
            m = re.search(r"\b([0-9a-f]{64})\b", line)
            if m:
                return m.group(1), line.strip()[:96]
    return None, "那一格没在读回来的输出里出现：" + txt[:120]


def confront(manifest):
    """三方比：文件（原像）↔ manifest（摘要与那一格）↔ 账（那一格的指纹）。缺哪一方说哪一句。"""
    path = os.path.join(WORK, manifest["subject_sha256"] + ".preimage")
    if not os.path.exists(path):
        return "核不了", "原像不在场（文件 %s 不存在）—— 这不是篡改，是手里的字节不足以重算" % os.path.basename(path)
    with open(path, "rb") as f:
        raw = f.read()
    file_digest = hashlib.sha256(raw).hexdigest()
    if file_digest != manifest["subject_sha256"]:
        return "不符", "文件与它自己的摘要不符：文件算出 %s…，manifest 记的是 %s…（内容被改过，摘要没重算）" % (
            file_digest[:12], manifest["subject_sha256"][:12])
    cell_digest, shown = ledger_digest()
    if cell_digest is None:
        return "核不了", shown
    if cell_digest != manifest["subject_sha256"]:
        return "不符", "账上那一格与 manifest 指的不是同一个东西：账 %s… vs manifest %s…（读回来的那行：%s）" % (
            cell_digest[:12], manifest["subject_sha256"][:12], shown)
    return "过", "三方一致：文件摘要 = manifest = 账上 %s；账里的原文字节数 %d 与文件实长 %d 也对得上" % (
        CELL, manifest["bytes"], len(raw))


def main():
    if not os.path.exists(EXE):
        w("NO_BINARY：先 go build -o ../.bin/mcphttp.exe ./cmd/mcphttp（本机编到哪就用哪份，别拿旧字节读数）")
        return 3
    os.makedirs(WORK, exist_ok=True)
    md5 = hashlib.md5(open(EXE, "rb").read()).hexdigest()
    srv = subprocess.Popen([EXE, "-listen", "127.0.0.1:%d" % PORT], cwd=ROOT,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(25):
            try:
                call("kernel.celltable", {})
                break
            except Exception:
                time.sleep(1)
        w("== 产物对质·链外原像（#32 方案 1） · 用的字节 md5=%s · exe=%s" % (md5, os.path.basename(EXE)))
        w("题面：%s" % CODE)

        r = subprocess.run([sys.executable, "-c", CODE], capture_output=True, text=True, timeout=60)
        stdout = (r.stdout or "").strip()
        w("真运行：exit=%d stdout=%r（自述给的是另一个值 %r，谁终结这一发由运行说了算）" % (r.returncode, stdout, CLAIM_WRONG))
        if r.returncode != 0:
            w("!! 真运行没跑成，这一支不算数（不许把跑不通读成通过）")
            return 4

        # ① 原像进链外：文件名 = 它的摘要（内容寻址；改内容必然对不上名）
        digest = hex64(stdout)
        with open(os.path.join(WORK, digest + ".preimage"), "wb") as f:
            f.write(stdout.encode("utf-8"))
        manifest = {"subject_sha256": digest, "bytes": len(stdout.encode("utf-8")),
                    "recorded_in": CELL, "session": "attracta", "call": 1,
                    "builder": "sys.executable -c", "code": CODE}
        with open(os.path.join(WORK, digest + ".manifest.json"), "w", encoding="utf-8") as f:
            json.dump(manifest, f, ensure_ascii=False, indent=1)

        # ② 账里只留指纹
        txt, is_err = call("kernel.record_call", legal_call(stdout))
        w("落账（账里存的是指纹，不是原话）→ isErr=%s：%s" % (is_err, txt[:200]))
        if is_err:
            w("!! 底样没落进去，后面三条臂都没有参照物（先修装配，别改判据）")
            return 5

        state, why = confront(manifest)
        w("P0 三方一致      → %s：%s" % (state, why))

        # C① 改文件内容，文件名与 manifest 都不动（最常见那一族：内容改了、摘要没重算）
        with open(os.path.join(WORK, digest + ".preimage"), "wb") as f:
            f.write(CLAIM_WRONG.encode("utf-8"))
        state1, why1 = confront(manifest)
        w("C① 改内容留摘要  → %s：%s" % (state1, why1))
        os.remove(os.path.join(WORK, digest + ".preimage"))
        with open(os.path.join(WORK, digest + ".preimage"), "wb") as f:
            f.write(stdout.encode("utf-8"))

        # C② 改账上那一格的指纹，并连 payload 自带的 sha 一起改（"看起来自洽"的那种改法）
        e_txt, _ = call("kernel.encode_journal", {"payload": "included"})
        book = "\n".join([l for l in e_txt.splitlines()[1:] if l.strip()])
        lines, tampered, hit = [], 0, 0
        for line in book.splitlines():
            try:
                d = json.loads(line)
            except Exception:
                lines.append(line)
                continue
            if d.get("kind") == CELL:   # 逐格落账时条目名就拼在 kind 上：<kind>:<key>
                hit += 1
                lie = json.dumps(CLAIM_WRONG, ensure_ascii=False).encode("utf-8")
                d["payload_b64"] = base64.b64encode(lie).decode("ascii")
                if "payload_sha256" in d:
                    d["payload_sha256"] = hashlib.sha256(lie).hexdigest()
                tampered += 1
            lines.append(json.dumps(d, ensure_ascii=False))
        w("账里那一格（%s）找到 %d 条、改了 %d 条" % (CELL, hit, tampered))
        if hit == 0:
            w("!! 那一格都没找到 —— 这一臂不算跑成（对质的格选错了，不许当拒住了）")
            state2 = "选错格"
        else:
            l_txt, l_err = call("kernel.load_journal", {"text": "\n".join(lines) + "\n", "acknowledge": "replace"})
            w("C② 改账上指纹   → 载入 isErr=%s：%s" % (l_err, l_txt[:240]))
            v_txt, _ = call("kernel.verify", {})
            w("   载入后 verify：%s" % v_txt[:240])
            state2 = "拒" if (l_err or ("✗" in v_txt) or ("不符" in v_txt)) else "过了（这条路还开着）"
            w("C② 结论        → %s" % state2)
            # 载入被拒时账本没被顶掉；若真顶掉了，recompute 的那一方才会说话
            if not l_err:
                state2b, why2b = confront(manifest)
                w("   C② 之后再三方比 → %s：%s" % (state2b, why2b))

        # C③ 原像不在场：三态里的那一态，不许塌成 ✓ 也不许塌成 ✗
        os.remove(os.path.join(WORK, digest + ".preimage"))
        state3, why3 = confront(manifest)
        w("C③ 原像不在场   → %s：%s" % (state3, why3))

        w("")
        w("自印分母：臂 4 条（P0/C①/C②/C③）；跑成 %d 条；核到那一格 %d 次" % (
            4 if hit else 3, hit))
        w("判分线（跑前定的）：P0 必须是「过」；C① 必须是「不符」；C② 必须被拒且点名是谁拒的；")
        w("C③ 必须是「核不了」——不许读成通过，也不许喊成被人改过。")
        return 0
    finally:
        srv.terminate()
        try:
            srv.wait(timeout=10)
        except Exception:
            srv.kill()
        report = os.path.join(ROOT, "报告", "产物对质-链外原像-1017.txt")
        open(report, "w", encoding="utf-8").write("\n".join(out) + "\n")
        print("REPORT=" + report.encode("unicode_escape").decode("ascii"))


if __name__ == "__main__":
    sys.exit(main())
