package rsi

import (
	"strings"
	"testing"

	"agentkernel"
)

func f64(x float64) *float64 { return &x }

func baseManifest(gen int) Manifest {
	return Manifest{
		Producer:       "p",
		Generation:     gen,
		Artifacts:      []Artifact{{Path: "a.go", SHA256: strings.Repeat("a", 64), Bytes: 10}},
		CriteriaSHA256: "c1",
		Readings: []Reading{
			{Metric: "bytes", N: 100, Basis: int(kernel.BasisMeasured), Direction: "down"},
			{Metric: "pass", N: 8, Basis: int(kernel.BasisMeasured), Direction: "up", Denominator: f64(10)},
		},
		Claim: "基线",
	}
}

func nextManifest() Manifest {
	m := baseManifest(2)
	m.Artifacts = []Artifact{{Path: "a.go", SHA256: strings.Repeat("b", 64), Bytes: 9}}
	m.Readings = []Reading{
		{Metric: "bytes", N: 90, Basis: int(kernel.BasisMeasured), Direction: "down"},
		{Metric: "pass", N: 9, Basis: int(kernel.BasisMeasured), Direction: "up", Denominator: f64(10)},
	}
	m.Claim = "更小更准"
	return m
}

func TestJudgeImproved(t *testing.T) {
	v := Judge(baseManifest(1), nextManifest())
	if v.State != Improved {
		t.Fatalf("要 improved， got %s（%v）", v.State, v.Reasons)
	}
	if len(v.Metrics) != 2 {
		t.Fatalf("要比到 2 个指标，got %d", len(v.Metrics))
	}
	for _, mr := range v.Metrics {
		if !mr.Better {
			t.Fatalf("指标 %s 应判变好", mr.Metric)
		}
	}
}

func TestR1Structure(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(prev, next *Manifest)
	}{
		{"缺producer", func(p, n *Manifest) { n.Producer = "" }},
		{"缺claim", func(p, n *Manifest) { n.Claim = "" }},
		{"claim超长", func(p, n *Manifest) { n.Claim = strings.Repeat("字", kernel.MaxFreeText+1) }},
		{"缺判据指纹", func(p, n *Manifest) { n.CriteriaSHA256 = "" }},
		{"产物清单空", func(p, n *Manifest) { n.Artifacts = nil }},
		{"产物sha坏", func(p, n *Manifest) { n.Artifacts[0].SHA256 = "zz" }},
		{"产物路径重复", func(p, n *Manifest) { n.Artifacts = append(n.Artifacts, n.Artifacts[0]) }},
		{"basis没填", func(p, n *Manifest) { n.Readings[0].Basis = 0 }},
		{"basis不在档", func(p, n *Manifest) { n.Readings[0].Basis = 9 }},
		{"缺席无理由", func(p, n *Manifest) { n.Readings[0].Basis = int(kernel.BasisAbsent) }},
		{"转述无source", func(p, n *Manifest) { n.Readings[0].Basis = int(kernel.BasisRelayed) }},
		{"方向不认", func(p, n *Manifest) { n.Readings[0].Direction = "left" }},
		{"指标重复", func(p, n *Manifest) { n.Readings = append(n.Readings, n.Readings[0]) }},
		{"分母非正", func(p, n *Manifest) { n.Readings[1].Denominator = f64(0) }},
		{"代际不增", func(p, n *Manifest) { n.Generation = 1 }},
		{"生产者不同", func(p, n *Manifest) { n.Producer = "q" }},
		{"方向两代不一致", func(p, n *Manifest) { n.Readings[0].Direction = "up" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, n := baseManifest(1), nextManifest()
			tc.mutate(&p, &n)
			v := Judge(p, n)
			if v.State != Refused {
				t.Fatalf("要 refused，got %s", v.State)
			}
			if len(v.Reasons) == 0 || !strings.HasPrefix(v.Reasons[0], "R1:") {
				t.Fatalf("要点名 R1，got %v", v.Reasons)
			}
		})
	}
}

func TestR2SameArtifactsRefused(t *testing.T) {
	p, n := baseManifest(1), nextManifest()
	n.Artifacts = p.Artifacts // 同字节换标签
	v := Judge(p, n)
	if v.State != Refused || !strings.HasPrefix(v.Reasons[0], "R2:") {
		t.Fatalf("同产物要 R2 拒，got %s %v", v.State, v.Reasons)
	}
}

func TestR3Criteria(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(n *Manifest)
		want   string
	}{
		{"阈值放水", func(n *Manifest) {
			n.CriteriaSHA256 = "c2"
			n.CriteriaDiff = []CriterionDiff{{ID: "acc", Change: "loosened"}}
		}, "R3:"},
		{"删判据", func(n *Manifest) {
			n.CriteriaSHA256 = "c2"
			n.CriteriaDiff = []CriterionDiff{{ID: "acc", Change: "removed"}}
		}, "R3:"},
		{"change不认", func(n *Manifest) {
			n.CriteriaSHA256 = "c2"
			n.CriteriaDiff = []CriterionDiff{{ID: "acc", Change: "whatever"}}
		}, "R3:"},
		{"指纹没变却报diff", func(n *Manifest) {
			n.CriteriaDiff = []CriterionDiff{{ID: "acc", Change: "tightened"}}
		}, "R3:"},
		{"指纹变了没交diff", func(n *Manifest) { n.CriteriaSHA256 = "c2" }, "R3:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, n := baseManifest(1), nextManifest()
			tc.mutate(&n)
			v := Judge(p, n)
			if v.State != Refused || !strings.HasPrefix(v.Reasons[0], tc.want) {
				t.Fatalf("要 %s 拒，got %s %v", tc.want, v.State, v.Reasons)
			}
		})
	}
	p, n := baseManifest(1), nextManifest()
	n.CriteriaSHA256 = "c2"
	n.CriteriaDiff = []CriterionDiff{{ID: "acc", Change: "tightened"}, {ID: "new", Change: "added"}}
	if v := Judge(p, n); v.State != Improved {
		t.Fatalf("收紧+新增必须放行，got %s %v", v.State, v.Reasons)
	}
}

func TestR4Basis(t *testing.T) {
	for _, basis := range []kernel.Basis{kernel.BasisEstimated, kernel.BasisModelSays} {
		p, n := baseManifest(1), nextManifest()
		n.Readings[0].Basis = int(basis)
		v := Judge(p, n)
		if v.State != Refused || !strings.HasPrefix(v.Reasons[0], "R4:") {
			t.Fatalf("basis=%s 要 R4 拒，got %s %v", basis, v.State, v.Reasons)
		}
	}
	p, n := baseManifest(1), nextManifest()
	n.Readings[0].Basis = int(kernel.BasisAbsent)
	n.Readings[0].WhyAbsent = "这一代没量字节"
	v := Judge(p, n)
	if v.State != Undecidable {
		t.Fatalf("诚实缺席要 undecidable（不塌成红/绿），got %s", v.State)
	}
}

func TestR5Improvement(t *testing.T) {
	t.Run("没变好", func(t *testing.T) {
		p, n := baseManifest(1), nextManifest()
		n.Readings[0].N = 100 // bytes 没动
		v := Judge(p, n)
		if v.State != NotImproved || !strings.HasPrefix(v.Reasons[0], "R5:") {
			t.Fatalf("要 not_improved 且点名 R5，got %s %v", v.State, v.Reasons)
		}
	})
	t.Run("无分母比率", func(t *testing.T) {
		p, n := baseManifest(1), nextManifest()
		n.Readings[1].Denominator = nil
		v := Judge(p, n)
		if v.State != Refused || !strings.HasPrefix(v.Reasons[0], "R5:") {
			t.Fatalf("无分母比率要 R5 拒，got %s %v", v.State, v.Reasons)
		}
	})
	t.Run("指标只有一侧", func(t *testing.T) {
		p, n := baseManifest(1), nextManifest()
		n.Readings = n.Readings[:1]
		if v := Judge(p, n); v.State != Undecidable {
			t.Fatalf("匹配不上要 undecidable，got %s", v.State)
		}
	})
	t.Run("两侧都没读数", func(t *testing.T) {
		p, n := baseManifest(1), nextManifest()
		p.Readings, n.Readings = nil, nil
		if v := Judge(p, n); v.State != Undecidable {
			t.Fatalf("没量过不许算改进，got %s", v.State)
		}
	})
	t.Run("比率按分母算", func(t *testing.T) {
		p, n := baseManifest(1), nextManifest()
		p.Readings = []Reading{{Metric: "rate", N: 1, Basis: int(kernel.BasisMeasured), Direction: "up", Denominator: f64(2)}}
		n.Readings = []Reading{{Metric: "rate", N: 2, Basis: int(kernel.BasisMeasured), Direction: "up", Denominator: f64(3)}}
		v := Judge(p, n)
		if v.State != Improved || len(v.Metrics) != 1 {
			t.Fatalf("0.5→0.667 要 improved，got %s %v", v.State, v.Reasons)
		}
		if v.Metrics[0].Before != 0.5 {
			t.Fatalf("比率值需要除分母，got %v", v.Metrics[0].Before)
		}
	})
}

func TestR6Anchor(t *testing.T) {
	p, n := baseManifest(1), nextManifest()
	v := Judge(p, n)
	l := kernel.NewLedger()
	if err := Anchor(l, p, n, v); err != nil {
		t.Fatalf("锚定需要过：%v", err)
	}
	if got := len(l.Docs()); got != 3 {
		t.Fatalf("链上应 3 条（两代 manifest + 判定），got %d", got)
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("链要验得过：%v", err)
	}
	if err := Anchor(l, p, n, v); err != nil {
		t.Fatalf("同一份重锚要幂等：%v", err)
	}
	if got := len(l.Docs()); got != 3 {
		t.Fatalf("幂等重锚不许多出条目，got %d", got)
	}
	n2 := nextManifest()
	n2.Claim = "换了一份说法"
	if err := Anchor(l, p, n2, Judge(p, n2)); err == nil || !strings.Contains(err.Error(), "R6:") {
		t.Fatalf("同一代不同 manifest 要 R6 拒，got %v", err)
	}
	n3 := nextManifest()
	n3.Readings[0].N = 100 // 判定会变成 not_improved
	if err := Anchor(l, p, n3, Judge(p, n3)); err == nil || !strings.Contains(err.Error(), "R6:") {
		t.Fatalf("同一代不同判定要 R6 拒（manifest 冲突先响也算 R6），got %v", err)
	}
}

func TestPrecedenceRefusedBeatsUndecidable(t *testing.T) {
	p, n := baseManifest(1), nextManifest()
	n.Readings[1].Denominator = nil // pass 一侧带分母另一侧没交 → R5 拒
	n.Readings[0].Basis = int(kernel.BasisAbsent)
	n.Readings[0].WhyAbsent = "这一代没量字节" // bytes 缺席 → R4 核不了
	v := Judge(p, n)
	if v.State != Refused {
		t.Fatalf("R5 拒收要压过 R4 核不了（refused > undecidable），got %s %v", v.State, v.Reasons)
	}
	if !strings.Contains(strings.Join(v.Reasons, "\n"), "R5:") {
		t.Fatalf("要点名 R5，got %v", v.Reasons)
	}
	// 对照：分母补回去，同一份缺席才轮到 undecidable —— 证明翻转是 R5 拒收引起的
	n.Readings[1].Denominator = f64(10)
	if v := Judge(p, n); v.State != Undecidable {
		t.Fatalf("只剩缺席时要 undecidable，got %s", v.State)
	}
}

func TestR6VerdictConflict(t *testing.T) {
	p, n := baseManifest(1), nextManifest()
	v := Judge(p, n)
	l := kernel.NewLedger()
	if err := Anchor(l, p, n, v); err != nil {
		t.Fatalf("锚定需要过：%v", err)
	}
	// manifest 原样（幂等跳过），只换判定 —— 冲突必须在判定那一格响，不是 manifest 那格截胡
	v2 := v
	v2.State = NotImproved
	err := Anchor(l, p, n, v2)
	if err == nil || !strings.Contains(err.Error(), "R6:") || !strings.Contains(err.Error(), "重判被拒") {
		t.Fatalf("同一代不同判定要 R6 重判拒，got %v", err)
	}
	if got := len(l.Docs()); got != 3 {
		t.Fatalf("拒了就不许涨条目，got %d", got)
	}
}

func TestR6AnchorRejectsR1(t *testing.T) {
	p, bad := baseManifest(1), nextManifest()
	bad.Readings[0].Direction = "left" // R1 坏，但账本 Append 不管 payload 内容
	l := kernel.NewLedger()
	err := Anchor(l, p, bad, Judge(p, bad))
	if err == nil || !strings.Contains(err.Error(), "R1:") {
		t.Fatalf("结构坏件不许进链，要 R1 拒，got %v", err)
	}
	if got := len(l.Docs()); got != 0 {
		t.Fatalf("R1 拒了就不许留下条目，got %d", got)
	}
}

func TestCatalogMatchesImplementation(t *testing.T) {
	impl := ImplementedCriteria()
	if len(impl) != len(CriteriaCatalog) {
		t.Fatalf("目录声明 %d 条、实现登记 %d 条 —— 声明未实现或私增", len(CriteriaCatalog), len(impl))
	}
	seen := map[string]bool{}
	for _, id := range impl {
		seen[id] = true
		if criterionImpl[id] == nil {
			t.Fatalf("%s 登记了空实现（恒等返回的形状）", id)
		}
	}
	for _, id := range CriteriaCatalog {
		if !seen[id] {
			t.Fatalf("目录里的 %s 没有实现登记", id)
		}
	}
}
