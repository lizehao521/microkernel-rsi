# -*- coding: utf-8 -*-
"""看板自校的尺子：段名齐不齐、每张分母对不对得上、卡片有没有被编辑吞掉标题。

为什么要有这一支（10-17 我当面数错三次同一件事）：
 第一版拿 `^- ` 数「在跑」，数出 3 —— 真值 0，因为那 3 行是归档说明不是卡；
 第二版改成「后面跟非标点字符」，数出 1 —— 又错，因为「- （02:53 那一刻…）」这种以全角括号开头的说明被当成了卡；
 第三版才认卡片标记那一组字面 emoji。同一个事实换三种样式得三个数，说明**样式本身要先钉住**，
 所以这条规矩写进文件，不再每次现编正则。

卡片的定义：以 `- ` 开头、紧跟一个已知标记（❓ 等表态｜▫️ 待跑｜✅ 已完成｜⏳/🏃 在跑｜⚠️/⛔ blocked）。
分母的定义：抬头括号里那个数（`## 在跑（0/1）` 取 0；`## 等表态（8）` 取 8）。
没有括号的段（已完成）只数不核。

正对照（钉这把尺子）：已知标段的卡数不可能全为 0 —— 若五个段一个卡都没数到，是标记字面量漂了，exit 6。

跑法：python tasks/probe/board_check.py   （只读，不改看板）
"""
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BOARD = os.path.join(ROOT, "\u62a5\u544a", "\u770b\u677f.md")
MARK = "\u2753\u25ab\u2705\U0001f3c3\u26a0\u26d4\u23f3\u274c"
EXPECT_SECTIONS = 5


def main():
    if not os.path.exists(BOARD):
        print("NOT_RUN: board file missing")
        return 4
    lines = io.open(BOARD, encoding="utf-8").read().splitlines()
    card = re.compile("^- [%s]" % MARK)
    secs = []
    cur = None
    for l in lines:
        if l.startswith("## "):
            cur = {"h": l, "cards": 0, "notes": 0}
            secs.append(cur)
        elif cur is not None and l.startswith("- "):
            cur["cards" if card.match(l) else "notes"] += 1
    print("看板 %s" % BOARD.encode("unicode_escape").decode("ascii"))
    print("段 %d ｜ 应有 %d" % (len(secs), EXPECT_SECTIONS))
    problems = []
    if len(secs) != EXPECT_SECTIONS:
        problems.append("段数不对")
    total = 0
    for s in secs:
        head = s["h"][3:]
        m = re.search("\uff08(\\d+)(?:/\\d+)?\uff09", head)
        decl = int(m.group(1)) if m else None
        total += s["cards"]
        if decl is None:
            print("  %-14s \u5361=%-4d \u6ce8\u91ca=%-3d \u62ac\u5934\u6ca1\u5199\u6570\uff08\u4e0d\u6838\uff09" % (
                head.encode("unicode_escape").decode("ascii")[:14], s["cards"], s["notes"]))
            continue
        ok = decl == s["cards"]
        print("  %-14s \u5361=%-4d \u6ce8\u91ca=%-3d \u62ac\u5934\u5199=%d ｜ %s" % (
            head.encode("unicode_escape").decode("ascii")[:14], s["cards"], s["notes"], decl,
            "OK" if ok else "MISMATCH"))
        if not ok:
            problems.append("head=%d cards=%d" % (decl, s["cards"]))
    inflight = [s for s in secs if "/1" in s["h"]][0] if any("/1" in s["h"] for s in secs) else None
    if inflight and inflight["cards"] > 1:
        problems.append("在跑超上限：%d>1" % inflight["cards"])
    print("自核：五段卡数合计 %d ｜ 标记字面量命中 0 卡 = 尺子漂" % total)
    if total == 0:
        print("坏：一个段都没数到卡，说明 MARK 那串字面量与看板里的标记不再是同一批字符 —— 这份读数不许引用。")
        return 6
    print("问题 %d 处：%s" % (len(problems), " ； ".join(problems) or "无"))
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
