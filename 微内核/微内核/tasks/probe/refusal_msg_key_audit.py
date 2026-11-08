# -*- coding: utf-8 -*-
"""#48 的数据：内核每一条会印给人看的拒收模板，到底带不带**机器可读键名**。

为什么要它：`isError=True` 只说「这发被拒了」（前提②已经落地），但接手的人要修就得知道**是哪一格**。
今天运行时撞的 8 臂（`报告/非Measure那批-运行时撞一遍-v2-1017.txt`）拒收原话里 0 条印出格表里那个 snake 键名，
而 `record.go:112`、`cells.go:165` 那两条模板明明是把 `c.Key` 打进句子里的 —— 所以「带不带」是**按模板分**的，
不是按调用方分的。这一支把全部模板数出来、按带法分类，再拿运行时那 8 臂对一遍。

分类只认源码里的实参，不认我读后觉得：
  A 带键名    —— 实参里有 `xxx.Key`（如 `c.Key`）
  B 带键名集合 —— 实参是把某个「键名生产者」的返回拼进句子的（生产者在表里现取：`KeyCollisions(`、
                `DuplicateFieldTags(`、`BasisMismatch(`、`DeadFields(`、`Cells(`），要求同函数上文里
                有 `got := 生产者(` 这类赋值，才认它是键名
  C 带中文标签 —— 实参是 `field`/`where`/`side`/`kind`/`name` 这类「说给人听的」变量或中文字面量
  D 只报值    —— 上面都不沾（只把出事的数打进去）
一条模板同时命中多类比标 A 更可疑，单独标「双带」列出来，不许塌成「算它有键名」。

自核（钉我这把尺子）：正对照 = `record.go:112` 与 `cells.go:165` 必须落进 A；落不进就是分类器坏，exit 6。
对开 = 运行时那 8 臂报的 file:line 必须**全都不在 A/B**；只要有一条被我判成带键名，就是静态与运行时打架，
     这一支自己印分歧，不替谁圆场。

跑法：python tasks/probe/refusal_msg_key_audit.py [v2报告.txt] [输出.txt]
"""
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
KEY_PRODUCERS = ["KeyCollisions(", "DuplicateFieldTags(", "BasisMismatch(", "DeadFields(", "Cells("]
LABEL_ARGS = ["field", "where", "side", "kind", "name"]


def strip_line(l):
    """去掉行尾 // 注释（字符串里的 // 不管：本包的拒收句子没有 URL）。"""
    q = False
    for i, ch in enumerate(l):
        if ch == '"':
            q = not q
        elif not q and l[i:i + 2] == "//":
            return l[:i]
    return l


def literals_and_args(blob):
    """取 fmt.Errorf( 之后的第一段字符串，与其后的实参列表（按顶层逗号切）。"""
    m = re.search(r'"((?:[^"\\]|\\.)*)"', blob)
    if not m:
        return None, []
    after = blob[m.end():]
    args, depth, cur = [], 0, ""
    inq = False
    for ch in after:
        if ch == '"':
            inq = not inq
        if not inq:
            if ch in "([{":
                depth += 1
            elif ch in ")]}":
                if depth == 0:
                    break
                depth -= 1
            elif ch == "," and depth == 0:
                args.append(cur.strip())
                cur = ""
                continue
        cur += ch
    if cur.strip():
        args.append(cur.strip())
    return m.group(1), [a for a in args if a and a != "..."]


def classify(fname, line, args, src_lines):
    tags = []
    if re.search(r"\w+\.Key\b", " ".join(args)):
        tags.append("A")
    for p in KEY_PRODUCERS:
        for a in args:
            if p in a or re.search(r"strings\.Join\(\s*\w+", a):
                # 只认「这个变量在上文里就是生产者给的」
                for m2 in re.finditer(r"(\w+)\s*:?=\s*" + re.escape(p.rstrip("(")) + r"\(", "\n".join(src_lines[max(0, line - 12):line])):
                    if m2.group(1) in a:
                        tags.append("B")
                        break
    body = " ".join(args)
    if any(re.search(r"\b%s\b" % w, body) for w in LABEL_ARGS) or re.search(r'"[^"]*[\u4e00-\u9fff][^"]*"', body):
        tags.append("C")
    if not tags:
        tags.append("D")
    return tags


def main():
    rep = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "报告", "非Measure那批-运行时撞一遍-v2-1017.txt")
    outp = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "报告", "拒收模板带不带键名-静态全表-1017.txt")
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

    pairs = [(f, os.path.join(ROOT, f)) for f in sorted(os.listdir(ROOT))
             if f.endswith(".go") and not f.endswith("_test.go")]
    sub = os.path.join(ROOT, "mcpsrv")
    if os.path.isdir(sub):
        pairs += [("mcpsrv/" + f, os.path.join(sub, f)) for f in sorted(os.listdir(sub))
                  if f.endswith(".go") and not f.endswith("_test.go")]
    gofiles = [p for _n, p in pairs]
    rows = []
    for f, path in pairs:
        ls = io.open(path, encoding="utf-8").read().splitlines()
        for i, raw in enumerate(ls):
            l = strip_line(raw)
            if "fmt.Errorf(" not in l:
                continue
            blob = l
            j = i
            while ("kernel:" not in blob and j + 1 < len(ls)) or blob.count("(") > blob.count(")"):
                if "kernel:" in blob and blob.count("(") <= blob.count(")"):
                    break
                j += 1
                blob += " " + strip_line(ls[j])
                if j - i > 4:
                    break
            msg, args = literals_and_args(blob[blob.index("fmt.Errorf("):])
            if not msg or not msg.startswith("kernel:"):
                continue
            rows.append((f, i + 1, msg, args, classify(f, i + 1, args, ls)))

    cnt = {}
    for _f, _l, _m, _a, tags in rows:
        k = "+".join(sorted(set(tags)))
        cnt[k] = cnt.get(k, 0) + 1
    w("== 拒收模板（全项目根目录 .go，排除 _test.go）· %s ==" % __import__("time").strftime("%Y-%m-%d %H:%M:%S"))
    w("扫的文件 %d 个 ｜ 以 `kernel:` 开头的拒收模板 %d 条" % (len(gofiles), len(rows)))
    w("分类（一条模板可以双带，所以计数相加可以大于条数）：")
    for k in sorted(cnt):
        w("  %s : %d 条" % (k, cnt[k]))
    w("  求和 %d 条 vs 模板 %d 条 —— 不等就是有模板没进任何一态"
      % (sum(cnt.values()), len(rows)))
    w("")
    w("A 带键名（实参里有 xxx.Key）：")
    for f, l, m, _a, t in rows:
        if "A" in t:
            w("  %s:%d  %s" % (f, l, m[:76]))
    w("B 带键名集合（拼的是生产者给的键名列表）：")
    for f, l, m, _a, t in rows:
        if "B" in t:
            w("  %s:%d  %s" % (f, l, m[:76]))
    w("D 只报值、不带任何名字（这些才是「修的人得去猜是哪一格」）：")
    susp = 0
    for f, l, m, a, t in rows:
        if t == ["D"]:
            if re.search(r"\bkeys?\b", " ".join(a)):
                susp += 1
            w("  %s:%d  %s ｜ 实参: %s" % (f, l, m[:70], ", ".join(a)[:60] or "无"))

    # 正对照：这两条必须落进 A
    ctl = [("record.go", 112), ("cells.go", 165)]
    bad = []
    byln = {(f, l): t for f, l, _m, _a, t in rows}
    w("分类器可能漏判（D 态但实参里就有 key/keys 这个变量名）%d 条 —— 上面 D 段逐条把实参印出来了，"
      "这一列只提示「认法的词表是我定的」这件事，不改动分类，也不并进 A 的数。" % susp)
    for f, l in ctl:
        if "A" not in byln.get((f, l), []):
            bad.append("%s:%d 判成 %s" % (f, l, byln.get((f, l), "没扫到")))
    w("")
    w("== 自核（钉我这把尺子）==")
    w("正对照：record.go:112 与 cells.go:165 必须判 A ｜ 坏的：%s" % (" ".join(bad) or "无"))

    # 与运行时对开：v2 报告里每一臂的「期望那条：file:line」
    runtime = []
    if os.path.exists(rep):
        for l in io.open(rep, encoding="utf-8"):
            m = re.search(r"期望那条：([\w.]+:\d+)", l)
            if m:
                runtime.append(m.group(1))
    clash = []
    shifted = []
    for fl in runtime:
        f, ln2 = fl.split(":")
        ln2 = int(ln2)
        t = byln.get((f, ln2))
        where = ln2
        if t is None:
            # 报告里引的行号有时是**判据那一行**（`if …{`），模板在下一两行 —— 这一类不许读成「静态没这条」。
            for d in (1, 2):
                if byln.get((f, ln2 + d)):
                    t = byln[(f, ln2 + d)]
                    where = ln2 + d
                    break
        if t is None:
            clash.append("%s 静态 3 行内没有模板" % fl)
        elif "A" in t or "B" in t:
            clash.append("%s 判 %s 但运行时没印键名" % (fl, "+".join(t)))
        elif where != ln2:
            shifted.append("%s→模板实为 %s:%d" % (fl, f, where))
    w("对开：运行时撞过的臂 %d 条（取自 %s）｜ 静态与运行时打架的：%s"
      % (len(runtime), os.path.basename(rep), " ； ".join(clash) or "无"))
    w("  引的是判据行、模板在下一两行的：%d 条：%s" % (len(shifted), " ".join(shifted) or "无"))
    if not runtime:
        w("  注意：一份都没对上 —— 那这条「对开」是空的，不许读成「一致」。")
    if bad:
        w("坏：分类器连自己钉的两条正对照都判不进 A，这份表不许引用。")
        return finish(6)
    return finish(0)


if __name__ == "__main__":
    sys.exit(main())
