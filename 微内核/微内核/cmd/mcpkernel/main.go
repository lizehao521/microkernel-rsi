// mcpkernel 把内核挂成一个 MCP（Model Context Protocol）服务，stdio 那一面：
// 让**任何一个 agent** 都能直接记账、读账、声明自己是哪一格的读者。
//
// 为什么要挂它：这个包最大的一条批评是「没有消费者」——
// 自造输入证明的是接线，不是口径。而 MCP 把「调用方」这件事从"等框架写好"
// 变成"任何会发 JSON-RPC 的进程现在就是调用方"（包括问它的那个 agent 自己）。
//
// 依赖方向仍然只有一个：**调用方 → 内核**。
// 这一层是外壳，不是包的一部分：它自己管 stdio，
// package kernel 一行都没 import net/http，也没 import os 之外的东西。
//
// 协议本身（方法名、工具清单、每一句回话的措辞）**不在这里** —— 住在 `mcpsrv`。
// 理由与 `tasks/mcp/client.py` 头部记的那条同源：`cmd/mcphttp` 要说的是同一套话，
// 两处各写一遍就会有两套「我以为服务端长什么样」，调用方拿到哪一套取决于它怎么连上的。
//
// 要说清的代价：账本活在**这个进程的记忆里** —— 断开连接即失，
// 而 `Ledger` 没有锁（README 缺口第 6 条），所以这里刻意**一次只处理一条请求**，
// 不做并发；要持久就调 encode_journal 拿字节，写盘仍然是调用方的事。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	"agentkernel/mcpsrv"
)

func main() {
	// 两面共用同一份判定（mcpsrv），这里只是把旗标接上：stdio 面原先一颗旗标都没有，
	// 于是"容量闸只在 HTTP 面可设"成了一条不对称 —— 10-18 深夜记在未闭合，现在补掉。
	maxEntries := flag.Int("max-entries", 0, "这本账最多写多少条；0 = 不拦")
	appendOnly := flag.Bool("append-only", false, "开了就不提供 load_journal（服务面只增）")
	flag.Parse()
	srv := mcpsrv.New()
	srv.SetEntryCap(*maxEntries)
	srv.SetAppendOnly(*appendOnly)
	// 横幅只能走 stderr：stdout 是行分隔 JSON-RPC 的通道，往那儿印一个字就会把客户端的第一帧咬坏。
	fmt.Fprintf(os.Stderr, "mcpkernel（stdio）：%d 个工具；条数上限 %s；只增=%v\n",
		mcpsrv.ToolCount(), mcpsrv.CapWords(*maxEntries), map[bool]string{true: "开", false: "关"}[*appendOnly])
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return
		}
		// 一行读不懂不致命：通知类消息本来就没有 id，也不能让整条会话死掉。
		if out := srv.Serialize(line); out != nil {
			write(os.Stdout, out)
		}
		if err == io.EOF {
			return
		}
	}
}

func write(w io.Writer, b []byte) {
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}
