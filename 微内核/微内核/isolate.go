package kernel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// 隔离这件事由机器判，不由文档声明判。
//
// 一句写在 README 里的「本包不依赖框架」是散文：它不会在新加一条 import 时变红。
// 所以这里做两件事，都是机械的：
//  1. 解析本包（含子目录）每一个 .go 文件的 import 块，逐条问
//     「这是标准库吗」「这是框架包吗」「这是第三方模块吗」。
//  2. 工具链够得着时，再跑一次 go list -deps 交叉核对第 1 步的结论。
//
// 第 2 步跑不成的时候必须说「核不了」，既不许读成绿也不许读成红：
// runloop 的闸门在 PATH 上没有 go 的那一次报了 13 项假红，
// 而「没跑成」与「不合格」在报告里长得一模一样，是那个项目付过一轮的教训。
//
// 这条检查**看不见**的路，写在这里而不是藏在注释里：plugin.Open、os/exec 起一个框架进程、
// 以及调用方把一个框架结构体塞进 Cell.Value 那个 any 里 —— 三条都不产生 import 边。
// 真正兜住它们的是模块边界与 go list -deps 那一路；本包的 import 扫描是第二道，不是唯一一道。

// frameworkModules 是框架那一侧的**模块前缀**。按前缀匹配，不按末段匹配 ——
// 按末段匹配会造出一台假红发生器：golang.org/x/tools 的末段是 tools，
// 而 tools 恰好是框架的一个包名，于是引一个静态检查工具会被报成「隔离破了」。
// 假红比漏报更贵：它把下一个人指到错的凶手上，而这个项目为假红付过一轮。
var frameworkModules = []string{"runloop"}

// frameworkPkgs 是框架那一侧的**裸包名**（不带斜杠的 import 路径）。
// 它只匹配裸名，所以 golang.org/x/tools 不会撞进来。
// 这一份名单今天多半是**前瞻**的：只要内核还是独立 module，而框架包都在 runloop/internal/ 下面，
// Go 的 internal 规则就让编译器先把这类 import 挡死（编译都过不去，而崩不算红）。
// 它真正生效的那一天是：内核被并进框架的模块树，或框架包被提出 internal/ ——
// 那时编译器不再保护这一格，这条规则才第一次有活干。
var frameworkPkgs = []string{
	"loop", "provider", "ledger", "tools", "ctxbuf", "grader", "perms",
	"subagent", "keys", "secret", "clip", "fake", "runnerlock", "amount", "digest",
}

// Isolation 是一次隔离检查的结论，自带分母。
type Isolation struct {
	Files   int      // 扫了几个非测试 .go 文件（含子目录）
	Imports int      // 其中一共几条 import
	Stdlib  int      // 其中几条是标准库
	Self    int      // 其中几条指向本模块自己（子包引父包）
	Leaks   []string // 破了隔离的那几条（框架包或第三方模块），逐条带文件名
	Cgo     []string // import "C" 那几条 —— cgo 能链进任何原生代码，所以单列一栏
	Odd     []string // 认不出来的那条（既不像标准库也不像本模块）—— 单独报，不混进 Leaks

	// 测试文件也算，只是分开计数：只扫生产文件的话，把一条框架依赖藏进
	// _test.go 就能让这个检查永远绿，而那正是它该抓的事。
	TestFiles int
	TestLeaks []string

	// 交叉核对那一路（go list -deps）。Checked() 为假时 DepsWhy 说清为什么核不了。
	DepsChecked   bool
	Deps          int
	DepsFramework []string
	DepsOther     []string
	DepsWhy       string
}

// Isolated 答「这一包与框架隔离吗」。
// 扫不到文件时返回 false —— 一个什么都没扫的检查说「隔离」，那是最坏的一种绿。
func (r Isolation) Isolated() bool {
	if r.Files == 0 {
		return false
	}
	return len(r.Leaks) == 0 && len(r.TestLeaks) == 0 && len(r.Cgo) == 0 && len(r.Odd) == 0
}

// Checked 答「go list -deps 那一路跑成了没有」。没跑成不算不合格，算核不了。
// String() 与判据都从这个方法读，不直接读字段 —— 一个没人读的访问器就是死格。
func (r Isolation) Checked() bool { return r.DepsChecked }

// DepsLeaks 是闭包那一路查出来的全部越界包（框架的与其它的合起来）。
func (r Isolation) DepsLeaks() []string {
	out := append(append([]string{}, r.DepsFramework...), r.DepsOther...)
	sort.Strings(out)
	return out
}

func (r Isolation) String() string {
	parts := []string{
		"扫了 " + strconv.Itoa(r.Files) + " 个文件、" + strconv.Itoa(r.Imports) + " 条 import",
		"其中标准库 " + strconv.Itoa(r.Stdlib) + " 条" + selfTail(r.Self),
		"另扫了 " + strconv.Itoa(r.TestFiles) + " 个测试文件",
	}
	if len(r.Leaks) > 0 {
		parts = append(parts, "✗ 隔离破了："+strings.Join(r.Leaks, "、"))
	}
	if len(r.TestLeaks) > 0 {
		parts = append(parts, "✗ 测试文件里隔离破了："+strings.Join(r.TestLeaks, "、"))
	}
	if len(r.Cgo) > 0 {
		parts = append(parts, "✗ 有 cgo 导入："+strings.Join(r.Cgo, "、"))
	}
	if len(r.Odd) > 0 {
		parts = append(parts, "✗ 认不出来："+strings.Join(r.Odd, "、"))
	}
	switch {
	case r.Files == 0:
		parts = append(parts, "—（一个源文件都没扫到，这一次核不了，不是合格）")
	case r.Isolated():
		parts = append(parts, "✓ 只依赖标准库")
	}
	if r.Checked() {
		parts = append(parts, "依赖闭包 "+strconv.Itoa(r.Deps)+" 个包"+depsTail(r.DepsLeaks()))
	} else {
		parts = append(parts, "依赖闭包核不了："+r.DepsWhy)
	}
	return strings.Join(parts, "；")
}

func depsTail(leaks []string) string {
	if len(leaks) == 0 {
		return "（全在标准库或本模块内）"
	}
	return "（✗ 其中 " + strings.Join(leaks, "、") + " 不在）"
}

func selfTail(n int) string {
	if n == 0 {
		return ""
	}
	return "、本模块内 " + strconv.Itoa(n) + " 条"
}

// CheckIsolation 递归扫 dir 下的 .go 文件。它是纯函数式的：给一个临时目录也能跑，
// 所以「把它弄坏看着它红」这件事不需要动本包一行代码。
// selfModule 空时会去读 dir/go.mod 里那一行 module —— 模块名的 owner 是 go.mod，不是这里。
func CheckIsolation(dir, selfModule string) Isolation {
	r := Isolation{}
	if strings.TrimSpace(selfModule) == "" {
		selfModule = moduleOf(dir)
	}
	files, why := walkGoFiles(dir)
	if why != "" {
		r.DepsWhy = why
		return r
	}
	if len(files) == 0 {
		r.DepsWhy = dir + " 下没有 .go 文件（连子目录也没有）"
		return r
	}
	if selfModule == "" {
		// 认不出本模块的名字，就没法把「子包引父包」与「引了一个无点的第三方模块」分开 ——
		// 两者都会被算成标准库。那是一个假绿，所以记在「认不出」那一栏，让 Isolated() 返回假。
		r.Odd = append(r.Odd, "读不到 "+dir+"/go.mod 里的 module 行，自引用会被当成标准库（这一次核不了）")
	}
	fset := token.NewFileSet()
	for _, f := range files {
		isTest := strings.HasSuffix(filepath.Base(f), "_test.go")
		parsed, perr := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if perr != nil {
			r.Odd = append(r.Odd, rel(dir, f)+" 解析不了："+perr.Error())
			continue
		}
		if isTest {
			r.TestFiles++
		} else {
			r.Files++
		}
		for _, imp := range parsed.Imports {
			path := importPath(imp)
			if !isTest {
				r.Imports++
			}
			switch classifyImport(path, selfModule) {
			case impSelf:
				if !isTest {
					r.Self++
				}
			case impStdlib:
				if !isTest {
					r.Stdlib++
				}
			case impCgo:
				r.addCgo(isTest, rel(dir, f)+" 用了 cgo（import \"C\"），它能链进任何原生代码")
			case impFramework:
				r.leak(isTest, rel(dir, f)+" 引了框架包 "+path)
			case impThirdParty:
				r.leak(isTest, rel(dir, f)+" 引了第三方模块 "+path)
			case impOdd:
				r.Odd = append(r.Odd, rel(dir, f)+" 引了认不出来的 "+path)
			}
		}
	}
	r.deps(dir, selfModule)
	return r
}

func (r *Isolation) leak(isTest bool, what string) {
	if isTest {
		r.TestLeaks = append(r.TestLeaks, what)
		return
	}
	r.Leaks = append(r.Leaks, what)
}

func (r *Isolation) addCgo(isTest bool, what string) {
	if isTest {
		r.TestLeaks = append(r.TestLeaks, what)
		return
	}
	r.Cgo = append(r.Cgo, what)
}

func rel(dir, f string) string {
	if p, err := filepath.Rel(dir, f); err == nil {
		return filepath.ToSlash(p)
	}
	return filepath.Base(f)
}

// walkGoFiles 递归收 .go 文件。不递归的话，将来任何一个子包都完全不在扫描范围内 ——
// 而 go list -deps 那一路在工具链缺席时是核不了的，那时子包就一格守卫都没有。
func walkGoFiles(dir string) ([]string, string) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, "读不到目录 " + dir + "（这一次核不了，不是合格）"
	}
	out := []string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, "扫 " + dir + " 时出错：" + err.Error()
	}
	sort.Strings(out)
	return out, ""
}

// moduleOf 从 go.mod 里读模块名。读不到就返回空串，而 CheckIsolation 会把这件事
// 记进「认不出」那一栏 —— 因为认不出模块名就没法把自引用与第三方分开，
// 而「分不开」如果被静默算成标准库，那是一场假绿，不是一场假红。
func moduleOf(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

type impClass int

const (
	impSelf impClass = iota
	impStdlib
	impFramework
	impThirdParty
	impCgo
	impOdd
)

// classifyImport 的判据：
//   - 标准库的路径首段不含点号（没有域名）。这条启发式**不等价于** go 命令自己的判据
//     （go 是拿模块图与 GOROOT 去解析的），它会在两种形状上判错，两种都单独处理：
//     `import "C"`（cgo，它压根不是一个包）单列一栏；首段不含点号的第三方模块
//     （Go 允许 module acmepkg 这种无点路径）会被读成标准库 —— 那一路由 go list -deps 兜。
//   - 框架按**模块前缀**匹配（runloop 或 runloop/…），裸名按 frameworkPkgs 匹配。
func classifyImport(path, selfModule string) impClass {
	if strings.TrimSpace(path) == "" {
		return impOdd
	}
	if path == "C" {
		return impCgo
	}
	if selfModule != "" && (path == selfModule || strings.HasPrefix(path, selfModule+"/")) {
		return impSelf
	}
	for _, m := range frameworkModules {
		if path == m || strings.HasPrefix(path, m+"/") {
			return impFramework
		}
	}
	first, last := path, path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		last = path[i+1:]
	}
	// 裸名（不带斜杠）才去比框架包名表。带斜杠的一律按前缀判，
	// 否则 golang.org/x/tools 这类路径会因为末段撞名而被误报成框架泄漏。
	if first == path {
		for _, f := range frameworkPkgs {
			if last == f {
				return impFramework
			}
		}
	}
	if strings.Contains(first, ".") {
		return impThirdParty
	}
	if strings.ContainsAny(first, " \t") || last == "" {
		return impOdd
	}
	return impStdlib
}

// deps 是交叉核对那一路。够不着工具链就老实说核不了。
func (r *Isolation) deps(dir, selfModule string) {
	goBin := os.Getenv("GOBIN_EXE")
	if goBin == "" {
		p, err := exec.LookPath("go")
		if err != nil {
			r.DepsWhy = "PATH 上找不到 go，也没给 GOBIN_EXE（这一路核不了，不是合格）"
			return
		}
		goBin = p
	}
	cmd := exec.Command(goBin, "list", "-deps", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		r.DepsWhy = "go list -deps 没跑成：" + firstLine(err.Error())
		return
	}
	r.DepsChecked = true
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r.Deps++
		// 框架的与其它的分开报：混成一栏的话，报红那一句说不出该去查依赖还是查 import。
		switch classifyImport(line, selfModule) {
		case impStdlib, impSelf:
		case impFramework:
			r.DepsFramework = append(r.DepsFramework, line)
		default:
			r.DepsOther = append(r.DepsOther, line)
		}
	}
	sort.Strings(r.DepsFramework)
	sort.Strings(r.DepsOther)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// SelfIsolation 检查本包自己。源文件的位置由编译期记下的路径给出 ——
// 编好的二进制被挪走之后那个路径可能不存在，那时它报「核不了」，不报绿。
func SelfIsolation() Isolation {
	dir, why := selfDir()
	if dir == "" {
		return Isolation{DepsWhy: why}
	}
	// 模块名从 go.mod 读，不写死在这里：写死的话，模块一改名，
	// 所有自引用会被判成第三方，而那是一场指向错凶手的假红。
	return CheckIsolation(dir, moduleOf(dir))
}

func selfDir() (string, string) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", "runtime.Caller 拿不到本文件的编译期路径"
	}
	dir := filepath.Dir(file)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", "本包源码目录 " + dir + " 不在了（二进制被挪走了？），这一路核不了"
	}
	return dir, ""
}

// importPath 取一条 import 的路径。它认双引号也认反引号 ——
// Go 允许 `path` 这种写法，只 Trim 双引号的话那条路径会连着引号一起被当成包名，
// 于是「引了第三方模块」被静默读成「引了一个认不出来的东西」。
func importPath(spec *ast.ImportSpec) string {
	if spec == nil || spec.Path == nil {
		return ""
	}
	return strings.Trim(spec.Path.Value, "\"`")
}
