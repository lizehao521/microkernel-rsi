# -*- coding: utf-8 -*-
"""看板里每一条「第 N 行」引用，逐条打开对到那份读数的真实行号上 —— 数出来，不靠我手工逐条看。

为什么要有它（也不与现有两支重叠）：
· `chain_status.py` 打开的是 `报告/融合链-逐段对账-1017.txt` 那一份里引用的凭据（文件在不在、自印行在不在），
  它不解析看板正文里的**行号**；`premises_ledger.py` 合的是三条前提的数。
· 今天手工撞到的那一发就是这一类：`决策件-第18条…` 第 6 条把「命中规则」写成第 28、34、40 行，
  而 28/34/40 是字节对照行、真正的命中行是 27/33/39 —— 文件在、行数够，**这种错那两支都看不见**。

判据（红那一条）：可归属的引用里，越界（引用行号 > 那份文件现长）与指到空行的，必须都是 0。
三态不塌：可核 / 越界 / 核不了（不可归属·文件找不到·指到空行）分开印；
临时件（Temp 那类，本来就不该在盘上）单列「不适用」，**不并入可核、也不说成挡住了**。

自核（先证这把尺子会红）：造假读数 + 假看板，十二条臂各钉一条判据 ——
甲 指存在的行 ⇒ 可核；乙 指超出文件长度的行 ⇒ 越界；丙 文件不存在 ⇒ 核不了；丁 指到空行 ⇒ 核不了；
戊 正对照：把被指那份改长后，乙那条必须翻成可核（证明判据真在读那份文件的长度）；
己 Temp 路径 ⇒ 不适用；庚 上下文没有文件名 ⇒ 不可归属；
辛 裸名住子目录（看板里 `mcpsrv.go:561` 那一族的省目录写法）⇒ 靠文件名索引命中；
壬 两份同名 ⇒ 多命中不猜；癸 只有文档名没有读数件 ⇒ 走次选并明写「非读数件」；
子 冒号式指到只有一行的代码 ⇒ 越界；丑 冒号式指住子目录的裸代码名 ⇒ 靠文件名索引命中。
任何一条判错 ⇒ exit 5，不出正文结论。

跑法：python tasks/probe/board_line_census.py [输出.txt]
退出码：0 数完且判据成立 ｜ 4 判据红（有越界或指到空行）｜ 5 自核红 ｜ 3 输出件已存在（拒绝覆盖）
"""
import io
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
REPORT = os.path.join(REPO, "报告")
BOARD = os.path.join(REPORT, "看板.md")

FILE_TOKEN = re.compile(r"[A-Za-z0-9_\-…\u4e00-\u9fff][A-Za-z0-9_\-…\u4e00-\u9fff./（）()]*\.(?:txt|md|py|go|jsonl|sh|csv|yaml|yml)")
LINE_REF = re.compile(r"第\s*([0-9]+(?:\s*[、,]\s*[0-9]+)*(?:\s*-\s*[0-9]+)?)\s*行")
FILE_COLON = re.compile(r"(?:^|[^A-Za-z0-9\u4e00-\u9fff])([A-Za-z0-9_\-\u4e00-\u9fff.]+\.(?:py|go|md|txt|jsonl|sh)):(\d+)(?:-(\d+))?")

TEMP_HINTS = ("Temp", "TEMP", "tmp\\", "tmp/")


def read_lines(path):
    with io.open(path, "rb") as f:
        data = f.read()
    return data.decode("utf-8", "replace").splitlines()


def numbers(token):
    out = []
    for part in re.split(r"[、,]", token):
        part = part.strip()
        m = re.match(r"^(\d+)(?:\s*-\s*(\d+))?$", part)
        if not m:
            continue
        a = int(m.group(1))
        b = int(m.group(2)) if m.group(2) else a
        out.extend([a, b] if b != a else [a])
    return out


def locate(name, dirs, root, index):
    """把引用里写的名字对到一份真实文件上。返回 (绝对路径 | None, 命中方式)。
       裸名（`mcpsrv.go`、`main.go`、`attracta_probe.py`）住子目录，按原样路径必然找不到 ——
       那是看板里省目录的写法，不是文件不在，所以最后一级用「整棵树的文件名索引」，多命中就明说不猜。"""
    raw = name
    bare = raw.replace("\\", "/").split("/")[-1]
    ellipsis = bare.startswith("…") or bare.startswith("...")
    if ellipsis:
        bare = bare.lstrip("….")
    cand = []
    if os.path.isabs(raw):
        cand.append((raw, "绝对路径"))
    for d in dirs:
        cand.append((os.path.join(d, raw), "原样相对"))
        cand.append((os.path.join(d, bare), "纯文件名"))
    for p, how in cand:
        if os.path.isfile(p):
            return os.path.abspath(p), how
    hits = list(index.get(bare.lower(), []))
    uniq = sorted(set(hits))
    if len(uniq) == 1:
        return uniq[0], "文件名索引唯一命中（省目录写法）"
    if len(uniq) > 1:
        return None, "同名多命中 %d 份（%s），不猜" % (
            len(uniq), "、".join(os.path.relpath(p, root).replace("\\", "/") for p in uniq[:3]))
    if bare and len(bare.lstrip("-")) > 4:
        # 省略式写法（`…-1310.txt`）：按后缀在索引里找，仍要求唯一
        hits = [p for k, ps in index.items() if k.endswith(bare.lower()) for p in ps]
        uniq = sorted(set(hits))
        if len(uniq) == 1:
            return uniq[0], "文件名索引后缀唯一命中（省略式写法）"
        if len(uniq) > 1:
            return None, "后缀多命中 %d 份（%s），不猜" % (
                len(uniq), "、".join(os.path.relpath(p, root).replace("\\", "/") for p in uniq[:3]))
    return None, "找不到"


def suffix_of(name):
    bare = name.replace("\\", "/").split("/")[-1].lstrip("….")
    return os.path.splitext(bare)[1].lower()


READOUT = (".txt", ".jsonl", ".csv")
DOC = (".md",)
CODE = (".go", ".py", ".sh")


def build_index(roots):
    idx = {}
    for r in roots:
        if not os.path.isdir(r):
            continue
        for dirpath, dirnames, filenames in os.walk(r):
            dirnames[:] = [d for d in dirnames if d not in ("__pycache__", ".git", "node_modules")]
            for fn in filenames:
                idx.setdefault(fn.lower(), []).append(os.path.abspath(os.path.join(dirpath, fn)))
    return idx


STANDINS = ("读数", "对账件", "见证件", "原件", "脚本", "凭据", "那份", "上面", "下面", "卡片", "那一份")


def abuts_standin(text, ref_pos):
    """引用是不是紧跟在一个代称后面（`读数第 39-41 行`、`脚本第 15-34 行`、`对账件第 178 行`）。
       这一族的真实目标住在上下文里，本尺**不回扫**（回扫会把上一条卡的文件名拉过来），所以判核不了。"""
    head = text[:ref_pos].rstrip(" 「『（(")
    for s in STANDINS:
        if head.endswith(s):
            return s
    return None


def candidates(line_idx, ref_pos, lines):
    """只认**同一行**里的文件名（引用前的优先，其次引用后的）。
       不回扫上文：本看板一条卡里前后几行各有文件名，回扫就会把上一条读的件安到这一句上，
       安错了还会报出一个假「越界」——那比漏一条更糟。"""
    text = lines[line_idx]
    toks = [(m.start(), m.group(0), "同行·引用前") for m in FILE_TOKEN.finditer(text)]
    toks += [(m.start(1), m.group(1), "同行·引用后") for m in FILE_COLON.finditer(text)]
    for k, (pos, name, how) in enumerate(toks):
        if pos >= ref_pos:
            toks[k] = (pos, name, "同行·引用后")
    before = sorted([t for t in toks if t[0] < ref_pos], key=lambda t: -t[0])
    after = sorted([t for t in toks if t[0] >= ref_pos], key=lambda t: t[0])
    out = []
    for pos, name, how in before + after:
        if name not in [o[0] for o in out]:
            out.append((name, how))
    return out


def choose_for_ref(cands, kind, standin):
    """`名:行` 冒号式 ⇒ 代码/脚本/文档都认（那是看板里指源码的写法）。
       `第 N 行` ⇒ 只认**读数件**(.txt/.jsonl/.csv)：这一族在本看板指归档读数；
       同一行只有 .md 或代码名 ⇒ 判核不了（不猜、不回扫），有代称 ⇒ 直接判代称。"""
    if standin:
        return None, "代称：`第 N 行` 紧跟「%s」，真实目标在上下文里，本尺不回扫 ⇒ 不核" % standin
    if kind == "colon":
        return (cands[0][0], cands[0][1]) if cands else (None, "不可归属：冒号式却没抓到文件名")
    seen = []
    for name, how in cands:
        sfx = suffix_of(name)
        if sfx in READOUT:
            return name, how + " · 读数件"
        seen.append("%s(%s)" % (name, sfx or "无后缀"))
    if not cands:
        return None, "本行没有文件名 ⇒ 不可归属（不回扫上文）"
    return None, "本行只有非读数件（%s）⇒ `第 N 行` 不猜它指哪份" % "、".join(seen)


def census(lines, dirs, root, index):
    rows = []
    for i, text in enumerate(lines):
        refs = []
        for m in LINE_REF.finditer(text):
            refs.append((m.start(), m.group(0), numbers(m.group(1)), None, "line"))
        for m in FILE_COLON.finditer(text):
            refs.append((m.start(1), "%s:%s" % (m.group(1), m.group(2)),
                         [int(m.group(2))] + ([int(m.group(3))] if m.group(3) else []), m.group(1), "colon"))
        if not refs:
            continue
        for pos, shown, nums, forced, kind in refs:
            cands = [(forced, "同行·冒号式自名")] if forced else candidates(i, pos, lines)
            fname, how_attr = choose_for_ref(cands, kind, abuts_standin(text, pos))
            if fname is None:
                rows.append((i + 1, shown, "", "", "", "核不了", how_attr))
                continue
            if any(h in fname for h in TEMP_HINTS):
                rows.append((i + 1, shown, fname, "", "", "不适用", "临时件（本来不该在盘上），不并入可核"))
                continue
            path, how = locate(fname, dirs, root, index)
            if path is None:
                rows.append((i + 1, shown, fname, "", "", "核不了", "文件找不到：%s" % how))
                continue
            tgt = read_lines(path)
            bad = [n for n in nums if n > len(tgt)]
            blank = [n for n in nums if 1 <= n <= len(tgt) and not tgt[n - 1].strip()]
            rel = os.path.relpath(path, root).replace("\\", "/")
            if bad:
                rows.append((i + 1, shown, rel, str(len(tgt)),
                             "、".join(str(n) for n in nums), "越界",
                             "引用行号超出（第 %s 行不在，那份现长 %d 行）｜归属：%s · %s" % (
                                 "、".join(str(n) for n in bad), len(tgt), how_attr, how)))
            elif blank:
                rows.append((i + 1, shown, rel, str(len(tgt)),
                             "、".join(str(n) for n in nums), "核不了",
                             "指到空行（第 %s 行是空白）｜归属：%s · %s" % (
                                 "、".join(str(n) for n in blank), how_attr, how)))
            else:
                rows.append((i + 1, shown, rel, str(len(tgt)),
                             "、".join(str(n) for n in nums), "可核",
                             "归属：%s · %s" % (how_attr, how)))
    return rows


def selfcheck():
    import tempfile
    d = tempfile.mkdtemp(prefix="blc-selfcheck-")
    sub = os.path.join(d, "报告")
    os.makedirs(sub)
    w = lambda n, t: io.open(os.path.join(sub, n), "w", encoding="utf-8", newline="\n").write(t)
    w("good.txt", "L1\nL2\nL3\n")
    w("blank.txt", "L1\n\nL3\n")
    os.makedirs(os.path.join(sub, "probe"))
    os.makedirs(os.path.join(sub, "a"))
    os.makedirs(os.path.join(sub, "b"))
    io.open(os.path.join(sub, "probe", "arm.txt"), "w", encoding="utf-8", newline="\n").write("P1\nP2\nP3\n")
    io.open(os.path.join(sub, "probe", "自核-1502.txt"), "w", encoding="utf-8", newline="\n").write("Q1\nQ2\nQ3\n")
    for dd in ("a", "b"):
        io.open(os.path.join(sub, dd, "twin.txt"), "w", encoding="utf-8", newline="\n").write("T1\nT2\nT3\n")
    io.open(os.path.join(sub, "README.md"), "w", encoding="utf-8", newline="\n").write("R1\nR2\nR3\n")
    io.open(os.path.join(sub, "main.go"), "w", encoding="utf-8", newline="\n").write("package main\n")
    io.open(os.path.join(sub, "probe", "tail.go"), "w", encoding="utf-8", newline="\n").write("package p\n// 第二行\n")
    w("board.md",
      "第 2 行 庚（这一行和它上面都没有文件名）\n"
      "`报告/good.txt` 第 2 行 甲\n"
      "`报告/good.txt` 第 9 行 乙\n"
      "`报告/nosuch.txt` 第 2 行 丙\n"
      "`报告/blank.txt` 第 2 行 丁\n"
      "`Temp/x/good.txt` 第 2 行 己\n"
      "`arm.txt` 第 2 行 辛\n"
      "`twin.txt` 第 2 行 壬\n"
      "`README.md` 第 2 行 癸\n"
      "看板里 `main.go:209` 这种冒号式写法 子时\n"
      "冒号式指住子目录的代码名 `tail.go:2` 丑时\n"
      "对账件第 2 行 寅（同一行后面就有 `good.txt`，但引用紧跟代称 ⇒ 必须判代称、不许拿它算）\n"
      "`…-1502.txt` 第 2 行 卯（省略式写法，靠文件名索引的后缀命中）\n")
    # (臂, 假看板第几行, 那条引用的写法, 期望判定, 期望理由里含这一段)
    exp_rows = [("庚", 1, "第", "核不了", "不可归属"),
                ("甲", 2, "第", "可核", ""),
                ("乙", 3, "第", "越界", ""),
                ("丙", 4, "第", "核不了", "文件找不到"),
                ("丁", 5, "第", "核不了", "指到空行"),
                ("己", 6, "第", "不适用", ""),
                ("辛", 7, "第", "可核", "文件名索引唯一命中"),
                ("壬", 8, "第", "核不了", "同名多命中"),
                ("癸", 9, "第", "核不了", "非读数件"),
                ("子", 10, "main.go", "越界", "引用行号超出"),
                ("丑", 11, "tail.go", "可核", "文件名索引唯一命中"),
                ("寅", 12, "第", "核不了", "代称"),
                ("卯", 13, "第", "可核", "后缀唯一命中")]
    rows_all = census(read_lines(os.path.join(sub, "board.md")), [sub], REPO, build_index([sub]))
    arms = []
    for k, ln, pref, want, why in exp_rows:
        hit = [r for r in rows_all if r[0] == ln and r[1].startswith(pref)]
        r = hit[0] if hit else None
        have = r[5] if r else "没解析出这条引用"
        why_ok = (not why) or (r and why in r[6])
        arms.append((k, want, have, have == want and bool(why_ok), (r[6][:70] if r else "看板那行没被解析")))
    # 正对照：把 good.txt 改长，乙那条必须从越界翻成可核 ⇒ 证明判据真的在读那份文件的长度
    io.open(os.path.join(sub, "good.txt"), "w", encoding="utf-8", newline="\n").write("x\n" * 12)
    rows2 = [r for r in census(read_lines(os.path.join(sub, "board.md")), [sub], REPO, build_index([sub]))
             if r[0] == 3 and r[1].startswith("第")]
    flip = rows2[0] if rows2 else None
    arms.append(("戊", "可核", flip[5] if flip else "没出现", bool(flip) and flip[5] == "可核",
                 "把被指文件改到 12 行后，指第 9 行那条（假看板第 3 行）必须翻成可核"))
    ok = all(a[3] for a in arms)
    return ok, arms, d


def main():
    out_p = sys.argv[1] if len(sys.argv) > 1 else None
    if out_p and os.path.exists(out_p):
        print("输出件已存在，拒绝覆盖：%s" % out_p)
        return 3
    ok, arms, tmpd = selfcheck()
    if not ok:
        print("== 自核红 ==")
        for a in arms:
            print("  %s 期望 %s ｜ 判 %s ｜ %s" % (a[0], a[1], a[2], a[4]))
        print("退出码 5：这把尺子自己没通过，正文结论不出")
        return 5

    lines = read_lines(BOARD)
    dirs = [REPORT, REPO, os.path.dirname(REPO),
            os.path.join(os.path.dirname(os.path.dirname(REPO)), "构建-1017")]
    t0 = __import__("time").time()
    index = build_index(dirs)
    n_idx = sum(len(v) for v in index.values())
    rows = census(lines, dirs, REPO, index)
    secs = __import__("time").time() - t0

    tally = {}
    for r in rows:
        tally[r[5]] = tally.get(r[5], 0) + 1
    red_rows = [r for r in rows if r[5] == "越界"] + [r for r in rows if "指到空行" in r[6]]
    dirs_s = "、".join(os.path.relpath(d, os.path.dirname(REPO)).replace("\\", "/") for d in dirs)

    buf = []
    W = buf.append
    W("看板行号引用逐条对账 ｜ 生成 %s ｜ 尺子 tasks/probe/board_line_census.py（只读，不动看板、不动任何读数）" %
      __import__("datetime").datetime.now().strftime("%Y-%m-%d %H:%M:%S"))
    W("读数 of record：`报告/看板.md` 现长 %d 行" % len(lines))
    W("自核 %d 臂全对上（TEMP 微型假看板跑真判据；含正对照：把被指文件改长后，越界那条必须翻成可核）" % len(arms))
    W("")
    W("== 一 · 分母自印 ==")
    W("  引用条数 %d ｜ 逐态：%s" % (len(rows), "　".join("%s %d" % kv for kv in sorted(tally.items()))))
    W("  找文件用了这几处目录：%s" % dirs_s)
    W("  文件名索引 %d 个名字 / %d 份文件 ｜ 归属＋比对这一发 %.2f 秒（含建索引，只读）" % (
      len(index), n_idx, secs))
    W("  归属口径（每条都印了它用的是哪一种，不合并）：")
    W("    · `名:行` 冒号式 ⇒ 认代码/脚本/文档（看板指源码的写法）；`第 N 行` ⇒ **只认读数件**(.txt/.jsonl/.csv)，"
      "同一行只有 .md 或代码名 ⇒ 判核不了，不猜。")
    W("    · 只认**同一行**的文件名（引用前的优先，其次引用后的），**不回扫上文**：一条卡前后几行各有文件名，"
      "回扫就会把上一条读的件安到这一句上 —— 这一发头一轮就因回扫造出 5 条假「越界」，改准后它们全判核不了。")
    W("    · 引用紧跟代称（读数／对账件／见证件／脚本／原件／凭据…）⇒ 判核不了；真实目标在上下文里，尺子不替看板补。")
    W("    · 名字按原样路径找不到时走整棵树的文件名索引（省目录写法 `main.go:209` 靠它），同名多命中就明说有几份、不猜。")
    W("  所以「可核」只证明**那个行号在那份文件里存在且非空**，不证明那一行写的就是卡里说的那件事 —— "
      "后一半是内容比对，这把尺子不做（做就要人工定每句该对上什么词）。")
    W("")
    W("== 二 · 判据（红的那两类）==")
    W("  越界（引用行号 > 那份现长）%d 条 ｜ 指到空行 %d 条" % (
      len([r for r in rows if r[5] == "越界"]), len([r for r in rows if "指到空行" in r[6]])))
    for r in red_rows:
        W("  红：看板第 %d 行 `%s` → %s（引用 %s 行｜那份 %s 行）｜ %s" % (r[0], r[1][:40], r[2], r[4], r[3], r[6]))
    if not red_rows:
        W("  （这条判据本轮成立 —— 但成立只到「行号落得进去」这一层）")
    W("")
    W("== 三 · 核不了 / 不适用（分开印，不并入可核）==")
    def cls(t):
        for k, v in (("代称", "代称（引用紧跟「读数／对账件／脚本」这一类字）"),
                     ("不可归属", "本行没有文件名（不回扫上文）"),
                     ("非读数件", "本行只有 .md／代码名，`第 N 行` 不猜"),
                     ("多命中", "同名多命中，尺子不猜"),
                     ("指到空行", "行号落得进去但那一行是空白"),
                     ("找不到", "文件名在找过的目录与索引里都没有")):
            if k in t:
                return v
        return "其它"
    by_cls = {}
    for r in rows:
        if r[5] in ("核不了", "不适用"):
            by_cls.setdefault(cls(r[6] if r[5] == "核不了" else r[6]), []).append(r)
    W("  按原因分类（这几类不是「看板错了」，是这条引用尺子没法机械核 —— 逐类给数，不并成一个数）：")
    for k in sorted(by_cls, key=lambda x: -len(by_cls[x])):
        W("    %s ｜ %d 条" % (k, len(by_cls[k])))
    for r in rows:
        if r[5] == "核不了":
            W("  核不了：看板第 %d 行 `%s` ｜ %s ｜ %s" % (r[0], r[1][:40], r[2] or "（无文件）", r[6]))
        elif r[5] == "不适用":
            W("  不适用：看板第 %d 行 `%s` ｜ %s" % (r[0], r[1][:40], r[6]))
    W("")
    W("== 四 · 逐条（可核的那些只印归因方式，全量在退出码里不藏）==")
    for r in rows:
        if r[5] == "可核":
            W("  可核：看板第 %d 行 → %s 第 %s 行（现长 %s）｜ %s" % (r[0], r[2], r[4], r[3], r[6]))
    W("")
    W("== 五 · 自核各臂的读数 ==")
    for a in arms:
        W("  %s 期望 %s ｜ 判 %s ｜ %s" % (a[0], a[1], a[2], a[4]))

    text = "\n".join(buf) + "\n"
    if out_p:
        with io.open(out_p, "w", encoding="utf-8", newline="\n") as f:
            f.write(text)
        print("已落盘：%s" % out_p)
    else:
        print(text)
    print("BL | refs=%d %s" % (len(rows), " ".join("%s=%d" % kv for kv in sorted(tally.items()))))
    if red_rows:
        print("判据红：%d 条越界/空行" % len(red_rows))
        return 4
    return 0


if __name__ == "__main__":
    sys.exit(main())
