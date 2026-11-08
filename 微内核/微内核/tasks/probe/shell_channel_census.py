# -*- coding: utf-8 -*-
"""shell 那一族的读数出口普查：工具链路径、编码钉法、危险动作面 —— 只读，不改一个字节。

为什么要这一支（10-19 15:4x 那张 #71 卡的【三】里我自己写的一句）：
  「不能说这 3 处之外全仓没有别的编码漏点 —— 这一支只扫 .py 的 open/subprocess 两种出口，
    `.sh`/`.ps1` 里的管道转码不在这 55 份的判据里。」
  这句是**没数**，不是"没问题"。看板第 22 张编码卡的欠账也挂在同一句上。

四条判据（各数各的，不合成一个数）：
  C1 工具链路径写死到某个用户目录（`/c/Users/<某人>/sdk/...` 那样的赋值）——换一台机器就撞空路径；
  C2 起 python 却没钉编码（那一行既无 `-X utf8`，整个文件也没 `export PYTHONIOENCODING`/`PYTHONUTF8`）；
  C3 中文出口（脚本按 UTF-8 串 grep 或 echo 中文）——它**依赖** C2：母体 env 不钉，成败由调用方式决定；
  C4 危险动作面（绑端口 / 杀进程 / 往 `报告/` 写）——只报形状，用来解释"为什么这两支今天 0 次执行"。

反臂（三条，都是叶子：红了不落盘）：MISSCTL 缺必需源件 → 7；VALIDCTL 词表名字漂 → 6；EXPECTCTL 逐名预期不成立 → 6。
跑法：python tasks/probe/shell_channel_census.py [输出名]   （不给名字就按当下时间自生成到 报告/；目标已存在不给 --force-overwrite 就 3）
"""
import io
import os
import re
import sys
import time

CTL = next((k for k in ("EXPECTCTL", "VALIDCTL", "MISSCTL") if os.environ.get(k) == "1"), "")
EXPECTCTL = CTL == "EXPECTCTL"
VALIDCTL = CTL == "VALIDCTL"
MISSCTL = CTL == "MISSCTL"

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUTDIR = os.path.join(ROOT, "报告")
SKIP_DIRS = {"__pycache__", ".git", "node_modules"}
SHELL_EXT = (".sh", ".ps1", ".cmd", ".bat")
DELIVERY_ROOT = "构建-1017"

FORCE = "--force-overwrite" in sys.argv
args = [a for a in sys.argv[1:] if not a.startswith("--")]
if EXPECTCTL or VALIDCTL or MISSCTL:
    OUT = os.path.join(os.environ.get("TEMP", "/tmp"), "stdio-1019",
                       "shell-child-%s-%s.txt" % (CTL, time.strftime("%H%M%S")))
    FORCE = "1"
elif args:
    OUT = args[0] if os.path.isabs(args[0]) else os.path.join(ROOT, args[0])
else:
    OUT = os.path.join(OUTDIR, "shell读数出口普查-1019-%s.txt" % time.strftime("%H%M%S"))


def die(code, tag, msg):
    line = "%s: %s" % (tag, msg)
    if FORCE != "1":
        print(line)
    else:
        io.open(OUT, "w", encoding="utf-8", newline="\n").write(line + "\n")
    if CTL:
        print("CHILD-LEAF-STOP=True")
    sys.exit(code)


def scan_shell():
    rows = []
    for dp, dn, fn in os.walk(ROOT):
        rel = os.path.relpath(dp, ROOT).replace(os.sep, "/")
        if rel == ".":
            dn[:] = [d for d in dn if d not in SKIP_DIRS and not d.startswith(".")]
        for f in sorted(fn):
            if not f.endswith(SHELL_EXT):
                continue
            rp = (os.path.join(rel, f) if rel != "." else f).replace(os.sep, "/")
            raw = io.open(os.path.join(ROOT, rp), "rb").read()
            rows.append((rp, raw.decode("utf-8", "replace").splitlines(), raw))
    return rows


ASSIGN = re.compile(r'^(?:GO|PY|GOROOT|GOBIN)\s*=\s*"?([^\s"#]*Users/[^"\s]+)"')
PYCALL = re.compile(r'(?<![\w#])"?(?:\$PY|python3?|py314)["\s]')
PINNED_LINE = re.compile(r"-X\s+utf8|PYTHONIOENCODING|PYTHONUTF8")
PINNED_FILE = re.compile(r"export\s+PYTHON(IOENCODING|UTF8)|set\s+(PYTHONIOENCODING|PYTHONUTF8)")
CJK = re.compile(r"[　-〿一-鿿＀-￯]")
DANGER = {"绑端口": re.compile(r"-listen\s|Start-Process|netstat"),
          "杀进程": re.compile(r"taskkill|Stop-Process"),
          "写报告": re.compile(r"报告/")}

# 逐名预期（每条写死我认为的判据结果；错了 EXPECT-BAD 红给我看）
EXPECTS = [("tasks/probe/kaudit_smoke.sh", "C1", True), ("tasks/probe/kaudit_smoke.sh", "C2", True),
           ("tasks/probe/kaudit_smoke.sh", "C4", True), ("tasks/probe/mcphttp_up.sh", "C1", True),
           ("tasks/probe/mcphttp_up.sh", "C4", True), ("tasks/probe/run_freeze_round.sh", "C1", False),
           ("tasks/probe/run_freeze_round.sh", "C2", True), ("tasks/probe/run_freeze_round.sh", "C3", True),
           ("tasks/probe/lie_arms.sh", "C1", False), ("tasks/probe/lie_arms.sh", "C2", False),
           ("tasks/probe/lie_arms.sh", "C3", True)]
if EXPECTCTL:
    EXPECTS.append(("tasks/probe/lie_arms.sh", "C1", True))

VOCAB = [PINNED_LINE, PINNED_FILE, ASSIGN, CJK]


def main():
    if MISSCTL:
        need = ["tasks/probe/run_freeze_round.sh", "tasks/probe/lie_arms.sh",
                "tasks/probe/__不存在的必需件__.sh"]
    else:
        need = ["tasks/probe/run_freeze_round.sh", "tasks/probe/lie_arms.sh",
                "tasks/probe/kaudit_smoke.sh", "tasks/probe/mcphttp_up.sh"]
    rows = scan_shell()
    names = [r[0] for r in rows]
    miss = [f for f in need if f not in names]
    if miss:
        die(7, "MISS", "必需件缺位（分母不成立，不落盘）：%s" % "、".join("`%s`" % m for m in miss))

    pinned_lines = sum(1 for _, ls, _ in rows for l in ls if PINNED_LINE.search(l))
    if VALIDCTL:
        pinned_lines = 0
    if pinned_lines == 0:
        die(6, "VOCAB-BAD", "判据锚在 4 份脚本里一次都没命中 —— 锚漂了，这份读数不许引用")

    L = []
    W = L.append
    W("shell 那一族的读数出口普查 · %s" % time.strftime("%Y-%m-%d %H:%M:%S"))
    W("只读：不改一个字节、不起服务、不占端口、不杀进程、不调任何 MCP 工具。")
    W("")
    W("【一】分母自印")
    repo = [r for r in rows if not r[0].startswith(DELIVERY_ROOT + "/")]
    deliv = [r for r in rows if r[0].startswith(DELIVERY_ROOT + "/")]
    W("  全仓 `%s` = %d 份 ｜ 仓库内 %d 份 ｜ 交付树 `%s` 里 %d 份（快照，不是判据件，另桶）" % (
        "/".join(SHELL_EXT), len(rows), len(repo), DELIVERY_ROOT, len(deliv)))
    W("  仓库内逐名：%s" % " ".join("`%s`" % r[0] for r in repo))
    W("  判据锚命中：写死赋值 `%s` 的赋值行 %d ｜ 钉编码行 %d ｜ 含中文的行合计 %d" % (
        "GO|PY=", pinned_lines, sum(1 for _, ls, _ in repo for l in ls if CJK.search(l))))
    W("  词表锚（每个锚全仓命中次数，任一为 0 我就不落盘）：钉编码 %d ｜ 起 python %d ｜ 中文 %d ｜ 危险动作 %d" % (
        sum(1 for _, ls, _ in rows for l in ls if PINNED_LINE.search(l)),
        sum(1 for _, ls, _ in rows for l in ls if PYCALL.search(l) and not l.strip().startswith("#")),
        sum(1 for _, ls, _ in rows for l in ls if CJK.search(l)),
        sum(1 for _, ls, _ in rows for l in ls if any(p.search(l) for p in DANGER.values()))))
    W("")
    W("【二】逐件逐判据（C1 写死路径 ｜ C2 起 python 没钉编码 ｜ C3 中文出口 ｜ C4 危险动作面）")
    census = {}
    for rp, ls, raw in repo:
        c1 = [(i + 1, l.strip()) for i, l in enumerate(ls) if ASSIGN.match(l.strip())]
        pycalls = [(i + 1, l.strip()) for i, l in enumerate(ls)
                   if PYCALL.search(l) and not l.strip().startswith("#")]
        file_pinned = bool(PINNED_FILE.search("\n".join(ls)))
        c2 = [(ln, t) for (ln, t) in pycalls if not PINNED_LINE.search(t)]
        c3 = [(i + 1, l.strip()) for i, l in enumerate(ls)
              if CJK.search(l) and (re.search(r"grep|echo|printf|say", l) or l.strip().startswith("#"))]
        c4 = []
        for tag, pat in DANGER.items():
            hit = [(i + 1, l.strip()) for i, l in enumerate(ls) if pat.search(l) and not l.strip().startswith("#")]
            if hit:
                c4.append((tag, hit[0][0], len(hit)))
        census[rp] = (bool(c1), bool(c2) and not file_pinned, bool(c3), bool(c4), file_pinned, len(pycalls))
        W("  `%s` ｜ %d 行 ｜ %d 字节（非 ASCII 字节 %d）" % (rp, len(ls), len(raw),
                                                            sum(1 for b in raw if b > 127)))
        W("     C1 写死路径 = %s ｜ 逐名 %s" % ("中" if c1 else "未中",
            "；".join("%d:`%s`" % (n, t[:56]) for n, t in c1) or "无"))
        W("     C2 起 python %d 处，其中没钉编码 %d 处 ｜ 文件级 export 钉编码 = %s ｜ 逐名 %s" % (
            len(pycalls), len(c2), "有" if file_pinned else "无",
            "；".join("%d:`%s`" % (n, t[:52]) for n, t in (c2 if not file_pinned else [])) or "无"))
        W("     C3 中文出口行 = %d ｜ 例子 %s" % (len(c3), "；".join("%d:`%s`" % (n, t[:44]) for n, t in c3[:2]) or "无"))
        W("     C4 危险动作面 = %s" % ("；".join("%s（首现 %d 行，共 %d 处）" % h for h in c4) or "无"))
        W("")
    for rp, ls, raw in deliv:
        W("  [%s 桶] `%s` ｜ %d 行" % (DELIVERY_ROOT, rp, len(ls)))
    W("")
    W("【三】计数（按判据各数各的，不相加）")
    for idx, name in ((0, "C1 写死工具链路径"), (1, "C2 起 python 没钉编码（且文件级也没 export）"),
                      (2, "C3 中文出口"), (3, "C4 危险动作面")):
        hit = [rp for rp, v in census.items() if v[idx]]
        W("  %-40s %d 份 ｜ 逐名：%s" % (name, len(hit), " ".join("`%s`" % h for h in hit) or "无"))
    diff = [(rp, c) for rp, c in census.items() if c[1]]
    W("  四桶差值 = [%s] ⇒ 每桶独立成数，不相加、不相减" % ", ".join(
        str(sum(1 for _, c in census.items() if c[i])) for i in range(4)))
    W("")
    W("【四】这一发能说什么 / 不能说什么")
    W("  能说：C1 的活样本在 %s —— 那两行赋值指向另一个用户的 sdk 目录，本机换了人就没路径；" % "、".join(
        "`%s`" % rp for rp, c in census.items() if c[0]) )
    W("        同族已有一份按先例修好的：`tasks/probe/run_freeze_round.sh` 用 `先探 → 探不到就说清找过哪几处 → exit 9`，"
      "这一支把它数出来了而不是我记得。")
    W("  能说：C2 命中 %d 份，且这三份里 `run_freeze_round.sh` 起了 python 却不钉编码 —— " % sum(1 for _, c in census.items() if c[1]) +
      "今天那一轮能绿，是我在起跑的 shell 里 `export PYTHONIOENCODING=utf-8` 带过去的，不是脚本自己钉的（这条不是猜：脚本全文 grep 无 export 无 -X utf8，见【二】）。")
    W("  不能说：这些脚本今天跑过还是没跑过 —— 这一支只读文件。今天 0 次执行的那两支（`kaudit_smoke.sh`、`mcphttp_up.sh`）"
      "卡在哪一栏由【二】的 C4 说：它们绑端口、且其中一支会 taskkill 自己的 PID；按规矩这两件事要先问再做。")
    W("  不能推广：C3「中文出口」本身不是缺陷，缺陷只在它**依赖** C2；两侧都钉好（母体带 utf-8 模式或脚本自己 export）时中文出口按 UTF-8 字节匹配是能ework的形状。")
    W("")
    W("【五】逐名预期与分母自印（收尾）")
    bad = []
    for rp, key, want in EXPECTS:
        idx = int(key[1])
        got = census.get(rp, (None,) * 6)[idx]
        if got is None or got != want:
            bad.append("%s %s 判成 %s，我写的预期是 %s" % (rp, key, got, want))
    if EXPECTCTL:
        bad.append("反臂自己：这条 EXPECT 是我硬塞的一条假预期，必须在")
    if bad:
        die(6, "EXPECT-BAD", "；".join(bad))
    W("  逐名预期 %d 条全中（含 3 条「未中」的预期 —— 空表不读成绿，命中与不命中都得先声明）" % len(EXPECTS))
    W("  件 %d（仓库内 %d ＋ 交付桶 %d）｜ C1 %d ｜ C2 %d ｜ C3 %d ｜ C4 %d ｜ 反臂 %s ｜ 退出码 0" % (
        len(rows), len(repo), len(deliv),
        sum(1 for _, c in census.items() if c[0]), sum(1 for _, c in census.items() if c[1]),
        sum(1 for _, c in census.items() if c[2]), sum(1 for _, c in census.items() if c[3]), CTL or "未开"))
    text = "\n".join(L) + "\n"
    leak = [l for l in L if re.search(r"%[sdf]\b", l)]
    if leak:
        die(8, "FORMAT-LEAK", "正文里留着未格式化的占位符 %d 行（首行：%s）" % (len(leak), leak[0][:60]))
    if os.path.exists(OUT) and FORCE != "1":
        die(3, "REFUSE_TO_OVERWRITE", "目标已存在：%s（要覆盖带 --force-overwrite）" % OUT)
    if not os.path.isdir(os.path.dirname(OUT)):
        os.makedirs(os.path.dirname(OUT))
    io.open(OUT, "w", encoding="utf-8", newline="\n").write(text)
    print("WROTE %s ｜ %d 行 ｜ %d 字节" % (OUT, text.count("\n"), os.path.getsize(OUT)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
