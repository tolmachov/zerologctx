package zerologctx

import "testing"

func TestSummaryConflictingReturnedParametersEscape(t *testing.T) {
	first := functionSummary{
		Results: []ctxState{stateHasContext}, ResultParams: []int{1},
		ParamEffects: []ctxState{stateUnreachable, stateUnreachable}, ParamEscapes: []bool{false, false},
	}
	second := functionSummary{
		Results: []ctxState{stateHasContext}, ResultParams: []int{2},
		ParamEffects: []ctxState{stateUnreachable, stateUnreachable}, ParamEscapes: []bool{false, false},
	}
	joined := joinSummary(first, second)
	if _, ok := joined.result(0).returnedParameter(); ok {
		t.Fatal("conflicting identities kept a must-alias result")
	}
	if !joined.parameter(0).escapes || !joined.parameter(1).escapes {
		t.Fatal("conflicting returned parameters must both escape")
	}
	if joined.result(0).state != stateHasContext {
		t.Fatal("losing identity must retain the common context proof")
	}
}

func TestSummaryMissingSlotsFailClosed(t *testing.T) {
	summary := functionSummary{}
	if effect := summary.parameter(0); effect.state != stateUnknown || !effect.escapes || effect.preserves() {
		t.Fatalf("missing parameter = %+v, want unknown and escaping", effect)
	}
	if result := summary.result(0); result.state != stateUnknown || !result.mayAlias() {
		t.Fatalf("missing result = %+v, want unknown and potentially aliased", result)
	}
	summary = functionSummary{ParamEffects: []ctxState{stateUnreachable}, ParamEscapes: []bool{false}}
	if !summary.parameter(0).preserves() {
		t.Fatal("unwritten, non-escaping parameter must be preserved")
	}
}

func TestSummaryAccumulatorJoinLaws(t *testing.T) {
	summaries := []functionSummary{
		{Results: []ctxState{stateUnreachable}, ResultParams: []int{0}, ParamEffects: []ctxState{stateUnreachable, stateUnreachable}, ParamEscapes: []bool{false, false}},
		{Results: []ctxState{stateHasContext}, ResultParams: []int{1}, ParamEffects: []ctxState{stateHasContext, stateUnreachable}, ParamEscapes: []bool{false, false}},
		{Results: []ctxState{stateNoContext}, ResultParams: []int{2}, ParamEffects: []ctxState{stateUnreachable, stateNoContext}, ParamEscapes: []bool{false, false}},
		{Results: []ctxState{stateUnknown}, ResultParams: []int{notAParam}, ParamEffects: []ctxState{stateUnknown, stateUnknown}, ParamEscapes: []bool{true, true}},
	}
	for _, a := range summaries {
		if !equalSummary(joinSummary(a, a), a) {
			t.Fatalf("summary join is not idempotent: %s", a.String())
		}
		for _, b := range summaries {
			if !equalSummary(joinSummary(a, b), joinSummary(b, a)) {
				t.Fatal("summary join is not commutative")
			}
			for _, c := range summaries {
				if !equalSummary(joinSummary(joinSummary(a, b), c), joinSummary(a, joinSummary(b, c))) {
					t.Fatal("summary join is not associative")
				}
			}
		}
	}
}
