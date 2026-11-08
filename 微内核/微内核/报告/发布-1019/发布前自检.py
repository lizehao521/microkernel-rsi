#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""发布前自检：白皮书里的每个阿拉伯数字都必须来自报告 §8.7 第一列。"""
import datetime
import hashlib
import re
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

ROOT = Path(__file__).resolve().parent
REPORT = ROOT.parent / "研究报告-可验证记账层的可行边界-1019.md"
PAPER = ROOT / "白皮书-当Agent说我做完了-1019.md"
OUT = ROOT / f"发布前自检-1019-{datetime.datetime.now():%H%M%S}.txt"

BANNED = ["更安全", "更聪明", "有买家"]
MARKERS = [
    ("弱判据", "双臂的弱判据被命名"),
    ("严格判据没有通过", "严格判据的未通过状态被写明"),
    ("Measure", "干净臂的检查范围被限定在 Measure 族"),
    ("结构缺口", "字符串格/计数格的边界被写明"),
    ("调用方口述", "130/110 的口述口径被写明"),
    ("单源自印", "8/8 vs 6/8 的单源口径被写明"),
]
NUM_RE = re.compile(r"\d+")


def load() -> tuple[str, str]:
    if not REPORT.exists():
        raise SystemExit(f"缺报告：{REPORT}")
    if not PAPER.exists():
        raise SystemExit(f"缺白皮书：{PAPER}")
    return REPORT.read_text(encoding="utf-8"), PAPER.read_text(encoding="utf-8")


def sec87_rows(report: str) -> list[str]:
    lines = report.splitlines()
    start = None
    for i, line in enumerate(lines):
        if line.startswith("### 8.7"):
            start = i
            break
    if start is None:
        raise SystemExit("报告里找不到 ### 8.7（尺子读不到分母）")
    rows = []
    for line in lines[start + 1:]:
        if line.startswith("## ") or line.startswith("---"):
            break
        if not line.startswith("|"):
            continue
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        if not cells:
            continue
        first = cells[0]
        if first == "数" or set(first) <= set("-: "):
            continue
        rows.append(first)
    if not rows:
        raise SystemExit("§8.7 表格解析出 0 行（尺子坏了）")
    return rows


def scan(text: str, allowed: set[str]) -> tuple[list[str], list[str], list[tuple[str, str]]]:
    banned_hits = [w for w in BANNED if w in text]
    found = NUM_RE.findall(text)
    unknown = sorted({n for n in found if n not in allowed}, key=lambda x: (len(x), x))
    contexts = []
    for n in sorted(set(found), key=lambda x: (len(x), x)):
        idx = text.find(n)
        s = max(0, idx - 18)
        e = min(len(text), idx + len(n) + 18)
        contexts.append((n, text[s:e].replace("\n", " ")))
    return banned_hits, unknown, contexts


def main() -> int:
    report, paper = load()
    rows = sec87_rows(report)
    allowed = {n for row in rows for n in NUM_RE.findall(row)}
    if len(allowed) < 10:
        print(f"尺子坏了：§8.7 只抽出 {len(allowed)} 个数字，少于 10")
        return 2

    banned_hits, unknown, contexts = scan(paper, allowed)
    found = set(NUM_RE.findall(paper))
    missing_markers = [why for needle, why in MARKERS if needle not in paper]

    # 尺子自检：合成红样本必须红，合成干净样本必须绿。
    red = "这份稿子更聪明，还能把 6327 次调用压成一发。"
    green = "一次调用 49 格；收口约 6 次调用；截至 2026-10-19。"
    red_banned, red_unknown, _ = scan(red, allowed)
    green_banned, green_unknown, _ = scan(green, allowed)
    selftest_ok = bool(red_banned) and bool(red_unknown) and not green_banned and not green_unknown

    verdict = "PASS"
    if banned_hits or unknown or missing_markers or not selftest_ok:
        verdict = "FAIL"
    rc = 0 if verdict == "PASS" else (1 if selftest_ok else 2)

    digest = hashlib.sha256(PAPER.read_bytes()).hexdigest()
    lines = [
        f"== 发布前自检 · {datetime.datetime.now():%Y-%m-%d %H:%M:%S} ==",
        f"白皮书：{PAPER}",
        f"字节={PAPER.stat().st_size} ｜ sha256={digest}",
        f"出处表：{REPORT} §8.7（{len(rows)} 行）",
        f"允许数字集合（从 §8.7 第一列抽出，{len(allowed)} 个）：{sorted(allowed, key=lambda x: (len(x), x))}",
        "",
        "== 白皮书数字普查 ==",
        f"出现 {len(NUM_RE.findall(paper))} 次，{len(found)} 个不同数字；不在 §8.7 的：{unknown if unknown else '无'}",
    ]
    for n, ctx in contexts:
        lines.append(f"  {n:>4} ×{NUM_RE.findall(paper).count(n):<2} ｜ …{ctx}…")
    lines += [
        "",
        "== 禁用词普查 ==",
    ]
    for w in BANNED:
        lines.append(f"  {w}：{paper.count(w)} 次")
    lines += [
        f"  结论：{'无禁用词' if not banned_hits else '出现 ' + str(banned_hits)}",
        "",
        "== 口径标记 ==",
    ]
    for needle, why in MARKERS:
        lines.append(f"  {'✓' if needle in paper else '✗'} {needle} —— {why}")
    lines += [
        "",
        "== 尺子自检 ==",
        f"  合成红样本（含“更聪明”与 6327）→ 禁用词 {red_banned} ｜ 越界数字 {red_unknown}",
        f"  合成干净样本（只用 49/6/2026/10/19）→ 禁用词 {green_banned} ｜ 越界数字 {green_unknown}",
        f"  判定：{'红样本会红、干净样本会绿' if selftest_ok else '尺子坏了'}",
        "",
        f"== 判定：{verdict} ｜ rc={rc} ==",
        "",
        "本自检证明：白皮书里的阿拉伯数字都在 §8.7 第一列出现过，禁用词 0 次，六条口径标记都在。",
        "本自检不证明：§8.7 每一行引用的盘上原件本身说对了；不证明中文数词（三种状态、一条底线）也是读数；不证明正文论证成立。",
    ]

    text = "\n".join(lines) + "\n"
    OUT.write_text(text, encoding="utf-8")
    print(text)
    print(f"写入：{OUT}")
    return rc


if __name__ == "__main__":
    sys.exit(main())
