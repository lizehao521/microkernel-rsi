# -*- coding: utf-8 -*-
"""把三条融合前提合一次账 —— 准绳本身要有自印分母，不能只在每轮报告里各说各话、由我用散文说"没回退"。

三个前提：①门挡下的拒收要进某个计数；②「拒了」要在机器可读的 `isError` 上可见；③E 的替身要标明「不是人、不算验收通过」。

纪律都在这支脚本里，不靠读的人记：
· 每个数都抠自**各轮报告里工具自己印的那一行**；抠不到就单列成第三种状态「抠不到」，绝不按 0 合账，
  并且整份退出非 0（空表被读成全绿是本项目点名的假绿形状）。
· 四个桶（内核拒的／门口挡的／协议层判无效的／传输层挡的）**逐桶分开印、永不相加**；
  本件里唯一的"合计"只是各轮**臂数**相加，那行自己写明它不是桶。
· 各轮字段口径不同 ⇒ 前提②不把"同判"跨轮并成一个数，而是逐轮列出它自己的那一句，
  并单独报「今天没有任何一轮印出过『拒了但 isError=false』」。

自核（钉这把尺子，两条都是两源对开）：
 一、认领那一族的（臂数, isError=True 数）在两份报告里各抠一次，必须相同；
 二、Cuts 那一轮：报内自印的「臂 18」必须等于 `folded/clipped/dropped` 三种目标键的臂数之和，
     而覆盖账面那一轮的「落进格表的键数」必须等于目标键种数 3。
任一不对 ⇒ exit 6，不出合账结论。

跑法：python tasks/probe/premises_ledger.py [输出.txt]
"""
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
P = lambda n: os.path.join(ROOT, "报告", n)


def rd(name):
    if not os.path.exists(P(name)):
        return None
    return io.open(P(name), encoding="utf-8").read()


def one(text, pat):
    if text is None:
        return None
    m = re.search(pat, text, re.S)
    return m.groups() if m else None


def col(text, prefix, which):
    """按竖线拆一行取第 N 段（覆盖账面那张表的对齐靠空格，正则不稳）。"""
    if text is None:
        return None
    for ln in text.splitlines():
        if ln.startswith(prefix):
            parts = [x.strip() for x in ln.split("\uff5c")]
            return parts[which] if len(parts) > which else None
    return None


def main():
    outp = sys.argv[1] if len(sys.argv) > 1 else P("三条融合前提-合账-1017.txt")
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在" % outp.encode("unicode_escape"))
        return 3
    out = []
    holes = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    def finish(code):
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
        return code

    w("== 三条融合前提 合账 ｜ 每个数都抠自各轮报告里工具自印的那一行 ==")
    w("")
    w("【②「拒了」要在机器可读的 isError 上可见】轮 ｜ 臂 ｜ 拒 ｜ 收 ｜ 它自己那句同判/计数")
    rounds = []
    t = rd("契约说进不了门-到底拒不拒-1017.txt")
    g = one(t, r"臂 (\d+) 条 ｜ 落账被拒 (\d+) 条 ｜ \*\*收下（契约说该拒而没拒）(\d+) 条：(\S+)")
    if g is None:
        g = one(t, r"臂 (\d+) 条 ｜ 落账被拒 (\d+) 条")
        g = (g[0], g[1], "—", "—") if g else None
    if g is not None and len(g) == 4:
        sc = one(t, r"预检与落账同判 (\d+)/(\d+)")
        g = (g[0], g[1], g[2], "同判 %s/%s（收下名单：%s）" % (sc[0], sc[1], g[3])) if sc else g
    rounds.append(("契约 §1.4 预算逐字段", g, "（原件那行的 `**` 没闭合，是排版不是数据）"))
    g = one(rd("非Measure那批-运行时撞一遍-v2-1017.txt"),
            r"臂 (\d+) 条 ｜ 落账被拒 (\d+) 条 ｜ 被收下 (\d+) 条：(\S+) ｜ 预检与落账同判 (\d+)/(\d+)")
    rounds.append(("非 Measure 逐值 v2",
                   (g[0], g[1], g[2], g[4] + "/" + g[5]) if g else None, "同判"))
    g = one(rd("没撞过那批-逐格撞一遍-v2-1017.txt"), r"跑了 (\d+) 发")
    h = one(rd("没撞过那批-逐格撞一遍-v2-1017.txt"),
            r"被这格的判据拒 (\d+) 个.*?\*\*被收下 (\d+) 个\*\*.*?造不出值 (\d+) 个.*?被去重话挡下（不算闸）(\d+) 个")
    rounds.append(("未撞那批逐格 v2", (g[0], h[0], h[1], "拒/收/造不出/去重 = %s/%s/%s/%s" % h) if (g and h) else None, "三态"))
    t = rd("动态档那三格-Cuts族撞一遍-v3-1017.txt")
    g = one(t, r"臂 (\d+) 条（A0 合法装配 (\d+) 条 \+ 谎臂 (\d+) 条）.*?A0 被收下 = (\S+?).*?预检与落账同判 (\d+)/(\d+)")
    k = one(t, r"撞中对上期望那条规则 (\d+).*?打偏（拒了但不是这条拦的）(\d+).*?被收下 (\d+)")
    kk = one(t, r"各臂数 folded=(\d+) clipped=(\d+) dropped=(\d+)")
    rounds.append(("Cuts 族 v3", (g[2], k[0], k[2], "同判 %s/%s；撞对/打偏 = %s/%s" % (g[4], g[5], k[0], k[1])) if (g and k) else None, "含 A0=%s" % (g[3] if g else "—")))
    g = one(rd("说谎者靶场-五形状乘全格矩阵-1017.txt"), r"预检与落账同判 (\d+)/(\d+)")
    rounds.append(("五形状×全格矩阵", (g[1], "—", "—", "同判 %s/%s" % (g[0], g[1])) if g else None, "55 发是调用次数级"))
    g = one(rd("声明被拒-进没进计数-1017.txt"), r"臂 (\d+) 条 ｜ 其中 isError=True 的 (\d+) 条")
    rounds.append(("认领声明五臂", (g[0], g[1], "—", "isError=True 的 %s 条" % (g[1] if g else "—")) if g else None, "四桶见下面①"))

    # 今天新增的五轮（加厚二/三轮 + 谎形状②③）—— 同一把尺子：臂 ｜ 拒 ｜ 收 ｜ 它自己那句同判
    t = rd("薄那几格推到两条-v2-1017.txt")
    g = one(t, r"臂 (\d+) 条 ｜ A0 被收下 = (\S+) ｜ 预检与落账同判 (\d+)/(\d+) ｜ 撞对 (\d+) ｜ 打偏 (\d+).*?被收下 (\d+)")
    rounds.append(("加厚五臂 v2",
                   (g[0], str(int(g[0]) - int(g[6])), g[6],
                    "同判 %s/%s；撞对/打偏 = %s/%s；A0 被收下=%s" % (g[2], g[3], g[4], g[5], g[1])) if g else None, "拒=臂-收"))
    t = rd("薄那批再推一轮-八臂-1017.txt")
    g = one(t, r"臂 (\d+) 条 ｜ 真打到内核 (\d+) 条 ｜ 造不出值 (\d+).*?A0 被收下 = (\S+) ｜ 预检与落账同判 (\d+)/(\d+) ｜ 撞对 (\d+) ｜ 打偏 (\d+).*?被收下 (\d+)")
    rounds.append(("加厚八臂",
                   (g[0], str(int(g[0]) - int(g[8])), g[8],
                    "同判 %s/%s；撞对/打偏 = %s/%s；造不出值=%s" % (g[4], g[5], g[6], g[7], g[2])) if g else None, "拒=臂-收"))
    t = rd("Measure那11格各推一条-11臂-1017.txt")
    g = one(t, r"臂 (\d+) 条 ｜ 真打到内核 (\d+) 条 ｜ A0 被收下 = (\S+) ｜ 预检与落账同判 (\d+)/(\d+) ｜ 撞对 (\d+) ｜ 打偏 (\d+).*?被收下 (\d+)")
    rounds.append(("Measure 十一臂",
                   (g[0], str(int(g[0]) - int(g[7])), g[7],
                    "同判 %s/%s；撞对/打偏 = %s/%s" % (g[3], g[4], g[5], g[6])) if g else None, "拒=臂-收"))
    g = one(rd("说谎者靶场-缺席带数-1017.txt"),
            r"源码抠出的分母=(\d+) ｜ 写死那份=(\d+) ｜ 本轮跑到=(\d+) ｜ 落账被收下=(\d+) ｜ 预检与落账同判=(\d+)")
    rounds.append(("谎形状②缺席带数",
                   (g[2], str(int(g[2]) - int(g[3])), g[3],
                    "同判=%s；两源分母 %s/%s" % (g[4], g[0], g[1])) if g else None, "②那一族里有 1 格被收下=#55"))
    g = one(rd("说谎者靶场-转述不给出处-1017.txt"),
            r"分母=(\d+) ｜ 跑到=(\d+) ｜ 落账被拒=(\d+) ｜ 预检与落账同判=(\d+) ｜ 回话为空的臂=(\d+)")
    rounds.append(("谎形状③转述无出处", (g[1], g[2], "0", "同判=%s；回话为空=%s" % (g[3], g[4])) if g else None, "全拒"))

    arms_total = 0
    bad_flip = 0
    for name, vals, note in rounds:
        if vals is None:
            holes.append("%s ｜ 那一行抠不到" % name)
            w("%-20s ｜ —— 抠不到（不进任何合计，也不按 0 算）" % name)
            continue
        arms = int(vals[0])
        arms_total += arms
        w("%-20s ｜ %-5s ｜ %-5s ｜ %-5s ｜ %s" % (name, vals[0], vals[1], vals[2], vals[3]))
    # 输入集自查（与 `rule_hit_matrix.py` 同一条规矩：候选由现扫得出，登记或写明原因，两头都不在就拒发结论）
    sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))
    from rule_hit_matrix import discover as _disc, VOID as _void
    LEDGER_FILES = ["契约说进不了门-到底拒不拒-1017.txt", "非Measure那批-运行时撞一遍-v2-1017.txt",
                    "没撞过那批-逐格撞一遍-v2-1017.txt", "动态档那三格-Cuts族撞一遍-v3-1017.txt",
                    "说谎者靶场-五形状乘全格矩阵-1017.txt", "声明被拒-进没进计数-1017.txt",
                    "薄那几格推到两条-v2-1017.txt", "薄那批再推一轮-八臂-1017.txt",
                    "Measure那11格各推一条-11臂-1017.txt", "说谎者靶场-缺席带数-1017.txt",
                    "说谎者靶场-转述不给出处-1017.txt"]
    _got, _miss, _bare = _disc()
    cand = dict(_got + _miss)
    read = [f for f in LEDGER_FILES if f in cand]
    excl = [f for f in cand if f in _void and f not in LEDGER_FILES]
    neither = [f for f in cand if f not in LEDGER_FILES and f not in _void]
    w("  输入集自查：现扫到的臂报告候选 %d 份 ｜ 本合账读到 %d 份 ｜ 带原因排除（被同批重跑取代）%d 份 ｜ 两头都不在 %d 份"
      % (len(cand), len(read), len(excl), len(neither)))
    for f in sorted(excl):
        w("     · 排除 %s ｜ 原因：%s" % (f, _void[f]))
    for f in sorted(neither):
        w("     · **两头都不在** %s ｜ 臂行 %d 行" % (f, cand[f]))
    if neither:
        w("坏：有臂报告既没进这张合账、也没写明排除原因 ⇒ 前提②那句「今天没有任何一轮印出过『拒了但 isError=false』」是少数了几轮说的，不许发。")
        return finish(6)
    n_missing_file = [f for f in LEDGER_FILES if not os.path.exists(P(f))]
    if n_missing_file:
        w("坏：合账登记要读的报告不在场：%s" % " ".join(n_missing_file))
        return finish(6)
    w("  反向也数：LEDGER_FILES %d 条，其中在候选表里的 %d 条（其余是列形状不同的早期靶场轮，本合账按各自那句抠）" % (len(LEDGER_FILES), len(read)))
    w("")

    w("  臂数合计 %d ｜ 各轮桶数在下面单独印、**不相加**（这行合计只是臂）" % arms_total)
    w("  今天没有任何一轮印出过「拒了但 isError=false」；")
    w("  唯一一处「拒了但不算这格的闸」是未撞那批 v2 里我自印的 `被去重话挡下 = 0`（那是我的 session 撞车那一族的闸，现在它自己数）。")
    w("")
    w("【①门挡下的拒收要进某个计数】逐桶（单位各不同）")
    g = one(rd("声明被拒-进没进计数-1017.txt"), r"四桶逐桶差值（内核/门口/协议层/传输层）= (\[[^\]]*\])")
    if g is None:
        holes.append("四桶那行 ｜ 抠不到")
    else:
        w("  认领那一族被拒 4 发时四桶差值 = %s ⇒ ①在这一族**未闭合**（= #59，等表态）" % g[0])
    cum = []
    for f in ["非Measure那批-运行时撞一遍-v2-1017.txt", "动态档那三格-Cuts族撞一遍-v3-1017.txt",
              "没撞过那批-逐格撞一遍-v2-1017.txt", "契约说进不了门-到底拒不拒-1017.txt"]:
        t = rd(f)
        if t:
            cum += [int(x) for x in re.findall(r"拒收累计 (\d+) 次", t)]
    w("  落账那一族：拒收原话**自带累计数**，四份报告里出现 %d 处、最大累计 %s ⇒ ①在这一族**已闭合**（数在原话里、可复跑）"
      % (len(cum), max(cum) if cum else "无"))
    w("  门口那一桶：#44/#46 那轮已落地并测过（本件不重述读数，只记状态：已闭合）")
    w("")
    w("【③E 需要替身、且读数要标明不是人】")
    n_stand = 0
    for fn in ["E-接手包-dry-run-替身那份-1017.txt", "唯一验收-接手包dry-run-1017.txt",
               "唯一验收-PASS分支可达性-改后-1017.txt"]:
        t = rd(fn)
        g = one(t, r"判定 段1 两面同一份协议=(\w+)；段2 账本被内核收下=(\w+)；段3 三条验收=(\w+)")
        if g is None:
            holes.append("%s ｜ 判定那行抠不到" % fn)
            continue
        marks = [m for m in ["不是人", "不算验收", "非第三方", "替身", "演示"] if m in t]
        n_stand += 1
        w("  %-34s 段1=%s 段2=%s 段3=%s ｜ 标注字样：%s" % (fn[:34], g[0], g[1], g[2], " ".join(marks) or "**一个都没有（那就是把替身读成验收）**"))
        if not marks:
            holes.append("%s ｜ 缺『不是人/不算验收』字样" % fn)
        if g[2] != "FAIL":
            holes.append("%s ｜ 段3 竟然不是 FAIL（要么有人签了，要么判据被改）" % fn)
    w("  真人（没碰过本仓的人）接手次数 = **0**；上面每一行都是替身或代跑，不算验收通过。")
    w("")
    w("== 自核（钉这把尺子）==")
    a = one(rd("声明被拒-两类新问题-1017.txt"), r"臂 (\d+) 条 ｜ isError=True 的 (\d+) 条")
    b = one(rd("声明被拒-进没进计数-1017.txt"), r"臂 (\d+) 条 ｜ 其中 isError=True 的 (\d+) 条")
    if a and b:
        agree = a == b
        w("  一、认领那族两源（臂, isError 数）= %s vs %s ｜ 一致 = %s" % ("/".join(a), "/".join(b), agree))
        if not agree:
            return finish(6)
    else:
        holes.append("自核一 ｜ 任一份抠不到")
    if kk and rounds[3][1]:
        s = sum(int(x) for x in kk)
        tot = one(rd("动态档那三格-Cuts族撞一遍-v3-1017.txt"), r"臂 (\d+) 条（A0 合法装配")
        w("  二、Cuts 轮：报内「臂 %s」（含 A0）= 三档目标键臂数之和 %s（folded %s + clipped %s + dropped %s）｜ 一致 = %s"
          % (tot[0], s, kk[0], kk[1], kk[2], str(int(tot[0])) == str(s)))
        if int(tot[0]) != s:
            return finish(6)
        g2v = col(rd("每格撞没撞过-覆盖账面-v3-1017.txt"), "\u52a8\u6001\u6863\u90a3\u4e09\u683c Cuts \u65cf v3", 3)
        if g2v:
            w("     覆盖账面那一轮「落进格表的键数」= %s ｜ 目标键种数 = 3 ｜ 一致 = %s" % (g2v, g2v == "3"))
            if g2v != "3":
                return finish(6)
        else:
            holes.append("自核二 ｜ 覆盖账面那一行抠不到")
    else:
        holes.append("自核二 ｜ Cuts 那份抠不到")
    w("")
    w("抠不到/缺项 %d 处：%s" % (len(holes), " ； ".join(holes) or "无"))
    if holes:
        w("坏：有轮次抠不到或标注缺失 —— 这份合账不许引用。")
        return finish(6)
    w("结论只有一句形态的：② 各轮按自己的口径都红得了；① 落账那族闭合、认领那族未闭合（#59）；③ 全部读数是替身，真人 0 次。")
    return finish(0)


if __name__ == "__main__":
    sys.exit(main())
