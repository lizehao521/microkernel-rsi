# -*- coding: utf-8 -*-
"""第二层账面：每格被**哪几条规则**碰过（不是"有没有臂撞过"，是"撞它的是几条不同的判据"）。

为什么还要这一层：`cell_arm_coverage.py` 数到 45/48 键有条臂撞过，但"撞过"太粗 ——
一格只被**一条**规则碰过，与一格被五条各角度的规则碰过，在那张表上长一样。那不行：
前者那条规则一改口径，这格当场就没闸（#55 正是"一格上三条规则从来不响"抓出来的）。

**不发明"形状"这套分类**（那是我事后贴的标签）。矩阵两根轴都来自代码：
  轴一 键名 —— 各轮报告臂行里本来就有那一列；
  轴二 规则行 —— 把全仓每条 `fmt.Errorf("kernel: …")` 现取成「文件:行 → 模板」，
        再用**签名片段**（模板里最长的那段字面量，即两个格式动词之间那块源码里写死的中文）去撞报告里的原话。
        为什么用片段而不是整条模板：报告里的原话尾巴是按列宽**截过**的，整条模板永远匹配不上，
        而签名片段是那句里最先出现、也最少被截掉的一段 —— 拿整条模板匹配会安静地得到"哪条都没命中"。

自核三条（钉这把尺子，任一不对 exit 6、不出矩阵）：
 一、模板枚举数 ≥100（同日静态全表那份数的是 134，少太多就是枚举漂了）；
 二、`非 Measure 逐值 v2` 那份报告每臂当场写明了「期望那条：file:line」，
     我从原话反推的规则行必须与它写明的那一条同源（同文件、行号差 ≤2，差是判据行 vs 模板行那种）；
 三、签名片段的歧义率：一条原话命中 ≥2 个不同规则行的臂数要单列，超过总数一半就判这把尺子分不开。

跑法：python tasks/probe/rule_hit_matrix.py [输出.txt]
"""
import io
import os
import re
import sys
import time

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "tasks", "probe"))
from contract_value_arms_v2 import rule_template

SEP = "\uff5c"
VERB = re.compile(r"%[-+ #0]*\d*(?:\.\d+)?[dsqvf]")
REPORTS = [("非Measure逐值v2", "非Measure那批-运行时撞一遍-v2-1017.txt", 1),
           ("Cuts族v3", "动态档那三格-Cuts族撞一遍-v3-1017.txt", 0),
           ("未撞那批v2", "没撞过那批-逐格撞一遍-v2-1017.txt", 0),
           ("加厚五臂v2", "薄那几格推到两条-v2-1017.txt", 0),
           ("加厚八臂", "薄那批再推一轮-八臂-1017.txt", 0),
           ("谎形状②缺席带数", "说谎者靶场-缺席带数-1017.txt", 0),
           ("谎形状③转述无出处", "说谎者靶场-转述不给出处-1017.txt", 0),
           ("Measure十一臂", "Measure那11格各推一条-11臂-1017.txt", 0)]

# 有臂行但**故意不进矩阵**的报告，必须逐份写原因 —— 没有原因的排除就是少数了几格。
# 这四份都是被同批重跑取代的原件（原件留着，只是那批数不再引用）。
VOID = {"没撞过那批-逐格撞一遍-去重挤车那轮-1017.txt": "session 被我截成键名前 6 字 ⇒ 三臂共用一本会话，拒收回话是去重那句，整批当场作废",
        "非Measure那批-运行时撞一遍-1017.txt": "v1 的 N3 只钉 MalformedStreak、底样 ToolCalls 本来 0 ⇒ 记错功，同一批由 v2 重跑",
        "薄那几格推到两条-五臂-1017.txt": "v1 的 B1 打偏（响的是 :373 不是 :234），同一批五臂由 v2 重跑",
        "动态档那三格-Cuts族撞一遍-v2-1017.txt": "夹具发号只进输入侧 + dropkind 没重算 LeftoverPct ⇒ 5 臂打偏，同一批 18 臂由 v3 重跑"}

CITED_FILE = "非Measure那批-运行时撞一遍-v2-1017.txt"


def signature(msg):
    """签名片段 = 去掉 `kernel: ` 后**最早**那段 ≥6 字的源码字面量（两个格式动词之间）。
       第一跳我取的是"最长那段"，撞上两处：① 最长那段常在句尾，而报告里的原话尾巴是按列宽截过的，       句尾那段根本不在文本里（input.go:396 就是这么漏的）；② 长句里最长的往往是结尾的评价语，       比开头的宾语更不具区分度。所以改取最早那段。"""
    body = msg[len("kernel:"):].strip() if msg.startswith("kernel:") else msg
    parts = [x.strip() for x in VERB.split(body)]
    parts = [x for x in parts if len(x) >= 4]
    if not parts:
        return None
    return parts[0]


def all_lines(f):
    return io.open(os.path.join(ROOT, f), encoding="utf-8").read().splitlines()


def build_templates():
    rows, nosig, wrap = [], [], []
    files = [f for f in sorted(os.listdir(ROOT)) if f.endswith(".go") and not f.endswith("_test.go")]
    files += ["mcpsrv/" + f for f in sorted(os.listdir(os.path.join(ROOT, "mcpsrv")))
              if f.endswith(".go") and not f.endswith("_test.go")]
    for f in files:
        ls = all_lines(f)
        for i, l in enumerate(ls, 1):
            if "fmt.Errorf(" not in l:
                continue
            msg = rule_template(f, i)
            if not msg or not msg.startswith("kernel:"):
                continue
            if msg.rstrip().endswith("%v"):   # 包装层 = 把里面那条错原样拼在句尾（`…：%v`）；句中出现 %v 不算
                wrap.append((f, i, msg))
                continue   # 包装层不参与"是哪条规则"的判定
            sig = signature(msg)
            if sig is None:
                nosig.append((f, i, msg))
            else:
                rows.append((f, i, msg, sig))
    return rows, nosig, wrap


DROPPED = []


def _drop_contained(cand, tpls):
    """签名互相包含时只认最长那条 —— 短的那条是被共有字面撞上的（本轮实测：
       output.go:310 的签名「执行不了的提议」整段住在 output.go:315 的签名里），
       计成第二条闸就是我这把尺子的错，不是那格真有两道闸。丢掉的每一次都记账、当场自印。"""
    sig_of = {(f, l): s for f, l, m, s in tpls}
    keep = []
    for c in cand:
        s = sig_of.get(c, "")
        if s and any(o != c and s in sig_of.get(o, "") and s != sig_of.get(o, "") for o in cand):
            DROPPED.append((c[0], c[1], s))
            continue
        keep.append(c)
    return keep


def cited_lines(path):
    out = []
    for ln in io.open(path, encoding="utf-8").read().splitlines():
        m = re.search(r"期望那条：([\w./]+):(\d+)", ln)
        if m:
            out.append((m.group(1), int(m.group(2))))
    return out


KEYNAME = re.compile(r"[a-z][a-z0-9_]{2,}")


ARM_MARKS = ("同判", "**分歧**", "撞对", "打偏", "被收下", "拒-", "落账")


def arm_lines(text):
    """数一行里有键名的臂行，分两种交回来：
       (带拒收原话的行, 只有键没有原话的行)。
       只有前者能喂轴二（规则行是从原话里撞出来的）；后者是「格 × 形状」那种表 ——
       它必须**被看见**（印出来），但不许悄悄当成输入集缺件，也不许当成已覆盖。"""
    with_text = key_only = 0
    for ln in text.splitlines():
        if SEP not in ln:
            continue
        parts = [x.strip() for x in ln.split(SEP)]
        if len(parts) < 5:
            continue
        if not any(KEYNAME.fullmatch(p) for p in parts[:2]):
            continue
        if "kernel:" in ln or any(m in ln for m in ARM_MARKS):
            with_text += 1
        else:
            key_only += 1
    return with_text, key_only


def discover():
    """扫 报告/ 下所有臂行报告，按「已登记 / 未登记 / 只有键没原话」分开 —— 未登记的必须逐个点名。"""
    d = os.path.join(ROOT, "报告")
    known = {fn for _n, fn, _c in REPORTS}
    got, miss, shape_only = [], [], []
    for fn in sorted(os.listdir(d)):
        if not fn.endswith(".txt"):
            continue
        try:
            n, bare = arm_lines(io.open(os.path.join(d, fn), encoding="utf-8").read())
        except (UnicodeDecodeError, OSError):
            continue
        if n == 0 and bare >= 3:
            shape_only.append((fn, bare))
        if n == 0:
            continue
        (got if fn in known else miss).append((fn, n))
    return got, miss, shape_only


def arm_rows(text, keycol):
    rows = []
    for ln in text.splitlines():
        if SEP not in ln:
            continue
        parts = [x.strip() for x in ln.split(SEP)]
        if len(parts) < 3:
            continue
        key = parts[keycol]
        if not re.fullmatch(r"[a-z][a-z0-9_]{2,}", key):
            continue
        if "｜" in key:
            continue
        body = " ".join(parts[keycol + 1:])
        if "造不出值" in body or "打不进去" in body:
            continue          # 第三种状态：这一臂没打到内核，不进矩阵
        rows.append((key, body))
    return rows


def main():
    outp = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "报告", "每格被哪几条规则碰过-1017.txt")
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

    w("== 每格被哪几条规则碰过 · %s ｜ 轴一 = 报告里的键名列，轴二 = 源码里的 fmt.Errorf 行 ==" % time.strftime("%Y-%m-%d %H:%M:%S"))
    tpls, nosig, wrap = build_templates()
    uniq = sorted({(f, ln) for f, ln, _m, _s in tpls})
    w("自核一：扫到带签名的模板 %d 条（行号去重 %d）｜ 没签名的 %d 条 ｜ 排除的包装层模板 %d 条"
      "（句里带 %%v 的那几条说的是「哪一侧的账不自洽」，不是哪条判据）"
      % (len(tpls), len(uniq), len(nosig), len(wrap)))
    if len(uniq) < 100:
        w("坏：模板枚举不到 100 条 —— 轴二不可信，这份矩阵不出。")
        return finish(6)
    w("  排除那 %d 条逐条列出（判「是不是包装层」用的是「句尾 %%v」这一条机械规则，它会误伤真判据 —— "
      "本轮 input.go:610「不是十六进制」就被撞中了，却因句尾带 %%v 不计入闸数；要不要改口径，是作者的权）：" % len(wrap))
    for f, l, m in sorted(set(wrap)):
        w("     · %s:%d ｜ %s" % (f, l, m[:52]))

    got, miss, bare = discover()
    unreg = [(fn, n) for fn, n in miss if fn not in VOID]
    voided = [(fn, n) for fn, n in miss if fn in VOID]
    w("自核四（输入集自查）：报告/ 里「有键名列也有拒收原话」的报告 %d 份 ｜ 进矩阵 %d 份 ｜ 点名排除 %d 份 ｜ 两头都不在 %d 份"
      % (len(got) + len(miss), len(got), len(voided), len(unreg)))
    w("      另有 %d 份只有键名列、行里没有拒收原话（那种「格 × 形状」的表喂不了轴二 —— 列出来让人看见，既不当缺件也不当覆盖）：%s"
      % (len(bare), " ".join("%s(%d行)" % (fn, n) for fn, n in bare) or "无"))
    for fn, n in got:
        w("     · 进矩阵 %s ｜ 臂行 %d 行" % (fn, n))
    for fn, n in voided:
        w("     · 点名排除 %s ｜ 臂行 %d 行 ｜ 原因：%s" % (fn, n, VOID[fn]))
    for fn, n in unreg:
        w("     · **两头都不在** %s ｜ 臂行 %d 行" % (fn, n))
    if unreg:
        w("坏：有臂行的报告既没进矩阵、也没写排除原因 —— 输入集不全，这份矩阵不许引用（闸数会少数）。")
        return finish(6)

    pairs = set()
    amb_rows = 0
    all_rows = 0
    per_round = {}
    absent_reg = []
    for name, fn, keycol in REPORTS:
        p = os.path.join(ROOT, "报告", fn)
        if not os.path.exists(p):
            w("  %s ｜ **登记了但文件不在**：%s" % (name, fn))
            absent_reg.append(fn)
            continue
        rows = arm_rows(io.open(p, encoding="utf-8").read(), keycol)
        got = set()
        amb = 0
        miss = 0
        for key, body in rows:
            cand = sorted({(f, ln) for f, ln, _m, sig in tpls if sig in body})
            hits = _drop_contained(cand, tpls)
            all_rows += 1
            if len(hits) > 1:
                amb += 1
                amb_rows += 1
            if not hits:
                got.add((key, "未命中", 0))
                miss += 1
            for h in hits:
                got.add((key, h[0], h[1]))
        per_round[name] = (len(rows), len([1 for k, f, l in got if f != "未命中"]), amb)
        pairs |= {(k, f, l) for k, f, l in got if f != "未命中"}
        w("  %-16s ｜ 臂行 %d ｜ 对上规则行 %d 对 ｜ 一条原话命中多规则的 %d 行 ｜ 一条都没对上的 %d 行（说明行也算在内，不筛掉）"
          % (name, len(rows), per_round[name][1], amb, miss))

    if absent_reg:
        w("坏：登记进矩阵的报告文件不在场（%s）—— 输入集缺件，这份矩阵不许引用。" % " ".join(absent_reg))
        return finish(6)

    w("")
    w("== 尺子自证：签名互相包含时丢掉的命中 %d 次（不丢会把字面巧合计成第二条闸）==" % len(DROPPED))
    for f, l, s in sorted(set(DROPPED)):
        w("   · %s:%d ｜ 签名「%s」被同里更长那条盖住" % (f, l, s[:16]))
    w("")
    w("== 自核二：从原话反推的规则行 vs 那份报告当场写明的「期望那条」==")
    cited = cited_lines(os.path.join(ROOT, "报告", CITED_FILE))
    hitset = {(f, l) for _k, f, l in pairs}
    okl, badl = [], []
    for cf, cl in cited:
        if (cf, cl) in hitset or any(f == cf and abs(l - cl) <= 2 for f, l in hitset):
            okl.append("%s:%d" % (cf, cl))
        else:
            badl.append("%s:%d" % (cf, cl))
    w("  写明 %d 条 ｜ 反推同源 %d 条 ｜ 对不上：%s" % (len(cited), len(okl), " ".join(badl) or "无"))
    if badl:
        w("坏：反推与写明不一致 —— 我这把匹配尺子错，矩阵不许引用。")
        return finish(6)
    w("自核三：歧义率 %d/%d 行（命中不止一条规则行）｜ 超过一半就判这把尺子分不开" % (amb_rows, all_rows))
    if all_rows and amb_rows * 2 > all_rows:
        w("坏：过半臂行分不清是哪条规则 —— 签名片段太像，这份矩阵不出。")
        return finish(6)

    per_key = {}
    for k, f, l in pairs:
        per_key.setdefault(k, set()).add((f, l))
    one = sorted([k for k, v in per_key.items() if len(v) == 1])
    multi = sorted([k for k, v in per_key.items() if len(v) > 1])
    w("")
    w("== 矩阵自印分母 ==")
    w("格（键名）%d 个 ｜ 碰过它们的规则行 %d 条 ｜ 格×规则 %d 对 ｜ 只被 1 条碰过的格 %d ｜ 被 ≥2 条碰过的格 %d"
      % (len(per_key), len({(f, l) for _k, f, l in pairs}), len(pairs), len(one), len(multi)))
    w("只被 1 条规则碰过的格（那条一改口径，这格当场没闸 —— 第一批要回头重测的就是这些）：")
    for k in one:
        f, l = sorted(per_key[k])[0]
        msg = next((m for ff, ll, m, _s in tpls if (ff, ll) == (f, l)), "?")
        w("   %-22s ｜ %s:%d ｜ %s" % (k, f, l, msg[:54]))
    w("被 ≥2 条碰过的格：")
    for k in multi:
        w("   %-22s ｜ %d 条：%s" % (k, len(per_key[k]), " ".join("%s:%d" % (f, l) for f, l in sorted(per_key[k]))))
    w("")
    w("读法：这张表数的是**闸的条数**，不是「这格守得好不好」；只被一条碰过不代表该加闸（加不加是判据设计，作者的权）。")
    w("      轴一那个键名是「这一臂打的**目标格**」（哪一格的值被我改了），不是「这条规则守的是哪一格」——")
    w("      所以本表只说：在这些撞过的回合里，每格被几条不同的规则接住；不等于这格在全仓总共只有这几条。")
    w("      没进这张表的格 = 这几轮的报告里没有它的臂行（覆盖账面那份数过：45/48 键有条臂，剩 3 个是方法格）。")
    return finish(0)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        # 「跑完了」要产物不要退出码：崩也要把现场落进文件
        import traceback
        dest = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "报告", "每格被哪几条规则碰过-崩那跳-1017.txt")
        if os.path.exists(dest):
            dest = dest + ".crash"
        io.open(dest, "w", encoding="utf-8").write(traceback.format_exc())
        raise
