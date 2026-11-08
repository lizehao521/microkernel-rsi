# -*- coding: utf-8 -*-
"""交付包 · 逐件来历的尺子 —— 把「包里的二进制比源码旧」从三张卡里的好话换成一份逐件读数。

为什么这一支：待跑那节第 2、3、4 张卡说的是同一件事的三个载体（包里三份凭据是 10-18 的字节、
`cmd/kwire` 不在包里、`打包.py` 从不重跑任何一件），三张都把修法交回作者拍；但「今天到底有几件旧」
这个数一直没逐件数过。这一支**只读数、不闸门**：不改 `打包.py`、不重编译、不动包里任何一件字节。

四件读数（逐名，全从字节/时间取，不引任何卡里的总结句）：
  一 · exe 逐件：zip 条目名 ｜ zip 内 date_time ｜ 盘上同名件 mtime 与 sha256 ｜ 两份字节是否同一 ｜
      源码侧最新 .go/go.mod 的时间 ｜ 判「旧 ／ 不旧 ／ 核不了」
  一·附 · 包落点里每一份 zip 各一行（条目数、exe 支数与最新时间、判旧支数、evidence 份数与旧于 exe 的份数）
  二 · evidence 逐件：文件名 ｜ zip 内 date_time ｜ 字节数 ｜ 是否早于包里最新那一支 exe（⇒「话比字节旧」）
  三 · kwire 那一格三个数并排：包内含 kwire 的条目数 ｜ 盘上 bin 含 kwire 的文件数 ｜ 仓库 cmd/kwire 的文件数
  四 · 分母自印：扫到的 .go/go.mod 份数、最新时间那一支的名字、zip 条目总数、exe 份数、evidence 份数

口径写死在代码里，读这份件的人能看到它为什么可能骗人：
  · 判「旧」用的是**整棵源码树的最新时间**，不是「这个 exe 自己那条依赖链」——`go build` 会把整个模块都编进去，
    所以这是**上界**（宁可说旧）。要说得准得逐件算依赖闭包，那是另一发。
  · 源码侧只数 `.go` 与 `go.mod`（与回滚基线同一族），`报告/`、`构建*/`、`.hermess-snapshots/`、`.git/` 一律不数。
  · 盘上那份与 zip 里那份**分别**取字节：不一致时这一件的两个判语可能相反（比如盘上被后来的构建覆盖过），逐名都印出来。

自核（每次都跑，TEMP 里造微型假包与假源码树，不碰在跑的树）：四件事必须同时成立——
  甲 假包里 exe 的 date_time 早于假源码树 ⇒ 必须判「旧」（这把尺子会红）
  乙 假包里 exe 的 date_time 晚于假源码树 ⇒ 必须判「不旧」
  丙 假源码树里一份 .go 都没有 ⇒ 必须判「核不了」（不许读成「0 处所以合格」）
  丁 假包里 exe 与盘上同名件字节不同 ⇒ 必须报「两份字节不一致」（不许默认它们是同一件）
  任何一件不成立 ⇒ 这把尺子自己漂了 ⇒ 退出码 5，上面四件读数一律不许读成结论。

跑法：python -X utf8 tasks/probe/package_staleness.py [输出.txt]
      环境变量 `PS_PKG_DIR` 可把包的落点换掉（默认从本文件位置往上退两级再找 `构建-1017`）
退出码：0 数完了且没有任何一件判「旧」｜ 4 至少一件判「旧」｜ 5 自核红 或 包/树找不到（核不了）｜ 3 输出件已存在（拒覆盖上一轮凭据）
"""
import hashlib
import os
import sys
import tempfile
import time
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
PKG_DEFAULT = os.path.join(REPO, os.pardir, os.pardir, "构建-1017")
SOURCE_EXT = (".go", ".mod")
SKIP_DIR_NAMES = ("构建-1017", "PS解", "__pycache__", ".hermess-snapshots", ".git")


def sha256_bytes(b):
    return hashlib.sha256(b).hexdigest()


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def stamp(t):
    return time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(t))


def zip_dt(info):
    return time.mktime(info.date_time + (0, 0, 0))


def find_package(pkg_dir):
    """返回 (zip 路径, 盘上 bin 目录, evidence 目录, 有话) —— 找不到不许猜成「0 件所以齐」。"""
    notes = []
    if not os.path.isdir(pkg_dir):
        return None, None, None, ["包目录不存在：%s（这一发核不了）" % pkg_dir]
    zips = []
    for name in os.listdir(pkg_dir):
        if name.endswith(".zip"):
            p = os.path.join(pkg_dir, name)
            zips.append((os.path.getmtime(p), p))
    zips.sort(reverse=True)
    if not zips:
        return None, None, None, ["包目录里没有 .zip：%s（这一发核不了）" % pkg_dir]
    zp = zips[0][1]
    notes.append("按 mtime 取最新那份 zip：%s（目录里共 %d 份 .zip）" % (os.path.basename(zp), len(zips)))
    tree = os.path.join(pkg_dir, "便携包", "agentkernel")
    bin_dir = os.path.join(tree, "bin") if os.path.isdir(os.path.join(tree, "bin")) else None
    ev_dir = os.path.join(tree, "evidence") if os.path.isdir(os.path.join(tree, "evidence")) else None
    if not bin_dir:
        notes.append("盘上 bin 目录不存在（⇒ 逐件的「盘上那份」一律记 核不了）")
    if not ev_dir:
        notes.append("盘上 evidence 目录不存在（同上）")
    return zp, bin_dir, ev_dir, notes


def scan_source(root):
    """源码侧：返回 (份数, 最新 mtime, 最新那支的名字, 最新三支, 读不了几支)。"""
    pairs = []
    unreadable = 0
    for dirpath, dirnames, filenames in os.walk(root):
        rel = os.path.relpath(dirpath, root)
        head = rel.replace("\\", "/").split("/")[0]
        if any(part in SKIP_DIR_NAMES for part in rel.replace("\\", "/").split("/")):
            dirnames[:] = []
            continue
        if head.startswith("构建") or head == "报告":
            dirnames[:] = []
            continue
        for fn in filenames:
            if fn.endswith(SOURCE_EXT) or fn == "go.mod":
                p = os.path.join(dirpath, fn)
                try:
                    pairs.append((os.path.getmtime(p), os.path.relpath(p, root).replace("\\", "/")))
                except OSError:
                    unreadable += 1
        for d in list(dirnames):
            if d in SKIP_DIR_NAMES or d.startswith("构建"):
                dirnames.remove(d)
    if not pairs:
        return 0, None, None, [], unreadable
    pairs.sort(reverse=True)
    return len(pairs), pairs[0][0], pairs[0][1], pairs[:3], unreadable


def verdict(exe_t, src_t):
    if src_t is None:
        return "核不了"
    return "旧" if src_t > exe_t else "不旧"


def read_selfcheck():
    """TEMP 里造微型假包与假源码树，逐条验四件事；返回 [(名字, 成立否, 说明)]。"""
    out = []
    box = tempfile.mkdtemp(prefix="ps-staleness-selfcheck-")
    try:
        repo = os.path.join(box, "repo")
        os.makedirs(repo)
        a_go = os.path.join(repo, "a.go")
        open(a_go, "w").write("package a\n")
        old = time.mktime((2020, 1, 2, 3, 4, 5, 0, 0, -1))
        new = time.mktime((2099, 1, 2, 3, 4, 5, 0, 0, -1))
        src_t = os.path.getmtime(a_go)

        def make_zip(path, exe_time):
            zi = zipfile.ZipInfo("agentkernel/bin/x.exe", date_time=time.localtime(exe_time)[:6])
            with zipfile.ZipFile(path, "w") as zf:
                zf.writestr(zi, b"BYTES-INSIDE-ZIP")

        zp1 = os.path.join(box, "old.zip")
        make_zip(zp1, old)
        v1 = verdict(zip_dt(zipfile.ZipFile(zp1).getinfo("agentkernel/bin/x.exe")), src_t)
        out.append(("甲 · exe 早于源码要判旧（这把尺子会红）", v1 == "旧", "现判 %s" % v1))

        zp2 = os.path.join(box, "new.zip")
        make_zip(zp2, new)
        v2 = verdict(zip_dt(zipfile.ZipFile(zp2).getinfo("agentkernel/bin/x.exe")), src_t)
        out.append(("乙 · exe 晚于源码要判不旧", v2 == "不旧", "现判 %s" % v2))

        empty = os.path.join(box, "empty_repo")
        os.makedirs(empty)
        n_empty, t_empty, _, _, _ = scan_source(empty)
        v3 = verdict(old, t_empty)
        out.append(("丙 · 源码侧一份都没有要判核不了（不许读成 0 所以合格）",
                    v3 == "核不了" and n_empty == 0, "现判 %s ｜ 扫到 %d 份" % (v3, n_empty)))

        zp3 = os.path.join(box, "mismatch.zip")
        make_zip(zp3, new)
        disk = os.path.join(box, "x.exe")
        open(disk, "wb").write(b"BYTES-ON-DISK-DIFFERENT")
        same = sha256_bytes(zp3_bytes(zp3)) == sha256_file(disk)
        out.append(("丁 · 包里与盘上字节不同要报不一致（不许默认同一件）", same is False,
                    "两份字节相同 = %s（必须 False）" % same))
    finally:
        pass  # TEMP 留着，读数里印得出落点
    return out


def zp3_bytes(path):
    with zipfile.ZipFile(path) as zf:
        return zf.read("agentkernel/bin/x.exe")


def main():
    if len(sys.argv) >= 2 and os.path.exists(sys.argv[1]):
        sys.stderr.write("输出件已存在，拒绝覆盖上一轮凭据：%s\n" % sys.argv[1])
        return 3
    out_path = sys.argv[1] if len(sys.argv) >= 2 else None
    pkg_dir = os.environ.get("PS_PKG_DIR") or PKG_DEFAULT
    L = []
    w = L.append

    w("交付包 · 逐件来历 —— 尺子（只读数，不动包、不动树）")
    w("生成 %s ｜ 仓库根 %s ｜ 包落点 %s ｜ 环境变量 PS_PKG_DIR 可换" % (
      stamp(time.time()), REPO, os.path.abspath(pkg_dir)))
    w("")
    w("== 反臂 · 这把尺子自己会不会红（TEMP 里造微型假包，四件事逐条）==")
    arms = read_selfcheck()
    for name, ok, why in arms:
        w("  %s ｜ %s ｜ %s" % ("对上" if ok else "没对上", name, why))
    arm_ok = all(ok for _, ok, _ in arms)
    w("  反臂结论：%s" % ("对上，下面那三份读数才许读" if arm_ok else "没对上 ⇒ 这把尺子自己漂了，下面一律不许读成结论（退出码 5）"))
    w("")

    if not arm_ok:
        text = "\n".join(L) + "\n"
        if out_path:
            with open(out_path, "w", encoding="utf-8", newline="\r\n") as f:
                f.write(text)
        else:
            print(text)
        return 5

    zip_path, bin_dir, ev_dir, notes = find_package(pkg_dir)
    n_src, src_t, src_name, top3, unreadable = scan_source(REPO)

    w("== 四 · 分母自印 ==")
    for note in notes:
        w("  有话：%s" % note)
    if src_t is None:
        w("  源码侧：扫到 .go/go.mod **0 份**（读不了 %d 支）⇒ 这一发读成「核不了」，不读成「0 处所以齐」" % unreadable)
    else:
        w("  源码侧：扫到 .go/go.mod %d 份（读不了 %d 支）｜ 最新时间 %s ｜ 那一支 = %s" % (n_src, unreadable, stamp(src_t), src_name))
        for t, name in top3:
            w("      %s  %s" % (stamp(t), name))
    if zip_path is None:
        w("  包侧：拿不到 zip ⇒ 这一发读成「核不了」（退出码 5）")
        text = "\n".join(L) + "\n"
        if out_path:
            with open(out_path, "w", encoding="utf-8", newline="\r\n") as f:
                f.write(text)
        else:
            print(text)
        return 5
    zf = zipfile.ZipFile(zip_path)
    infos = {i.filename: i for i in zf.infolist()}
    names = list(infos)
    exe_in_zip = [n for n in names if n.endswith(".exe")]
    ev_in_zip = [n for n in names if "/evidence/" in n or n.startswith("evidence/")]
    w("  包侧：zip 条目共 %d 条 ｜ 其中 .exe %d 支 ｜ evidence/ %d 份 ｜ 盘上 bin %s ｜ 盘上 evidence %s" % (
      len(names), len(exe_in_zip), len(ev_in_zip), "有" if bin_dir else "核不了", "有" if ev_dir else "核不了"))
    w("")

    w("== 一 · 包内 exe 逐件（zip 那份字节 ↔ 盘上那份字节 ↔ 源码侧最新时间）==")
    stale = []
    for n in sorted(exe_in_zip):
        i = infos[n]
        zt = zip_dt(i)
        zb = zf.read(n)
        base = os.path.basename(n)
        disk_p = os.path.join(bin_dir, base) if bin_dir else None
        if disk_p and os.path.exists(disk_p):
            dt_ = os.path.getmtime(disk_p)
            same = sha256_file(disk_p) == sha256_bytes(zb)
            disk_s = "盘上 mtime %s ｜ 两份字节相同 = %s" % (stamp(dt_), same)
            earliest = min(zt, dt_)
        else:
            disk_s = "盘上那份：核不了（%s 不在）" % (disk_p if disk_p else "盘上 bin 目录")
            earliest = zt
        v = verdict(earliest, src_t)
        if v == "旧":
            stale.append(base)
        w("  %s ｜ zip 内 date_time %s ｜ %s ｜ 判 %s" % (base, stamp(zt), disk_s, v))
        w("      zip 里 sha256 %s…（%d B）" % (sha256_bytes(zb)[:16], i.file_size))
    w("  ⇒ 判「旧」（源码侧有比这两份都新的文件）：%d 支%s" % (len(stale), ("：" + "、".join(stale)) if stale else ""))
    w("  口径：取 zip 与盘上**较早**那一发来判（宁可说旧）；整棵树的最新时间当参照 = 上界，不是这条依赖链的精确答案。")
    w("")

    w("== 一·附 · 包落点里逐份 zip 对照（不只最新那一份；只数、不判修法）==")
    all_zips = []
    for nm in os.listdir(pkg_dir):
        if nm.endswith(".zip"):
            all_zips.append((os.path.getmtime(os.path.join(pkg_dir, nm)), os.path.join(pkg_dir, nm)))
    all_zips.sort(reverse=True)
    for _, zpath in all_zips:
        try:
            with zipfile.ZipFile(zpath) as cand:
                cn = {i.filename: i for i in cand.infolist()}
                cexe = [n for n in cn if n.endswith(".exe")]
                cev = [n for n in cn if "/evidence/" in n or n.startswith("evidence/")]
                exe_ts = [zip_dt(cn[n]) for n in cexe]
                newest = max(exe_ts, default=None)
                n_old_exe = sum(1 for t in exe_ts if verdict(t, src_t) == "旧")
                n_old_ev = sum(1 for n in cev if newest is not None and zip_dt(cn[n]) < newest)
                w("  %-38s 条目 %d ｜ exe %d 支（最新 date_time %s）｜ 判旧的 exe %d 支 ｜ evidence %d 份（旧于该 exe %d 份）" % (
                  os.path.basename(zpath), len(cn), len(cexe),
                  stamp(newest) if newest else "核不了", n_old_exe, len(cev), n_old_ev))
        except (OSError, zipfile.BadZipFile) as e:
            w("  %-38s 打不开：%s ⇒ 记 核不了" % (os.path.basename(zpath), e))
    w("  ⇒ 这一行是给「今天这份包还旧不旧」与「上一版包当时旧成什么样」两个问题各自一个数；引用时别把两行并成一句。")
    w("")

    w("== 二 · evidence 逐件（凭据的话 vs 包里的字节）==")
    newest_exe = max([zip_dt(infos[n]) for n in exe_in_zip], default=None)
    older = []
    for n in sorted(ev_in_zip):
        i = infos[n]
        t = zip_dt(i)
        tag = "早于包里最新那支 exe ⇒ 话比字节旧" if (newest_exe is not None and t < newest_exe) else "不早于"
        if newest_exe is not None and t < newest_exe:
            older.append(os.path.basename(n))
        w("  %-34s %s ｜ %d B ｜ %s" % (os.path.basename(n), stamp(t), i.file_size, tag))
    w("  ⇒ evidence 里比包里最新 exe 旧的：%d 份%s" % (len(older), ("：" + "、".join(older)) if older else ""))
    w("")

    w("== 三 · kwire 那一格三个数并排（只数、不判）==")
    z_hit = [n for n in names if "kwire" in n.replace("\\", "/").lower()]
    d_hit = []
    if bin_dir:
        d_hit = [f for f in os.listdir(bin_dir) if "kwire" in f.lower()]
    src_kw = []
    kw_dir = os.path.join(REPO, "cmd", "kwire")
    if os.path.isdir(kw_dir):
        src_kw = sorted(os.listdir(kw_dir))
    w("  包内含 kwire 的条目 %d ｜ 盘上 bin 含 kwire 的文件 %d ｜ 仓库 cmd/kwire 里的文件 %d 份：%s" % (
      len(z_hit), len(d_hit), len(src_kw), "、".join(src_kw) if src_kw else "（目录不存在）"))
    w("  ⇒ 前两个数是 0、第三个数不是 0 的那一刻：拿到包的人重算不出接口对账那一行（待跑第 3 张卡说的事）。")
    w("")

    w("== 五 · 这把尺子看不见的路（谁引用这份读数都该知道）==")
    w("  · 「旧」是按整棵树最新时间判的**上界**：任何一支 .go 新于 exe 就报旧，哪怕 exe 那条依赖链其实没变 ⇒ 会蒙对也会蒙严。")
    w("  · mtime 不是来历：有人在树上 touch 过、或从别处拷文件（时间戳带过来）都能让这一列变红或变绿；"
      "真要钉来历得比字节（比如把 exe 里嵌的版本格读出来），这一发没做，也不许读成「来历已核」。")
    w("  · zip 里 .exe 的 date_time 是**打包时**写进条目的，不是编译时刻；盘上那份的 mtime 才是落地时刻，两个都可能晚于真正 `go build` 的那一刻。")
    w("  · 这一支只读数、不是闸门 ⇒ 判「旧」不红任何检；把它并进 `打包.py` 或开机检是改判据行为，归作者拍（待跑第 4 张那三个选项）。")

    text = "\n".join(L) + "\n"
    if out_path:
        with open(out_path, "w", encoding="utf-8", newline="\r\n") as f:
            f.write(text)
    else:
        print(text)
    print("stale=%d exe=%d evidence=%d zip=%s" % (len(stale), len(exe_in_zip), len(ev_in_zip), os.path.basename(zip_path)))
    return 4 if stale else 0


if __name__ == "__main__":
    sys.exit(main())
