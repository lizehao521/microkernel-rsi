package kernel

import (
	"fmt"
	"strings"
)

// FailKind 是「这一发为什么没走下去」。六种故障 + 一个「没故障」，后果各不相同，
// 不许合并成「失败」一档：合并之后「管路断了，同样的请求可以再发」与
// 「密钥错了，再发一百次也一样」在账上长得一模一样，于是重试被省掉、诊断被支错方向。
//
// 零值是 FailNone（没故障），这与本包 Basis 的零值纪律**刻意相反**：
// Basis 的零值是「忘了填」必须被拒，而这里「没填」与「没故障」在语义上确实是同一件事 ——
// 一发正常回来的响应不会去填故障类别。代价写在这里，不藏起来：
// 一个忘了设 Kind 的 Failure 会被读成「这一发正常」。兜住它的是 Check 里那条
// 「什么都没回却记成正常收尾」的判据，而不是零值本身。
type FailKind string

const (
	FailNone            FailKind = ""                 // 这一发正常回来了
	FailTransport       FailKind = "transport"        // 连不上/超时/断了
	FailConfig          FailKind = "config"           // 密钥错、模型名不存在、参数非法
	FailHTTP            FailKind = "http"             // 408/425/429/5xx
	FailProtocol        FailKind = "protocol"         // 响应读不出来/形状不对
	FailMalformedAction FailKind = "malformed_action" // 端点回得通，但那个动作执行不了
	FailBudget          FailKind = "budget"           // 预算到顶：一次都不该发
)

// failKinds 是「六种故障 + 一个没故障」那七个取值的唯一一份表。
// Known() 与报红时那句「不在几种取值里」都从它数出来 —— 数写死在文案里的话，
// 加一种故障就会让文案变成假话（这里曾经写着「六种」而表里有七个）。
var failKinds = []FailKind{
	FailNone, FailTransport, FailConfig, FailHTTP, FailProtocol, FailMalformedAction, FailBudget,
}

// RetrySame 只答「同样的请求原样再发一次有没有用」。
// 它与下面那个 Downgrade 是**两个问题**，所以住在两个地方：
// 共用一个字段的话，改一处故障分类会顺手关掉另一种恢复能力。
func (k FailKind) RetrySame() bool { return k == FailTransport || k == FailHTTP }

// MayCarryStatus 答的是**另一根轴**：这一类故障发生时，我们到底收到过对端响应没有。
// 收到过才有状态码可记；没收到（transport 管路没通、budget 是我们自己掐的）就没有 ——
// 硬记就成了把「没发生」写成「量到 0」，那一族是规矩一禁止的。
//
// 它必须与 RetrySame() 分开住：两轴只在 http 那一档恰好重合。
// 曾经用「是不是 http」冒充这根轴，代价实测过两次：一发真 401（config 有码）
// 与一发 200 坏 body（protocol 有码）都被那条按类别的互核挡在门外
// （报告/假key一发-401.md、报告/协议档一发-200坏body.md）。
func (k FailKind) MayCarryStatus() bool {
	switch k {
	case FailHTTP, FailConfig, FailProtocol, FailMalformedAction:
		return true
	default:
		return false
	}
}

func (k FailKind) Known() bool {
	for _, x := range failKinds {
		if x == k {
			return true
		}
	}
	return false
}

// Failure 是这一发没走下去时的全部说法。
type Failure struct {
	Kind   FailKind
	Detail string // 对端或管路给的说法。内核不脱敏也脱不了（擦码器在框架那一侧），
	//                    所以这一格由调用方负责先擦过 —— 这是一条**契约**，不是一条被核过的不变量。
	Status Measure // 对端回的 HTTP 状态码。没收到响应的那一类**必须是缺席**，
	//                    不是 0 —— 这一格曾经被写死成 measured，于是每一发没故障的往来
	//                    都在账上说「我们量到状态码是 0」（改前在册的账全是那样；
	//                    数以 tasks/audit/status_axis_dist.py 重数，读数在 报告/状态码轴-在册分布.txt）。
	RetryAfterMs Measure // 对端说要等多久；对端没说就是缺席，不是 0
	Downgrade    bool    // 第二个问题：换成非流式再发一次值不值得试
}

// Finish 是端点自己说的收尾原因。刻意**不**归一化成枚举：
// 归一化等于替端点说话，而换一家端点就会多出一个我们没见过的值，
// 那时「其它」那一档会把一个新形状悄悄藏起来。所以原样留着，
// 另外单独记「它到底说没说」—— 没说不等于说了个空字符串。
type Finish struct {
	Reason string
	Said   bool
}

// Says 是「模型说了多少话」。它与「通道上过了多少字节」是两码事：
// 后者含收尾帧、含心跳、含没被解析的部分。
type Says struct {
	Bytes  int64  // 原话多少字节
	Kept   int64  // 我们留下了多少字节（进历史的那一份）
	SHA256 string // 原话指纹。收下每一发的原文指纹，是「线路上没有第三方见证」那个缺口的前一半
}

func (s Says) Clipped() bool { return s.Kept < s.Bytes }

// SentState 是「这一发到底有没有花出去」的三态答案。
// 刻意不是一个 bool：裸 bool 会把「预算挡下，一个字节都没发」与
// 「管路在计数回来之前断了，我们不知道发了多少」压成同一个 false，
// 而前者是 0 花费、后者可能已经花了钱 —— 把后者读成前者，就是把一笔真开销记成了没花。
type SentState int

const (
	SentUnknown   SentState = iota // 没数到
	SentNothing                    // 数到了，是 0
	SentSomething                  // 数到了，非 0
)

func (s SentState) String() string {
	switch s {
	case SentNothing:
		return "没发出去（数到了，是 0 字节）"
	case SentSomething:
		return "发出去了"
	default:
		return "不知道发没发出去（没数到，不是 0）"
	}
}

// OutputMeta 是「一发响应回来之后，关于我们收到了什么」的全部格子。
type OutputMeta struct {
	Call      int
	Streaming bool
	Finish    Finish
	Model     string // 端点自报的模型 id。它只能是自述，永远不是事实
	ModelSaid bool   // 端点到底有没有自报 —— 与 Finish.Said 同形：没报不等于报了个空串
	// WireFormat 是**对端自己说它按什么格式回的**那串原字（响应体顶层 `object`，
	// 实测非流式 `chat.completion`、流式 `chat.completion.chunk`）。它和 Model 同族：
	// 只能转述，不能实测；也不能由我们填自己那套接线常量 —— 那等于转述对端没说过的话。
	// 这一格的来历是一发真 400：我们把模型上一句参数被截的话原样喂回去，
	// 对端回「Assistant tool call ***.arguments must be valid JSON」—— 格式是它规定的，
	// 而账上此前没有任何一格记着这一趟是按哪个格式谈的（报告/待定-两个状态与格式那一格.md）。
	WireFormat     string
	WireFormatSaid bool // 对端有没有自报这一项 —— 没报与报了个空串是两个事实
	Says           Says
	// 两个 token 数都只能是**转述**：分词器在对端，我们数不出来。
	// 把它们记成「实测」会让一张成本表看着像我们自己量的（runloop 今天就记成了实测）。
	UsageIn  Measure
	UsageOut Measure
	// WireBytesOut 是**真过网线的请求体字节**，是一个三态量而不是裸 int64。
	// 它刻意不叫 bytes_out：输入侧那个「装配后的内容字节」是另一个量，
	// 实测差过 6327 → 11570（JSON 框架与转义）。两个量共用一个键名，
	// 任何按键名扫账本的分析层都会把它们加成一堆。
	WireBytesOut Measure
	WireBytesIn  Measure // 从通道上数出来的字节
	// 下面三格只在流式那一发存在。非流式那一发它们是**缺席**，不是 0 ——
	// 一个恒为 0 的分帧数会被读成「一帧都没收到」，而那恰好是协议错的判据。
	Chunks      Measure
	FirstByteMs Measure
	MaxGapMs    Measure
	ToolCalls   int
	Malformed   int // 这一发的提议里执行不了的有几个（没名字、参数解不出来）
	// MalformedStreak 是**链上**连续歪了几发（含这一发），0 = 这一发没歪。
	// 它与 Malformed 是两个量：一个数这一发里的提议，一个数跨发的连续性。
	// 少了它，「连歪三发就停」这条判定在元数据里重建不出来，
	// 框架就得自己维护计数器 —— 于是一个决定停不停的数有了第二个 owner。
	MalformedStreak int
	Failure         Failure
}

// Sent 答「这一发到底有没有花出去」，三态。判据是过网线的字节，不是我们的意图。
func (m OutputMeta) Sent() SentState {
	switch {
	case m.WireBytesOut.Absent:
		return SentUnknown
	case m.WireBytesOut.N > 0:
		return SentSomething
	default:
		return SentNothing
	}
}

// CostCountable 答「这一发的收字节能不能进成本核算」。
// 管路失败那一发不能：那一支的计数可能把没解析的、心跳的、甚至我们自己多算的都算进去
// （runloop 有一发记了 82112 字节，而同一趟正常那一发只 1922，至今没查清是哪一个）。
// 没数到发没发出去的那一发同样不能：连分母都不知道，算出来的成本没有意义。
func (m OutputMeta) CostCountable() bool {
	return m.Failure.Kind == FailNone && m.Sent() == SentSomething
}

// usageOK 是「token 那一格允许什么口径」的**唯一一份**判据。
// Check 与 TokenTotal 都从这里读 —— 各写一份的话，一份改了另一份不会跟着红，
// 而合计正是要拿去做成本核算的那个出口。
func usageOK(field string, m Measure) error {
	if err := m.Check(field); err != nil {
		return err
	}
	// 白名单，不是黑名单。写成「不许实测」的话，估算与模型自述会从旁边溜过去，
	// 而字段注释正在说「只能是转述」。
	if !m.Absent && m.Basis != BasisRelayed {
		return fmt.Errorf("kernel: %s 那一格的出处是 %s —— 分词器在对端，我们只能转述（或如实记缺席）", field, m.Basis)
	}
	return nil
}

// TokenTotal 是这一发的入+出 token 合计，带分母。
// 它是 Total 的一个真读者 —— 一个没有读者的导出类型等于没有这个类型。
// 只合同量纲的两格：把毫秒、字节、token 加成一堆是这份 API 最容易犯的误用。
// 口径不合法时它必须报错，而不是把那一笔当成 0 加进去 —— 一个悄悄少了一笔的合计
// 比一个报错的合计贵得多。
func (m OutputMeta) TokenTotal() (Total, error) {
	var t Total
	if err := usageOK("入 token", m.UsageIn); err != nil {
		return t, err
	}
	if err := usageOK("出 token", m.UsageOut); err != nil {
		return t, err
	}
	if err := t.Add(m.UsageIn); err != nil {
		return t, fmt.Errorf("kernel: 入 token 进不了合计：%v", err)
	}
	if err := t.Add(m.UsageOut); err != nil {
		return t, fmt.Errorf("kernel: 出 token 进不了合计：%v", err)
	}
	return t, nil
}

func (m OutputMeta) Check() error {
	if m.Call < 1 {
		return fmt.Errorf("kernel: 第几发是 %d，发号从 1 起（0 会被读成「还没发」）", m.Call)
	}
	if !m.Failure.Kind.Known() {
		return fmt.Errorf("kernel: 故障类别是 %q，不在 %d 种取值里（%s）—— 一个新的形状正在被当成旧的读",
			string(m.Failure.Kind), len(failKinds), failKindNames())
	}
	if err := m.Failure.check(); err != nil {
		return err
	}
	// 「换成非流式再发一次」对一发本来就不是流式的响应无从谈起。
	// 这一条要跨两个字段才判得出来，所以住在 OutputMeta.Check 里，不住在 Failure.check 里。
	if m.Failure.Downgrade && !m.Streaming {
		return fmt.Errorf("kernel: 这一发本来就不是流式的，却建议「换成非流式再发一次」")
	}
	if m.Finish.Said != (m.Finish.Reason != "") {
		return fmt.Errorf("kernel: 收尾原因那一格自相矛盾（said=%v reason=%q）—— 端点没说与端点说了个空串是两件事",
			m.Finish.Said, m.Finish.Reason)
	}
	if m.ModelSaid != (m.Model != "") {
		return fmt.Errorf("kernel: 模型 id 那一格自相矛盾（said=%v model=%q）—— 端点没报与端点报了个空串是两件事",
			m.ModelSaid, m.Model)
	}
	if m.WireFormatSaid != (m.WireFormat != "") {
		return fmt.Errorf("kernel: 往来格式那一格自相矛盾（said=%v format=%q）—— 对端没报与报了个空串是两件事",
			m.WireFormatSaid, m.WireFormat)
	}
	// 自称「对端报了它按什么格式回」却又称压根没收到对端响应 —— 这两句不能同时成立。
	// 这一条借的是 ① 那张轴表，不是新加的一根轴：收到过响应才有格式可转述。
	if m.WireFormatSaid && !m.Failure.Kind.MayCarryStatus() && m.Failure.Kind != FailNone {
		return fmt.Errorf("kernel: 说对端自报了格式 %q，可 %s 这一类压根没收到对端响应 —— 那串字是从哪来的？",
			m.WireFormat, m.Failure.Kind)
	}
	if err := m.Says.check(); err != nil {
		return err
	}
	for _, u := range []struct {
		name string
		m    Measure
	}{{"入 token", m.UsageIn}, {"出 token", m.UsageOut}} {
		if err := usageOK(u.name, u.m); err != nil {
			return err
		}
	}
	if err := m.WireBytesIn.Check("通道上收到的字节"); err != nil {
		return err
	}
	// 收字节是我们在通道上数出来的，不是对端说的。记成转述就意味着
	// 「这个数得去问对端」，而我们本来就在数它。
	if m.WireBytesIn.Decidable() && m.WireBytesIn.Basis != BasisMeasured {
		return fmt.Errorf("kernel: 收字节那一格的出处是 %s —— 它是我们在通道上数的，只能记实测", m.WireBytesIn.Basis)
	}
	if err := m.WireBytesOut.Check("过网线的请求体字节"); err != nil {
		return err
	}
	if m.WireBytesOut.Decidable() && m.WireBytesOut.Basis != BasisMeasured {
		return fmt.Errorf("kernel: 过网线字节那一格的出处是 %s —— 它是我们编码出来再数的，只能记实测", m.WireBytesOut.Basis)
	}
	// 预算到顶的那一发一个字节都不该过网线。而「没数到」不许冒充「一个字节都没发」：
	// 前者是不知道，后者是 0 花费，两者的后果一个是接得回来、一个是钱已经花了。
	if m.Failure.Kind == FailBudget {
		if m.WireBytesOut.Absent {
			return fmt.Errorf("kernel: 报的是预算挡下，过网线字节却记成没数到 —— 预算闸在发之前就拦，那一格必然是数到的 0")
		}
		if m.WireBytesOut.N != 0 {
			return fmt.Errorf("kernel: 报的是预算挡下，却有 %d 字节过了网线", m.WireBytesOut.N)
		}
	}
	stream := []struct {
		name string
		m    Measure
	}{{"分帧数", m.Chunks}, {"首帧等待", m.FirstByteMs}, {"最大帧间等待", m.MaxGapMs}}
	for _, s := range stream {
		if err := s.m.Check(s.name); err != nil {
			return err
		}
		// 两头都要判。非流式写了 0 是把「不分帧」说成「一帧都没收到」；
		// 流式却报缺席，是把一个该量到的数量丢了。
		if m.Streaming && s.m.Absent {
			return fmt.Errorf("kernel: 这一发是流式的，%s 却报缺席 —— 流式就该量得到它", s.name)
		}
		if !m.Streaming && !s.m.Absent {
			return fmt.Errorf("kernel: 这一发不是流式的，%s 却报了 %d —— 非流式没有这一格，写数就是编数", s.name, s.m.N)
		}
	}
	if m.ToolCalls < 0 || m.Malformed < 0 || m.MalformedStreak < 0 {
		return fmt.Errorf("kernel: 工具提议 %d 个、执行不了的 %d 个、链上连歪 %d 发", m.ToolCalls, m.Malformed, m.MalformedStreak)
	}
	if m.Malformed > m.ToolCalls {
		return fmt.Errorf("kernel: 执行不了的提议 %d 个，比提议总数 %d 还多 —— 两个数不是从同一个集合数出来的", m.Malformed, m.ToolCalls)
	}
	// 这一发没歪，链上的连续计数就必须归零；这一发歪了，计数至少是 1。
	// 这条把「一发内的个数」与「跨发的连续性」两个量钉在一起，免得其中一个悄悄漂掉。
	if m.Malformed == 0 && m.MalformedStreak != 0 {
		return fmt.Errorf("kernel: 这一发没有执行不了的提议，链上却记着连歪 %d 发", m.MalformedStreak)
	}
	if m.Malformed > 0 && m.MalformedStreak < 1 {
		return fmt.Errorf("kernel: 这一发有 %d 个提议执行不了，链上的连续计数却是 %d", m.Malformed, m.MalformedStreak)
	}
	// 一个字都没回、也没有一个提议，却记成正常收尾 —— 这一格在真端点上出现过：
	// 对端回了「有 id、名字空、参数 2 字节」的提议，被当成协议错停了整趟。
	if m.Says.Bytes == 0 && m.ToolCalls == 0 && m.Failure.Kind == FailNone {
		return fmt.Errorf("kernel: 这一发什么都没回（原话 0 字节、0 个提议），却记成了正常收尾")
	}
	if _, err := m.TokenTotal(); err != nil {
		return err
	}
	return CheckCells("输出侧", m, m.Cells())
}

func (f Failure) check() error {
	if f.Kind == FailNone && (f.Detail != "" || f.Status.Decidable()) {
		return fmt.Errorf("kernel: 说是没故障，却带着说法 %q / 状态 %d", f.Detail, f.Status.N)
	}
	if f.Kind != FailNone && strings.TrimSpace(f.Detail) == "" {
		return fmt.Errorf("kernel: 报了故障 %s 却没说是什么 —— 这一格报红时得说得出下一个人该去哪儿查", f.Kind)
	}
	// 说法首尾不许带空白：它会被抄进日志、也会被人拿去逐字比对，
	// 多一个空格就是一次「明明一样却说对不上」。
	if strings.TrimSpace(f.Detail) != f.Detail {
		return fmt.Errorf("kernel: 故障说法首尾带空白（%q）—— 它会被抄进日志，也会被人拿去比对", f.Detail)
	}
	// 上限与"首尾空白"是同族判据：都只管形状，不管这句话是什么意思。
	// 故障原话是最容易被塞长的一句（对端的整段 HTML 报错、或一段指令形状的文字），
	// 而它带着"这是我们对端收到的说法"的外观被 read_back 交回给下一个 Agent。
	if err := checkFreeText("故障说法(Failure.Detail)", f.Detail); err != nil {
		return err
	}
	// 「收到过对端响应没有」与「是哪一类故障」是两根轴（见 MayCarryStatus 那段注释）：
	// 前者决定这一格能不能有状态码，后者决定原样重发有没有用。以前用 kind==http 一根轴冒充，
	// 于是真 401 与 200 坏 body 两发都装不进自己的状态码。
	if f.Kind.MayCarryStatus() && !f.Status.Decidable() {
		return fmt.Errorf("kernel: %s 这一类是按对端响应分的，却没有状态码 —— 读的人分不清它收到响应没有", f.Kind)
	}
	if !f.Kind.MayCarryStatus() && f.Status.Decidable() {
		return fmt.Errorf("kernel: %s 这一类压根没收到对端响应，却记着状态码 %d —— 那个数是从哪来的？",
			f.Kind, f.Status.N)
	}
	// 状态码得像个 HTTP 的：0 现在由缺席那一态表达（不再许写成量到的 0），越界的数一律拒。
	if f.Status.Decidable() && (f.Status.N < 100 || f.Status.N > 599) {
		return fmt.Errorf("kernel: 状态码 %d 不在 HTTP 的 100..599 里 —— 真量到的数不会长这样", f.Status.N)
	}
	if err := f.RetryAfterMs.Check("对端要我们等多久"); err != nil {
		return err
	}
	if f.RetryAfterMs.Decidable() && f.RetryAfterMs.N < 0 {
		return fmt.Errorf("kernel: 对端说要等 %d 毫秒", f.RetryAfterMs.N)
	}
	// 没故障却记着「等 N 毫秒再发」：这两个说法打架，读的人不知道该不该重试。
	if f.Kind == FailNone && f.RetryAfterMs.Decidable() {
		return fmt.Errorf("kernel: 说是没故障，却记着对端要我们等 %d 毫秒", f.RetryAfterMs.N)
	}
	// 原样重发不会变好的类别，却记着「等一会儿再发」—— 那是把两种恢复方式混成了一种。
	if f.Kind != FailNone && f.RetryAfterMs.Decidable() && !f.Kind.RetrySame() {
		return fmt.Errorf("kernel: %s 这一类原样重发不会变好，却记着「等 %d 毫秒再发」", f.Kind, f.RetryAfterMs.N)
	}
	// 「换成非流式再发」这一条建议，对一发本来就不是流式的响应无从谈起。
	if f.Downgrade && f.Kind == FailNone {
		return fmt.Errorf("kernel: 说是没故障，却又建议换个请求重发")
	}
	return nil
}
func (s Says) check() error {
	if s.Bytes < 0 || s.Kept < 0 {
		return fmt.Errorf("kernel: 模型原话 %d 字节、留下 %d 字节", s.Bytes, s.Kept)
	}
	if s.Kept > s.Bytes {
		return fmt.Errorf("kernel: 留下了 %d 字节，而原话只有 %d 字节 —— 裁不会把话裁长", s.Kept, s.Bytes)
	}
	return checkFingerprint("原话指纹", s.SHA256)
}

func failKindNames() string {
	out := make([]string, 0, len(failKinds))
	for _, k := range failKinds {
		if k == FailNone {
			out = append(out, "（空）=没故障")
			continue
		}
		out = append(out, string(k))
	}
	return strings.Join(out, " ")
}
