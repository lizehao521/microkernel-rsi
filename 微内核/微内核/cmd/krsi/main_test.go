package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentkernel"
	"agentkernel/rsi"
)

func f64(x float64) *float64 { return &x }

func manifest(gen int) rsi.Manifest {
	m := rsi.Manifest{
		Producer:       "p",
		Generation:     gen,
		Artifacts:      []rsi.Artifact{{Path: "a.go", SHA256: strings.Repeat("a", 64), Bytes: 10}},
		CriteriaSHA256: "c1",
		Readings: []rsi.Reading{
			{Metric: "bytes", N: 100, Basis: int(kernel.BasisMeasured), Direction: "down"},
		},
		Claim: "说法",
	}
	if gen == 2 {
		m.Artifacts[0].SHA256 = strings.Repeat("b", 64)
		m.Readings[0].N = 90
	}
	return m
}

func writeManifest(t *testing.T, path string, m rsi.Manifest) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIImprovedRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "g1.json"), filepath.Join(dir, "g2.json")
	report, journal := filepath.Join(dir, "报告.txt"), filepath.Join(dir, "账本.jsonl")
	writeManifest(t, p1, manifest(1))
	writeManifest(t, p2, manifest(2))
	if rc := run([]string{p1, p2, report, journal}); rc != 0 {
		t.Fatalf("改进算数要 rc=0，got %d", rc)
	}
	rb, err := os.ReadFile(report)
	if err != nil || !strings.Contains(string(rb), "判定：improved") {
		t.Fatalf("报告要落盘且写明 improved：%v", err)
	}
	jb, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	l, _, rep, err := kernel.LoadLedger(jb)
	if err != nil || !rep.Decidable() {
		t.Fatalf("账本往返要可判定：%v / %s", err, rep.String())
	}
	if got := len(l.Docs()); got != 3 {
		t.Fatalf("链上应 3 条，got %d", got)
	}
	// 同一对重跑：幂等，链不涨，rc 不变
	if rc := run([]string{p1, p2, report, journal}); rc != 0 {
		t.Fatalf("重跑要 rc=0，got %d", rc)
	}
	jb2, _ := os.ReadFile(journal)
	l2, _, _, err := kernel.LoadLedger(jb2)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(l2.Docs()); got != 3 {
		t.Fatalf("幂等重跑链不许涨，got %d", got)
	}
}

func TestCLIFakeImprovementRefused(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "g1.json"), filepath.Join(dir, "g2.json")
	report, journal := filepath.Join(dir, "报告.txt"), filepath.Join(dir, "账本.jsonl")
	m2 := manifest(2)
	m2.Artifacts = manifest(1).Artifacts // 同字节换标签
	writeManifest(t, p1, manifest(1))
	writeManifest(t, p2, m2)
	if rc := run([]string{p1, p2, report, journal}); rc != 2 {
		t.Fatalf("假改进要 rc=2，got %d", rc)
	}
	rb, _ := os.ReadFile(report)
	if !strings.Contains(string(rb), "R2:") {
		t.Fatalf("报告要点名 R2：%s", rb)
	}
}

func TestCLIConflictingGenerationRefused(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "g1.json"), filepath.Join(dir, "g2.json")
	report, journal := filepath.Join(dir, "报告.txt"), filepath.Join(dir, "账本.jsonl")
	writeManifest(t, p1, manifest(1))
	writeManifest(t, p2, manifest(2))
	if rc := run([]string{p1, p2, report, journal}); rc != 0 {
		t.Fatalf("第一趟要 rc=0，got %d", rc)
	}
	m2 := manifest(2)
	m2.Claim = "换个说法同一代"
	writeManifest(t, p2, m2)
	if rc := run([]string{p1, p2, report, journal}); rc != 2 {
		t.Fatalf("同一代换 manifest 要 rc=2，got %d", rc)
	}
	rb, _ := os.ReadFile(report)
	if !strings.Contains(string(rb), "R6:") {
		t.Fatalf("报告要点名 R6：%s", rb)
	}
}

func TestCLIUndecidable(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "g1.json"), filepath.Join(dir, "g2.json")
	report, journal := filepath.Join(dir, "报告.txt"), filepath.Join(dir, "账本.jsonl")
	m2 := manifest(2)
	m2.Readings[0].Basis = int(kernel.BasisAbsent)
	m2.Readings[0].WhyAbsent = "这一代没量"
	writeManifest(t, p1, manifest(1))
	writeManifest(t, p2, m2)
	if rc := run([]string{p1, p2, report, journal}); rc != 3 {
		t.Fatalf("量不到要 rc=3（不塌成 0/1），got %d", rc)
	}
}

func TestCLINotImproved(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "g1.json"), filepath.Join(dir, "g2.json")
	report, journal := filepath.Join(dir, "报告.txt"), filepath.Join(dir, "账本.jsonl")
	m2 := manifest(2)
	m2.Readings[0].N = 100
	writeManifest(t, p1, manifest(1))
	writeManifest(t, p2, m2)
	if rc := run([]string{p1, p2, report, journal}); rc != 1 {
		t.Fatalf("没变好要 rc=1，got %d", rc)
	}
}

func TestCLIR1NotAnchored(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "g1.json"), filepath.Join(dir, "g2.json")
	report, journal := filepath.Join(dir, "报告.txt"), filepath.Join(dir, "账本.jsonl")
	m2 := manifest(2)
	m2.Producer = ""
	writeManifest(t, p1, manifest(1))
	writeManifest(t, p2, m2)
	if rc := run([]string{p1, p2, report, journal}); rc != 2 {
		t.Fatalf("R1 红要 rc=2，got %d", rc)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("R1 红不许进链（账本不该存在），err=%v", err)
	}
	rb, _ := os.ReadFile(report)
	if !strings.Contains(string(rb), "未锚定") {
		t.Fatalf("报告要写明未锚定：%s", rb)
	}
}
