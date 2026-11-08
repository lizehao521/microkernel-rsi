# -*- coding: utf-8 -*-
"""#56 的账面：49 格里到底有几格被某一条臂**真撞过**，哪几格一次都没有。

为什么单独数这个：今天一天里我跑了四种靶场（Measure 五形状矩阵、预算逐字段、非 Measure 逐值、认领声明），
每一份都自印了自己的分母，但**合起来覆盖到哪些格**这件事今天没人数过 —— 而 #56 要回答的正是"还剩几格没撞"。
不数就只能说"跑了挺多轮"，那是散文。

格的分母不许我挑：跑时现取 `kernel.celltable` 的 49 行（`侧 键名 ← 字段路径`），行数与表头对不上就 NOT_RUN。
覆盖的归属从**各轮报告原文**里抠，不用我的记忆：
  · 五形状矩阵那份 —— 表每行第一个字段就是 snake 键名（那支脚本的 11 格本来就是源码现取的）；
  · 非 Measure v2 那份 —— 表每行第二列就是那一格的键名；
  · 预算那份 —— 报告里印的是 Go 字段名，改从**脚本的臂表**里抠第二个字段（`("A1 …", "ContentCeiling", 999999, 依据)`），
    再用格表的「字段路径 → 键名」翻成键名。
一轮对不上它的自印臂数（比如键名列到 8 行而分母写 6 条）就把这一轮标「归不进」，不悄悄并集。
第四轮（未撞那批 v2）只认结果列里写了「被拒/被收下」的行 —— 「造不出值」那几行是**第三种状态**，不算撞过。

三态在这里不许塌：**撞过** / **没撞过** / **这轮格式抠不出格名**。第三种既不算撞过也不算没撞过。

跑法：python tasks/probe/cell_arm_coverage.py [端点URL] [输出.txt]
"""
import io
import json
import os
import re
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
R = lambda *p: os.path.join(ROOT, "报告", *p)

ROUNDS = [
    ("五形状乘全格矩阵（Measure 那 11 格）",
     R("说谎者靶场-五形状乘全格矩阵-1017.txt"), "row1", r"跑了 (\d+) 发"),
    ("非 Measure 逐值 v2（8 臂）",
     R("非Measure那批-运行时撞一遍-v2-1017.txt"), "col2", r"臂 (\d+) 条"),
    ("契约 §1.4 预算逐字段（7 臂）",
     R("契约说进不了门-到底拒不拒-1017.txt"), "script_field", r"臂 (\d+) 条"),
    ("未撞那批逐格撞 v2（17 发）",
     R("没撞过那批-逐格撞一遍-v2-1017.txt"), "row1_result", r"跑了 (\d+) 发"),
    ("动态档那三格 Cuts 族 v3（17 谎臂）",
     R("动态档那三格-Cuts族撞一遍-v3-1017.txt"), "row1_result", r"谎臂 (\d+) 条"),
]
BUDGET_SCRIPT = os.path.join(ROOT, "tasks", "probe", "contract_budget_arms.py")


def fetch_celltable(url):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": "kernel.celltable", "arguments": {}}}).encode("utf-8")
    r = json.loads(urllib.request.urlopen(urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}), timeout=30).read().decode("utf-8"))
    return "".join(c.get("text", "") for c in r["result"]["content"] if c.get("type") == "text")


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


def keys_from_round(mode, text):
    """按该轮的排版抠键名；抠不到的行不猜。竖线两种码位都认（全角 ｜ 与制表 │）。"""
    SEP = "[\uff5c\u2502]"
    got = []
    for ln in text.splitlines():
        if mode == "row1":
            m = re.match(r"^([a-z][a-z0-9_]{2,})\s*" + SEP, ln)
            if m:
                got.append(m.group(1))
        elif mode == "col2":
            parts = [x.strip() for x in re.split(SEP, ln)]
            if len(parts) >= 3 and re.fullmatch(r"[a-z][a-z0-9_]{2,}", parts[1] or ""):
                got.append(parts[1])
        elif mode == "row1_result":
            # 只有真跑了的臂才算覆盖：结果列写「造不出值」的那几行不计（它是第三种状态，不是撞过）。
            # 词表两批报告的写法都认：`被拒/被收下`（未撞那批）与 `拒-撞对|拒-打偏/收下`（Cuts 族）。
            m = re.match(r"^([a-z][a-z0-9_]{2,})\s*" + SEP, ln)
            if m and any(t in ln for t in ("被拒", "被收下", "拒-", "收下")):
                got.append(m.group(1))
    return got


def keys_from_budget_script(rows):
    """预算那轮：从脚本臂表取 Go 字段名，再用格表翻成键名。"""
    src = io.open(BUDGET_SCRIPT, encoding="utf-8").read()
    fields = re.findall(r'^\s*\("[^"]+",\s*"([A-Z]\w+)"', src, re.M)
    by_tail = {}
    for (side, path), key in rows.items():
        by_tail.setdefault(path.split(".")[-1].split("#")[0], set()).add(key)
    out, unresolved = [], []
    for f in fields:
        ks = by_tail.get(f)
        if ks and len(ks) == 1:
            out.append(next(iter(ks)))
        else:
            unresolved.append(f)
    return out, unresolved, len(fields)


def main():
    url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8797/mcp"
    outp = sys.argv[2] if len(sys.argv) > 2 else R("每格撞没撞过-覆盖账面-1017.txt")
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

    try:
        head = fetch_celltable(url)
    except Exception as e:
        print("NOT_RUN 端点没答：%r" % (e,))
        return 4
    if "唯一键名" not in head:
        w("NOT_RUN：格表回话里没有抬头那句。")
        return finish(4)
    rows = parse_rows(head)
    keys = set(rows.values())
    declared = int(re.search(r"\u5f53\u524d (\d+) \u683c", head).group(1))
    decl_keys = int(re.search(r"\u552f\u4e00\u952e\u540d (\d+) \u4e2a", head).group(1))
    w("== 覆盖账面 · %s ｜ 端点 %s ｜ 字节自印见 up 那行 ==" % (time.strftime("%Y-%m-%d %H:%M:%S"), url))
    w("格表：抬头钉 %d 格 / %d 键 ｜ 我抠到 %d 行 / %d 键 ｜ 对得上 = %s"
      % (declared, decl_keys, len(rows), len(keys), str(len(rows) == declared and len(keys) == decl_keys)))
    if len(rows) != declared or len(keys) != decl_keys:
        w("NOT_RUN：分母对不上，后面所有「覆盖到几格」都会错。")
        return finish(4)
    w("")
    w("轮 ｜ 自印发/臂数 ｜ 报告里抠出的键名数 ｜ 去重后落进格表的格数 ｜ 抠不到格表的键 ｜ 状态")
    w("-" * 78)
    covered = set()
    any_round = 0
    for name, path, mode, denrx in ROUNDS:
        if not os.path.exists(path):
            w("%-30s ｜ —— ｜ 报告不在 ｜ —— ｜ —— ｜ **没这份凭据**" % name[:30])
            continue
        text = io.open(path, encoding="utf-8").read()
        dm = re.search(denrx, text)
        den = dm.group(1) if dm else "?"
        ks = keys_from_round(mode, text)
        if mode == "script_field":
            ks, unresolved, nf = keys_from_budget_script(rows)
            w("   （这轮从脚本臂表翻字段名：字段 %d 个，翻不出键名的 %s）" % (nf, " ".join(unresolved) or "无"))
        inreg = [k for k in ks if k in keys]
        stray = sorted(set(ks) - set(inreg))
        cov = set(inreg)
        st = "OK" if cov else "**这轮格式抠不出格名**"
        if cov:
            any_round += 1
        if len(set(ks)) != len(ks):
            st += " ｜ 同一键名重复出现（臂多于格）"
        covered |= cov
        w("%-30s ｜ %-14s ｜ %-14s ｜ %-18s ｜ %-12s ｜ %s"
          % (name[:30], den, len(ks), len(cov), " ".join(stray)[:12] or "无", st))
    w("")
    w("== 三态（不许塌）==")
    miss = sorted(keys - covered)
    w("按**键名**数：撞过 %d 个 ｜ 一次没撞 %d 个 ｜ 合计 %d（格表自报键名 %d 个）"
      % (len(covered), len(miss), len(covered) + len(miss), len(keys)))
    from collections import Counter
    per = Counter(rows.values())
    w("   行数(%d) 比键数(%d) 多的那几个键名（同键住两行）：%s"
      % (len(rows), len(keys), " ".join("%s×%d" % (k, n) for k, n in sorted(per.items()) if n > 1) or "无"))
    if any_round == 0:
        w("坏：一轮都没抠出格名 —— 那是我的抠法漂了，这份表不许引用。")
        return finish(6)
    w("没撞过的（按侧分组）：")
    for side in ("input", "output"):
        sk = sorted(k for (s, _p), k in rows.items() if s == side and k in miss)
        w("  %s 侧 %d 格：%s" % (side, len(sk), " ".join(sk) or "无"))
    w("不证明：这份表只说「哪几格有条臂撞过」；撞过的格是否**每种谎形状都试过**另说（矩阵那把只铺了 Measure 那 11 格 × 5 形状）。")
    w("        认领类那 5 条臂不计入：它改的是 `Readers` 那张认领表，不是账里的格（`报告/声明被拒-两类新问题-1017.txt`）。")
    return finish(0)


if __name__ == "__main__":
    sys.exit(main())
