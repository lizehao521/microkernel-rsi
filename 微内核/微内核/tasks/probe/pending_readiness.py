# -*- coding: utf-8 -*-
"""量一件事：「待跑」里每一张卡，它欠的那一步现在**在不在盘上**。

为什么要这一支（10-19 的教训形状）：#37、#55 那两张挂在「待跑」的卡，读数其实早就在盘上、
卡上没有下一步动作 —— 那种「账面假欠」靠人记是记不住的（我就是靠人肉搬走的）。
这张尺子把「读数已在盘上」做成机械判据，红着不许读成「还在排队」。

口径（每条都印它用的是哪一类，不合并、不塌成两态）：
  A 件齐·我能自跑      —— 卡点名了尺子脚本且该脚本在盘上，且卡里没有「等他拍/归你定」这类字样
  A2 件齐·下一步是问句 —— 尺子也在盘上，但同一张卡把下一步写成「要不要/是否」或明写着「那是你的驱动」⇒ 不红、不并进 A
  B 读数已交·卡上没下一步 —— 点名的读数件在盘上，且卡里带「没有下一步／无动作／读数已交」这类**字面痕迹** ⇒ **判据红**（假欠）
  B2 卡里引了旧读数（上下文） —— 读数件在盘上、但卡里没写上面那类字样 ⇒ **不红**（它欠的通常是「把读数换成判据」那一步）
  C 等他拍              —— 卡里带决策字样（归你／你定／你拍／等他拍／要他圈／表态）
  D 缺件                —— 卡点名了脚本或读数件，但一份都不在盘上
  E 核不了              —— 卡里没点名任何可比对的文件（不回扫上文、不替卡猜它要跑什么）
「可核」只证明**那个文件此刻在盘上**，不证明它就是卡里欠的那一步 —— 后一半要人读。

跑法：python tasks/probe/pending_readiness.py <输出件路径>   （只读，不动看板）
退出码：0 数完且判据不红 ｜ 4 判据红（B 类或题头数不符）｜ 5 自核红或核不了 ｜ 3 输出件已存在
"""
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BOARD = os.path.join(ROOT, "报告", "看板.md")
EXTRA_DIRS = [os.path.join(os.path.dirname(ROOT), "构建-1017")]

CARD = re.compile(r"^- [\u25ab]")            # 待跑标记
ANY_CARD = re.compile(r"^- [\u2753\u25ab\u2705\U0001f3c3\u26a0\u26d4\u23f3\u274c]")
HEAD_NUM = re.compile("\uff08(\\d+)(?:/\\d+)?\uff09")
FILE_TOKEN = re.compile(r"[A-Za-z0-9_\-\u4e00-\u9fff][A-Za-z0-9_\-\u4e00-\u9fff.]*\.(?:py|go|sh|txt|jsonl|csv)")
SCRIPT_SFX = (".py", ".go", ".sh")
READOUT_SFX = (".txt", ".jsonl", ".csv")
DECIDE = ("归你", "你定", "你拍", "要他圈", "等他拍", "等他圈", "表态", "由你选", "你选", "归你看")
READOUT_WORD = ("读数", "of record", "见证件", "对账件")
# B 类要红，必须有「这一步已经做完」的字面痕迹；只引用旧读数 = 上下文，不算假欠
NOACT = ("没有下一步", "无动作", "读数已交", "读数已经交齐", "读数交齐")
# A2：尺子在盘上，但卡把下一步写成了问句 / 明写着归他 ⇒ 下一步不轮到我跑
ASK = ("要不要", "是否", "算不算")
HISDRIVE = ("你的驱动", "那是你的", "归你的")


def build_index(dirs):
    idx = {}
    for d in dirs:
        if not os.path.isdir(d):
            continue
        for sub, dirs2, files in os.walk(d):
            dirs2[:] = [x for x in dirs2 if x not in ("__pycache__", ".git", "node_modules", ".bin")]
            for fn in files:
                idx.setdefault(fn.lower(), []).append(os.path.join(sub, fn))
    return idx


def exists_any(name, dirs, idx):
    bare = os.path.basename(name.replace("\\", "/")).lower()
    p = name if os.path.isabs(name) else os.path.join(ROOT, name)
    if os.path.isfile(p):
        return [p]
    for d in dirs:
        q = os.path.join(d, bare)
        if os.path.isfile(q):
            return [q]
    return list(idx.get(bare, []))


def parse_cards(lines):
    cards, cur, inpending = [], None, False
    decl = None
    for i, l in enumerate(lines, 1):
        if l.startswith("## "):
            if cur:
                cards.append(cur)
                cur = None
            if "待跑" in l:
                inpending = True
                m = HEAD_NUM.search(l)
                decl = int(m.group(1)) if m else None
                continue
            inpending = False
            continue
        if not inpending:
            continue
        if ANY_CARD.match(l):
            if cur:
                cards.append(cur)
            m = CARD.match(l)
            cur = {"i": i, "t": l[2:].strip(), "body": [l], "is_card": bool(m)}
            continue
        if cur is not None:
            cur["body"].append(l)
    if cur:
        cards.append(cur)
    real = [c for c in cards if c["is_card"]]
    return real, decl, len(cards) - len(real)


def classify(c, dirs, idx):
    text = "\n".join(c["body"])
    names = []
    for m in FILE_TOKEN.finditer(text):
        nm = m.group(0)
        if nm not in names:
            names.append(nm)
    scripts = [n for n in names if os.path.splitext(n)[1].lower() in SCRIPT_SFX]
    readouts = [n for n in names if os.path.splitext(n)[1].lower() in READOUT_SFX]
    hit_scripts = [n for n in scripts if exists_any(n, dirs, idx)]
    hit_readouts = [n for n in readouts if exists_any(n, dirs, idx)]
    decided = [w for w in DECIDE if w in text]
    said_readout = [w for w in READOUT_WORD if w in text]
    if decided:
        extra = ""
        if hit_readouts and said_readout:
            extra = " ｜ 注：这张**同时**有 B 的形状（读数件在盘上：%s），但卡里带决策字样 ⇒ 归 C，不并进 B" % "、".join(
                os.path.basename(x) for x in hit_readouts[:2])
        return "C 等他拍", "带决策字样 %s ｜ 件：脚本在 %d／读数在 %d（不并入其它类）%s" % (
            "、".join(decided), len(hit_scripts), len(hit_readouts), extra), names
    if not names:
        return "E 核不了", "卡里没点名任何可比对的文件，也没有决策字样（不回扫上文、不替它猜要跑什么）", names
    if hit_readouts and said_readout:
        na = [w for w in NOACT if w in text]
        fn = "、".join(sorted(set(os.path.basename(x) for x in hit_readouts))[:3])
        if na:
            return "B 读数已交·卡上没下一步", "卡里带「%s」字样，且点名的读数件在盘上：%s" % (na[0], fn), names
        return "B2 卡里引了旧读数（上下文）", "点名的读数件在盘上：%s ｜ 但卡里**没有**「没有下一步／无动作／读数已交」这类字样 ⇒ **不算假欠、不红**（它欠的可能是「把读数换成判据」那一步）" % fn, names
    if hit_scripts:
        fns = "、".join(os.path.basename(x) for x in hit_scripts[:3])
        ask = [w for w in ASK + HISDRIVE if w in text]
        if ask:
            return "A2 件齐·下一步是问句或明写归他", "尺子在盘上：%s ｜ 同一张卡里带 %s ⇒ **不红、也不进 A**（要先有人点头才轮得到跑）" % (
                fns, "、".join(ask)), names
        return "A 件齐·我能自跑", "卡点名的尺子在盘上：%s" % fns, names
    if hit_readouts:
        return "B2 卡里引了旧读数（上下文）", "点名的读数件在盘上、但卡里连「读数」二字都没写 ⇒ 只能算上下文引用，**不红**：%s" % "、".join(
            os.path.basename(x) for x in hit_readouts[:3]), names
    return "D 缺件", "点名的件一份都不在盘上：%s" % "、".join(names[:4]), names


def lbl(t):
    m = re.search(r"\*\*([\u4e00-\u9fff])", t)
    return m.group(1) if m else t[:4]


def selfcheck():
    """TEMP 微型假看板 + 假件，跑真判据；返回 (ok, 各臂)。"""
    import tempfile, shutil
    d = tempfile.mkdtemp(prefix="pending_readiness_selfcheck-")
    arms = []
    try:
        rdir = os.path.join(d, "报告")
        os.makedirs(rdir)
        io.open(os.path.join(d, "good.py"), "w", encoding="utf-8").write("# -*- coding: utf-8 -*-\n")
        io.open(os.path.join(d, "报告", "有读数.txt"), "w", encoding="utf-8").write("BL 有一行\n")
        board = "\n".join([
            "## 待跑（8）",
            "",
            "- \u25ab **甲 有尺子没有读数，也不等人拍**（10-19 01:00，尺子 `good.py`）：还没跑。",
            "- \u25ab **癸 尺子在盘上但要他先点头**：要不要再推一轮加厚（尺子 `good.py`）。",
            "- \u25ab **乙 读数早就在盘上**：读数 `报告/有读数.txt` 第 1 行，卡上没有下一步动作。",
            "- \u25ab **丑 引了旧读数但欠的是改判据**：读数 `报告/有读数.txt` 第 1 行；下一步 = 把这条读数做成判据。",
            "- \u25ab **丙 缺件**：要跑 `tasks/probe/这份不存在.py`。",
            "- \u25ab **丁 等他拍**：改法归你定（① 或 ②）。",
            "- \u25ab **戊 没点名任何件**：等一个外部人来接手，具体要什么卡里没说。",
            "  （这一行是注释缩进，不该另算一张卡）",
            "",
        ])
        bp = os.path.join(d, "报告", "看板.md")
        io.open(bp, "w", encoding="utf-8", newline="\n").write(board)
        lines = io.open(bp, encoding="utf-8").read().splitlines()
        cards, decl, notes = parse_cards(lines)
        got = {}
        for c in cards:
            k, why, _n = classify(c, [rdir, d] + EXTRA_DIRS, build_index([d, rdir]))
            got[lbl(c["t"])] = k
        exp = {"甲": "A 件齐·我能自跑", "癸": "A2 件齐·下一步是问句或明写归他", "乙": "B 读数已交·卡上没下一步",
               "丑": "B2 卡里引了旧读数（上下文）", "丙": "D 缺件",
               "丁": "C 等他拍", "戊": "E 核不了"}
        for ch, want in exp.items():
            gotc = got.get(ch)
            arms.append((ch, want, gotc, gotc == want, ""))
        arms.append(("辛 注释不当卡", "卡 7 张", "%d 张 / 注释 %d 行" % (len(cards), notes),
                     len(cards) == 7, ""))
        arms.append(("己 题头数与实到不符要判红", "decl=8 != 卡 7", "decl=%s 卡=%d" % (decl, len(cards)),
                     decl is not None and decl != len(cards), ""))
        # 庚 反臂：把乙那张点名的读数件从盘上撤掉 ⇒ 必须从 B 翻成 D（证明真在查盘，不是恒红）
        os.remove(os.path.join(d, "报告", "有读数.txt"))
        cards2, _, _ = parse_cards(lines)
        for c in cards2:
            if lbl(c["t"]) == "乙":
                k2, _, _n = classify(c, [rdir, d] + EXTRA_DIRS, build_index([d, rdir]))
                arms.append(("庚 反臂撤件后必须翻类", "不是 B", "%s" % k2, k2 != "B 读数已交·卡上没下一步", ""))
        # 壬 正对照：把件再放回去 ⇒ 必须翻回 B
        io.open(os.path.join(d, "报告", "有读数.txt"), "w", encoding="utf-8").write("BL 有一行\n")
        for c in cards2:
            if lbl(c["t"]) == "乙":
                k3, _, _n = classify(c, [rdir, d] + EXTRA_DIRS, build_index([d, rdir]))
                arms.append(("壬 正对照放回后翻回 B", "B 读数已交·卡上没下一步", "%s" % k3,
                             k3 == "B 读数已交·卡上没下一步", ""))
        gui = [c for c in cards2 if lbl(c["t"]) == "癸"][0]
        stripped = {"i": gui["i"], "t": gui["t"], "is_card": True,
                    "body": [x.replace(u"要不要", "") for x in gui["body"]]}
        k4, _, _n = classify(stripped, [rdir, d] + EXTRA_DIRS, build_index([d, rdir]))
        arms.append(("戌 反臂·撤掉「要不要」必须翻成 A", "A 件齐·我能自跑", "%s" % k4,
                     k4 == "A 件齐·我能自跑", ""))
        k5, _, _n = classify(gui, [rdir, d] + EXTRA_DIRS, build_index([d, rdir]))
        arms.append(("未 正对照·原样必须回到 A2", "A2 件齐·下一步是问句或明写归他", "%s" % k5,
                     k5 == "A2 件齐·下一步是问句或明写归他", ""))
        ok = all(a[3] for a in arms)
        return ok, arms, len(cards)
    finally:
        shutil.rmtree(d, ignore_errors=True)


def main():
    if len(sys.argv) > 1 and os.path.exists(sys.argv[1]):
        sys.stdout.write("REFUSE_TO_OVERWRITE %s\n" % sys.argv[1])
        return 3
    ok, arms, n_prev = selfcheck()
    if not ok:
        bad = [a for a in arms if not a[3]]
        sys.stdout.write("SELFCHECK-RED 自核红 %d/%d 臂（先不出一份正文读数）\n" % (len(bad), len(arms)))
        for a in bad:
            sys.stdout.write("  臂 %s ｜ 期望 %s ｜ 判 %s\n" % (a[0], a[1], a[2]))
        return 5
    lines = io.open(BOARD, encoding="utf-8").read().splitlines()
    cards, decl, notes = parse_cards(lines)
    dirs = [os.path.join(ROOT, "报告"), ROOT, "."] + EXTRA_DIRS
    idx = build_index([ROOT] + EXTRA_DIRS)
    rows = []
    for c in cards:
        k, why, names = classify(c, dirs, idx)
        m = re.search(r"#(\d+)", c["t"])
        rows.append((m.group(1) if m else "?%d" % c["i"], c["t"][:46], k, why, len(names)))
    tally = {}
    for r in rows:
        tally[r[2]] = tally.get(r[2], 0) + 1
    reds = []
    if decl is not None and decl != len(cards):
        reds.append("题头写 %d ｜ 实到卡 %d（不含注释 %d 行）" % (decl, len(cards), notes))
    bs = [r for r in rows if r[2].startswith("B ")]
    b2 = [r for r in rows if r[2].startswith("B2")]
    if bs:
        reds.append("B 类 %d 张：读数已交、卡上没有下一步动作，却还挂在待跑（账面假欠）" % len(bs))

    out = []
    W = out.append
    W("待跑卡「欠的那一步在不在盘上」逐张对账 ｜ 生成 %s" % __import__("datetime").datetime.now().strftime("%Y-%m-%d %H:%M:%S"))
    W("尺子 `tasks/probe/pending_readiness.py`（只读，不动看板、不动任何卡）")
    W("自核 %d 臂全对上（TEMP 微型假看板跑真判据；两条反臂 = 撤掉读数件必须翻类、撤掉「要不要」必须从 A2 翻成 A；两条正对照 = 放回必须翻回 B、原样必须回到 A2）" % len(arms))
    W("")
    W("== 一 · 分母自印 ==")
    W("  待跑卡 %d 张（题头写 %s）｜ 段内注释行 %d ｜ 点名可查的文件名共 %d 个" % (
      len(cards), decl, notes, sum(x[4] for x in rows)))
    W("  逐态：%s" % "　".join("%s %d" % kv for kv in sorted(tally.items())))
    W("  B2 是**刻意分开的一类**（%d 张）：卡里引了旧读数当上下文，但欠的那一步是「把读数变成判据/改代码」——" % len(b2))
    W("     第一版把这一类并进了 B，一次报出 **11 张假「账面假欠」**，正是「一个判据把两种形状读成一个数」；改开后 B 只认「没有下一步／无动作／读数已交」这类字面痕迹。")
    W("  找件用了这几处：%s（另有全树文件名索引 %d 个名字）" % (
      "、".join(os.path.relpath(x, ROOT).replace("\\", "/") or "." for x in dirs), len(idx)))
    W("  口径：**「在盘上」只证明那个文件此刻存在**，不证明它就是卡里欠的那一步；后一半要人读，这一发不做。")
    W("  A2 也是**刻意分开的一类**（%d 张）：尺子在盘上，可卡把下一步写成了问句或明写着归他 ⇒ 它不该算进「我能自跑」。"
      % tally.get("A2 件齐·下一步是问句或明写归他", 0))
    W("     这一类是 10-19 手开对账逼出来的：尺子头一发报 **A 3 张**，人工表 `queue_owner_census.py` 报 **我能自跑 1**，"
      "逐名开三张卡后 #62 的卡头自印「已跑完」、剩下的问句是「要不要再推一轮加厚」，#53 的卡尾写着「改法我没动（那是你的驱动）」"
      "⇒ 那两张的下一步不在我手里，只有 #58 是。第一版把这算成尺子读错，其实是**口径少了这一态**。")
    W("")
    W("== 二 · 判据（红的那两类）==")
    if reds:
        for x in reds:
            W("  红：%s" % x)
    else:
        W("  （两类判据本轮都不红：没有 B 类，题头数与实到卡数一致）")
    W("")
    W("== 三 · 逐张归因 ==")
    for cid, title, k, why, nn in rows:
        W("  #%-4s %-46s ｜ %s ｜ %s" % (cid, title, k, why))
    W("")
    W("== 四 · 这把尺子看不到什么 ==")
    W("  · 一张卡该不该搬走它不知道（搬卡改「待跑」分母，撞上 #73 甲正在拍的那句口径 ⇒ 归你，我只把 B 类红着列出来）；")
    W("  · 「A 件齐」不等于「我能自跑」：#58 与 #53 点名的 `cells.go`、`tasks/mcp/llm_probe.py` **都在回滚基线 1019n 里**（第 4、50 行）⇒ 动它们就是在树字节变、80 根整轮凭据过期，这一发量不出该不该动；")
    W("  · C 类是按**卡里的字样**判的，不是按我读没读出来 —— 字样漏写的卡会掉进 A/D/E，不塌成「等拍」。")
    W("")
    W("BL | cards=%d 题头=%s B类=%d A含A2=%d C=%d D=%d E=%d | 退出码 %s" % (
      len(cards), decl, len(bs),
      tally.get("A 件齐·我能自跑", 0) + tally.get("A2 件齐·下一步是问句或明写归他", 0),
      tally.get("C 等他拍", 0),
      tally.get("D 缺件", 0), tally.get("E 核不了", 0), 4 if reds else 0))
    body = "\n".join(out) + "\n"
    if sys.argv[1:]:
        io.open(sys.argv[1], "w", encoding="utf-8", newline="\r\n").write(body)
        sys.stdout.write("已落盘：%s\n" % sys.argv[1])
    else:
        sys.stdout.write(body.encode("utf-8", "replace").decode("utf-8"))
    return 4 if reds else 0


if __name__ == "__main__":
    sys.exit(main())
