package kernel

import (
	"strings"
	"testing"
)

// validOutput 造一份流式、正常收尾、处处自洽的输出侧账。
func validOutput(t *testing.T) OutputMeta {
	t.Helper()
	return OutputMeta{
		Call:         1,
		Streaming:    true,
		Finish:       Finish{Reason: "stop", Said: true},
		Model:        "agnes-1",
		ModelSaid:    true,
		Says:         Says{Bytes: 900, Kept: 900, SHA256: sha("模型这一发的原话")},
		UsageIn:      RelayedOf(1587, "endpoint:agnes"),
		UsageOut:     RelayedOf(244, "endpoint:agnes"),
		WireBytesOut: MeasuredOf(11570),
		WireBytesIn:  MeasuredOf(4096),
		Chunks:       MeasuredOf(37),
		FirstByteMs:  MeasuredOf(921),
		MaxGapMs:     MeasuredOf(1200),
		ToolCalls:    1,
		Failure: Failure{RetryAfterMs: AbsentBecause("这一发没故障"),
			// 没故障的那一发**没有状态码可量**（这一发按定义没故障）——
			// 以前这一格被写死成 measured，于是每一发正常往来都在账上说「量到状态码 0」。
			Status: AbsentBecause("这一发没故障，也就没有要记的状态码")},
	}
}

// 非流式那一发：三格必须是缺席，不是 0。
func validNonStreaming(t *testing.T) OutputMeta {
	t.Helper()
	m := validOutput(t)
	m.Streaming = false
	m.Chunks = AbsentBecause("这一发不分帧")
	m.FirstByteMs = AbsentBecause("这一发不分帧")
	m.MaxGapMs = AbsentBecause("这一发不分帧")
	return m
}

// ① 这一条是整个输出侧最重要的一条：非流式那一发的分帧数写成 0，
// 会被读成「一帧都没收到」—— 而那恰好是协议错的判据。两头都要判。
func TestNonStreamingCellsAreAbsentNotZero(t *testing.T) {
	m := validNonStreaming(t)
	if err := m.Check(); err != nil {
		t.Fatalf("非流式那一发（三格缺席）该被收：%v", err)
	}
	// 坏的一头：非流式却报了一个 0。
	bad := validNonStreaming(t)
	bad.Chunks = MeasuredOf(0)
	if err := bad.Check(); err == nil {
		t.Errorf("非流式那一发的分帧数写成 0 竟被收了 —— 0 会被读成一帧都没收到")
	} else if !strings.Contains(err.Error(), "分帧数") {
		t.Errorf("那句话没点名是哪一格：%v", err)
	}
	// 坏的另一头：流式却报缺席（该量到的数被丢了）。
	bad2 := validOutput(t)
	bad2.FirstByteMs = AbsentBecause("忘了接")
	if err := bad2.Check(); err == nil {
		t.Errorf("流式那一发的首帧等待报缺席竟被收了")
	} else if !strings.Contains(err.Error(), "流式") {
		t.Errorf("那句话没说清理由：%v", err)
	}
	// 缺席那三格渲染出来都不像 0。
	for _, s := range []string{m.Chunks.String(), m.FirstByteMs.String(), m.MaxGapMs.String()} {
		if !strings.Contains(s, "没量到") {
			t.Errorf("缺席那一格渲染成了 %q", s)
		}
	}
}

// ② 两个字节量必须是两个名字。这是 runloop 今天真实存在的一处缺陷：
// 请求侧的「装配后内容字节」与响应侧的「真过网线字节」共用 bytes_out 一个键，
// 实测差过 6327 → 11570（JSON 框架与转义），任何按键名扫账本的分析层都会把它们加成一堆。
//
// 这一条**调用**内核那把撞名判据，而不是在测试里重写一遍 ——
// 重写一遍的话，把那把判据改成永远返回空，这条测试照样绿（探针 m16 抓到过这个形状）。
func TestWireBytesAndContentBytesAreTwoNames(t *testing.T) {
	in := validInput(t)
	out := validOutput(t)
	all := append(append([]Cell{}, in.Cells()...), out.Cells()...)
	if got := KeyCollisions(all); len(got) > 0 {
		t.Errorf("有键名住了两个量：%v", got)
	}
	keys := map[string]bool{}
	for _, c := range all {
		if c.Key == "bytes_out" {
			t.Errorf("内核里不该再出现 bytes_out 这个键名（它装过两个量），来自 %s", c.Field)
		}
		keys[c.Key] = true
	}
	for _, want := range []string{"content_bytes", "wire_bytes_out"} {
		if !keys[want] {
			t.Errorf("键名表里少了 %s", want)
		}
	}
	// 两个量在同一发里可以差很多，而这份差值本身就是信息（框架膨胀了多少）。
	if out.WireBytesOut.N <= in.ContentBytes {
		t.Errorf("夹具里过网线 %d 该大于内容 %d —— 不然这条判据没在真形状上跑过",
			out.WireBytesOut.N, in.ContentBytes)
	}
	// 反证：照 runloop 今天的样子把两个字节量都叫 bytes_out，内核那把判据要当场撞出来。
	bad := []Cell{
		cell("bytes_out", "ContentBytes", int64(6327), BasisMeasured),
		cell("bytes_out", "WireBytesOut", int64(11570), BasisMeasured),
	}
	got := KeyCollisions(bad)
	if len(got) != 1 {
		t.Fatalf("该撞出 1 处，得到 %d 处：%v", len(got), got)
	}
	for _, want := range []string{"bytes_out", "ContentBytes", "WireBytesOut"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("那句话少了 %q：%s", want, got[0])
		}
	}
	// 正面对照：两侧共用 call 是同一个量，不该算撞名。先确认它真出现在两侧，
	// 不然这条对照是空的。
	var inHas, outHas bool
	for _, c := range in.Cells() {
		if c.Key == "call" {
			inHas = true
		}
	}
	for _, c := range out.Cells() {
		if c.Key == "call" {
			outHas = true
		}
	}
	if !inHas || !outHas {
		t.Fatalf("call 没有同时出现在两侧（输入 %v / 输出 %v），这条对照是空的", inHas, outHas)
	}
	same := []Cell{cell("call", "Call", 1, BasisMeasured), cell("call", "Call", 1, BasisMeasured)}
	if got := KeyCollisions(same); len(got) != 0 {
		t.Errorf("同名同字段是同一个量，不该算撞名：%v", got)
	}
}

// ③ 对端自报的 token 数只能记转述。闸是**白名单**而不是黑名单：
// 写成「不许实测」的话，估算与模型自述会从旁边溜过去，而字段注释正在说「只能是转述」。
func TestUsageIsRelayedNotMeasured(t *testing.T) {
	for _, bad := range []struct {
		name string
		m    Measure
	}{
		{"实测", MeasuredOf(1587)},
		{"估算", EstimatedOf(1587, "夹具：拿字节数除四")},
		{"模型自述", Measure{N: 1587, Basis: BasisModelSays}},
	} {
		m := validOutput(t)
		m.UsageIn = bad.m
		if err := m.Check(); err == nil {
			t.Errorf("入 token 记成%s竟被收了 —— 分词器在对端，我们只能转述", bad.name)
		} else if !strings.Contains(err.Error(), "分词器") {
			t.Errorf("那句话没说清为什么不行：%v", err)
		}
	}
	m := validOutput(t)
	m.UsageOut = AbsentBecause("对端没回 usage")
	if err := m.Check(); err != nil {
		t.Errorf("对端没回 usage 就是缺席，该收：%v", err)
	}
	m = validOutput(t)
	m.UsageOut = Distrusted("对端自报的数不可能是真的", 900000000)
	if err := m.Check(); err != nil {
		t.Errorf("被判成不可信也是缺席的一种，该收：%v", err)
	}
	// 收字节是我们自己在通道上数的，只能记实测 —— 反方向也要钉住。
	m = validOutput(t)
	m.WireBytesIn = RelayedOf(4096, "endpoint")
	if err := m.Check(); err == nil {
		t.Errorf("收字节记成转述竟被收了")
	}
	m = validOutput(t)
	m.WireBytesOut = RelayedOf(11570, "endpoint")
	if err := m.Check(); err == nil {
		t.Errorf("过网线字节是我们编码出来再数的，记成转述竟被收了")
	}
}

// ④ 「这一发有没有花出去」是三态，不是一个布尔。
// 裸布尔会把「预算挡下，一个字节都没发」与「管路在计数回来之前断了，我们不知道发了多少」
// 压成同一个 false，而前者是 0 花费、后者可能已经花了钱。
func TestSentIsThreeState(t *testing.T) {
	if got := validOutput(t).Sent(); got != SentSomething {
		t.Errorf("过网线 11570 字节该是「发出去了」，得到 %s", got)
	}
	m := validOutput(t)
	m.WireBytesOut = MeasuredOf(0)
	m.Failure = Failure{Kind: FailBudget, Detail: "累计字节到顶，这一发没发出去",
		RetryAfterMs: AbsentBecause("不该重发")}
	m.Says, m.ToolCalls = Says{SHA256: sha("")}, 0
	if err := m.Check(); err != nil {
		t.Fatalf("预算挡下、0 字节过网线，该被收：%v", err)
	}
	if got := m.Sent(); got != SentNothing {
		t.Errorf("数到的 0 该是「没发出去」，得到 %s", got)
	}
	if m.CostCountable() {
		t.Errorf("没发出去的那一发不该进成本核算")
	}
	// 没数到 —— 管路在计数回来之前断了。这一格不许冒充「一个字节都没发」。
	unknown := validOutput(t)
	unknown.WireBytesOut = AbsentBecause("连接在计数回来之前断了")
	if got := unknown.Sent(); got != SentUnknown {
		t.Errorf("没数到该是第三态，得到 %s", got)
	}
	if strings.Contains(unknown.Sent().String(), "没发出去（数到了") {
		t.Errorf("第三态渲染成了「没发出去」：%s", unknown.Sent())
	}
	if unknown.CostCountable() {
		t.Errorf("连发没发出去都不知道，算出来的成本没有意义")
	}
	// 预算挡下的那一发必然是数到的 0 —— 预算闸在发之前就拦，不可能「没数到」。
	bad := validOutput(t)
	bad.WireBytesOut = AbsentBecause("不知道")
	bad.Failure = Failure{Kind: FailBudget, Detail: "到顶了", RetryAfterMs: AbsentBecause("x")}
	bad.Says, bad.ToolCalls = Says{SHA256: sha("")}, 0
	if err := bad.Check(); err == nil {
		t.Errorf("报的是预算挡下、过网线字节却记成没数到，竟被收了")
	} else if !strings.Contains(err.Error(), "必然是数到的 0") {
		t.Errorf("那句话没说清为什么这一格不可能是缺席：%v", err)
	}
	// 报预算挡下却真有字节过网线。
	bad2 := validOutput(t)
	bad2.Failure = Failure{Kind: FailBudget, Detail: "到顶了", RetryAfterMs: AbsentBecause("x")}
	if err := bad2.Check(); err == nil {
		t.Errorf("报的是预算挡下、却有 %d 字节过了网线，竟被收了", bad2.WireBytesOut.N)
	}
}

// ⑤ 故障那一格报红时必须说得出下一个人该去哪儿查。
// TestFailureNeedsADetailAndOnlyHTTPCarriesAStatus 判的是两根轴，不是一根：
// 「收到过对端响应没有」决定能不能带状态码，「原样重发有没有用」决定 RetrySame。
// 旧版用 kind==http 一根轴冒充，两发真数据把它撞开了（config/401 与 protocol/200）。
func TestFailureNeedsADetailAndOnlyHTTPCarriesAStatus(t *testing.T) {
	m := validOutput(t)
	m.Says = Says{SHA256: sha("")}
	m.Failure = Failure{Kind: FailTransport, RetryAfterMs: AbsentBecause("对端没说")}
	if err := m.Check(); err == nil {
		t.Errorf("报了 transport 却没说是什么，竟被收了")
	}
	m.Failure.Detail = "读响应头时连接被对端关掉"
	m.Failure.Status = AbsentBecause("管路没通，压根没收到响应")
	if err := m.Check(); err != nil {
		t.Errorf("transport 带着缺席的状态码该收：%v", err)
	}
	m.Failure.Status = MeasuredOf(429)
	if err := m.Check(); err == nil {
		t.Errorf("transport 压根没收到对端响应，却记着量到的状态码 429，竟被收了")
	}
	// 轴的另一头：config 是**收到过**响应的，真 401 必须装得进来。
	m.Failure = Failure{Kind: FailConfig, Detail: "密钥不对", Status: MeasuredOf(401),
		RetryAfterMs: AbsentBecause("对端没给 Retry-After")}
	if err := m.Check(); err != nil {
		t.Errorf("config 带着真 401 该收（旧互核把这一发挡在外面，实测见报告/假key一发-401.md）：%v", err)
	}
	m.Failure.Status = AbsentBecause("没收到状态码")
	if err := m.Check(); err == nil {
		t.Errorf("config 这一类是按对端响应分的，却没状态码，竟被收了")
	}
	// 0 不再是"没带码"的合法长相：它以前正是每一发的默认形状，而 HTTP 没有 0 这个码。
	m.Failure.Status = MeasuredOf(0)
	if err := m.Check(); err == nil {
		t.Errorf("状态码 0 不是 HTTP 的数，竟被收了 —— 缺席得用 absent 那一档说")
	}
	m.Failure.Status = MeasuredOf(600)
	if err := m.Check(); err == nil {
		t.Errorf("状态码 600 越界，竟被收了")
	}
	// 没故障却带着说法，同样是矛盾。
	m = validOutput(t)
	m.Failure = Failure{Detail: "其实有点问题", RetryAfterMs: AbsentBecause("x"),
		Status: AbsentBecause("没故障")}
	if err := m.Check(); err == nil {
		t.Errorf("说是没故障却带着说法，竟被收了")
	}
	m = validOutput(t)
	m.Failure = Failure{Status: MeasuredOf(500), RetryAfterMs: AbsentBecause("x")}
	if err := m.Check(); err == nil {
		t.Errorf("说是没故障却带着状态码，竟被收了")
	}
	m = validOutput(t)
	m.Failure = Failure{Status: MeasuredOf(500), Detail: "有码却没归类",
		RetryAfterMs: AbsentBecause("x")}
	if err := m.Check(); err == nil {
		t.Errorf("没故障那一档带码，该拒（这一条与上一条同族，但走的是 MayCarryStatus 那一支）")
	}
	// 说法首尾不许带空白：它会被抄进日志，也会被人拿去比对。
	// 慢闸针 m33 第一次跑这条时报 VACUOUS（破坏落上了、测试却仍绿）：
	// 这一份原来是 `FailProtocol` 不带状态码 —— 那条按类别的互核**先**把它拒了，
	// 于是"被拒"这件事并不是空白那条判据干的，断言只判 `err == nil` 就看不出差别。
	// 修法是把这一份别处都装合法（状态码照轴给上），并且**认报错里那句理由**。
	m = validOutput(t)
	m.Failure = Failure{Kind: FailProtocol, Detail: " 巨帧超过上限 ", Status: MeasuredOf(200),
		RetryAfterMs: AbsentBecause("x")}
	m.Says = Says{SHA256: sha("")}
	err := m.Check()
	if err == nil {
		t.Errorf("说法首尾带空白竟被收了")
	} else if !strings.Contains(err.Error(), "首尾带空白") {
		t.Errorf("空白那条判据没被这条输入触发，拒它的是另一条：%v", err)
	}
}

// ⑥ 陌生的故障类别是一个新形状正在被当成旧的读 —— 必须当场拒，
// 且报出来的话要带上「一共几种取值、都是哪些」这个分母。
func TestUnknownFailKindIsRefused(t *testing.T) {
	m := validOutput(t)
	m.Says = Says{SHA256: sha("")}
	m.Failure = Failure{Kind: FailKind("timeout"), Detail: "超时了", RetryAfterMs: AbsentBecause("x")}
	err := m.Check()
	if err == nil {
		t.Fatalf("陌生的故障类别 timeout 竟被收了")
	}
	for _, want := range []string{"timeout", "transport", "malformed_action"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("那句话少了 %q（它该把取值表一起说出来，读的人才知道该往哪儿归）：%v", want, err)
		}
	}
	// 那句「几种取值」必须由表自己数出来，不许写死在文案里 ——
	// 写死的话，加一种故障就会让文案变成假话（这里曾经写着「六种」而表里有七个）。
	if !strings.Contains(err.Error(), "7 种") {
		t.Errorf("文案里的取值个数该由 failKinds 自己数出来（7 = 六种故障 + 一个没故障），得到：%v", err)
	}
	for _, k := range failKinds {
		if !k.Known() {
			t.Errorf("%s 该在取值表里", k)
		}
		m = validOutput(t)
		if k == FailNone {
			if err := m.Check(); err != nil {
				t.Errorf("没故障该收：%v", err)
			}
			continue
		}
		m.Says = Says{SHA256: sha("")}
		m.Failure = Failure{Kind: k, Detail: "说法", RetryAfterMs: AbsentBecause("对端没说")}
		if k.MayCarryStatus() {
			m.Failure.Status = MeasuredOf(500)
		} else {
			m.Failure.Status = AbsentBecause("这一类没收到对端响应")
		}
		if k == FailBudget {
			m.WireBytesOut = MeasuredOf(0)
			m.ToolCalls = 0
		}
		if err := m.Check(); err != nil {
			t.Errorf("%s 是取值表里的，该被收：%v", k, err)
		}
	}
	if FailKind("timeout").Known() {
		t.Errorf("timeout 不该被认成合法类别")
	}
}

// ⑦ 「同样的请求原样再发一次」与「换个请求再发一次」是两个问题，所以住在两个地方。
// 共用一个字段的话，改一处故障分类会顺手关掉另一种恢复能力。
//
// 这一条走过门口（Check），不是在测试里自己搭一个结构体再读它自己的字段 ——
// 后者是重言式：读的是测试两行前写进去的值，中间没有任何生产代码，无论怎么改都不会红。
func TestRetrySameAndDowngradeAreTwoQuestions(t *testing.T) {
	if !FailTransport.RetrySame() || !FailHTTP.RetrySame() {
		t.Errorf("transport 与 http 都该能原样重发")
	}
	for _, k := range []FailKind{FailConfig, FailProtocol, FailMalformedAction, FailBudget, FailNone} {
		if k.RetrySame() {
			t.Errorf("%s 原样重发不会变好，不该说能重发", k)
		}
	}
	// config 那一类（例如 400）原样重发没用，但换成非流式值得试 —— 两个答案同时成立。
	m := validOutput(t)
	m.Says = Says{SHA256: sha("")}
	m.Failure = Failure{Kind: FailConfig, Detail: "对端不接受 stream=true", Downgrade: true,
		Status: MeasuredOf(400), RetryAfterMs: AbsentBecause("不该原样重发")}
	if err := m.Check(); err != nil {
		t.Errorf("原样重发没用、但值得换成非流式，这一份该被收：%v", err)
	}
	if m.Failure.Kind.RetrySame() {
		t.Errorf("这一发不该说能原样重发")
	}
	if !m.Failure.Downgrade {
		t.Errorf("这一发该说值得换个请求重发")
	}
	// 把 Downgrade 关掉，同一份账仍然该被收 —— 两轴互不牵动。
	m.Failure.Downgrade = false
	if err := m.Check(); err != nil {
		t.Errorf("Downgrade 关掉之后同一份账仍该被收：%v", err)
	}
	// 本来就不是流式的那一发，「换成非流式再发」无从谈起。
	// 慢闸针 m34 第一次整轮也报 VACUOUS：这一份原来给 `FailConfig` 却没带状态码，
	// 于是先被状态码那条互核拒掉，断言 `err == nil` 看不出是谁拒的 —— 同一味药：
	// 别处装合法，并且**认报错里那句理由**。
	m = validNonStreaming(t)
	m.Says = Says{SHA256: sha("")}
	m.Failure = Failure{Kind: FailConfig, Detail: "对端不接受这个参数", Downgrade: true,
		Status: MeasuredOf(400), RetryAfterMs: AbsentBecause("x")}
	if err := m.Check(); err == nil {
		t.Errorf("对一发本来就不是流式的响应建议「换成非流式再发」，竟被收了")
	} else if !strings.Contains(err.Error(), "本来就不是流式") {
		t.Errorf("非流式不许建议降级那条判据没被这条输入触发，拒它的是另一条：%v", err)
	}
	// 没故障却建议换个请求重发，同样是矛盾。
	m = validOutput(t)
	m.Failure = Failure{Downgrade: true, RetryAfterMs: AbsentBecause("x")}
	if err := m.Check(); err == nil {
		t.Errorf("说是没故障却又建议重发，竟被收了")
	}
	// 原样重发不会变好的类别，却记着「等 N 毫秒再发」⇒ 拒。
	m = validOutput(t)
	m.Says = Says{SHA256: sha("")}
	m.Failure = Failure{Kind: FailProtocol, Detail: "巨帧超过上限", RetryAfterMs: MeasuredOf(2000)}
	if err := m.Check(); err == nil {
		t.Errorf("protocol 原样重发只会拿回同一个巨帧，却记着等 2000 毫秒，竟被收了")
	}
	// 没故障却记着要等多久，同样是矛盾。
	m = validOutput(t)
	m.Failure = Failure{RetryAfterMs: MeasuredOf(2000)}
	if err := m.Check(); err == nil {
		t.Errorf("说是没故障却记着等 2000 毫秒，竟被收了")
	}
	m = validOutput(t)
	m.Failure = Failure{Kind: FailHTTP, Detail: "429", Status: MeasuredOf(429),
		RetryAfterMs: RelayedOf(2000, "Retry-After 头")}
	if err := m.Check(); err != nil {
		t.Errorf("429 带着对端给的等待时长该收：%v", err)
	}
	m = validOutput(t)
	m.Failure = Failure{Kind: FailHTTP, Detail: "429", Status: MeasuredOf(429), RetryAfterMs: RelayedOf(-1, "Retry-After 头")}
	if err := m.Check(); err == nil {
		t.Errorf("对端说要等 -1 毫秒竟被收了")
	}
}

// ⑧ 端点没说，与端点说了个空字符串，是两件事 —— 收尾原因与模型 id 两格同形，都要有旗标。
func TestSaidVersusEmptyString(t *testing.T) {
	for _, f := range []Finish{{Reason: "", Said: true}, {Reason: "stop", Said: false}} {
		m := validOutput(t)
		m.Finish = f
		if err := m.Check(); err == nil {
			t.Errorf("收尾原因那一格自相矛盾（%+v）竟被收了", f)
		}
	}
	m := validOutput(t)
	m.Finish = Finish{}
	if err := m.Check(); err != nil {
		t.Errorf("端点没说，就是没说，该收：%v", err)
	}
	// 刻意不归一化成枚举：换一家端点会多出一个我们没见过的值，
	// 那时「其它」那一档会把一个新形状悄悄藏起来。所以原样留着。
	m = validOutput(t)
	m.Finish = Finish{Reason: "insufficient_system_resource", Said: true}
	if err := m.Check(); err != nil {
		t.Errorf("一个我们从没见过的收尾原因该被原样收下：%v", err)
	}
	// 模型 id 同形：没报与报了个空串必须分得开。
	for _, said := range []bool{true, false} {
		m = validOutput(t)
		m.ModelSaid, m.Model = said, map[bool]string{true: "", false: "agnes-1"}[said]
		if err := m.Check(); err == nil {
			t.Errorf("模型 id 那一格自相矛盾（said=%v model=%q）竟被收了", m.ModelSaid, m.Model)
		}
	}
	m = validOutput(t)
	m.Model, m.ModelSaid = "", false
	if err := m.Check(); err != nil {
		t.Errorf("端点没自报模型 id 是合法形状，该收：%v", err)
	}
}

// ⑨ 原话指纹必须是完整的 64 位十六进制，留下的字节不许比原话还多。
func TestSaysFingerprintAndKeptBytes(t *testing.T) {
	m := validOutput(t)
	m.Says.SHA256 = sha("模型这一发的原话")[:16]
	if err := m.Check(); err == nil {
		t.Errorf("16 位指纹竟被收了 —— 收下每一发的原文指纹，正是「线路上没有第三方见证」那个缺口的前一半")
	} else if !strings.Contains(err.Error(), "64") {
		t.Errorf("那句话没说该是几位：%v", err)
	}
	// 与输入侧共用同一把尺：64 位但不是十六进制也要拒。
	// 这一格曾经只被长度判据守着，非法十六进制从旁边溜了过去。
	m = validOutput(t)
	m.Says.SHA256 = strings.Repeat("z", 64)
	if err := m.Check(); err == nil {
		t.Errorf("64 个 z 不是十六进制，竟被收了")
	} else if !strings.Contains(err.Error(), "十六进制") {
		t.Errorf("那句话没说真凶：%v", err)
	}
	m = validOutput(t)
	m.Says.Kept = m.Says.Bytes + 1
	if err := m.Check(); err == nil {
		t.Errorf("留下的比原话还多竟被收了 —— 裁不会把话裁长")
	}
	m = validOutput(t)
	m.Says.Kept = 400
	if err := m.Check(); err != nil {
		t.Errorf("裁到 400 字节该收：%v", err)
	}
	if !m.Says.Clipped() {
		t.Errorf("900 留 400 该算裁过")
	}
	full := validOutput(t)
	if full.Says.Clipped() {
		t.Errorf("900 留 900 不该算裁过")
	}
	// 模型一个字都没说（纯 function-calling 那一发）是合法形状，指纹照样要有。
	empty := validOutput(t)
	empty.Says = Says{SHA256: sha("")}
	empty.ToolCalls = 2
	if err := empty.Check(); err != nil {
		t.Errorf("原话 0 字节但有 2 个提议，是 function-calling 的正常形状：%v", err)
	}
	if empty.Says.Clipped() {
		t.Errorf("0 留 0 不该算裁过")
	}
}

// ⑩ 一个字都没回、也没有提议，却记成正常收尾 ⇒ 拒。
func TestEmptyReplyIsNotANormalFinish(t *testing.T) {
	m := validOutput(t)
	m.Says = Says{SHA256: sha("")}
	m.ToolCalls = 0
	if err := m.Check(); err == nil {
		t.Errorf("什么都没回却记成正常收尾，竟被收了")
	}
	// 这一发**收到过**响应（200，只是里头啥动作都没有），所以状态码是量到的 ——
	// 旧夹具在这里留空是当年那根冒充轴逼出来的形状，新规则下它自己就该被拒。
	m.Failure = Failure{Kind: FailMalformedAction, Detail: "提议没有名字",
		Status: MeasuredOf(200), RetryAfterMs: AbsentBecause("x")}
	if err := m.Check(); err != nil {
		t.Errorf("把它记成 malformed_action 就该收：%v", err)
	}
}

// ⑪ 「这一发里执行不了的提议」与「链上连歪几发」是两个量，少了后者，
// 「连歪三发就停」这条判定在元数据里就重建不出来，框架得自己维护计数器 ——
// 于是一个决定停不停的数有了第二个 owner。
func TestMalformedCountAndStreakAreTwoQuantities(t *testing.T) {
	m := validOutput(t)
	m.ToolCalls, m.Malformed = 1, 3
	if err := m.Check(); err == nil {
		t.Errorf("3 个执行不了、总数才 1，竟被收了")
	} else if !strings.Contains(err.Error(), "同一个集合") {
		t.Errorf("那句话没说清这是分母问题：%v", err)
	}
	m.ToolCalls, m.Malformed, m.MalformedStreak = 3, 1, 2
	if err := m.Check(); err != nil {
		t.Errorf("3 个提议里 1 个执行不了、链上连歪 2 发，该收：%v", err)
	}
	// 两个量必须对得上：这一发没歪，链上的连续计数就该归零。
	m = validOutput(t)
	m.MalformedStreak = 3
	if err := m.Check(); err == nil {
		t.Errorf("这一发没有执行不了的提议，链上却记着连歪 3 发，竟被收了")
	}
	m = validOutput(t)
	m.ToolCalls, m.Malformed, m.MalformedStreak = 2, 1, 0
	if err := m.Check(); err == nil {
		t.Errorf("这一发歪了、链上的连续计数却是 0，竟被收了")
	}
	m = validOutput(t)
	m.ToolCalls, m.Malformed = 2, -1
	if err := m.Check(); err == nil {
		t.Errorf("负的计数竟被收了")
	}
}

// ⑫ 管路失败那一发的收字节不许拿去做成本核算；但发出去过的钱不会因为失败而不见。
// 对应真实形状：有一发记了 82112 字节，而同一趟正常那一发只 1922，至今没查清是哪一个。
func TestCostCountableDistrustsFailedCalls(t *testing.T) {
	m := validOutput(t)
	if !m.CostCountable() {
		t.Errorf("正常收尾、字节也数到了，那一发该能进成本核算")
	}
	m.Failure = Failure{Kind: FailTransport, Detail: "读到一半连接断了", RetryAfterMs: AbsentBecause("x")}
	if m.CostCountable() {
		t.Errorf("管路失败那一发的收字节不该进成本核算 —— 那一支可能把心跳、没解析的、甚至我们自己多算的都算进去")
	}
	// 「发出去过的钱不会因为失败而不见」：那一发的过网线字节照记。
	if m.Sent() != SentSomething {
		t.Errorf("失败那一发仍然真发了 %d 字节，该报「发出去了」", m.WireBytesOut.N)
	}
}

// ⑬ 端点自报的模型 id 只能是自述：它进不了合计，也当不了事实。
func TestEndpointModelIsOnlyASelfReport(t *testing.T) {
	m := validOutput(t)
	cells := m.Cells()
	var found *Cell
	for i := range cells {
		if cells[i].Key == "endpoint_model" {
			found = &cells[i]
		}
	}
	if found == nil {
		t.Fatalf("endpoint_model 那一格不在键名表里 —— 它就是死格")
	}
	if found.Basis != BasisModelSays {
		t.Errorf("端点自报的模型 id 出处该是 model_says，得到 %s", found.Basis)
	}
	if found.Basis.Decidable() {
		t.Errorf("model_says 那一格不该能拿去做判定")
	}
	// 它的值是一个字符串，不是 Measure —— 所以它结构上就进不了任何合计。
	// 这一条比「另造一个 model_says 的 Measure 试试 Total 收不收」更贴：
	// 后者测的是 Total，测不到「这一格喂不进去」这件事本身。
	if _, isMeasure := found.Value.(Measure); isMeasure {
		t.Errorf("endpoint_model 那一格的值不该是 Measure（那它就能被加进合计）")
	}
	if s, isStr := found.Value.(string); !isStr || s != m.Model {
		t.Errorf("endpoint_model 那一格该原样装着端点自报的 %q，得到 %#v", m.Model, found.Value)
	}
	// 而 Total 那一道门也确实不收模型自述的数（两头都要有）。
	var tot Total
	if err := tot.Add(Measure{N: 1, Basis: BasisModelSays}); err == nil {
		t.Errorf("模型自述的数竟进了合计")
	}
}

// ⑭ 合计只合同量纲的两格。把毫秒、字节、token 加成一堆是这份 API 最容易犯的误用，
// 所以合计的入口是一个具名方法，不是一个「把所有格子都加起来」的通用函数。
func TestTokenTotalOnlySumsTokens(t *testing.T) {
	m := validOutput(t)
	tot, err := m.TokenTotal()
	if err != nil {
		t.Fatalf("两侧都是转述，该能合计：%v", err)
	}
	if want := m.UsageIn.N + m.UsageOut.N; tot.N() != want {
		t.Errorf("合计该是 %d，得到 %d", want, tot.N())
	}
	if tot.Counts() != 2 {
		t.Errorf("分母该是 2 笔，得到 %d", tot.Counts())
	}
	// 两笔都是对端转述的，所以这个合计不是一个「我们自己量出来的完整事实」。
	if tot.Complete() {
		t.Errorf("两笔都是转述，不该自称完整事实：%s", tot.String())
	}
	if !strings.Contains(tot.String(), "转述 2 笔") {
		t.Errorf("合计那句话没带分母：%q", tot.String())
	}
	// 一侧缺席：合计仍然算得出来，但必须说自己不完整。
	m = validOutput(t)
	m.UsageOut = AbsentBecause("对端没回 completion_tokens")
	tot, err = m.TokenTotal()
	if err != nil {
		t.Fatalf("一侧缺席不该让合计报错（它该如实报「不完整」）：%v", err)
	}
	if tot.Complete() || !strings.Contains(tot.String(), "缺席 1 笔") {
		t.Errorf("缺席那一笔没进分母：%q", tot.String())
	}
	// 一侧口径非法：合计必须报错，而不是把它当成 0 加进去。
	m = validOutput(t)
	m.UsageIn = MeasuredOf(1587)
	if _, err := m.TokenTotal(); err == nil {
		t.Errorf("一侧记成实测，合计竟没报错")
	}
}

// TestWireFormatSaidMatchesAndNeedsAResponse 判的是新那一格 `wire_format` 的两种说谎形状。
// 规矩五：这一支写完必须真的红过一次 —— 下面每一个 t.Errorf 都对应一种坏输入，
// 而 m.WireFormatSaid 那一条在旧代码里根本没有对应字段，等于新判据自己先被证伪过。
func TestWireFormatSaidMatchesAndNeedsAResponse(t *testing.T) {
	m := validOutput(t)
	m.WireFormat = "chat.completion"
	m.WireFormatSaid = true
	if err := m.Check(); err != nil {
		t.Errorf("正常一发带对端自报的格式，该被收：%v", err)
	}
	// 形状一：said 与值不自相矛盾的那两向。
	m.WireFormatSaid = false
	if err := m.Check(); err == nil {
		t.Errorf("报了格式却说「对端没报」，自相矛盾竟被收了")
	}
	m = validOutput(t)
	m.WireFormatSaid = true
	if err := m.Check(); err == nil {
		t.Errorf("说对端报了格式、值却是空串，竟被收了（没报与报了个空串是两件事）")
	}
	// 形状二：串是从哪来的 —— 管路没通那一发不可能转述到对端的格式声明。
	m = validOutput(t)
	m.Says = Says{SHA256: sha("")}
	m.WireFormat, m.WireFormatSaid = "chat.completion", true
	m.Failure = Failure{Kind: FailTransport, Detail: "连接被对端关掉",
		Status:       AbsentBecause("压根没收到响应"),
		RetryAfterMs: AbsentBecause("对端没机会说")}
	if err := m.Check(); err == nil {
		t.Errorf("transport 那一发连响应都没有，却转述出对端自报的格式，竟被收了")
	}
	// 反过来：收到过响应的那一类带格式，必须收 —— 这一条就是这一格存在的全部理由。
	m.Failure = Failure{Kind: FailProtocol, Detail: "body 解不出约定的形状",
		Status: MeasuredOf(200), RetryAfterMs: AbsentBecause("对端没给 Retry-After")}
	if err := m.Check(); err != nil {
		t.Errorf("protocol 那一发确实收到了响应（200），带对端的 object 该被收：%v", err)
	}
}
