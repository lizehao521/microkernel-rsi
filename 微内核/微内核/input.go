package kernel

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// PartKind 是「段的类型」。取值照 runloop internal/ctxbuf 那一份枚举，一个不多一个不少：
// 多出来的会是一个谁也填不进去的死格（那个仓库的注释里就留着一个 head-kept，代码从不产出），
// 少一个会让某一类段无处归类、退化成自由文本，于是「哪一类被挤了」这个问题答不出来。
type PartKind string

const (
	KindSystem   PartKind = "system"            // 协议说明：装不下就整发不发，不许裁一半
	KindSkill    PartKind = "skill"             // 说明书
	KindMemory   PartKind = "memory"            // 注入的记忆：被挤了要报「没进去」，不许说「我们替你折了」
	KindHistory  PartKind = "history"           // 往轮轨迹
	KindToolSpec PartKind = "toolspec"          // 工具清单
	KindObserved PartKind = "observation"       // 工具读回来的正文
	KindUser     PartKind = "user"              // 题面
	KindNotice   PartKind = "truncation-notice" // 「本节被裁过」那句知情通知
)

// Role 是「这一段是谁说的话」。
// 它不是装饰：runloop 有过一次真事故 —— 裁过的段一律被现搭成 user 说的话，
// 于是模型自己上一轮那句话在下一发里变成了人给的话。被裁的段丢掉了说话人，
// 事后就分不清裁掉的是模型的自述还是人的指令，而这两者的后果正好相反。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

func (r Role) Known() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// MayFold 答「这一类段能不能被折成桩」。只有内容可以折：
// 协议、题面、注入的记忆、说明书、工具清单各有各的「不许折」理由，
// 而那句通知本身就是用来交代折叠的，把它折成桩等于用一句话交代它自己。
// 白名单只此一处 —— 第二份定义就是缺陷。
func (k PartKind) MayFold() bool { return k == KindObserved || k == KindHistory }

// MayShrink 答「这一类段能不能被裁小或整段挤出去」。
// 只有协议是整段的：装不下就这一发不发（0 次请求），不许裁一半留下。
// 通知**可以**被裁尾 —— 这不是放宽，是照着证据写的：runloop 装配那一句通知时走的就是
// 通用那把裁刀，裁过之后另记一格「通知被裁了」。早先这里把通知也写成整段的，
// 于是「通知被裁尾」这个真形状在内核里无处可放，而本包自己的结构体上就带着一格 Trimmed。
func (k PartKind) MayShrink() bool {
	switch k {
	case KindObserved, KindHistory, KindMemory, KindUser, KindNotice:
		return true
	default:
		return false
	}
}

// Squeeze 是「这一段为什么变小了」。四个取值，三档后果（下面 CutLists 是唯一那份分档表）：
// 折过的还留着原文指纹能核回去，裁尾的留下了一个前缀，整段没进的模型彻底看不见。
// 不许把它们并成一个「一共挤了几段」的总数 —— 那样「能核回原文」与「彻底看不见」会互相抵销。
type Squeeze string

const (
	SqueezeNone            Squeeze = ""                 // 原样发出去了
	SqueezeFolded          Squeeze = "folded"           // 折成带原文指纹的桩
	SqueezeTailDropped     Squeeze = "tail-dropped"     // 留前缀、裁掉尾巴
	SqueezeDroppedWhole    Squeeze = "dropped-whole"    // 一个字都没进去（含桩都塞不下）
	SqueezeBudgetExhausted Squeeze = "budget-exhausted" // 总预算已花完，这段没轮到
)

// CutLists 是「裁剪表拆成哪几档」的**唯一一份定义**。
// Cells() 与 Folded/Clipped/Dropped 三个方法都从这里读；抄第二份的话，
// 加一档就会出现「账上有这一档、方法里没有」或反过来的漂，而没有东西会红。
// dropped-whole 与 budget-exhausted 合成一档是有意的：两者在模型那边同为「彻底看不见」，
// 区别只在成因，而成因留在每条 Cut 自己的 Squeeze 里，没有丢。
var CutLists = []struct {
	Key   string
	Label string
	Has   []Squeeze
}{
	{"folded", "折成桩", []Squeeze{SqueezeFolded}},
	{"clipped", "裁尾", []Squeeze{SqueezeTailDropped}},
	{"dropped", "整段没进", []Squeeze{SqueezeDroppedWhole, SqueezeBudgetExhausted}},
}

// CheckCutLists 核那张分档表自己：除 SqueezeNone 之外，每个取值必须恰好落在一档里。
// 落两档 ⇒ 同一段被数两遍；一档都没落 ⇒ 那一档在账上不存在，而它明明发生了。
func CheckCutLists() error {
	seen := map[Squeeze]string{}
	for _, l := range CutLists {
		if strings.TrimSpace(l.Key) == "" || strings.TrimSpace(l.Label) == "" {
			return fmt.Errorf("kernel: 裁剪分档表里有一档缺键名或缺说法：%+v", l)
		}
		if len(l.Has) == 0 {
			return fmt.Errorf("kernel: 裁剪分档 %s 一个取值都没收 —— 那一档永远是空的", l.Key)
		}
		for _, s := range l.Has {
			if s == SqueezeNone {
				return fmt.Errorf("kernel: 裁剪分档 %s 收了「没被挤」—— 没被挤的段不该出现在裁剪表里", l.Key)
			}
			if prev, dup := seen[s]; dup {
				return fmt.Errorf("kernel: %s 同时落在 %s 与 %s 两档里 —— 它会被数两遍", s, prev, l.Key)
			}
			seen[s] = l.Key
		}
	}
	for _, s := range []Squeeze{SqueezeFolded, SqueezeTailDropped, SqueezeDroppedWhole, SqueezeBudgetExhausted} {
		if _, ok := seen[s]; !ok {
			return fmt.Errorf("kernel: %s 一档都没落 —— 它发生了也不会在账上出现", s)
		}
	}
	return nil
}

// Cut 是一段被挤压之后的逐项留痕。
// runloop 今天只对折叠留了逐项账，裁尾与整段丢弃落账时只取了 len() ——
// 于是「哪一段没进去、它原来多大、拿什么核回原文」三问都答不出来，
// 只剩一句最多点名三段的人话。这一格就是冲那个缺陷来的：三类挤压同一张表。
type Cut struct {
	Kind    PartKind
	Ref     string // 这一段的身份（哪道题、哪一步、哪次工具调用）
	RefCall int    // 这一段来自**第几发**（0 = 不是从某一发来的，比如一步工具调用本身）
	// 这一格是给⑤用的：`Ref` 是人写的散文，读账本的东西没法拿它去跟账对。
	// 挤过的那一段要能机器地连回它来自哪一发，才谈得上以后核内容。
	// 实例：报告/挤历史-判生死.md —— 三把指纹单位不同，其中「它是第几发」这件事当时只在散文里。
	Role      Role // 它是谁说的话
	Squeeze   Squeeze
	FromBytes int64  // 原文多少字节
	ToBytes   int64  // 真发出去多少字节：折过=桩长，整段没进=0
	SHA256    string // 原文指纹，64 位十六进制。拿账上这一行能核回原文
}

// PartCount 是「某一类段在这一发里占了几段、多少字节」。
// 逐项而不是只给一个总数：runloop 的账上 parts 是一串类型名、bytes 是一个总数，
// 两者对不上时没有第三格能说话。
// 字节数是**真发出去的那一份**（折叠/裁尾之后），不是原文大小 —— 原文大小在 Cut.FromBytes。
type PartCount struct {
	Kind  PartKind
	Parts int
	Bytes int64
}

// Budget 是这一发的预算口径。它只能由 DeriveBudget 造出来、再由 Clamp 逐刀收紧 ——
// 「内容额度 = 单发上限 × (100 - 传输余量)%」这条公式只许有一个 owner。
// runloop 把这三个常数放在依赖最重的编排层里，于是问「观察档有多大」必须穿过整个框架；
// 口径住在内核里，框架只负责把人给的旗标递进来。
//
// 而「只能由 DeriveBudget 造出来」这句话不能只写在注释里：结构体是导出的，
// 谁都能手搭一个 ContentCeiling=999999 的 Budget。所以 InputMeta.Check 会把这条公式
// **重算一遍**并比对，手搭的那一份进不了门（这是注释不能替判据站岗那条纪律）。
type Budget struct {
	CallBytesCeiling  int64            // 单发请求体上限（人给的）
	TransportSlackPct int              // 留给 JSON 结构与传输开销的百分比
	ContentCeiling    int64            // 内容额度，由上面两个推出（公式的唯一出口）
	Shares            map[PartKind]int // 每类段占内容额度的百分比，加总 ≤ 100
	LeftoverPct       int              // 100 减去份额之和；省下来的自然归最旧的内容段
	FixedOverhead     Measure          // 骨架+模型名+工具清单的固定开销（估的或量的，不是对端说的）
	EffectiveCeiling  int64            // 这一发**真正生效**的内容额度（回夹之后）
	ClampRounds       int              // 回夹了几刀
}

// DeriveBudget 是预算口径的唯一入口。
// 份额之和**可以小于** 100（省下来的归最旧的内容段），但**不许大于** ——
// 那等于给了一个兑现不了的承诺。这一条是照着证据源写的，不是我自己定的：
// 早先这里写的是「必须恰好 100」，把证据源明确放宽的不变量收紧了，
// 于是一份 runloop 真能装配出来的预算在内核这里会被拒。
func DeriveBudget(ceiling int64, slackPct int, shares map[PartKind]int, overhead Measure) (Budget, error) {
	if ceiling <= 0 {
		return Budget{}, fmt.Errorf("kernel: 单发上限 %d 不可能是正的额度", ceiling)
	}
	if slackPct < 0 || slackPct >= 100 {
		return Budget{}, fmt.Errorf("kernel: 传输余量 %d%% 不在 [0,100) 里", slackPct)
	}
	sum := 0
	for k, v := range shares {
		if v < 0 {
			return Budget{}, fmt.Errorf("kernel: %s 的份额是 %d%%", k, v)
		}
		sum += v
	}
	if sum > 100 {
		return Budget{}, fmt.Errorf("kernel: %d 类段的份额相加是 %d，超过 100 —— 那是一个兑现不了的承诺", len(shares), sum)
	}
	if err := overhead.Check("固定开销"); err != nil {
		return Budget{}, err
	}
	if overhead.Decidable() && overhead.Basis != BasisEstimated && overhead.Basis != BasisMeasured {
		return Budget{}, fmt.Errorf("kernel: 固定开销那一格的出处是 %s —— 它是我们估的或量的，不是对端说的", overhead.Basis)
	}
	cp := make(map[PartKind]int, len(shares))
	for k, v := range shares {
		cp[k] = v
	}
	// 这条公式在包里出现三次，而这里才是**唯一的 owner**，另外两处不是抄本：
	//   · Budget.check 把它重算一遍再比对 —— 那是「owner 要被复核」。公式一改它就**大声**红
	//     （每一份合法预算都会被拒），不是静默漂；它同时也是抓手搭预算的那道闸。
	//   · BytesToHoldObservations 是它的反函数 —— 公式一改它会**静默**算错，所以那边有一条
	//     在一段范围上往返核的判据盯着，探针里也有一根专门把取整方向切反。
	// 所以看见三处就动手「合并」的话，被删掉的多半是复核那一份 —— 那正是最不该删的一处。
	derived := ceiling * int64(100-slackPct) / 100
	return Budget{
		CallBytesCeiling:  ceiling,
		TransportSlackPct: slackPct,
		ContentCeiling:    derived,
		Shares:            cp,
		LeftoverPct:       100 - sum,
		FixedOverhead:     overhead,
		EffectiveCeiling:  derived,
	}, nil
}

// Clamp 记一刀回夹：真编码出来的请求体超了上限，就把内容额度收小一点重装配。
// 每一刀必须真收小 —— 一刀没夹小还继续夹，就是拿循环换一个不会到的结论
// （runloop 那边的原话是「上一刀没夹小就立刻停手」）。
func (b Budget) Clamp(effective int64) (Budget, error) {
	if effective <= 0 {
		return b, fmt.Errorf("kernel: 回夹到 %d 字节 —— 那就什么都装不下了，该报「额度太紧」而不是夹到 0", effective)
	}
	if effective >= b.EffectiveCeiling {
		return b, fmt.Errorf("kernel: 这一刀夹到 %d 字节，而生效额度已经是 %d —— 没收小就不是一刀", effective, b.EffectiveCeiling)
	}
	b.EffectiveCeiling = effective
	b.ClampRounds++
	return b, nil
}

// ShareOf 是某一类段在这一发里最多能吃多少字节（按**生效**额度算，不按公式算出来的那个）。
// 不吃份额的那几类返回 0 与 false —— 0 不是「份额是零」，是「这一类没登记份额」，
// 两件事必须分得开，否则「一类段没登记」会被读成「它这次分到了 0 字节」。
func (b Budget) ShareOf(k PartKind) (int64, bool) {
	pct, ok := b.Shares[k]
	if !ok {
		return 0, false
	}
	return b.EffectiveCeiling * int64(pct) / 100, true
}

// ObservationShare 是观察档（工具读回来的正文）在这一发里最多能吃多少字节。
// 它是 ShareOf 的一个具名出口，因为「跑不完」这一族问题的第一个算术就是它：
// 拿这个数去比材料字节数，就知道会不会折、会不会重读 —— 先算这三个数，再谈模型能力。
func (b Budget) ObservationShare() (int64, bool) { return b.ShareOf(KindObserved) }

// BytesToHoldObservations 是 ShareOf 的反函数：要让观察档装得下 n 字节，
// 单发上限至少得给到多少。它必须与正函数住在同一处 ——
// 比例（(100-余量)% × 观察份额%）抄到第二处就不再是同一个数了。
func BytesToHoldObservations(b Budget, n int64) (int64, error) {
	pct, ok := b.Shares[KindObserved]
	if !ok || pct <= 0 {
		return 0, fmt.Errorf("kernel: 预算里没有观察档的份额，反算不出来")
	}
	if b.TransportSlackPct >= 100 {
		return 0, fmt.Errorf("kernel: 传输余量 %d%% 把内容额度吃光了", b.TransportSlackPct)
	}
	// 向上取整：正函数一路向下取整，反函数不向上取整就会算出一个装不下的上限。
	content := (n*100 + int64(pct) - 1) / int64(pct)
	return (content*100 + int64(99-b.TransportSlackPct)) / int64(100-b.TransportSlackPct), nil
}

// Notice 是那句知情通知自己的下场。两个布尔 + 一句理由 + 正文，四种组合里有一种是矛盾的：
// 没说（模型不知道自己看的是节选）、说了、说了但只说了半句；
// 而「没说」与「被裁了半句」不能同时成立 —— 没发出去的东西裁不了。
type Notice struct {
	Sent    bool
	Trimmed bool
	Why     string // 没发出去的理由；有裁剪却没发时必填
	Text    string
}

// InputMeta 是「一发请求装配完之后，关于我们发了什么」的全部格子。
// 它不认识任何框架类型：装配的动作在框架里，装配的账在这里。
type InputMeta struct {
	Call  int         // 第几发，1 起
	Order []PartKind  // 按**发送顺序**的段名表，含重复。顺序就是花钱顺序，也是可复用前缀的判据
	Parts []PartCount // 每类段发了几段、多少字节（按类聚合，与 Order 是两个量，所以是两个键名）
	// ContentBytes 是各段正文之和。**不是** JSON 请求体字节 —— 那一格在 OutputMeta，
	// 且换了名字（wire_bytes_out）。两个量共用一个键名的代价是实测过的：6327 与 11570。
	ContentBytes int64
	Tokens       Measure // token 数，口径只能是估算或转述：分词器在对端
	Budget       Budget
	Cuts         []Cut
	Notice       Notice
}

// Folded / Clipped / Dropped 是三张分开的名单，全部从 CutLists 那一份定义读出来。
func (m InputMeta) Folded() []Cut  { return m.CutList("folded") }
func (m InputMeta) Clipped() []Cut { return m.CutList("clipped") }
func (m InputMeta) Dropped() []Cut { return m.CutList("dropped") }

// CutList 按分档表取一档。键名不在表里就返回 nil 与 false ——
// 一个拼错的键名静静返回一个空名单，会被读成「这一档没发生」。
func (m InputMeta) CutList(key string) []Cut {
	var want []Squeeze
	for _, l := range CutLists {
		if l.Key == key {
			want = l.Has
			break
		}
	}
	if want == nil {
		return nil
	}
	// 没交这张表，与交了一张空表，是两个不同的事实 —— 解码之后它们本来分得开
	// （缺键 ⇒ nil，`"Cuts": []` ⇒ 非 nil 空），一旦走到下面那句 `out := []Cut{}`
	// 就被抹成同一个空表，而那空表会带着「实测」的出处落账：
	// 账上留下一句「0 段（裁剪表是空的）」，读的人分不清「确认这一档没发生」与「压根没说」。
	// 这一条是缺口 #66 的本体，改它必须连 cells.go 的 cutCell 与 record.go 的 basisOf 一起改。
	if m.Cuts == nil {
		return nil
	}
	out := []Cut{}
	for _, c := range m.Cuts {
		for _, s := range want {
			if c.Squeeze == s {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Check 把这一发的账自己核一遍。每条都对应一个真实发生过的形状。
func (m InputMeta) Check() error {
	if m.Call < 1 {
		return fmt.Errorf("kernel: 第几发是 %d，发号从 1 起（0 会被读成「还没发」）", m.Call)
	}
	if err := CheckCutLists(); err != nil {
		return err
	}
	if err := m.Budget.check(); err != nil {
		return err
	}
	if err := m.Tokens.Check("token 数"); err != nil {
		return err
	}
	if !m.Tokens.Absent && m.Tokens.Basis != BasisEstimated && m.Tokens.Basis != BasisRelayed {
		return fmt.Errorf("kernel: token 那一格的出处是 %s —— 我们数不出对端的分词，只有估算或转述两种可能", m.Tokens.Basis)
	}
	if len(m.Parts) == 0 {
		return fmt.Errorf("kernel: 段表是空的 —— 一发请求至少有一段（协议或题面），空表会被读成「这一发什么都没装」")
	}
	if len(m.Order) == 0 {
		return fmt.Errorf("kernel: 发送顺序表是空的 —— 顺序就是花钱顺序，丢了它就看不出可复用前缀稳不稳")
	}

	var sum, orderCount int64
	seen := map[PartKind]int{}
	for _, p := range m.Parts {
		if p.Parts <= 0 {
			return fmt.Errorf("kernel: %s 那一行报了 %d 段 —— 0 段就不该占一行", p.Kind, p.Parts)
		}
		if p.Bytes < 0 {
			return fmt.Errorf("kernel: %s 那一行报了 %d 字节", p.Kind, p.Bytes)
		}
		if _, dup := seen[p.Kind]; dup {
			return fmt.Errorf("kernel: %s 在段表里出现了两次 —— 一类段一行，两行就会被数两遍", p.Kind)
		}
		seen[p.Kind]++
		orderCount += int64(p.Parts)
		// 每一类要发的段都必须登记过份额。这一条不留豁免：
		// 早先这里豁免了协议/说明书/工具清单三类，而证据源对**每一类**都先查份额、
		// 没登记就装配失败 —— 于是内核收得下的一份账，框架压根装配不出来。
		// 而「协议不按份额封顶」是另一件事（它整段发或整段不发），住在 MayShrink 里，不住在这里。
		if _, ok := m.Budget.ShareOf(p.Kind); !ok {
			return fmt.Errorf("kernel: %s 发了 %d 段，但预算里没有它的份额", p.Kind, p.Parts)
		}
		sum += p.Bytes
	}
	if int64(len(m.Order)) != orderCount {
		return fmt.Errorf("kernel: 发送顺序表有 %d 项，而段表相加是 %d 段 —— 两个数得是从同一批段数出来的",
			len(m.Order), orderCount)
	}
	// 两个方向都要核：顺序表里有段表没有的类 ⇒ 那一类没记账；
	// 段表里有顺序表没出现的类 ⇒ 那一类记了账却说不出它排在哪儿。
	ordered := map[PartKind]int{}
	for _, k := range m.Order {
		ordered[k]++
		if _, ok := seen[k]; !ok {
			return fmt.Errorf("kernel: 发送顺序表里有 %s，而段表里没有它那一行", k)
		}
	}
	// 按段表的顺序遍历，不按 map 遍历：报红时点名哪一类必须是确定的，
	// 否则同一份坏账两次跑出两句不同的话，下一个人会以为是两个缺陷。
	for _, p := range m.Parts {
		if ordered[p.Kind] != p.Parts {
			return fmt.Errorf("kernel: %s 在段表里记了 %d 段，在发送顺序表里却出现 %d 次 —— 两张表数的是同一批段",
				p.Kind, p.Parts, ordered[p.Kind])
		}
	}
	// 各段字节相加必须等于内容总字节。这条是「同一个量由两处各算一遍」的探针：
	// runloop 的计划字节与真过网线字节差过 6327 → 11570，
	// 而两个数共用一个键名，所以那一次没有任何一格能说出它们不是一回事。
	if sum != m.ContentBytes {
		return fmt.Errorf("kernel: 各类段相加是 %d 字节，而内容总字节记的是 %d —— 两个数各算了一遍，至少一个是错的", sum, m.ContentBytes)
	}
	if m.ContentBytes > m.Budget.EffectiveCeiling {
		return fmt.Errorf("kernel: 内容 %d 字节超过了这一发真正生效的额度 %d 字节（本该被回夹；公式算出来的是 %d，夹过 %d 刀）",
			m.ContentBytes, m.Budget.EffectiveCeiling, m.Budget.ContentCeiling, m.Budget.ClampRounds)
	}
	for i := range m.Cuts {
		if err := m.Cuts[i].check(i); err != nil {
			return err
		}
	}
	// ⑤ 被挤掉的**历史**段必须说得出它来自第几发，且那一发必须在它之前发生过。
	// `Ref` 那一格是人写的散文，读账本的东西拿它没法跟账对 —— 这一格是第一个能机器用的跨发引用。
	// 实例：报告/挤历史-判生死.md，三把指纹单位不同，「它是第几发」当时只活在散文里。
	for i, c := range m.Cuts {
		if c.Kind != KindHistory {
			continue
		}
		if c.RefCall <= 0 {
			return fmt.Errorf("kernel: 第 %d 条裁剪记录挤掉了一段历史，却没说它来自第几发（RefCall=%d）", i+1, c.RefCall)
		}
		if c.RefCall >= m.Call {
			return fmt.Errorf("kernel: 第 %d 条裁剪记录说那段历史来自第 %d 发，可现在才第 %d 发 —— 引用未来的那一发，账上核不到",
				i+1, c.RefCall, m.Call)
		}
	}
	if err := m.checkCutsAgainstParts(); err != nil {
		return err
	}
	// 裁过了却不告诉模型，也不说为什么不告诉 —— 这是「只写进账本、没发出去」那一族。
	if len(m.Cuts) > 0 && !m.Notice.Sent && strings.TrimSpace(m.Notice.Why) == "" {
		return fmt.Errorf("kernel: 这一发挤掉了 %d 段，却没把那句知情通知发出去，也没说为什么", len(m.Cuts))
	}
	if !m.Notice.Sent && m.Notice.Trimmed {
		return fmt.Errorf("kernel: 通知报的是「没发出去」，同时又报「被裁了半句」—— 没发出去的东西裁不了")
	}
	// 说了却没有正文，等于没说：账上会留下一格「通知已送达」，而收件人收到的是空。
	if m.Notice.Sent && strings.TrimSpace(m.Notice.Text) == "" {
		return fmt.Errorf("kernel: 通知报的是「发出去了」，却没有正文")
	}
	// 这两格的话会被 `read_back` 原样交给下一个读账的人（常常就是另一个 Agent）。
	// 内核不判语义，能钉的只有形状：一格**能吞掉多少下游上下文**必须有上限（见 kernel.go 的 MaxFreeText）。
	if err := checkFreeText("通知理由(Notice.Why)", m.Notice.Why); err != nil {
		return err
	}
	if err := checkFreeText("通知正文(Notice.Text)", m.Notice.Text); err != nil {
		return err
	}
	// ④ 这一条把「发出去了」钉在**真发出去的段表**上 —— 实例是本 session 撞出来的（不是伪造）：
	// 驱动的折叠逻辑排在附通知之前，于是裁剪表多了一条记录、Notice.Sent 写着 true，
	// 而段里没有那句通知，内核当时整发照收。见 报告/挤历史-判生死.md 第 7 节与 README §8 第 15 条。
	noticeInParts := false
	for _, k := range m.Order {
		if k == KindNotice {
			noticeInParts = true
			break
		}
	}
	if m.Notice.Sent && !noticeInParts {
		return fmt.Errorf("kernel: 通知报的是「发出去了」，可段表里没有 truncation-notice 那一段 —— 发不出去就不算通知过")
	}
	if !m.Notice.Sent && noticeInParts {
		return fmt.Errorf("kernel: 段表里明明有 truncation-notice 那一段（它真发出去了），通知却报「没发出去」")
	}
	return CheckCells("输入侧", m, m.Cells())
}

// checkCutsAgainstParts 把裁剪表与段表对一遍：某一类段真发出去的字节，
// 不可能比它那些被裁记录里「留下来」的字节还少。
// 这一条盯的是 InputMeta 里那句「Parts 记的是真发出去的那一份」——
// 一句写在注释里的口径，如果没有任何判据把两张表对过，那它就是散文。
func (m InputMeta) checkCutsAgainstParts() error {
	kept := map[PartKind]int64{}
	for _, c := range m.Cuts {
		kept[c.Kind] += c.ToBytes
	}
	for _, p := range m.Parts {
		if k := kept[p.Kind]; k > p.Bytes {
			return fmt.Errorf("kernel: %s 的裁剪记录说留下了 %d 字节，而段表说这一类一共才发了 %d 字节 —— 两张表对不上",
				p.Kind, k, p.Bytes)
		}
	}
	for k, keptBytes := range kept {
		// 这一类全被整段挤出去时，段表里没有它那一行是对的（0 段的行本来就是噪音）——
		// 只有在真留下了字节的时候，才要求它在段表里有一行。
		if keptBytes == 0 {
			continue
		}
		if _, ok := seen2(m.Parts, k); !ok {
			return fmt.Errorf("kernel: %s 的裁剪记录说留下了 %d 字节，段表里却没有它那一行", k, keptBytes)
		}
	}
	return nil
}

func seen2(parts []PartCount, k PartKind) (int, bool) {
	for _, p := range parts {
		if p.Kind == k {
			return p.Parts, true
		}
	}
	return 0, false
}

// check 复核预算自己是否自洽。它把 DeriveBudget 那条公式**重算一遍** ——
// 「公式只有一个 owner」这句话靠这里成立，不靠注释。
func (b Budget) check() error {
	if b.CallBytesCeiling <= 0 {
		return fmt.Errorf("kernel: 单发上限是 %d —— 这一份预算不是 DeriveBudget 造出来的", b.CallBytesCeiling)
	}
	if b.TransportSlackPct < 0 || b.TransportSlackPct >= 100 {
		return fmt.Errorf("kernel: 传输余量是 %d%%，不在 [0,100) 里", b.TransportSlackPct)
	}
	if want := b.CallBytesCeiling * int64(100-b.TransportSlackPct) / 100; b.ContentCeiling != want {
		return fmt.Errorf("kernel: 内容额度记的是 %d，而按公式（上限 %d 扣 %d%% 余量）该是 %d —— 这条公式有了第二个 owner",
			b.ContentCeiling, b.CallBytesCeiling, b.TransportSlackPct, want)
	}
	sum := 0
	for k, v := range b.Shares {
		if v < 0 {
			return fmt.Errorf("kernel: %s 的份额是 %d%%", k, v)
		}
		sum += v
	}
	if sum > 100 {
		return fmt.Errorf("kernel: %d 类段的份额相加是 %d，超过 100", len(b.Shares), sum)
	}
	if b.LeftoverPct != 100-sum {
		return fmt.Errorf("kernel: 剩余额度记的是 %d%%，而份额相加是 %d（该剩 %d）—— 两个数各算了一遍",
			b.LeftoverPct, sum, 100-sum)
	}
	if err := b.FixedOverhead.Check("固定开销"); err != nil {
		return err
	}
	if b.FixedOverhead.Decidable() && b.FixedOverhead.Basis != BasisEstimated && b.FixedOverhead.Basis != BasisMeasured {
		return fmt.Errorf("kernel: 固定开销那一格的出处是 %s —— 它是我们估的或量的，不是对端说的", b.FixedOverhead.Basis)
	}
	if b.EffectiveCeiling <= 0 {
		return fmt.Errorf("kernel: 生效额度是 %d 字节 —— 什么都装不下", b.EffectiveCeiling)
	}
	if b.EffectiveCeiling > b.ContentCeiling {
		return fmt.Errorf("kernel: 生效额度 %d 比公式算出来的 %d 还大 —— 回夹只会收小，不会放大",
			b.EffectiveCeiling, b.ContentCeiling)
	}
	if b.ClampRounds < 0 {
		return fmt.Errorf("kernel: 回夹了 %d 刀", b.ClampRounds)
	}
	if b.ClampRounds == 0 && b.EffectiveCeiling != b.ContentCeiling {
		return fmt.Errorf("kernel: 生效额度 %d 与公式算出来的 %d 不一样，却记着 0 刀回夹 —— 那一刀没记账",
			b.EffectiveCeiling, b.ContentCeiling)
	}
	if b.ClampRounds > 0 && b.EffectiveCeiling == b.ContentCeiling {
		return fmt.Errorf("kernel: 记着 %d 刀回夹，生效额度却与公式算出来的一样 —— 刀数与结果对不上", b.ClampRounds)
	}
	return nil
}

func (c Cut) check(i int) error {
	where := fmt.Sprintf("第 %d 条裁剪记录（%s/%s）", i+1, c.Kind, c.Ref)
	if c.Squeeze == SqueezeNone {
		return fmt.Errorf("kernel: %s 没被挤，就不该出现在裁剪表里", where)
	}
	if !c.Role.Known() {
		return fmt.Errorf("kernel: %s 的说话人是 %q，不在四种取值里 —— 被裁的段丢了说话人，事后就分不清裁掉的是模型自述还是人的指令",
			where, string(c.Role))
	}
	if c.FromBytes < 0 || c.ToBytes < 0 {
		return fmt.Errorf("kernel: %s 原文 %d 字节、发出去 %d 字节", where, c.FromBytes, c.ToBytes)
	}
	if err := checkFingerprint(where+" 的原文指纹", c.SHA256); err != nil {
		return err
	}
	if strings.TrimSpace(c.Ref) == "" {
		return fmt.Errorf("kernel: %s 没有 ref —— 事后谁也说不清被挤掉的是哪一段", where)
	}
	switch c.Squeeze {
	case SqueezeFolded:
		if !c.Kind.MayFold() {
			return fmt.Errorf("kernel: %s 被折成了桩，但 %s 这一类不许折", where, c.Kind)
		}
		if c.FromBytes <= 0 {
			return fmt.Errorf("kernel: %s 原文 0 字节 —— 0 字节的段无从折起", where)
		}
		// 桩是整段的：塞不下就记成「整段没进」，不许发半截桩。
		if c.ToBytes <= 0 {
			return fmt.Errorf("kernel: %s 折过之后是 %d 字节 —— 桩要么整段发，要么记成整段没进", where, c.ToBytes)
		}
		if c.ToBytes >= c.FromBytes {
			return fmt.Errorf("kernel: %s 折过之后 %d 字节，原文才 %d 字节 —— 那它没被折，是被变大了", where, c.ToBytes, c.FromBytes)
		}
	case SqueezeTailDropped:
		if !c.Kind.MayShrink() {
			return fmt.Errorf("kernel: %s 被裁尾，但 %s 这一类是整段的", where, c.Kind)
		}
		if c.FromBytes <= 0 {
			return fmt.Errorf("kernel: %s 原文 0 字节 —— 0 字节的段无从裁起", where)
		}
		if c.ToBytes <= 0 || c.ToBytes >= c.FromBytes {
			return fmt.Errorf("kernel: %s 留了 %d 字节、原文 %d 字节 —— 裁尾要留下一个非空的真前缀", where, c.ToBytes, c.FromBytes)
		}
	case SqueezeDroppedWhole, SqueezeBudgetExhausted:
		if !c.Kind.MayShrink() {
			return fmt.Errorf("kernel: %s 记成 %s，但 %s 这一类是整段的", where, c.Squeeze, c.Kind)
		}
		if c.ToBytes != 0 {
			return fmt.Errorf("kernel: %s 说是 %s，却记着发出去 %d 字节", where, c.Squeeze, c.ToBytes)
		}
		// 原文 0 字节是允许的：一个空段在预算花完时确实会被记成「没轮到」。
	default:
		return fmt.Errorf("kernel: %s 的 squeeze 是 %q，不在四种取值里", where, string(c.Squeeze))
	}
	return nil
}

// checkFingerprint 是「指纹必须是 64 位十六进制」这**一条**规则的唯一 owner。
// 它以前在输入侧与输出侧各写了一遍（四行几乎逐字相同），
// 而两份拷贝里只有一份被测试守过 —— 换成别的摘要算法时，改一处另一处不会红。
func checkFingerprint(where, s string) error {
	if len(s) != 64 {
		return fmt.Errorf("kernel: %s是 %d 位，不是 64 位十六进制 —— 拿它核不回原文", where, len(s))
	}
	if _, err := hex.DecodeString(s); err != nil {
		return fmt.Errorf("kernel: %s不是十六进制：%v", where, err)
	}
	return nil
}

// SortedKinds 把段表里的类型名排好序交出来，供渲染与落账用。
// 排序只此一处：一个来自 map 的表如果不排序就落账，两份账逐行比对会因为遍历顺序而「对不上」。
func SortedKinds(shares map[PartKind]int) []string {
	out := make([]string, 0, len(shares))
	for k, v := range shares {
		out = append(out, fmt.Sprintf("%s=%d", string(k), v))
	}
	sort.Strings(out)
	return out
}
