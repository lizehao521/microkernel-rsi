# -*- coding: utf-8 -*-
"""按上一份基线的精确路径集重生成下一份，并把本轮新增的那把尺子显式加进去。
   打印：分母 / 对上 / 没对上 / 与上一份逐名比对（相同·变了·新增）"""
import hashlib, io, os, re, sys, datetime

SRC = r"C:\Users\Admin\Desktop\微内核\微内核\微内核"
LINE = re.compile(r"^([0-9a-f]{64})\s+\*?(\S+)\s*$")
prev_p = sys.argv[1]
new_p = sys.argv[2]
extra = sys.argv[3:]

def sha(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for b in iter(lambda: f.read(1 << 20), b""):
            h.update(b)
    return h.hexdigest()

prev = {}
order = []
with io.open(prev_p, encoding="utf-8") as f:
    for raw in f:
        m = LINE.match(raw.rstrip("\r\n"))
        if m:
            prev[m.group(2)] = m.group(1)
            order.append(m.group(2))

paths = list(order)
added = []
for e in extra:
    rel = e.replace("\\", "/")
    if rel not in prev:
        paths.append(rel)
        added.append(rel)

missing = [p for p in paths if not os.path.exists(os.path.join(SRC, p.replace("/", os.sep)))]
assert not missing, "上一份点名的文件现在不在了：%r" % missing[:5]

stamp = datetime.datetime.now().strftime("%m-%d %H:%M")
body = ["# 回滚基线 · 全部代码文件 · %s（由 %s 的精确路径集重生成，%d 条；本轮显式加入 %d 条：%s）" % (
    stamp, os.path.basename(prev_p), len(paths), len(added), "、".join(added) or "无"),
    "# 计数先数分母再吹 OK/FAILED；读这份件的人应当同时看到两个数"]
for p in paths:
    body.append("%s *%s" % (sha(os.path.join(SRC, p.replace("/", os.sep))), p))
with io.open(new_p, "w", encoding="utf-8", newline="\r\n") as f:
    f.write("\n".join(body) + "\n")

total = ok = 0
bad = []
with io.open(new_p, encoding="utf-8") as f:
    for raw in f:
        m = LINE.match(raw.rstrip("\r\n"))
        if not m:
            continue
        total += 1
        cur = sha(os.path.join(SRC, m.group(2).replace("/", os.sep)))
        ok += 1 if cur == m.group(1) else 0
        if cur != m.group(1):
            bad.append(m.group(2))

same = [p for p in order if prev[p] == sha(os.path.join(SRC, p.replace("/", os.sep)))]
moved = [p for p in order if p not in same]
print("BL %s | den=%d ok=%d fail=%d | vs %s: same=%d moved=%d added=%d" % (
    os.path.basename(new_p), total, ok, len(bad), os.path.basename(prev_p), len(same), len(moved), len(added)))
if moved:
    print("MOVED: %s" % ", ".join(moved))
if bad:
    print("BAD: %s" % ", ".join(bad))
sys.exit(0 if total == ok and not bad else 1)
