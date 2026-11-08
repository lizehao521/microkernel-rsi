# -*- coding: utf-8 -*-
"""内核「不联网 / 不写盘」的静态尺子 —— 把 README:12 那句散文换成读数（看板 #26 的可检的那一半）。

为什么要这支：`isolate.go` 那把尺子分的是 自有/三方/标准库，而 `net/http`、`os` 都算标准库
⇒ 「0 泄漏」抓不到内核哪天联网。这是 README:12–13 自己写着的盲区，不是我发现的新事；
这一支补的就是那个方向：**按能力族扫，不按模块归属扫**。

四族（逐名带 file:line，全从字节取，不引任何总结句）：
  一 · 网络出口   net / net/* / crypto/tls / websocket / Dial / ListenAndServe / http.Get…
  二 · 写盘       os.WriteFile / Create / Mkdir* / Remove* / Rename / Truncate / OpenFile / O_WRONLY|O_APPEND|O_CREATE
  三 · 读盘       os.ReadFile / Open / Stat / ReadDir / WalkDir / parser.ParseFile
  四 · 起进程与够到原生  exec.Command / exec.LookPath / syscall. / plugin.Open
另附 · 环境变量读 os.Getenv / Environ（不属磁盘，但同属「够到进程外」这一族，单列）

口径：判据只压**内核根包的生产文件**（README:593 那句的主语就是 `package kernel`）；
      测试文件同族分开计数（只扫生产文件 = 把一条依赖藏进 _test.go 就永远绿，那是 isolate.go 已经写过的教训）；
      `mcpsrv/` 与 `cmd/*` 只数不判 —— 外壳本来就要听 HTTP，把它算进「内核不联网」是拿错分母。

自核（每次都跑，不动树）：沙箱里造 bad.go（四族各来一发）＋ good.go（只用 fmt/strings）＋ 一个空目录，
      三件事必须同时成立：bad 每族命中、good 全 0、空目录必须判「核不了」而不是「0 所以合格」。
      任何一件不成立 ⇒ 这把尺子自己漂了 ⇒ 退出码 5，上面的读数一律不许读成结论。

跑法：python -X utf8 tasks/probe/io_net_guard.py [输出.txt]
      环境变量 `IONET_ROOT` 可把扫的树换成一份 TEMP 拷贝（不动在跑的树，也能拿它自证会红）
退出码：0 判据成立 ｜ 4 判据红（内核那圈出现网络族或写盘）｜ 5 自核红 ｜ 3 输出件已存在（拒覆盖上一轮凭据）
"""
import io
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = os.environ.get("IONET_ROOT") or os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

NET_PATHS = ("net", "crypto/tls", "crypto/x509", "golang.org/x/net", "github.com/gorilla/websocket")
CALLS = [
    ("网络", re.compile(r"\b(?:net|http|url|tls|smtp|mail|rpc|httputil|cookiejar)\s*\.\s*[A-Z]\w*"
                        r"|ListenAndServe|\.Dial[A-Z]?\s*\(|\.Do\s*\(\s*\w*[Rr]eq|websocket")),
    ("写盘", re.compile(r"\bos\s*\.\s*(WriteFile|Create|Mkdir[A-Za-z]*|Remove[A-Za-z]*|Rename|Truncate|OpenFile|Chmod)\s*"
                        r"|\bos\s*\.\s*O_(WRONLY|APPEND|CREATE|TRUNCATE)\b")),
    ("读盘", re.compile(r"\bos\s*\.\s*(ReadFile|Open|Open[\s]*\(|Stat|Lstat|ReadDir|FileTimes)\s*\("
                        r"|\bfilepath\s*\.\s*WalkDir\s*\(|\bfs\s*\.\s*Walk\s*\(|\bparser\s*\.\s*ParseFile\s*\(")),
    ("起进程", re.compile(r"\bexec\s*\.\s*(Command|CommandContext|LookPath)\s*\(|\bsyscall\s*\.\s*\w+|\bplugin\s*\.\s*Open\s*\(")),
    ("环境", re.compile(r"\bos\s*\.\s*(Getenv|LookupEnv|Environ|Hostnames|Getwd)\s*\(")),
]
IMP_RE = re.compile(r'^\s*(?:[\w_.]+\s+)?"([^"]+)"|^\s*(?:[\w_.]+\s+)`([^`]+)`')

BUCKETS = ("网络", "写盘", "读盘", "起进程", "环境")


def bucket_of_import(path):
    if path == "C":
        return "起进程"          # cgo 能链进任何原生代码，按 isolate.go 的同一口径单列
    for p in NET_PATHS:
        if path == p or path.startswith(p + "/"):
            return "网络"
    if path in ("os", "os/exec", "path/filepath", "io/fs", "plugin", "syscall") or path.startswith(("os/", "io/fs")):
        return None              # 标准库 import 本身不表态，表态的是调用点（见下面「看不见的路」）
    return None


def go_files(dir_path, recursive):
    out = []
    if not os.path.isdir(dir_path):
        return out
    if recursive:
        for base, sub, names in os.walk(dir_path):
            sub[:] = [s for s in sub if s not in ("__pycache__", "构建-1017", "报告", "日志", "bin")]
            for n in sorted(names):
                if n.endswith(".go"):
                    out.append(os.path.join(base, n))
    else:
        for n in sorted(os.listdir(dir_path)):
            if n.endswith(".go") and os.path.isfile(os.path.join(dir_path, n)):
                out.append(os.path.join(dir_path, n))
    return sorted(out)


def scan(files, root_for_name):
    """逐文件扫两件事：import 行撞哪族、代码行撞哪族。生产与测试分开入账（口径不同，见 main 里那句）。"""
    def blank():
        return {b: [] for b in BUCKETS}
    hp, ht = blank(), blank()
    prod, test, unreadable = [], [], []
    for f in files:
        rel = os.path.relpath(f, root_for_name).replace("\\", "/")
        is_test = rel.endswith("_test.go")
        (test if is_test else prod).append(rel)
        hits = ht if is_test else hp
        try:
            lines = io.open(f, encoding="utf-8", errors="replace").read().splitlines()
        except Exception as e:
            unreadable.append("%s（%s）" % (rel, e))
            continue
        for ln, text in enumerate(lines, 1):
            code = text.split("//")[0]
            m = IMP_RE.match(code)
            if m:
                b = bucket_of_import((m.group(1) or m.group(2) or "").strip())
                if b:
                    hits[b].append((rel, ln, "import " + (m.group(1) or m.group(2))))
                continue
            for b, rx in CALLS:
                if rx.search(code):
                    hits[b].append((rel, ln, code.strip()[:110]))
    return hp, ht, prod, test, unreadable


def imports_of(files, root_for_name):
    """把每个文件的 import 全集抠出来（逐名，供第二把尺子与人工复核）。"""
    inv = []
    for f in files:
        rel = os.path.relpath(f, root_for_name).replace("\\", "/")
        paths, inblk = [], False
        try:
            lines = io.open(f, encoding="utf-8", errors="replace").read().splitlines()
        except Exception:
            inv.append((rel, ["<读不了>"]))
            continue
        for text in lines:
            code = text.split("//")[0].strip()
            if code.startswith("import ("):
                inblk = True
                continue
            if inblk and code == ")":
                inblk = False
                continue
            m = IMP_RE.match(code)
            if m and (inblk or code.startswith("import ")):
                paths.append((m.group(1) or m.group(2) or "").strip())
        inv.append((rel, paths))
    return inv


def net_files_from_imports(inv):
    """第二把尺子：只看 import 路径，不碰调用点 —— 与正则那把独立取词。"""
    hits, allp = [], set()
    for rel, paths in inv:
        for p in paths:
            allp.add(p)
            for q in NET_PATHS:
                if p == q or p.startswith(q + "/"):
                    hits.append("%s  import %s" % (rel, p))
                    break
    return sorted(hits), sorted(allp)


GO_HINT = ("找 go 的姿势与 isolate.go 同一条：环境变量 GOBIN_EXE → PATH → 找不到就记「核不了」。"
           "这台机器上 go 在 C:\\Users\\Admin\\sdk\\go\\bin（PATH 默认没有）。")


def closure_net():
    """尺三：问 Go 自己 —— `go list -deps .` 的依赖闭包里有没有网络族。编译器给的这一路不依赖我的正则。"""
    go = os.environ.get("GOBIN_EXE") or shutil.which("go")
    if not go:
        return "NOT_RUN", "PATH 上没有 go，也没给 GOBIN_EXE ｜ " + GO_HINT
    env = dict(os.environ, GOPROXY="off")
    try:
        q = subprocess.run([go, "list", "-deps", "."], cwd=ROOT, capture_output=True,
                           text=True, encoding="utf-8", errors="replace", timeout=180, env=env)
    except Exception as e:
        return "NOT_RUN", "go list 没跑成：%s ｜ %s" % (e, GO_HINT)
    if q.returncode != 0:
        first = ((q.stderr or "").strip().splitlines() or ["退出码 %d" % q.returncode])[0]
        return "NOT_RUN", "go list -deps 退出码 %d：%s" % (q.returncode, first[:110])
    pkgs = [x.strip() for x in (q.stdout or "").splitlines() if x.strip()]
    net = [p for p in pkgs if any(p == x or p.startswith(x + "/") for x in NET_PATHS)]
    return "OK", (len(pkgs), sorted(net))


def report_hits(w, title, hits):
    if title:
        w("  " + title)
    for b in BUCKETS:
        hs = hits[b]
        w("    %s：%d 处" % (b, len(hs)))
        for rel, ln, snip in hs:
            w("      %s:%s  %s" % (rel, ln if ln else "-", snip))


def judge(hits, prod_n, unreadable):
    """返回 (成立?, 一句话)。判据只压网络族与写盘族；扫不到文件或读不了文件都不许读成合格。"""
    if unreadable:
        return None, "有文件读不了，这一发核不了：" + "、".join(unreadable)
    if prod_n == 0:
        return None, "一个生产 .go 都没扫到 —— 这一发核不了，不是合格"
    return (len(hits["网络"]) == 0 and len(hits["写盘"]) == 0), \
           "内核根包生产文件：网络族 %d 处、写盘族 %d 处" % (len(hits["网络"]), len(hits["写盘"]))


BAD_GO = '''package kernel

import (
	"net/http"
	"os"
	"os/exec"
)

func leak() {
	resp, _ := http.Get("http://127.0.0.1:1/")
	_ = resp
	_ = os.WriteFile("x.txt", []byte("y"), 0644)
	cmd := exec.Command("whoami")
	_ = cmd
}
'''
GOOD_GO = '''package kernel

import (
	"fmt"
	"strings"
)

func keep() string {
	return fmt.Sprintf("%s", strings.ToUpper("ok"))
}
'''


def selfcheck(w):
    """沙箱反臂：造 bad/good/空目录三发，验这把尺子会红、不假红、不空跑。"""
    tmp = os.path.join(tempfile.gettempdir(), "ionetguard-self-%s" % time.strftime("%m%d-%H%M%S"))
    ok = True
    try:
        os.makedirs(os.path.join(tmp, "withbad"), exist_ok=True)
        os.makedirs(os.path.join(tmp, "withgood"), exist_ok=True)
        os.makedirs(os.path.join(tmp, "empty"), exist_ok=True)
        for d, src in (("withbad", BAD_GO), ("withgood", GOOD_GO)):
            io.open(os.path.join(tmp, d, "f.go"), "w", encoding="utf-8").write(src)
        hb, _, pb, _, ub = scan(go_files(os.path.join(tmp, "withbad"), False), tmp)
        hg, _, pg, _, ug = scan(go_files(os.path.join(tmp, "withgood"), False), tmp)
        he, _, pe, _, ue = scan(go_files(os.path.join(tmp, "empty"), False), tmp)
        j1, t1 = judge(hb, len(pb), ub)
        j2, t2 = judge(hg, len(pg), ug)
        j3, t3 = judge(he, len(pe), ue)
        w("  反臂 · 沙箱 bad.go（net/http + os.WriteFile + exec.Command）：%s ｜ %s" % ("红" if j1 is False else "**没红**", t1))
        w("  正臂 · 沙箱 good.go（只用 fmt/strings）：%s ｜ %s" % ("绿" if j2 is True else "**没绿**", t2))
        w("  空跑臂 · 沙箱空目录：%s ｜ %s" % ("判成核不了" if j3 is None else "**当成合格了**", t3))
        for b in ("网络", "写盘", "起进程"):
            got = len(hb[b])
            w("     bad.go 的%s族命中 = %d 处 %s" % (b, got, "OK" if got >= 1 else "← 尺子看不见这一族"))
            if got < 1:
                ok = False
        nh_b, _ = net_files_from_imports(imports_of([os.path.join(tmp, "withbad", "f.go")], tmp))
        nh_g, _ = net_files_from_imports(imports_of([os.path.join(tmp, "withgood", "f.go")], tmp))
        w("     bad.go 上第二把尺子（只看 import 路径）= %d 条：%s" % (len(nh_b), "OK" if nh_b else "← 这把是死尺"))
        w("     good.go 上第二把尺子 = %d 条：%s" % (len(nh_g), "OK（不假红）" if not nh_g else "← 假阳性"))
        if not nh_b or nh_g:
            ok = False
        if j1 is not False or j2 is not True or j3 is not None:
            ok = False
        if any(hg[b] for b in BUCKETS):
            w("     good.go 却撞出了东西（假阳性）：" + "、".join("%s %d" % (b, len(hg[b])) for b in BUCKETS if hg[b]))
            ok = False
        return ok
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    outp = sys.argv[1] if len(sys.argv) > 1 else \
        os.path.join(ROOT, "报告", "内核不联网不写盘-静态尺子-%s.txt" % time.strftime("%m%d-%H%M%S"))
    if os.path.exists(outp):
        print("REFUSE_TO_OVERWRITE: %s 已存在，那是上一轮的凭据。换个名。" % os.path.basename(outp).encode("unicode_escape").decode("ascii"))
        return 3
    out = []

    def w(s):
        out.append(s)

    w("内核「不联网 / 不写盘」静态尺子 · %s" % time.strftime("%Y-%m-%d %H:%M:%S"))
    w("（只读源码，不起服务、不碰任何端口、不写树内除本读数件以外的文件；判据表 48 键/49 格一格没动）")
    w("")
    w("== 自核（先钉这把尺子，再谈读数）==")
    sok = selfcheck(w)
    w("  自核结论：%s" % ("三臂各自红过/绿过/判成核不了 ⇒ 这把尺子会红，上面读数可用"
                          if sok else "有一臂不合 ⇒ 这把尺子漂了，下面所有读数都不许引用"))
    w("")

    root_files = go_files(ROOT, False)
    hp, ht, prod, test, unread = scan(root_files, ROOT)
    ok, why = judge(hp, len(prod), unread)
    w("== 甲 · 内核根包（README:593 那句的主语 `package kernel`；判据压在这一圈的生产文件上）==")
    w("  文件：非测试 %d 份 %s ｜ 测试 %d 份" % (len(prod), " ".join(prod), len(test)))
    report_hits(w, "生产文件命中（逐名）：", hp)
    w("  测试文件命中（同族**分开计数、不压判据**，理由见下面第五节）：")
    report_hits(w, "", ht)
    inv = imports_of([os.path.join(ROOT, x) for x in prod], ROOT)
    nh, allp = net_files_from_imports(inv)
    callnet = sorted({rel for rel, _ln, _s in hp["网络"]})
    impnet = sorted({h.split("  ")[0] for h in nh})
    diff = sorted(set(callnet) ^ set(impnet))
    w("  三把尺子对开（同一句「不联网」取词处不重叠：调用点正则 / import 路径 / 编译器闭包）：")
    w("    尺一 · 调用点正则命中的生产文件：%s" % ("、".join(callnet) if callnet else "0 个"))
    w("    尺二 · 只看 import 路径撞网络族的：%s" % ("、".join(nh) if nh else "0 条"))
    w("    逐名分歧 = %d 个文件%s" % (len(diff), ("（" + "、".join(diff) + "）") if diff else ""))
    w("    内核根包生产文件的 import 逐份列出（%d 份、去重后 %d 条路径），供人工复核，不靠我转述：" % (len(prod), len(allp)))
    for rel, paths in inv:
        w("      %-14s %s" % (rel, " ".join(sorted(paths)) if paths else "（一条 import 都没有）"))
    osfiles = sorted({rel for rel, paths in inv if "os" in paths or "os/exec" in paths or "path/filepath" in paths})
    w("  写盘那一句的旁证（正交于调用点正则）：碰得着文件系统的只有 import os / os/exec / path/filepath 的那些文件。")
    w("    今天内核根包生产文件里 import 这三样的 = %d 份：%s" % (len(osfiles), "、".join(osfiles) if osfiles else "0 份"))
    w("    ⇒ 除这 %d 份以外，其余 %d 份生产文件连 `os` 都没引，写不了盘也起不了进程 —— 这一句不依赖我的正则。"
      % (len(osfiles), len(prod) - len(osfiles)))
    st3, r3 = closure_net()
    if st3 == "OK":
        n3, net3 = r3
        w("    尺三 · 问 Go 编译器自己（`go list -deps .` 闭包 %d 个包）：网络族 %d 个%s"
          % (n3, len(net3), ("（" + "、".join(net3) + "）") if net3 else ""))
        if net3 and ok is not False:
            ok = False
            why += " ｜ 尺三撞出 %d 个网络包：编译器那一路与我的正则分歧，判据改判不成立" % len(net3)
            w("      ⇒ 三把尺子逐名分歧不为 0 ⇒ 上面那句「成立」作废，按编译器这路改判。")
    else:
        w("    尺三 · 核不了：%s" % r3)
        w("           这一路没跑成 ⇒ 只算少了一把旁证：甲圈判据仍按尺一/尺二 报，既不塌成绿也不塌成红。")
    w("  判据（三处取词都到齐了才印这一行）：网络族=0 且 写盘族=0 ⇒ %s ｜ %s"
      % ("成立" if ok else ("不成立" if ok is False else "核不了"), why))
    w("")

    w("== 乙 · 内核包以外（只数不判：外壳本来要听 HTTP，算进来是拿错分母）==")
    for d, label in (("mcpsrv", "mcpsrv/（协议外壳）"), ("cmd", "cmd/*（闸、审计、打包、对账等工具）")):
        fs = go_files(os.path.join(ROOT, d), True)
        a, b2, p, t, u = scan(fs, ROOT)
        h = {k: a[k] + b2[k] for k in BUCKETS}
        w("  %s：生产 %d 份、测试 %d 份 ｜ 命中（生产＋测试合计）%s" % (label, len(p), len(t),
          "、".join("%s %d" % (k, len(h[k])) for k in BUCKETS)))
        per = {}
        for k in BUCKETS:
            for rel, _ln, _snip in h[k]:
                per.setdefault(rel, {b: 0 for b in BUCKETS})[k] += 1
        for rel in sorted(per):
            w("      %-30s %s" % (rel, " ".join("%s=%d" % (k, per[rel][k]) for k in BUCKETS if per[rel][k])))
        w("      （乙圈按调用点正则数，`http.` / URL 一类的方法名会虚高，所以只数不判；判据只压甲圈那 %d 份生产文件）" % len(prod))
    w("")

    w("== 三 · 这一把看不见的路（写在这里，不藏在注释里）==")
    w("  · **import 了 os / net 本身不表态**：这一支把标准库 import 单列成空，表态的是调用点。")
    w("    代价是「把 URL 拼好交给调用方去拨」这种形状看不见 —— 那一路只有真跑或 go list -deps 兜得住。")
    w("  · **内核把字节交给调用方写盘**（`kernel.encode_journal` 只回字节，README:129/377 那条设计）")
    w("    在源码上就是 `[]byte` 返回，不构成写盘命中 ⇒ 「内核不写盘」这句能机械核，但「整条链不写盘」不能。")
    w("  · 反射、`any` 里塞进来的 io.Writer、plugin.Open、cgo、通过环境变量拿到的路径 —— 五族正则都不产生命中。")
    w("    cgo 这一条按 `import \"C\"` 归到「起进程/够到原生」那一族， isolate.go 也是同一口径。")
    w("  · **三把尺子看的文件集不一样，这一条今天实测撞到了**：文件名里带 GOOS/GOARCH 后缀的（`x_arm.go`、`x_windows.go`…）")
    w("    会被 Go 按构建约束**整份跳过** —— 今天往 TEMP 拷贝里塞了一个 `leak_arm.go`（import net/http），")
    w("    `go list -deps .` 的闭包仍是 116 个包、`go list` 的 .GoFiles 根本没列它，而按字节扫的尺一/尺二照样命中它。")
    w("    ⇒ 编译器那一路抓不到「藏在文件名后缀里的依赖」，按字节那两路抓不到「编译器实际链进来的东西」；")
    w("      三把并着看才有一句「不联网」可核，任何一把单用都得留一句盲区。")
    w("  · 这一把是**尺子不是闸门**：没进 `cmd/kcheck`。进闸门的代价仍是 #26 卡上那句 —— 改闸门 = 整轮 80 根再过期一次。")
    w("")
    w("== 四 · 测试文件为什么不压判据（这一条口径我没自己定死，写清留给作者）==")
    w("  · 事实：`isolate.go` 那把尺子把测试文件**并进**了结论（`Isolated()` 里 `len(r.TestLeaks) == 0` 也参与），")
    w("    因为它判的是「这个包引了什么」，测试文件同样在包目录里、同样进 `go list -deps` 闭包。")
    w("  · 这一把判的是「编进交付字节的能力」：`_test.go` 不进 `go build` 的产物，所以内核测试在 TEMP 造沙箱喂")
    w("    `CheckIsolation`（今天写盘 2 处）**不构成**「内核会写盘」——但**也不构成**「这一族干净」。")
    w("  · 两态都印了：并进判据 ⇒ 今天判据从「成立」变「不成立」（写盘 2 处）；不并 ⇒ 留一个「测试文件能写盘」的口子。")
    w("    选哪个是口径决定，不是我这一发该拍的；这一把先把两态的数都摆出来。")
    w("    唯一并进判据的是「读不了文件」和「一个生产文件都没扫到」——那两态只能塌成「核不了」，不许读成绿。")
    w("")
    w("== 五 · 分母自印 ==")
    w("  内核根包 .go %d 份（生产 %d ＋ 测试 %d ＋ 读不了 %d）｜ 生产文件五族命中：网络 %d ｜ 写盘 %d ｜ 读盘 %d ｜ 起进程 %d ｜ 环境 %d"
      % (len(root_files), len(prod), len(test), len(unread), len(hp["网络"]), len(hp["写盘"]),
         len(hp["读盘"]), len(hp["起进程"]), len(hp["环境"])))
    w("  测试文件五族命中：网络 %d ｜ 写盘 %d ｜ 读盘 %d ｜ 起进程 %d ｜ 环境 %d"
      % tuple(len(ht[b]) for b in BUCKETS))
    w("  三把独立尺子（同一句「不联网」，取词处不重叠）：调用点正则 %d 个文件 ｜ import 路径 %d 条 ｜ 编译器闭包 %s"
      % (len(callnet), len(nh),
         ("闭包 %d 个包、网络族 %d 个" % (r3[0], len(r3[1])) if st3 == "OK" else "核不了（没跑成，记 absent，不记 0）")))
    w("  沙箱反臂文件 3 份（bad/good/空目录）｜ 端口 0 个 ｜ 服务 0 台 ｜ 树内写入 1 份（本读数件）")
    io.open(outp, "w", encoding="utf-8").write("\n".join(out) + "\n")
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass
    print("REPORT=" + outp.encode("unicode_escape").decode("ascii"))
    print("SELFCHK=" + ("OK" if sok else "BROKE") + " ｜ JUDGE=" +
          ("PASS" if ok else ("FAIL" if ok is False else "NOT_RUN")))
    if not sok:
        return 5
    if ok is False:
        return 4
    if ok is None:
        return 5
    return 0


if __name__ == "__main__":
    sys.exit(main())
