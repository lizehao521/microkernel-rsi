package kernel

import (
	"strings"
	"testing"
)

// ① 每个导出字段都得有一格替它落账，反过来每一格都得指着一个真字段或真方法。
// runloop 今天有二十多格写了零读者（首帧等待、帧间等待、收尾原因、分帧数……），
// 而它们一路绿 —— 因为没有任何东西数过分母。
//
// 这一条量的是**写侧**（有没有落账）；「落了账但没人读」那一半由 ⑦ 的声明表承担。
// 两半合起来才是 runloop 那个缺陷的完整形状，缺任一半都会给它开一条活路。
func TestEveryExportedFieldHasACell(t *testing.T) {
	in, out := validInput(t), validOutput(t)
	sides := []struct {
		name   string
		v      any
		cells  []Cell
		fields []string
	}{
		{"输入侧", in, in.Cells(), FieldPaths(in)},
		{"输出侧", out, out.Cells(), FieldPaths(out)},
	}
	for _, s := range sides {
		if len(s.fields) == 0 {
			t.Errorf("%s一个字段都没数出来 —— 反射那一路是空的，这条判据在空转", s.name)
		}
		dead := DeadFields(s.v, s.cells)
		ghost := OrphanCells(s.v, s.cells)
		t.Logf("%s：%d 个导出字段、%d 格；没人替它落账的 %d 个 %v；指着不存在字段的 %d 格 %v",
			s.name, len(s.fields), len(s.cells), len(dead), dead, len(ghost), ghost)
		if len(dead) > 0 {
			t.Errorf("%s有 %d 个字段没有任何一格替它落账（死格）：%v", s.name, len(dead), dead)
		}
		if len(ghost) > 0 {
			t.Errorf("%s有 %d 格指着不存在的字段（幽灵键，它会一直落账、一直有值，而那个值谁也核不回结构体）：%v",
				s.name, len(ghost), ghost)
		}
		if err := CheckCells(s.name, s.v, s.cells); err != nil {
			t.Errorf("%s的键名表自己没过：%v", s.name, err)
		}
		// 格数不许少于字段数。下限来自字段数本身，不是手抄的一个数。
		if len(s.cells) < len(s.fields) {
			t.Errorf("%s的格数 %d 少于字段数 %d", s.name, len(s.cells), len(s.fields))
		}
	}
}

// ② 上面那条判据自己必须能红。摘掉一格 / 塞进一个幽灵键，都要被当场点名。
func TestDeadFieldHuntActuallyBites(t *testing.T) {
	out := validOutput(t)
	cells := out.Cells()

	// 摘掉「首帧等待」那一格。
	trimmed := make([]Cell, 0, len(cells))
	for _, c := range cells {
		if c.Key != "first_byte_ms" {
			trimmed = append(trimmed, c)
		}
	}
	if len(trimmed) != len(cells)-1 {
		t.Fatalf("摘掉一格之后该正好少一格：%d → %d（键名表里压根没有 first_byte_ms 的话，这一发反证是空的）",
			len(cells), len(trimmed))
	}
	dead := DeadFields(out, trimmed)
	if len(dead) != 1 || dead[0] != "FirstByteMs" {
		t.Errorf("该报出 FirstByteMs 一个死格，得到 %v", dead)
	}
	if err := CheckCells("输出侧", out, trimmed); err == nil {
		t.Errorf("少了一格，CheckCells 竟没报错")
	}

	// 塞进一个指着不存在字段的格。
	ghost := append(append([]Cell{}, cells...), cell("nope", "NoSuchField", 1, BasisMeasured))
	if got := OrphanCells(out, ghost); len(got) != 1 || !strings.Contains(got[0], "NoSuchField") {
		t.Errorf("该报出那一个幽灵键，得到 %v", got)
	}
	if err := CheckCells("输出侧", out, ghost); err == nil {
		t.Errorf("多了一个幽灵键，CheckCells 竟没报错")
	}
	// 派生格（来自方法而不是字段）不该被误报成幽灵。
	if got := OrphanCells(out, cells); len(got) != 0 {
		t.Errorf("真的键名表里被报出幽灵键 %v", got)
	}
}

// ③ 派生格必须写成 `Path.Method()`。不带那对括号的话，一个什么都不说的方法名
// （String、Check）也会被当成合法来源 —— 那等于给幽灵键开了一扇比需要更大的门。
func TestDerivedCellMustBeWrittenAsAMethod(t *testing.T) {
	out := validOutput(t)
	// 带括号：方法真存在 ⇒ 不算幽灵。
	if got := OrphanCells(out, []Cell{cell("clipped", "Says.Clipped()", true, BasisMeasured)}); len(got) != 0 {
		t.Errorf("Says.Clipped() 是真方法，不该被报成幽灵：%v", got)
	}
	if got := OrphanCells(out, []Cell{cell("sent", "Sent()", SentSomething, BasisMeasured)}); len(got) != 0 {
		t.Errorf("Sent() 是真方法，不该被报成幽灵：%v", got)
	}
	// 不带括号：它就不是一个字段路径，必须被报出来。
	for _, bad := range []string{"Says.Clipped", "Sent", "Check", "String"} {
		if got := OrphanCells(out, []Cell{cell("x", bad, 1, BasisMeasured)}); len(got) != 1 {
			t.Errorf("Field 写成 %q（没有括号）该被报成幽灵键，得到 %v", bad, got)
		}
	}
	// 方法不存在时，带了括号也一样要报。
	if got := OrphanCells(out, []Cell{cell("x", "Says.Nope()", 1, BasisMeasured)}); len(got) != 1 {
		t.Errorf("Says 上没有 Nope 这个方法，该被报出来，得到 %v", got)
	}
	// 中间那一段必须是真字段。
	if got := OrphanCells(out, []Cell{cell("x", "Nope.Clipped()", 1, BasisMeasured)}); len(got) != 1 {
		t.Errorf("OutputMeta 上没有 Nope 这个字段，该被报出来，得到 %v", got)
	}
}

// ④ 一个字段拆成几档时，档名不许重 —— 三格共用 Field:"Cuts" 的时候，
// 砍掉其中任意两格，「Cuts 有人替它说话」仍然成立，死格判据分不出砍掉的是哪一档。
func TestFieldTagsMustBeDistinct(t *testing.T) {
	in := validInput(t)
	cells := in.Cells()
	var tagged int
	for _, c := range cells {
		if strings.HasPrefix(c.Field, "Cuts#") {
			tagged++
		}
	}
	// 下限从那张分档表自己数出来，不抄一个常量。
	if tagged != len(CutLists) {
		t.Errorf("Cuts 该拆成 %d 档（分档表里有 %d 档），键名表里只有 %d 格带 #tag",
			len(CutLists), len(CutLists), tagged)
	}
	if got := DuplicateFieldTags(cells); len(got) != 0 {
		t.Errorf("真的键名表里档名重了：%v", got)
	}
	// 反证：把两档写成同一个名字，就该当场报出来。
	bad := []Cell{
		cell("folded", "Cuts#folded", []Cut{}, BasisMeasured),
		cell("clipped", "Cuts#folded", []Cut{}, BasisMeasured),
	}
	got := DuplicateFieldTags(bad)
	if len(got) != 1 || !strings.Contains(got[0], "Cuts#folded") {
		t.Errorf("两档共用 Cuts#folded 该被报出来，得到 %v", got)
	}
	if err := CheckCells("输入侧", in, append(append([]Cell{}, cells...), bad...)); err == nil {
		t.Errorf("档名重了，CheckCells 竟没报错")
	}
	// 摘掉一档：那一档就没人替它说话了，而死格判据要能分出来。
	trimmed := make([]Cell, 0, len(cells))
	for _, c := range cells {
		if c.Field != "Cuts#clipped" {
			trimmed = append(trimmed, c)
		}
	}
	if len(trimmed) != len(cells)-1 {
		t.Fatalf("摘掉一档之后该正好少一格：%d → %d（这一发反证会是空的）", len(cells), len(trimmed))
	}
	// 说明这一格：摘掉一档之后 DeadFields 仍然不报（Cuts 还有两格替它说话），
	// 所以「每一档都要在」这件事由 ④ 上面那条档名计数与 TestThreeSqueezeListsStaySeparate 一起守，
	// 不由 DeadFields 守。把这句话写下来，免得下一个人以为这里有一道其实没有的守卫。
	if got := DeadFields(in, trimmed); len(got) != 0 {
		t.Errorf("摘掉一档不该被报成死格（Cuts 还有别的档替它说话），得到 %v", got)
	}
}

// ⑤ Measure 型的格子有两处能写出处（格子上一处、值里一处），两处必须一字不差。
func TestBasisIsWrittenOncePerCell(t *testing.T) {
	in, out := validInput(t), validOutput(t)
	for _, s := range []struct {
		name  string
		cells []Cell
	}{{"输入侧", in.Cells()}, {"输出侧", out.Cells()}} {
		if got := BasisMismatch(s.cells); len(got) > 0 {
			t.Errorf("%s有 %d 格的两处出处对不上：%v", s.name, len(got), got)
		}
	}
	bad := []Cell{{Key: "usage_in", Field: "UsageIn", Value: RelayedOf(1587, "endpoint"), Basis: BasisMeasured}}
	if got := BasisMismatch(bad); len(got) != 1 || !strings.Contains(got[0], "usage_in") {
		t.Errorf("把转述写成实测该被当场抓到，得到 %v", got)
	}
	if err := CheckCells("输出侧", out, append(append([]Cell{}, out.Cells()...), bad...)); err == nil {
		t.Errorf("两处出处对不上，CheckCells 竟没报错")
	}
	// 缺席那一格的出处该是 unset（它说的是「这一发没有这个数」），写成 measured 也是漂。
	bad2 := []Cell{{Key: "chunks", Field: "Chunks", Value: AbsentBecause("非流式"), Basis: BasisMeasured}}
	if got := BasisMismatch(bad2); len(got) != 1 {
		t.Errorf("缺席那一格写着 measured 该被抓到，得到 %v", got)
	}
	// 不是 Measure 的格子不参与这条判据（它只有一处能写出处）。
	if got := BasisMismatch([]Cell{cell("call", "Call", 1, BasisMeasured)}); len(got) != 0 {
		t.Errorf("非 Measure 的格子被误报了：%v", got)
	}
}

// ⑥ 键序必须稳定，且来自 map 的那一格必须先排过序 ——
// 否则两份账逐行比对会因为遍历顺序而「对不上」，而那不是缺陷。
//
// 这一条连**值**一起比：只比键名的话，键名来自切片字面量、编译期就定死了，
// 跑六十遍必然相同，而真正会随 map 遍历顺序漂的是份额那一格的值。
func TestCellOrderIsStable(t *testing.T) {
	in, out := validInput(t), validOutput(t)
	render := func() string {
		parts := []string{}
		for _, c := range append(append([]Cell{}, in.Cells()...), out.Cells()...) {
			parts = append(parts, c.Key+"="+renderValue(c.Value))
		}
		return strings.Join(parts, "|")
	}
	first := render()
	if !strings.Contains(first, "shares=") {
		t.Fatalf("键名表里没有 shares 那一格，这条判据在空转：%s", first)
	}
	for i := 0; i < 80; i++ {
		if got := render(); got != first {
			t.Fatalf("第 %d 次渲染与第一次不同 —— map 的遍历顺序漏进了键名表", i+1)
		}
	}
	// 份额那一格的值来自 map，必须先排过序，不然它自己就是随机的。
	found := false
	for _, c := range in.Cells() {
		if c.Key != "shares" {
			continue
		}
		found = true
		vs, ok := c.Value.([]string)
		if !ok {
			t.Fatalf("shares 那一格的值是 %T，不是排好序的字符串表", c.Value)
		}
		if len(vs) != len(fullShares()) {
			t.Errorf("shares 该有 %d 条，得到 %d 条", len(fullShares()), len(vs))
		}
		for i := 1; i < len(vs); i++ {
			if vs[i-1] > vs[i] {
				t.Fatalf("shares 没排过序：%v", vs)
			}
		}
	}
	if !found {
		t.Fatalf("shares 那一格不在键名表里，上面那段排序断言一次都没执行 —— 这一发反证是空的")
	}
}

// renderValue 把值渲染成一个可比的串。它只服务于上面那条稳定性判据。
func renderValue(v any) string {
	switch x := v.(type) {
	case []string:
		return strings.Join(x, ",")
	case Measure:
		return x.String()
	case []PartKind:
		names := make([]string, 0, len(x))
		for _, k := range x {
			names = append(names, string(k))
		}
		return strings.Join(names, ",")
	case []Cut:
		parts := make([]string, 0, len(x))
		for _, c := range x {
			parts = append(parts, string(c.Kind)+"/"+c.Ref+"/"+string(c.Squeeze))
		}
		return strings.Join(parts, ",")
	case []PartCount:
		parts := make([]string, 0, len(x))
		for _, p := range x {
			parts = append(parts, string(p.Kind)+":")
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}

// ⑦ 「落了账但没人读」那一半。
//
// 这一条是冲着 runloop 那个缺陷的真实形状去的：那二十多格死格**都有写入者**
// （finish / chunks / first_byte_ms 都在账上），缺的是读者。
// 只核写侧的判据对它们一声不响 —— 把它们拿去跑 runloop 的形状，全会被报成「活的」。
// 读者住在还没写的框架里，内核自己核不到，所以做成一份必须由框架填的声明：
// 每读一格声明一次，内核报出「N 格里 M 格没有任何声明者读」。
func TestUnreadCellsAreCountedAgainstADenominator(t *testing.T) {
	out := validOutput(t)
	cells := out.Cells()
	r := NewReaders()
	// 一个拼错的声明必须被拒：它会让一格死格看起来是活的，而静默漏报比报错危险。
	if err := r.Declare("frist_byte_ms", "resume", cells); err == nil {
		t.Errorf("声明了一个不在键名表里的键名竟被收了")
	} else if !strings.Contains(err.Error(), "拼错") {
		t.Errorf("那句话没说出真凶：%v", err)
	}
	if err := r.Declare("", "resume", cells); err == nil {
		t.Errorf("空键名的声明竟被收了")
	}
	if err := r.Declare("first_byte_ms", "  ", cells); err == nil {
		t.Errorf("空读者的声明竟被收了")
	}
	// 一个读者声明两遍会把分母数大。
	if err := r.Declare("first_byte_ms", "resume", cells); err != nil {
		t.Fatal(err)
	}
	if err := r.Declare("first_byte_ms", "resume", cells); err == nil {
		t.Errorf("同一个读者声明两遍竟被收了 —— 那会把分母数大")
	}
	if err := r.Declare("first_byte_ms", "retrospect", cells); err != nil {
		t.Errorf("换一个读者声明同一格该收：%v", err)
	}
	if got := r.ReadersOf("first_byte_ms"); len(got) != 2 {
		t.Errorf("那一格该有 2 个声明者，得到 %v", got)
	}
	// 没人读的那一格必须在账上看得见。
	if got := r.ReadersOf("max_gap_ms"); len(got) != 0 {
		t.Errorf("没人声明过的格子该返回空表，得到 %v", got)
	}
	unread := r.Unread(cells)
	if len(unread) != len(cells)-1 {
		t.Errorf("声明了 1 格、键名表有 %d 格，没人读的该是 %d 格，得到 %d", len(cells), len(cells)-1, len(unread))
	}
	rep := r.Report(cells)
	t.Logf("读者声明表：%s", rep)
	for _, want := range []string{"max_gap_ms", "finish"} {
		if !strings.Contains(rep, want) {
			t.Errorf("那句话里该点名 %s：%s", want, rep)
		}
	}
	// 分母必须在场：一个光秃秃的 M 没法判断是漏了一格还是漏了整侧。
	if !strings.Contains(rep, "键名表 ") || !strings.Contains(rep, "格") {
		t.Errorf("那句话没带分母：%s", rep)
	}
	// 全都声明过之后，差集必须是空的。
	all := NewReaders()
	for _, c := range cells {
		if err := all.Declare(c.Key, "framework", cells); err != nil {
			t.Fatal(err)
		}
	}
	if got := all.Unread(cells); len(got) != 0 {
		t.Errorf("全都声明过之后还有人没人读的格子：%v", got)
	}
	if !strings.Contains(all.Report(cells), "0 格") {
		t.Errorf("全读过之后那句话该报 0 格没人读：%s", all.Report(cells))
	}
	// 空键名表：分母没有意义，那句话要自己说出来，不许报成「0 格没人读，很好」。
	if got := NewReaders().Report(nil); !strings.Contains(got, "没有意义") {
		t.Errorf("空键名表的那句话该说自己没有意义：%s", got)
	}
}

// ⑧ 格数与字段数一起报出来。这一条不断言某个具体的数 —— 数会随字段增减而漂，
// 漂了的手抄数比没有数更有害；这里只断言两个数之间的关系。
func TestCellCountCarriesADenominator(t *testing.T) {
	in, out := validInput(t), validOutput(t)
	inFields, outFields := FieldPaths(in), FieldPaths(out)
	inCells, outCells := in.Cells(), out.Cells()
	all := append(append([]Cell{}, inCells...), outCells...)
	t.Logf("输入侧 %d 个字段 / %d 格；输出侧 %d 个字段 / %d 格；合计 %d 格、%d 个字段",
		len(inFields), len(inCells), len(outFields), len(outCells), len(all), len(inFields)+len(outFields))
	if len(inCells) < len(inFields) || len(outCells) < len(outFields) {
		t.Errorf("有一侧的格数少于字段数，那就是有字段没人替它落账")
	}
	for _, c := range all {
		if strings.TrimSpace(c.Key) == "" || strings.TrimSpace(c.Field) == "" {
			t.Errorf("有一格键名或字段路径是空的：%+v", c)
		}
	}
	// CheckCells 对一份空表必须报错，而不是安静地过 ——
	// 一个什么都没核的检查说「没问题」，那是最坏的一种绿。
	// 这里要断言**是哪一条规则**报的红：只断 err != nil 的话，DeadFields 会替它挡着
	// （18 个字段全成死格），把这条守卫整条删掉测试照样绿（探针 m44 抓到的就是这个）。
	if err := CheckCells("空的一侧", in, nil); err == nil {
		t.Errorf("空键名表竟被 CheckCells 收了")
	} else if !strings.Contains(err.Error(), "一格都没交出来") {
		t.Errorf("报的不是「键名表是空的」那一条，是别的规则替它挡着了：%v", err)
	}
	// 而这条守卫唯一无可替代的场合，是一个压根没有导出字段的类型：
	// 那时 DeadFields 无话可说，只剩它。
	if err := CheckCells("空的", struct{}{}, nil); err == nil {
		t.Errorf("空键名表 + 没有导出字段的类型，竟被收了")
	} else if !strings.Contains(err.Error(), "一格都没交出来") {
		t.Errorf("该由那条守卫报红，得到：%v", err)
	}
}

// ⑨ 反射那一路的两条边界：传 nil 不许崩，非导出字段不许被数进分母。
func TestFieldPathsBoundaries(t *testing.T) {
	if got := FieldPaths(nil); got != nil {
		t.Errorf("传 nil 该返回空表，得到 %v", got)
	}
	if got := DeadFields(nil, nil); len(got) != 0 {
		t.Errorf("传 nil 不该报出死格，得到 %v", got)
	}
	if got := OrphanCells(nil, []Cell{cell("k", "F", 1, BasisMeasured)}); len(got) != 1 {
		t.Errorf("传 nil 时那一格该被报成幽灵键（它核不回任何结构体），得到 %v", got)
	}
	// Total 有六个非导出字段；它们外面读不到，也就无所谓死不死，不该进分母。
	if got := FieldPaths(Total{}); len(got) != 0 {
		t.Errorf("非导出字段被数进了分母：%v", got)
	}
	// Measure 是一片叶子：它自己就是「一个数 + 谁算的 + 有没有」，再拆就把两根轴拆散了。
	// 放在根上时它连路径都没有（没有字段名可言），所以返回空表；
	// 嵌在结构体里时才带上路径前缀 —— 下面那一段核的是后者。
	if got := FieldPaths(Measure{}); len(got) != 0 {
		t.Errorf("Measure 放在根上该返回空表，得到 %v", got)
	}
	// 而嵌在结构体里的 Measure 要带上路径前缀。
	if got := FieldPaths(OutputMeta{}); len(got) == 0 {
		t.Fatalf("OutputMeta 一个字段都没数出来 —— 反射那一路是空的")
	} else {
		var hasUsageIn bool
		for _, p := range got {
			if p == "UsageIn" {
				hasUsageIn = true
			}
			if strings.HasPrefix(p, "UsageIn.") {
				t.Errorf("Measure 被展开了（%s）—— 它该是一片叶子", p)
			}
		}
		if !hasUsageIn {
			t.Errorf("UsageIn 没被数进分母：%v", got)
		}
	}
}

// TestFailureCellsFollowTheStatusAxis 钉的是②③落在**格子这一层**：
// `fail_status` 的出处随 `MayCarryStatus()` 那根轴走（不再写死 measured）、
// `fail_detail` 跟着同一根轴、`wire_format` 是转述且指得出是谁算的。
// 为什么非要有这一条：判据测试只核 `Check()` 拒不拒 —— 把 cells.go 里那句
// `measureCell("fail_status"…)` 改回 `cell(…, BasisMeasured)`，**没有任何测试会红**，
// 而账上又会重新长回"每发都报称量到状态码 0"那个形状（改前在册的账就是那样，
// 那个数由 tasks/audit/status_axis_dist.py 从落盘账上重数，不写死在这里）。
func TestFailureCellsFollowTheStatusAxis(t *testing.T) {
	one := func(k FailKind, st Measure, detail string, blankSays bool) map[string]RecordedCell {
		t.Helper()
		out := validOutput(t)
		// 格式那格只有"收到过对端响应"的那几类才填得出来 —— 上面③那条互核就是拦这个的：
		// 本测试第一版给 transport 也填了 `chat.completion`，被内核整发拒收（拒得对，是夹具在说谎）。
		if k.MayCarryStatus() {
			out.WireFormat, out.WireFormatSaid = "chat.completion", true
		} else {
			out.WireFormat, out.WireFormatSaid = "", false
		}
		if blankSays {
			// 故障那一发可以没内容；没故障那一发不行（内核拒"啥也没回却记成正常收尾"）
			out.Says, out.ToolCalls = Says{Bytes: 0, Kept: 0, SHA256: sha("")}, 0
		}
		out.Failure = Failure{Kind: k, Detail: detail, Status: st, RetryAfterMs: AbsentBecause("对端没给")}
		if k == FailBudget {
			// 预算挡下 = 一发都没该发，过网线的字节必须是量到的 0（这条判据早就在，夹具得合规）
			out.WireBytesOut, out.ToolCalls = MeasuredOf(0), 0
		}
		l := NewLedger()
		if _, err := l.RecordCall("s-axis", "a", validInput(t), out); err != nil {
			t.Fatalf("%s 这一发该被收下：%v", k, err)
		}
		return recordedByKey(l.Docs(), "s-axis", out.Call, KindModelOutput)
	}

	// 收到过对端响应的四类：状态码是量到的，说法是转述对端的
	for _, k := range []FailKind{FailHTTP, FailConfig, FailProtocol, FailMalformedAction} {
		c := one(k, MeasuredOf(401), "端点原话", true)
		if c["fail_status"].Basis != BasisMeasured {
			t.Errorf("%s：fail_status 该是实测，得到 %v", k, c["fail_status"].Basis)
		}
		if c["fail_detail"].Basis != BasisRelayed || c["fail_detail"].Source != SourceEndpoint {
			t.Errorf("%s：fail_detail 该标转述且指得出对端，得到 basis=%v source=%q",
				k, c["fail_detail"].Basis, c["fail_detail"].Source)
		}
		if c["wire_format"].Basis != BasisRelayed || c["wire_format"].Source != SourceEndpoint {
			t.Errorf("wire_format 该是转述且 Source 指得出是谁算的，得到 basis=%v source=%q",
				c["wire_format"].Basis, c["wire_format"].Source)
		}
		if c["wire_format"].Claim != "chat.completion" {
			t.Errorf("wire_format 装的对端原字被改了：%q", c["wire_format"].Claim)
		}
		// 「知不知道对端自报了格式」是我们自己数出来的，不是对端说的：
		// 出处不许指对端 —— 指对端就成了「对端告诉我们我们知道」。
		if c["wire_format_said"].Basis != BasisMeasured || c["wire_format_said"].Source == SourceEndpoint {
			t.Errorf("%s：wire_format_said 该是实测且不指对端，得到 basis=%v source=%q",
				k, c["wire_format_said"].Basis, c["wire_format_said"].Source)
		}
	}
	// 没收到对端响应的两类，以及没故障那一发：状态码只能是**缺席**，说法是我们自己的
	for _, tc := range []struct {
		k FailKind
		d string
	}{
		{FailTransport, "连接被对端关掉"}, {FailBudget, "额度到顶"}, {FailNone, ""},
	} {
		c := one(tc.k, AbsentBecause("这一发没收到对端响应"), tc.d, tc.k != FailNone)
		if c["fail_status"].Basis != BasisAbsent {
			t.Errorf("%s：fail_status 该是缺席（旧代码在这里写死 measured 0），得到 %v / claim=%q",
				tc.k, c["fail_status"].Basis, c["fail_status"].Claim)
		}
		if c["fail_detail"].Basis != BasisMeasured {
			t.Errorf("%s：fail_detail 没收到对端话，该是实测的说法，得到 %v", tc.k, c["fail_detail"].Basis)
		}
	}
}
