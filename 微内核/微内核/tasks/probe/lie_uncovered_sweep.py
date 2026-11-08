# -*- coding: utf-8 -*-
"""把覆盖账面里「一次都没撞过」的那批键名，逐格拿一个坏值撞一遍。

上一支（`cell_arm_coverage.py`）数出：48 个键名里 25 个有条臂撞过、**23 个从来没撞过**。
这一支就把那 23 个撞掉，好让 #56 那句「哪一格没有闸」第一次有全表读数，而不是"跑了挺多轮"。

**这一支不预判「该不该拒」**（那是作者的判据设计权），只印三态：
  被拒（附撞中的模板 file:line + 原话）｜ 被收下（这一格今天没有任何闸碰它）｜ 造不出值（类型不认识，标 — ）。
值是按类型机械造的（int→-1、str→一段自带「演示」字样的哨兵串、bool→取反、配对格联动改 said/依赖格），
所以**红的是"这格没人管"，不是"这格值不合契约"** —— 后者要 #47/#42 那种判据先定下来。

未撞集合不许我抄：跑时现取 `kernel.celltable`，再从三份报告里按各自的排版抠出已撞键名（抠法与 `cell_arm_coverage.py` 同一套）。
正对照（钉这支尺子）：`tool_calls` 与 `model_says_bytes` 这两格造负数**必须被拒**，
因为它们的规则今天就读得到（`output.go:306`、`output.go:378`）；这两条只要有一条被收下，
说明调用路径或值没打进去 —— 整份表作废、退出非 0，不给你读"被收下 N 格"那句。
另有两道闸：撞到「同一发已经记过了」那句去重话的臂**单独计数并整份作废**（v1 就栽在这：session 取键名前 6 字，
`notice_text/notice_trimmed/notice_why` 三臂挤进同一个 session，第三臂拿到的是去重话、我却把它记成这格的闸）；
一个键名住两行时（`call` 输入侧+输出侧）每一行都要标出来，v1 只在「造不出值」那几行标、跑成的行没标。

跑法：python tasks/probe/lie_uncovered_sweep.py [mcphttp.exe 路径] [输出.txt]
"""
import hashlib
import io
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))

ROUNDS = [("说谎者靶场-五形状乘全格矩阵-1017.txt", "row1"),
          ("非Measure那批-运行时撞一遍-v2-1017.txt", "col2"),
          ("契约说进不了门-到底拒不拒-1017.txt", "script_field")]
BUDGET_SCRIPT = os.path.join(ROOT, "tasks", "probe", "contract_budget_arms.py")
SEP = "[\uff5c\u2502]"
CTL = ["tool_calls", "model_says_bytes"]


def setcell(d, path, val):
    cur = d
    for k in path[:-1]:
        cur = cur[k]
    cur[path[-1]] = val


def getcell(d, path):
    cur = d
    for k in path:
        if not isinstance(cur, dict) or k not in cur:
            return None, False
        cur = cur[k]
    return cur, True


def has_field(fp):
    """格表里的字段路径要能直接落到 fixture 的键上；带 # 的是动态档、没有的是方法格，都不猜。"""
    return "#" not in fp


def parse_rows(head):
    rows = {}
    for ln in head.splitlines()[1:]:
        if "←" not in ln:
            continue
        side_key, _, path = ln.partition("←")
        tok = side_key.split()
        if len(tok) == 2:
            rows[(tok[0], path.strip())] = tok[1]
    return rows


def covered_keys(rows):
    """照搬覆盖账面那套抠法（同一事实只认一种数法）。"""
    cov = set()
    per = {}
    for fn, mode in ROUNDS:
        p = os.path.join(ROOT, "报告", fn)
        if not os.path.exists(p):
            per[fn] = "报告不在"
            continue
        text = io.open(p, encoding="utf-8").read()
        ks = []
        if mode == "row1":
            for ln in text.splitlines():
                m = re.match(r"^([a-z][a-z0-9_]{2,})\s*" + SEP, ln)
                if m:
                    ks.append(m.group(1))
        elif mode == "col2":
            for ln in text.splitlines():
                parts = [x.strip() for x in re.split(SEP, ln)]
                if len(parts) >= 3 and re.fullmatch(r"[a-z][a-z0-9_]{2,}", parts[1] or ""):
                    ks.append(parts[1])
        else:
            by_tail = {}
            for (side, path), key in rows.items():
                by_tail.setdefault(path.split(".")[-1].split("#")[0], set()).add(key)
            for f in re.findall(r'^\s*\("[^"]+",\s*"([A-Z]\w+)"', io.open(BUDGET_SCRIPT, encoding="utf-8").read(), re.M):
                ks2 = by_tail.get(f)
                if ks2 and len(ks2) == 1:
                    ks.append(next(iter(ks2)))
        good = [k for k in ks if k in set(rows.values())]
        cov |= set(good)
        per[fn] = "%d 行 / 落进格表 %d" % (len(ks), len(set(good)))
    return cov, per


def make_bad(cur):
    """按现值的类型机械造一个坏值；类型不认识就返回 None（标 — ）。"""
    tag = "\u6f14\u793a\u503c\u00b7\u975e\u771f\u5b9e\u6d41\u91cf"
    if isinstance(cur, bool):
        return not cur, "bool 取反"
    if isinstance(cur, int):
        return -1, "int 造 -1"
    if isinstance(cur, float):
        return -1.0, "float 造 -1.0"
    if isinstance(cur, str):
        return tag, "str 造哨兵串"
    if isinstance(cur, dict):
        return None, "dict 不认（要逐子字段设计）"
    if isinstance(cur, list):
        if cur and isinstance(cur[0], dict):
            copy = json.loads(json.dumps(cur))
            n = 0
            for it in copy:
                for k, v in it.items():
                    if isinstance(v, int) and not isinstance(v, bool):
                        it[k] = -1
                        n += 1
                        break
                if n:
                    break
            if n:
                return copy, "list[dict] 里第一个整数改 -1"
        return None, "list 不认"
    if cur is None:
        return tag, "现值是 null，塞一段哨兵串"
    return None, "类型 %s 不认" % type(cur).__name__


def main():
    exe = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", ".bin", "mcphttp.exe")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "没撞过那23格-逐格撞一遍-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    import attracta_probe as AP
    port = 8811
    url = "http://127.0.0.1:%d/mcp" % port
    proc = subprocess.Popen([exe, "-listen", "127.0.0.1:%d" % port], cwd=ROOT,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    def finish(code):
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
        return code

    def call(tool, args):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                           "params": {"name": tool, "arguments": args}}).encode("utf-8")
        r = json.loads(urllib.request.urlopen(urllib.request.Request(
            url, data=body, headers={"Content-Type": "application/json"}), timeout=60).read().decode("utf-8"))
        res = r.get("result") or {}
        txt = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
        return txt, bool(res.get("isError"))

    try:
        time.sleep(1.2)
        head, _ = call("kernel.celltable", {})
        if "唯一键名" not in head:
            w("NOT_RUN：端点没答。")
            return 4
        rows = parse_rows(head)
        keys = set(rows.values())
        declared = int(re.search(r"\u5f53\u524d (\d+) \u683c", head).group(1))
        decl_keys = int(re.search(r"\u552f\u4e00\u952e\u540d (\d+) \u4e2a", head).group(1))
        cov, per = covered_keys(rows)
        todo = sorted(keys - cov)
        w("== 未撞那批逐格撞 · %s ｜ 字节 sha256=%s ==" % (
            time.strftime("%Y-%m-%d %H:%M:%S"), hashlib.sha256(io.open(exe, "rb").read()).hexdigest()[:16]))
        w("格表：抬头 %d 格 / %d 键 ｜ 抠到 %d 行 / %d 键 ｜ 对得上 = %s"
          % (declared, decl_keys, len(rows), len(keys), str(len(rows) == declared and len(keys) == decl_keys)))
        if len(rows) != declared or len(keys) != decl_keys:
            w("NOT_RUN：分母对不上。")
            return finish(4)
        w("已撞抠法（与 cell_arm_coverage.py 同一套）：")
        for fn, s in sorted(per.items()):
            w("   %s ｜ %s" % (fn, s))
        w("未撞 %d 个键名：%s" % (len(todo), " ".join(todo)))
        if len(todo) != 23:
            w("**注意**：账面那份印的是 23，这里数出 %d —— 两次取的字节或报告文件有一边变了，读数要并排看，别只信一边。"
              % len(todo))
        w("")
        base = json.loads(json.dumps(AP.legal_call("未撞那批这一发的真 stdout")))
        _, ce = call("kernel.check_call", {"session": "ctlU", "actor": "sweep", "input": base["input"], "output": base["output"]})
        _, re0 = call("kernel.record_call", {"session": "ctlU", "actor": "sweep", "input": base["input"], "output": base["output"]})
        w("控制臂（什么都没改）：check=%s record=%s —— 收不下就整批作废" % (ce, re0))
        if re0:
            return finish(5)
        w("")
        w("键名 ｜ 字段路径 ｜ 现值 → 造的值 ｜ check ｜ record ｜ 同判 ｜ 结果")
        w("-" * 78)
        refused, accepted, unsupported, deduped = [], [], [], []
        same_n = 0
        fired = {}
        for k in todo:
            hits = sorted([(s, p) for (s, p), kk in rows.items() if kk == k])
            side, fp = hits[0]
            path = (side,) + tuple(fp.split("."))
            multi = "" if len(hits) == 1 else "（这键名住 %d 行，这里只撞 %s.%s 那一行）" % (len(hits), side, fp)
            cur, present = getcell(base["input"] if side == "input" else base["output"], path[1:])
            if not has_field(fp) or not present:
                why = "动态档（路径带 #，要靠 Cuts 表构造）" if "#" in fp else "方法格（载荷里根本没这条路）"
                unsupported.append(k)
                w("%-20s ｜ %-26s ｜ %-22s ｜ ——  ｜ ——  ｜ —— ｜ **造不出值：%s**%s"
                  % (k, side + "." + fp, why, why, multi))
                continue
            val, how = make_bad(cur)
            if k == "parts":
                val = list(cur) + ["system"]
                how = "Order 多塞一段（让相加核对不上）"
            if val is None:
                unsupported.append(k)
                w("%-20s ｜ %-26s ｜ %-22s ｜ ——  ｜ ——  ｜ —— ｜ **造不出值：%s**%s"
                  % (k, side + "." + fp, how, how, multi))
                continue
            pack = json.loads(json.dumps(base))
            try:
                setcell(pack["input"] if side == "input" else pack["output"], path[1:], val)
            except Exception as e:
                unsupported.append(k)
                w("%-20s ｜ %-26s ｜ %-22s ｜ ——  ｜ ——  ｜ —— ｜ **打不进去：%r**%s" % (k, side + "." + fp, how, e, multi))
                continue
            # session 用整格名：v1 截了前 6 字，notice_text/trimmed/why 三臂挤进同一个 session，
            # 于是第三臂拿到的是「同一发记过两次」那句去重话 —— 拒了，但拒它的不是这格的判据。
            sid = "U" + k
            c_txt, c_err = call("kernel.check_call", {"session": sid, "actor": "sweep",
                                                     "input": pack["input"], "output": pack["output"]})
            r_txt, r_err = call("kernel.record_call", {"session": sid, "actor": "sweep",
                                                      "input": pack["input"], "output": pack["output"]})
            same = (c_err == r_err)
            same_n += same
            txt = (r_txt or c_txt or "").replace("\n", " / ")
            is_dedup = "已经记过了" in txt
            if is_dedup:
                deduped.append(k)
            (refused if r_err else accepted).append(k)
            if r_err:
                tail = txt.split("kernel: ")[-1][:58]
                fired[k] = tail
            w("%-20s ｜ %-26s ｜ %-22s ｜ %-5s ｜ %-5s ｜ %-4s ｜ %s%s"
              % (k, side + "." + fp, how, c_err, r_err, "同判" if same else "**分歧**",
                 ("被拒（去重话，不算这格的闸）：" if is_dedup else "被拒：") if r_err else "**被收下**",
                 (tail if r_err else "") + multi))
        w("")
        ctl_bad = [k for k in CTL if k in accepted]
        w("== 自核（钉这支尺子）==")
        w("正对照：`tool_calls`、`model_says_bytes` 造负数必须被拒（规则见 `output.go:306`、`output.go:378`）｜ 坏的：%s"
          % (" ".join(ctl_bad) or "无"))
        if ctl_bad:
            w("坏：这两条被收下了 —— 说明值没打进去或调用路径不对，这份表里的「被收下 N 格」全都不成立。")
            return finish(6)
        if same_n != len(todo) - len(unsupported):
            w("坏：预检与落账不同判（%d / %d），#20 那条不变量在这一批上不成立，读数要单列分歧。"
              % (same_n, len(todo) - len(unsupported)))
        w("")
        w("== 自印分母 ==")
        w("未撞待撞 %d 个 ｜ 造不出值 %d 个：%s ｜ 跑了 %d 发（预检+落账各一次 = %d 次调用）"
          % (len(todo), len(unsupported), " ".join(unsupported) or "无",
             len(todo) - len(unsupported), 2 * (len(todo) - len(unsupported))))
        w("三态：被这格的判据拒 %d 个 ｜ **被收下 %d 个**：%s ｜ 造不出值 %d 个 ｜ 被去重话挡下（不算闸）%d 个：%s"
          % (len(refused) - len(deduped), len(accepted), " ".join(accepted) or "无", len(unsupported),
             len(deduped), " ".join(deduped) or "无"))
        if deduped:
            w("坏：这几臂撞到的是「同一发记过两次」那句去重话 —— session/发号挤车，不是这格的判据；"
              "这一份里的「被拒 N 个」不能引用。")
            return finish(6)
        w("被拒的那批撞中的句子（原话尾巴，规则行号请对着 `报告/拒收模板带不带键名-静态全表-v3-实参逐条-1017.txt` 查）：")
        for k in refused:
            w("   %-20s ｜ %s" % (k, fired.get(k, "")))
        w("读法：**被收下 = 这一格今天没有任何判据碰它**（不预判它该不该有 —— 那是 #47/#42/#56 要拍的）。")
        w("       演示值里带「演示值·非真实流量」字样，别把它读成一次真流量。")
        return finish(0)
    finally:
        proc.terminate()


if __name__ == "__main__":
    sys.exit(main())
