package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentkernel/mcpsrv"
)

// 判据只有 mcpsrv 那一份。宿主在同一个进程里驱动它，走的是 cmd/mcpkernel/main.go
// 同一条 Serialize 路 —— 不在这里重写 Check，也不起子进程换一套解码。
type kernelRPC struct {
	srv *mcpsrv.Server
	n   int
}

func newKernelRPC(entryCap int, appendOnly bool) *kernelRPC {
	s := mcpsrv.New()
	s.SetEntryCap(entryCap)
	s.SetAppendOnly(appendOnly)
	return &kernelRPC{srv: s}
}

type reply struct {
	Text  string
	IsErr bool
}

func (k *kernelRPC) call(tool string, args map[string]any) (reply, error) {
	k.n++
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      k.n,
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		return reply{}, fmt.Errorf("agentd: 拼 %s 的请求帧就散了：%w", tool, err)
	}
	out := k.srv.Serialize(append(raw, '\n'))
	if len(out) == 0 {
		return reply{}, fmt.Errorf("agentd: %s 没有回帧（第 %d 次调用）", tool, k.n)
	}
	var msg struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &msg); err != nil {
		return reply{}, fmt.Errorf("agentd: %s 的回帧解不开（%v）：%s", tool, err, string(out))
	}
	if msg.Error != nil {
		return reply{Text: msg.Error.Message, IsErr: true}, nil
	}
	if msg.Result == nil || len(msg.Result.Content) == 0 {
		return reply{}, fmt.Errorf("agentd: %s 回了但正文一格都没有：%s", tool, string(out))
	}
	return reply{Text: msg.Result.Content[0].Text, IsErr: msg.Result.IsError}, nil
}

// cellRowsFromReadBack 把 read_back 的整段正文拆成一行一格。
// 行格式（mcpsrv 那侧定的）：%-22s %-9s %s%s —— 键名、出处、人话、形状标签。
type cellRow struct {
	Key   string
	Basis string
	Claim string
}

func cellRowsFromReadBack(text string) []cellRow {
	out := []cellRow{}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimLeft(line, " \t")
		fields := strings.Fields(t)
		if len(fields) < 2 {
			continue
		}
		// 认格的办法是「第二个词是五种出处之一」，不靠键名长什么样：
		// 键名有单字的（folded、parts、sent），按下划线筛会把它们整行丢掉。
		if !isBasisWord(fields[1]) {
			continue
		}
		i := strings.Index(t, fields[1])
		claim := ""
		if i >= 0 {
			claim = strings.TrimSpace(t[i+len(fields[1]):])
		}
		out = append(out, cellRow{Key: fields[0], Basis: fields[1], Claim: claim})
	}
	return out
}

func (r *run) cellsOf(call int, side string) []cellRow {
	if !r.readBack {
		return nil
	}
	rb, err := r.rpc.call("kernel.read_back", map[string]any{"session": r.session, "call": call, "side": side})
	if err != nil || rb.IsErr {
		return nil
	}
	return cellRowsFromReadBack(rb.Text)
}

// cellFromReadBack 从 read_back 的正文里取某一格的出处与人话。行格式由 mcpsrv 定：
//
//	%-22s %-9s %s%s —— 键名、档、人话、形状标签
//
// 10-25 起，行尾多了 ⟦出处=… 指纹=…⟧ 两栏。它们是**渲染**，不是账上的原话：
// 本包 loop.go:763 拿 `model_says_sha256` 那格的原话与重算出来的 sha256 逐字相等
// 判「能不能带着历史续跑」，后缀混进原话就是每一发都"指纹对不上"，而那格账一个字没变。
// 所以人话在交出之前先过 `mcpsrv.StripCellCols` —— 记号归内核外壳定义，这里只引它。
//
// 值只能从这段字节里读出来。读不到就回 false，调用方必须把那一发的依据记成
// 「本地信息，没问内核」—— 不许拿自己内存里那份结构冒充账上的值，
// 否则 KPI 数的是「我们打算读」，不是「账真的决定了这一步」。
func cellFromReadBack(text, key string) (basis, claim string, ok bool) {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(t, key) {
			continue
		}
		rest := t[len(key):]
		if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
			continue // 前缀撞车：fail_kind 不许命中 fail_kind_detail 那一行
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return "", "", false
		}
		i := strings.Index(t[len(key):], f[0])
		claim := ""
		if i >= 0 {
			claim = strings.TrimSpace(mcpsrv.StripCellCols(t[len(key)+i+len(f[0]):]))
		}
		return f[0], claim, true
	}
	return "", "", false
}

func isBasisWord(s string) bool {
	switch s {
	case "measured", "estimated", "relayed", "model_says", "absent":
		return true
	}
	return false
}
