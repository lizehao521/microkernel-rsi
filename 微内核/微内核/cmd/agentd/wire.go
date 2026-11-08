package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"agentkernel"
)

// 两侧的键名照 contract.md §2.5 那份 wire 形状填。这一层不另立形状，也不许把读侧格名
// （content_bytes）当写侧字段名（ContentBytes）用 —— 10-17 冷接实测过：拿格名当字段名的
// 那一发被静默吞掉，闸门回的是「整发落账 49 条、被拒的写入 0 次」。

const (
	callCeiling = int64(8192)
	slackPct    = 10
	noticeText  = "本节往轮轨迹已被折成桩，原文不在这一发里。"
	historyStub = "[历史桩｜原文指纹 %s｜原文 %d 字节]"
)

var shares = map[kernel.PartKind]int{
	kernel.KindSystem:   15,
	kernel.KindToolSpec: 25,
	kernel.KindHistory:  15,
	kernel.KindUser:     20,
	kernel.KindObserved: 15,
	kernel.KindNotice:   10,
}

type part struct {
	kind kernel.PartKind
	body string
}

// assembly 是一发要装的东西。history 非空 = 这一发真带往轮轨迹；foldHistory 为真时
// 发出去的是桩，原文只在裁剪表里留下指纹与字节数 —— 这条是账上「挤过历史」的唯一证据。
type assembly struct {
	system        string
	toolspec      string
	user          string
	observation   string
	history       string
	historyOrig   string
	foldHistory   bool
	historyRefCal int
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (a *assembly) segs() []part {
	out := []part{{kernel.KindSystem, a.system}, {kernel.KindToolSpec, a.toolspec}}
	if a.history != "" {
		out = append(out, part{kernel.KindHistory, a.history})
	}
	if a.foldHistory {
		out = append(out, part{kernel.KindNotice, noticeText})
	}
	out = append(out, part{kernel.KindUser, a.user})
	if a.observation != "" {
		out = append(out, part{kernel.KindObserved, a.observation})
	}
	return out
}

func (a *assembly) buildInput(call int) (kernel.InputMeta, []byte, error) {
	segs := a.segs()
	order := make([]kernel.PartKind, 0, len(segs))
	for _, s := range segs {
		order = append(order, s.kind)
	}
	var content int64
	seen := map[kernel.PartKind]bool{}
	parts := make([]kernel.PartCount, 0, len(segs))
	for _, s := range segs {
		content += int64(len(s.body))
		if seen[s.kind] {
			for i := range parts {
				if parts[i].Kind == s.kind {
					parts[i].Parts++
					parts[i].Bytes += int64(len(s.body))
				}
			}
			continue
		}
		seen[s.kind] = true
		parts = append(parts, kernel.PartCount{Kind: s.kind, Parts: 1, Bytes: int64(len(s.body))})
	}
	budget, err := kernel.DeriveBudget(callCeiling, slackPct, shares,
		kernel.EstimatedOf(128, "agentd：骨架+模型名+工具清单的固定开销，本地估的"))
	if err != nil {
		return kernel.InputMeta{}, nil, err
	}
	m := kernel.InputMeta{
		Call:         call,
		Order:        order,
		Parts:        parts,
		ContentBytes: content,
		Tokens:       kernel.EstimatedOf(content/4+int64(len(order)), "agentd 本地粗算（字节/4+段数）；分词器在对端，这不是测量"),
		Budget:       budget,
	}
	if a.foldHistory {
		m.Cuts = []kernel.Cut{{
			Kind:    kernel.KindHistory,
			Ref:     "往轮轨迹（第 " + strconv.Itoa(a.historyRefCal) + " 发的回答）",
			RefCall: a.historyRefCal,
			Role:    kernel.RoleAssistant, Squeeze: kernel.SqueezeFolded,
			FromBytes: int64(len(a.historyOrig)), ToBytes: int64(len(a.history)), SHA256: sha(a.historyOrig),
		}}
		m.Notice = kernel.Notice{Sent: true, Why: "预算被上一发的失败挤占，折历史而不是整段丢历史", Text: noticeText}
	} else {
		m.Notice = kernel.Notice{}
	}
	body, err := json.Marshal(map[string]any{
		"model":    fixtureID,
		"messages": order,
		"bytes":    content,
	})
	if err != nil {
		return kernel.InputMeta{}, nil, err
	}
	return m, body, nil
}

// proposal 是驱动（端点那一侧）交回来的一发。剧本里这些数全是 fixture 自报或本地数出来的，
// 所以下面每一格的出处都按那个来写，不许因为「跑绿了」就升成 measured。
type proposal struct {
	Text         string
	ToolCalls    int
	Malformed    int
	Streak       int
	Fail         kernel.FailKind
	Detail       string
	Status       int  // 0 = 这一类压根没收到对端响应
	RetryAfterMs int  // -1 = 对端没说
	ReportsModel bool // 对端这一发的体我们读得出来，所以它自报的 id 有来源
	FinishSaid   bool
	FinishReason string
	UsageIn      int64 // 对端自报的入 token
	UsageOut     int64
	Model        string // 对端自报的模型 id 原字；空 = 它没说
	// Sent 说的是「这一发真过了网线没有」。剧本那一路不开 socket，于是过网线字节
	// 只能是**缺席 + 理由**：把我们自己编码出来的字节署成 measured 的「过了网线」，
	// 就是把「我们打算发」写成「我们量到发了」。
	Sent    bool
	BytesIn int64 // 真在通道上数出来的收字节；<0 = 没数到
	Stream  bool  // 这一发真是分帧回来的（只有开了 socket 才可能是 true）
	Chunks  int64 // 在通道上数到的帧数
	FirstMS int64 // 首帧等待（毫秒）
	MaxGap  int64 // 最大帧间等待（毫秒）
	// WireFormat 是**对端自报**的格式原字（响应体顶层 object）。非空才说它报了。
	WireFormat string
}

func buildOutput(call int, p proposal, reqBody []byte) kernel.OutputMeta {
	// 「对端说的」这一族只能转述，来源名必须是它自报的那串字；剧本没有就说剧本。
	peer := p.Model
	if peer == "" {
		peer = fixtureID
	}
	m := kernel.OutputMeta{
		Call:         call,
		Streaming:    false,
		Chunks:       kernel.AbsentBecause("非流式：这一发没有分帧这回事"),
		FirstByteMs:  kernel.AbsentBecause("非流式：没有首帧"),
		MaxGapMs:     kernel.AbsentBecause("非流式：没有帧间"),
		WireBytesOut: kernel.AbsentBecause("这一发没开 socket，一个字节都没过网线（剧本，不是真端点）"),
		ToolCalls:    p.ToolCalls,
		Malformed:    p.Malformed,
		Says:         kernel.Says{Bytes: int64(len(p.Text)), Kept: int64(len(p.Text)), SHA256: sha(p.Text)},
	}
	gotReply := p.Fail == kernel.FailNone || p.Fail.MayCarryStatus()
	if p.Stream {
		// 分帧三格只有在真开 socket 的那一路才允许有数：
		// 非流式写数是编数，流式报缺席是把该量到的数量丢了 —— 内核两头都判。
		if !p.Sent {
			panic("agentd: 没开 socket 的一发不许声称是分帧回来的")
		}
		m.Streaming = true
		m.Chunks = kernel.MeasuredOf(p.Chunks)
		m.FirstByteMs = kernel.MeasuredOf(p.FirstMS)
		m.MaxGapMs = kernel.MeasuredOf(p.MaxGap)
	}
	if p.Sent {
		m.WireBytesOut = kernel.MeasuredOf(int64(len(reqBody)))
	}
	if gotReply && p.Sent && p.BytesIn >= 0 {
		m.WireBytesIn = kernel.MeasuredOf(p.BytesIn)
	} else if gotReply && p.Sent {
		m.WireBytesIn = kernel.AbsentBecause("发了也收到状态了，但这一发没在通道上数出字节")
	} else if gotReply {
		m.WireBytesIn = kernel.AbsentBecause("这一发没开 socket：回话是本地剧本给的字符串，不在任何通道上")
	} else {
		m.WireBytesIn = kernel.AbsentBecause("这一类按定义没收到对端响应，收字节无从数起")
	}
	if p.UsageIn > 0 {
		m.UsageIn = kernel.RelayedOf(p.UsageIn, peer)
	} else {
		m.UsageIn = kernel.AbsentBecause("这一发没有可对端报的入 token：" + failReason(p.Fail))
	}
	if p.UsageOut > 0 {
		m.UsageOut = kernel.RelayedOf(p.UsageOut, peer)
	} else {
		m.UsageOut = kernel.AbsentBecause("这一发没有可对端报的出 token：" + failReason(p.Fail))
	}
	switch p.Fail {
	case kernel.FailNone:
		// 成功那一发也照对端自报的结束原因记：带工具调用的那一发是 tool_calls，
		// 写死 "stop" 会让账上「说 stop 却带了工具调用」—— 那一格就是假的。
		reason := p.FinishReason
		if reason == "" {
			reason = "stop"
		}
		m.Finish = kernel.Finish{Reason: reason, Said: true}
		m.Model = peer
		m.ModelSaid = true
		m.WireFormat = p.WireFormat
		if m.WireFormat == "" {
			m.WireFormat = "chat.completion"
		}
		m.WireFormatSaid = true
		m.Failure = kernel.Failure{
			Status:       kernel.AbsentBecause("正常收尾，没有故障状态码可记"),
			RetryAfterMs: kernel.AbsentBecause("没故障就不该有「等多久」"),
		}
	default:
		f := kernel.Failure{Kind: p.Fail, Detail: p.Detail}
		if p.Status > 0 {
			f.Status = kernel.RelayedOf(int64(p.Status), peer)
		} else {
			f.Status = kernel.AbsentBecause("这一类按定义没收到对端响应，状态码没有来源")
		}
		if p.RetryAfterMs >= 0 && p.Fail.RetrySame() {
			f.RetryAfterMs = kernel.RelayedOf(int64(p.RetryAfterMs), peer)
		} else {
			f.RetryAfterMs = kernel.AbsentBecause("对端没说等多久")
		}
		if p.Fail == kernel.FailBudget {
			m.WireBytesOut = kernel.MeasuredOf(0)
		}
		if p.ReportsModel {
			m.Model = peer
			m.ModelSaid = true
			m.WireFormat = p.WireFormat
			if m.WireFormat == "" {
				m.WireFormat = "chat.completion"
			}
			m.WireFormatSaid = m.WireFormat != ""
		}
		if p.FinishSaid {
			m.Finish = kernel.Finish{Reason: p.FinishReason, Said: true}
		}
		m.Failure = f
	}
	if p.Malformed > 0 {
		m.MalformedStreak = p.Streak
	}
	return m
}

func failReason(k kernel.FailKind) string {
	if k == kernel.FailNone {
		return "fixture 没报用量"
	}
	return string(k) + " 这一发没有可对端报的用量"
}

var fixtureID = "scripted-fixture（本地剧本，不是真端点）"

func sides(session string, in kernel.InputMeta, out kernel.OutputMeta) map[string]any {
	return map[string]any{"session": session, "actor": "agentd", "input": in, "output": out}
}
