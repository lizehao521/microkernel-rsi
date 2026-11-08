package kernel

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 这一份文件是元数据两侧与那道出处闸门账本之间**唯一的接线**。
//
// 没有它的话，两半是各自成立的孤岛：Check() 能把一份坏账挡在门外，
// 而 Ledger 能给带出处的说法上哈希链 —— 但元数据从来没真进过那条链，
// 「缺依据的写入在门口被拒，且拒收本身有数」这句话对元数据那一侧就是空的：
// 拒了，没人计数；收了，也没落到能被第三方重算的地方。

// 账本上两种条目的名字。
const (
	KindModelInput  = "model_input"
	KindModelOutput = "model_output"
)

// RecordInput 把一发请求的输入侧账写进账本，逐格一条。返回写进去几条。
//
// 两条都有账：Check 不过 ⇒ 拒收计数 +1、一条都不写、原因返回给调用方；
// Check 过了 ⇒ 每一格各成一条带出处与指纹的记录。
// 刻意不做「写了半发」这种状态 —— 半截记录会让下游的「不拿半截历史续跑」那类守卫
// 拒掉整条链，而根因其实在我们这边（证据源那个仓库真踩过一次）。
func (l *Ledger) RecordInput(session, actor string, m InputMeta) (int, error) {
	if err := m.Check(); err != nil {
		l.noteRejected()
		return 0, fmt.Errorf("kernel: 输入侧账不自洽，第 %d 发整发拒收：%v", m.Call, err)
	}
	recs, err := l.plan(session, actor, m.Call, KindModelInput, m.Cells())
	if err != nil {
		l.noteRejected()
		return 0, err
	}
	return l.commit(recs)
}

// RecordOutput 把一发响应的输出侧账写进账本，逐格一条。返回写进去几条。
func (l *Ledger) RecordOutput(session, actor string, m OutputMeta) (int, error) {
	if err := m.Check(); err != nil {
		l.noteRejected()
		return 0, fmt.Errorf("kernel: 输出侧账不自洽，第 %d 发整发拒收：%v", m.Call, err)
	}
	recs, err := l.plan(session, actor, m.Call, KindModelOutput, m.Cells())
	if err != nil {
		l.noteRejected()
		return 0, err
	}
	return l.commit(recs)
}

// RecordCall 把一发的两侧一起写进去。任一侧不自洽 ⇒ 整发拒收、两侧都不写：
// 只写一侧的话，账上会出现「有请求没响应」或反过来的半发，
// 而那正是下游最难诊断的形状（它长得像管路断了）。
func (l *Ledger) RecordCall(session, actor string, in InputMeta, out OutputMeta) (int, error) {
	if err := in.Check(); err != nil {
		l.noteRejected()
		return 0, fmt.Errorf("kernel: 输入侧账不自洽，第 %d 发整发拒收（输出侧也不写）：%v", in.Call, err)
	}
	if err := out.Check(); err != nil {
		l.noteRejected()
		return 0, fmt.Errorf("kernel: 输出侧账不自洽，第 %d 发整发拒收（输入侧也不写）：%v", out.Call, err)
	}
	if in.Call != out.Call {
		l.noteRejected()
		return 0, fmt.Errorf("kernel: 两侧的发号对不上（输入 %d、输出 %d）—— 它们不是同一发", in.Call, out.Call)
	}
	// 两侧**全部翻译完**才开始落账。这一步是「不写半发」的结构性保证，
	// 不是靠「Check 恰好挡掉了所有会让 Append 失败的形状」涌现出来的 ——
	// 后者今天成立，但它是两层之间的隐式契约：将来谁加一种新格子或新出处档，
	// 让 Check 过而 Append 拒，就会悄悄在链上留下半发，而链自己照样自洽。
	inRecs, err := l.plan(session, actor, in.Call, KindModelInput, in.Cells())
	if err != nil {
		l.noteRejected()
		return 0, err
	}
	outRecs, err := l.plan(session, actor, out.Call, KindModelOutput, out.Cells())
	if err != nil {
		l.noteRejected()
		return 0, err
	}
	n, err := l.commit(inRecs)
	if err != nil {
		return n, err
	}
	m, err := l.commit(outRecs)
	return n + m, err
}

// plan 把一侧的格子全部翻译成账本记录，**一条都不写**。
// 翻译与落账分成两步是有意的，见 RecordCall 里那段。
func (l *Ledger) plan(session, actor string, step int, kind string, cells []Cell) ([]Record, error) {
	// 链只证明「写下来的没被动过」，证明不了「不该写的没写」。
	// 同一发记两遍，哈希链照样自洽，而任何按键名扫账本的分析层会把字节与 token 加两遍，
	// 且账上没有一格说得出「这两套是同一发」。所以这一格由门口拦，不靠读的人去重。
	if l.alreadyRecorded(session, step, kind) {
		return nil, fmt.Errorf("kernel: %s 第 %d 发（session=%s）已经记过了 —— 同一发只许记一次，"+
			"记两遍会让按键名扫账本的分析层把字节与 token 加两遍", kind, step, session)
	}
	if len(cells) == 0 {
		return nil, fmt.Errorf("kernel: %s 第 %d 发一格都没有，没什么可记的", kind, step)
	}
	out := make([]Record, 0, len(cells))
	for _, c := range cells {
		basis, claim, err := basisOf(c)
		if err != nil {
			return nil, fmt.Errorf("kernel: %s 第 %d 发的 %s 那一格进不了账：%v", kind, step, c.Key, err)
		}
		payload, err := json.Marshal(c.Value)
		if err != nil {
			return nil, fmt.Errorf("kernel: %s 第 %d 发的 %s 那一格序列化不了：%v", kind, step, c.Key, err)
		}
		rec := Record{
			Session: session,
			Actor:   actor,
			Step:    step,
			Kind:    kind + ":" + c.Key,
			Claim:   claim,
			Basis:   basis,
			Source:  c.Source,
			Payload: payload,
		}
		// 用 Append 那**同一份**判据先过一遍。这一步是「plan 放过的，commit 一定收」的唯一保证 ——
		// 少了它，两阶段就只是把半发推迟到 commit 中途，而不是消灭它。
		if err := checkRecord(rec); err != nil {
			return nil, fmt.Errorf("kernel: %s 第 %d 发的 %s 那一格账本不会收：%v", kind, step, c.Key, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// commit 把翻译好的记录逐条落账。
// 走到这一步就不该再失败，而这不是靠运气：plan 已经用 Append 那**同一份**判据
// （checkRecord）逐条过了一遍，所以「plan 收下、Append 拒掉」在结构上到不了。
// 这一层只接受 plan 出来的记录，不接受调用方手搭的 Record ——
// 手搭的那一份绕过了 checkRecord，半发就又有可能了。
func (l *Ledger) commit(recs []Record) (int, error) {
	n := 0
	for _, r := range recs {
		// Append 自己会校验并在拒收时计数，所以这里不重复计。
		if _, err := l.Append(r); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (l *Ledger) alreadyRecorded(session string, step int, kind string) bool {
	prefix := kind + ":"
	for _, e := range l.docs {
		if e.Session == session && e.Step == step && strings.HasPrefix(e.Kind, prefix) {
			return true
		}
	}
	return false
}

// basisOf 把一个格子翻译成账本上那一格的出处与那句人话。
//
// 关键的一处翻译：Measure 用 Absent 那个布尔表示「这一发没有这一格」，
// 而账本用 BasisAbsent 那一档表示同一件事。两边必须对得上 ——
// 少了这一档，缺席就只能落成 0，而 0 会被下一个人读成「量到了，是零」。
//
// 第二处翻译走的是反方向：账本上 `BasisAbsent` 那一档**谁能进**由这里定。
// 三态量（上面那一段）与一张 nil 名单（下面那一段）之外，一个值说不出「没有」，
// 于是它报缺席就是一条无从核起的假话 —— 当场拒。
func basisOf(c Cell) (Basis, string, error) {
	if m, ok := c.Value.(Measure); ok {
		if m.Absent {
			if c.Basis != BasisUnset {
				return BasisUnset, "", fmt.Errorf("值是缺席，格子上却写着出处 %s", c.Basis)
			}
			// String() 已经把「没量到，不是 0」与理由一起说出来了；
			// 被判成不可信的那一格说的是另一句，两句必须不一样。
			return BasisAbsent, m.String(), nil
		}
		if c.Basis == BasisUnset {
			return BasisUnset, "", fmt.Errorf("有值 %s 却没说谁算的", m.String())
		}
		if c.Basis != m.Basis {
			return BasisUnset, "", fmt.Errorf("格子写 %s、值写 %s，两处出处对不上", c.Basis, m.Basis)
		}
		return c.Basis, m.String(), nil
	}
	if c.Basis == BasisUnset {
		return BasisUnset, "", fmt.Errorf("这一格没带出处（值 %s）", claimOf(c.Value))
	}
	if c.Basis == BasisAbsent {
		// 三态量之外，只有一种值说得出「没有」这两个字：一张 nil 名单。
		// 它必须有自己那句话 —— 落到 `claimOf` 会变成「0 段（裁剪表是空的）」，
		// 那是交了一张空表的人才配说的话，没交的人不该借它。
		if isNilSlice(c.Value) {
			return BasisAbsent, absentListClaim, nil
		}
		return BasisUnset, "", fmt.Errorf("只有三态量和 nil 名单能报缺席，%T 不是 —— 一个非三态的值说「没量到」无从核起", c.Value)
	}
	return c.Basis, claimOf(c.Value), nil
}

// claimOf 是那一格的人话版本，进 Claim；原值整体进 Payload 并被指纹覆盖。
// 两者分工：Claim 给人读、要短；Payload 给第三方重算、要全。
//
// 它**永不交出空字符串**。一个空的 Claim 在账本上与「这一格没写」长得一样，
// 而读的人分不清「值是空串」与「没有值」—— 那正是这个包要消灭的「缺席长成 0」那一族，
// 只是换到了字符串上。真会走到这一支的格子有三个：没发通知时的 notice_why、
// 没故障时的 fail_kind 与 fail_detail。
func claimOf(v any) string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return emptyClaim
		}
		return x
	case bool:
		if x {
			return "是"
		}
		return "否"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []PartKind:
		if len(x) == 0 {
			return "0 段（发送顺序表是空的）"
		}
		names := make([]string, 0, len(x))
		for _, k := range x {
			names = append(names, string(k))
		}
		return strconv.Itoa(len(x)) + " 段：" + strings.Join(names, " ")
	case []PartCount:
		if len(x) == 0 {
			return "0 类"
		}
		parts := make([]string, 0, len(x))
		var sum int64
		for _, p := range x {
			parts = append(parts, string(p.Kind)+"="+strconv.Itoa(p.Parts))
			sum += p.Bytes
		}
		return strconv.Itoa(len(x)) + " 类共 " + strconv.FormatInt(sum, 10) + " 字节：" + strings.Join(parts, " ")
	case []Cut:
		if len(x) == 0 {
			return "0 段（裁剪表是空的）"
		}
		parts := make([]string, 0, len(x))
		for _, c := range x {
			parts = append(parts, string(c.Kind)+"/"+c.Ref)
		}
		return strconv.Itoa(len(x)) + " 段：" + strings.Join(parts, " ")
	case []string:
		if len(x) == 0 {
			return emptyClaim
		}
		return strings.Join(x, " ")
	default:
		s := fmt.Sprintf("%v", v)
		if strings.TrimSpace(s) == "" {
			return emptyClaim
		}
		return s
	}
}

// emptyClaim 是「这一格的值确实是空的」那句话。只此一处 ——
// 三个格子都会用到它，各写一个字面量的话，改一处另外两处不会跟着红。
const emptyClaim = "（这一格是空的）"

// absentListClaim 是「这个键压根没交」那句话。它必须与 emptyClaim 是**两句不同的话**：
// 前者是没交（缺席），后者是交了一张空表（实测过的「没有」）。
// 共用一句，这一次改动就等于什么都没改 —— 账上还是分不清。
const absentListClaim = "（没交这一档的名单，不是空表）"

// RecordedCell 是从账本上读回来的一格。
//
// 它与 Cell 刻意是两个类型，不共用：Cell 是「装配出来、还没落账」的那一份
// （带 Field 与活的 Value），RecordedCell 是「已经落账」的那一份
// （只剩那句人话、出处、序列化后的字节与它的指纹）。
// 共用一个类型的话，读回来的一格会带着一个永远为空的 Field，
// 而一个空字段看起来像「这一格没有来源」——那正是这个包要消灭的形状。
type RecordedCell struct {
	Key        string
	Claim      string
	Basis      Basis
	Source     string
	Payload    []byte
	PayloadSHA string
}

// CellsOf 从账本上把某一发、某一侧的格子读回来，按键名排好序。
// 它是 RecordInput/RecordOutput 的逆：写进去几格、读回来几格必须对得上，
// 而对不上时要说得出是哪一格丢了 —— 一个只写不读的账本没法被第三方复核。
//
// 身份由**三个轴**定：session、发号、侧。这与 alreadyRecorded(session, step, kind)
// 那一份是同一个定义 —— 少一个轴，两本不同 session 的同号同侧就会被并成一次回答。
// 这不是设想：把内核挂成 MCP 之后，两个 session 各记一次第 1 发，
// 读侧当场回「共 40 格」，而写侧的门明明知道它们是两发。
func CellsOf(docs []Entry, session string, step int, kind string) []RecordedCell {
	out := []RecordedCell{}
	prefix := kind + ":"
	for _, e := range docs {
		if e.Session != session || e.Step != step || !strings.HasPrefix(e.Kind, prefix) {
			continue
		}
		out = append(out, RecordedCell{
			Key:        strings.TrimPrefix(e.Kind, prefix),
			Claim:      e.Claim,
			Basis:      e.Basis,
			Source:     e.Source,
			Payload:    e.Payload,
			PayloadSHA: e.PayloadH,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// AbsentKeys 报出某一发里「记成缺席」的那些键名。
// 缺席必须能被追问：一个读不出「哪几格没量到」的账本，
// 会让人把「没量到」读成「量到了 0」—— 那一格是 0 还是缺席，后果完全相反。
func AbsentKeys(docs []Entry, session string, step int, kind string) []string {
	out := []string{}
	for _, c := range CellsOf(docs, session, step, kind) {
		if c.Basis == BasisAbsent {
			out = append(out, c.Key)
		}
	}
	return out
}

// Keys 是某一发里落了账的键名（排好序）。用来与 Cells() 的键名表逐格对齐。
func Keys(docs []Entry, session string, step int, kind string) []string {
	cs := CellsOf(docs, session, step, kind)
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Key)
	}
	return out
}
