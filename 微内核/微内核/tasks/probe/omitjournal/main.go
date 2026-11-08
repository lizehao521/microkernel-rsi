// omitjournal —— 冒烟脚本的小工具：把一份带原像的账重新序列化成"没有原像"的那一种。
//
// 为什么要有它：远程审计最危险的读法是"链是绿的"被读成"每一格都对得上原话"。
// 那种形状（PayloadOmitted）此前只在 Go 测试里被验过，冒烟想在真 HTTP 进程里也读到一次，
// 就得先能在盘上造出这样一份文件 —— 手删 payload_b64 是不行的，那会连 hash 一起对不上，
// 造出来的是"被篡改"，不是"核不了"。
package main

import (
	"fmt"
	"os"

	"agentkernel"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "用法: omitjournal <带原像的账.jsonl> <写出去的文件.jsonl>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "omitjournal: 读不到 %s：%v\n", os.Args[1], err)
		os.Exit(1)
	}
	led, _, rep, err := kernel.LoadLedger(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "omitjournal: 这份账不肯被载入：%v\n", err)
		os.Exit(1)
	}
	out, err := kernel.MarshalJournal(led.Docs(), kernel.PayloadOmitted)
	if err != nil {
		fmt.Fprintf(os.Stderr, "omitjournal: 重新序列化失败：%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "omitjournal: 写不出去：%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("omitjournal: 原账 %d 条（比对了 %d 条原像）→ 新文件 %s（一条原像都不带）\n",
		rep.Entries, rep.Checked, os.Args[2])
}
