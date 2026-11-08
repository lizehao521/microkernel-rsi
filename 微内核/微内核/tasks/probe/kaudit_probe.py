#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""kaudit_probe —— 只读审计外壳的"能红一次"记录。

为什么单独一个脚本，而不是把这 7 根针塞进 tasks/probe/kprobe.py：
kprobe.py 那 76 根是**封板的内核判据表**的针（48 键/49 格，2026-10-15 已冻）。
cmd/kaudit 是封板之后新长的外壳，把它塞进那张表就等于宣布封板轮作废、重跑 1.6 小时。
所以这里给它一份**自己的**针表和**自己的**汇总 —— 数字由这个脚本自己印，不由我转述。

它证的是一件事：外壳里那七条口头承诺，每一条都有一个测试会在它被抹掉时报红，
而且红的就是点名的那一个（或明确说清红了哪些）。

用法（项目根，机器空闲时）：
    C:/Users/Admin/sdk/py314/python.exe tasks/probe/kaudit_probe.py <GO> <报告文件> [只跑名单]
"""
import os
import re
import subprocess
import sys

GO = sys.argv[1] if len(sys.argv) > 1 else r"C:\Users\Admin\sdk\go\bin\go.exe"
REPORT = sys.argv[2] if len(sys.argv) > 2 else os.path.join(
    os.environ.get("TEMP", "."), "kaudit-probe-report.txt")
FILTER = [f for f in (sys.argv[3].split(",") if len(sys.argv) > 3 and sys.argv[3] else []) if f]

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SRC = os.path.join(ROOT, "cmd", "kaudit", "main.go")
TIMEOUT = int(os.environ.get("KAUDIT_PROBE_TIMEOUT", "420"))

# (名字, 原字节, 坏字节, 点名该红的测试)
NEEDLES = [
    ("a1-healthz不看口令",
     b'\tmux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {\n'
     b'\t\tif !s.auth(w, r) {\n\t\t\treturn\n\t\t}\n',
     b'\tmux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {\n',
     ["TestAuthRequiredAndSilent"]),
    ("a2-写请求先吃401",
     b'\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {\n'
     b'\t\tif r.Method != http.MethodGet && r.Method != http.MethodHead {\n'
     b'\t\t\tw.Header().Set("Allow", "GET, HEAD")\n',
     b'\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {\n'
     b'\t\tif false {\n\t\t\tw.Header().Set("Allow", "GET, HEAD")\n',
     ["TestReadOnly"]),
    ("a3-405不告知只许GET",
     b'\t\t\tw.Header().Set("Allow", "GET, HEAD")\n',
     b'',
     ["TestReadOnly"]),
    ("l4-没口令也许绑全网",
     b'\tif os.Getenv("KAUDIT_TOKEN") == "" {\n',
     b'\tif false {\n',
     ["TestListenGuardRefusesPublicBindWithoutToken"]),
    ("s5-人读表头当成账",
     b'\tif strings.HasPrefix(strings.TrimSpace(text[:i]), "{") {\n\t\treturn text, false\n\t}\n',
     b'\tif true {\n\t\treturn text, false\n\t}\n',
     ["TestHeaderLineIsSkippedNotCountedBad"]),
    ("t6-核不了压成绿",
     b'\tif !s.report.Decidable() {\n\t\treturn "undecidable"\n\t}\n',
     b'',
     ["TestNoPreimageIsNotGreen"]),
    ("r7-少一个轴也照答",
     b'\tif session == "" {\n\t\treturn "", 0, "", fmt.Errorf("session',
     b'\tif false {\n\t\treturn "", 0, "", fmt.Errorf("session',
     ["TestReadBackNeedsThreeAxes"]),
    # 10-15 用户表态后加的一根：一份一条都没有的账供出去不许念成 green。
    # 实例（真读数，不是设想）：`kaudit -journal 空文件` 的 /v1/verify 曾回 state:"green"。
    ("a8-空账供出去念成绿",
     b'\tif s.report.Entries == 0 {\n',     b'\tif false && s.report.Entries == 0 {\n',     ["TestEmptyBookIsNotGreen"]),
]

lines = []
console = []


def w(s):
    lines.append(s)


def say(s):
    console.append(s)


def run(args):
    """跑一条 go 命令，返回 (returncode, 合流后的输出)。超时算红，不算绿。"""
    env = dict(os.environ)
    env["PATH"] = os.path.dirname(GO) + os.pathsep + env.get("PATH", "")
    try:
        p = subprocess.run([GO] + args, cwd=ROOT, capture_output=True, text=True,
                           encoding="utf-8", errors="replace", timeout=TIMEOUT)
        return p.returncode, (p.stdout or "") + (p.stderr or "")
    except subprocess.TimeoutExpired:
        return 124, "probe-timeout: %d 秒没跑完（不是绿）" % TIMEOUT


def testmap(out):
    return sorted(set(re.findall(r"^--- FAIL: (\S+)", out, re.M)))


def verdicts(out):
    return bool(re.search(r"^--- (PASS|FAIL): ", out, re.M))


def broke(out):
    """测试压根没跑起来的形状。Go 对"坏在包内"和"坏在测试装配"印的是两个不同标记，
    只认其中一个的话，另一个会伪装成"干净"—— 对照 T 就是为了让这种瞎法当场露出来。"""
    return "[build failed]" in out or "[setup failed]" in out


original = open(SRC, "rb").read()

# 先只验锚点（不编译）：改 main.go 之后能快速知道哪几根针失效了。
if os.environ.get("KAUDIT_PROBE_CHECK") == "1":
    dead = [n[0] for n in NEEDLES if n[1] not in original]
    dup = [n[0] for n in NEEDLES if original.count(n[1]) > 1]
    print("ANCHORS total=%d missing=%s ambiguous=%s" % (len(NEEDLES), dead or "none", dup or "none"))
    sys.exit(1 if dead or dup else 0)

w("==== 对照 ====")
code, out = run(["test", "-p", "1", "-count=1", "-timeout", "120s", "-v", "./cmd/kaudit"])
npass = len(re.findall(r"^--- PASS: ", out, re.M))
nfail = len(re.findall(r"^--- FAIL: ", out, re.M))
w("对照 0（未改动的树）：go test 退出码 %d、PASS %d 条、FAIL %d 条（该 0 / %d / 0）" % (code, npass, nfail, npass))
baseline_ok = code == 0 and nfail == 0 and npass > 0
if not baseline_ok:
    w("!! 干净树上就没全过 —— 这一轮不算，先修树。")
    for ln in out.splitlines():
        if ".go:" in ln or ln.startswith("--- FAIL") or "build failed" in ln:
            w("   " + ln[:200])
            break
    open(REPORT, "w", encoding="utf-8").write("\n".join(lines) + "\n")
    for c in console:
        print(c)
    print("BASELINE NOT GREEN -> abort")
    sys.exit(1)

# 对照 T：故意把一个测试文件弄坏，脚本必须认出"测试压根没跑起来"，而不是读成干净。
TST = os.path.join(ROOT, "cmd", "kaudit", "main_test.go")
t_orig = open(TST, "rb").read()
open(TST, "wb").write(t_orig + b"\nfunc brokenSyntax( {\n")
code_t, out_t = run(["test", "-p", "1", "-count=1", "./cmd/kaudit"])
open(TST, "wb").write(t_orig)
t_restored = open(TST, "rb").read() == t_orig
has_marker = broke(out_t)
w("对照 T（弄坏测试文件）：退出码 %d（该非 0）、认出坏了标记=%s、有 PASS/FAIL 判决行=%s（该 True / False）、修回一致=%s"
  % (code_t, has_marker, verdicts(out_t), t_restored))
control_t_ok = code_t != 0 and has_marker and not verdicts(out_t) and t_restored

sel = [n for n in NEEDLES if not FILTER or any(n[0].startswith(p) for p in FILTER)]
if FILTER and len(sel) != len(NEEDLES):
    w("!! 这一轮筛过针（只跑 %d 根，表里共 %d 根）—— 不能当整轮结论用" % (len(sel), len(NEEDLES)))
w("")

results = []
restored_total = 0
for name, old, new, named in sel:
    cur = open(SRC, "rb").read()
    if cur != original:
        w("!! %s 起手时树不干净 —— 上一根针没修回去，这一轮不算" % name)
        results.append((name, "TREE-DIRTY", ""))
        break
    if old not in cur:
        w("\n== %s  锚点找不到（main.go 的形状变了，这根针已失效）" % name)
        results.append((name, "ANCHOR-BAD", ""))
        continue
    open(SRC, "wb").write(cur.replace(old, new, 1))
    code, out = run(["test", "-p", "1", "-count=1", "-timeout", "120s", "-v", "./cmd/kaudit"])
    open(SRC, "wb").write(original)
    restored_total += 1
    if open(SRC, "rb").read() != original:
        w("!! %s 修回后字节不一致" % name)
        results.append((name, "RESTORE-BAD", ""))
        continue
    failed = testmap(out)
    if not verdicts(out):
        status = "BUILD-BROKE" if broke(out) else "TEST-BROKE(没跑成)"
    elif code == 0 and not failed:
        status = "VACUOUS"
    elif set(failed) == set(named):
        status = "RED-ONLY-NAMED"
    else:
        status = "RED-WIDER"
    w("\n== %s" % name)
    w("   点名该红：%s" % ", ".join(named))
    w("   实际报红：%s" % (", ".join(failed) if failed else "无"))
    if status == "RED-WIDER":
        extra = sorted(set(failed) - set(named))
        w("   多出点名的：%s" % (", ".join(extra) if extra else "少红了点名的"))
    w("   全量共红 %d 条" % len(failed))
    results.append((name, status, str(code)))

w("\n==== 汇总 ====")
for name, status, extra in results:
    w("%-34s %-14s %s" % (name, status, extra))
kinds = {}
for _, status, _ in results:
    kinds[status] = kinds.get(status, 0) + 1
w("")
w("针数 %d（表里 %d，本轮实际跑 %d）；分类：%s"
  % (len(NEEDLES), len(NEEDLES), len(results),
     "、".join("%s %d" % (k, v) for k, v in sorted(kinds.items()))))
w("取材方式：只改 cmd/kaudit/main.go 一个文件，每根针后按原字节修回（累计修回 %d 次）" % restored_total)
w("对照 0（干净树全过）：%s；对照 T（认得出测试没跑起来）：%s"
  % ("成立" if baseline_ok else "不成立", "成立" if control_t_ok else "不成立"))
bad = kinds.get("VACUOUS", 0) + kinds.get("ANCHOR-BAD", 0) + kinds.get("BUILD-BROKE", 0) + \
    kinds.get("TREE-DIRTY", 0) + kinds.get("RESTORE-BAD", 0)
if bad or not baseline_ok or not control_t_ok or len(results) != len(NEEDLES):
    w("!! 有坏针或对照不成立 —— 这一轮不绿，退出码 1")
    green = False
else:
    w("每条口头承诺都有一根针能把它抹红；无空针、无坏针、两根对照都成立。")
    green = True
open(REPORT, "w", encoding="utf-8").write("\n".join(lines) + "\n")

for c in console:
    print(c)
print("NEEDLES=%d %s" % (len(results),
                         " ".join("%s=%d" % (k, v) for k, v in sorted(kinds.items()))))
print("REPORT=" + REPORT)
sys.exit(0 if green else 1)
