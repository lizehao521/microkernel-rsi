package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// 事件层是宿主侧的东西：内核 v1 已冻结（48 键 / 49 格），这里一格都不新增，
// 只是把循环「已经落进账的那些事实」按发按序另写一份，给界面与断线续传当底。
// 形状照 研究-协同工作-1022.md §5.1。

// 五档出处的名字与内核那本账一致。写不进这五个就算不合格 ——
// 宁可当场拒，也不要落一条「看起来像 measured」的假凭据。
var evidenceTiers = map[string]bool{
	"measured": true, "estimated": true, "model_says": true, "relayed": true, "absent": true,
}

// 状态是「这一条在讲什么阶段」，必须是个可分档的 token；
// 结束原因的整句话走 Stop 字段，不许塞进 state（下游就没法按 state 归并了）。
var eventStates = map[string]bool{
	"running": true, "ok": true, "refused": true, "failed": true, "settled": true, "interrupted": true,
}

type EventStep struct {
	No   *int   `json:"no,omitempty"`
	Name string `json:"name,omitempty"`
	Tool string `json:"tool,omitempty"`
}

// Progress 的 Total 用指针：算不出来就是「没这个数」，写 0 会被读成「总共 0 步」，
// 那是这本账最恨的那一族。Limit 另放：额度是额度、工作量是工作量，
// 混在一个字段里会把「跑完」印成「跑了一半」。
type Progress struct {
	Done  int  `json:"done"`
	Total *int `json:"total,omitempty"`
	Limit *int `json:"limit,omitempty"`
}

type Event struct {
	V         int        `json:"v"`
	Session   string     `json:"session"`
	Run       string     `json:"run"` // 第几趟：号是**趟内**的轴，跨趟追加的日志要靠它分开
	Turn      int        `json:"turn"`
	Seq       int        `json:"seq"`
	TS        string     `json:"ts"`
	Kind      string     `json:"kind"`
	Step      *EventStep `json:"step,omitempty"`
	State     string     `json:"state"`
	Evidence  string     `json:"evidence"`
	Why       string     `json:"why,omitempty"`
	Stop      string     `json:"stop,omitempty"`     // 六态的 **token**（script/turns/policy/driver/capped/abort）
	StopWhy   string     `json:"stop_why,omitempty"` // 那一态的整句话 —— 别拿散文让下游做中文字符串匹配
	Progress  *Progress  `json:"progress,omitempty"`
	Accept    *Accept    `json:"accept,omitempty"` // 只有 turn_accepted 带
	Settle    *Settle    `json:"settle,omitempty"` // 只有结算那一条带
	ElapsedMS int64      `json:"elapsed_ms"`

	// enteredAt 是「请求入口那一刻」，由造回执的那里定死。它非导出 ⇒ encoding/json 天然跳过，
	// 不用加 tag（M2 路 B 的 P2-5：注释原来写着 json:"-"，行为对、说明错）。
	// Emit 拿它算 ts 与 ack_ms —— 等 emit 时才现取，ack_ms 恒为 0，针1 就是装饰。
	enteredAt time.Time

	// idleOnly 也非导出：它是「这条是看门狗补的位」这个来历，不是要落盘的内容。
	// 带上它，Emit 才能在同一条锁里判「这一发是不是已经结过账」——
	// 结算之后落进来的补位条会让自印的 counts 比账上少一条，那是判据抓不住的假账。
	idleOnly bool
}

// Accept 是回执的载荷：收到的是哪一发、打算走几步、上限多少、从哪儿能把它停下、
// 断了能不能续。少一样，界面就只能猜 —— 而「猜出来的可续」正是这份研究要治的那一族。
type Accept struct {
	// Planned 说实话是「还剩几发（含本发）」，不是「还剩几步」—— 契约 §5.2.1 那个「计划」字面
	// 被 M2 路 A 点出名实不符（turn1..6 报 6..1，而每发实际 3–4 步）。字段名跟着事实改，
	// 别的驱动不知道就 nil（绝不写 0）。
	Planned   *int   `json:"planned_shots"`
	Limit     *int   `json:"turn_limit"`
	StopVia   string `json:"stop_via"`
	Resumable *bool  `json:"resumable"` // 指针且不带 omitempty：旧记录读回 nil = 没量过，不许塌成 false
	AckMS     *int64 `json:"ack_ms"`    // 距入口多久才回执 —— 针1 数的就是这个
}

// 针1 的口径：回执距请求入口超过这个数就红。
const ackLimitMS = 100

// Settle 是结算的载荷（契约 §5.2.4「停=结算」要的那三样）：
// 停在哪一相、已经发生了什么、没发生什么。
// 「没发生的」必须逐条点名并写明**根本没执行** —— 把没跑的步含糊说成「跑了没结果」，
// 就是 pgagent 测试 11 抓的那一族假象。
type Settle struct {
	Phase      string         `json:"phase"`                   // 停下来时正在做哪一相
	Reached    []string       `json:"reached"`                 // 这一发真走过的相
	NotReached []string       `json:"not_reached"`             // 根本没执行的相
	Counts     map[string]int `json:"counts"`                  // 本发各 state 各几条（成功/被拒/失败/没量到）
	Kind       string         `json:"kind"`                    // 造成停的那一类（interrupted / settled）
	Why        string         `json:"why"`                     // 结算自己的理由（absent 那一族的原因走各条事件的 why）
	DoneBefore int            `json:"done_before_shots"`       // 打断前已经落账的发数 —— 计数要活过打断
	KPI        int            `json:"ledger_driven_decisions"` // 账驱动的步数：打断路径上不许被清零
}

// inPhaseTable 是相位名的唯一门禁：相位表只有一处（turnPhases），判据与产物都从它读。
// 早先这个判断只活在测试里（isPhase），改一相名字时 Go 全包绿、只有尺子在别处崩 —— 两份表就是两张皮。
func inPhaseTable(p string) bool {
	for _, x := range turnPhases {
		if x == p {
			return true
		}
	}
	return false
}

// 一发今天的相位表。**只列今天真有事件对的相**：
// read_back / decide 还没有专属事件（属 u.hook-coverage），这里不假装能判它们走没走到 ——
// 相位表与事件种类必须同源，否则补步针数的是空气。
var turnPhases = []string{"accept", "work", "check_call", "record_call"}
var phaseKind = map[string]string{
	"accept":      "turn_accepted",
	"work":        "shot_started",
	"check_call":  "precheck",
	"record_call": "recorded",
}

// stopTokens 是六态的 token 词表（与 exitOf 一一对应，一格不新增）。
var stopTokens = map[string]bool{
	"script": true, "turns": true, "policy": true, "driver": true, "capped": true, "abort": true,
}

// ValidateSettlement 是针4（结算）+ 针5（补步）的可达形态，判读回来的整本日志：
//
//	每一发要么有且只有一条结算，要么这条发没开始过（不能半结算）；
//	reached 与 not_reached 不相交、并集正好是相位表（少一相就是漏判）；
//	not_reached 里的相，它对应的事件 kind **一条都不许出现在这一发里**（补步针）；
//	counts 的加总必须等于这一发除结算外的事件条数（自印与账对得上，三态不塌）；
//	stop 必须是六 token 之一，整句话走 stop_why。
func ValidateSettlement(evs []Event) error {
	perTurn := map[string][]Event{}
	prevDone := map[string]int{}
	var turns []string
	for _, e := range evs {
		if e.Turn == 0 {
			continue
		}
		key := blockOf(e)
		if _, has := perTurn[key]; !has {
			turns = append(turns, key)
		}
		perTurn[key] = append(perTurn[key], e)
	}
	for _, e := range evs {
		if e.Kind == "run_settled" {
			if !stopTokens[e.Stop] || e.StopWhy == "" {
				return fmt.Errorf("run 级结算的 stop=%q / stop_why=%q 不合格 —— token 与整句话分两格放", e.Stop, e.StopWhy)
			}
		}
	}
	// 每趟都得有 run_settled：**开了账**（有 run_started）却没收口，这本账不能当「跑完的账」读（M5 P1-3）。
	// 只认 run_started 起的那几趟：拿残缺的手工件去判「缺收口」是判一个不存在的形状。
	startedRun := map[string]bool{}
	seenRun := map[string]bool{}
	for _, e := range evs {
		switch e.Kind {
		case "run_started":
			startedRun[e.Session+"|"+e.Run] = true
		case "run_settled":
			seenRun[e.Session+"|"+e.Run] = true
		}
	}
	for _, e := range evs {
		if e.Turn == 0 || e.Kind == "run_settled" {
			continue
		}
		k := e.Session + "|" + e.Run
		if startedRun[k] && !seenRun[k] {
			return fmt.Errorf("趟 %s|%s 开了账（有 run_started）却没有 run_settled —— 收口那一步没走完（或被半路杀掉）", e.Session, e.Run)
		}
	}
	for _, key := range turns {
		group := perTurn[key]
		t := group[0].Turn
		var settles []Event
		for _, e := range group {
			if e.Kind == "turn_settled" || e.Kind == "turn_interrupted" {
				settles = append(settles, e)
			}
		}
		if len(settles) == 0 {
			// 这一发有事件却一条结算都没有 —— 函数头一直承诺「不能半结算」，
			// 代码原来是 continue（M2 路 A 的 P1-③，复算：这一形当时判不出）。
			return fmt.Errorf("第 %d 发有 %d 条事件却没有结算 —— 半结算：循环进去了没收口", t, len(group))
		}
		if len(settles) > 1 {
			return fmt.Errorf("第 %d 发有 %d 条结算 —— 只能结一次，多条就是两套收口代码", t, len(settles))
		}
		s := settles[0]
		if s.Settle == nil {
			return fmt.Errorf("第 %d 发有结算却没带载荷 —— 三样一样都没交", t)
		}
		switch s.Settle.Kind {
		case "completed", "refused", "failed", "interrupted":
		default:
			return fmt.Errorf("第 %d 发结算的 kind=%q 不在四类里 —— 结束类别不许散写（state 有白名单，这里同理）", t, s.Settle.Kind)
		}
		if (s.Kind == "turn_interrupted") != (s.Settle.Kind == "interrupted") {
			return fmt.Errorf("第 %d 发 kind=%s 与事件名 %s 对不上 —— 打断与正常收口不许混着标", t, s.Settle.Kind, s.Kind)
		}
		if s.Settle.KPI < 0 {
			return fmt.Errorf("第 %d 发结算的 KPI=%d 是负数", t, s.Settle.KPI)
		}
		if !inPhaseTable(s.Settle.Phase) {
			return fmt.Errorf("第 %d 发结算的 phase=%q 不在相位表里 —— 相位名不许散写（表只有 turnPhases 一处）", t, s.Settle.Phase)
		}
		if s.Settle.Why == "" {
			return fmt.Errorf("第 %d 发结算说了 kind=%s 却没带 why —— 三样里「停在哪儿」不算交清", t, s.Settle.Kind)
		}
		if s.Settle.DoneBefore < 0 || s.Settle.DoneBefore > t {
			return fmt.Errorf("第 %d 发结算说停下时已完成 %d 发 —— 不可能大于发号本身", t, s.Settle.DoneBefore)
		}
		runKey := group[0].Session + "|" + group[0].Run
		if prev, has := prevDone[runKey]; has && s.Settle.DoneBefore < prev {
			return fmt.Errorf("同一趟里第 %d 发结算说已完成 %d 发，前一发说的是 %d 发 —— 计数不许倒退", t, s.Settle.DoneBefore, prev)
		}
		prevDone[runKey] = s.Settle.DoneBefore
		seen := map[string]bool{}
		for _, p := range s.Settle.Reached {
			if seen[p] {
				return fmt.Errorf("第 %d 发 reached 里 %q 重复", t, p)
			}
			seen[p] = true
		}
		sum := map[string]bool{}
		for _, p := range append(append([]string{}, s.Settle.Reached...), s.Settle.NotReached...) {
			if sum[p] {
				return fmt.Errorf("第 %d 发 %q 既算走到又算没走到 —— 两格打架", t, p)
			}
			sum[p] = true
		}
		// 相位不只数条数，还要按位置对：reached 必须是相位表的前缀、not_reached 是剩下的那段、
		// phase 必须等于前缀最后一项。只数条数的话，「accept 没走但 record_call 走了」这种内容错配全绿（M5 P1-2）。
		if err := checkPhaseShape(t, s.Settle); err != nil {
			return err
		}
		if len(s.Settle.Reached)+len(s.Settle.NotReached) != len(turnPhases) {
			return fmt.Errorf("第 %d 发的相位条数 %d，与相位表 %d 不符（少一相就是漏一相的判据）",
				t, len(s.Settle.Reached)+len(s.Settle.NotReached), len(turnPhases))
		}
		// 补步针：说「没走到」的相，它的事件一条都不许在这一发里出现
		for _, p := range s.Settle.NotReached {
			k, has := phaseKind[p]
			if !has {
				return fmt.Errorf("第 %d 发 not_reached 里的 %q 不在相位表里 —— 相名散写，判据就没法核对它", t, p)
			}
			for _, e := range group {
				if e.Kind == k {
					return fmt.Errorf("第 %d 发结算说 %q 没执行，流里却来了 %s —— 没执行的步被写成了走过的步", t, p, k)
				}
			}
		}
		// 反过来也要对：说「走到了」的相，事件必须真在（这是 C2 那族坏法的牙）
		for _, p := range s.Settle.Reached {
			k, has := phaseKind[p]
			if !has {
				return fmt.Errorf("第 %d 发 reached 里的 %q 不在相位表里 —— 补一个表外的相就当走过了", t, p)
			}
			found := false
			for _, e := range group {
				if e.Kind == k {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("第 %d 发结算说走过 %q，可这一发没有一条 %s 事件 —— 补出来的步", t, p, k)
			}
		}
		want := map[string]int{}
		for _, e := range group {
			if settleKinds[e.Kind] {
				continue
			}
			want[e.State]++
		}
		total, n := 0, 0
		for _, v := range s.Settle.Counts {
			total += v
		}
		for _, v := range want {
			n += v
		}
		if total != n {
			return fmt.Errorf("第 %d 发结算自印 counts 加总 %d，本发实际 %d 条 —— 两个数不许并存", t, total, n)
		}
		// 加总相等还不够：键名散写（running 写成 Succes）时两个数照样对得上。
		// 只走并集一次：两段互为掩护的写法测不出「哪一段在管」，一段就够，摘掉必须红。
		names := map[string]bool{}
		for st := range want {
			names[st] = true
		}
		for st := range s.Settle.Counts {
			names[st] = true
		}
		for st := range names {
			if s.Settle.Counts[st] != want[st] {
				return fmt.Errorf("第 %d 发结算 counts[%s]=%d，账上这一发实际 %d 条 —— 本发各 state 必须逐名对上（散写、少算、多算都在这一条里）",
					t, st, s.Settle.Counts[st], want[st])
			}
		}
		for _, e := range group {
			if e.Evidence == "absent" && e.Why == "" {
				return fmt.Errorf("第 %d 发有 absent 事件没带 why：%+v", t, e)
			}
		}
	}
	return nil
}

// reCanResume 判「这句话在声称能续」。它必须放过否定式：
// 「断开即取消，不可续」里含「可续」三字，按裸子串判就是把诚实的话当谎抓。
var reCanResume = regexp.MustCompile(`(^|[^不])可续`)

// settleKinds 是「终止类」事件：它们不进本发的 state 统计 ——
// 否则结算那条把自己也算成「发生过的一步」，counts 就与账多出一格。
var settleKinds = map[string]bool{"turn_settled": true, "turn_interrupted": true, "run_settled": true}

type eventBus struct {
	mu       sync.Mutex // seq 与 written 都是「数出来的」，第二个生产者一进来，没锁就是竞态
	path     string
	f        *os.File
	seq      map[int]int
	states   map[string]map[string]int // 每一「发身份块」的 state 计数：counts 由这里现数，不靠人手打
	t0       time.Time
	written  int
	closed   bool
	run      string // 第几趟的号：pid + 起表时刻，够分开「同一对路径上的两趟」
	degraded string // 为什么没落成文件；横幅要说，不许静默降级

	last      time.Time         // 上一条**真落盘**的时刻；看门狗量的是它，不是逻辑进度
	kindLast  map[string]string // 每个发身份块最后落的那条 kind：补位要说「停在哪一相」
	quiet     map[string]bool   // 结算抄过 counts 之后，这一发不再收补位条（否则 counts 与账差一条）
	idleFails []string          // 补位落不出去的原文，由循环在收口时印；不印就是静默吞
}

// blockOf 是一条「发」在日志里的完整身份：session + run + turn。
// 只用 session+turn 不够 —— 发号是趟内的轴，同一对路径连跑三趟就会撞成三条回执
// （M2 路 B 的 P0-1，我自己复现过：三趟同 session，(session,turn) 上仍是 3 条）。
func blockOf(e Event) string {
	return e.Session + "" + e.Run + "" + strconv.Itoa(e.Turn)
}

// newEventBus：path 为空就不开文件，只在内存里数序（用于「关掉事件层」那一趟与单元测试）。
func newEventBus(path string) *eventBus {
	b := &eventBus{path: path, seq: map[int]int{}, states: map[string]map[string]int{}, t0: time.Now(),
		kindLast: map[string]string{}, quiet: map[string]bool{},
		run: fmt.Sprintf("r%d-%d", os.Getpid(), time.Now().UnixNano())}
	if path == "" {
		return b
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.degraded = "建不了落点目录：" + err.Error()
		b.path = ""
		return b
	}
	// O_APPEND 而不是 O_TRUNC：这本日志是 E4「Last-Event-ID 重放」要读的底，
	// 开文件就把上一趟抹了，等于把别人要续的那个游标源删掉。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		b.degraded = "开不了日志文件：" + err.Error()
		b.path = ""
		return b
	}
	b.f = f
	// 同一本日志可能留着上一趟：append-only 就不许让 seq 重新从 1 开始，
	// 否则 (session,turn,seq) 这个三元组不再唯一，续传会指向两条。
	prev, err := ReadEvents(path)
	if err != nil {
		b.degraded = "读不回既有事件日志，序号没法接续，这一趟不落盘：" + err.Error()
		_ = b.f.Close()
		b.f, b.path = nil, ""
		return b
	}
	for _, e := range prev {
		if e.Seq > b.seq[e.Turn] {
			b.seq[e.Turn] = e.Seq
		}
		// state 统计也按既有那本接着数，但**按发身份块**接：结算的 counts 判的是
		// 「这一发（这条 session+这一趟+这个发号）里数出来的那些条」。
		// 只按 turn 接就会把上一趟同发号的那几条算进来 —— 那是把两趟的账并成一条。
		if !settleKinds[e.Kind] {
			if b.states[blockOf(e)] == nil {
				b.states[blockOf(e)] = map[string]int{}
			}
			b.states[blockOf(e)][e.State]++
		}
	}
	return b
}

func (b *eventBus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.f != nil {
		_ = b.f.Close()
		b.f = nil
	}
	b.closed = true
}

// stats 是给横幅用的那一读：落了哪、落了几条、为什么没落成。
// 三个都要，少一个就把「量不到」写成「没有」。
func (b *eventBus) stats() (path string, written int, degraded string) {
	if b == nil {
		return "", 0, "事件层没开（--events=false）"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.path, b.written, b.degraded
}

// runID 是这一趟的号：同一对路径上追加过好几趟，投影页要数的是**这一趟**。
// 不隔这一层，上一趟的发数会混进这一页的「共几发」。
func (b *eventBus) runID() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.run
}

// canResume 当场回答「断线之后接得上吗」，并给出理由。
//
// 三态在这里不许塌：nil 才算没量过，而这一格是可量的 —— 落点在不在、这本账降没降级，
// 总线上当场就知道。所以要么 true 要么 false，理由跟着走。
// true 的门槛与投送层一致：sseHandler 只读那个文件，文件读不回来就一切免谈。
func (b *eventBus) canResume() (bool, string) {
	if b == nil {
		return false, "断开不能续：事件层没开（--events=false），一条都没落"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.degraded != "" {
		return false, "断开不能续：事件层已降级（" + b.degraded + "）"
	}
	if b.path == "" {
		return false, "断开不能续：没有落点，这一趟只在内存数序"
	}
	f, err := os.Open(b.path)
	if err != nil {
		return false, "断开不能续：落点读不回来（" + err.Error() + "）"
	}
	_ = f.Close()
	return true, "断开可续：游标就是日志里的身份四元组，投送层按日志行序取游标之后的尾部"
}

// seqCanFollow 是序号判据本体：同一发内必须严格 +1。
// 它现在只被 ValidateLog（读回来的整本日志）与臂调用 —— 早先在 Emit 里也调一次，
// 但那里的 next 恒等于 prev+1，是不可达的死牙，删了。
func seqCanFollow(prev, next int) error {
	switch {
	case next == prev:
		return fmt.Errorf("序号重复：%d 又来一次", next)
	case next < prev:
		return fmt.Errorf("序号回退：%d 之后来了 %d", prev, next)
	}
	return nil
}

// ValidateAccepts 用契约的 100ms 判（生产与闸口用这一条）。
func ValidateAccepts(evs []Event) error { return ValidateAcceptsWithin(evs, ackLimitMS) }

// ValidateAcceptsWithin 同一套判据，但把「回执延迟上限」交给调用方：
// 满载 `-shuffle=on` 下实测出现过 483ms 的合法跑法（Sync 与调度被别的测试压着），
// 绝对阈值这时候量的是机器不是口径。所以：顺序/载荷/续传三件事永远判，
// 只有 ack 那一格按传入的限度判 —— 真要量 0.1s 就传契约值 100（`-check-log` 与延迟那两支都传 100）。
//
// 判读回来的这本日志：每一发的第一条必须是 turn_accepted；ack_ms 给得出来且不超传入的上限；
// 停止入口说得出；resumable 不许缺（缺=没量过，另记一笔，不塌成 false）；写成 false 时要有为什么；
// 且这一发的任何一条都不许出现「可续」这种没兑现的声称。
func ValidateAcceptsWithin(evs []Event, limitMS int64) error {
	order := map[string][]Event{}
	var turns []string
	for _, e := range evs {
		if e.Turn == 0 {
			continue // run 级事件不属于任何一发
		}
		key := blockOf(e)
		if _, has := order[key]; !has {
			turns = append(turns, key)
		}
		order[key] = append(order[key], e)
	}
	if len(turns) == 0 {
		return fmt.Errorf("这本日志一条发级事件都没有 —— 回执针没东西可判")
	}
	for _, key := range turns {
		group := order[key]
		t := group[0].Turn
		if first := group[0]; first.Kind != "turn_accepted" {
			return fmt.Errorf("第 %d 发第一条是 %q，不是 turn_accepted —— 「先回执再干活」落点错了", t, first.Kind)
		}
		var acc *Accept
		claims := []string{}
		for _, e := range group {
			if e.Accept != nil {
				if acc != nil {
					return fmt.Errorf("第 %d 发有两条回执 —— 受理只能有一次", t)
				}
				acc = e.Accept
			}
			for _, s := range []string{e.Why, e.Stop, nameOf(e)} {
				if reCanResume.MatchString(s) {
					claims = append(claims, s)
				}
			}
		}
		if acc == nil {
			return fmt.Errorf("第 %d 发的 turn_accepted 没带载荷", t)
		}
		switch {
		case acc.AckMS == nil:
			return fmt.Errorf("第 %d 发的回执没有 ack_ms —— 针1 无从判断", t)
		case *acc.AckMS > limitMS:
			return fmt.Errorf("第 %d 发的回执距入口 %dms，超上限 %dms", t, *acc.AckMS, limitMS)
		case acc.StopVia == "":
			return fmt.Errorf("第 %d 发的回执没说停止入口在哪", t)
		case acc.Resumable == nil:
			return fmt.Errorf("第 %d 发的 resumable 是 nil —— 能不能续要当场给个话，不许留成没量过", t)
		}
		if acc.Resumable != nil && !*acc.Resumable {
			if group[0].Why == "" {
				return fmt.Errorf("第 %d 发写了不可续却没给为什么", t)
			}
			if len(claims) > 0 {
				return fmt.Errorf("第 %d 发写着 resumable=false，流里却出现「可续」字样：%q —— 界面据此画可续就是谎", t, claims[0])
			}
		}
	}
	// 「发级不许挂 step、run 级不许挂发号」只判一处：ValidateTurnShape。
	// 原来这里还有一份重复判据，而且一条测试没走过 —— 看着两道闸、实际一道（M2 路 B 的 P1-4）。
	if err := ValidateTurnShape(evs); err != nil {
		return err
	}
	return nil
}

func nameOf(e Event) string {
	if e.Step == nil {
		return ""
	}
	return e.Step.Name + " " + e.Step.Tool
}

// ValidateTurnShape 单独管「回执不许挂 step、run 级事件不许挂发号」这一族轴问题。
// 为什么拆出来：混在序号判据里，红了不知道是哪条在响。
func ValidateTurnShape(evs []Event) error {
	for _, e := range evs {
		switch {
		case e.Turn == 0 && e.Step != nil:
			return fmt.Errorf("run 级事件 %q 带了 step —— 两个轴混在一格", e.Kind)
		case e.Kind == "turn_accepted" && e.Step != nil:
			return fmt.Errorf("turn_accepted 带了 step —— 受理不是某一步")
		}
	}
	return nil
}

// ValidateLog 是序号针的**可达**形态：判「读回来的这本日志同一发内严格递增」。
// 判据只有从公开路径走得到，红才是真红。
func ValidateLog(evs []Event) error {
	last := map[int]int{}
	for i, e := range evs {
		if err := seqCanFollow(last[e.Turn], e.Seq); err != nil {
			return fmt.Errorf("第 %d 条不合格：%v", i+1, err)
		}
		// 写侧 validate() 那三查搬到读回路径上来（M5 扫端的 P1-5）：只判序号的话，
		// 一本被手改过的日志里 state=banana、出处散写、absent 没理由都能四支全绿。
		// 「回执必须带入口时刻」那一半不复制过来 —— enteredAt 是非导出字段，读回来永远是零值，
		// 拿读不到的东西判就是假牙。
		if e.Kind == "" {
			return fmt.Errorf("第 %d 条没有 kind —— 下游没法分档", i+1)
		}
		if !evidenceTiers[e.Evidence] {
			return fmt.Errorf("第 %d 条出处档 %q 不在五档里 —— 落盘之后被改写，或写侧门禁被绕过", i+1, e.Evidence)
		}
		if !eventStates[e.State] {
			return fmt.Errorf("第 %d 条状态 %q 不在白名单里 —— 同上", i+1, e.State)
		}
		if e.Evidence == "absent" && e.Why == "" {
			return fmt.Errorf("第 %d 条写着 absent 却没带为什么量不到 —— 三态在盘上塌成了一态", i+1)
		}
		last[e.Turn] = e.Seq
	}
	return nil
}

// validate 不合格就返回错误且**不落盘**：absent 没理由、档名不认识、kind 或 state 乱写，都属这一族。
func (e *Event) validate() error {
	switch {
	case e.Kind == "":
		return fmt.Errorf("事件没有 kind")
	case !evidenceTiers[e.Evidence]:
		return fmt.Errorf("出处档 %q 不在五档里", e.Evidence)
	case !eventStates[e.State]:
		return fmt.Errorf("状态 %q 不在白名单里 —— 五档有门禁而状态没有，是不对称的", e.State)
	case e.Evidence == "absent" && e.Why == "":
		return fmt.Errorf("absent 事件没带 why —— 量不到要写为什么量不到，不许静默")
	case e.Accept != nil && e.enteredAt.IsZero():
		// 回执没带入口时刻就没法算 ack_ms；放过去等于让针1 恒过。
		return fmt.Errorf("turn_accepted 没带请求入口时刻 —— ack_ms 会变成 0，那是自证不是度量")
	}
	return nil
}

// Emit 落一条事件。返回的 error 只可能来自这条事件自己不合格或写盘失败；
// 调用方（宿主循环）拿到错误要喊出来，不许吞 —— 吞掉就是「事件层坏了但没人知道」。
func (b *eventBus) Emit(e Event) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("事件层已关闭，这条不落盘（kind=%s）", e.Kind)
	}
	e.V = 1
	if e.Run == "" {
		e.Run = b.run // 趟身份由总线盖，不靠调用方记得填
	}
	if e.idleOnly && b.quiet[blockOf(e)] {
		// 看门狗补的位，遇到「这一发已经结过账」就不落：结算自印的 counts 是抄当时的账，
		// 之后多进来一条 running，counts 与账就差一条 —— 那种账判据只能事后喊「两个数不许并存」，
		// 与其留一本对不上的账，不如这条不写（超时的解释已经尽到，见 idleView 的静默时长）。
		return nil
	}
	// 时刻优先用「请求入口那一刻」（回执的意义就是那一刻到的多早）；没有入口的其他事件用现在。
	at := time.Now()
	if !e.enteredAt.IsZero() {
		at = e.enteredAt
	}
	e.TS = at.Format(time.RFC3339Nano)
	e.ElapsedMS = time.Since(b.t0).Milliseconds()
	e.Seq = b.seq[e.Turn] + 1
	ne := e
	if ne.Accept != nil {
		// 深拷一份再填：`ne := e` 只拷了指针，直接写就等于往调用方那份 Accept 上写 ——
		// 同一个对象复用两次，第二次的 ack_ms 会照抄第一次（M2 路 B 的 P1-1，已复现）。
		// 而且**无条件覆盖**：调用方递进来的 ack_ms 一律不算数，否则写侧一行赋值就能抹平针1（P1-2）。
		na := *ne.Accept
		ne.Accept = &na
		ms := time.Since(ne.enteredAt).Milliseconds()
		ne.Accept.AckMS = &ms
	}
	if err := ne.validate(); err != nil {
		return err
	}
	line, err := json.Marshal(ne)
	if err != nil {
		return err
	}
	if b.f != nil {
		// 先落盘（含 Sync）再算落了：写失败或 sync 失败都不推进计数与序号，
		// 否则会留下一本「自印条数比文件里多」的假账。
		if _, err := b.f.Write(append(line, '\n')); err != nil {
			return err
		}
		if err := b.f.Sync(); err != nil {
			return err
		}
	}
	b.seq[e.Turn] = e.Seq
	b.written++
	// 落了盘才算「有人说过话」：上面任何一步 return err 都到不了这里，
	// 所以看门狗量的静默是「真的没写进去东西」，不是「写了但没人看见」。
	b.last = time.Now()
	b.kindLast[blockOf(e)] = e.Kind
	if !settleKinds[e.Kind] {
		if b.states[blockOf(e)] == nil {
			b.states[blockOf(e)] = map[string]int{}
		}
		b.states[blockOf(e)][e.State]++
	}
	return nil
}

// countsFor 把这一发已经落出去的 state 分布抄一份出来给结算用。
// 关键在「抄」：counts 由这本账自己数出来，不是人手打的 —— 打的那份会与账不一致。
//
// 键是**发身份块**（session+run+turn），不是 turn：日志跨趟接续后，同一个 session 的两趟
// 各有一个 turn=1，只按 turn 数会把上一趟的那几条算进这一发（M2 路 A 的 P0：第二趟起 counts 整族翻倍）。
func (b *eventBus) countsFor(session string, turn int) map[string]int {
	if b == nil {
		return map[string]int{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// 抄 counts 的同一把锁里把这一发标成「别再补位」：标晚了（比如等结算行落盘才标），
	// 看门狗就有一条挤在 counts 与结算行中间，那两个数从此不一样。
	b.quiet[blockOf(Event{Session: session, Run: b.run, Turn: turn})] = true
	out := map[string]int{}
	for k, v := range b.states[blockOf(Event{Session: session, Run: b.run, Turn: turn})] {
		out[k] = v
	}
	return out
}

// idleView 是给看门狗的一次锁内快照：静默多久、这一发最后落的一条是什么、已经落了几条。
// 分三次取会拿到「前一句与后一句不同源」的现场（中间正好插进别的事件），那是这本账最恨的事。
// seen=false 表示这条账一条还没落过 —— 那是「没量过」，不是「静默 0 秒」，看门狗据此不抢跑。
func (b *eventBus) idleView(session string, turn int) (idle time.Duration, lastKind string, landed int, seen bool) {
	if b == nil {
		return 0, "", 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	blk := blockOf(Event{Session: session, Run: b.run, Turn: turn}) // 键格式只由 blockOf 一处定，手拼会拼出另一把键
	for _, n := range b.states[blk] {
		landed += n
	}
	if b.last.IsZero() || landed == 0 {
		// 整本账没开，或这一发一条没落：那是「没量过」，不是「静默 0 秒」。
		// 看门狗据此不抢跑，判据据此不拿读不到的东西判。
		return 0, "", 0, false
	}
	return time.Since(b.last), b.kindLast[blk], landed, true
}

// noteIdleFail 把补位失败攒在总线里（这把锁本来就在），由循环收口时取走印出来。
func (b *eventBus) noteIdleFail(s string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.idleFails = append(b.idleFails, s)
}

// takeIdleFails 取走并清空：同一句失败印两遍会把收口那一段读成噪音。
func (b *eventBus) takeIdleFails() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.idleFails
	b.idleFails = nil
	return out
}

// ReadEvents 把落出去的那份读回来逐条解析 —— 自证用：只数文件字节数不算数，
// 读不回同样条数与同样顺序，就是写了一堆看不见的字。
func ReadEvents(path string) ([]Event, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := []Event{}
	for _, ln := range splitLines(string(b)) {
		var e Event
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			return out, fmt.Errorf("第 %d 行解析不了：%v", len(out)+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// emit 是 run 上的薄壳：事件层关掉（bus 为 nil）时整条路径就是零成本。
// 拒收要喊，但喊去 stderr —— stdout 那份正文是能被逐字比对的运行账，
// 诊断写进去等于事件层一坏就把上一条针也一起弄红（分不清是谁在响）。
func (r *run) emit(e Event) {
	if r.events == nil {
		return
	}
	e.Session = r.session
	if err := r.events.Emit(e); err != nil {
		r.errf("    事件层拒了（第 %d 发，kind=%s）：%v —— 这条没落盘", r.turnNo(), e.Kind, err)
	}
}

func intp(v int) *int { return &v }

func (r *run) stepEv(kind, state, evidence, why, name string, no int) Event {
	return Event{
		Kind: kind, State: state, Evidence: evidence, Why: why, Turn: r.turnNo(),
		Step: &EventStep{No: intp(no), Name: name},
	}
}

// acceptedEv 造这一发的回执。**入口时刻在这里就定死**，不等 emit 时现取 ——
// 现取的话 ack_ms 恒为 0，针1 就成了挂在没人走的枝上的装饰。
//
// resumable 由 eventBus.canResume() 当场算：E4 落了投送层，这一格从「恒 false 的约定」
// 变成「看得见的性质」。落点在不在、这本账降没降级，决定 true 还是 false，理由同格带出。
// 诚实地说不可续，比假装能续更协同（研究件 §5.2.4 / 引用 P25-P26）。
func (r *run) acceptedEv() Event {
	// E4 之后「能不能续」是可测的：日志落在一处读得回来的文件上、且这本账没降级，
	// 投送层才接得住 Last-Event-ID。内存模式/降级接不住，就老实写 false 并说为什么。
	resumable, why := r.events.canResume()
	return Event{
		Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: r.turnNo(),
		Why: why,
		// 上限只放一处（Accept.Limit）——同一件事在两格里各写一份，改一份就分叉。
		// 上限与停止入口只放一处（Accept.Limit / Accept.StopVia）。
		// E3 之后进程内也有停止入口（run.Stop），所以这里两条都列出来 —— 只写 signal 就是把能停的那一半藏了。
		Accept:    &Accept{Planned: r.plannedLeft(), Limit: intp(r.maxTurns), StopVia: "signal:SIGINT|in-proc:run.Stop(why)", Resumable: &resumable},
		enteredAt: r.ackAnchor(),
	}
}

// plannedLeft：只有剧本驱动知道还剩几发；其他驱动给 nil —— 不知道就写 nil，绝不写 0。
func (r *run) plannedLeft() *int {
	if s, ok := r.drv.(*scripted); ok {
		return intp(s.Left())
	}
	return nil
}

// ValidatePhaseTables 是四张相位表之间的整表自洽判据（M5 工具债 #113-3 落点）。
// 为什么要有它：turnPhases / phaseKind / stopTokens / settleKinds 原来互不连线 ——
// 上一轮实测：改一相名 ⇒ Go 全包绿，只有 Python 冒烟以 KeyError 崩在 rc=1，
// 而 rc=1 根本不在六态退出码契约里。判据必须活在产码里，红才走得到闸口。
// 反臂与钉住在 events_phase_source_test.go：改名 / 加相 / 删项三种各一根，必须红。
//
// 三条连线：
//  1. phaseKind 的键集合必须**恰等于** turnPhases 的元素集合 —— 改一相名、加一相、删一项都红；
//     turnPhases 里出现重名也红（重名会让 reached/not_reached 的条数与表悄悄脱钩）。
//  2. 每个相位对应的 kind 不得属于 settleKinds —— 结算类事件若兼作某相的「走到了」凭证，
//     Emit/newEventBus/ValidateSettlement 里三处 settleKinds 扣减就会把补步针咬到自己。
//  3. stopTokens 必须与六个 stopReason 常量的 Token() 产出**双向恰等**：
//     表里每个 token 都要真被产出过（改名/散写那一族），产出的每个 token 也必须在表里
//     （否则 run_settled 那一条会在 ValidateSettlement 里被自家词表拒掉）。
//
// 三态不塌的交代：Go 反射拿不到「某类型的全部常量」，产出侧只能逐名枚举六个常量 ——
// 这一行枚举就是**显式耦合点**：新增第七态而这里不登记，判据直接红，逼人来登记，不是漏网。
// 而「Token() 的每个调用站点都覆盖了哪六态」是静态文本属性，运行时判不了 ——
// 记 NOT_MEASURABLE：站点读码可核（loop.go 的 settle 处与 events_test.go 各一处），
// 本判据判的是值集合自洽，不冒充站点枚举，也不折算成 0。
func ValidatePhaseTables() error {
	inList := map[string]bool{}
	for _, p := range turnPhases {
		if inList[p] {
			return fmt.Errorf("turnPhases 里 %q 出现两次 —— 相位表自己先打架，条数判据(%d 相)就成了空气", p, len(turnPhases))
		}
		inList[p] = true
		if _, has := phaseKind[p]; !has {
			return fmt.Errorf("相位 %q 在 turnPhases 里、phaseKind 却没有它 —— reached/not_reached 对这一相只能猜", p)
		}
	}
	for p := range phaseKind {
		if !inList[p] {
			return fmt.Errorf("phaseKind 里有 %q、turnPhases 却没有 —— 「两套口径并存」就是这么开始的", p)
		}
	}
	if len(phaseKind) != len(turnPhases) {
		return fmt.Errorf("phaseKind 键 %d 个、turnPhases 相 %d 个 —— 键集合必须恰等于相位表，不许多不许少", len(phaseKind), len(turnPhases))
	}
	for p, k := range phaseKind {
		if k == "" {
			return fmt.Errorf("相位 %q 的 kind 是空串 —— 空串配不出任何事件，补步针数的是空气", p)
		}
		if settleKinds[k] {
			return fmt.Errorf("相位 %q 的 kind=%q 落在 settleKinds 里 —— 结算类事件不许兼作相位凭证，counts 扣减会咬到自己", p, k)
		}
	}
	produced := map[string]bool{}
	for _, s := range []stopReason{stopScript, stopTurns, stopPolicy, stopDriver, stopCapped, stopAbort} {
		tk := s.Token()
		if tk == "" {
			return fmt.Errorf("stopReason %q 的 Token() 产出空串 —— 六态之外不许有无名 token 混进这套表", string(s))
		}
		if produced[tk] {
			return fmt.Errorf("两个 stopReason 都产出 token %q —— 下游按 token 分支会把两态并成一条", tk)
		}
		produced[tk] = true
	}
	for tk := range stopTokens {
		if !produced[tk] {
			return fmt.Errorf("stopTokens 里有 %q，但没有任何 stopReason.Token() 产出它 —— 表与产出脱钩（改名/散写那一族）", tk)
		}
	}
	for tk := range produced {
		if !stopTokens[tk] {
			return fmt.Errorf("stopReason 产出了 token %q，stopTokens 里却没有 —— run 级结算会把真 token 判红", tk)
		}
	}
	return nil
}

// checkPhaseShape 判一发的相位形状：位置一致，且「停下来那一相」就是走得最远的那一相。
// 空 reached 是合法的（还没走到任何有事件对的相就被挡），那时只要求 phase 在表里（由上游那一条判）。
func checkPhaseShape(t int, st *Settle) error {
	k := len(st.Reached)
	for i, p := range st.Reached {
		if i >= len(turnPhases) || turnPhases[i] != p {
			return fmt.Errorf("第 %d 发的 reached=%v 不是相位表 %v 的前缀 —— 走过哪几相要按位置说，不是凑条数", t, st.Reached, turnPhases)
		}
	}
	for i, p := range st.NotReached {
		want := k + i
		if want >= len(turnPhases) || turnPhases[want] != p {
			return fmt.Errorf("第 %d 发的 not_reached=%v 不是 reached 之后的那一段 —— 两格拼起来必须正好是整张表", t, st.NotReached)
		}
	}
	if k > 0 && st.Phase != st.Reached[k-1] {
		return fmt.Errorf("第 %d 发走到 %v，停下那一相却写成 %q（应为 %q）—— 界面按这一格画「现在在做什么」，谎报就画错",
			t, st.Reached, st.Phase, st.Reached[k-1])
	}
	return nil
}
