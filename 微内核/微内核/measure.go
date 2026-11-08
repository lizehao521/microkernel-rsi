package kernel

import (
	"fmt"
	"strconv"
	"strings"
)

// Measure 是「一个数 + 这个数是谁算的 + 这一发到底有没有这个数」。
//
// 两根轴刻意分开住，不许合成一个枚举：
//   - Basis 答「谁算的」：我们量的 / 转述别人的 / 估的 / 模型自述的。
//   - Absent 答「有没有」：这一发在结构上就没有这一格（非流式那一发没有首帧时间）。
//
// 合成一个枚举的后果是实测过的：runloop 把首帧时间只在流式那一支写账，
// 而非流式那一发的分帧数写成 0 —— 0 会被下一个人读成「一帧都没收到」，
// 而它真正的意思是「这一发压根不分帧」。缺席必须长得和 0 不一样。
type Measure struct {
	N       int64
	Basis   Basis
	Source  string // 只有 BasisRelayed 需要：转述的是谁
	Absent  bool
	Refused bool   // 对端给了数、我们判成不可信 —— 与「结构上没有这一格」分开
	Why     string // 缺席/被拒的理由，可空；有值时一定跟着渲染出来
}

// MeasuredOf 是我们自己数出来的（字节数、退出码、行号、覆盖区间）。
func MeasuredOf(n int64) Measure { return Measure{N: n, Basis: BasisMeasured} }

// EstimatedOf 是估的。渲染时永远带着「这是估算，不是测量」那半句 ——
// 一个光秃秃的 ≈N 会被抄进结论，然后没人记得它是估的。
// 它也必须说清**估的是什么**：一个说不出估算依据的估算，没法被反驳，
// 也就没法在将来被真读数替换掉。
func EstimatedOf(n int64, source string) Measure {
	return Measure{N: n, Basis: BasisEstimated, Source: source}
}

// RelayedOf 是别人算的、我们只是收下（对端自报的 token 用量、判分器报的条数）。
// 必须带出处：一个说不出转述谁的数，和模型自述的数在证据上没有区别。
func RelayedOf(n int64, source string) Measure {
	return Measure{N: n, Basis: BasisRelayed, Source: source}
}

// needsSource 是「哪几种出处必须能追到来源」的**唯一一份**判据。
// 读它的有三处：Measure.Check 直接读；CheckCells 直接读；
// 账本那一层经 checkRecord 读（Append 与 plan 共用 checkRecord，所以是**一条**链而不是两条）。
// 各写一份的话会慢慢漂开 —— 这一次就是：账本那层要求估算也带来源，
// 而 Measure 那层原先只要求转述带，于是一份合法的 Measure 在落账时才被拒。
func needsSource(b Basis) bool { return b == BasisRelayed || b == BasisEstimated }

// AbsentBecause 记一格「这一发没有这个数」。理由可以空，但空理由的缺席
// 与「忘了填」在渲染上必须能分开 —— 前者是 —（没量到），后者在门口就被拒。
func AbsentBecause(why string) Measure {
	return Measure{Absent: true, Why: why}
}

// Distrusted 记一格「对端给了数，但那数不可能是真的」，于是我们收下理由、丢掉数。
// 与 Absent 分开：Absent 是结构上没有，Distrusted 是有、但被判成不可信 ——
// 所以渲染必须是两句话，共用一句「没量到」的话，对 Distrusted 那一格是假话（我们量到了，只是没收）。
// 名字刻意不叫 Rejected：本包里 Ledger.Rejected() 数的是「被门口拒收的写入次数」，
// 两个「拒」撞在一个账上，读到 rejected 3 就分不清是哪一种。
func Distrusted(why string, got int64) Measure {
	return Measure{Absent: true, Refused: true, Why: fmt.Sprintf("%s（对端给的是 %d，没收）", why, got)}
}

// Check 是这一格的门口。field 只用于把话说清楚：报红时报不出是哪一格，
// 下一个人就得去猜（runloop 里「一句空红比不报更糟」那一族）。
func (m Measure) Check(field string) error {
	if m.Absent {
		// 缺席却带着数或出处 = 一个 0 正在冒充「没量到」，或一个没量到的格正在冒充实测。
		if m.N != 0 {
			return fmt.Errorf("kernel: %s 报的是缺席，却带着数 %d —— 缺席不许顺手塞一个值", field, m.N)
		}
		if m.Basis != BasisUnset {
			return fmt.Errorf("kernel: %s 报的是缺席，却带着出处 %s —— 没量到的东西说不出是谁算的", field, m.Basis)
		}
		if m.Refused && strings.TrimSpace(m.Why) == "" {
			return fmt.Errorf("kernel: %s 说是被判成不可信的，却没说为什么，也没留住对端给的那个数", field)
		}
		return nil
	}
	if m.Refused {
		return fmt.Errorf("kernel: %s 说是被判成不可信的，却又不是缺席 —— 那它到底收没收这个数", field)
	}
	if m.Basis == BasisUnset {
		return fmt.Errorf("kernel: %s 有数 %d 却没说谁算的（basis 空）—— 这一格会被下游当成实测", field, m.N)
	}
	if needsSource(m.Basis) && strings.TrimSpace(m.Source) == "" {
		return fmt.Errorf("kernel: %s 说是 %s 的，却没说来源（转述谁 / 估的是什么）", field, m.Basis)
	}
	return nil
}

// Decidable 答「这个数能不能拿去做判定」。
// 缺席没有数；估算与模型自述由 Basis.Decidable 那**一份**判据裁，这里只加上缺席那一半。
func (m Measure) Decidable() bool { return !m.Absent && m.Basis.Decidable() }

func (m Measure) String() string {
	if m.Absent {
		// 两句话必须不一样：对「被判成不可信」那一格说「没量到」是假话 —— 我们量到了，只是没收。
		s := "—（没量到，不是 0）"
		if m.Refused {
			s = "—（对端给了数，被判成不可信，没收；不是 0）"
		}
		if m.Why != "" {
			s += "：" + m.Why
		}
		return s
	}
	n := strconv.FormatInt(m.N, 10)
	switch m.Basis {
	case BasisEstimated:
		s := "≈" + n + "（这是估算，不是测量）"
		if m.Source != "" {
			s += "：估的是" + m.Source
		}
		return s
	case BasisRelayed:
		return n + "（转述自 " + m.Source + "）"
	case BasisModelSays:
		return n + "（模型自述，不是事实）"
	default:
		return n
	}
}

// Total 是带分母的合计。N() 交出去的确是一个光秃秃的 int64 ——
// 所以规矩不在类型上，在读的人身上：N() 必须与 Counts()/Complete() 一起读，
// 单读 N() 就等于把「混了一笔估算的合计」当成一个事实抄走。
// 混进一笔估算或一笔缺席之后 Complete() 就是假，而 String() 会把分母一起说出来。
type Total struct {
	n         int64
	measured  int
	relayed   int
	estimated int
	absent    int
	refused   int
}

// Add 收一笔。模型自述的数一律拒收 —— 那不是「口径弱一点」，
// 是说话人给自己打分；让它进合计，合计就成了它的转述。
func (t *Total) Add(m Measure) error {
	if err := m.Check("合计里的一笔"); err != nil {
		return err
	}
	if m.Basis == BasisModelSays {
		return fmt.Errorf("kernel: 模型自述的数 %d 不许进合计（它是线索，不是事实）", m.N)
	}
	switch {
	case m.Absent:
		t.absent++
		if m.Refused {
			t.refused++
		}
	case m.Basis == BasisMeasured:
		t.measured++
		t.n += m.N
	case m.Basis == BasisRelayed:
		t.relayed++
		t.n += m.N
	case m.Basis == BasisEstimated:
		t.estimated++
		t.n += m.N
	}
	return nil
}

// Complete 答「这个合计是不是一个完整的事实」。
// 只有每一笔都是我们量的才算；转述的能追到出处但不是我们量的，
// 估算与缺席更要说出来。
func (t *Total) Complete() bool {
	return t.Counts() == t.measured && t.measured > 0
}

// Counts 是分母：一共收了几笔（含缺席的那些）。
func (t *Total) Counts() int {
	return t.measured + t.relayed + t.estimated + t.absent
}

func (t *Total) N() int64 { return t.n }

func (t *Total) String() string {
	parts := []string{"合计 " + strconv.FormatInt(t.n, 10)}
	denom := []string{}
	if t.measured > 0 {
		denom = append(denom, "实测 "+strconv.Itoa(t.measured)+" 笔")
	}
	if t.relayed > 0 {
		denom = append(denom, "转述 "+strconv.Itoa(t.relayed)+" 笔")
	}
	if t.estimated > 0 {
		denom = append(denom, "估算 "+strconv.Itoa(t.estimated)+" 笔")
	}
	if t.absent > 0 {
		denom = append(denom, "缺席 "+strconv.Itoa(t.absent)+" 笔")
	}
	if t.refused > 0 {
		denom = append(denom, "缺席里有 "+strconv.Itoa(t.refused)+" 笔是对端给了数、被判成不可信")
	}
	if len(denom) == 0 {
		return parts[0] + "（一笔都没有，这个合计是空的）"
	}
	parts = append(parts, "由 "+strings.Join(denom, "、")+" 加出来")
	if !t.Complete() {
		parts = append(parts, "这不是一个完整的事实")
	}
	return strings.Join(parts, "，")
}
