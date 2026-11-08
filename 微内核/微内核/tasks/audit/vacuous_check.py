# -*- coding: utf-8 -*-
"""vacuous_check.py —— 给判定做的判定：扫所有驱动里的 check(...)，把**形状上就不会红**的报出来。

为什么要有这一支：一个 session 里我写了 11 条测不出东西的判定，其中 3 条恒真、1 条恒假。
那些错没有一个能被"更小心"拦住 —— 它们都能从**调用形状**机械地看出来。
这一支不读我的措辞、不听我的解释，只看形状：一条判定如果不可能红，它就不是判定。

规则（都是 client.Judge 的语义直接推出来的：ok = all(needles in txt) and not any(forbidden in txt)）：
  VACUOUS-TRUE   needles 与 forbidden 都空 ⇒ 恒 PASS
  （判"空不空"只看列表结构，元素是不是表达式无关 —— 这一条修之前，
    派生式期望值被整批算成"没判"，覆盖率掉了 30 根而没人报红：工具的错最容易读成代码干净）
  VACUOUS-FALSE  forbidden 里有空串 ⇒ "" in 任何文本 ⇒ 恒 FAIL
  LITERAL-TRUE   txt 是字面量且每个 needle 都是它的子串 ⇒ 恒 PASS（"see book" 里查 "see" 那一族）
  LITERAL-FALSE  txt 是字面量且某个 forbidden 是它的子串 ⇒ 恒 FAIL
  SELF-CONTRA    同一个字面串同时出现在 needles 与 forbidden ⇒ 恒 FAIL
控制台只打 ASCII（Windows GBK），全文写 UTF-8 报告。
"""
import ast
import glob
import io
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORT = os.path.join(ROOT, "报告", "判定体检.txt")
rows = []


def lit(node):
    """只认字面量与字面量列表；其它（变量、函数调用）一律 None —— 那是动态的，本工具不猜。"""
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    return None


def members(node):
    """列表/元组字面量的成员：返回 (字面量成员, 成员数, 是不是一个静态的列表)。
    「空不空」「有没有一个空串成员」这两件事只看结构，元素是表达式也照样知道。"""
    if isinstance(node, (ast.List, ast.Tuple)):
        return [lit(e) for e in node.elts], len(node.elts), True
    return [], -1, False


def judge(path):
    src = io.open(path, encoding="utf-8").read()
    try:
        tree = ast.parse(src)
    except SyntaxError as e:
        rows.append((os.path.relpath(path, ROOT), e.lineno, "PARSE-BROKE", str(e)[:80]))
        return 0, 0
    n = bad = 0
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        f = node.func
        fname = f.attr if isinstance(f, ast.Attribute) else getattr(f, "id", None)
        if fname != "check":
            continue
        if len(node.args) < 3:
            continue
        n += 1
        name = lit(node.args[0]) or "(动态名)"
        txt = lit(node.args[1])
        needles, n_nd, nd_list = members(node.args[2])
        # forbidden 既可能是关键字参数，也可能是**第 4 个位置参数**（Judge.__call__ 两种都收，
        # 而本仓库的驱动基本都用位置参数）。上一版只读 keywords，于是几条有禁项的判定被误报成"恒真"。
        forbidden, n_fb, fb_list = [], 0, True
        # 没写第 4 个参数 = 默认值 `()`，那是**静态可知为空**，不是"不知道"
        # （上一版把它算成没判，于是三参数的恒真判定整批漏检：种了 4 根坏针只报出 1 根）
        if len(node.args) >= 4:
            forbidden, n_fb, fb_list = members(node.args[3])
        for kw in node.keywords:
            if kw.arg == "forbidden":
                forbidden, n_fb, fb_list = members(kw.value)
                break
        # 只有"连列表本身都看不出来"（传的是变量、星号、生成器）才算没判。
        # 以前把"成员是表达式"整列表判成不知道，于是派生式期望值（`["整发落账 %d 条" % NC]`）
        # 全被记成没判 —— 覆盖率从 49/92 掉到 78/92 之类，那是工具的错不是代码的错。
        unjudged = 0 if (nd_list and fb_list) else 1
        why = []
        if not unjudged:
            if n_nd == 0 and n_fb == 0:
                why.append("VACUOUS-TRUE(needles 与 forbidden 都空)")
            if any(x == "" for x in forbidden):
                why.append("VACUOUS-FALSE(禁项里有空串，任何文本都命中)")
            if txt is not None:
                lit_nd = [x for x in needles if x is not None]
                lit_fb = [x for x in forbidden if x is not None]
                if lit_nd and len(lit_nd) == len(needles) and all(x in txt for x in lit_nd):
                    why.append("LITERAL-TRUE(txt 是字面量且含全部 needles)")
                if any(x in txt for x in lit_fb):
                    why.append("LITERAL-FALSE(txt 是字面量且撞在禁项上)")
            # 自相矛盾与 txt 是什么无关：同一个串既必须出现又不许出现，那一行永远红
            overlap = set(x for x in needles if x is not None) & set(x for x in forbidden if x is not None)
            if overlap:
                why.append("SELF-CONTRA(%r 同属 needles 与 forbidden)" % list(overlap)[0][:24])
        if why:
            bad += 1
            rows.append((os.path.relpath(path, ROOT), node.lineno, ";".join(why), name[:70]))
        globals()["UNJUDGED"] = globals().get("UNJUDGED", 0) + unjudged
    return n, bad


def main():
    files = sorted(glob.glob(os.path.join(ROOT, "tasks", "**", "*.py"), recursive=True))
    tot = bad = 0
    for p in files:
        n, b = judge(p)
        tot += n
        bad += b
    unjudged = globals().get("UNJUDGED", 0)
    out = []
    out.append("vacuous check: files=%d check() calls=%d 形状可疑=%d 参数读不懂没判=%d"
               % (len(files), tot, bad, unjudged))
    out.append("")
    cur = None
    for f, ln, why, name in rows:
        if f != cur:
            out.append("-- %s" % f)
            cur = f
        out.append("   :%-5d %-52s 判定名：%s" % (ln, why[:52], name))
    io.open(REPORT, "w", encoding="utf-8").write("\n".join(out) + "\n")
    for ln in out:
        sys.stdout.buffer.write((ln.encode("ascii", "replace") + b"\n"))
        sys.stdout.flush()
    print("report: %s" % REPORT.encode("ascii", "replace").decode())
    return 0


if __name__ == "__main__":
    sys.exit(main())
