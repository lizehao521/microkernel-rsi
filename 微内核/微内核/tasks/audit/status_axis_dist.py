# -*- coding: utf-8 -*-
"""在册账本里 `fail_status` 的出处分布，按「这本账是改前还是改后写的」分堆。

为什么这么分堆：不靠文件时间（拷来拷去会漂），靠**内容** —— `wire_format` 这一格
是封板轮才加进键名表的，一本账里有它，就只能是改后落的那本。
改前那一堆应当**一个例外都没有**地全是 measured `0`（那是「没发生」被写成「量到 0」），
改后那一堆里不许再出现 measured 0：没故障的那一发只能 absent 带理由。

控制台只印 ASCII（这台机器的控制台是 GBK），全文另写 UTF-8 的
`报告/状态码轴-在册分布.txt`。
"""
import io, json, glob, os, sys, collections

B = {0: "unset", 1: "measured", 2: "relayed", 3: "estimated", 4: "model_says", 5: "absent"}


def turns_of(path):
    turns = {}
    for raw in io.open(path, encoding="utf-8"):
        raw = raw.strip()
        if not raw:
            continue
        try:
            e = json.loads(raw)
        except Exception:
            continue
        if "kind" not in e or ":" not in e["kind"]:
            continue
        side, key = e["kind"].split(":", 1)
        turns.setdefault((e["step"], side), {})[key] = (e.get("claim"), B.get(e.get("basis"), "?"))
    return turns


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else os.getcwd()
    files = sorted(glob.glob(os.path.join(root, "报告", "样例-*.jsonl")))
    lines, tot, rows = [], collections.Counter(), []
    split = {"new": collections.Counter(), "old": collections.Counter()}
    for i, p in enumerate(files, 1):
        t = turns_of(p)
        out = [v for k, v in t.items() if k[1] == "model_output"]
        d = collections.Counter()
        for v in out:
            if "fail_status" not in v:
                continue
            claim, basis = v["fail_status"]   # turns_of 交的是 (claim, basis)
            d[basis if basis != "measured" else "measured " + str(claim)] += 1
        new = any("wire_format" in v for v in t.values())
        rows.append((i, new, d))
        tot.update(d)
        split["new" if new else "old"].update(d)
        lines.append("led%02d %s turns=%-3d %-30s %s" % (
            i, "NEW" if new else "OLD", len(out), os.path.basename(p), dict(d)))
    lines.append("")
    lines.append("ALL     %s" % dict(tot))
    lines.append("OLD(改前落账) %s   <- 这一堆该是清一色 measured 0" % dict(split["old"]))
    lines.append("NEW(改后落账) %s   <- 这一堆里不许再有 measured 0" % dict(split["new"]))
    bad = ["led%02d 改后的账里又长出 measured 0 ×%d" % (i, d["measured 0"])
           for i, new, d in rows if new and d.get("measured 0")]
    pre = sum(v for k, v in split["old"].items() if k.startswith("measured 0"))
    post = sum(v for k, v in split["new"].items() if k.startswith("measured 0"))
    # 这一条是体检给自己加的：改前那一堆**必须**数得出 measured 0（在册 39 发全是那个长相）。
    # 数出 0 只说明一件事 —— 这个脚本自己读不懂账本了，而不是「形状修好了」。
    # 本脚本第一版就是这么错的：把 (claim, basis) 拆反了，于是「改前 39 个 measured 0」
    # 印成了「改前 0 个」，一份读不懂的读数长得像一份好读数。
    if pre == 0:
        bad.append("PARSE-BROKE: 改前那一堆一个 measured 0 都没数到 —— 是这本脚本读不懂账")
    lines.append("")
    lines.append("SELFTEST pre-change measured-0 count=%d (must be >0)  post-change=%d" % (pre, post))
    out_path = os.path.join(root, "报告", "状态码轴-在册分布.txt")
    with io.open(out_path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(lines) + "\n")
    for ln in lines:
        sys.stdout.buffer.write(ln.encode("ascii", "replace") + b"\n")
    sys.stdout.buffer.write(("STATUS %s ledger_files=%d pre_zero_shapes=%d post_zero_shapes=%d\n" % (
        "BAD" if bad else "OK", len(files), pre, post)).encode("ascii") + b"\n")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
