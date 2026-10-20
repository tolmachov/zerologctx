package zerologctx

import (
	"fmt"
	"go/types"
	"iter"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// Semantic slots keep consumers from interpreting parallel fact vectors or
// sentinel encodings. A missing slot is always unknown and potentially escaping.
type resultProof struct {
	state     ctxState
	parameter int
}

func (r resultProof) returnedParameter() (int, bool) {
	return r.parameter - 1, r.parameter > 0
}

func (r resultProof) mayAlias() bool { return r.parameter < 0 }

type parameterEffect struct {
	state   ctxState
	escapes bool
}

func (p parameterEffect) preserves() bool { return p.state == stateUnreachable && !p.escapes }

func (s functionSummary) result(idx int) resultProof {
	proof := resultProof{state: stateUnknown, parameter: notAParam}
	if idx >= 0 && idx < len(s.Results) {
		proof.state = s.Results[idx]
		if idx < len(s.ResultParams) {
			proof.parameter = s.ResultParams[idx]
		}
	}
	return proof
}

func (s functionSummary) parameter(idx int) parameterEffect {
	effect := parameterEffect{state: stateUnknown, escapes: true}
	if idx >= 0 && idx < len(s.ParamEffects) {
		effect.state = s.ParamEffects[idx]
		if idx < len(s.ParamEscapes) {
			effect.escapes = s.ParamEscapes[idx]
		}
	}
	return effect
}

func (s functionSummary) results() iter.Seq2[int, resultProof] {
	return func(yield func(int, resultProof) bool) {
		for idx := range s.Results {
			if !yield(idx, s.result(idx)) {
				return
			}
		}
	}
}

func (s functionSummary) parameters() iter.Seq2[int, parameterEffect] {
	return func(yield func(int, parameterEffect) bool) {
		for idx := range s.ParamEffects {
			if !yield(idx, s.parameter(idx)) {
				return
			}
		}
	}
}

func (s functionSummary) anyEscapes() bool { return slices.Contains(s.ParamEscapes, true) }

// functionSummary is a function's proven postconditions, and the fact that
// carries them across package boundaries. Both state vectors hold raw ctxState
// values. For a result the bottom proves nothing, and callers read it as
// unknown. For a parameter it means no path wrote through it: the callee
// preserves the argument. ResultParams holds, per result, the 1-based position
// of the parameter that result certainly is; 0 while no exit has been seen,
// notAParam for anything else. ParamEscapes marks a
// parameter whose target the callee may leave reachable from somewhere the
// caller cannot see - a global, the heap, a result other than itself, a
// goroutine - after it returns.
//
// Preservation and non-escape are not proofs on their own: a function with no
// context postcondition and no proven returned parameter exports no fact,
// so callers in other packages treat it as unknown.
type functionSummary struct {
	Results      []ctxState
	ResultParams []int
	ParamEffects []ctxState
	ParamEscapes []bool
}

func (*functionSummary) AFact() {}

func (s *functionSummary) String() string {
	return fmt.Sprintf("zerolog context summary results=%v returns=%v effects=%v escapes=%v", s.Results, s.ResultParams, renderEffects(s.ParamEffects), s.ParamEscapes)
}

// renderEffects spells out what the bottom state means for a parameter, so the
// one encoding does not read as two.
func renderEffects(effects []ctxState) []string {
	rendered := make([]string, len(effects))
	for idx, effect := range effects {
		rendered[idx] = effect.String()
		if effect == stateUnreachable {
			rendered[idx] = "preserved"
		}
	}
	return rendered
}

func equalSummary(a, b functionSummary) bool {
	return slices.Equal(a.Results, b.Results) && slices.Equal(a.ResultParams, b.ResultParams) &&
		slices.Equal(a.ParamEffects, b.ParamEffects) && slices.Equal(a.ParamEscapes, b.ParamEscapes)
}

// joinSummary is the accumulator join. Two rounds that disagree about which
// parameter a result returns agree on none, and the result then reaches both,
// so both escape.
func joinSummary(a, b functionSummary) functionSummary {
	merged := functionSummary{
		Results: slices.Clone(a.Results), ResultParams: slices.Clone(a.ResultParams),
		ParamEffects: slices.Clone(a.ParamEffects), ParamEscapes: slices.Clone(a.ParamEscapes),
	}
	for idx := range merged.Results {
		merged.Results[idx] = joinState(merged.Results[idx], b.Results[idx])
		merged.ResultParams[idx] = joinParam(a.ResultParams[idx], b.ResultParams[idx])
		if merged.ResultParams[idx] == notAParam {
			for _, param := range []int{a.ResultParams[idx], b.ResultParams[idx]} {
				if param > 0 {
					merged.ParamEscapes[param-1] = true
				}
			}
		}
	}
	for idx := range merged.ParamEffects {
		merged.ParamEffects[idx] = joinState(merged.ParamEffects[idx], b.ParamEffects[idx])
		merged.ParamEscapes[idx] = merged.ParamEscapes[idx] || b.ParamEscapes[idx]
	}
	return merged
}

// unknownSummary claims nothing about a function: no result and no parameter
// effect is proven, and every parameter may escape, so every caller falls
// through to reporting.
func unknownSummary(fn *ssa.Function) functionSummary {
	summary := emptySummary(fn)
	for idx := range summary.Results {
		summary.Results[idx] = stateUnknown
		summary.ResultParams[idx] = notAParam
	}
	for idx := range summary.ParamEffects {
		summary.ParamEffects[idx] = stateUnknown
		summary.ParamEscapes[idx] = true
	}
	return summary
}

func emptySummary(fn *ssa.Function) functionSummary {
	return functionSummary{
		Results:      make([]ctxState, fn.Signature.Results().Len()),
		ResultParams: make([]int, fn.Signature.Results().Len()),
		ParamEffects: make([]ctxState, len(fn.Params)),
		ParamEscapes: make([]bool, len(fn.Params)),
	}
}

// exportSummaries publishes the proven postconditions of this package's
// exported functions. SSA is built without InstantiateGenerics, so each source
// function appears once and its object identifies it uniquely.
func (e *engine) exportSummaries() {
	for _, fn := range e.srcFuncs {
		obj, ok := fn.Object().(*types.Func)
		if !ok || obj.Pkg() != e.pass.Pkg || !obj.Exported() {
			continue
		}
		summary := e.local[fn]
		if !summary.hasProof() {
			continue
		}
		e.pass.ExportObjectFact(obj, &summary)
	}
}

func (s functionSummary) hasProof() bool {
	for _, param := range s.ResultParams {
		if param > 0 {
			return true
		}
	}
	for _, state := range s.Results {
		if state == stateHasContext || state == stateNoContext {
			return true
		}
	}
	for _, state := range s.ParamEffects {
		if state == stateHasContext || state == stateNoContext {
			return true
		}
	}
	return false
}

// notAParam is the top of the lattice ResultParams entries live in: the bottom
// is 0, no exit seen yet, and in between is the one parameter every exit
// returns.
const notAParam = -1

func joinParam(a, b int) int {
	switch {
	case a == 0:
		return b
	case b == 0 || a == b:
		return a
	default:
		return notAParam
	}
}

// summaryOf is the one summary lookup: this package's own solution for a body
// it analyses, otherwise the fact exported for the object, which only another
// package can have supplied while this one is being solved.
func (e *engine) summaryOf(object *types.Func, body *ssa.Function) (functionSummary, bool) {
	if summary, ok := e.local[body]; ok {
		return summary, true
	}
	if object == nil {
		return functionSummary{}, false
	}
	imported, cached := e.imported[object]
	if !cached {
		var fact functionSummary
		if e.pass.ImportObjectFact(object, &fact) {
			fact = clampSummary(fact)
			imported = &fact
		}
		e.imported[object] = imported
	}
	if imported == nil {
		return functionSummary{}, false
	}
	return *imported, true
}

// clampSummary keeps an imported fact inside the lattice: a state above the
// top reads as unknown, a parameter without an escape entry escapes, and a
// result whose parameter is missing or out of range returns none of them, so
// every parameter escapes through it. A malformed fact can therefore only add
// reports.
func clampSummary(fact functionSummary) functionSummary {
	escapes := make([]bool, len(fact.ParamEffects))
	for idx := range escapes {
		escapes[idx] = idx >= len(fact.ParamEscapes) || fact.ParamEscapes[idx]
	}
	params := make([]int, len(fact.Results))
	for idx := range params {
		if idx < len(fact.ResultParams) && fact.ResultParams[idx] >= notAParam && fact.ResultParams[idx] <= len(escapes) {
			params[idx] = fact.ResultParams[idx]
			continue
		}
		params[idx] = notAParam
		for param := range escapes {
			escapes[param] = true
		}
	}
	return functionSummary{Results: clampStates(fact.Results), ResultParams: params, ParamEffects: clampStates(fact.ParamEffects), ParamEscapes: escapes}
}

func clampStates(states []ctxState) []ctxState {
	clamped := make([]ctxState, len(states))
	for idx, state := range states {
		if state > stateUnknown {
			state = stateUnknown
		}
		clamped[idx] = state
	}
	return clamped
}

// summarizeExits owns the ordering of identity, proof, write and escape
// observations. All results and effects use the same terminal frame.
func (e *engine) summarizeExits(fn *ssa.Function, out map[*ssa.BasicBlock]*frame) functionSummary {
	summary := emptySummary(fn)
	// Results and parameter effects are both read at the same moment — after
	// the deferred and concurrent calls have taken effect — so each returning
	// block's terminal frame is built once and both are derived from it.
	type exit struct {
		ret      *ssa.Return
		terminal *frame
	}
	var exits []exit
	for _, block := range fn.Blocks {
		ret, returns := block.Instrs[len(block.Instrs)-1].(*ssa.Return)
		if current := out[block]; current != nil && returns {
			exits = append(exits, exit{ret: ret, terminal: e.withLaterEffects(fn, current)})
		}
	}
	// A result every exit returns as the same parameter is that parameter, not
	// an escape of it. Any other result that reaches a parameter is an alias
	// the caller cannot see.
	for idx := range summary.results() {
		for _, exit := range exits {
			parameter, known := returnedParam(fn, e.value(exit.terminal, exit.ret.Results[idx]))
			identity := notAParam
			if known {
				identity = parameter + 1
			}
			summary.ResultParams[idx] = joinParam(summary.ResultParams[idx], identity)
		}
	}
	results := fn.Signature.Results()
	written := make([]bool, len(fn.Params))
	for _, exit := range exits {
		terminal := exit.terminal
		for idx, value := range exit.ret.Results {
			if kindOf(results.At(idx).Type()) == kindOther {
				continue
			}
			summary.Results[idx] = joinState(summary.Results[idx], e.value(terminal, value).latticeState(terminal))
		}
		for idx, param := range fn.Params {
			kind := trackedPointerKind(param.Type())
			if kind == kindOther {
				continue
			}
			post := terminal.parameterState(param)
			written[idx] = written[idx] || post.written
			summary.ParamEffects[idx] = joinState(summary.ParamEffects[idx], post.state)
		}
		// A result leaves the function with everything it reaches. What a
		// parameter's target holds is already escaped: the target counts as
		// escaped from entry, so every store into it marks what it stores.
		for idx, value := range exit.ret.Results {
			if summary.result(idx).mayAlias() {
				e.invalidateEscape(terminal, value)
			}
		}
		for idx, param := range fn.Params {
			summary.ParamEscapes[idx] = summary.ParamEscapes[idx] || terminal.parameterState(param).escaped
		}
	}
	for idx, changed := range written {
		if !changed {
			summary.ParamEffects[idx] = stateUnreachable
		}
	}
	return summary
}
