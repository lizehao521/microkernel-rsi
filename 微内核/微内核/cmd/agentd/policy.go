package main

import (
	"strings"

	"agentkernel"
)

// POLICY 是 tasks/mcp/agent.py:53-60 那一份的形状搬进 Go —— 逐条抄，不改口径。
// 每一条的依据都写明「住在账上哪一格」，动作由 read_back 读回来的值触发。
type policyRule struct {
	Action string
	Why    string
}

var POLICY = map[kernel.FailKind]policyRule{
	kernel.FailTransport:       {"重发原样一次", "账上 fail_kind=transport：这一类按定义没收到对端响应，原样再发有用"},
	kernel.FailHTTP:            {"等一等再原样发", "账上 fail_kind=http：对端忙/断了，RetrySame 那一族"},
	kernel.FailConfig:          {"停 —— 重发只会再吃一次同样的拒绝", "账上 fail_kind=config：密钥、模型名、参数这一类"},
	kernel.FailProtocol:        {"停 —— 我们对不上话，再发也是", "账上 fail_kind=protocol"},
	kernel.FailMalformedAction: {"换个说法再问一次", "账上 fail_kind=malformed_action：这一发没做成动作"},
	kernel.FailBudget:          {"挤历史再发", "账上 fail_kind=budget：额度不够，不是对端的错"},
}

const noFailureAction = "没有故障：接着算下一发"

// normalizeFail 把 read_back 那一格的人话收回成故障类别。
// 内核把「没有故障」渲染成空或「（这一格是空的）」—— 这两种都是 FailNone，不是别的。
func normalizeFail(v string) kernel.FailKind {
	s := strings.TrimSpace(v)
	if s == "" || strings.HasPrefix(s, "（") {
		return kernel.FailNone
	}
	return kernel.FailKind(s)
}

// driftOf 是那条「读回来的值 vs 当场给的类别」的比较，单独住在这儿才测得动：
// 正常路径上两者永远相等，所以它的反臂只能直接喂一对不一致的进去。
func driftOf(readValue string, local kernel.FailKind) bool {
	return normalizeFail(readValue) != local
}
