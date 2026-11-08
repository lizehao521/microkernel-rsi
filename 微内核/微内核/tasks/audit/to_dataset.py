# -*- coding: utf-8 -*-
"""to_dataset.py —— 把在册的账与内核的裁决导出成带出处的样本，并如实报"可用对数"。

为什么需要一个数而不是一个想法：训练要的是 (说法, 出处, 内核回执, 改正后的说法) 这种带裁决的样本。
而内核第 3 条不变量是「拒收不留半条」—— 被拒的那份装配**按定义不在账上**。
所以负样本只能从调用方日志里的 `tools/call` 请求体里捞。这个脚本就干这件事：捞出来，数一对少一对。

只读，不改任何在册文件；输出全在 报告/数据集/ 下。控制台只打 ASCII（Windows GBK），全文写 UTF-8。
"""
import glob
import io
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
REPORTS = os.path.join(ROOT, "报告")
OUT = os.path.join(REPORTS, "数据集")
BASIS_NAME = {0: "unset", 1: "measured", 2: "relayed", 3: "estimated", 4: "model_says", 5: "absent"}

summary = []
def say(s):
    summary.append(s)
    sys.stdout.buffer.write(s.encode("ascii", "replace") + b"\n")
    sys.stdout.flush()


def parse_ledger(path):
    """一本 jsonl 账 → {(step, side): {key: (claim, basis_name)}}，顺带扫密钥。"""
    turns, leak = {}, 0
    for raw in io.open(path, encoding="utf-8"):
        raw = raw.strip()
        if not raw:
            continue
        try:
            e = json.loads(raw)
        except Exception:
            leak += raw.count("sk-")
            continue
        if "sk-" in raw:
            leak += 1
        side, key = e["kind"].split(":", 1)
        turns.setdefault((e["step"], side), {})[key] = (e["claim"], BASIS_NAME.get(e["basis"], "?"))
    return turns, leak


def parse_log(path):
    """调用方日志 → [(请求体, 内核回执)]。被拒那份装配只有这里才有。"""
    rows, pending = [], None
    for line in io.open(path, encoding="utf-8", errors="replace"):
        if line.startswith(">>> ["):
            i = line.find('"name": "kernel.')
            if i < 0:
                continue
            tool = re.search(r'"name": "(kernel\.[a-z_]+)"', line)
            tool = tool.group(1) if tool else "?"
            if tool in ("kernel.record_call", "kernel.check_call"):
                j = line.find("{", i)
                pending = (tool, line[j:].strip(), None)
        elif pending and ("拒收" in line or "落账" in line or "整发" in line):
            rows.append(pending[:2] + (line.strip(),))
            pending = None
    return [r for r in rows if r[2]]


def main():
    if not os.path.isdir(OUT):
        os.makedirs(OUT)
    say("export run to %s" % OUT.encode("ascii", "replace").decode())

    # ---------- 1) 正样本：在册账的每一发 ----------
    rows_f = io.open(os.path.join(OUT, "turns_labeled.jsonl"), "w", encoding="utf-8", newline="\n")
    n_turns = n_cells = n_absent = 0
    total_leak = 0
    for path in sorted(glob.glob(os.path.join(REPORTS, "样例-*.jsonl"))):
        turns, leak = parse_ledger(path)
        total_leak += leak
        for (step, side), cells in sorted(turns.items()):
            n_turns += 1
            n_cells += len(cells)
            n_absent += sum(1 for c, b in cells.values() if b == "absent")
            rows_f.write(json.dumps({"src": os.path.basename(path), "session": os.path.basename(path),
                                     "turn": step, "side": side,
                                     "cells": {k: {"claim": v[0], "basis": v[1]} for k, v in cells.items()}},
                                    ensure_ascii=False) + "\n")
    rows_f.close()

    # ---------- 2) 负样本候选：日志里的请求体 + 内核回执 ----------
    n_verdict_rows = 0
    rej_f = io.open(os.path.join(OUT, "verdicts.jsonl"), "w", encoding="utf-8", newline="\n")
    for path in sorted(glob.glob(os.path.join(REPORTS, "*.txt"))):
        for tool, req, verdict in parse_log(path):
            rej_f.write(json.dumps({"src": os.path.basename(path), "tool": tool,
                                    "rejected": "拒收" in verdict,
                                    "verdict": verdict[:400], "assembly": req[:4000]},
                                   ensure_ascii=False) + "\n")
            n_verdict_rows += 1
    rej_f.close()

    # ---------- 3) 数：能凑成"犯错 → 裁决 → 改正"整对的到底有几对 ----------
    # 整对 = 日志里真有**被拒那份装配**（不是只有裁决句）+ 账上有改正后那份。
    # 判法必须是查内容：上一版按"文件在不在"数，报成 4/4，而 agent.py 那趟压根没接
    # Kernel 的 sink、没把 tools/call 请求体写进日志 —— 那是第四次"看着像测到了"。
    by_src = {}
    for path in sorted(glob.glob(os.path.join(REPORTS, "*.txt"))):
        by_src[os.path.basename(path)] = parse_log(path)

    def has_rejected_assembly(fname):
        # 被拒那份装配：请求体里带 Failure/Output 侧字段，且回执说了拒收
        return any(("拒收" in v) and ('"Failure"' in a or '"Says"' in a)
                   for _, a, v in by_src.get(fname, []))

    KNOWN = [
        ("200kong-as-normal", "工具提议-第一次真跑被拒那发.txt", "样例-工具提议.jsonl"),
        ("protocol-with-200", "协议档-照实记被拒.txt", "样例-协议档-让步装.jsonl"),
        ("config-with-401", "假key一发.txt", "样例-假key一发.jsonl"),
        ("usage-basis-wrong", "agent-自检假端点.txt", "样例-agent.jsonl"),
    ]
    whole = 0
    for name, logf, bookf in KNOWN:
        asm = has_rejected_assembly(logf)
        book = os.path.exists(os.path.join(REPORTS, bookf))
        both = asm and book
        whole += 1 if both else 0
        say("  pair %-20s rejected_assembly_in_log=%-5s corrected_on_book=%-5s -> %s"
            % (name, asm, book, "BOTH-HALFS" if both else "HALF-ONLY"))

    say("")
    say("LEDGERS   turns=%d cells=%d absent_cells=%d" % (n_turns, n_cells, n_absent))
    say("VERDICTS  rows_written=%d known_correction_pairs=%d/%d"
        % (n_verdict_rows, whole, len(KNOWN)))
    say("KEY_SCAN  occurrences_of_sk_in_ledgers=%d (must be 0)" % total_leak)
    io.open(os.path.join(OUT, "summary.txt"), "w", encoding="utf-8").write("\n".join(summary) + "\n")
    say("written: turns_labeled.jsonl verdicts.jsonl summary.txt")
    return 1 if total_leak else 0


if __name__ == "__main__":
    sys.exit(main())
