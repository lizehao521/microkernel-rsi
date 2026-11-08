# -*- coding: utf-8 -*-
"""refusal_coverage.py —— 【分母口径错了，已被 tasks/probe/lie_sweep_all.py 取代】

为什么留着而不是删掉：这一支按 `grep 拒收消息` 数出来的"分母"是 106 条，里面一大半是
`CheckCells` / `LoadLedger` / `CheckChain` 那族**表自检与读盘自检** —— 调用方拿一发 `record_call`
的载荷根本打不到它们。拿它当"元数据谎完整名单"的分母是**类别错误**，算出来的"覆盖率 1%"是纯误导；
而且它的字符串匹配还会假阴（同一条拒收在码里有两种措辞）。
真分母 = `cells.go` 里 `measureCell(...)` 登记的 **11 格**（只有 Measure 型字段有出处档可谎），
那份前后对开在 `报告/说谎者靶场-全格-前后对开-1017.txt`；这一支的错误读数留档为
`报告/拒收路径覆盖面-1017-分母类别错误那份.txt`。

—— 以下是原说明 ——


为什么要这一支：A0 靶场自印的是「种谎臂 7 条」，那只是**我列了几条**，不是
「内核从服务面能被几条元数据谎打红」。目标里写的是"拿到 #20 的完整名单"，
所以这件事要么有分母，要么就该被读成没做完。

它数三样：
  分母 = 住在**写路径那族 Check 函数**里的拒收分支（按消息去重，逐条带出处 文件:函数）
  覆盖 = 这些消息里，有多少**真从服务面**被打出来过（从 A0 两份改前/改后读数里抠原话）
  未覆盖 = 差集，逐条列出来 —— 这些就是"今天只能从 Go 测试里红、面上打不出来"的那些

跑法：python tasks/probe/refusal_coverage.py [读数文件...]
不带参数时用 报告/ 里那三份 A0 读数（改前/改后/端点没答那轮）。
"""
import glob
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, "报告", "拒收路径覆盖面-1017.txt")

# 写路径那一族：一发调用的元数据从这儿进内核。别的 Check（链、账本、journal）不归"元数据谎"管。
WRITE_FILES = ("input.go", "output.go", "measure.go", "cells.go", "record.go")
CHECK_FUNC_HINT = re.compile(r"\((?:\s*\w+\s+\*?\w+)?\)\s*(Check|check|plan|DeriveBudget|validate)\w*\b")


def func_spans(text):
    """粗略切顶层 func：返回 [(函数名, 起始行, 文本片段)]，够用来把消息归到函数。"""
    lines = text.splitlines()
    spans, cur, start = [], None, 0
    for i, l in enumerate(lines):
        m = re.match(r"^func (?:\([^)]*\) )?(\w+)", l) or re.match(r"^func \([^)]*\) (\w+)", l)
        if m:
            if cur:
                spans.append((cur, start, "\n".join(lines[start:i])))
            cur, start = m.group(1), i
    if cur:
        spans.append((cur, start, "\n".join(lines[start:])))
    return spans


def collect_paths():
    """分母：写路径那一族里的拒收分支，按消息去重。"""
    msgs = {}
    for f in WRITE_FILES:
        p = os.path.join(ROOT, f)
        if not os.path.isfile(p):
            continue
        text = io.open(p, encoding="utf-8").read()
        for name, start, body in func_spans(text):
            if not (name.lower().startswith("check") or name in ("plan", "DeriveBudget")
                    or "Check" in name or CHECK_FUNC_HINT.search("func " + name)):
                continue
            for m in re.finditer(r'(?:fmt\.)?(?:Errorf|errors\.New)\("kernel: (.*?)"', body, re.S):
                raw = re.sub(r"\s+", " ", m.group(1)).strip()
                key = re.sub(r'%[sdvq]\w*|%\.\d+f', '⟨数⟩', raw)
                if key not in msgs:
                    msgs[key] = {"file": f, "func": name, "line": text[:m.start()].count("\n") + 1,
                                 "raw": raw}
    return msgs


def observed_from_reports(paths):
    """覆盖：从读数里抠内核真的说过的拒收句（正文(check)/正文(record)/拒因/预检结论 那几行）。"""
    seen = {}
    for p in paths:
        if not os.path.isfile(p):
            continue
        t = io.open(p, encoding="utf-8", errors="replace").read()
        for m in re.finditer(r"kernel: ([^\"\\\n]{6,200})", t):
            s = re.sub(r"\s+", " ", m.group(1)).strip()
            s = re.sub(r'"$|⟨半字已洗⟩$', "", s).strip()
            seen.setdefault(s, []).append(os.path.basename(p))
    return seen


def match(obs, paths):
    """把面上看到的句子对回分母：规范化后子串匹配（内核会把几条拼在一句里回）。"""
    hit, unmatched = {}, []
    norm = lambda s: re.sub(r"[\s，。；：、（）\"'⟨数]+", "", s)
    np = {k: norm(v["raw"]) for k, v in paths.items()}
    for s, srcs in obs.items():
        ns = norm(s)
        got = [k for k, nk in np.items() if nk and (nk in ns or ns in nk)]
        if got:
            for k in got:
                hit.setdefault(k, []).append((s[:70], srcs[0]))
        else:
            unmatched.append(s)
    return hit, unmatched


def main():
    reports = sys.argv[1:] or [
        os.path.join(ROOT, "报告", "说谎者靶场-A0-1017-改后重跑.txt"),
        os.path.join(ROOT, "报告", "说谎者靶场-A0-1017.txt"),
        os.path.join(ROOT, "报告", "说谎者靶场-A0-前后逐臂对开-1017.txt"),
    ] + sorted(glob.glob(os.path.join(ROOT, "报告", "拒收数不落盘*.txt")))
    paths = collect_paths()
    obs = observed_from_reports(reports)
    hit, unmatched = match(obs, paths)
    log = []
    w = log.append
    w("== 元数据谎的拒收覆盖面 · %s · 判据表由现场 celltable 要，不抄 ==" % time_str())
    w("")
    w("== 分母（工具自印，不是我列的）==")
    w("住在写路径那一族（%s）里、且落在 Check/plan/DeriveBudget 函数体内的拒收分支：%d 条（按消息去重）"
      % ("、".join(WRITE_FILES), len(paths)))
    w("这些是「一发元数据谎能被内核拒掉的所有话术」的全集 —— A0 那 7 条臂只覆盖其中一部分。")
    w("")
    w("== 覆盖（只认从服务面真打出来的）==")
    w("拿来对开的读数文件 %d 份：%s" % (len(reports), "、".join(os.path.basename(r) for r in reports)))
    w("读数里抠到内核原话 %d 句；其中能对上分母的 %d 条分母被面上打到过"
      % (len(obs), len(hit)))
    w("")
    w("已覆盖（分母 → 面上原话出处的份数）：")
    for k in sorted(hit):
        v = paths[k]
        w("  [√] %-56s %s:%s:%s  ←%d 处" % (v["raw"][:56], v["file"], "func " + v["func"], v["line"], len(hit[k])))
    w("")
    w("未覆盖（今天只能从 Go 测试里红、面上打不出来，或压根没有载荷能打到）：")
    miss = [k for k in sorted(paths) if k not in hit]
    for k in miss:
        v = paths[k]
        w("  [ ] %-56s %s:func %s:%s" % (v["raw"][:56], v["file"], v["func"], v["line"]))
    w("")
    w("== 自印分母 ==")
    w("分母=%d ｜ 面上打到=%d ｜ 未打到=%d ｜ 覆盖率=%s" % (len(paths), len(hit), len(miss),
      ("%.0f%%" % (100.0 * len(hit) / len(paths)) if paths else "分母为 0，不计算")))
    w("面上抠到但对不上分母的句子（可能是别的层说的，或跨函数拼句）=%d 句，列前 8 句：" % len(unmatched))
    for s in sorted(unmatched)[:8]:
        w("   · %s" % s[:110])
    w("")
    w("一句实话：这份数**不判**「未覆盖的那些该不该补臂」—— 它只把『完整名单』这句话从我的清单")
    w("换成一个可复算的分母。要补哪些、面到底能不能打到，是 #20/#48 那一族要拍的口径。")
    io.open(OUT, "w", encoding="utf-8").write("\n".join(log) + "\n")
    sys.stdout.buffer.write(("WROTE %d 行 → %s\n" % (len(log), OUT)).encode("ascii", "replace"))
    sys.stdout.buffer.write(("分母=%d 覆盖=%d 未覆盖=%d\n" % (len(paths), len(hit), len(miss))).encode("ascii", "replace"))
    return 0


def time_str():
    import time
    return time.strftime("%F %T")


if __name__ == "__main__":
    sys.exit(main())
