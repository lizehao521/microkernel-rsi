// Package kernel 只做一件别家没有的事：让每一条在代理之间流动的说法都带着"谁算的"，
// 并且**缺这一格就进不来**。调度、渠道、MCP 都不在这里——那些已经有商品化实现。
//
// 四条不变量（每条都配了一正一反两条测试）：
//  1. Basis 的零值是 BasisUnset ⇒ "忘了填"不可能伪装成"实测"（今天真出现过的形状：
//     一行没有依据的计数被下游代理当证据写进结论）。
//  2. 账本是哈希链 ⇒ 改任意一格，重算必须发现；而链只能证明"写下来的没被动过"，
//     证明不了"该写的都写了"，所以缺尾必须单独报，不许读成自洽。
//  3. 一次写入要么成功要么被拒，两条都有账（拒收不留半条）；计数必须按结论分桶，
//     **不许有一格在两种结论下都 +1**（那正是我今天埋的那根假计数器）。
//  4. 模型输入/输出元数据的**每一格**都要真进这条链，缺席记成 absent 而不是 0，
//     且一份自相矛盾的元数据是整发拒收 + 拒收有数（见 record.go）。
package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Basis 是"这一格的话是谁算出来的"。零值是 Unset，不是任何一种合法来源：
// 这样漏填会在 Append 处被拒，而不是悄悄落到某个看起来正经的类别里。
type Basis int

const (
	BasisUnset     Basis = iota // 没填 —— 内核拒收，不存在"默认按实测算"
	BasisMeasured               // 运行时/内核自己量的（字节数、退出码、行号、覆盖区间）
	BasisRelayed                // 转述别人算的（判分器、子代理、外部工具）：可追到 source，但不是本内核算的
	BasisEstimated              // 估算：必须带"这是估算，不是测量"，不许参与判定
	BasisModelSays              // 模型自述（它说它做了什么、它说多少条）：永远是线索，不是事实
	// BasisAbsent 是"这一格在这一发结构上不存在"（非流式那一发没有首帧等待），
	// 或"对端给了数、被判成不可信、没收"。它**不是** BasisUnset：
	// Unset 是忘了填、要拒收；Absent 是一个如实的结论、要收下并且带上理由。
	// 少了这一档，缺席就只能落成 0，而 0 会被下一个人读成"量到了，是零"。
	BasisAbsent
)

func (b Basis) String() string {
	switch b {
	case BasisMeasured:
		return "measured"
	case BasisRelayed:
		return "relayed"
	case BasisEstimated:
		return "estimated"
	case BasisModelSays:
		return "model_says"
	case BasisAbsent:
		return "absent"
	default:
		return "unset"
	}
}

// Decidable 答「带着这个出处的数能不能拿去做判定」。
// 估算没有校准过、模型自述是说话人给自己打分、缺席压根没有数、没填的不是数 —— 四种都不能。
// 这一条住在 Basis 上而不是 Measure 上：键名表里每一格带的是 Basis，
// 而 Measure.Decidable 委派给它，于是「能不能判定」这件事只有一个 owner。
func (b Basis) Decidable() bool { return b == BasisMeasured || b == BasisRelayed }

// ErrNoBasis 是这一包最硬的错：宁可整条拒绝，也不给一个"大概齐"的默认。
var ErrNoBasis = errors.New("kernel: 这一格没带 basis（谁算的），拒收")

// MaxFreeText 是一格**自由文本**允许的长度（按字符数，不按字节）。
//
// 为什么这条判据归内核、且只归形状不归语义：这些句子不会安安静静待在账里 ——
// 它们被 `read_back` 原样交回给下一个读账的人（常常就是另一个 Agent），也被抄进日志。
// 10-18 实测：请求体有 4 MiB 上限，但**单格没有上限**，一发里灌 4MB 指令形状的文本照样落账，
// 落账之后它和"没折叠没裁尾"那种正常句子长得一模一样，还盖着 `measured`。
// 上限管不了"说什么"（内核从不判语义），只把"一格能吞掉多少下游上下文"这件事钉死。
// 要写长文，放账外，指一个出处进来 —— 那是 `Source` 那一格的活。
const MaxFreeText = 2000

// checkFreeText 是上面那条的唯一实现处（一个口径只许一个 owner，别在三格各写一遍数字）。
func checkFreeText(what, s string) error {
	if n := utf8.RuneCountInString(s); n > MaxFreeText {
		return fmt.Errorf("kernel: %s 有 %d 字符，超过单格上限 %d —— 这一格会被原样抄给下一个读账的人，"+
			"长度本身就是一条注入面；长文放账外，指个出处过来", what, n, MaxFreeText)
	}
	return nil
}

// Entry 是一条记录。PayloadHash 由内核自己算，不接受调用方递进来的指纹 ——
// 否则"我演过/我量过"又可以由说话的人自己填。
type Entry struct {
	Seq     int       `json:"seq"`
	Session string    `json:"session"`
	Actor   string    `json:"actor"`
	Step    int       `json:"step,omitempty"`
	Kind    string    `json:"kind"`
	Claim   string    `json:"claim,omitempty"`
	Basis   Basis     `json:"basis"`
	Source  string    `json:"source,omitempty"`
	At      time.Time `json:"ts"`
	// Payload 是原值，`json:"-"` 让它**不随 Entry 一起序列化** ——
	// 落盘时该带什么由框架决定（它可能要擦凭据、可能要换个封装）。
	// 代价要写在这里，别让它变成一句没人知道的承诺：框架若原样把 Entry 写成 JSONL，
	// 指纹的原像就丢了，第三方拿得到 PayloadH 却重算不出它 ——
	// 那时「别人能按同规则重算」只剩链上那几格，不含 payload。
	Payload  []byte `json:"-"`
	PayloadH string `json:"payload_sha256"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`

	// payloadAbsent 不是「这一格没有内容」，而是「读的人手里没有这一格的原像」。
	// 它只有 ReadJournal 会置真（那一行没有 payload_b64 时），所以它不可由
	// 读文件的人伪造：JSON 里没有一个字段能把它设成 true。
	// 它也不进 seal 的输入 —— seal 盖的是那 11 格，多带一个不参与哈希的布尔才不会
	// 改动链上任何一个字节。
	//
	// 为什么要单独存这一位：把没有原像的条目直接拿 sum(nil) 去比 PayloadH，
	// 会把一条完好无损的账念成「内容被动过」。缺席与篡改长成同一句话，
	// 是规矩一那一族最贵的实例，只是这次换到了读盘这一路上。
	payloadAbsent bool
}

// Record 是给调用方填的形状：没有 Basis 字段可省 ——它是必填的，且零值无效。
type Record struct {
	Session string
	Actor   string
	Step    int
	Kind    string
	Claim   string
	Basis   Basis
	Source  string
	Payload []byte
	At      time.Time
}

// Gate 是「闸门拦了几次」那一栏的六个数。**单位不同，永不相加**
// （加起来就是「一个名字住六个量」，#42/#44/#46 三次踩的都是这个）：
//
//	Rejected          内核拒的落账   单位＝一发（整发拒收，链上一条不写）—— 内核自己数（Append/RecordCall）
//	PrecheckRejected  内核拒的预检   单位＝一发 —— 判据是内核的，但内核不参与预检，由外壳转报（relayed）
//	PrecheckDoor      门口挡的预检   单位＝一次预检请求 —— 外壳申报
//	DoorRefused       门口挡的落账   单位＝一次写请求 —— 外壳申报
//	BadRequests       协议层判无效    单位＝一条请求 —— 外壳申报
//	TransportRefused  传输层挡的      单位＝一条请求 —— 外壳申报
//
// **再加一格 `State`：这六个数的口径**（Live / Partial；Unknown 不落盘 —— 落盘了就等于有这一行）。
// 它必须和六个数一起被盖进封条。两路互审（10-18 深夜）各独立撞到同一处：少了这一位，
// 一本「只覆盖这一程」的下界账导出再读回就被洗成「这一程完整可数」，
// 一次往返就把这次改动存在的理由自己抹掉 —— 而那正是本包第一不变量的形状。
//
// 后四笔住在壳上（它们是 atomic，壳在并发路上数），内核只是**原样带着它们落盘**，
// 并在每一栏都说清是谁数的 ——「谁拒的」一合就答不出，而那是这个项目最不肯让的一件事。
type Gate struct {
	Rejected         int       `json:"rejected"`
	PrecheckRejected int       `json:"precheck_rejected"`
	PrecheckDoor     int       `json:"precheck_door"`
	DoorRefused      int       `json:"door_refused"`
	BadRequests      int       `json:"bad_requests"`
	TransportRefused int       `json:"transport_refused"`
	State            GateState `json:"state"`
}

// GateState 回答「这几笔闸门计数覆盖到哪一段」——三态，因为两态会说谎：
//
//	GateUnknown 手里根本没有这一栏（老账本、别人手写的文件）—— 读成 0 就是本包第一天就反对的形状
//	GateLive    这一进程从空账开始一路数到现在，完整
//	GatePartial 从一份**没有计数栏**的账本接着记：本程这几笔是真的，载入前那一段无从知道
//
// 为什么值得为它开一个枚举而不是一个 bool：Partial 与 Unknown 的**能说什么**不同。
// Unknown 只能说"不可知"；Partial 能说"这一程拦了 3 发，之前不可知"——
// 后者是可核的下界，压成"不可知"就白数了，说成"一共拦了 3 次"就少算了。
type GateState int

const (
	GateUnknown GateState = iota // 零值是"不可知"，不是"量到的 0"（与 Basis 同一条规矩）
	GateLive
	GatePartial
)

func (s GateState) String() string {
	switch s {
	case GateLive:
		return "这一程完整可数"
	case GatePartial:
		return "只覆盖这一程（载入前那份文件里没有计数栏）"
	default:
		return "不可知（手里这份字节里没有计数那一栏）"
	}
}

// AnyLive 答「这六笔里有没有一笔数到过东西」——只答有没有，不答一共有几笔。
// 为什么由内核给这个方法而不是让调用方把六笔相加：一旦印出那个和，它就成了
// 「一个名字住六个量」（本包第一规矩，#42/#44/#46 三次踩的都是它）。
func (g Gate) AnyLive() bool {
	return g.Rejected != 0 || g.PrecheckRejected != 0 || g.PrecheckDoor != 0 ||
		g.DoorRefused != 0 || g.BadRequests != 0 || g.TransportRefused != 0
}

// Ledger 是内存里的追加链；落盘交给上层（它只管字节，不认识磁盘）。
type Ledger struct {
	docs []Entry
	// 被拒的尝试也要有数：一个只记成功、不记被拒的账，会把"没写进去"读成"没发生"。
	countRejected int
	// 预检（kernel.check_call）那一路被内核判据拒掉的整发数。
	// 它刻意不与 countRejected 合成一个数：一个是"想写进链、被拒"，一个是"问过能不能写、
	// 被拒、什么也没打算写"。合成一个数就答不出「拦下来之后他到底有没有试着落账」，
	// 而外部调用方量的正是这个：预检拒收在账面上完全隐形（10-18 两个互不相知的调用方各撞一次）。
	countPrecheck int
	gateState     GateState
}

func NewLedger() *Ledger { return &Ledger{gateState: GateLive} }

// checkRecord 是「一条记录能不能进账」的**唯一一份**判据。
// Append 与 plan 都从它读 —— 各写一份的话两份会慢慢漂开，而漂开的后果是具体的：
// plan 放过、Append 拒掉的记录会让 commit 在链上留下半发，
// 那正是「先全部翻译、再统一落账」这个两阶段设计要消灭的形状。
func checkRecord(r Record) error {
	if r.Basis == BasisUnset {
		return ErrNoBasis
	}
	// 转述与估算都必须能追到出处：一个要说"谁的输出"，一个要能被认成不是测量。
	if needsSource(r.Basis) && strings.TrimSpace(r.Source) == "" {
		return fmt.Errorf("kernel: basis=%s 必须带 source（转述谁 / 估的什么），拒收", r.Basis)
	}
	if err := checkFreeText("出处(Source)", r.Source); err != nil {
		return err
	}
	if err := checkFreeText("这一格的人话(Claim)", r.Claim); err != nil {
		return err
	}
	// 缺席是一个如实的结论，但它必须带着理由：一个说不出为什么的缺席，
	// 与"忘了填"在账上就分不开了 —— 而这一档存在的**全部理由**就是把它俩分开。
	if r.Basis == BasisAbsent && strings.TrimSpace(r.Claim) == "" {
		return fmt.Errorf("kernel: basis=absent 必须带理由（为什么这一发没有这一格），拒收")
	}
	if r.Actor == "" || r.Kind == "" {
		return fmt.Errorf("kernel: actor 与 kind 不能空（这条 %s/%s 将来无从归属）", r.Actor, r.Kind)
	}
	// 注意这里**不**要求 Claim 非空（缺席那一档除外）：账本这一层从最初就允许
	// 一条记录只带 Payload 不带人话（Claim 的 JSON tag 是 omitempty）。
	// 「每格都得有一句人话」这条纪律住在元数据那一侧的 claimOf 里，
	// 它保证经 RecordInput/RecordOutput/RecordCall 落账的格子 Claim 永不为空。
	// 边界要写清：直接调 Append 的调用方绕过 claimOf，那条纪律就不替他站岗。
	return nil
}

// Append 校验后追加。任何一次尝试（成功或被拒）都会进 Count() 的账 ——
// 被拒也必须看得见，否则"没写进去"会被后面读成"没发生"。
func (l *Ledger) Append(r Record) (Entry, error) {
	if err := checkRecord(r); err != nil {
		l.noteRejected()
		return Entry{}, err
	}
	e := Entry{
		Seq: len(l.docs) + 1, Session: r.Session, Actor: r.Actor, Step: r.Step, Kind: r.Kind,
		Claim: r.Claim, Basis: r.Basis, Source: r.Source, At: r.At, Payload: append([]byte(nil), r.Payload...),
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.PayloadH = sum(e.Payload)
	if n := len(l.docs); n > 0 {
		e.PrevHash = l.docs[n-1].Hash
	}
	e.Hash = e.seal()
	l.docs = append(l.docs, e)
	return e, nil
}

func (e Entry) seal() string {
	return sum([]byte(strings.Join([]string{
		strconv.Itoa(e.Seq), e.Session, e.Actor, strconv.Itoa(e.Step), e.Kind, e.Claim,
		e.Basis.String(), e.Source, e.At.UTC().Format(time.RFC3339Nano), e.PayloadH, e.PrevHash,
	}, "\n")))
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Docs 返回只读视图（复制，避免调用方拿到切片后原地改格 —— 那正是链要防的事）。
func (l *Ledger) Docs() []Entry {
	out := make([]Entry, len(l.docs))
	copy(out, l.docs)
	return out
}

// Verify 重算整条链。判据本身住在 CheckChain（journal.go）—— 这一层只是它的一个调用方，
// 于是「内存里的账本」与「从盘上读回来的账本」共用同一套规则，不会漂成两份（规矩三）。
//
// 它只回答"写下来的没被动过、且没被剪掉可见的一段"：
// 首条 PrevHash 必须为空、序号必须连续、每条指纹必须能由自身重算出来。
//
// 两种失败必须说两句话：链真被改过（红），与手里没有原像、无从重算（核不了）。
// 把后者念成前者，下一个读报告会去查一个不存在的篡改者。
func (l *Ledger) Verify() error {
	rep := CheckChain(l.docs)
	if rep.Mismatch != "" {
		return fmt.Errorf("kernel: %s", rep.Mismatch)
	}
	if len(rep.NoPreimage) > 0 {
		return fmt.Errorf("kernel: 链上 %d 条没有原像（第 %s）—— 这「不是」篡改，是手里的字节不足以重算那些条的指纹；"+
			"字段级自洽已核过，但「第三方能重算原值」那一半核不了，不许读成自洽",
			len(rep.NoPreimage), seqList(rep.NoPreimage))
	}
	return nil
}

// Tally 按 basis 分桶计数。这里刻意**不**共用一个"跑过了"的计数器：
// 今天那行 `ran += 1` 在通过和失败两条分支上都加，于是它不是判据。
type Tally struct {
	ByBasis map[string]int
	Total   int
}

func (l *Ledger) Tally() Tally {
	t := Tally{ByBasis: map[string]int{}, Total: len(l.docs)}
	for _, e := range l.docs {
		t.ByBasis[e.Basis.String()]++
	}
	return t
}

// noteRejected 是「内核拒了一次落账」这**一个**动作的唯一入口：先升计数状态，再 +1。
// 九处拒收都走它 —— 自己写 `countRejected++` 的地方一多，"哪些拒收进了数"就变成
// 九个各说各话的现场（这一族的教训在 #42/#44/#46 上各演过一次）。
func (l *Ledger) noteRejected() {
	l.touchGate()
	l.countRejected++
}

// Rejected 是被拒的写入次数（不含链上的记录）。
func (l *Ledger) Rejected() int { return l.countRejected }

// PrecheckRejected 是预检（不落账的那一路）被内核判据拒掉的整发数。
func (l *Ledger) PrecheckRejected() int { return l.countPrecheck }

// GateStatus 答「这几笔计数覆盖到哪一段」。读的人必须先听这一句再听数：
// Unknown 时的 0 是"没那一栏"，Partial 时的 0 是"这一程没拦过、之前无从知道"。
func (l *Ledger) GateStatus() GateState { return l.gateState }

// touchGate 记一笔之前调用：一本从"没有计数栏"的账本接过来的 Ledger，
// 一旦自己数到第一笔，就从 Unknown 升成 Partial —— 那仍是**下界**，不是全量，
// 所以渲染时要把 GateStatus 一起说出去，不许只报数。
func (l *Ledger) touchGate() {
	if l.gateState == GateUnknown {
		l.gateState = GatePartial
	}
}

// NotePrecheckRejected 由外壳在"预检交回内核判据、判据说不自洽"那一刻调一次。
// 内核自己不数这一笔：预检不写链、不产生 Entry，判据虽然是内核的，事件却是外壳看见的，
// 所以这一栏落盘时与外壳那四笔同一待遇 —— 谁数的就说是谁数的。
// 通过预检**不许**调它（反臂：那会把"检查过 10 次"念成"拒了 10 次"）。
func (l *Ledger) NotePrecheckRejected() int {
	l.touchGate()
	l.countPrecheck++
	return l.countPrecheck
}

// Gate 给出内核这一侧的三格现值（两笔数 + 口径）；外壳那四笔由壳在编码时补进来（见 MarshalJournalWithGate）。
func (l *Ledger) Gate() Gate {
	return Gate{Rejected: l.countRejected, PrecheckRejected: l.countPrecheck, State: l.gateState}
}

func (l *Ledger) String() string {
	t := l.Tally()
	keys := make([]string, 0, len(t.ByBasis))
	for k := range t.ByBasis {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.Itoa(t.ByBasis[k]))
	}
	if t.Total == 0 {
		// 「账 0 条（）」这种空括号是给不出来的：它长得像一张填好了的分桶表，
		// 而实际上一格都没记过。10-15 是 `mcphttp_up.sh status` 那一趟第一次把它读出来的。
		return "账 0 条（一格都没记过 —— 这不是各档都是 0，是还没有账）"
	}
	return fmt.Sprintf("账 %d 条（%s）", t.Total, strings.Join(parts, " "))
}
