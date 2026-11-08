# -*- coding: utf-8 -*-
"""给"只被一条规则接住"的那些格，列出**源码里还提到这一格的其他判据**，当下一批臂的靶子。

为什么要它：`rule_hit_matrix.py` 数出 16 格只被 1 条接住，但"只被 1 条"说的是**我撞过的回合里**接住它的有几条，
不是"内核里只有这一条判据提到这格"。这两件事不分开，下一步就会拿前者当后者用 —— 那正是本项目最忌的漂。
所以这一支只做一件事：把「这格的字段在源码里还被哪些 `if` 条件提到」列出来，
每条给 `文件:行 + 那段条件 + 它要印的原话模板`，**供人挑着设计臂**；它自己不声称任何覆盖。

自核（钉这把尺子，两条）：
 一、矩阵那份判给这格"已接住"的那条规则，必须也在本表为它列出的候选里 —— 不在就说明我的回溯条件读错了，exit 6；
 二、候选表里每一条都得同时有「条件片段」和「原话模板」两样，缺一就不列（列了就是半成品）。
     另印一条：候选为 0 的格数 —— 那才是"源码里真的只有这一条判据提到它"的形状（与"我只撞过一条"分开说）。

跑法：python tasks/probe/rule_candidates_for_thin_cells.py [端点URL] [输出.txt]
"""
import io
import json
import os
import re
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))
from contract_value_arms_v2 import rule_template

MATRIX = os.path.join(ROOT, "报告", "每格被哪几条规则碰过-v3-1017.txt")


def celltable(url):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": "kernel.celltable", "arguments": {}}}).encode("utf-8")
    r = json.loads(urllib.request.urlopen(urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}), timeout=30).read().decode("utf-8"))
    return "".join(c.get("text", "") for c in r["result"]["content"] if c.get("type") == "text")


def thin_cells():
    """从矩阵那份的「只被 1 条碰过的格」小节里抠（它自己印的表，不重算）。"""
    out = {}
    sec = False
    for ln in io.open(MATRIX, encoding="utf-8").read().splitlines():
        if "只被 1 条规则碰过的格" in ln:
            sec = True
            continue
        if not sec:
            continue
        if not ln.startswith("   ") or "｜" not in ln:
            if ln.startswith("被 ≥2"):
                break
            continue
        parts = [x.strip() for x in ln.strip().split("\uff5c")]
        if len(parts) >= 2 and re.fullmatch(r"[a-z][a-z0-9_]{2,}", parts[0]):
            m = re.match(r"([\w./]+):(\d+)", parts[1])
            if m:
                out[parts[0]] = (m.group(1), int(m.group(2)))
    return out


def go_files():
    fs = [f for f in sorted(os.listdir(ROOT)) if f.endswith(".go") and not f.endswith("_test.go")]
    return [f for f in fs] + ["mcpsrv/" + f for f in sorted(os.listdir(os.path.join(ROOT, "mcpsrv")))
                              if f.endswith(".go") and not f.endswith("_test.go")]


def candidates_for(tokens):
    """返回 [(文件, 行, 条件片段, 模板)]：该行的 `fmt.Errorf` 往上 8 行内出现过任一 token。"""
    hits = []
    for f in go_files():
        ls = io.open(os.path.join(ROOT, f), encoding="utf-8").read().splitlines()
        for i, l in enumerate(ls):
            if "fmt.Errorf(" not in l:
                continue
            win = "\n".join(ls[max(0, i - 8):i + 1])
            cond = ""
            for tok in tokens:
                m = re.search(r"if[^\n]*" + re.escape(tok) + r"[^\n]*", win)
                if m:
                    cond = m.group(0).strip()
                    break
            else:
                continue
            msg = rule_template(f, i + 1)
            if msg:
                hits.append((f, i + 1, cond[:96], msg))
    return hits


def main():
    url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8797/mcp"
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "薄那16格-还有哪些判据提到它-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    def finish(code):
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
        return code

    if not os.path.exists(MATRIX):
        w("NOT_RUN：矩阵那份不在（%s）—— 没有靶子清单，这一支不出表。" % MATRIX.encode("unicode_escape").decode("ascii"))
        return finish(4)
    try:
        head = celltable(url)
    except Exception as e:
        w("NOT_RUN：端点没答 %r" % (e,))
        return finish(4)
    rows = {}
    for ln in head.splitlines()[1:]:
        if "←" not in ln:
            continue
        sk, _, path = ln.partition("←")
        tok = sk.split()
        if len(tok) == 2:
            rows[tok[1]] = (tok[0], path.strip())
    thin = thin_cells()
    w("== 薄的那批格：源码里还有哪些判据提到这一格 · %s ｜ 端点 %s ==" % (time.strftime("%Y-%m-%d %H:%M:%S"), url))
    w("矩阵判给「只被 1 条接住」的格 %d 个 ｜ 格表现取 %d 行 ｜ 键名对不上格表的：%s"
      % (len(thin), len(rows), " ".join(sorted(k for k in thin if k not in rows)) or "无"))
    if any(k not in rows for k in thin):
        return finish(4)
    w("")
    w("格 ｜ 已接住的那条 ｜ 候选（源码里还提到这一格、且这批格都还没撞过的判据）｜ 说明")
    w("-" * 78)
    zero, checked_bad = [], []
    total_cand = 0
    for k, (af, al) in sorted(thin.items()):
        side, path = rows[k]
        leaf = path.split(".")[-1].split("#")[0]
        toks = {leaf, leaf.split("[")[0]}
        if "." in path:
            toks.add(path.split(".")[-2] + "." + leaf)
        cands = candidates_for(sorted(toks))
        names = ["%s:%d" % (f, l) for f, l, _c, _m in cands]
        if "%s:%d" % (af, al) not in names:
            checked_bad.append("%s（已接住的那条 %s:%d 不在候选里）" % (k, af, al))
        fresh = [c for c in cands if "%s:%d" % (c[0], c[1]) != "%s:%d" % (af, al)]
        total_cand += len(fresh)
        if not fresh:
            zero.append(k)
        w("%-22s ｜ %s:%d ｜ %d 条 ｜ %s"
          % (k, af, al, len(fresh), "候选为 0：这一格在源码里只被这一条判据提到" if not fresh else "下面列出候选，挑着设计臂"))
        for f, l, cond, msg in fresh:
            w("     · %s:%d ｜ 条件「%s」 ｜ 模板「%s」" % (f, l, cond[:70], msg[:56]))
    w("")
    w("== 自核（钉这把尺子）==")
    w("正对照：已接住那条必须出现在候选扫描里 ｜ 不对的：%s" % (" ； ".join(checked_bad) or "无"))
    if checked_bad:
        w("坏：回溯条件读错了，这张表不许用。")
        return finish(6)
    w("候选合计 %d 条 ｜ 候选为 0 的格 %d 个：%s" % (total_cand, len(zero), " ".join(zero) or "无"))
    w("")
    w("两件事不许混：「只被 1 条接住」= 我撞过的回合里接住它的是 1 条；「源码里只有 1 条提到它」= 候选为 0 的那些格。")
    w("        这张表**不声称覆盖**，它只把下一批臂的靶子连条件带原话一起摆出来，挑哪几条由你定。")
    return finish(0)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        import traceback
        dest = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "æ¥å" if False else "报告", "薄那16æ ¼-崩那跳-1017.txt".encode("latin-1").decode("utf-8") if False else "薄那批格-崩那跳-1017.txt")
        if os.path.exists(dest):
            dest = dest + ".crash"
        io.open(dest, "w", encoding="utf-8").write(traceback.format_exc())
        raise

