# -*- coding: utf-8 -*-
"""交付包 · 文档侧同步的尺子 —— 把「仓库 .md 是唯一源，改文档必须重跑打包.py」从一句规矩换成逐件字节对照。

为什么这一支：待跑那节 #66 那一族（第 2、3、4 张卡）量的是**字节侧**（包里的 exe 与凭据谁比谁新）。
文档侧一直没逐件比过：包里那几份 .md 到底是仓库那几份的同一字节，还是某一次拷贝之后就再没跟上？
规矩盖不盖得住，不能靠我复述规矩。这一支**只读数、不闸门**：不改 `打包.py`、不动包里任何一件字节、不动仓库任何一份 .md。

三方逐件（对 zip 里每一份 .md）：
  zip 条目字节 sha256 ｜ 盘上便携树同名件字节 sha256 ｜ 两份是否同一
  → 仓库源（按 NAME-MAP 反查 + 两条兜底候选，命中哪条规则一并印出来）字节 sha256
  → 判「相同 ／ 不同 ／ 核不了」

读数分五节：
  一 · 最新那份 zip 里的 .md 逐件（含源候选命中了哪条规则；映射指向的源不在仓库时，去交付树里找它落在哪）
  二 · 打包器写死的同步名单（从 `打包.py` 现抓，抓不到就记 核不了）vs 仓库根 .md 全数 —— 谁是「仓库有、打包器根本不读」
  三 · 落点里逐份 zip 各一行（这份包当时齐不齐，与今天这份分开读）
  四 · 分母自印
  五 · 这把尺子看不见的路

口径写死在代码里，读这份件的人能看到它为什么可能骗人：
  · 源候选按四条规则依次试：NAME-MAP 原样名整路径 → NAME-MAP 原样名的**文件名**落仓库根 → 包内相对名同路径同名 → 仓库根同名。
    命中哪条都印出来 —— 因为 `docs/README.md` 这条 NAME-MAP 没列，表头那句「未列出的名字两边一致」按字面找 `仓库/docs/README.md` 会找不到，
    真源其实在仓库根；反过来 `docs/product-positioning.md` 表里列了，可它写的 `docs/产品定位.md` 是**交付树**里的路径，仓库根本没有 `docs/` 目录。
    表头那句话与打包器的实际取材路径不是一回事，这一发把两边都摊开，兜底命中一律点名。
  · 「不同」只说包里的字节与仓库源不同，**不说谁错**：可能作者在改包内那一份，也可能包没跟上仓库。谁该动，归作者拍。
  · 只比 `.md`；`PACKAGE-README.md` 之外还有 MANIFEST/NAME-MAP 那两件是打包器每轮生成的，不在这发的分母里。

自核（每次都跑，TEMP 里造微型假包假仓库，不碰在跑的树）：五件事必须同时成立——
  甲 zip 与仓库源字节一致 ⇒ 必须判「相同」（正对照，证明判对也会判）
  乙 zip 与仓库源字节不一致 ⇒ 必须判「不同」（这把尺子会红）
  丙 仓库里三份候选都没有 ⇒ 必须判「核不了」（不许读成「0 处不同所以齐」）
  丁 zip 与盘上同名件字节不一致 ⇒ 必须报「两份字节不一致」（不许默认它们是同一件）
  戊 zip 里一份 .md 都没有 ⇒ 清单必须判「核不了（0 件）」
  任何一件不成立 ⇒ 这把尺子自己漂了 ⇒ 退出码 5，下面四件读数一律不许读成结论。

跑法：python -X utf8 tasks/probe/package_doc_sync.py [输出.txt]
      环境变量 `PDS_PKG_DIR` 可换包的落点（默认从本文件位置往上退两级再找 `构建-1017`）
退出码：0 数完了且没有任何一份判「不同」｜ 4 至少一份判「不同」｜ 5 自核红 或 包/仓库拿不到（核不了）｜ 3 输出件已存在（拒覆盖上一轮凭据）
"""
import hashlib
import os
import re
import shutil
import sys
import tempfile
import time
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
WORK_DEFAULT = os.path.join(REPO, os.pardir, os.pardir, "构建-1017")
MAPFILE = "NAME-MAP.txt"
SYNC_RE = re.compile(r"for name in \((.*?)\):")


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


def pick_docs(names):
    """zip 条目名 → 包内相对名（剥掉顶层目录）；一份 .md 都没有时返回空表，由调用方判 核不了。"""
    out = []
    for n in names:
        if not n.endswith(".md") or n.endswith("/"):
            continue
        rel = n.split("/", 1)[1] if "/" in n else n
        out.append((rel, n))
    return sorted(out)


def load_map(map_text):
    """NAME-MAP.txt → {包内相对名: 原样名}；带 # 的表头那行含 -> 但不算条目。"""
    out = {}
    for ln in map_text.splitlines():
        s = ln.strip()
        if not s or s.startswith("#") or "->" not in s:
            continue
        raw, dst = s.split("->", 1)
        out[dst.strip()] = raw.strip()
    return out


def resolve_source(rel, mapping, repo_root):
    """返回 (命中的绝对路径或 None, 命中/失败的话)。四条候选按序试，谁命中都写出来 ——
    因为 NAME-MAP 那张表说的是**交付树内部的改名**（原样中文名 → 包内 ASCII 名），它写的目录是交付树的目录，
    不等于仓库的目录：`docs/产品定位.md` 在仓库里根本没有 `docs/`，真源在仓库根。
    所以规则二是一条**明说的兜底**（拿映射里的文件名去仓库根找），命中它要在读数里点名，引用的人才能看出这不是直接命中。"""
    raw = mapping.get(rel)
    cands = []
    if raw:
        cands.append((os.path.join(repo_root, raw.replace("/", os.sep)), "规则一 NAME-MAP 原样名整路径命中（%s）" % raw))
        cands.append((os.path.join(repo_root, os.path.basename(raw)),
                      "规则二 兜底：拿 NAME-MAP 原样名的**文件名**去仓库根找（表里写的目录是交付树的，仓库没有）"))
    cands.append((os.path.join(repo_root, rel.replace("/", os.sep)), "规则三 包内相对名在仓库同路径同名（仓库/%s）" % rel))
    cands.append((os.path.join(repo_root, os.path.basename(rel)), "规则四 包内相对名的文件名去仓库根找"))
    for p, why in cands:
        if os.path.isfile(p):
            return p, why
    return None, "四条候选都没命中（试了：%s）" % "、".join(os.path.basename(p) for p, _ in cands)


def find_in_delivery_tree(raw_rel, work_dir):
    """映射里的原样名在仓库不存在时，去交付树的中文/便携两份目录里找它到底落在哪。返回话或 None。"""
    sub = raw_rel.replace("/", os.sep)
    for tree in ("agentkernel", os.path.join("便携包", "agentkernel")):
        p = os.path.join(work_dir, tree, sub)
        if os.path.isfile(p):
            return "%s ｜ mtime %s ｜ %d B" % (p, stamp(os.path.getmtime(p)), os.path.getsize(p))
    return None


def judge(zip_sha, src_sha):
    if src_sha is None:
        return "核不了"
    return "相同" if zip_sha == src_sha else "不同"


def pair_verdict(zip_sha, disk_sha):
    if disk_sha is None:
        return "核不了（盘上那份不在）"
    return "一致" if zip_sha == disk_sha else "不一致"


def read_sync_list(packer_path):
    """从 打包.py 现抓【文档同步】那个写死的名单；抓不到就返回 None 并说明，绝不猜名单内容。"""
    if not os.path.isfile(packer_path):
        return None, None, "打包器不在：%s ⇒ 这一节核不了" % packer_path
    with open(packer_path, "r", encoding="utf-8", errors="replace") as f:
        lines = f.read().splitlines()
    for idx, ln in enumerate(lines, 1):
        if "for name in" in ln and "README.md" in ln and "contract.md" in ln:
            m = SYNC_RE.search(ln)
            if not m:
                return None, None, "第 %d 行形状对但没抓到括号名单 ⇒ 这一节核不了" % idx
            names = re.findall(r'"([^"]+)"', m.group(1))
            if not names:
                return None, None, "第 %d 行抓到的名单是空表 ⇒ 这一节核不了（不读成「名单为空所以不用同步」）" % idx
            return names, idx, "抓到名单 %d 件（第 %d 行）：%s" % (len(names), idx, "、".join(names))
    return None, None, "打包器里没有那行 for 名单 ⇒ 这一节核不了（不猜它同步了哪几份）"


def repo_root_mds(repo_root):
    if not os.path.isdir(repo_root):
        return []
    return sorted(n for n in os.listdir(repo_root) if n.endswith(".md") and os.path.isfile(os.path.join(repo_root, n)))


def read_selfcheck():
    """TEMP 里造微型假包 + 假仓库 + 假便携树，逐条验五件事；返回 [(名字, 成立否, 说明)]。"""
    out = []
    box = tempfile.mkdtemp(prefix="pds-docsync-selfcheck-")
    try:
        repo = os.path.join(box, "repo")
        os.makedirs(repo)
        src = os.path.join(repo, "README.md")
        with open(src, "wb") as f:
            f.write(b"SAME-BYTES\n")
        zp = os.path.join(box, "same.zip")
        with zipfile.ZipFile(zp, "w") as zf:
            zf.writestr("agentkernel/docs/README.md", "SAME-BYTES\n")
        with zipfile.ZipFile(zp) as zf:
            zs = sha256_bytes(zf.read("agentkernel/docs/README.md"))
        v = judge(zs, sha256_file(src))
        out.append(("甲 · 字节一致要判相同（正对照）", v == "相同", "现判 %s" % v))

        zp2 = os.path.join(box, "diff.zip")
        with zipfile.ZipFile(zp2, "w") as zf:
            zf.writestr("agentkernel/docs/README.md", "OTHER-BYTES\n")
        with zipfile.ZipFile(zp2) as zf:
            zs2 = sha256_bytes(zf.read("agentkernel/docs/README.md"))
        v2 = judge(zs2, sha256_file(src))
        out.append(("乙 · 字节不一致要判不同（这把尺子会红）", v2 == "不同", "现判 %s" % v2))

        v3 = judge(zs2, None)
        out.append(("丙 · 仓库无源要判核不了（不许读成 0 处不同所以齐）", v3 == "核不了", "现判 %s" % v3))

        tree = os.path.join(box, "便携包", "agentkernel", "docs")
        os.makedirs(tree)
        disk = os.path.join(tree, "README.md")
        with open(disk, "wb") as f:
            f.write(b"DISK-OTHER\n")
        pv = pair_verdict(zs, sha256_file(disk))
        out.append(("丁 · zip 与盘上同名件字节不一致要报不一致", pv == "不一致", "现报 %s" % pv))
        pv2 = pair_verdict(zs, None)
        out.append(("丁·附 · 盘上那份不在要报核不了", "核不了" in pv2, "现报 %s" % pv2))

        zp3 = os.path.join(box, "nodoc.zip")
        with zipfile.ZipFile(zp3, "w") as zf:
            zf.writestr("agentkernel/bin/x.exe", b"BYTES")
        with zipfile.ZipFile(zp3) as zf:
            docs = pick_docs([i.filename for i in zf.infolist()])
        out.append(("戊 · 包里一份 .md 都没有要判核不了（不读成齐）", len(docs) == 0, "现数 %d 件" % len(docs)))

        mp = load_map("# 原样中文名 -> 便携包内 ASCII 条目名 ｜ 未列出的名字两边一致\ndocs/产品定位.md -> docs/pp.md\n")
        one = len(mp) == 1 and mp.get("docs/pp.md") == "docs/产品定位.md"
        out.append(("己 · NAME-MAP 表头那行含 -> 不能当条目（否则多算一件）", one, "现解析 %d 条" % len(mp)))

        r1, _ = resolve_source("docs/pp.md", mp, repo)
        miss = r1 is None
        os.makedirs(os.path.join(repo, "docs"))
        with open(os.path.join(repo, "docs", "pp.md"), "wb") as f:
            f.write(b"x\n")
        r2, why2 = resolve_source("docs/pp.md", mp, repo)
        hit3 = r2 is not None and "规则三" in why2
        shutil.rmtree(os.path.join(repo, "docs"))
        r3, _ = resolve_source("docs/pp.md", mp, repo)
        back_to_miss = r3 is None
        with open(os.path.join(repo, "产品定位.md"), "wb") as f:
            f.write(b"x\n")
        r4, why4 = resolve_source("docs/pp.md", mp, repo)
        hit2 = r4 is not None and "规则二" in why4
        out.append(("庚 · 源候选按序试：映射整路径 → 映射的文件名落仓库根 → 包内相对名同路径同名 → 仓库根同名",
                    miss and hit3 and back_to_miss and hit2,
                    "全空 %s ｜ 有仓库/docs/pp.md 命中「%s」｜ 抹掉 docs 又全空 %s ｜ 只有仓库/产品定位.md（映射里的文件名）时命中「%s」" % (
                      "全落空" if miss else "竟命中了", why2 if hit3 else why2,
                      "是" if back_to_miss else "否", why4 if hit2 else why4)))
        return out
    finally:
        try:
            shutil.rmtree(box, ignore_errors=True)
        except OSError:
            pass


def main():
    t0 = time.time()
    if len(sys.argv) >= 2 and os.path.exists(sys.argv[1]):
        sys.stderr.write("输出件已存在，拒绝覆盖上一轮凭据：%s\n" % sys.argv[1])
        return 3
    out_path = sys.argv[1] if len(sys.argv) >= 2 else None
    work_dir = os.path.abspath(os.environ.get("PDS_PKG_DIR") or WORK_DEFAULT)
    packer = os.path.join(work_dir, "打包.py")
    L = []
    w = L.append

    def emit(code, tag):
        text = "\n".join(L) + "\n"
        if out_path:
            with open(out_path, "w", encoding="utf-8", newline="\r\n") as f:
                f.write(text)
        else:
            print(text)
        print("PDS %s rc=%d elapsed_s=%.2f out=%s" % (tag, code, time.time() - t0, out_path or "-"))
        return code

    w("交付包 · 文档侧同步 —— 尺子（只读数，不动包、不动仓库 .md）")
    w("生成 %s ｜ 仓库根 %s ｜ 交付落点 %s ｜ 环境变量 PDS_PKG_DIR 可换" % (stamp(time.time()), REPO, work_dir))
    w("")
    arms = read_selfcheck()
    w("== 反臂 · 这把尺子自己会不会红（TEMP 里造微型假包假仓库，逐条 %d 件）==" % len(arms))
    for name, ok, why in arms:
        w("  %s ｜ %s ｜ %s" % ("对上" if ok else "没对上", name, why))
    arm_ok = all(ok for _, ok, _ in arms)
    w("  反臂结论：%s" % ("对上，下面那几节读数才许读" if arm_ok else "没对上 ⇒ 这把尺子自己漂了，下面一律不许读成结论（退出码 5）"))
    w("")
    if not arm_ok:
        return emit(5, "自核红")

    if not os.path.isdir(work_dir):
        w("包目录不存在：%s ⇒ 这一发读成「核不了」（退出码 5）" % work_dir)
        return emit(5, "包目录不在")
    zips = sorted([(os.path.getmtime(os.path.join(work_dir, n)), os.path.join(work_dir, n))
                   for n in os.listdir(work_dir) if n.endswith(".zip")], reverse=True)
    if not zips:
        w("包目录里没有 .zip：%s ⇒ 这一发读成「核不了」（退出码 5）" % work_dir)
        return emit(5, "无zip")
    zip_path = zips[0][1]
    stg = os.path.join(work_dir, "便携包", "agentkernel")
    map_path = os.path.join(stg, MAPFILE)
    mapping = {}
    if os.path.isfile(map_path):
        with open(map_path, "r", encoding="utf-8", errors="replace") as f:
            mapping = load_map(f.read())
        w("NAME-MAP：从 %s 解析出映射 %d 条（表头那行不算条目）" % (map_path, len(mapping)))
    else:
        w("NAME-MAP：便携树里没有 %s ⇒ 映射按空表走，源候选只能靠同名兜底（这一发有一部分要记 核不了）" % MAPFILE)
    w("按 mtime 取最新那份 zip：%s（落点里共 %d 份 .zip）" % (os.path.basename(zip_path), len(zips)))
    w("")

    zf = zipfile.ZipFile(zip_path)
    infos = {i.filename: i for i in zf.infolist()}
    docs = pick_docs(list(infos))
    w("== 一 · 最新 zip 里的 .md 逐件（zip 字节 ↔ 盘上便携树 ↔ 仓库源）==")
    if not docs:
        w("  这份 zip 里一份 .md 都没有 ⇒ 整节记 核不了，不读成「0 处不同所以文档侧全齐」")
        return emit(5, "包内无md")
    diffs = []
    unable = []
    for rel, entry in docs:
        zb = zf.read(entry)
        zs = sha256_bytes(zb)
        disk_p = os.path.join(stg, rel.replace("/", os.sep)) if os.path.isdir(stg) else None
        disk_sha = sha256_file(disk_p) if disk_p and os.path.isfile(disk_p) else None
        pv = pair_verdict(zs, disk_sha)
        src_p, why = resolve_source(rel, mapping, REPO)
        src_sha = sha256_file(src_p) if src_p else None
        v = judge(zs, src_sha)
        if v == "不同":
            diffs.append(rel)
        if v == "核不了":
            unable.append(rel)
        w("  %-34s zip 内条目 %s ｜ %d B ｜ date_time %s" % (
          rel, entry, infos[entry].file_size, stamp(zip_dt(infos[entry]))))
        w("      盘上那份：%s ｜ zip↔盘上 %s" % (
          ("mtime %s ｜ %d B" % (stamp(os.path.getmtime(disk_p)), os.path.getsize(disk_p))) if disk_sha else "不在（%s）" % (disk_p or "便携树目录不在"), pv))
        w("      仓库源：%s" % (("命中 %s ⇒ %s（mtime %s ｜ %d B）" % (
            why, src_p, stamp(os.path.getmtime(src_p)), os.path.getsize(src_p))) if src_p else why))
        w("      字节 zip %s… ｜ 源 %s…" % (zs[:16], src_sha[:16] if src_sha else "-"))
        w("      判 %s" % v)
        if src_p and "规则一" not in why:
            w("      有话：这一件不是映射表直接对上，是按「%s」兜底命中的 —— 引用这条要一并带上" % why)
        if not src_p:
            raw = mapping.get(rel)
            if raw:
                where = find_in_delivery_tree(raw, work_dir)
                w("      映射说它的源是仓库里的「%s」，仓库没有这份；交付树里：%s" % (raw, where or "两份目录都没有"))
        if pv == "不一致":
            w("      有话：zip 与盘上便携树这一件字节不是同一份，两个判语各取各自的，别并成一句")
    w("  ⇒ 判「不同」（包里的文档字节与仓库源不是一件事）：%d 份%s" % (len(diffs), ("：" + "、".join(diffs)) if diffs else ""))
    w("  ⇒ 判「核不了」（仓库里找不到可比的那一份）：%d 份%s" % (len(unable), ("：" + "、".join(unable)) if unable else ""))
    w("")

    w("== 二 · 打包器写死的同步名单 ↔ 仓库根 .md 全数 ==")
    names, line_no, list_why = read_sync_list(packer)
    w("  同步名单来源：%s" % list_why)
    root_mds = repo_root_mds(REPO)
    w("  仓库根 .md 现数 %d 份：%s" % (len(root_mds), "、".join(root_mds) or "（一份没有 ⇒ 记 核不了）"))
    if names:
        ns = set(names)
        outside = [n for n in root_mds if n not in ns]
        missing_in_repo = [n for n in names if n not in root_mds]
        w("  在名单里（打包器每轮从仓库重搬）：%s" % ("、".join([n for n in root_mds if n in ns]) or "无"))
        w("  仓库根有、但不在名单里（打包器根本不读 ⇒ 永远进不了包）：%d 份%s" % (len(outside), ("：" + "、".join(outside)) if outside else ""))
        w("  名单里有、但仓库根没有（拷贝会 continue 静默跳过）：%d 份%s" % (len(missing_in_repo), ("：" + "、".join(missing_in_repo)) if missing_in_repo else ""))
        md_map = [(rel, raw) for rel, raw in sorted(mapping.items()) if rel.endswith(".md")]
        md_no_src = ["%s（表里说源是「%s」，仓库那个路径没有）" % (rel, raw) for rel, raw in md_map
                     if not os.path.isfile(os.path.join(REPO, raw.replace("/", os.sep)))]
        all_no_src = sum(1 for rel, raw in mapping.items()
                         if not os.path.isfile(os.path.join(REPO, raw.replace("/", os.sep))))
        w("  全表按「表里写的路径 = 仓库里的路径」去核：落空 %d / %d 条；只取 .md 那几条：落空 %d / %d 条%s" % (
          all_no_src, len(mapping), len(md_no_src), len(md_map),
          ("；" + "；".join(md_no_src)) if md_no_src else ""))
        w("  ⇒ 口径：这张表说的是**交付树内部**的改名（中文原样名 → 包内 ASCII 名），它写的目录是 `构建-1017\\agentkernel\\` 那一侧的，不是仓库的；"
          "仓库根没有 `docs/`、也没有 `凭据/`。所以「全部按仓库去找就落空」那 %d 条**不是 %d 个缺陷**，是这条核法不对；"
          "这一行只留 .md 那几条，是为了把「包内文档 ↔ 仓库源」这条线单独摊开。evidence 那族的源在交付工作树的 `日志/`，不在这一发的分母里。" % (all_no_src, all_no_src))
    w("  口径：名单是从 %s 现抓的一行；打包器改了那行，这份读数跟着变，引用时带上生成时间。" % packer)
    w("")

    w("== 三 · 落点里逐份 zip 对照（不只最新那一份）==")
    for _, zpath in zips:
        try:
            with zipfile.ZipFile(zpath) as cand:
                cd = pick_docs([i.filename for i in cand.infolist()])
                line = "  %-42s .md %d 件 ｜ " % (os.path.basename(zpath), len(cd))
                if not cd:
                    line = "  %-42s .md 0 件 ⇒ 这一份的文档侧记 核不了" % os.path.basename(zpath)
                    w(line)
                    continue
                same = 0
                dif = 0
                una = 0
                parts = []
                for rel, entry in cd:
                    zs = sha256_bytes(cand.read(entry))
                    sp, _ = resolve_source(rel, mapping, REPO)
                    if sp is None:
                        una += 1
                        parts.append("%s 核不了" % rel)
                        continue
                    if zs == sha256_file(sp):
                        same += 1
                    else:
                        dif += 1
                        parts.append("%s 不同" % rel)
                line += "与仓库源相同 %d ｜ 不同 %d ｜ 核不了 %d" % (same, dif, una)
                w(line)
                for p in parts:
                    w("        %s" % p)
        except (OSError, zipfile.BadZipFile) as e:
            w("  %-42s 打不开：%s ⇒ 记 核不了" % (os.path.basename(zpath), e))
    w("  ⇒ 每行是**那份包当时**与仓库现在这份字节的差，两份历史包之间不保证互相可比；引用按行取，别并成一句。")
    w("")

    w("== 四 · 分母自印 ==")
    w("  zip 条目共 %d 条 ｜ 其中 .md %d 件 ｜ NAME-MAP 解析 %d 条 ｜ 落点 zip %d 份" % (len(infos), len(docs), len(mapping), len(zips)))
    w("  仓库根 .md %d 份 ｜ 同步名单 %d 件 ｜ 判「不同」%d 份 ｜ 判「核不了」%d 份" % (len(root_mds), len(names) if names else 0, len(diffs), len(unable)))
    w("  取数耗时 %.2f 秒（含解释器起来那一段）" % (time.time() - t0))
    w("")

    w("== 五 · 这把尺子看不见的路（谁引用这份读数都该知道）==")
    w("  · 只比 .md 的字节；不看内容语义 —— 文档里那句话与代码对不对得上，是 kwire/接口对账那一发的事，这一发替它背书不了。")
    w("  · zip 条目的 date_time 是**打包时**写进去的，不是这份 .md 最后一次被改的时刻；mtime 也不能当来历用。")
    w("  · 「不同」不指谁错：可能是包没跟上仓库，也可能是有人直接改了交付树/包里那一份。这一发只说两串字节不相等。")
    w("  · 便携树是解包产物，不是交付物本体；盘上那份与 zip 那份各判各的，读数里都印了。")
    w("  · NAME-MAP **不能当「仓库里哪份文档变成哪个条目」的清单用**：这一发实测它 3 条 .md 映射按仓库路径去找 3 条全落空（表里的路径是交付树那一侧的）。"
      "所以判「相同」里有 %d 件是靠兜底规则（规则二/规则四）对上的，不是表直接对上 —— 逐件那一行都点名了用哪条。" % sum(1 for rel, entry in docs if resolve_source(rel, mapping, REPO)[0] and "规则一" not in resolve_source(rel, mapping, REPO)[1]))
    w("  · 第三节那三行是**历史 zip 对现在的仓库**：仓库再改一份 .md，历史包的「不同」只增不减，读那几行时把仓库当前的字节当作一把活动的尺，别当已封板的参照。")
    w("  · 这一支只读数、不是闸门 ⇒ 判「不同」不红任何检；要不要把文档同步并进开机检或打包前置闸，是改判据行为，归作者拍。")

    return emit(4 if diffs else 0, "数完")


if __name__ == "__main__":
    sys.exit(main())
