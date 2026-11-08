# -*- coding: utf-8 -*-
"""融合链逐段状态机检：把 A0→E 每一段**引用的凭据文件**与**四条约束**核成读数，不靠谁的总结句。

为什么要有它（也为什么这一支不算重复）：
· `报告/融合链-逐段对账-1017.txt` 里每个数都指明了出处，但"出处那份文件今天还在不在、里面那行自印还在不在"没人核过；
  文件名被改过（坏那轮留档就靠改名），引用最容易在这里断。这一支就是**逐条打开看**。
· `tasks/probe/premises_ledger.py` 合的是三条前提；这一支合的是**阶段与约束**（六段 + 四条约束）。两者不重叠。
· 完成审计要有能红的形式：这里任何一条引用断链、任何一条约束核不上，工具就退出非 0，
  而不是由我说一句"应该都齐了"。

它核什么：
 六段（阶段 0／1 A0／2 B／3 C／4 D／5 E）+ 【约束逐条核对】那一节里引用的每份文件；
 每条约束另做机械检查：判据表 48 键/49 格（现取 `kernel.celltable`）、§8 十五条（现数 README）、
 看板「在跑」上限 1（**调用** `board_check.py`，不重抄它的正则）、回滚不用 git（`.git` 必须不存在）、
 三桶不相加（前提合账里那一句必须在）、最新一份回滚基线自校验（跑 `sha256sum -c`）。

自印行的形状在自己的归档里有两种：新轮用全角竖线分隔，早轮（A0、真流量、agent 真跑、#51 那些）用 `名=数`。只认一种就会把早轮全部误报成「没证据」——今天这支就在这里改了两轮。
自核（钉这把尺子）：表里塞一条**故意不存在的引用**，它必须被判成断链；判不出来就说明"全部可核"是假的 ⇒ exit 6。

跑法：python tasks/probe/chain_status.py <端点URL> [输出.txt] [--force-overwrite]
      端点**没有默认值**（裸跑 exit 9）；输出名不给就按当下日期时间自己生成；目标已存在不给 `--force-overwrite` 就 exit 3。
"""
import io
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DOC = os.path.join(ROOT, "报告", "融合链-逐段对账-1017.txt")
BAD_MARKS = ["那跳", "坏那轮", "没写全", "串了列", "正则没对上", "没抠到", "作废"]
SENTINEL = "报告/__故意不存在-正对照__.txt"


def run(cmd, cwd=None):
    """捕获子进程读数。两处都写死、不吃系统 locale：
    ① 给子进程 PYTHONIOENCODING=utf-8（否则它在 GBK 控制台把中文编成 GBK 字节）；
    ② 本层 encoding 显式 utf-8。10-19 12:4x 只做了 ②、没做 ①，于是看板那一行在归档里全变 `?`，
       只剩 ASCII 数字能读 —— 所以「脏字符数」在调用处要自己印出来，别让它静悄悄落盘（见下面编码两根对照）。"""
    env = dict(os.environ, PYTHONIOENCODING="utf-8")
    return subprocess.run(cmd, cwd=cwd or ROOT, capture_output=True, text=True,
                          encoding="utf-8", errors="replace", env=env)


def stage_sections(text):
    """按【…】切段；`== …` 那种当天追加的小节**也算一段**（10-17 07:4x 补的）。
       旧写法把 `== ` 只当"结束一段"的分隔符 —— 于是全部追加小节的正文掉在段外，
       里面那些引用既不进六段也不进"追加段"，我上一条刚加的逐条核压根扫不到它们（自印的 12 条其实只是老【】段剩下的）。"""
    parts = []
    cur_name, buf = None, []
    for ln in text.splitlines():
        m = re.match(r"^\s*【([^】]+)】", ln)
        m2 = re.match(r"^== (.+?)\s*$", ln)
        if m:
            if cur_name:
                parts.append((cur_name, "\n".join(buf)))
            cur_name, buf = m.group(1), [ln]
        elif m2:
            if cur_name:
                parts.append((cur_name, "\n".join(buf)))
            cur_name, buf = ("追加 · " + m2.group(1)[:26]), [ln]
        elif cur_name:
            buf.append(ln)
    if cur_name:
        parts.append((cur_name, "\n".join(buf)))
    return parts


SHAPE_SKIPPED = []


def refs_in(body):
    got = re.findall(r"\x60((?:报告/)?[^\x60\n|…<>]+\.(?:txt|md|jsonl|py|go|sh))\x60", body)
    out = []
    for r in got:
        if "…" in r or "*" in r or "{" in r or "}" in r:
            SHAPE_SKIPPED.append(("花括号简写", r))
            continue          # 花括号那种是我写的简写（一条顶四条），不是一个路径
        if r.startswith("-"):
            SHAPE_SKIPPED.append(("省略写法", r))
            continue          # `-v7-1017.txt` 这种是我接在上一个全名后面的省略写法
        if re.match(r"^\d+[acd]\d*\s", r):
            SHAPE_SKIPPED.append(("diff 行号记法", r))
            continue          # `62a64 thicken_thin_cells.py` 是 diff 的行号记法，不是文件名
        out.append(r)
    return out


def _index():
    idx = {}
    for dp, dn, fn in os.walk(ROOT):
        if ".hermess-snapshots" in dp:
            continue
        for f in fn:
            idx.setdefault(f, os.path.join(dp, f))
    return idx


IDX = _index()


def resolve(ref):
    for cand in (os.path.join(ROOT, ref), os.path.join(ROOT, "报告", os.path.basename(ref)),
                 os.path.join(ROOT, "tasks", "probe", os.path.basename(ref)),
                 os.path.join(ROOT, "tasks", "mcp", os.path.basename(ref)),
                 os.path.join(ROOT, ".hermess-snapshots", os.path.basename(ref))):
        if os.path.exists(cand):
            return cand
    return IDX.get(os.path.basename(ref))


def kind_of(path, ref):
    """按**引用对象的类别**各用自己那条判据，一把尺子量所有文件就会把"类别不适用"误报成"证据没了"：
       报告里的证据件 ⇒ 要有一行自印（竖线 + 数字）；回滚基线 ⇒ 要有 `<64位哈希> *路径` 那样的行；
       .py ⇒ 有 `def `；.sh ⇒ shebang 或 case；.go ⇒ 存在即可（它不是读数，是被引的源码）；
       .jsonl ⇒ 是链/账本，不是读数 ⇒ 判据是"非空且逐行是 JSON 对象"，不该拿"有没有自印行"去问它。"""
    if path is None:
        return "断链"
    base = os.path.basename(path)
    s = io.open(path, encoding="utf-8", errors="replace").read()
    if base.endswith(".go"):
        return "源码·存在即可"
    if base.endswith(".jsonl"):
        ls = [l for l in s.splitlines() if l.strip()]
        ok = bool(ls) and all(l.lstrip().startswith("{") for l in ls[:3])
        return "账本件·%d 行" % len(ls) if ok else "账本件·**形状不对或空**"
    if base.startswith("回滚基线"):
        n = len([l for l in s.splitlines() if re.match(r"^[0-9a-f]{64}\s+[*]?\S+$", l)])
        return "基线件·有哈希行 %d 条" % n if n else "基线件·**没哈希行**"
    if base.endswith(".py"):
        return "脚本·有 def" if "def " in s else "脚本·**没 def**"
    if base.endswith(".sh"):
        return "脚本·有入口" if (s.startswith("#!") or "case " in s) else "脚本·**没入口**"
    if any("\uff5c" in l and re.search(r"\d", l) for l in s.splitlines()):
        return "证据件·有自印行"
    if any(re.search(r"[=＝]\s*\d", l) for l in s.splitlines()) or "分母" in s:
        return "证据件·有自印行（`名=数` 那种形状）"
    return "证据件·**没自印行**"


def main():
    # 10-19 待跑卡 ①②③ 三件一起做：
    # ① 端点**没有默认值**了。原来那句默认是 `http://127.0.0.1:8797/mcp` —— 那是别人的真账（2940 条），
    #    不带参数裸跑就会打上去；不写默认值比写一个"安全"默认值好，所以裸跑改成 exit 9 并指名它原本打给谁。
    # ② 目标已存在就起跑前拒绝（10-19 12:3x 已在），要覆盖必须显式带 `--force-overwrite`。
    # ③ 输出名里的日期段由脚本按当下生成，不再手写 `1017` —— 手写会盖到上一跳的原件上。
    flags = [a for a in sys.argv[1:] if a.startswith("--")]
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    bad_flags = [f for f in flags if f != "--force-overwrite"]
    if bad_flags:
        print("BAD_FLAG: 只认 `--force-overwrite`，拿到 %s" % ";".join(bad_flags).encode("unicode_escape"))
        return 4
    if not args:
        print("NEED_ENDPOINT: 端点必须显式给。这个参数原本是 `http://127.0.0.1:8797/mcp`，"
              "那是别人的账（不是我起的实例），所以宁可不给默认值 —— 裸跑直接退出。")
        return 9
    url = args[0]
    # 10-17 05:49 我把输出路径当 argv[1] 传进来过：那发把路径当成了端点，却照样落盘、照样印「退出码 0」——
    # 一份"链路齐"的假读数。所以形状要在门口就判死，不等到内容层再去猜。
    if not url.startswith("http"):
        print("BAD_ARGV: 第一个位置参数是端点 URL（要 http 开头），拿到的是 %s" % url.encode("unicode_escape"))
        return 4
    if len(args) > 2:
        print("BAD_ARGV: 位置参数只接两个（端点、输出名），拿到 %d 个：%s"
              % (len(args), ";".join(args).encode("unicode_escape")))
        return 4
    outp = args[1] if len(args) > 1 else os.path.join(
        ROOT, "报告", "融合链-逐段状态机检-%s.txt" % time.strftime("%m%d-%H%M"))
    if os.path.exists(outp) and "--force-overwrite" not in flags:
        print("REFUSE_TO_OVERWRITE: %s 已存在 —— 一份读数一个文件名；要覆盖必须显式带 `--force-overwrite`"
              % outp.encode("unicode_escape"))
        return 3
    out = []

    def w(s):
        out.append(s)
        sys.stdout.buffer.write((s + "\n").encode("unicode_escape", "backslashreplace").decode("ascii").encode("ascii") + b"\n")
        sys.stdout.flush()

    def finish(code):
        io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
        return code

    if not os.path.exists(DOC):
        w("NOT_RUN：对账件本体不在（%s）" % DOC.encode("unicode_escape").decode("ascii"))
        return finish(4)

    w("== 融合链逐段状态机检 · %s ｜ 端点 %s ==" % (time.strftime("%Y-%m-%d %H:%M:%S"), url))
    text = io.open(DOC, encoding="utf-8").read()
    secs = stage_sections(text)
    want = ["阶段 0", "阶段 1", "阶段 2", "阶段 3", "阶段 4", "阶段 5", "约束逐条核对"]
    w("对账件切出 %d 段；六段 + 约束那节是否齐：%s"
      % (len(secs), all(any(x in nm for nm, _b in secs) for x in want)))
    missing_secs = [x for x in want if not any(x in nm for nm, _b in secs)]
    if missing_secs:
        w("坏：对账件少了这些段：%s（结构被编辑吞过，这条必须报出来）" % " ".join(missing_secs))
        return finish(6)
    w("")
    w("段 ｜ 引用 ｜ 证据件可核 ｜ 脚本/源码/基线件可核 ｜ 坏那轮原件 ｜ 断链 ｜ 不可核（列在下面）")
    w("-" * 78)
    total_refs = ok_doc = ok_code = total_bad = total_broken = total_unver = 0
    broken_list = []
    unver_list = []
    other_refs = []
    for name, body in secs:
        rs = refs_in(body)
        inside = any(x in name for x in want)
        if not inside:
            other_refs += [(name, r) for r in rs]
            continue
        okd = okc = bad = broken = unver = 0
        for r in rs:
            p = resolve(r)
            k = kind_of(p, r)
            if p is None:
                broken += 1
                broken_list.append("%s ｜ %s" % (name[:12], r))
            elif p and any(mk in os.path.basename(p) for mk in BAD_MARKS):
                # 坏那轮/作废的原件：它"不可核"正是它被留着的原因，不能再算进「不可核」那一栏
                bad += 1
            elif p and "**" in k:
                unver += 1
                sam = next((l.strip()[:60] for l in io.open(p, encoding="utf-8", errors="replace").read().splitlines()
                            if re.search(r"\d", l)), "整份没有数字行")
                unver_list.append("%s ｜ %s ｜ %s ｜ 它里面第一行带数字的是：%s" % (name[:10], r, k, sam))
            elif k.startswith("证据件"):
                okd += 1
            else:
                okc += 1
        total_refs += len(rs)
        ok_doc += okd
        ok_code += okc
        total_bad += bad
        total_broken += broken
        total_unver += unver
        w("%-14s ｜ %-5s ｜ %-10s ｜ %-9s ｜ %-12s ｜ %-5s ｜ %s"
          % (name[:14], len(rs), okd, okc, bad, broken, unver))
    # 追加段以前只"列着"（前 6 条），等于今天新写的那十几节从没被机器打开过 —— 现在也逐条核
    a_okd = a_okc = a_bad = a_broken = a_unver = 0
    a_broken_list = []
    for nm, r in other_refs:
        p = resolve(r)
        k = kind_of(p, r)
        if p is None:
            a_broken += 1
            a_broken_list.append("%s ｜ %s" % (nm[:14], r))
        elif p and any(mk in os.path.basename(p) for mk in BAD_MARKS):
            a_bad += 1
        elif p and "**" in k:
            a_unver += 1
            unver_list.append("追加段 ｜ %s ｜ %s ｜ %s" % (nm[:10], r, k))
        elif k.startswith("证据件"):
            a_okd += 1
        else:
            a_okc += 1
    w("六段之外（各追加段）另有引用 %d 条 —— 现在也一条条打开核（以前只列前 6 条不核 = 新写的十几节没人验过）："
      % len(other_refs))
    w("   追加段：证据件可核 %d ｜ 脚本/源码/基线件可核 %d ｜ 坏那轮原件 %d ｜ 断链 %d ｜ 不可核 %d"
      % (a_okd, a_okc, a_bad, a_broken, a_unver))
    for b in a_broken_list[:8]:
        w("   追加段断链： %s" % b.encode("unicode_escape").decode("ascii")[:130])
    w("   形状不合、没当引用扫的 %d 条（省略写法 / diff 行号 / 花括号简写；列出来，不当成静默丢弃）：%s"
      % (len(SHAPE_SKIPPED),
         " ；".join("%s=`%s`" % (why, r) for why, r in SHAPE_SKIPPED[:8]) or "无"))
    total_refs += len(other_refs)
    ok_doc += a_okd
    ok_code += a_okc
    total_bad += a_bad
    total_broken += a_broken
    total_unver += a_unver
    broken_list += ["追加段 ｜ " + x for x in a_broken_list]
    w("")
    w("合计：引用 %d 条 ｜ 证据件可核 %d ｜ 脚本可核 %d ｜ 坏那轮/作废原件 %d ｜ 断链 %d ｜ 不可核 %d"
      % (total_refs, ok_doc, ok_code, total_bad, total_broken, total_unver))
    for u in unver_list:
        w("   不可核： %s" % u)
    for b in broken_list[:10]:
        w("   断链： %s" % b.encode("unicode_escape").decode("ascii")[:120])
    w("")
    w("== 四条约束（机械检查，不引用任何总结句）==")
    # 1) 判据表 48 键 / 49 格：现取端点格表
    try:
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                           "params": {"name": "kernel.celltable", "arguments": {}}}).encode("utf-8")
        r = json.loads(urllib.request.urlopen(urllib.request.Request(
            url, data=body, headers={"Content-Type": "application/json"}), timeout=30).read().decode("utf-8"))
        head = "".join(c.get("text", "") for c in r["result"]["content"] if c.get("type") == "text")
        decl = re.search(r"当前 (\d+) 格", head).group(1)
        dkeys = re.search(r"唯一键名 (\d+) 个", head).group(1)
        rows = len([l for l in head.splitlines()[1:] if "←" in l])
        keys = len({l.partition("←")[0].split()[1] for l in head.splitlines()[1:] if "←" in l and len(l.partition("←")[0].split()) == 2})
        w("  判据表：抬头 %s 格 / %s 键 ｜ 实到 %d 行 / %d 键 ｜ 没动 = %s"
          % (decl, dkeys, rows, keys, str(int(decl) == rows and int(dkeys) == keys and decl == "49" and dkeys == "48")))
    except Exception as e:
        w("  判据表：**端点没答（%r）** —— 这一条不算核过，也不算通过" % (e,))
    # 2) §8 十五条：现数 README
    rd = io.open(os.path.join(ROOT, "README.md"), encoding="utf-8").read()
    m = re.search(r"^#+\s*8[^\n]*\n(.*?)(?=^#+\s*9|\Z)", rd, re.S | re.M)
    if m:
        sec = m.group(1)
        num = len(re.findall(r"^\s*\d+[.)、]\s", sec, re.M))
        bul = len(re.findall(r"^\s*[-*]\s", sec, re.M))
        w("  §8 缺口清单：**编号条目 %d 条**（那句「十五条」核这个数） ｜ 另有子项/续行 %d 条不计入"
          % (num, bul))
    else:
        w("  §8 缺口清单：**没切到 §8 那一节** —— 这条没核上")
    # 3) 看板在跑上限：调用那把尺子，不重抄
    bc = run([sys.executable, os.path.join(ROOT, "tasks", "probe", "board_check.py")])
    # 这里必须写反斜杠转义：编辑层会把 U+FFFD 那个字面字符从源码里吃掉，留下 `count("")` —— 那是个永远非零的判据。
    bc_dirty = bc.stdout.count("\ufffd")
    bc_tail = " ／ ".join(l.strip() for l in bc.stdout.strip().splitlines()[-3:]) if bc.stdout.strip() else "（stdout 是空的）"
    w("  看板：调 `board_check.py` 退出码 %d ｜ 捕获到的 stdout 里脏字符 %d 个 ｜ 末尾三行：%s"
      % (bc.returncode, bc_dirty, bc_tail[:150]))
    if bc_dirty:
        total_unver += 1
        w("  ⇒ 这一行有 %d 个 U+FFFD：归档里中文不可复原、只剩 ASCII 数字在守 —— 记进「不可核」，"
          "「看板那行绿了」这句话不能引用" % bc_dirty)
    # 3b) 等表态那张卡的条数：不许在这支探针里写死（v9 就写死过一回「8 条」）
    bd = io.open(os.path.join(ROOT, "报告", "看板.md"), encoding="utf-8").read().splitlines()
    sec, cnt, claimed = "", 0, None
    for l in bd:
        if l.startswith("## "):
            sec = l[3:].strip()
            if "等表态" in sec:
                mm = re.search(r"（(\d+)）", l)
                claimed = int(mm.group(1)) if mm else None
            continue
        if "等表态" in sec and l.startswith("- ❓ "):
            cnt += 1
    w("  等表态：看板题头写 %s ｜ 这支探针现数 `- ❓ ` 卡 %d 张 ｜ 两者相等 = %s"
      % (claimed, cnt, claimed == cnt))
    # 4) 回滚不用 git + 三桶不相加 + 最新基线自校验
    w("  不用 git：`.git` 目录存在 = %s（必须为 False） ｜ 快照点 `.hermess-snapshots/points.jsonl` 存在 = %s"
      % (os.path.isdir(os.path.join(ROOT, ".git")), os.path.exists(os.path.join(ROOT, ".hermess-snapshots", "points.jsonl"))))
    pl = os.path.join(ROOT, "报告", "三条融合前提-合账-v3-1017.txt")
    if os.path.exists(pl):
        t = io.open(pl, encoding="utf-8").read()
        bq = re.search(r"四桶.{0,6}差值[^\n]*", t)
        w("  三桶不相加：合账里那句「不相加」在 = %s ｜ 逐桶那一行 = %s"
          % ("不相加" in t, bq.group(0)[:70] if bq else "**抠不到**"))
    else:
        w("  三桶不相加：**前提合账那份不在** —— 这条没核上")
    # 10-19 12:44 这一条 glob 硬写过 `1017`，结果 12:38:13 重出的 `回滚基线-1019a.txt` 它一眼看不见，
    # 机检拿着 87 条的旧基线报「FAILED 18」——那是尺子漂，不是基线对不上。（时间按 mtime 现读，不靠手感：
    # 上一版这句我写成 13:2x，错了，改正记录在 `报告/基线对照-1019c-逐名归因.txt` 第 1 行。）
    # 换成按日期段无关的形状；同时把「全集里有几种写法」自己印出来，并把旧 glob 的读数并排列在旁边，
    # 这样下一次漂（比如出现 `回滚基线-1019a-说明.txt`）还能被看见，而不是静悄悄少扫一份。
    # 尾巴用 `[a-z]*` 不是 `+`：`回滚基线-1017.txt`（10-17 10:18 那一份）是真基线，写 `+` 会把它扫没 ——
    # 这一条不是我看出来的，是下面那串合成名负对照自己报的（它当时被判成"认不到"）。
    BL_GLOB = r"^回滚基线-\d{4}[a-z]*\.txt$"
    BL_OLD = r"^回滚基线-1017[a-z]+\.txt$"
    # 以名字当契约：`回滚基线-` 打头但不是基线本体的，只有下面这三份，必须逐名声明、每次把名单印出来。
    # 空表不豁免、漏声明要报红 —— 否则「glob 认不到」和「这本来就不该被认」会被混成一句。
    # 10-19 现场：全集 42 份里有 3 份走不进 glob（两支「作废那轮」的留痕 + 一份逐名归因说明），
    # 它们各自有理由，不是一个「大概没事」。
    BL_EXEMPT = {
        "回滚基线-1017d-作废那轮.txt": "那一轮作废，留原名做留痕，不该被当基线读",
        "回滚基线-1017g-轮中取的那份作废.txt": "同上：轮中取错的那份，留着但不作基线",
        "回滚基线-1019a-逐名归因.txt": "10-19 的归因说明，不是基线本体",
    }
    bl_dir = os.path.join(ROOT, "报告")
    bl_all = sorted(f for f in os.listdir(bl_dir) if f.startswith("回滚基线") and f.endswith(".txt"))
    cands = [f for f in bl_all if re.match(BL_GLOB, f)]
    odd = [f for f in bl_all if not re.match(BL_GLOB, f)]
    odd_real = [f for f in odd if f not in BL_EXEMPT]
    no_cands = [f for f in BL_EXEMPT if f not in odd]
    w("  基线全集扫法：`回滚基线*.txt` 共 %d 份 ｜ 现 glob `%s` 认 %d 份 ｜ glob 认不到 %d 份：%s"
      % (len(bl_all), BL_GLOB, len(cands), len(odd),
         "、".join("`%s`（%s）" % (o, BL_EXEMPT[o]) for o in odd) if odd else "（一份没有）"))
    if odd_real:
        total_unver += 1
        w("  ⇒ glob 认不到、又没逐名声明过的：%s ｜ 记进「不可核」，整份机检的退出码不为 0"
          % "、".join("`" + o + "`" for o in odd_real))
    else:
        w("  ⇒ glob 认不到的 %d 份都已逐名声明（不是「静悄悄少扫一份」）" % (len(odd),))
    if no_cands:
        total_unver += 1
        w("  ⇒ 声明名单里有 %d 支文件盘上已经不存在：%s ｜ 记进「不可核」——豁免名单也不能只写不核"
          % (len(no_cands), "、".join("`" + n + "`" for n in no_cands)))
    # 负对照一：同一批文件用旧 glob 数一遍，证明"换 glob"不是换个说法把数做大
    old_cands = [f for f in bl_all if re.match(BL_OLD, f)]
    n_new = max(cands, key=lambda f: os.path.getmtime(os.path.join(bl_dir, f))) if cands else None
    n_old = max(old_cands, key=lambda f: os.path.getmtime(os.path.join(bl_dir, f))) if old_cands else None
    w("  负对照（新旧两版 glob 数同一批文件）：旧 `%s` 认 %d 份、最新取到 %s ｜ 新认 %d 份、最新取到 %s ｜ 两版最新同一支 = %s"
      % (BL_OLD, len(old_cands), "`%s`" % n_old if n_old else "无",
         len(cands), "`%s`" % n_new if n_new else "无", n_new == n_old))
    # 负对照二：把 glob 本身拿去打一串合成名，认到的和认不到的都必须非空 ——
    # 否则「认不到」这条永远不红，是因为它根本没有能认不到的形状（空表不读成绿）。
    probe_names = ["回滚基线-1019a.txt", "回滚基线-1017am.txt", "回滚基线-1017.txt",
                   "回滚基线-1019a-逐名归因.txt", "回滚基线-说明.txt", "基线-1019a.txt"]
    p_in = [x for x in probe_names if re.match(BL_GLOB, x)]
    p_out = [x for x in probe_names if not re.match(BL_GLOB, x)]
    w("  负对照（glob 自己会不会红）：合成名 %d 支里认 %d 支、不认 %d 支 ｜ 两侧都非空 = %s"
      % (len(probe_names), len(p_in), len(p_out), bool(p_in) and bool(p_out)))
    if not (bool(p_in) and bool(p_out)):
        total_unver += 1
        w("  ⇒ glob 变成了一边倒的判据，这条记进「不可核」")
    # 「最新」按落盘时间取，不按文件名字母序 —— 基线字母发到 q…z 之后接的是 aa，
    # 字母序会把 z 当成最新（实测错过一回：那 1 条 FAILED 就是这么来的）。
    cands = sorted(cands, key=lambda f: os.path.getmtime(os.path.join(bl_dir, f)))
    newest = cands[-1] if cands else None
    if newest:
        w("  最新基线按 mtime 取 = %s ｜ 按字母序会取到 %s（%s）"
          % (newest, sorted(cands)[-1],
             "两者相同，这一发不区分" if newest == sorted(cands)[-1] else "不同 —— 命名已越过 z"))
        try:
            # 基线是 CRLF 写的，`sha256sum -c` 会把每行末尾的 CR 当成路径的一部分（实测：66 条全报「找不到文件」）。
            # 所以先落一份去 CR 的副本再核 —— 这不是放水，是让那把外部尺子读得懂这份文件。
            tmp = os.path.join(os.environ.get("TEMP", "/tmp"), "chain_status_baseline_lf.txt")
            io.open(tmp, "w", encoding="utf-8", newline="\n").write(
                io.open(os.path.join(ROOT, "报告", newest), encoding="utf-8").read().replace("\r", ""))
            c = run(["sha256sum", "-c", tmp], cwd=ROOT)
        except Exception as e:
            w("  最新基线：**跑不了 sha256sum（%r）** —— 这一条不算核过" % (e,))
            c = None
        if c is not None:
            oks = c.stdout.count(": OK")
            fns = len([l for l in c.stdout.splitlines() if ": FAILED" in l])
            denom = len([l for l in io.open(os.path.join(ROOT, "报告", newest), encoding="utf-8").read().splitlines() if not l.startswith("#") and l.strip()])
            clean = (oks == denom and fns == 0 and c.returncode == 0)
            w("  最新基线：%s ｜ 分母 %d 条 ｜ OK %d ｜ FAILED %d ｜ 退出码 %d ｜ 全对上 = %s"
              % (newest, denom, oks, fns, c.returncode, clean))
            if not clean:
                total_unver += 1
                w("  ⇒ 基线没全对上，这一条记进「不可核」，整份机检的退出码不为 0")
    else:
        w("  最新基线：一份都没找到 —— 这条没核上")
    w("")
    w("== 自核（钉这把尺子）==")
    hit = resolve(SENTINEL)
    w("  正对照：那条故意不存在的引用被判成断链 = %s（若为 False，上面「断链 0」就是假的）" % (hit is None))
    if hit is not None:
        w("坏：正对照失效 —— 这份机检不许引用。")
        return finish(6)
    # 自核二（10-19 待跑卡新立的那条：捕获—转码这一层自己也要能红）：
    # 同一 `run()` 路径喂两种字节，正反两头各一根 —— 只跑正对照会重演「绿是空的」。
    zh = "看板：这一句要走完捕获到归档整条路"
    ok = run([sys.executable, "-c", "import sys;sys.stdout.buffer.write(%r)" % zh.encode("utf-8")])
    bad = run([sys.executable, "-c", "import sys;sys.stdout.buffer.write(%r)" % zh.encode("gbk")])
    dirty = chr(0xFFFD)
    w("  编码正对照：子进程写 UTF-8，本层逐字收回来 = %s ｜ 脏字符 %d 个"
      % (ok.stdout == zh, ok.stdout.count(dirty)))
    w("  编码负对照：同一层喂 GBK 字节，判出脏 = %s ｜ 脏字符 %d 个（若为 False，上面那句「不脏」没被证明过）"
      % (dirty in bad.stdout, bad.stdout.count(dirty)))
    if ok.stdout != zh:
        total_unver += 1
        w("  ⇒ 正对照没过：中文在捕获—转码这一层被改动，归档里那一行不可复原 —— 记进「不可核」")
    if dirty not in bad.stdout:
        total_unver += 1
        w("  ⇒ 负对照红不起来：这条编码闸门是空的，它挡不住任何脏读数 —— 记进「不可核」")
    w("")
    w("还缺的（工具核不出来、只能由人给的那两件）：")
    w("  · E 段：一个**没碰过本仓的人**跑 `acceptance_probe.py` 并自己签 `--attest-caller` —— 今天真人接手 0 次；")
    dec = sorted(f for f in os.listdir(os.path.join(ROOT, "报告"))
                 if f.startswith("决策件-") and f.endswith(".txt"))
    w("  · 作者拍：等表态现数 %d 条 ｜ `报告/` 里决策件现数 %d 份：%s"
      % (cnt, len(dec), "、".join("`" + d + "`" for d in dec)))
    w("    （两份数**口径不同、不相减**：卡片按 `- ❓ ` 现数，决策件按文件现数 —— 有的件一份盖 8 张卡，有的卡单独成文；"
      "旧那句「只成文了 8 条」是抄来的账面，已改成现数。）")
    w("    其中 #48 决定 §唯一验收 条件 1 的后半能不能机械核，#59 决定前提①还剩那一处闭不闭，#64 决定 BH 那一家以哪种件算用过。")
    code = 1 if (total_broken or total_unver) else 0
    w("")
    w("结论：断链 %d 处、不可核 %d 处；六段 + 约束那节共引用 %d 条，其中证据件可核 %d、脚本可核 %d、坏那轮原件 %d。"
      % (total_broken, total_unver, total_refs, ok_doc, ok_code, total_bad))
    w("        退出码 %d（断链或不可核都不许读成「链路齐」）。" % code)
    return finish(code)


if __name__ == "__main__":
    sys.exit(main())
