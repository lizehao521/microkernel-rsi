package main

import (
	"reflect"
	"testing"
)

// M5 工具债 #113-3：四张相位表（turnPhases / phaseKind / stopTokens / settleKinds）的
// 同源钉与反臂。判据本体在产码 events.go 的 ValidatePhaseTables ——
// 上一轮实测：改一相名 ⇒ Go 全包绿，只有 Python 冒烟以 KeyError 崩在 rc=1（六态契约外）。
// 这一族针把「红」搬回 Go 侧：
//   - TestPhaseTablesAreSelfConsistent 钉出货态自洽（真树上必须绿）；
//   - TestPhaseTablesMatchShippedNames 用独立字面量钉「表长什么样」——静默改名的最后一道，
//     它不是第二条判据，是反臂的独立期望值（臂是叶子：不依赖被它审的那张表自己）；
//   - 改名 / 加相 / 删项 三种反臂各一根，另配 settleKinds 撞相、stopTokens 改名/删项三根，
//     每根都必须红；红不了 = 判据是装饰。变异只发生在测试进程内的表副本上，defer 还原，
//     不动盘上文件（非 git 树，改文件没有回滚）。

func swapPhaseTables(t *testing.T) {
	t.Helper()
	tp, pk, st, sk := turnPhases, phaseKind, stopTokens, settleKinds
	t.Cleanup(func() { turnPhases, phaseKind, stopTokens, settleKinds = tp, pk, st, sk })
}

func copyKindMap() map[string]string {
	out := map[string]string{}
	for k, v := range phaseKind {
		out[k] = v
	}
	return out
}

func copyBoolMap(src map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range src {
		out[k] = v
	}
	return out
}

func TestPhaseTablesAreSelfConsistent(t *testing.T) {
	if err := ValidatePhaseTables(); err != nil {
		t.Fatalf("出货态的四张相位表应当自洽，判据却红了：%v", err)
	}
}

func TestPhaseTablesMatchShippedNames(t *testing.T) {
	wantPhases := []string{"accept", "work", "check_call", "record_call"}
	wantKinds := map[string]string{
		"accept": "turn_accepted", "work": "shot_started",
		"check_call": "precheck", "record_call": "recorded",
	}
	wantStop := map[string]bool{"script": true, "turns": true, "policy": true, "driver": true, "capped": true, "abort": true}
	wantSettle := map[string]bool{"turn_settled": true, "turn_interrupted": true, "run_settled": true}
	if !reflect.DeepEqual(turnPhases, wantPhases) {
		t.Fatalf("turnPhases 漂了：出货态是 %v，盘上成了 %v —— 改相名要在两边同步登记，别静默", wantPhases, turnPhases)
	}
	if !reflect.DeepEqual(phaseKind, wantKinds) {
		t.Fatalf("phaseKind 漂了：出货态是 %v，盘上成了 %v", wantKinds, phaseKind)
	}
	if !reflect.DeepEqual(stopTokens, wantStop) {
		t.Fatalf("stopTokens 漂了：出货态是 %v，盘上成了 %v（六态一格不新增）", wantStop, stopTokens)
	}
	if !reflect.DeepEqual(settleKinds, wantSettle) {
		t.Fatalf("settleKinds 漂了：出货态是 %v，盘上成了 %v", wantSettle, settleKinds)
	}
	// Token() 的产出集也当场点一遍（钉住 loop.go 那六条 case；产出漂 ⇒ 判据之外这里也红）
	prod := map[string]bool{}
	for _, s := range []stopReason{stopScript, stopTurns, stopPolicy, stopDriver, stopCapped, stopAbort} {
		prod[s.Token()] = true
	}
	if !reflect.DeepEqual(prod, wantStop) {
		t.Fatalf("stopReason.Token() 的产出集与出货态不符：%v", prod)
	}
}

// 反臂一：改名 —— 只有 turnPhases 改了相名（phaseKind 没跟上），同源判据必须红。
// 这正是上一轮「改一相名 Go 全绿」那一族的第一格。
func TestPhaseTablesArmRenameIsRed(t *testing.T) {
	swapPhaseTables(t)
	turnPhases = []string{"accept", "work", "check_call", "record_call_v2"}
	if err := ValidatePhaseTables(); err == nil {
		t.Fatal("反臂失效：改了相位名，ValidatePhaseTables 仍然绿 —— 两套口径并存又回来了")
	} else {
		t.Logf("改名臂红在：%v", err)
	}
}

// 反臂二：加相 —— turnPhases 里添一相（演「给还没事件对的相硬配一个名」那一族）。
func TestPhaseTablesArmAddPhaseIsRed(t *testing.T) {
	swapPhaseTables(t)
	turnPhases = append(append([]string{}, turnPhases...), "read_back")
	if err := ValidatePhaseTables(); err == nil {
		t.Fatal("反臂失效：加了相（phaseKind 没跟上），判据仍然绿")
	} else {
		t.Logf("加相臂红在：%v", err)
	}
}

// 反臂三：删项 —— phaseKind 里删掉一项（turnPhases 没删，键集合不再恰等于相位表）。
func TestPhaseTablesArmDropEntryIsRed(t *testing.T) {
	swapPhaseTables(t)
	phaseKind = copyKindMap()
	delete(phaseKind, "record_call")
	if err := ValidatePhaseTables(); err == nil {
		t.Fatal("反臂失效：删了 phaseKind 一项，判据仍然绿")
	} else {
		t.Logf("删项臂红在：%v", err)
	}
}

// 反臂四：撞相 —— 某相的 kind 指到结算类事件上（连线 2 的现场）。
func TestPhaseTablesArmSettleKindCollideIsRed(t *testing.T) {
	swapPhaseTables(t)
	phaseKind = copyKindMap()
	phaseKind["work"] = "turn_settled"
	if err := ValidatePhaseTables(); err == nil {
		t.Fatal("反臂失效：相位 kind 撞进 settleKinds，判据仍然绿")
	} else {
		t.Logf("撞相臂红在：%v", err)
	}
}

// 反臂五：stopTokens 改名 —— 表里出现没有任何 Token() 产出过的 token（连线 3 正向）。
func TestPhaseTablesArmStopTokenRenameIsRed(t *testing.T) {
	swapPhaseTables(t)
	stopTokens = copyBoolMap(stopTokens)
	delete(stopTokens, "script")
	stopTokens["scrit"] = true
	if err := ValidatePhaseTables(); err == nil {
		t.Fatal("反臂失效：stopTokens 里混进没被产出的 token，判据仍然绿")
	} else {
		t.Logf("stop 改名臂红在：%v", err)
	}
}

// 反臂六：stopTokens 删项 —— 真产出的 token 不在表里（连线 3 反向；run 级结算会把真 token 判红）。
func TestPhaseTablesArmStopTokenDropIsRed(t *testing.T) {
	swapPhaseTables(t)
	stopTokens = copyBoolMap(stopTokens)
	delete(stopTokens, "abort")
	if err := ValidatePhaseTables(); err == nil {
		t.Fatal("反臂失效：Token() 产出的 abort 不在 stopTokens 里，判据仍然绿")
	} else {
		t.Logf("stop 删项臂红在：%v", err)
	}
}
