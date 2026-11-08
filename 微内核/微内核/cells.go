package kernel

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Cell 是「一个键名 + 一个值 + 这个值是谁算的 + 它来自哪个字段」。
//
// Field 那一格不是装饰，它是几条机械判据的支点。它的写法有三种，各自对应一种格子：
//   - `Path`            —— 一个字段（Budget.ContentCeiling）
//   - `Path#tag`        —— 一个字段拆成的几档之一（Cuts#folded）；tag 在同一字段下不许重
//   - `Path.Method()`   —— 一个派生格，值来自方法而不是字段（Says.Clipped()）
//
// 为什么要带 Field：
//   - 死格判据靠它数分母（反射出全部导出字段，逐个问「有没有哪一格替它说话」）。
//   - 撞名判据靠它判「一个键名是不是住了两个量」：两侧都有 call，那是同一个量，不算撞；
//     而内容字节与过网线字节若共用 bytes_out，字段路径不同 ⇒ 当场撞出来。
//     runloop 的 bytes_out 就是那个反例，实测 6327 与 11570。
type Cell struct {
	Key   string
	Field string
	Value any
	Basis Basis
	// Source 只在 Basis 是转述/估算时必填：说不出转述谁的那一格，
	// 和模型自述的那一格在证据上没有区别。这一格由 CheckCells 强制，不靠注释。
	Source string
}

// SourceEndpoint 是「这一格是对端响应里说的」那一个出处。只此一处 ——
// 输出侧有几格都是转述对端的话，各写一个字面量的话，改一处另一处不会跟着红。
const SourceEndpoint = "endpoint-response"

func cell(key, field string, v any, b Basis) Cell {
	return Cell{Key: key, Field: field, Value: v, Basis: b}
}

func relayedCell(key, field string, v any, source string) Cell {
	return Cell{Key: key, Field: field, Value: v, Basis: BasisRelayed, Source: source}
}

func measureCell(key, field string, m Measure) Cell {
	b := m.Basis
	if m.Absent {
		// 缺席那一格没有「谁算的」可言 —— 它说的是「这一发没有这个数」。
		b = BasisUnset
	}
	return Cell{Key: key, Field: field, Value: m, Basis: b, Source: m.Source}
}

// cutCell 是裁剪表拆出的那三档唯一的一档出处判据。
//
// 为什么不能一律写死 `BasisMeasured`：一张 nil 名单说的是「调用方没交这个键」，
// 而一张空表说的是「交过，是空的」—— 后者才是我们查过、确认这一档没发生。
// 一律标实测，等于让「没说话」在账上长成一句实测过的「0 段」，
// 那是缺口 #66，也是本包第 2 条不变量（缺席 ≠ 0 ≠ 忘了填）在名单这一侧的形状。
//
// 只此三格会走到 nil 那一支：`parts`/`part_totals`/`shares` 在 `Check()` 里
// 已经先被空表拒掉了（一段不发、一类不发、份额缺失都进不了账），所以不给它们加这条。
func cutCell(key, field string, cuts []Cut) Cell {
	if cuts == nil {
		return Cell{Key: key, Field: field, Value: cuts, Basis: BasisAbsent}
	}
	return cell(key, field, cuts, BasisMeasured)
}

// isNilSlice 认「一张没交的名单」。只有 Cell 的出处已经是 BasisAbsent 时才需要它：
// 那一档的入场券是「值本身说得出『没有』」，三态量之外只有 nil 名单说得出。
func isNilSlice(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Slice && rv.IsNil()
}

// Cells 交出输入侧的全部格子。顺序固定：顺序不固定的话，
// 两份账逐行比对会因为遍历 map 的随机顺序而「对不上」，而那不是缺陷。
func (m InputMeta) Cells() []Cell {
	out := []Cell{
		cell("call", "Call", m.Call, BasisMeasured),
		// parts 是**按发送顺序**的段名表（含重复），与 runloop 账上那个 parts 是同一个量；
		// 按类聚合的那一份换了名字（part_totals），因为它是另一个量 ——
		// 迁移期两个不同的量共用一个键名，就是 bytes_out 那个缺陷的翻版。
		cell("parts", "Order", m.Order, BasisMeasured),
		cell("part_totals", "Parts", m.Parts, BasisMeasured),
		cell("content_bytes", "ContentBytes", m.ContentBytes, BasisMeasured),
		// 键名刻意不叫 tokens_est：那一格的口径可以是估算**或**转述（对端回了 usage 时），
		// 把 est 写进键名等于让键名在装了一个转述值的时候撒谎。口径住在 Basis 那一格里。
		measureCell("tokens", "Tokens", m.Tokens),
		cell("call_bytes_ceiling", "Budget.CallBytesCeiling", m.Budget.CallBytesCeiling, BasisMeasured),
		cell("transport_slack_pct", "Budget.TransportSlackPct", m.Budget.TransportSlackPct, BasisMeasured),
		cell("content_ceiling", "Budget.ContentCeiling", m.Budget.ContentCeiling, BasisMeasured),
		cell("effective_ceiling", "Budget.EffectiveCeiling", m.Budget.EffectiveCeiling, BasisMeasured),
		cell("clamp_rounds", "Budget.ClampRounds", m.Budget.ClampRounds, BasisMeasured),
		cell("leftover_pct", "Budget.LeftoverPct", m.Budget.LeftoverPct, BasisMeasured),
		measureCell("fixed_overhead", "Budget.FixedOverhead", m.Budget.FixedOverhead),
		cell("shares", "Budget.Shares", SortedKinds(m.Budget.Shares), BasisMeasured),
		cell("notice_sent", "Notice.Sent", m.Notice.Sent, BasisMeasured),
		cell("notice_trimmed", "Notice.Trimmed", m.Notice.Trimmed, BasisMeasured),
		cell("notice_why", "Notice.Why", m.Notice.Why, BasisMeasured),
		cell("notice_text", "Notice.Text", m.Notice.Text, BasisMeasured),
	}
	// 三类挤压各一个键，键名与分档全部从 CutLists 那**一份**定义读出来。
	// 刻意不给「一共挤了几段」的总数：折过的还留着指纹能核回原文，
	// 整段没进的模型彻底看不见，一个总数会让两者互相抵销。
	for _, l := range CutLists {
		out = append(out, cutCell(l.Key, "Cuts#"+l.Key, m.CutList(l.Key)))
	}
	return out
}

// Cells 交出输出侧的全部格子。
func (m OutputMeta) Cells() []Cell {
	return []Cell{
		cell("call", "Call", m.Call, BasisMeasured),
		cell("streaming", "Streaming", m.Streaming, BasisMeasured),
		// 收尾原因那个**字符串**是对端说的（转述）；「它到底说没说」是我们观察到的（实测）。
		// 两格分开，因为没说不等于说了个空串。
		relayedCell("finish", "Finish.Reason", m.Finish.Reason, SourceEndpoint),
		cell("finish_said", "Finish.Said", m.Finish.Said, BasisMeasured),
		// 端点自报的模型 id 只能是自述：它说什么就是什么，我们没有第二条路去核。
		cell("endpoint_model", "Model", m.Model, BasisModelSays),
		cell("endpoint_model_said", "ModelSaid", m.ModelSaid, BasisMeasured),
		// 往来格式：对端在响应体顶层自己写的那个 `object`（实测 chat.completion /
		// chat.completion.chunk）。它是**转述**不是自述：我们不选它、也改不了它，
		// 但它也不是模型说的 —— 是端点说的，所以跟 `finish` 同档，不是 `endpoint_model` 那一档。
		relayedCell("wire_format", "WireFormat", m.WireFormat, SourceEndpoint),
		cell("wire_format_said", "WireFormatSaid", m.WireFormatSaid, BasisMeasured),
		cell("model_says_bytes", "Says.Bytes", m.Says.Bytes, BasisMeasured),
		cell("model_says_kept", "Says.Kept", m.Says.Kept, BasisMeasured),
		cell("model_says_clipped", "Says.Clipped()", m.Says.Clipped(), BasisMeasured),
		cell("model_says_sha256", "Says.SHA256", m.Says.SHA256, BasisMeasured),
		measureCell("usage_in", "UsageIn", m.UsageIn),
		measureCell("usage_out", "UsageOut", m.UsageOut),
		measureCell("wire_bytes_out", "WireBytesOut", m.WireBytesOut),
		measureCell("wire_bytes_in", "WireBytesIn", m.WireBytesIn),
		measureCell("chunks", "Chunks", m.Chunks),
		measureCell("first_byte_ms", "FirstByteMs", m.FirstByteMs),
		measureCell("max_gap_ms", "MaxGapMs", m.MaxGapMs),
		cell("tool_calls", "ToolCalls", m.ToolCalls, BasisMeasured),
		cell("malformed_actions", "Malformed", m.Malformed, BasisMeasured),
		cell("malformed_streak", "MalformedStreak", m.MalformedStreak, BasisMeasured),
		cell("fail_kind", "Failure.Kind", string(m.Failure.Kind), BasisMeasured),
		// 那一格的出处**随故障类别而变**：带 HTTP 状态码时它抄的是对端响应正文里的
		// message/type（转述），其余几类是我们在本地构造的说法（实测）。
		// 一律标成实测的话，一句对端的原话会被读成我们自己量出来的结论。
		failDetailCell(m),
		// 这一格以前写死 BasisMeasured —— 于是每一发没故障的往来都在账上说
		// 「我们量到状态码是 0」（改前在册的账全是那个长相 —— 那个数不许抄进注释里当定值，
		// 由 tasks/audit/status_axis_dist.py 每次从落盘账上重数，读数在 报告/状态码轴-在册分布.txt）。
		// 现在它跟着 Failure.Status 自己那一格走：
		// 没收到对端响应的那几类是缺席，带理由。
		measureCell("fail_status", "Failure.Status", m.Failure.Status),
		measureCell("retry_after_ms", "Failure.RetryAfterMs", m.Failure.RetryAfterMs),
		cell("downgrade", "Failure.Downgrade", m.Failure.Downgrade, BasisMeasured),
		cell("sent", "Sent()", m.Sent().String(), BasisMeasured),
		cell("cost_countable", "CostCountable()", m.CostCountable(), BasisMeasured),
	}
}

// failDetailCell 是「故障说法那一格的出处由什么决定」的唯一一处判据。
// 跟着 **MayCarryStatus 那根轴**走，不再跟着 `Status != 0` 走：
// 收到过对端响应的那几类，Detail 抄的是对端响应正文里的话（转述）；
// 其余是我们在本地构造的说法（实测）。以前跟着 `Status != 0` 走，
// 而那条互核又不许 config/protocol 带码 —— 于是抄了原话也只能标成"我们量的"。
func failDetailCell(m OutputMeta) Cell {
	if m.Failure.Kind.MayCarryStatus() {
		return relayedCell("fail_detail", "Failure.Detail", m.Failure.Detail, SourceEndpoint)
	}
	return cell("fail_detail", "Failure.Detail", m.Failure.Detail, BasisMeasured)
}

// CheckCells 把一份键名表自己核一遍。它是那几条机械判据的**生产入口**：
// 只被测试调用的判据，等于只在跑测试那几秒里存在 ——
// 一个真调用方拿到一份 InputMeta、调 Check() 得到 nil，而它的键名表可能已经撞了名。
// 所以 InputMeta.Check 与 OutputMeta.Check 末尾各调它一次。
func CheckCells(side string, v any, cells []Cell) error {
	if len(cells) == 0 {
		return fmt.Errorf("kernel: %s一格都没交出来 —— 那要么是键名表空了，要么是这条判据在空转", side)
	}
	for _, c := range cells {
		if strings.TrimSpace(c.Key) == "" || strings.TrimSpace(c.Field) == "" {
			return fmt.Errorf("kernel: %s有一格键名或字段路径是空的：%+v", side, c)
		}
		// 转述与估算必须能追到出处。这一条在 Record 那一层也有，但那是**落账时**才拦；
		// 这里在键名表这一层就拦，为的是让「这一格是从哪儿来的」在还没进账本之前就说得出来。
		if needsSource(c.Basis) && strings.TrimSpace(c.Source) == "" {
			return fmt.Errorf("kernel: %s的 %s 那一格说是 %s，却没说出处", side, c.Key, c.Basis)
		}
	}
	if got := KeyCollisions(cells); len(got) > 0 {
		return fmt.Errorf("kernel: %s有 %d 个键名住了两个量：%s", side, len(got), strings.Join(got, "；"))
	}
	if got := DuplicateFieldTags(cells); len(got) > 0 {
		return fmt.Errorf("kernel: %s有 %d 处同一字段拆出的档重了名：%s", side, len(got), strings.Join(got, "；"))
	}
	if got := BasisMismatch(cells); len(got) > 0 {
		return fmt.Errorf("kernel: %s有 %d 格的两处出处对不上：%s", side, len(got), strings.Join(got, "；"))
	}
	if got := DeadFields(v, cells); len(got) > 0 {
		return fmt.Errorf("kernel: %s %d 个导出字段里有 %d 个没有任何一格替它说话（死格）：%s",
			side, len(FieldPaths(v)), len(got), strings.Join(got, " "))
	}
	if got := OrphanCells(v, cells); len(got) > 0 {
		return fmt.Errorf("kernel: %s有 %d 格指着不存在的字段（幽灵键）：%s", side, len(got), strings.Join(got, " "))
	}
	return nil
}

// FieldPaths 用反射数出一个类型全部导出的叶子字段路径。
// 结构体展开到底（Budget/Says/Finish/Failure/Notice），切片与 map 算一片叶子，
// Measure 也算一片叶子 —— 它自己就是「一个数 + 谁算的 + 有没有」，再拆就把两根轴拆散了。
// 传 nil 返回空表而不是崩：一份空账本该被报成「一格都没数出来」，不该让检查以崩溃收场
// （崩不算红，也不算绿）。
func FieldPaths(v any) []string {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	return fieldPaths(rv.Type(), "")
}

func fieldPaths(t reflect.Type, prefix string) []string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == reflect.TypeOf(Measure{}) {
		if prefix == "" {
			return nil
		}
		return []string{prefix}
	}
	out := []string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // 非导出字段不算：外面读不到，也就无所谓死不死
			continue
		}
		name := f.Name
		if prefix != "" {
			name = prefix + "." + f.Name
		}
		out = append(out, fieldPaths(f.Type, name)...)
	}
	return out
}

// DeadFields 报出「有字段、却没有任何一格替它**落账**」的那些。
//
// 这条判据量的是**写侧**，请照它量的那样读它：它抓的是「结构体上加了一格、
// 却忘了把它交进键名表」，不会抓「交了账但没人读」。后者是 runloop 那二十多格死格的真实形状
// （格在、读者没有），而读者住在还没写的框架里，内核自己核不到 ——
// 那一半由 Readers 那一份声明表承担，见 Unread。
func DeadFields(v any, cells []Cell) []string {
	said := map[string]bool{}
	for _, c := range cells {
		said[baseField(c.Field)] = true
	}
	out := []string{}
	for _, p := range FieldPaths(v) {
		if !said[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// OrphanCells 报出「键名指着的是一个不存在的字段或方法」的那些。
// 它是 DeadFields 的反方向：少一格是死字段，多一格是幽灵键 ——
// 后者更隐蔽，因为它会一直落账、一直有值，而那个值谁也核不回结构体。
func OrphanCells(v any, cells []Cell) []string {
	out := []string{}
	root := reflect.TypeOf(v)
	for _, c := range cells {
		if resolves(root, c.Field) {
			continue
		}
		out = append(out, c.Key+"→"+c.Field)
	}
	sort.Strings(out)
	return out
}

// baseField 去掉 `#tag` 与 `()` 那两段修饰，留下真字段路径。
func baseField(field string) string {
	if i := strings.IndexByte(field, '#'); i >= 0 {
		field = field[:i]
	}
	return strings.TrimSuffix(field, "()")
}

// resolves 逐段核一条字段路径。中间每一段都必须是导出的结构体字段；
// 最后一段带 `()` 时必须是挂在那个类型上的方法（派生格），不带时必须是字段。
// 早先这里允许最后一段是**任意**方法名，于是 Field 写成 String 或 Check 也算合法 ——
// 那等于给幽灵键开了一扇比需要更大的门。
func resolves(root reflect.Type, path string) bool {
	if root == nil || path == "" {
		return false
	}
	wantMethod := false
	if i := strings.IndexByte(path, '#'); i >= 0 {
		path = path[:i]
	}
	if strings.HasSuffix(path, "()") {
		wantMethod = true
		path = strings.TrimSuffix(path, "()")
	}
	segs := strings.Split(path, ".")
	t := root
	for i, s := range segs {
		last := i == len(segs)-1
		if last && wantMethod {
			return hasMethodName(t, s)
		}
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return false
		}
		f, ok := t.FieldByName(s)
		if !ok || f.PkgPath != "" {
			return false
		}
		t = f.Type
	}
	return !wantMethod
}

func hasMethodName(t reflect.Type, name string) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if _, ok := t.MethodByName(name); ok {
		return true
	}
	if t.Kind() != reflect.Pointer {
		if _, ok := reflect.PointerTo(t).MethodByName(name); ok {
			return true
		}
	}
	return false
}

// KeyCollisions 报出「一个键名住了两个量」的那些。
// 判据是字段路径：两侧都有 call，那是同一个量，不算撞；
// 而内容字节与过网线字节若共用 bytes_out，字段路径不同 ⇒ 当场撞出来。
func KeyCollisions(cells []Cell) []string {
	byKey := map[string]map[string]bool{}
	for _, c := range cells {
		if byKey[c.Key] == nil {
			byKey[c.Key] = map[string]bool{}
		}
		byKey[c.Key][baseField(c.Field)] = true
	}
	out := []string{}
	for k, fields := range byKey {
		if len(fields) > 1 {
			out = append(out, k+"（来自 "+strings.Join(sorted(fields), " 与 ")+"）")
		}
	}
	sort.Strings(out)
	return out
}

// DuplicateFieldTags 报出「同一个字段拆成几档时，档名重了」的那些。
// 三格共用 Field:"Cuts" 的时候，砍掉其中任意两格，「Cuts 有人替它说话」仍然成立 ——
// 死格判据分不出砍掉的是哪一档。带上 #tag 之后每一档都能被单独核到，
// 而这一条盯的是 tag 本身不许重名（重了就又变回两格共用一个身份）。
func DuplicateFieldTags(cells []Cell) []string {
	counts := map[string]int{}
	for _, c := range cells {
		if strings.Contains(c.Field, "#") {
			counts[c.Field]++
		}
	}
	out := []string{}
	for f, n := range counts {
		if n > 1 {
			out = append(out, fmt.Sprintf("%s 出现 %d 次", f, n))
		}
	}
	sort.Strings(out)
	return out
}

// BasisMismatch 报出「格子上写的出处与值自己带的出处不一致」的那些。
// Measure 型的格子有两处能写出处与来源（Cell 上一份、Measure 里一份），
// 两处就有漂的可能，所以钉一条：它们必须一字不差。
func BasisMismatch(cells []Cell) []string {
	out := []string{}
	for _, c := range cells {
		m, ok := c.Value.(Measure)
		if !ok {
			continue
		}
		want := m.Basis
		if m.Absent {
			want = BasisUnset
		}
		if c.Basis != want {
			out = append(out, fmt.Sprintf("%s（格子写 %s、值写 %s）", c.Key, c.Basis, want))
			continue
		}
		if c.Source != m.Source {
			out = append(out, fmt.Sprintf("%s（格子写的出处是 %q、值写的是 %q）", c.Key, c.Source, m.Source))
		}
	}
	sort.Strings(out)
	return out
}

// Readers 是「谁读了哪一格」的声明表。
//
// 为什么需要它：内核能机械核的是写侧（每个字段都有一格替它落账）。
// 而 runloop 那二十多格死格的真实形状是**格在、读者没有** —— 那一半在内核里核不到，
// 因为读者住在还没写的框架里。所以内核把它做成一份必须由框架填的声明：
// 框架每读一格就声明一次，内核报出「N 格里 M 格没有任何声明者读」。
//
// 声明一个不存在的键名会被拒：一个拼错的声明会让一格死格看起来是活的，
// 而静默漏报比报错危险。
type Readers struct {
	byKey map[string][]string
}

func NewReaders() *Readers { return &Readers{byKey: map[string][]string{}} }

// Declare 记一次「reader 读了 key 这一格」。known 是这一侧合法的键名集合，
// 由调用方从 Cells() 里取出来递进来 —— 内核不猜键名表长什么样。
func (r *Readers) Declare(key, reader string, known []Cell) error {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(reader) == "" {
		return fmt.Errorf("kernel: 声明里键名与读者都不能空（得到 %q / %q）", key, reader)
	}
	found := false
	for _, c := range known {
		if c.Key == key {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("kernel: 声明了一个不在键名表里的键 %q —— 拼错的声明会让一格死格看起来是活的", key)
	}
	for _, old := range r.byKey[key] {
		if old == reader {
			return fmt.Errorf("kernel: %s 已经被 %s 声明读过一次 —— 同一个读者声明两遍会把分母数大", key, reader)
		}
	}
	r.byKey[key] = append(r.byKey[key], reader)
	return nil
}

// Unread 报出没有任何声明者读过的格子，带分母。
func (r *Readers) Unread(cells []Cell) []string {
	out := []string{}
	for _, c := range cells {
		if len(r.byKey[c.Key]) == 0 {
			out = append(out, c.Key)
		}
	}
	sort.Strings(out)
	return out
}

// ReadersOf 报出某一格被谁读过。没人读过时返回空表，而不是一个空字符串 ——
// 「没人读」与「读的人叫空名字」必须分得开。
func (r *Readers) ReadersOf(key string) []string {
	out := append([]string{}, r.byKey[key]...)
	sort.Strings(out)
	return out
}

// Report 是那一句带分母的话：N 格里 M 格没有任何声明者读。
// 分母不许省 —— 一个光秃秃的 M 没法判断是漏了一格还是漏了整侧。
func (r *Readers) Report(cells []Cell) string {
	unread := r.Unread(cells)
	s := fmt.Sprintf("键名表 %d 格，其中 %d 格没有任何声明者读", len(cells), len(unread))
	if len(unread) > 0 {
		s += "：" + strings.Join(unread, " ")
	}
	if len(cells) == 0 {
		s += "（键名表是空的，这个分母没有意义）"
	}
	return s
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
