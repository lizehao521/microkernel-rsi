// krsi：两代改进对账 CLI。退出码 0=improved 1=not_improved 2=refused 3=undecidable。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"agentkernel"
	"agentkernel/rsi"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) != 4 {
		fmt.Fprintln(os.Stderr, "用法: krsi <上一代.json> <这一代.json> <报告.txt> <账本.jsonl>")
		return 2
	}
	prev, err := readManifest(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	next, err := readManifest(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	v := rsi.Judge(prev, next)

	l := kernel.NewLedger()
	if b, err := os.ReadFile(args[3]); err == nil {
		ll, _, rep, err := kernel.LoadLedger(b)
		if err != nil {
			fmt.Fprintf(os.Stderr, "账本载入被拒：%v\n", err)
			return 2
		}
		if !rep.Decidable() {
			fmt.Fprintf(os.Stderr, "账本核不了，不许接着记：%s\n", rep.String())
			return 2
		}
		l = ll
	}

	anchorNote := "未锚定（R1 红，结构不可信，不进链）"
	r1Red := v.State == rsi.Refused && len(v.Reasons) > 0 && strings.HasPrefix(v.Reasons[0], "R1:")
	if !r1Red {
		if err := rsi.Anchor(l, prev, next, v); err != nil {
			v.State = rsi.Refused
			v.Reasons = append(v.Reasons, "R6: "+err.Error())
			anchorNote = "锚定被拒"
		} else {
			anchorNote = fmt.Sprintf("链上 %d 条，Verify 过，口径=%s", len(l.Docs()), l.GateStatus().String())
		}
	}

	if len(l.Docs()) > 0 {
		jb, err := kernel.MarshalJournalWithGate(l.Docs(), kernel.PayloadIncluded, l.Gate())
		if err != nil {
			fmt.Fprintf(os.Stderr, "账本编不出去：%v\n", err)
			return 2
		}
		if err := os.WriteFile(args[3], jb, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "账本写不了：%v\n", err)
			return 2
		}
	}
	if err := os.WriteFile(args[2], []byte(buildReport(prev, next, v, anchorNote)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "报告写不了：%v\n", err)
		return 2
	}
	return exitCode(v.State)
}

func readManifest(path string) (rsi.Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return rsi.Manifest{}, err
	}
	var m rsi.Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return rsi.Manifest{}, fmt.Errorf("%s 解不开：%w", path, err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return rsi.Manifest{}, fmt.Errorf("%s 尾部还有多余字节", path)
	}
	return m, nil
}

func basisTally(m rsi.Manifest) string {
	by := map[string]int{}
	for _, r := range m.Readings {
		by[kernel.Basis(r.Basis).String()]++
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, by[k]))
	}
	return fmt.Sprintf("%d 条（%s）", len(m.Readings), strings.Join(parts, " "))
}

func buildReport(prev, next rsi.Manifest, v rsi.Verdict, anchorNote string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "两代改进对账 · krsi\n")
	fmt.Fprintf(&b, "生产者 %s：第 %d 代 → 第 %d 代\n", next.Producer, prev.Generation, next.Generation)
	fmt.Fprintf(&b, "判据目录：声明 %d 条 %v ／ 实现 %d 条 %v\n",
		len(rsi.CriteriaCatalog), rsi.CriteriaCatalog, len(rsi.ImplementedCriteria()), rsi.ImplementedCriteria())
	fmt.Fprintf(&b, "读数：上一代 %s；这一代 %s\n", basisTally(prev), basisTally(next))
	better := 0
	for _, mr := range v.Metrics {
		if mr.Better {
			better++
		}
	}
	fmt.Fprintf(&b, "指标：比到 %d 个，严格变好 %d 个\n", len(v.Metrics), better)
	for _, mr := range v.Metrics {
		fmt.Fprintf(&b, "  · %s：%v → %v（变好=%v）\n", mr.Metric, mr.Before, mr.After, mr.Better)
	}
	fmt.Fprintf(&b, "判定：%s\n", v.State)
	if len(v.Reasons) == 0 {
		b.WriteString("理由：（无 —— 全部指标严格变好）\n")
	}
	for _, r := range v.Reasons {
		fmt.Fprintf(&b, "理由：%s\n", r)
	}
	fmt.Fprintf(&b, "锚定：%s\n", anchorNote)
	fmt.Fprintf(&b, "退出码：%d（0=算数 1=不算数 2=拒收 3=核不了）\n", exitCode(v.State))
	b.WriteString("边界：只答「读数支不支持改进声明」；判据集由生产者交，R3 防放水、不防判据本来就错。\n")
	return b.String()
}

func exitCode(state string) int {
	switch state {
	case rsi.Improved:
		return 0
	case rsi.NotImproved:
		return 1
	case rsi.Undecidable:
		return 3
	default:
		return 2
	}
}
