import io, os, re, subprocess, sys

# celltable_audit.py —— contract.md 第 3 节那张格位表的**一致性检查**。
#
# 为什么要有这一份：那份文档写着「键名与字段路径由 Cells() **机械导出**，不是手抄」，
# 而它给的再生成办法是「在 %TEMP%\kernel-attach 下的 cellcount 子模块里 go run celltable.go」。
# 那个生成器**不在仓库里**，%TEMP% 也只活在一台机器上 —— 于是「机械导出不漂」这句话
# 今天其实是**不可执行的**：上一次是手动跑的，之后每一条改动的格子都没有东西会红。
# 这正是本包最恨的那一族：一个承诺由散文守着。
#
# 这一份脚本把那个承诺变成能重跑的判据：它不复制第二份真相 ——
# 实读那侧**只读 `go run ./cmd/kcheck` 第 5 节自己印的输出**（与闸门同一份 Cells()），
# 文档那侧解析 contract.md 的表；两边逐格对 键名 / sides / 字段路径 / 空样本出处。
#
# 三态（与本包其余判据同一条纪律）：
#   退出码 0 = 表与代码一致
#   退出码 1 = 漂了（逐格点名）
#   退出码 2 = 核不了（没有 go、闸门没跑成、或表解析不出行）—— 这不是合格

GO = sys.argv[1] if len(sys.argv) > 1 else "go"
SRC = sys.argv[2] if len(sys.argv) > 2 else "."
OUT = sys.argv[3] if len(sys.argv) > 3 else os.path.join(SRC, "报告", "格位表一致性-最新.txt")
DOC = os.path.join(SRC, "contract.md")

# 控制台是什么编码不由我们决定（Windows 上它多半是 GBK，一个 ✓ 就能让脚本崩在打印那一行，
# 而那会**被读成检查没过**）。所以细节一律写进 UTF-8 报告，终端只留一行纯 ASCII ——
# 这与 tasks/probe/kprobe.py 的做法是同一条。
#
# 输出件**不在起跑时打开**（这里原先是 `io.open(OUT,"w")`）：起跑就截断，等于"没跑成"也能盖掉凭据。
# 10-19 我裸跑了一次不带 GO 参数的，它把 `报告/格位表一致性-最新.txt` 那三行 OK 读数换成了两句「核不了」，
# 而退出码照样是 2、终端那行看着像"只是没跑成" —— 凭据被一次没跑成抹掉，比报红更坏，所以落点由结论决定：
# 真跑出读数（OK／DRIFT）才写 最新；核不了写到 `<最新>.不可核`，谁也不会把它读成一轮的结论。
lines = []
def w(s):
    lines.append(s)
NL = chr(10)


def emit(dest):
    f = io.open(dest, "w", encoding="utf-8")
    f.write(NL.join(lines) + NL)
    f.close()
    return dest


def flush(code):
    dest = emit(OUT)
    print("CELLTABLE AUDIT status=%s keys=%d cells=%d rows=%d drift=%d -> %s"
          % (code, len(live), cells, len(rows), len(bad) + len(ghosts),
             dest.encode("ascii", "replace").decode("ascii")))
    sys.exit({"OK": 0, "DRIFT": 1, "UNVERIFIABLE": 2}[code])


live = {}
rows = []
bad, ghosts, cells = [], [], 0


def die_code2(msg):
    w("— 核不了：%s" % msg)
    w("  这不是合格，也不是不合格。修好这一路再重跑。")
    dest = emit(OUT + ".不可核")
    print("CELLTABLE AUDIT status=UNVERIFIABLE detail=%s -> %s"
          % (msg.encode("ascii", "ignore").decode()[:70],
             dest.encode("ascii", "replace").decode("ascii")))
    sys.exit(2)


def found_go(path):
    # 给了分隔符就按路径查，否则按 PATH 查（与 isolate.go 里 GOBIN_EXE 然后 LookPath 同一次序）。
    if os.sep in path or (os.altsep and os.altsep in path):
        return os.path.exists(path)
    for d in env_search_path():
        if os.path.exists(os.path.join(d, path)) or os.path.exists(os.path.join(d, path + ".exe")):
            return True
    return False


def env_search_path():
    return [d for d in os.environ.get("PATH", "").split(os.pathsep) if d]


if not found_go(GO):
    die_code2("找不到 go 可执行文件（%s）。把完整路径作为第一个参数传进来。" % GO)
if not os.path.exists(DOC):
    die_code2("读不到 %s" % DOC)

env = dict(os.environ)
env["PATH"] = env.get("PATH", "") + os.pathsep + os.path.dirname(GO)
try:
    p = subprocess.run([GO, "run", "./cmd/kcheck"], cwd=SRC, env=env,
                       capture_output=True, text=True, encoding="utf-8", errors="replace")
except Exception as e:
    die_code2("闸门没跑成：%s" % e)

out = (p.stdout or "") + (p.stderr or "")
if p.returncode != 0:
    # 闸门自己没过，那它印的键名表就不可信 —— 别拿一份红树上的读数去对表。
    die_code2("go run ./cmd/kcheck 退出码 %d（闸门没过时这张表本就不可信）" % p.returncode)
if "== 5." not in out:
    die_code2("闸门输出里找不到第 5 节（节名或节号漂了，这份脚本得跟着改）")

sec5 = out.split("== 5.")[1].split("== 6.")[0]
live, side = {}, None
for ln in sec5.splitlines():
    s = ln.strip()
    if s in ("输入侧", "输出侧"):
        side = "input" if s == "输入侧" else "output"
        continue
    m = re.match(r"\s+([a-z_0-9]+)\s+←\s+(\S+)\s+｜\s+(\S+)", ln)
    if m and side:
        k, f, b = m.groups()
        d = live.setdefault(k, {"sides": set(), "fields": set(), "basis": {}})
        d["sides"].add(side)
        d["fields"].add(f)
        d["basis"][side] = b
if not live:
    die_code2("从第 5 节解析出 0 格（格式变了，不是表漂了）")

doc = io.open(DOC, encoding="utf-8").read()
if "## 3. 格位表" not in doc:
    die_code2("contract.md 里找不到「## 3. 格位表」")
tbl = doc.split("## 3. 格位表")[1].split("**presence_policy")[0]
rows = re.findall(r"^\| `([a-z_0-9]+)` \| (both|input|output) \| `([^`]+)` \| ([a-z_]+) \|", tbl, re.M)
if not rows:
    die_code2("解析不出表里的任何一行（表头或分隔符变了）")

WANT = {"both": {"input", "output"}, "input": {"input"}, "output": {"output"}}
bad, doc_keys = [], set()
for k, sides, field, basis in rows:
    doc_keys.add(k)
    L = live.get(k)
    if not L:
        bad.append((k, "文档有、实读没有 —— 幽灵行"))
        continue
    if L["sides"] != WANT[sides]:
        bad.append((k, "sides：实读 %s / 文档 %s" % (sorted(L["sides"]), sides)))
    if field.strip() not in L["fields"]:
        bad.append((k, "字段路径：实读 %s / 文档 %s" % (sorted(L["fields"]), field)))
    for sd, b in sorted(L["basis"].items()):
        # 一格在两侧可能报**不同**的出处（两侧是不同的结构体）。
        # 文档只有一列，所以只有当两侧一致时才比对；不一致时要求文档显式写 something，
        # 并把这一格报进「文档一列装两个量」那一栏 —— 那正是规矩二那一族。
        if b != basis:
            bad.append((k, "空样本出处(%s)：实读 %s / 文档 %s" % (sd, b, basis)))
ghosts = sorted(set(live) - doc_keys)

cells = sum(len(v["sides"]) for v in live.values())
w("实读 %d 个唯一键名 / %d 格；contract.md 第 3 节 %d 行" % (len(live), cells, len(rows)))
w("（实读那一侧不另存一份真相：就地解析 go run ./cmd/kcheck 第 5 节自己印的输出）")
for k in ghosts:
    w("✗ 实读里有、文档表里没有的键名：%s" % k)
for k, why in bad:
    w("✗ %-22s %s" % (k, why))
if bad or ghosts:
    w("")
    w("✗ 表与代码不一致：%d 处对不上、%d 个漏记键名 —— 按 contract.md 自己的规矩：以代码为准，改这份文件"
      % (len(bad), len(ghosts)))
    flush("DRIFT")
w("✓ 逐格对得上：键名、sides、字段路径、空样本出处 四项全一致")
flush("OK")
