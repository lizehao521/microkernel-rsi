package kernel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ① 本包自己：只依赖标准库。
// 数字由这次检查自己印出来（t.Logf 那一行），文档里不手抄 —— 手抄的数会漂，
// 而漂了的数比没有数更有害。
func TestSelfIsolation(t *testing.T) {
	r := SelfIsolation()
	t.Logf("隔离检查：%s", r.String())
	if r.Files == 0 {
		t.Fatalf("一个源文件都没扫到，这条判据在空转（它自己说的原因：%s）", r.DepsWhy)
	}
	if !r.Isolated() {
		t.Errorf("本包破了隔离：泄漏 %v；测试文件里泄漏 %v；cgo %v；认不出 %v",
			r.Leaks, r.TestLeaks, r.Cgo, r.Odd)
	}
	if r.Imports == 0 {
		t.Errorf("扫到了 %d 个文件却一条 import 都没数出来 —— 解析那一路是空的", r.Files)
	}
	if r.Stdlib+r.Self+len(r.Leaks)+len(r.Cgo)+len(r.Odd) != r.Imports {
		t.Errorf("分母对不上：标准库 %d + 本模块 %d + 泄漏 %d + cgo %d + 认不出 %d ≠ import 总数 %d",
			r.Stdlib, r.Self, len(r.Leaks), len(r.Cgo), len(r.Odd), r.Imports)
	}
	if r.TestFiles == 0 {
		t.Errorf("一个测试文件都没扫到 —— 那就有一条能把依赖藏进去的路没人看着")
	}
	// 交叉核对那一路：跑成了就要求它也是干净的；没跑成就要求它说清为什么。
	// 两种都不许静默 —— 「核不了」被瞒成「合格」是这里最坏的一种绿。
	// 这里走 Checked() 而不是直接读字段：一个没人读的访问器就是死格。
	if r.Checked() {
		if r.Deps == 0 {
			t.Errorf("go list -deps 跑成了却一个包都没列出来")
		}
		if got := r.DepsLeaks(); len(got) != 0 {
			t.Errorf("依赖闭包里有 %v 不在标准库或本模块内", got)
		}
		t.Logf("依赖闭包：%d 个包，全在标准库或本模块内", r.Deps)
	} else {
		if r.DepsWhy == "" {
			t.Errorf("交叉核对没跑成，却也没说为什么")
		}
		t.Logf("交叉核对核不了：%s", r.DepsWhy)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ② 把它弄坏看着它红。这一条不需要动本包一行代码 —— CheckIsolation 收一个目录，
// 所以临时目录就是现场。四种泄漏形状各来一发，外加一份干净的必须过。
func TestIsolationGoesRedOnALeak(t *testing.T) {
	cases := []struct {
		name string
		imp  string
		want string
	}{
		{"框架包（整条路径）", "runloop/internal/loop", "框架包"},
		// 这一发盯的是**第二条规则**：末段不在名单里、但模块前缀是框架的。
		// 早先只按末段匹配时，这一条会被一路放行到「标准库」那一档，隔离闸报 ✓ ——
		// 那是一个假绿，而且探针第一轮里它被另一条规则遮着，砍掉任一条都看不出红。
		{"框架包（末段不在名单里）", "runloop/internal/newpkg", "框架包"},
		{"框架包（裸名）", "provider", "框架包"},
		{"第三方模块", "github.com/foo/bar", "第三方模块"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		write(t, dir, "leak.go", "package kernel\n\nimport _ \""+c.imp+"\"\n")
		r := CheckIsolation(dir, "agentkernel")
		if r.Isolated() {
			t.Errorf("『%s』引了 %s 却说隔离 —— 这一根针是空的", c.name, c.imp)
			continue
		}
		if len(r.Leaks) != 1 {
			t.Errorf("『%s』该报出 1 处泄漏，得到 %d 处：%v", c.name, len(r.Leaks), r.Leaks)
			continue
		}
		for _, want := range []string{c.want, c.imp, "leak.go"} {
			if !strings.Contains(r.Leaks[0], want) {
				t.Errorf("『%s』那句话少了 %q：%s", c.name, want, r.Leaks[0])
			}
		}
	}

	// 假红比漏报更贵：它把下一个人指到错的凶手上，而这个项目为假红付过一轮。
	// 按末段匹配的话，下面这几条全会被报成「引了框架包」。
	for _, ok := range []string{"golang.org/x/tools", "honnef.co/go/tools", "github.com/vendor/secret",
		"github.com/runloop-ai/go-client", "github.com/x/clip"} {
		dir := t.TempDir()
		write(t, dir, "third.go", "package kernel\n\nimport _ \""+ok+"\"\n")
		r := CheckIsolation(dir, "agentkernel")
		if len(r.Leaks) != 1 || !strings.Contains(r.Leaks[0], "第三方模块") {
			t.Errorf("%s 该被报成第三方模块（不是框架包），得到 %v", ok, r.Leaks)
		}
	}

	// 干净的一份必须过 —— 只测「坏的会红」的话，一个永远报警的检查也算通过。
	dir := t.TempDir()
	write(t, dir, "clean.go", "package kernel\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nvar _ = fmt.Sprint(strings.ToUpper(\"x\"))\n")
	r := CheckIsolation(dir, "agentkernel")
	if !r.Isolated() {
		t.Errorf("只引标准库的一份竟被判成破了隔离：%v / %v / %v", r.Leaks, r.Cgo, r.Odd)
	}
	if r.Files != 1 || r.Imports != 2 || r.Stdlib != 2 {
		t.Errorf("分母不对：文件 %d、import %d、标准库 %d", r.Files, r.Imports, r.Stdlib)
	}
}

// ③ cgo 单列一栏。import "C" 的首段不含点号，按「首段有没有域名」那条启发式
// 它会被读成标准库并计入分母 —— 而 C 压根不是一个包，通过它能链进任何原生代码。
func TestCgoIsNotCountedAsStdlib(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "c.go", "package kernel\n\nimport \"C\"\n\nimport _ \"strings\"\n")
	r := CheckIsolation(dir, "agentkernel")
	if r.Isolated() {
		t.Errorf("用了 cgo 却说隔离")
	}
	if len(r.Cgo) != 1 || !strings.Contains(r.Cgo[0], "cgo") {
		t.Errorf("该把它报在 cgo 那一栏，得到 %v（泄漏栏是 %v）", r.Cgo, r.Leaks)
	}
	if len(r.Leaks) != 0 {
		t.Errorf("cgo 不该被混进泄漏那一栏：%v", r.Leaks)
	}
	if r.Stdlib != 1 {
		t.Errorf("cgo 不该被算进标准库那个分母，得到 %d（该是 1，只有 strings）", r.Stdlib)
	}
	if !strings.Contains(r.String(), "cgo") {
		t.Errorf("那句话里没提 cgo：%s", r.String())
	}
}

// ④ 藏在测试文件里的依赖也要被抓到 —— 只扫生产文件的话，这就是一个永久假绿。
func TestLeakInATestFileIsCaughtToo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package kernel\n\nimport _ \"strings\"\n")
	write(t, dir, "a_test.go", "package kernel\n\nimport _ \"runloop/internal/ledger\"\n")
	r := CheckIsolation(dir, "agentkernel")
	if r.Files != 1 || r.TestFiles != 1 {
		t.Errorf("该是 1 个生产文件 + 1 个测试文件，得到 %d + %d", r.Files, r.TestFiles)
	}
	if r.Imports != 1 || r.Stdlib != 1 {
		t.Errorf("生产文件的分母该是 1 条标准库 import，得到 %d / %d", r.Imports, r.Stdlib)
	}
	if r.Isolated() {
		t.Errorf("测试文件里引了框架包却说隔离")
	}
	if len(r.TestLeaks) != 1 || !strings.Contains(r.TestLeaks[0], "ledger") {
		t.Errorf("该把它报在测试文件那一栏，得到 %v（泄漏栏是 %v）", r.TestLeaks, r.Leaks)
	}
	if len(r.Leaks) != 0 {
		t.Errorf("它不该被混进生产文件那一栏：%v", r.Leaks)
	}
}

// ⑤ 子目录也要扫。不递归的话，将来任何一个子包都完全不在扫描范围内 ——
// 而 go list -deps 那一路在工具链缺席时是核不了的，那时子包就一格守卫都没有。
func TestSubdirectoriesAreScanned(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package kernel\n\nimport _ \"strings\"\n")
	write(t, dir, filepath.Join("sub", "b.go"), "package sub\n\nimport _ \"runloop/internal/newpkg\"\n")
	r := CheckIsolation(dir, "agentkernel")
	if r.Files != 2 {
		t.Errorf("该扫到 2 个文件（含子目录里那个），得到 %d", r.Files)
	}
	if r.Isolated() {
		t.Errorf("子目录里的泄漏没被抓到")
	}
	if len(r.Leaks) != 1 {
		t.Fatalf("该报 1 处，得到 %v", r.Leaks)
	}
	// 报出来的路径要能指到那个文件，否则下一个人不知道去哪儿改。
	for _, want := range []string{"sub/b.go", "newpkg"} {
		if !strings.Contains(r.Leaks[0], want) {
			t.Errorf("那句话少了 %q：%s", want, r.Leaks[0])
		}
	}
}

// ⑥ 反引号写的 import 也要被认出来。只 Trim 双引号的话，那条路径会连着引号
// 一起被当成包名，于是「引了第三方模块」被静默读成「引了一个认不出来的东西」。
func TestBacktickedImportIsStillClassified(t *testing.T) {
	dir := t.TempDir()
	bt := string(rune(96))
	write(t, dir, "bt.go", "package kernel\n\nimport _ "+bt+"github.com/x/y"+bt+"\n")
	r := CheckIsolation(dir, "agentkernel")
	if r.Isolated() {
		t.Errorf("反引号写的第三方 import 竟没被认出来")
	}
	if len(r.Leaks) != 1 || !strings.Contains(r.Leaks[0], "github.com/x/y") {
		t.Errorf("该报 1 处第三方泄漏，得到 %v（认不出的那栏是 %v）", r.Leaks, r.Odd)
	}
}

// ⑦ 「没跑成」既不许读成合格，也不许读成不合格。这一条是三态的支点：
// runloop 的闸门在 PATH 上没有 go 的那一次报了 13 项假红，
// 而「工具链不在」与「代码不合格」在报告里长得一模一样。
func TestUnscannableIsUnknownNotVerdict(t *testing.T) {
	// 目录不存在。
	gone := CheckIsolation(filepath.Join(t.TempDir(), "nope"), "agentkernel")
	if gone.Isolated() {
		t.Errorf("扫不到目录却说隔离 —— 那是最坏的一种绿")
	}
	if gone.DepsWhy == "" {
		t.Errorf("核不了却没说为什么")
	}
	if len(gone.Leaks) != 0 || len(gone.Odd) != 0 || len(gone.Cgo) != 0 {
		t.Errorf("扫不到目录却报出了 %v / %v / %v —— 「没跑成」被读成了「不合格」",
			gone.Leaks, gone.Odd, gone.Cgo)
	}
	if gone.Files != 0 || gone.Imports != 0 {
		t.Errorf("扫不到目录却数出了 %d 个文件 / %d 条 import", gone.Files, gone.Imports)
	}
	if !strings.Contains(gone.String(), "核不了") {
		t.Errorf("那句话没说「核不了」：%s", gone.String())
	}

	// 目录存在但没有 .go 文件。
	empty := CheckIsolation(t.TempDir(), "agentkernel")
	if empty.Isolated() {
		t.Errorf("一个源文件都没扫到却说隔离")
	}
	if empty.Files != 0 {
		t.Errorf("空目录数出了 %d 个文件", empty.Files)
	}
	if !strings.Contains(empty.String(), "核不了") {
		t.Errorf("那句话没说「核不了」：%s", empty.String())
	}

	// 解析不了的文件：报在「认不出」那一栏，不许静默跳过，也不许算进「扫到了」那个分母。
	bad := t.TempDir()
	write(t, bad, "broken.go", "package kernel\n\nimport (\n")
	r := CheckIsolation(bad, "agentkernel")
	if r.Isolated() {
		t.Errorf("一个解析不了的文件被静默跳过了")
	}
	if len(r.Odd) != 1 || !strings.Contains(r.Odd[0], "解析不了") {
		t.Errorf("该报「解析不了」，得到 %v", r.Odd)
	}
	if r.Files != 0 {
		t.Errorf("解析不了的文件不该被算进「扫到了」那个分母，得到 %d", r.Files)
	}
}

// ⑧ 交叉核对那一路跑不成时，import 扫描那一路的结论**不受影响**，
// 而它必须说清自己核不了。这正是「PATH 上没有 go」那一次的形状：
// 一次工具链缺席不该把十几项判据一起带红。
func TestMissingToolchainIsReportedNotFaked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module agentkernel\n\ngo 1.27\n")
	write(t, dir, "clean.go", "package kernel\n\nimport _ \"strings\"\n")
	t.Setenv("GOBIN_EXE", filepath.Join(t.TempDir(), "no-such-go"))
	r := CheckIsolation(dir, "")
	if r.Checked() {
		t.Errorf("工具链明明够不着，交叉核对却说自己跑成了")
	}
	if r.DepsWhy == "" {
		t.Errorf("核不了却没说为什么 —— 那就是把「核不了」瞒成了「合格」")
	}
	if !strings.Contains(r.DepsWhy, "没跑成") {
		t.Errorf("那句话没说清是哪一步没跑成：%s", r.DepsWhy)
	}
	// import 扫描那一路不依赖工具链，它的结论仍然成立。
	if !r.Isolated() {
		t.Errorf("工具链缺席不该把 import 扫描那一路一起带红：%v / %v", r.Leaks, r.Odd)
	}
	if r.Files != 1 || r.Stdlib != 1 {
		t.Errorf("分母不对：文件 %d、标准库 %d", r.Files, r.Stdlib)
	}
	if !strings.Contains(r.String(), "依赖闭包核不了") {
		t.Errorf("那句话没把「核不了」说出来：%s", r.String())
	}
	// 模块名从 go.mod 读，不写死在调用方 —— 写死的话模块一改名，
	// 所有自引用会被判成第三方，那是一场指向错凶手的假红。
	write(t, dir, "self.go", "package kernel\n\nimport _ \"agentkernel/sub\"\n")
	r2 := CheckIsolation(dir, "")
	if r2.Self != 1 {
		t.Errorf("go.mod 里写着 module agentkernel，那条自引用该被认成本模块内，得到 Self=%d（泄漏 %v）",
			r2.Self, r2.Leaks)
	}
	if !strings.Contains(r2.String(), "本模块内 1 条") {
		t.Errorf("那句话里没报本模块内那一条：%s", r2.String())
	}
	// 读不到 go.mod 时认不出本模块的名字，就没法把「子包引父包」与「引了一个无点的
	// 第三方模块」分开 —— 两者都会被算成标准库。那是一场**假绿**，所以必须记在
	// 「认不出」那一栏，让 Isolated() 返回假，而不是静默报一个 ✓。
	dir2 := t.TempDir()
	write(t, dir2, "self.go", "package kernel\n\nimport _ \"agentkernel/sub\"\n")
	r3 := CheckIsolation(dir2, "")
	if r3.Isolated() {
		t.Errorf("认不出模块名却说隔离 —— 那是一场假绿（自引用被静默算成了标准库）")
	}
	if len(r3.Odd) != 1 || !strings.Contains(r3.Odd[0], "go.mod") {
		t.Errorf("该把「读不到 go.mod」记在认不出那一栏，得到 %v", r3.Odd)
	}
	if len(r3.Leaks) != 0 {
		t.Errorf("它不该被报成泄漏（那会把下一个人指到错的凶手上）：%v", r3.Leaks)
	}
	if r3.Stdlib != 1 {
		t.Errorf("那条自引用今天确实被算进了标准库分母（%d）—— 这正是它必须被单独说出来的原因", r3.Stdlib)
	}
}

// ⑨ 注释里提到框架不算依赖：禁掉词汇的话，这份内核就没法写下它为什么长这样。
func TestMentioningTheFrameworkInProseIsNotALeak(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "prose.go",
		"package kernel\n\nimport _ \"strings\"\n\n// 这一行提到 loop、provider、ledger、ctxbuf 与 runloop，\n// 全是在讲证据从哪来，不是依赖。\nvar _ = strings.ToUpper(\"x\")\n")
	r := CheckIsolation(dir, "agentkernel")
	if !r.Isolated() {
		t.Errorf("散文里提到框架被判成了泄漏：%v", r.Leaks)
	}
	if r.Imports != 1 {
		t.Errorf("该只数到 1 条 import，得到 %d", r.Imports)
	}
}

// ⑩ 分类那一把尺自己也要被直接核：几种「既不像标准库也不像第三方」的形状
// 必须落在「认不出」那一栏，而不是被静默算进标准库那个分母。
func TestClassifyImportEdgeShapes(t *testing.T) {
	cases := []struct {
		path string
		want impClass
		why  string
	}{
		{"", impOdd, "空路径"},
		{"os", impStdlib, "标准库"},
		{"crypto/sha256", impStdlib, "标准库带斜杠"},
		{"C", impCgo, "cgo 不是包"},
		{"agentkernel", impSelf, "本模块"},
		{"agentkernel/sub", impSelf, "本模块子包"},
		{"runloop", impFramework, "框架模块本身"},
		{"runloop/internal/anything", impFramework, "框架模块下任何包（末段在不在名单里都算）"},
		{"provider", impFramework, "框架裸包名"},
		{"golang.org/x/tools", impThirdParty, "末段撞了框架包名，但它是第三方"},
		{"acmepkg/foo", impStdlib, "无点模块路径：这一条**已知会判错**，由 go list -deps 那一路兜"},
	}
	for _, c := range cases {
		if got := classifyImport(c.path, "agentkernel"); got != c.want {
			t.Errorf("%s（%s）：该判成 %d，得到 %d", c.path, c.why, c.want, got)
		}
	}
	// 上面那条「已知会判错」的必须真是一条判错的路 —— 否则那句注释就是在替一个
	// 其实存在的守卫站岗。这里把它钉住：它确实被算成标准库，而唯一的补救是闭包那一路。
	if classifyImport("acmepkg/foo", "agentkernel") != impStdlib {
		t.Errorf("无点模块路径的分类变了 —— 那么 isolate.go 里那句「那一路由 go list -deps 兜」的注释要跟着改")
	}
}
