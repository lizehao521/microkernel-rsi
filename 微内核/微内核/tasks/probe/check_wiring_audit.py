# -*- coding: utf-8 -*-
"""判据接线审计 —— 两件一起数，都从源码取词，不靠我读原话。

为什么要有这支：#55（`fail_status` 那格「缺席却带数」拦不住）不是"没有判据"，是**判据没接到那一格上**。
这类错只能靠"字段全集 vs 接线点全集"对开来看见，靠读代码的眼睛数不出漏了几个。

一 · 接线覆盖率
  分母 = 全部 `Measure` 类型的结构体字段（正则从 input.go/output.go 的 struct 声明里抠）；
  接线 = 该字段被送进 `Measure.Check(...)` / `usageOK(...)`，两种形状都认：
    (a) 直接调：`m.Tokens.Check("token 数")`
    (b) 表驱动：`[]struct{name string; m Measure}{{"分帧数", m.Chunks}, …}` 后面那个循环里调 `s.m.Check(s.name)`
  没接线的照实印出来（今天应当有 `Status` 那一个）。

二 · 每格的「人话名」从代码里取（这是 #48 缺的那张表的一半）
  同一个正则顺手把标签抠出来：直接调的第二个参数、表里的第一个字段。
  再拿这些**代码里的词**去撞归档拒收原话（`报告/说谎者靶场-全格-改后-原话三数-1017.txt`），
  数「原话里带着代码标签的有几条」 —— 这一条不靠我读中文，撞得上就算撞上。

跑法：python tasks/probe/check_wiring_audit.py [输出.txt]
"""
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SRC = ["input.go", "output.go", "cells.go", "record.go", "measure.go"]
CLAUSES = os.path.join(ROOT, "报告", "说谎者靶场-全格-改后-原话三数-1017.txt")


def read(fn):
    p = os.path.join(ROOT, fn)
    return io.open(p, encoding="utf-8", errors="replace").read() if os.path.exists(p) else ""


def main():
    outp = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "报告", "判据接线审计-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在，那是上一轮的凭据。换个名。" % outp.encode("unicode_escape"))
        return 3
    texts = {f: read(f) for f in SRC}
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    # —— 分母：Measure 类型的结构体字段 ——
    fields = []
    for f, t in texts.items():
        for m in re.finditer(r'^\s*([A-Z][A-Za-z0-9]*)\s+Measure\s*(//.*)?$', t, re.M):
            fields.append((m.group(1), f, (m.group(2) or "").strip()))
    w("== 判据接线审计 · 分母 = Measure 类型的结构体字段 ==")
    w("字段全集 %d 个（正则 `^\\s*(字段) Measure` 从 %s 里抠）：" % (len(fields), " ".join(SRC)))
    for name, f, cmt in fields:
        w("   %-14s %s" % (name, f))

    # —— 接线点 ——
    direct = {}   # 字段 → [(标签, 文件:行)]
    for f, t in texts.items():
        for ln, line in enumerate(t.split("\n"), 1):
            for m in re.finditer(r'([A-Za-z0-9_]+)\.Check\("([^"]*)"\)', line):
                direct.setdefault(m.group(1), []).append((m.group(2), "%s:%d" % (f, ln)))
            for m in re.finditer(r'usageOK\("([^"]*)",\s*([A-Za-z0-9_.]+)\)', line):
                direct.setdefault(m.group(2).split(".")[-1], []).append((m.group(1), "%s:%d" % (f, ln)))
    # 表驱动：{"标签", m.字段} —— 只要同文件里存在把循环变量送进 Check/usageOK 的循环，就算接线
    tabled = {}
    loop_wired = any(re.search(r'\b\w\.m\.Check\(|usageOK\(\w+\.name,\s*\w+\.m\)', t) for t in texts.values())
    for f, t in texts.items():
        for ln, line in enumerate(t.split("\n"), 1):
            for m in re.finditer(r'\{"([^"]+)",\s*(?:m|f|in|out)?\.?([A-Za-z0-9_]+)\}', line):
                if loop_wired:
                    tabled.setdefault(m.group(2), []).append((m.group(1), "%s:%d" % (f, ln)))

    w("")
    w("== 接线覆盖 ==")
    w("表驱动的循环确实把 `.m` 送进了判据（`s.m.Check(` / `usageOK(u.name, u.m)` 命中）= %s" % loop_wired)
    unwired = []
    for name, f, _ in fields:
        where = direct.get(name, []) + tabled.get(name, [])
        if where:
            w("   %-14s 接了 ｜ 标签 %s ｜ 出处 %s" % (name, " ".join(sorted({'"%s"' % x[0] for x in where})),
                                                "; ".join(sorted({x[1] for x in where}))))
        else:
            unwired.append(name)
            w("   %-14s **没接线** ｜ 全树找不到把它送进 Measure.Check/usageOK 的调用点" % name)
    w("覆盖率 = %d/%d 接了，%d 个没接：%s" % (len(fields) - len(unwired), len(fields), len(unwired), " ".join(unwired)))

    # —— 用代码里的标签去撞归档原话（#48 的另一半）——
    labels = sorted({lab for d in (direct, tabled) for v in d.values() for lab, _ in v})
    w("")
    w("== 代码里的「人话名」标签 %d 个（这就是 #48 缺的那半张表的一半）==" % len(labels))
    w("   " + " ｜ ".join(labels))
    if os.path.exists(CLAUSES):
        # 分母要纯：表里既有被拒的臂也有**被收下**的臂（后者的"内核那句"是 `输入侧：自洽` 那种回话）。
        # v1 那版把 11 臂全当拒收原话数，得出「11 条里撞得上 5」—— 分母不纯，那个数不作数。
        # 只取 `record=True` 的那些行，才是"拒收原话"这一族。
        cl = []
        for line in io.open(CLAUSES, encoding="utf-8"):
            m = re.search(r'record=True.*?内核那句：(.+)$', line)
            if m:
                cl.append(m.group(1).strip())
        hit = [c for c in cl if any(lab in c for lab in labels)]
        w("归档里**被落账拒收**的臂 %d 条（只取 record=True 的行；把被收下的臂混进来就是我 v1 那份的错），"
          "其中**带着代码标签**的 = %d 条；撞不上的 = %d 条" % (len(cl), len(hit), len(cl) - len(hit)))
        for c in cl:
            w("   [%s] %s" % ("撞得上" if any(lab in c for lab in labels) else "撞不上", c[:96]))
        w("一句实话：撞得上 = 那句人话里的名字**与代码传给判据的标签逐字相同**（可机核）；")
        w("        撞不上 不等于没说 —— 它可能换了写法（如「token 那一格」 vs 标签「token 数」）。")
        w("        这一条把 #48 从『只能人读』推进到『能数出多少条与代码同名』，但**结构化字段仍然没有**。")
    else:
        w("归档原话那份不在（%s）—— 这一段不印读数。" % os.path.basename(CLAUSES))
    w("")
    w("自印分母：字段 %d ｜ 接了 %d ｜ 没接 %d ｜ 代码标签 %d 个" %
      (len(fields), len(fields) - len(unwired), len(unwired), len(labels)))
    io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
    print("REPORT=" + outp.encode("unicode_escape").decode("ascii"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
