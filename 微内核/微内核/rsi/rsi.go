// Package rsi 判「这一代比上一代改进算不算数」：产物指纹、判据集、读数三件互核，
// 判定与两份 manifest 一起锚进内核哈希链。消费冻结内核（Ledger/Basis/Verify），
// 不新增 49 格以外的任何格子；RSI 循环本身不住在这里。
package rsi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"unicode/utf8"

	"agentkernel"
)

const (
	KindGeneration = "rsi_generation"
	KindVerdict    = "rsi_verdict"
	Session        = "rsi"
	JudgeActor     = "krsi"
)

const (
	Improved    = "improved"
	NotImproved = "not_improved"
	Refused     = "refused"
	Undecidable = "undecidable"
)

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type CriterionDiff struct {
	ID     string `json:"id"`
	Change string `json:"change"` // tightened|loosened|added|removed
	Note   string `json:"note"`
}

type Reading struct {
	Metric      string   `json:"metric"`
	N           float64  `json:"n"`
	Basis       int      `json:"basis"` // 照内核整数枚举（contract §2.1），不收字符串
	Source      string   `json:"source"`
	WhyAbsent   string   `json:"why_absent"`
	Direction   string   `json:"direction"` // up=越大越好 down=越小越好
	Denominator *float64 `json:"denominator,omitempty"`
}

type Manifest struct {
	Producer       string          `json:"producer"`
	Generation     int             `json:"generation"`
	Artifacts      []Artifact      `json:"artifacts"`
	CriteriaSHA256 string          `json:"criteria_sha256"`
	CriteriaDiff   []CriterionDiff `json:"criteria_diff"`
	Readings       []Reading       `json:"readings"`
	Claim          string          `json:"claim"`
}

type MetricResult struct {
	Metric string  `json:"metric"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Better bool    `json:"better"`
}

type Verdict struct {
	State   string         `json:"state"`
	Reasons []string       `json:"reasons"`
	Metrics []MetricResult `json:"metrics"`
}

// CriteriaCatalog 是声明目录，criterionImpl 是实现登记。
// 「声明 6 条、实现必须 6 条、不许恒等」由 TestCatalogMatchesImplementation 机械守。
var CriteriaCatalog = []string{"R1", "R2", "R3", "R4", "R5", "R6"}

var criterionImpl = map[string]any{
	"R1": checkStructure,
	"R2": checkArtifacts,
	"R3": checkCriteria,
	"R4": checkBasis,
	"R5": checkImprovement,
	"R6": Anchor,
}

func ImplementedCriteria() []string {
	out := make([]string, 0, len(criterionImpl))
	for k := range criterionImpl {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Judge 只判「读数支不支持改进声明」，不判产物质量本身。
//
//	precedence：refused > undecidable > not_improved > improved —— 量不到不许塌成「没改进」。
func Judge(prev, next Manifest) Verdict {
	if reasons := checkStructure(prev, next); len(reasons) > 0 {
		return Verdict{State: Refused, Reasons: reasons}
	}
	v := checkImprovement(prev, next)
	var refuse []string
	refuse = append(refuse, checkArtifacts(prev, next)...)
	refuse = append(refuse, checkCriteria(prev, next)...)
	r4refuse, r4undec := checkBasis(prev, next)
	refuse = append(refuse, r4refuse...)
	if v.State == Refused {
		refuse = append(refuse, v.Reasons...)
	}
	if len(refuse) > 0 {
		return Verdict{State: Refused, Reasons: refuse, Metrics: v.Metrics}
	}
	if v.State == Undecidable {
		r4undec = append(r4undec, v.Reasons...)
	}
	if len(r4undec) > 0 {
		return Verdict{State: Undecidable, Reasons: r4undec, Metrics: v.Metrics}
	}
	return v
}

func checkStructure(prev, next Manifest) []string {
	var rs []string
	for _, m := range []Manifest{prev, next} {
		side := fmt.Sprintf("第 %d 代", m.Generation)
		if strings.TrimSpace(m.Producer) == "" {
			rs = append(rs, "R1: "+side+" 缺 producer")
		}
		if m.Generation < 1 {
			rs = append(rs, fmt.Sprintf("R1: generation=%d，必须从 1 起", m.Generation))
		}
		if strings.TrimSpace(m.Claim) == "" {
			rs = append(rs, "R1: "+side+" 缺 claim")
		}
		if n := utf8.RuneCountInString(m.Claim); n > kernel.MaxFreeText {
			rs = append(rs, fmt.Sprintf("R1: %s claim 有 %d 字符，超单格上限 %d", side, n, kernel.MaxFreeText))
		}
		if strings.TrimSpace(m.CriteriaSHA256) == "" {
			rs = append(rs, "R1: "+side+" 缺 criteria_sha256")
		}
		if len(m.Artifacts) == 0 {
			rs = append(rs, "R1: "+side+" 产物清单为空")
		}
		seenPath := map[string]bool{}
		for _, a := range m.Artifacts {
			if a.Path == "" || a.Bytes < 0 || len(a.SHA256) != 64 {
				rs = append(rs, fmt.Sprintf("R1: %s 产物 %q 形状坏（path/sha256/bytes）", side, a.Path))
			} else if _, err := hex.DecodeString(a.SHA256); err != nil {
				rs = append(rs, fmt.Sprintf("R1: %s 产物 %q 的 sha256 不是十六进制", side, a.Path))
			}
			if seenPath[a.Path] {
				rs = append(rs, fmt.Sprintf("R1: %s 产物路径 %q 重复", side, a.Path))
			}
			seenPath[a.Path] = true
		}
		seenMetric := map[string]bool{}
		for _, r := range m.Readings {
			if strings.TrimSpace(r.Metric) == "" {
				rs = append(rs, "R1: "+side+" 有读数缺 metric 名")
			}
			if seenMetric[r.Metric] {
				rs = append(rs, fmt.Sprintf("R1: %s 指标 %q 重复", side, r.Metric))
			}
			seenMetric[r.Metric] = true
			if r.Direction != "up" && r.Direction != "down" {
				rs = append(rs, fmt.Sprintf("R1: %s 指标 %q 方向 %q 不认（只收 up/down）", side, r.Metric, r.Direction))
			}
			if r.Basis < int(kernel.BasisMeasured) || r.Basis > int(kernel.BasisAbsent) {
				rs = append(rs, fmt.Sprintf("R1: %s 指标 %q basis=%d 不在内核枚举档内", side, r.Metric, r.Basis))
			}
			if kernel.Basis(r.Basis) == kernel.BasisAbsent && strings.TrimSpace(r.WhyAbsent) == "" {
				rs = append(rs, fmt.Sprintf("R1: %s 指标 %q 缺席却没带理由", side, r.Metric))
			}
			if kernel.Basis(r.Basis) == kernel.BasisRelayed && strings.TrimSpace(r.Source) == "" {
				rs = append(rs, fmt.Sprintf("R1: %s 指标 %q 是转述却没带 source", side, r.Metric))
			}
			if r.Denominator != nil && *r.Denominator <= 0 {
				rs = append(rs, fmt.Sprintf("R1: %s 指标 %q 分母 %v 不是正数", side, r.Metric, *r.Denominator))
			}
		}
	}
	if prev.Producer != "" && next.Producer != "" && prev.Producer != next.Producer {
		rs = append(rs, fmt.Sprintf("R1: 两代生产者不同（%q vs %q），无从谈改进", prev.Producer, next.Producer))
	}
	if next.Generation <= prev.Generation {
		rs = append(rs, fmt.Sprintf("R1: 这一代（%d）不比上一代（%d）新", next.Generation, prev.Generation))
	}
	pm, nm := readingsOf(prev), readingsOf(next)
	for name, p := range pm {
		if n, ok := nm[name]; ok && p.Direction != n.Direction {
			rs = append(rs, fmt.Sprintf("R1: 指标 %q 两代方向不一致（%s vs %s）", name, p.Direction, n.Direction))
		}
	}
	return rs
}

func artifactSet(m Manifest) map[string]string {
	out := make(map[string]string, len(m.Artifacts))
	for _, a := range m.Artifacts {
		out[a.Path] = a.SHA256
	}
	return out
}

func checkArtifacts(prev, next Manifest) []string {
	if maps.Equal(artifactSet(prev), artifactSet(next)) {
		return []string{"R2: 两代产物指纹集完全相同 —— 同字节换标签不算改进"}
	}
	return nil
}

func checkCriteria(prev, next Manifest) []string {
	if prev.CriteriaSHA256 == next.CriteriaSHA256 {
		if len(next.CriteriaDiff) > 0 {
			return []string{"R3: 判据指纹没变却报了 criteria_diff"}
		}
		return nil
	}
	if len(next.CriteriaDiff) == 0 {
		return []string{"R3: 判据指纹变了却没交 criteria_diff"}
	}
	var rs []string
	for _, d := range next.CriteriaDiff {
		switch d.Change {
		case "tightened", "added":
		case "loosened", "removed":
			rs = append(rs, fmt.Sprintf("R3: 判据 %q 被 %s —— 判据放水，改进不算数", d.ID, d.Change))
		default:
			rs = append(rs, fmt.Sprintf("R3: 判据 %q 的 change=%q 不认", d.ID, d.Change))
		}
		if strings.TrimSpace(d.ID) == "" {
			rs = append(rs, "R3: criteria_diff 有条目缺 id")
		}
	}
	return rs
}

// checkBasis 把「诚实的缺席」（undecidable）与「拿不可判定的数冒充证据」（refused）分开。
func checkBasis(prev, next Manifest) (refuse, undec []string) {
	for _, m := range []Manifest{prev, next} {
		for _, r := range m.Readings {
			switch kernel.Basis(r.Basis) {
			case kernel.BasisMeasured, kernel.BasisRelayed:
			case kernel.BasisAbsent:
				undec = append(undec, fmt.Sprintf("R4: 指标 %q 第 %d 代缺席（%s）—— 量不到，不判", r.Metric, m.Generation, r.WhyAbsent))
			default:
				refuse = append(refuse, fmt.Sprintf("R4: 指标 %q 第 %d 代 basis=%s 不可判定，不许当证据", r.Metric, m.Generation, kernel.Basis(r.Basis)))
			}
		}
	}
	return refuse, undec
}

func readingsOf(m Manifest) map[string]Reading {
	out := make(map[string]Reading, len(m.Readings))
	for _, r := range m.Readings {
		out[r.Metric] = r
	}
	return out
}

func valueOf(r Reading) float64 {
	if r.Denominator != nil {
		return r.N / *r.Denominator
	}
	return r.N
}

func checkImprovement(prev, next Manifest) Verdict {
	pm, nm := readingsOf(prev), readingsOf(next)
	var refuse, undec []string
	var results []MetricResult
	var common []string
	for name := range pm {
		if _, ok := nm[name]; ok {
			common = append(common, name)
		} else {
			undec = append(undec, fmt.Sprintf("R5: 指标 %q 只有上一代有，量不到改进", name))
		}
	}
	for name := range nm {
		if _, ok := pm[name]; !ok {
			undec = append(undec, fmt.Sprintf("R5: 指标 %q 只有这一代有，量不到改进", name))
		}
	}
	sort.Strings(common)
	if len(pm) == 0 && len(nm) == 0 {
		undec = append(undec, "R5: 两侧都没有读数 —— 没量过不算改进")
	}
	for _, name := range common {
		p, n := pm[name], nm[name]
		if kernel.Basis(p.Basis) == kernel.BasisAbsent || kernel.Basis(n.Basis) == kernel.BasisAbsent {
			continue // 缺席归 R4 判，不拿它当 0 比
		}
		if (p.Denominator == nil) != (n.Denominator == nil) {
			refuse = append(refuse, fmt.Sprintf("R5: 指标 %q 一侧带分母另一侧没交 —— 无分母比率不判", name))
			continue
		}
		pv, nv := valueOf(p), valueOf(n)
		better := nv > pv
		if p.Direction == "down" {
			better = nv < pv
		}
		results = append(results, MetricResult{Metric: name, Before: pv, After: nv, Better: better})
	}
	if len(refuse) > 0 {
		return Verdict{State: Refused, Reasons: refuse, Metrics: results}
	}
	if len(undec) > 0 {
		return Verdict{State: Undecidable, Reasons: undec, Metrics: results}
	}
	v := Verdict{State: Improved, Metrics: results}
	for _, mr := range results {
		if !mr.Better {
			v.State = NotImproved
			v.Reasons = append(v.Reasons, fmt.Sprintf("R5: 指标 %q 没变好（%v → %v）", mr.Metric, mr.Before, mr.After))
		}
	}
	return v
}

func payloadH(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Anchor 把两份 manifest 与判定写进内核链。同一代发同一份 manifest 幂等跳过；
// 同一代发不同 manifest / 不同判定 → R6 拒。结构不可信（R1）的东西不许进链。
func Anchor(l *kernel.Ledger, prev, next Manifest, v Verdict) error {
	if rs := checkStructure(prev, next); len(rs) > 0 {
		return fmt.Errorf("%s", strings.Join(rs, "；"))
	}
	if err := appendManifest(l, prev); err != nil {
		return err
	}
	if err := appendManifest(l, next); err != nil {
		return err
	}
	if err := appendVerdict(l, next.Generation, v); err != nil {
		return err
	}
	return l.Verify()
}

func appendManifest(l *kernel.Ledger, m Manifest) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	h := payloadH(payload)
	for _, e := range l.Docs() {
		if e.Kind == KindGeneration && e.Actor == m.Producer && e.Step == m.Generation {
			if e.PayloadH == h {
				return nil
			}
			return fmt.Errorf("R6: %s 第 %d 代已有一份不同的 manifest 在链上，重记被拒", m.Producer, m.Generation)
		}
	}
	_, err = l.Append(kernel.Record{
		Session: Session, Actor: m.Producer, Step: m.Generation, Kind: KindGeneration,
		Claim: m.Claim, Basis: kernel.BasisRelayed, Source: m.Producer, Payload: payload,
	})
	return err
}

func appendVerdict(l *kernel.Ledger, generation int, v Verdict) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h := payloadH(payload)
	for _, e := range l.Docs() {
		if e.Kind == KindVerdict && e.Step == generation {
			if e.PayloadH == h {
				return nil
			}
			return fmt.Errorf("R6: 第 %d 代已有一份不同的判定在链上，重判被拒", generation)
		}
	}
	claim := fmt.Sprintf("%s：%d 条理由、%d 个指标（详见原像）", v.State, len(v.Reasons), len(v.Metrics))
	_, err = l.Append(kernel.Record{
		Session: Session, Actor: JudgeActor, Step: generation, Kind: KindVerdict,
		Claim: claim, Basis: kernel.BasisMeasured, Payload: payload,
	})
	return err
}
