import os, re, shutil, subprocess, sys, io

# rsi_probe：往临时拷贝里逐根造破坏，要求点名的测试真的红。
# 锚点命中数 != 1 算空针；编译不过算坏针（崩不算红）；rc=0 算没咬（恒绿=尺子没牙）。
# 用法: python rsi_probe.py <go.exe> <本包目录> <临时工作目录> <报告输出路径>
GO, SRC, WORK, OUT = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]

MUT = [
    ("m1-R2恒等-指纹相同也放行", "rsi/rsi.go",
     [("if maps.Equal(artifactSet(prev), artifactSet(next)) {",
       "if false && maps.Equal(artifactSet(prev), artifactSet(next)) {")],
     ["TestR2SameArtifactsRefused"]),
    ("m2-R3放水也收", "rsi/rsi.go",
     [('rs = append(rs, fmt.Sprintf("R3: 判据 %q 被 %s —— 判据放水，改进不算数", d.ID, d.Change))',
       "_ = d")],
     ["TestR3Criteria"]),
    ("m3-R4不可判定也当证据", "rsi/rsi.go",
     [('refuse = append(refuse, fmt.Sprintf("R4: 指标 %q 第 %d 代 basis=%s 不可判定，不许当证据", r.Metric, m.Generation, kernel.Basis(r.Basis)))',
       "_ = r")],
     ["TestR4Basis"]),
    ("m4-R5无分母也判", "rsi/rsi.go",
     [('refuse = append(refuse, fmt.Sprintf("R5: 指标 %q 一侧带分母另一侧没交 —— 无分母比率不判", name))',
       "_ = name")],
     ["TestR5Improvement"]),
    ("m5-R6重记也收", "rsi/rsi.go",
     [('return fmt.Errorf("R6: %s 第 %d 代已有一份不同的 manifest 在链上，重记被拒", m.Producer, m.Generation)',
       "return nil")],
     ["TestR6Anchor", "TestCLIConflictingGenerationRefused"]),
    ("m6-目录声明未实现", "rsi/rsi.go",
     [('\t"R5": checkImprovement,\n', "")],
     ["TestCatalogMatchesImplementation"]),
    ("m7-判定恒等improved", "rsi/rsi.go",
     [("v := checkImprovement(prev, next)", "v := Verdict{State: Improved}")],
     ["TestR5Improvement", "TestJudgeImproved"]),
    ("m8-缺席塌成绿", "rsi/rsi.go",
     [('undec = append(undec, fmt.Sprintf("R4: 指标 %q 第 %d 代缺席（%s）—— 量不到，不判", r.Metric, m.Generation, r.WhyAbsent))',
       "_ = r")],
     ["TestR4Basis"]),
    ("m9-退出码恒0", "cmd/krsi/main.go",
     [("switch state {", "switch rsi.Improved {")],
     ["TestCLIFakeImprovementRefused", "TestCLIUndecidable", "TestCLINotImproved", "TestCLIR1NotAnchored"]),
    ("m10-R6重判也收", "rsi/rsi.go",
     [('return fmt.Errorf("R6: 第 %d 代已有一份不同的判定在链上，重判被拒", generation)',
       "return nil")],
     ["TestR6VerdictConflict"]),
    ("m11-R1坏件也进链", "rsi/rsi.go",
     [("if rs := checkStructure(prev, next); len(rs) > 0 {",
       "if rs := checkStructure(prev, next); false && len(rs) > 0 {")],
     ["TestR6AnchorRejectsR1"]),
    ("m12-R5拒收被核不了截胡", "rsi/rsi.go",
     [("if v.State == Refused {", "if false && v.State == Refused {")],
     ["TestPrecedenceRefusedBeatsUndecidable"]),
]

def go_test(cwd):
    p = subprocess.run([GO, "test", "./rsi/", "./cmd/krsi/", "-count=1"],
                       cwd=cwd, capture_output=True, text=True,
                       encoding="utf-8", errors="replace", timeout=600)
    return p.returncode, (p.stdout or "") + (p.stderr or "")

lines = []
def log(s):
    lines.append(s)
    print(s, flush=True)

# 正对照：未造破坏的拷贝必须先全绿，否则整轮作废（尺子/现场坏了，不算臂的读数）
ctrl = os.path.join(WORK, "control_pristine")
shutil.copytree(SRC, ctrl)
rc, out = go_test(ctrl)
if rc != 0:
    log("CONTROL_RED 正对照全绿失败，整轮作废：\n" + out)
    with io.open(OUT, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    sys.exit(1)
log("CONTROL_OK 正对照：未造破坏的拷贝 go test ./rsi/ ./cmd/krsi/ rc=0")

ok = miss = broken = empty = 0
for name, rel, edits, tests in MUT:
    d = os.path.join(WORK, re.sub(r"[^A-Za-z0-9]", "_", name))
    shutil.copytree(SRC, d)
    f = os.path.join(d, rel.replace("/", os.sep))
    with io.open(f, encoding="utf-8") as fh:
        src = fh.read()
    bad_anchor = False
    for old, new in edits:
        n = src.count(old)
        if n != 1:
            log("EMPTY %s 锚点命中 %d 处（要恰 1）—— 空针，不算读数" % (name, n))
            empty += 1
            bad_anchor = True
            break
        src = src.replace(old, new)
    if bad_anchor:
        continue
    with io.open(f, "w", encoding="utf-8", newline="") as fh:
        fh.write(src)
    rc, out = go_test(d)
    named = all(("--- FAIL: " + t) in out for t in tests)
    if rc != 0 and named:
        ok += 1
        log("OK %s 咬住了（点名 %s 都红）" % (name, ",".join(tests)))
    elif rc != 0 and ("[build failed]" in out or "cannot find" in out or "undefined:" in out or "syntax error" in out):
        broken += 1
        log("BROKEN %s 编译不过 —— 坏针，崩不算红\n%s" % (name, out[-2000:]))
    else:
        miss += 1
        log("MISS %s 没咬（rc=%d，点名测试红=%v）—— 恒绿=尺子没牙\n%s" % (name, rc, named, out[-2000:]))

total = len(MUT)
log("SUMMARY needles=%d ok=%d miss=%d broken=%d empty=%d" % (total, ok, miss, broken, empty))
verdict = (ok == total and miss == 0 and broken == 0 and empty == 0)
log("VERDICT %s" % ("ALL_BIT" if verdict else "NOT_ALL_BIT"))
with io.open(OUT, "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + "\n")
sys.exit(0 if verdict else 1)