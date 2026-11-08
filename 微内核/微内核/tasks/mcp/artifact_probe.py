# -*- coding: utf-8 -*-
"""artifact_probe —— 把"自述冒充产物"这条路当场堵死，用一次**真运行**的产物当对质物。

为什么要有它：2026-10-15 一场 8 题盲测（A 纯模型 8/8、B 带 agentkernel 6/8）给出的根因是
**工具域错位**——内核的 Check() 只管"这一格的出身与自洽"，它不跑代码、不判语义。
那句被写进报告的硬结论我们接得住：**两份模型自述冲突时，唯一终结者是确定性产物。**
这一支就测这一句：产物由**真执行器**产出（`sys.executable -c` 起一个子进程跑死数），
自述由脚本文本给一个**不同的值**，内核必须只认能重算的那一份。

三条臂（判分线**跑之前**写在文件里，不许跑完再定）：

  A 只有自述            → 落账时 basis 只能标 `model_says`，若标 `measured` 必须被拒；
  B 真运行的 stdout     → `measured` + payload 交进去，链必须绿，且第三方重算得出同一个数；
  C 种坏（两种都种）    → ①改 stdout 一个字符、**留原指纹** → 必须 `✗ payload 与它自己的指纹不符`；
                          ②改 stdout 同时改指纹            → 必须 `✗ 整格 seal 重算不符`。

跑不过任何一条，就不是"效果不好"，是**闸门在编程场景里根本没有裁决力** —— 那按 `产品定位.md`
结尾那条，该改的是定位，不是再加一格。

用法（整轮慢闸跑完之后再跑，别抢这台 4 GB 的机器；它不编译，只起一个自己的服务实例）：
    C:/Users/Admin/sdk/py314/python.exe tasks/mcp/artifact_probe.py
"""
import hashlib
import json
import os
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
PORT = 8794
URL = "http://127.0.0.1:%d/mcp" % PORT
EXE = os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
REPORT = os.path.join(ROOT, "报告", "产物对质-三臂.txt")
PY = sys.executable

# 这道题有唯一客观答案，且**答案由运行给出**：字典推导里两次 j 的赋值不重叠，
# 所以第二次 `j=9` 不影响第一次写进去的值 —— 网上十个模型里有说 0 有说 9 的，
# 这正是"两份自述冲突、只能靠产物终结"那一族。
CODE = "print(0.1 + 0.2 == 0.3)"
CLAIM_Wrong = "True"   # 一份自述（浮点直觉那一类）
CLAIM_Other = "False"  # 另一份自述（按 IEEE754 推的那一类）


def call(name, args, rid=1):
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": "tools/call",
                       "params": {"name": name, "arguments": args}}).encode("utf-8")
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json"})
    rep = json.loads(urllib.request.urlopen(req, timeout=60).read().decode("utf-8"))
    res = rep.get("result", {})
    txt = (res.get("content") or [{}])[0].get("text", "")
    return txt, bool(res.get("isError"))


out = []


def w(s):
    out.append(s)
    print(s.encode("unicode_escape", "backslashreplace").decode("ascii"))  # 控制台保持 ASCII


def main():
    if not os.path.exists(EXE):
        print("NO_BINARY: 先 go build -o ../.bin/mcphttp.exe ./cmd/mcphttp")
        sys.exit(3)
    # 装配模板**不自己造**：`hello_kernel.py` 那份是已知能落账 49 条的（README §2.5 那条实测），
    # 自己拼一份 49 格的话，红起来分不清是判据拒的还是我拼错了 —— 这正是 #20 那一族。
    sys.path.insert(0, os.path.join(ROOT, "tasks", "mcp"))
    import client  # noqa: F401  —— 只要 hello_kernel 的模板，不用它的进程
    import hello_kernel as HK

    def with_tokens_claimed(inp):
        """A 臂：把模型自述的 token 数标成 measured（1）——内核有一条判据专拦这个。"""
        d = json.loads(json.dumps(inp))
        d["Tokens"] = {"N": d["Tokens"]["N"], "Basis": 1,
                       "Source": "端点响应的 usage.total_tokens", "Absent": False,
                       "Refused": False, "Why": ""}
        return d

    def with_says_claimed(inp, out, claimed_stdout):
        """B/C 臂用的装配：把真运行的 stdout 当产物挂进 Says（SHA 与字节数都按真 stdout 算）。"""
        d = json.loads(json.dumps(out))
        sha = hashlib.sha256(claimed_stdout.encode("utf-8")).hexdigest()
        d["Says"] = {"Bytes": len(claimed_stdout.encode("utf-8")),
                     "Kept": len(claimed_stdout.encode("utf-8")), "SHA256": sha}
        return d

    srv = subprocess.Popen([EXE, "-listen", "127.0.0.1:%d" % PORT], cwd=ROOT,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(20):
            try:
                call("kernel.celltable", {})
                break
            except Exception:
                time.sleep(1)
        w("== 产物对质三臂 ==")
        w("题目：%s → 唯一答案由真运行给出" % CODE)
        w("两份自述：%r 与 %r（同一份代码、同一个环境，只能靠运行终结）" % (CLAIM_Wrong, CLAIM_Other))

        # —— 真运行一次：stdout 就是产物，不是谁的自述
        r = subprocess.run([PY, "-c", CODE], capture_output=True, text=True, timeout=60)
        stdout = (r.stdout or "").strip()
        w("真运行：exit=%d stdout=%r stderr=%r" % (r.returncode, stdout, (r.stderr or "").strip()[:60]))
        if r.returncode != 0 or stdout not in (CLAIM_Wrong, CLAIM_Other):
            w("!! 真运行没给出可判的答案，这一支的题面要重选（不许把跑不通当跑过）")

        a_txt, a_err = call("kernel.check_call", {
            "session": "artifact", "actor": "probe",
            "input": with_tokens_claimed(HK.INPUT1), "output": HK.OUTPUT1})
        w("A 臂（把模型自述的 token 标成 measured）→ isErr=%s：%s" % (a_err, a_txt[:220]))

        b_in, b_out = HK.INPUT1, with_says_claimed(HK.INPUT1, HK.OUTPUT1, stdout)
        b_txt, b_err = call("kernel.record_call",
                            {"session": "artifact", "actor": "probe", "input": b_in, "output": b_out})
        w("B 臂（真 stdout=%r 落账）→ isErr=%s：%s" % (stdout, b_err, b_txt[:180]))
        v_txt, _ = call("kernel.verify", {})
        w("B 臂之后 verify：%s" % v_txt[:200])
        e_txt, _ = call("kernel.encode_journal", {"payload": "included"})
        book = "\n".join([l for l in e_txt.splitlines()[1:] if l.strip()])
        w("encode_journal：%s（交调用方写盘，内核不写）" % e_txt.splitlines()[0])

        # C 臂两种坏法：账里每一格存的是 payload 的 base64 加它的 sha256。
        # ① 只换 payload（自述冒充产物最常见的形状：内容改了、指纹没重算）
        # ② 连 sha 一起换（改得"看起来自洽"，但整格 seal 重算会对不上）
        import base64
        lines = [l for l in book.splitlines() if l.strip()]
        hit, bad1_lines, bad2_lines = 0, [], []
        for l in lines:
            try:
                d = json.loads(l)
            except Exception:
                bad1_lines.append(l); bad2_lines.append(l); continue
            pb = d.get("payload_b64")
            raw = base64.b64decode(pb) if pb else b""
            if raw.decode("utf-8", "replace").strip() == stdout:
                hit += 1
                lie_b64 = base64.b64encode(CLAIM_Wrong.encode("utf-8")).decode("ascii")
                d1 = dict(d); d1["payload_b64"] = lie_b64
                d2 = dict(d1)
                d2["payload_sha256"] = hashlib.sha256(CLAIM_Wrong.encode("utf-8")).hexdigest()
                bad1_lines.append(json.dumps(d1, ensure_ascii=False))
                bad2_lines.append(json.dumps(d2, ensure_ascii=False))
            else:
                bad1_lines.append(l); bad2_lines.append(l)
        w("账里以真 stdout=%r 当 payload 的格数：%d（这些就是要对质的那一格）" % (stdout, hit))
        if hit == 0:
            w("!! 一格都没找到 —— 两臂 C 都没跑成，不许当拒住了（对质的格选错了）")
            # 这不是判据弱，是**对质物压根不在账里**（产物只有长度与指纹，没有原话 —— 缺口 #32）。
            # 这一族现在改住链外：原像按内容寻址放在账外，账里那一格只留指纹，对质时文件↔manifest↔账三方比。
            # 跑法：python tasks/probe/attracta_probe.py <mcphttp.exe>（读数 报告/产物对质-链外原像-1017.txt）
            w("   这一族的对质已经改住链外（原像内容寻址 + 三方比）：tasks/probe/attracta_probe.py")
        else:
            for label, body in (("C① 改 payload、留原指纹", NL.join(bad1_lines)),
                                ("C② 改 payload、连指纹一起改", NL.join(bad2_lines))):
                t, is_err = call("kernel.load_journal", {"text": body + NL, "acknowledge": "replace"})
                w("%s -> isErr=%s：%s" % (label, is_err, t[:260]))
                vv, _ = call("kernel.verify", {})
                w("   载入后 verify：%s" % vv[:260])
        w("")
        w("判分线（跑前定的）：A 必须 isErr 且点名是哪条判据；B 必须落账、verify 绿且比对过原像；")
        w("C①②必须都被拒 —— 漏过去任何一种，就是「自述能冒充产物」这条路还开着。")
        w("任何一条不成立 → 内核在编程场景里没有裁决力；按产品定位结尾那条，该改的是定位。")
    finally:
        srv.terminate()
        try:
            srv.wait(timeout=10)
        except Exception:
            srv.kill()
        open(REPORT, "w", encoding="utf-8").write("\n".join(out) + "\n")
        print("REPORT written (utf-8): %s" % REPORT.encode("unicode_escape").decode("ascii"))


if __name__ == "__main__":
    main()
