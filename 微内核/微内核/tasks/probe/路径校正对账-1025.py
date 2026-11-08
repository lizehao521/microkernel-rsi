# -*- coding: utf-8 -*-
"""只读对账：这一趟把现场写死的旧主机工具链路径换正了没有，换了几处，还剩谁带着。

本件不写被核的任何文件，只写自己那份读数（报告/校正-工具链路径-1025-开局快照分档.txt）。
旧读数 `校正-工具链路径-1025.txt` 被报告/看板按行号引用，本件不覆盖它。

三态与分母口径：
- 「改前版」按 TEMP-备份/遗留修复-1025 里那份字节算，那批字节里有一部分是**第一趟改完的中间版**
  （同一个文件被两趟各改一次，第二趟的 cp 把第一趟的备份盖掉了），所以本件印的"换掉的处数"是**下界**；
  真正的原始版盘上没有第二份，不假称拿到。
- 每一档的分母都在本件里现数并自印，不引用任何摘要里的数。
- 一把尺子如果只会数到 0，它就没牙：本件把同一把尺子先打在备份树上（那里必然有旧路径），
  数到的非零值当负对照印出来，证明计数会红。
"""
import ast
import io
import os
import re
import subprocess
import sys

ROOT = r"C:/Users/Admin/Desktop/微内核"
TREE = os.path.join(ROOT, "微内核", "微内核")
BK = os.path.join(ROOT, "TEMP-备份", "遗留修复-1025", "微内核", "微内核")
# 新输出名：旧的 `校正-工具链路径-1025.txt` 被报告/看板按行号引用，这一趟不覆盖它。
OUT = os.path.join(TREE, "报告", "校正-工具链路径-1025-开局快照分档.txt")

# 旧主机上那五笔写法（同一件事的五种拼法；只换正"指向本机不存在的东西"的那些）
STALE = (
    "C:/Users/Administrator/sdk",
    "/c/Users/Administrator/sdk",
    r"C:\Users\Administrator\sdk",
    "C:/Users/Administrator/Desktop/" + "微内核" + "/.bin",
    r"C:\Users\Administrator\Desktop" + "\\" + "微内核" + "\\.bin",
)

# 这一趟动过的文件（= 备份树里现存的那批；分母由下面 walk 现数，不在这儿写死）
# 本机该存在的落点 —— 换正之后指的东西必须真在
TARGETS = (
    r"C:/Users/Admin/sdk/go/bin/go.exe",
    r"C:/Users/Admin/sdk/py314/python.exe",
    "C:/Users/Admin/Desktop/" + "微内核" + "/" + "微内核" + "/.bin/mcphttp.exe",
    "C:/Users/Admin/Desktop/" + "微内核" + "/" + "微内核" + "/.bin/mcpkernel.exe",
)

EXTS = (".go", ".py", ".sh", ".ps1", ".bat", ".md", ".txt", ".json", ".jsonl",
        ".html", ".js", ".yaml", ".yml", ".toml", ".csv")

L = []
bad = []


def a(s):
    return str(s).encode("ascii", "backslashreplace").decode("ascii")


def say(s):
    L.append(s)
    print(a(s))


def read_text(p):
    with io.open(p, "rb") as f:
        raw = f.read()
    return raw.decode("utf-8", "replace"), raw


def count_stale(txt):
    n = 0
    for s in STALE:
        n += txt.count(s)
    return n


def rel(p):
    try:
        return os.path.relpath(p, ROOT).replace(os.sep, "/")
    except ValueError:
        return p


def classify(r):
    """剩余存量归到哪一档 —— 归了类的就不是"还没修"，是"本来不该修"。"""
    r = r.replace(os.sep, "/")
    if r.startswith("TEMP-备份/") or "/TEMP-备份/" in r or r.startswith(".hermess-snapshots/") or "/.hermess-snapshots/" in r:
        return "备份/快照树（本来就该留着旧路径当凭证）"
    if os.path.basename(r).startswith("路径校正对账"):
        return "尺子自己的口径表（那几处是它要数的字符串，不是能跑的落点）"
    if r.startswith("构建-1017/"):
        return "打包产物（改仓库 .md 后要重跑 打包.py 才跟随；手改会造出第三份不一致）"
    if r.endswith("看板.md"):
        return "看板：这几行在讲这个缺陷本身"
    if r.endswith("run_freeze_round.sh"):
        return "候选路径清单（#63 修出来的回退写法，旧路径是候选之一，不是落点）"
    if r.endswith("client.py"):
        return "散文一行（讲旧主机那次踩坑的来历）"
    base = os.path.basename(r)
    if "那版.txt" in base:
        return "作废读数原件（自己名下写着为什么作废，留着是凭证）"
    if r.startswith("微内核/微内核/报告/") and base.endswith(".txt"):
        return "历史读数原件（记的是当时那台机器，改了就是伪造凭证）"
    if "/读数/" in r:
        return "历史读数原件"
    return "未分类"


# ---------------------------------------------------------------- 1 逐文件对账
say("== 1 · 逐文件：改前版（盘上留的那份）↔ 现文件 ==")
say("   口径：stale = 上面五笔写法的出现次数之和；Admin = 字符串 Administrator 的出现次数（比 stale 宽，含散文）")
say("        后stale 非 0 的行末尾给归类；归了类的不算没修（判据盯性质，不盯「某一串字必须是 0」）")
say("")
say("%-52s %6s %6s %6s %6s %8s %8s %-11s %s" % (
    "文件", "前stale", "后stale", "前Admin", "后Admin", "前字节", "后字节", "行尾", "归类"))

def eol_state(braw):
    """行尾三态：按 \\n 数与 \\r 数是否相等判，不看绝对值 —— 加行会改绝对值，不改态。"""
    lf, cr = braw.count(b"\n"), braw.count(b"\r")
    if cr == 0:
        return "LF", lf, cr
    if cr == lf:
        return "CRLF", lf, cr
    return "MIX", lf, cr


rows = []
for dirpath, dirnames, filenames in os.walk(BK):
    dirnames[:] = [d for d in dirnames if d not in (".git", "__pycache__")]
    for fn in sorted(filenames):
        bp = os.path.join(dirpath, fn)
        sub = os.path.relpath(bp, BK).replace(os.sep, "/")
        # 备份名是 <原路径> 或 <原路径>.<标记>（标记 = 改前-… / 改前现那份-… / 改后-… / 引号修正前）；
        # 不带标记的那批只能证明「动树前它长这样」，叫「开局快照」而不是「原件」—— 备份目录里的是副本。
        # 不带这一刀，带标记的那几十份会被当成"树上没有的文件"判红 —— 那是路径写错（本件写错），不是文件被删。
        m = re.search(r"\.(改前-.*|改前现那份-.*|改后-.*|引号修正前)$", sub)
        stem = sub[:m.start()] if m else sub
        tag = sub[len(stem) + 1:] if m else "开局快照"
        cp = os.path.join(TREE, *stem.split("/"))
        rows.append((stem, tag, sub, bp, cp))
rows.sort()

# 同一个原文件可能留了好几份改前版（两趟各改一次，每趟改前都 cp 过）。
# 每一份都单独印一行（不藏），但文件级的数按「stale 最多的那一份」当改前版 —— 那才是最早的一版。
by_stem = {}
for stem, tag, sub, bp, cp in rows:
    by_stem.setdefault(stem, []).append((tag, sub, bp, cp))
head_for = {}
for stem, items in by_stem.items():
    best = None
    for tag, sub, bp, cp in items:
        try:
            t, _ = read_text(bp)
        except Exception:
            continue
        v = count_stale(t)
        if best is None or v > best[0]:
            best = (v, tag)
    head_for[stem] = best[1] if best else items[0][0]

# 两类备份分开印分母（「带标记的改前版」与「无标记的开局快照」不是一回事，不许相加成一个数）：
n_tagged = sum(1 for r in rows if r[1] != "开局快照")
n_snapshot = len(rows) - n_tagged
tagged_files = len({r[0] for r in rows if r[1] != "开局快照"})
snapshot_files = len({r[0] for r in rows if r[1] == "开局快照"})
say("   备份件分母·带标记（.改前-… / 改前现那份-… / 改后-… / 引号修正前）：%d 份 / 对应 %d 个文件" % (
    n_tagged, tagged_files))
say("   备份件分母·无标记开局快照（只能证明动树前长这样，不叫原件）：%d 份 / 对应 %d 个文件" % (
    n_snapshot, snapshot_files))
say("   两类合计 %d 份 / 对应 %d 个文件（同一文件两类都留过的 %d 个）" % (
    len(rows), len(by_stem), tagged_files + snapshot_files - len(by_stem)))

n_files = 0
sum_before_stale = 0
sum_after_stale = 0
zero_after = 0
eol_state_bad = []
mix_pre = []
multi = {}
for stem, tag, sub, bp, cp in rows:
    if not os.path.isfile(cp):
        bad.append("1: 备份里有 %s（对应树上 %s）但树上没有 —— 文件被删了还是本件的路径刀切错" % (sub, stem))
        continue
    bt, braw = read_text(bp)
    ct, craw = read_text(cp)
    bs, cs = count_stale(bt), count_stale(ct)
    ba, ca = bt.count("Administrator"), ct.count("Administrator")
    st_b, lf_b, cr_b = eol_state(braw)
    st_c, lf_c, cr_c = eol_state(craw)
    eol = "%s(%d/%d)" % (st_c, lf_c, cr_c)
    if st_b != st_c:
        eol = "变档! %s(%d/%d)→%s(%d/%d)" % (st_b, lf_b, cr_b, st_c, lf_c, cr_c)
        eol_state_bad.append(sub)
    elif st_c == "MIX":
        eol = "本就混合(%d/%d)" % (lf_c, cr_c)
        mix_pre.append(sub)
    say("%-52s %6d %6d %6d %6d %8d %8d %s %s" % (
        sub if len(by_stem[stem]) == 1 else "%s ←%s" % (stem, tag),
        bs, cs, ba, ca, len(braw), len(craw), eol,
        ("" if cs == 0 else "→ " + classify("微内核/微内核/" + stem))))
    if len(by_stem[stem]) > 1 and tag == head_for[stem]:
        multi[stem] = len(by_stem[stem])
    if tag != head_for[stem]:
        continue  # 同一文件的第二、第三份改前版只出证据行，不重复计进文件级的数
    n_files += 1
    sum_before_stale += bs
    sum_after_stale += cs
    if cs == 0:
        zero_after += 1
    elif classify("微内核/微内核/" + stem) == "未分类":
        bad.append("1: %s 现版本还有 %d 处旧写法且没归类 —— 补修或给理由" % (stem, cs))


say("")
say("   现数：对账 %d 个文件 ｜ 改前版里 stale 共 %d 处 ｜ 现文件里剩 %d 处 ｜ 归零的 %d/%d 个" % (
    n_files, sum_before_stale, sum_after_stale, zero_after, n_files))
if sum_after_stale != 0:
    say("   注：现文件里剩的 %d 处已逐行给了归类（见上表最后一列）；未分类的那些在第 1 节就地判红。" % sum_after_stale)
if eol_state_bad:
    bad.append("1: 行尾换档了：%s（纯 CRLF↔纯 LF↔混合 之间换档是静默损坏）" % "、".join(eol_state_bad))
say("   行尾：判据是**三态**（纯 CRLF / 纯 LF / 混合）比态不比行数 —— 加一行会改 \\\\n 的绝对数，不改态；"
    "拿绝对数当判据时，本节 8 个文件被误判成「静默损坏」（现数 %d 个真换档）。" % len(eol_state_bad))
say("   代价：两趟都动过的文件（两支修脚本白名单的交集 —— 那两支在系统 TEMP、未留盘），")
say("         备份里存的是**第一趟改完的中间版**，所以它们的「前stale」已经少了一批，真实原始数拿不回来。")
say("         上面「改前版里 stale 共 %d 处」比真实原始数小，所以「换掉的处数」只能当下界读。" % sum_before_stale)
say("         按行相加的下界 = %d - %d = %d 处。" % (sum_before_stale, sum_after_stale, sum_before_stale - sum_after_stale))
say("         两趟当时各自 stdout 自印过 8 与 15（合 23 处），但那两发 stdout **没 tee 到盘上**")
say("         ⇒ 「23 处」现在只是口述，不能当凭证引；盘上能证的只有上面这个下界。学费：跑之前先 tee。")
say("   盲区：本节只数得到备份树里**有备份**的那些文件（现数 %d 份）；改了却没备份的文件这一节看不见，" % len(rows))
say("         唯一旁证是第 3 节的未分类计数 —— 铁律「改前先 cp」漏一次，这张表就少一行而不自知。")
say("   另：看板.md 的改前快照单开在 `TEMP-备份/遗留修复-1025-看板/`，**不放进本节 walk 那棵树** ——")
say("         否则每往看板插一张卡本表就 +1 行，引用本读数行号的那些卡会跟着漂。")

# ------------------------------------------------------- 2 换正之后指的东西在不在
say("")
say("== 2 · 换正之后这些落点必须真在机器上 ==")
for t in TARGETS:
    p = t.replace("/", os.sep)
    if os.path.isfile(p):
        say("   OK       %-56s %d B" % (t, os.path.getsize(p)))
    else:
        say("   MISSING  %-56s" % t)
        bad.append("2: 文档里现在指的是 %s，盘上没有" % t)
missing = [t for t in TARGETS if not os.path.isfile(t.replace("/", os.sep))]
say("   现数：%d 个落点，缺 %d 个" % (len(TARGETS), len(missing)))

# ------------------------------------------------------------- 3 全站的存量普查
say("")
say("== 3 · 全站还剩谁带着旧主机写法（逐名 + 归类，归类口径与第 1 节同一份代码）==")


walked = 0
hits = []
self_family = 0
for dirpath, dirnames, filenames in os.walk(ROOT):
    dirnames[:] = [d for d in dirnames
                   if d not in (".git", "__pycache__", "node_modules", ".venv", "venv")]
    for fn in filenames:
        if not fn.endswith(EXTS):
            continue
        if fn.startswith("校正-工具链路径"):
            self_family += 1
            continue  # 本族自己的读数不进普查：进了，每跑一发下一发的行号就往上漂
        p = os.path.join(dirpath, fn)
        r = rel(p)
        if r.startswith("TEMP-备份/") or "/TEMP-备份/" in r:
            continue  # 备份树留给第 1 节，不参与"还剩谁带着"
        try:
            txt, _ = read_text(p)
        except Exception:
            continue
        walked += 1
        n = count_stale(txt)
        w = txt.count("Administrator")
        if n or w:
            hits.append((r, n, w))


def census(label, idx):
    """同一批命中按两种口径各出一张表：idx=1 数精确写法（能跑的落点），idx=2 数任何 Administrator（含散文/traceback）。"""
    b = {}
    u = []
    for r, n, w in hits:
        v = (n, w)[idx - 1]
        if v == 0:
            continue
        k = classify(r)
        b.setdefault(k, [0, 0, []])
        b[k][0] += 1
        b[k][1] += v
        if k == "未分类":
            u.append((r, v))
    say("   --- %s ---" % label)
    for k in sorted(b, key=lambda x: -b[x][1]):
        f, tot, _ = b[k]
        names = sorted(["%s(%d)" % (r, (n, w)[idx - 1]) for r, n, w in hits
                        if (n, w)[idx - 1] and classify(r) == k])
        say("   [%s] %d 个文件 / %d 处" % (k, f, tot))
        for nm in names:
            say("        · %s" % nm)
    say("   现数：带这类命中的 %d 个文件 / %d 处 ｜ 未分类 %d 个" % (
        sum(v[0] for v in b.values()), sum(v[1] for v in b.values()), len(u)))
    return u


u1 = census("甲 · 只数那五笔精确写法（= 真会指到不存在的东西）", 1)
u2 = census("乙 · 数任何 Administrator（比甲宽，含散文与 traceback 原文）", 2)
for r, n in u2:
    say("   NEEDS-ACTION  %-70s %d 处" % (r, n))
say("   现数：扫过 %d 个文本件（按扩展名认，非文本不计）" % walked)
say("   现数：本族自己的读数 %d 份**不计入**上面两张表（含本件正写的这一份）—— 计进去的话每跑一发下一发行的号就漂" % self_family)
if u1 or u2:
    bad.append("3: 未分类的旧写法还剩 %d 个文件（%s）—— 要么补修，要么给它一个归类理由" % (
        len(u2), "、".join(r for r, _ in u2)))

# ----------------------------------------------------- 4 语法：改过的件还能不能读
say("")
say("== 4 · 语法（改过的脚本必须还能被自己的解释器读进去）==")
py_n = sh_n = 0
for stem in sorted(by_stem):
    cp = os.path.join(TREE, *stem.split("/"))
    sub = stem
    if not os.path.isfile(cp):
        continue
    if sub.endswith(".py"):
        py_n += 1
        try:
            ast.parse(io.open(cp, encoding="utf-8").read(), filename=sub)
            say("   ast OK   %s" % sub)
        except Exception as e:
            say("   ast FAIL %s：%s" % (sub, e))
            bad.append("4: %s 解不进 AST" % sub)
    elif sub.endswith(".sh"):
        sh_n += 1
        try:
            r = subprocess.run(["bash", "-n", cp], capture_output=True, timeout=30)
            say("   bash -n rc=%d %s %s" % (
                r.returncode, sub, (r.stderr.decode("utf-8", "replace").strip()[:70] or "")))
            if r.returncode != 0:
                bad.append("4: %s bash -n rc=%d" % (sub, r.returncode))
        except Exception as e:
            say("   bash -n ERR %s：%s" % (sub, e))
            bad.append("4: %s 没能跑 bash -n（%s）—— 没跑过就不算过" % (sub, e))
say("   现数：.py %d 支 ｜ .sh %d 支" % (py_n, sh_n))

# --------------------------------------------------------- 5 负对照：尺子会不会红
say("")
say("== 5 · 负对照（同一把尺子打在明知有旧写法的地方，必须数到非零）==")
ctl = 0
for stem, tag, sub, bp, cp in rows:
    bt, _ = read_text(bp)
    ctl += count_stale(bt)
probe = "C:/Users/Administrator/sdk/go/bin/go.exe 与 /c/Users/Administrator/sdk 与 " + \
    r"C:\Users\Administrator\sdk"
say("   打在备份树（%d 份备份件：带标记 %d ＋ 开局快照 %d，对应 %d 个文件）：%d 处" % (
    len(rows), n_tagged, n_snapshot, len(by_stem), ctl))
say("   打在一句人造串（含三笔写法）：%d 处" % count_stale(probe))
if ctl == 0:
    bad.append("5: 备份树里数到 0 —— 要么备份是空的，要么这把尺子恒 0 没牙")
if count_stale(probe) != 3:
    bad.append("5: 人造串该数到 3 处，实际 %d —— 计数逻辑对某笔写法不生效" % count_stale(probe))
say("   ⇒ 「现文件里剩 0 处」这句话的分母，是上面这个会响的计数器给的。")
arm_a = classify("微内核/微内核/某个没归类的新文件.md")
arm_b = classify("微内核/微内核/报告/看板.md")
say("   归类器两根臂：喂一个没归类的名字 ⇒ 「%s」%s ｜ 喂 看板.md ⇒ 「%s」%s" % (
    arm_a, "落进未分类（会拦）" if arm_a == "未分类" else "**没落进未分类 ⇒ 归类器恒放行，没牙**",
    arm_b, "没落进未分类（会放）" if arm_b != "未分类" else "**落进未分类 ⇒ 归错档**"))
if arm_a != "未分类":
    bad.append("5: 归类器对没归类的名字也不报未分类 ⇒ 第 1/3 节的「未分类=0」是恒绿")
if arm_b == "未分类":
    bad.append("5: 看板.md 被归成未分类 ⇒ 归类规则漂了，第 3 节那 4 处会被误判成没修")
# 行尾那把新判据自己的两根臂：恒绿的三态器与恒红的三态器都算没牙。
lf_bytes = b"a\nb\nc\n"
crlf_bytes = lf_bytes.replace(b"\n", b"\r\n")
mix_bytes = b"a\nb\r\nc\n"
same_state = eol_state(lf_bytes)[0] == eol_state(lf_bytes + b"d\n")[0]
switch_state = eol_state(lf_bytes)[0] != eol_state(crlf_bytes)[0]
mix_seen = eol_state(mix_bytes)[0] == "MIX"
say("   行尾三态器三根臂：LF 加一行 ⇒ 态相同=%s（该 True；False 就是「加行算损坏」那种误判）"
    "｜ LF→CRLF ⇒ 判成换档=%s（该 True；False 就是恒绿）｜ 掺一个裸 \\r ⇒ 认成混合=%s（该 True）" % (
        same_state, switch_state, mix_seen))
if not same_state:
    bad.append("5: 行尾器把「加一行」当成损坏 ⇒ 第 1 节每个改过的文件都会假红（本趟就错过一次，8 个文件）")
if not switch_state or not mix_seen:
    bad.append("5: 行尾器认不出换档/混合 ⇒ 真静默损坏时它是恒绿的")

# ------------------------------------------------------------------ 6 未跑声明
say("")
say("== 6 · 这一趟没跑的东西（不当成已验）==")
say("   - kaudit_smoke.sh / mcphttp_up.sh 只做了 bash -n，**没执行**：一支会 taskkill，")
say("     一支默认落点 127.0.0.1:8797（那台机器上是别人的实例在听）⇒ 能说的是「路径现在指得到」，")
say("     不是「今天跑起来会红/会绿」。看板 #70 乙（收进程不问来源）本趟**没动**。")
say("   - 构建-1017/打包.py 没跑 ⇒ 便携树与 zip 里那三份 docs/README.md 仍是旧写法（第 3 节已归类）。")
say("   - #63 那套 *_OVERRIDE 回退写法没搬到这两支脚本（#70 甲的选项①），所以 #70 甲只关到「路径是字节事实」这一半。")

# --------------------------------------------------------------------- 收尾
say("")
say("== 结论 ==")
expect_rows = len(L)
say("跑了 %d 行读数；判红 %d 条%s" % (
    expect_rows, len(bad), "" if not bad else "："))
for b in bad:
    say("   RED · %s" % b)
say("一句话：%s" % ("旧主机路径这一族的**换正与对账**过了（换正的下界见第 1 节，剩余存量全部有归类）"
                    if not bad else "没过，见上面 RED"))

txt = "\n".join(L) + "\n"
part = OUT + ".part"
with io.open(part, "w", encoding="utf-8", newline="\n") as f:
    f.write(txt)
os.replace(part, OUT)

# 落盘自证：分母从落盘那份重数（不是从内存里的 list 数）
back = io.open(OUT, encoding="utf-8").read().split("\n")
n_back = len([x for x in back if x.strip()])
n_mem = len([x for x in L if x.strip()])
tail = "landing check: %d/%d rows（落盘非空行数/内存非空行数，%s）" % (
    n_back, n_mem, "一致" if n_back == n_mem else "不一致 —— 打印被截了")
with io.open(OUT, "a", encoding="utf-8", newline="\n") as f:
    f.write(tail + "\n" + "写了 %s ｜ %d B\n" % (rel(OUT), os.path.getsize(OUT)))
print(a(tail))
print(a("wrote %s" % rel(OUT)))
sys.exit(0 if not bad else 1)
