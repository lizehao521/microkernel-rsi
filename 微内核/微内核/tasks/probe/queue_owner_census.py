# -*- coding: utf-8 -*-
"""「待跑 23」这个数里，下一次动作在谁手里 —— 三把机械尺子 + 一份人工逐名表对开。

为什么要这支：看板题头写 `## 待跑（23）`，这个数读起来像"我有 23 件事可以跑"。
它到底等不等我，卡片里没写成一列，也没人数过。这一支把 23 张卡逐名过三把尺子，
再把人工读的那一份并排列在同一件里 —— 谁漏了、谁一致地错了，都逐名印出来。

三把尺子（互相独立取词，都只读 `报告/看板.md` 的字节）：
  甲 · 索取词   正文里出现「等你拍／等你选／等你一句话／你点头／归你／要你认／仍等你／你拍／你选」
  乙 · 选项制   正文里出现「① ② ③」编号选项、「要么…要么」「要不要」「还是」——给了选项就是把选择交出去
  丙 · 前置引用 正文里写了「排在 #」「一起动」「算不算改判据」「动判据表」「等表态」
             （卡片里出现的别卡号**只数不判**：给出处写个 #NN 与"动作挂在别处"是两件事；
              引用 §8 也算给出处 —— 第一版把它写进丙，三张卡的丙是这么误抓的，已剔，见第六节）
判据（并集）：甲∨乙∨丙 任一命中 ⇒ 这张卡等他拍；三把全不命中 ⇒ 这张卡我能自跑。

人工那一列不是第四把尺子，是**读表**：每张卡一行标签加一句理由，
写死在本文件里，谁都可以对着卡片正文逐名复核我这句读得对不对 —— 所以它也能被证伪。

反臂（每次都跑）：在内存里造 4 张假卡（不写盘、不碰树），甲/乙/丙/干净各一张，
判出的标签必须与预期逐张对上；对不上 ⇒ 退出码 5，上面的读数一律不许读成结论。
另造两发分母形状：题头写 7 而实到 1 张、题头写 0 而实到 0 张 —— 前一发要能现数对不上，后一发要被判成「核不了」而不是「齐」。
还有一发对齐闸（第四件事）：卡片数与 `HAND` 行数差 ±1 时必须判「核不了」—— 两边按位置配对，
差一张就从断点之后每一行理由都配到别的卡上，那是一路印下去看着完全正常的假绿。

跑法：python -X utf8 tasks/probe/queue_owner_census.py [输出.txt]
      环境变量 `QOC_BOARD` 可把读的看板换成 TEMP 拷贝（不动在跑的那份）
退出码：0 数完且反臂对上（**这不代表队列健康**，只代表下面那份分布是量出来的）
        5 反臂红 ｜ 题头数与现数对不上（= 核不了）
        3 输出件已存在（拒覆盖上一轮凭据）
"""
import os
import re
import sys
import time

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

ROOT = os.environ.get("QOC_ROOT") or os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BOARD = os.environ.get("QOC_BOARD") or os.path.join(ROOT, "报告", "看板.md")

CARD_RE = re.compile(r"^- ▫️ ", re.M)

ZH = re.compile(r"等你[拍选一]|你点头|归你|要你认|仍等你|你拍一下|等你拍板|等你表态|[（(]你[拍选]")
OPT = re.compile(r"①|②|③|要么.{0,24}要么|要不要|还是只算|等你选|三条修法|两种可能")
PRE = re.compile(r"排在\s*#|一起动|算不算改判据|动判据表|等表态")
NUM = re.compile(r"#(\d{1,3})")
OWN = re.compile(r"^- ▫️ \*{0,2}#(\d{1,3})")

# 人工逐名表：序号 = 卡片在「待跑」那节里的出现顺序（1 起）。
# 标签只有四个：等他拍 ｜ 我能自跑 ｜ 等第三方 ｜ 读数已交·无动作
HAND = [
    ("等他拍", "卡片自述「占端口这件事按规矩要先问再做」「这条我不想自己拍」，两态都要他点头"),
    ("等他拍", "末句写死「修法等你选：① ② ③」"),
    ("等他拍", "末句「等你一句话」：kwire 算不算交付物"),
    ("等他拍", "「修法等你拍① ② ③」＋那支 `mcphttp.exe~` 挪不挪也「等你一句话」"),
    ("等他拍", "零行为改动的一行注释＋一行帮助文本我改得起，但卡片自己写了「排在 #67 一起动」⇒ 前置在一张等表态的卡上"),
    ("等他拍", "标题就是「要不要再推一轮加厚」"),
    ("等他拍", "「我不替你定要不要加闸（加就是把随便写改成拒收＝改判据行为）」"),
    ("等他拍", "「修法等你拍：① ② ③」"),
    ("等他拍", "「改法我没动（那是你的驱动）」"),
    ("等他拍", "「改不改、什么时候改…等你一句话」；改 README 还要重跑打包.py"),
    ("等他拍", "「两条路都不该我替作者走」"),
    ("等他拍", "「这算不算改判据表，等你拍」"),
    ("等他拍", "要补的是判据，而判据表冻在 48 键 49 格 ⇒ 动它归他；卡片里没有「等你」二字，也没有编号选项"),
    ("等他拍", "修它要改导出结构体字段类型，卡片写的是「那是另一轮那句成真了」"),
    ("等他拍", "「那是改驱动，不是改判据，等你点头」"),
    ("等他拍", "「加不加 kernel.derive_budget（工具 10→11）等你拍」"),
    ("等他拍", "尺子已交，欠的那半（并进 kcheck ＝整轮凭据再过期）已立成 #72 等表态"),
    ("等第三方", "缺的是「一个没参与过这个项目的人」，②③ 两件材料我无法替他签"),
    ("等他拍", "「改的是今天能收、改后要拒这个行为，等你点头」"),
    ("等他拍", "「要认的是留给外部读者还是多写的一格，仍等你」"),
    ("我能自跑", "「修法一行：known 查不到时再查另一侧的表」＋配套一根红针；卡片写「不动它也行」但没把选择交给我以外的人"),
]


def hand_guard(n_cards):
    """人工表与卡片**按位置配对**，所以只要两边长度不等，从断点之后每一张的理由都配错了卡 ——
    那是一种会一路印下去、看着完全正常的假绿。长度不等一律判「核不了」。"""
    if n_cards != len(HAND):
        return False, ("卡片 %d 张 vs 人工表 %d 行 ⇒ 从断点之后逐名全配错，这一发核不了（退出码 5）；"
                       "闭合或新增卡片时要把 HAND 一起改" % (n_cards, len(HAND)))
    return True, "长度对上（卡片 %d 张 = 人工表 %d 行）" % (n_cards, len(HAND))


def parse_cards(text):
    m = re.search(r"^## 待跑（(\d+)）\s*$", text, re.M)
    if not m:
        return None, None, "读不到 `^## 待跑（N）$` 那一行题头（这一发核不了）"
    header_n = int(m.group(1))
    rest = text[m.end():]
    nxt = re.search(r"^## ", rest, re.M)
    body = rest[: nxt.start()] if nxt else rest
    starts = [mm.start() for mm in CARD_RE.finditer(body)]
    if not starts:
        return header_n, [], "「待跑」那节里一张 `- ▫️ ` 卡都没解析到（分母为 0，核不了）"
    cards = []
    for i, s in enumerate(starts):
        e = starts[i + 1] if i + 1 < len(starts) else len(body)
        cards.append(body[s:e])
    return header_n, cards, ""


def judge(card):
    return [bool(ZH.search(card)), bool(OPT.search(card)), bool(PRE.search(card))]


def own_of(card):
    """这张卡**自己**的编号，只认标题那一段（`- ▫️ **#71 …`）；
    认正文第一个 #NN 会把"引用了 #63"印成这张卡叫 #63 —— 第一版就错在这一格（已留作废件）。"""
    m = OWN.search(card)
    return m.group(1) if m else None


def other_refs(card):
    """卡片里提到的别卡号（去掉它自己那个）。**只数、不作判据**：
    给出处写个 #NN 与「动作挂在别处」是两件事，混进丙就会把 23 张全判成等他。"""
    own = own_of(card)
    return sorted({n for n in NUM.findall(card) if n != own}, key=int)


def label(card):
    hits = judge(card)
    why = "、".join(n for n, h in zip("甲乙丙", hits) if h)
    return ("等他拍" if any(hits) else "我能自跑"), why


def card_key(idx, card):
    o = own_of(card)
    head = re.sub(r"\s+", " ", re.sub(r"^- ▫️\s+", "", card.strip(), count=1))
    return ("#%s" % o if o else "无名"), head


def selfcheck(w):
    """反臂：四张假卡必须各按预期判出来；分母对不上的那一发必须判「核不了」。"""
    arms = [
        ("- ▫️ 假卡甲：这一条要不要加闸，等你拍。\n", "等他拍", "甲"),
        ("- ▫️ 假卡乙：修法 ① 搬 ② 不搬 ③ 只写说明。\n", "等他拍", "乙"),
        ("- ▫️ 假卡丙：这条排在 #7 一起动，等那轮跑完。\n", "等他拍", "丙"),
        ("- ▫️ 假卡干净：把 known 换成两列，改一行，配一根红针。\n", "我能自跑", ""),
    ]
    bad = 0
    w("== 反臂 · 这把尺子自己会不会红（四张假卡，逐张）==")
    for body, want, which in arms:
        got, hits = label(body)
        ok = got == want
        if not ok:
            bad += 1
        w("  %s ｜ 预期 %s ｜ 现判 %s ｜ 命中 %s ｜ 卡片正文：%s"
          % ("对上" if ok else "✗ 对不上", want, got, hits or "（三把全不命中）", body.strip()[:52]))
    neg_header, neg_cards, neg_why = parse_cards("## 待跑（7）\n\n- ▫️ 只有一张卡。\n")
    ok2 = neg_header == 7 and len(neg_cards) == 1
    if not ok2:
        bad += 1
    w("  %s ｜ 题头分母与现数不同那一发：题头 %s ｜ 现数 %d ｜ 这一发在下面的判据里必须读成「核不了」（不许读成齐）"
      % ("形状造出来了" if ok2 else "✗ 形状没造出来", neg_header, len(neg_cards)))
    empty_h, empty_c, empty_why = parse_cards("## 待跑（0）\n\n正文里没有卡片。\n")
    ok3 = empty_h == 0 and len(empty_c) == 0
    if not ok3:
        bad += 1
    w("  %s ｜ 一张卡都没有那一发：题头 %s ｜ 现数 %d ｜ 解析话是「%s」"
      % ("形状造出来了" if ok3 else "✗ 形状没造出来", empty_h, len(empty_c), empty_why))
    w("  反臂 · 卡名那一格（第一版错在这里：把正文引用的第一个 #NN 当成这张卡的名字）：")
    keys = [
        ("- ▫️ **#71 三个探针…**（读数见 #63）\n", "#71", "1"),
        ("- ▫️ **交付包里的三份构建凭据…** 先撞 #63 那类写死路径\n", "无名", "1"),
    ]
    for body, want, nref in keys:
        got, _h = card_key(1, body)
        got_ref = str(len(other_refs(body)))
        okk = got == want and got_ref == nref
        if not okk:
            bad += 1
        w("    %s ｜ 预期卡名 %s ｜ 现读 %s ｜ 预期别卡引用 %s 个 ｜ 现数 %s 个 ｜ 正文：%s"
          % ("对上" if okk else "✗ 对不上", want, got, nref, got_ref, body.strip()[:46]))
    w("  反臂 · 人工表与卡片的对齐闸（长度差一张，从断点之后每行理由都配错卡）：")
    for n, want in ((len(HAND), True), (len(HAND) + 1, False), (len(HAND) - 1, False)):
        got, msg = hand_guard(n)
        okg = got == want
        if not okg:
            bad += 1
        w("    %s ｜ 卡片 %d 张 vs 表 %d 行 ⇒ 应判 %s ｜ 现判 %s ｜ %s"
          % ("对上" if okg else "✗ 对不上", n, len(HAND), "核不了" if not want else "能数",
             "核不了" if not got else "能数", msg))
    w("  反臂结论：%s（四件事：甲/乙/丙各抓一张、干净那张不抓、分母形状能造出来、对齐闸会红）"
      % ("对上，下面那份分布才许读" if bad == 0 else "✗ %d 件对不上 ⇒ 退出码 5，别读成结论" % bad))
    w("")
    return bad == 0


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
        ROOT, "报告", "待跑队列-下一次动作在谁手里-三把尺子加人工表-1019-%s.txt" % time.strftime("%H%M%S"))
    if os.path.exists(out):
        sys.stderr.write("REFUSE_TO_OVERWRITE " + out + "\n")
        return 3
    buf = []
    w = buf.append
    w("待跑队列 · 下一次动作在谁手里 —— 三把机械尺子 + 一份人工逐名表")
    w("生成 %s ｜ 看板 %s ｜ 只读，一行没动" % (time.strftime("%Y-%m-%d %H:%M:%S"), BOARD))
    if not os.path.isfile(BOARD):
        w("读不到看板（核不了，不是「没卡」）")
        open(out, "w", encoding="utf-8", newline="\r\n").write("\r\n".join(buf) + "\r\n")
        return 5
    text = open(BOARD, encoding="utf-8", errors="replace").read()
    ok = selfcheck(w)
    header_n, cards, why = parse_cards(text)
    if header_n is None:
        w("== 判据 ==")
        w("  %s ⇒ 核不了，退出码 5" % why)
        open(out, "w", encoding="utf-8", newline="\r\n").write("\r\n".join(buf) + "\r\n")
        return 5
    if not ok:
        w("== 判据 ==")
        w("  反臂没对上 ⇒ 这把尺子自己漂了，退出码 5，上面那份分布不许读成结论")
        open(out, "w", encoding="utf-8", newline="\r\n").write("\r\n".join(buf) + "\r\n")
        return 5
    w("== 一 · 分母自印 ==")
    gh_ok, gh_msg = hand_guard(len(cards))
    w("  看板题头写「待跑（%d）」｜ 这发按 `- ▫️ ` 现数 = %d ｜ 两者相等 = %s ｜ 人工表对齐闸：%s%s"
      % (header_n, len(cards), header_n == len(cards), gh_msg,
         "" if header_n == len(cards) and gh_ok else ("" if not why else " ｜ 解析提示：" + why)))
    if header_n != len(cards) or not gh_ok or why:
        w("  ⇒ 分母对不上、人工表与卡片不等长、或解析有话：这一发读成「核不了」（退出码 5），不读成分布。")
        open(out, "w", encoding="utf-8", newline="\r\n").write("\r\n".join(buf) + "\r\n")
        return 5
    w("")
    w("== 二 · 逐名（每张卡：三把尺子命中哪几把 ｜ 机械并集标签 ｜ 人工标签 ｜ 一致否）==")
    w("    甲=索取词 乙=选项制 丙=前置引用；机械判据 = 甲∨乙∨丙 ⇒ 等他拍，全不命中 ⇒ 我能自跑")
    agree = 0
    diff = []
    hand_n = {}
    mech_n = {}
    hand_his = set()
    mech_his = set()
    for i, c in enumerate(cards):
        key, head = card_key(i + 1, c)
        mech, hits = label(c)
        hv, hr = HAND[i]
        same = mech == hv
        if same:
            agree += 1
        else:
            diff.append((i + 1, key, mech, hv, hits, hr))
        hand_n[hv] = hand_n.get(hv, 0) + 1
        mech_n[mech] = mech_n.get(mech, 0) + 1
        if mech == "等他拍":
            mech_his.add(i + 1)
        if hv == "等他拍":
            hand_his.add(i + 1)
        w("  %2d %-4s %-34s 机械[%-4s] %-6s ｜ 人工 %-8s ｜ %s ｜ 另引用别卡 %d 个（只数、不判）"
          % (i + 1, key, head[:34], hits or "—", mech, hv, "一致" if same else "分歧", len(other_refs(c))))
        w("        人工那句理由：%s" % hr)
    w("")
    w("== 三 · 两本账并排 ==")
    w("  机械并集：%s" % " ｜ ".join("%s %d" % (k, v) for k, v in sorted(mech_n.items())))
    w("  人工读表：%s" % " ｜ ".join("%s %d" % (k, v) for k, v in sorted(hand_n.items())))
    w("  逐名一致 %d ／ %d ｜ 分歧 %d" % (agree, len(cards), len(diff)))
    inter = sorted(mech_his & hand_his)
    w("  **两个「等他拍」是不是同一批**：机械 %d 张 ∩ 人工 %d 张 = %d 张 ｜ 只在机械里 %s ｜ 只在人工里 %s"
      % (len(mech_his), len(hand_his), len(inter),
         "、".join("第%d张" % i for i in sorted(mech_his - hand_his)) or "0 张",
         "、".join("第%d张" % i for i in sorted(hand_his - mech_his)) or "0 张"))
    w("  ⇒ 两个数一样不等于同一批卡；引用这一份分布时按**逐名**取，别取那个总数。")
    w("")
    w("== 四 · 分歧逐名归因（不是总数，一张一行）==")
    if not diff:
        w("  0 张 —— 两本账逐名对上。")
    for idx, key, mech, hv, hits, hr in diff:
        w("  第 %2d 张 %s：机械判「%s」（命中 %s），人工判「%s」｜ 人工理由：%s"
          % (idx, key, mech, hits or "三把全不命中", hv, hr))
    w("")
    w("== 五 · 「blocked（0）」那句也数一遍 ==")
    pre_cards = [(i + 1, card_key(i + 1, c)[0]) for i, c in enumerate(cards) if judge(c)[2]]
    m = re.search(r"^## blocked（(\d+)）\s*$", text, re.M)
    b_now = None
    if m:
        brest = text[m.end():]
        bnext = re.search(r"^## ", brest, re.M)
        bbody = brest[: bnext.start()] if bnext else brest
        b_now = len(re.findall(r"^- ", bbody, re.M))
    w("  看板题头写 blocked（%s）｜ 那一节里现数 `- ` 开头的条目 = %s"
      % (m.group(1) if m else "读不到题头", "读不到那节" if b_now is None else "%d 条" % b_now))
    w("  「待跑」那节里丙命中的卡（正文写了「排在 #」「一起动」「算不算改判据」「动判据表」「等表态」之一）= %d 张：%s"
      % (len(pre_cards), "、".join("#%d(%s)" % (a, b) for a, b in pre_cards) or "0 张"))
    w("  ⇒ 这一句只报数：blocked 那一节现在有几条是盘面事实，「待跑里有 %d 张的动作挂在别处」是另一件事，两者不合成结论。"
      % len(pre_cards))
    w("")
    w("== 六 · 这把尺子看不见的路（谁引用这份读数都该知道）==")
    w("  甲乙丙三把全是**卡片正文里的措辞**，所以：")
    w("    · 卡片把「等他」写成「引用另一张等表态的卡」时，甲乙抓不到，只有丙抓到 ⇒ 丙漏了就整张漏；")
    w("    · 卡片把「等你拍」写成「这条排稳定性那两支之后」这种无标记句子时，三把都不抓 ⇒ 机械说我能自跑、人工说等他；")
    w("    · 反过来，卡片里出现 §8 或别卡号**只是为了给出处**（不是前置）：别卡号只数不判（见每行末列）；")
    w("      §8 第一版写在丙里，实测误抓 3 张（交付包凭据／README 22 根／#56 —— 都引用 §8 给出处），已剔 ⇒ 这一族留了痕；")
    w("      乙也会误抓：① ② 有两种用法，「修法 ① ② ③」是交选择，「两个毛病：①…②…」是列毛病，这一把分不开 ⇒ 会蒙对也会蒙错；")
    w("  所以这一支给的是「两本账逐名分歧几张」，不给「待跑真实数量」那个数。")

    with open(out, "w", encoding="utf-8", newline="\r\n") as f:
        f.write("\r\n".join(buf) + "\r\n")
    print("cards=%d hand=%s mech=%s diff=%d out=%s" % (len(cards), hand_n, mech_n, len(diff), out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
