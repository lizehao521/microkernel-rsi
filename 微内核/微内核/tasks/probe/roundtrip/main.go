// roundtrip —— 只读探针：把一份**出厂字节**按出厂 API 喂回内核自己的装载器。
//
// 为什么要有它：交付件 `构建-1017/客服靶场/导出/账本-全量-1018.jsonl` 的第一行是
// `encode_journal` 交回的人读表头，而"认表头"这件事此前只住在外壳（`mcpsrv.splitEncodeHeader`）。
// 于是绕过外壳、直接调 `kernel.LoadLedger` 的那条路上，那一行被计成坏账、整本拒
// （10-25 改前实测：Lines=2941 / Parsed=2940 / BadLines=1，罪名「这不是空账本，是读坏了」）。
// 「调用方照自家说明回灌」这句话，从来没有一发在真出厂文件上按出厂 API 走过。
//
// 这一件不修任何东西，只把那六个数当场印出来：改前那一发是红的、改后这一发是绿的，两发读数都留盘。
// 它不写盘、不建账本以外的东西，吃的路径由 argv 给（不写死出厂文件在哪，免得它哪天变成第二个 owner）。
package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"agentkernel"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: roundtrip <一份要喂回装载器的账本.jsonl>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: 读不到 %s：%v\n", os.Args[1], err)
		os.Exit(1)
	}
	sum := sha256.Sum256(raw)
	nonEmpty := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(ln) != "" {
			nonEmpty++
		}
	}
	fmt.Printf("文件：%s\n", os.Args[1])
	fmt.Printf("字节 %d ｜ sha256 %x ｜ 非空行 %d（自己按同一个切法数的，用来和内核的分母对账）\n",
		len(raw), sum, nonEmpty)
	first := strings.TrimSpace(strings.Split(string(raw), "\n")[0])
	fmt.Printf("第一行原样：%q\n", first)
	fmt.Printf("内核认不认它是人读表头（kernel.IsEncodeHeader）：%v\n", kernel.IsEncodeHeader(first))

	led, j, rep, err := kernel.LoadLedger(raw)
	fmt.Printf("\nkernel.LoadLedger err：%v\n", err)
	fmt.Printf("Lines=%d Parsed=%d HeaderLines=%d BadLines=%d WithPayload=%d NoPayload=%d GateLines=%d HasGate=%v\n",
		j.Lines, j.Parsed, j.HeaderLines, len(j.BadLines), j.WithPayload, j.NoPayload, j.GateLines, j.HasGate)
	// 对账句：分母必须互相咬得上，否则"读回 2940 条"这句话里有一条是编的。
	fmt.Printf("分母对账：Lines+HeaderLines=%d ｜ 上面自己数的非空行=%d ｜ 相等=%v\n",
		j.Lines+j.HeaderLines, nonEmpty, j.Lines+j.HeaderLines == nonEmpty)
	for i, b := range j.BadLines {
		if i >= 5 {
			fmt.Printf("  （其余 %d 条坏账不逐条印，总数已在上面）\n", len(j.BadLines)-(i))
			break
		}
		fmt.Printf("  坏账 %d：%s\n", i+1, b)
	}
	if j.GateProblem != "" {
		fmt.Printf("  计数栏毛病：%s\n", j.GateProblem)
	}
	fmt.Printf("Journal.String()：%s\n", j.String())
	if led == nil {
		fmt.Printf("没装成账本 ⇒ ChainReport 不作数（内核在装载门口就拒了）：%s\n", rep.String())
		os.Exit(1)
	}
	fmt.Printf("装成账本：%d 条在手\n", len(led.Docs()))
	fmt.Printf("ChainReport：%s ｜ Decidable=%v\n", rep.String(), rep.Decidable())
	if verr := led.Verify(); verr != nil {
		fmt.Printf("Ledger.Verify() 红：%v\n", verr)
		os.Exit(1)
	}
	fmt.Printf("Ledger.Verify() 绿（链自洽；「核不了」那一半由上面 Decidable 说，不由这句代答）\n")
}
