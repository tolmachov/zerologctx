package zerologctx

import (
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

// abstractValue is what is known about one SSA value.
//
// Four invariants are not expressible in the type and must be respected by
// every reader:
//
//   - state alone is meaningless for an event that carries locs. A zerolog
//     Event is mutated in place, so its context lives in frame.events keyed by
//     eventLocation; read it through latticeState or effectiveState, never as
//     v.state.
//   - nilCtx means something only once the effective state is stateNoContext.
//   - locs and memLocs are never mutated after construction. frame.clone
//     copies the frame's maps but shares these, so mutating one would corrupt
//     unrelated basic blocks.
//   - partialLocs marks that locs does not account for every path the value
//     may have taken. Only a complete singleton is a must-alias, and only a
//     must-alias may receive a postcondition; anything else may only be
//     widened.
//
// memLocs are the local memory locations the value is, or wraps, an address
// of. For a tracked pointer the state the value carries is a copy taken when
// it was formed; latticeState reads through memLocs as well, so a later store
// to the pointee is never missed.
//
// A non-empty elems makes the value an aggregate; its kind is kindOther and
// its state is unused.
type abstractValue struct {
	kind        valueKind
	state       ctxState
	nilCtx      bool
	locs        map[eventLocation]struct{}
	memLocs     map[memoryLocation]struct{}
	elems       []abstractValue
	partialLocs bool
}

// mustEvent returns the one event this value denotes, when the analyzer proved
// it denotes exactly one.
func (v abstractValue) mustEvent() (eventLocation, bool) {
	if v.partialLocs || len(v.locs) != 1 {
		return eventLocation{}, false
	}
	for location := range v.locs {
		return location, true
	}
	return eventLocation{}, false
}

func (v abstractValue) isZero() bool {
	return v.kind == kindOther && v.state == stateUnreachable && !v.nilCtx && len(v.locs) == 0 && len(v.memLocs) == 0 && len(v.elems) == 0
}

func unknownValue(kind valueKind) abstractValue {
	if kind == kindOther {
		return topValue
	}
	return abstractValue{kind: kind, state: stateUnknown, partialLocs: true}
}

// topValue is the lattice top: an untracked value about which nothing is
// proven. It is distinct from abstractValue{}, which is the bottom, so that
// joining incomparable values widens instead of collapsing to "unreachable".
var topValue = abstractValue{state: stateUnknown, partialLocs: true}

// freshEvent creates the event a location denotes and seeds what is known
// about it. Its own state stays at the bottom: an event with locs keeps its
// context in frame.events. Seeding is not a write, so no parameter write is
// recorded.
//
// A location is a call site, and a site in a loop creates a new event on every
// pass. When the event an earlier pass created may still be held, both share
// the location: seeding joins instead of replacing, and the new value is not a
// must-alias, so neither can receive a postcondition meant for the other.
// Otherwise the earlier event is unreachable and the location denotes the new
// one alone.
func (e *engine) freshEvent(current *frame, location eventLocation, state eventState) abstractValue {
	value := abstractValue{kind: kindEvent, locs: map[eventLocation]struct{}{location: {}}}
	if previous, revisited := current.events[location]; revisited && e.eventHeld(current, location) {
		current.events[location] = joinEventState(previous, state)
		value.partialLocs = true
		return value
	}
	current.events[location] = state
	return value
}

// eventHeld reports whether this function may still read an event created at
// location on an earlier pass when the site runs again: through local memory,
// through a value still available at the site - which in SSA reaches an
// earlier pass only through a phi - or through a deferred or concurrent call
// registered on that pass. An escaped earlier event is not held by that alone:
// anything reading it through the heap reads an untracked value, and anything
// writing it there is already accounted for by forgetEscaped and
// forgetAliased.
func (e *engine) eventHeld(current *frame, location eventLocation) bool {
	site, ok := location.value.(ssa.Instruction)
	if !ok {
		return true
	}
	for _, stored := range current.memory {
		if reachesEvent(stored, location) {
			return true
		}
	}
	for value, known := range current.values {
		if reachesEvent(known, location) && availableAt(value, site) {
			return true
		}
	}
	var operands []*ssa.Value
	for _, later := range e.laterCalls(site.Parent()) {
		for _, operand := range later.Operands(operands[:0]) {
			if *operand != nil && reachesEvent(current.values[*operand], location) {
				return true
			}
		}
	}
	return false
}

func reachesEvent(value abstractValue, location eventLocation) bool {
	if _, ok := value.locs[location]; ok {
		return true
	}
	return slices.ContainsFunc(value.elems, func(element abstractValue) bool { return reachesEvent(element, location) })
}

// availableAt reports whether value is defined on every path to site in the
// current pass, so that what the frame holds for it is current, not left over
// from an earlier pass.
func availableAt(value ssa.Value, site ssa.Instruction) bool {
	defining, ok := value.(ssa.Instruction)
	if !ok {
		return true
	}
	if defining.Block() != site.Block() {
		return defining.Block().Dominates(site.Block())
	}
	for _, instruction := range site.Block().Instrs {
		switch instruction {
		case site:
			return false
		case defining:
			return true
		}
	}
	return false
}

func joinValue(a, b abstractValue) abstractValue {
	if a.isZero() {
		return b
	}
	if b.isZero() {
		return a
	}
	if a.kind != b.kind {
		return topValue
	}
	r := abstractValue{kind: a.kind, state: joinState(a.state, b.state), partialLocs: a.partialLocs || b.partialLocs}
	r.nilCtx = joinNilCtx(a, b, r.state)
	if len(a.locs)+len(b.locs) != 0 {
		r.locs = make(map[eventLocation]struct{}, len(a.locs)+len(b.locs))
		maps.Copy(r.locs, a.locs)
		maps.Copy(r.locs, b.locs)
	}
	if len(a.memLocs)+len(b.memLocs) != 0 {
		r.memLocs = make(map[memoryLocation]struct{}, len(a.memLocs)+len(b.memLocs))
		maps.Copy(r.memLocs, a.memLocs)
		maps.Copy(r.memLocs, b.memLocs)
	}
	if len(a.elems) == len(b.elems) && len(a.elems) > 0 {
		r.elems = make([]abstractValue, len(a.elems))
		for idx := range a.elems {
			r.elems[idx] = joinValue(a.elems[idx], b.elems[idx])
		}
	}
	return r
}

// joinNilCtx is the must-fact half of joinValue: a nil final Ctx survives only
// when both sides are proven no-context with a nil argument.
func joinNilCtx(a, b abstractValue, joined ctxState) bool {
	return joined == stateNoContext && a.state == stateNoContext && b.state == stateNoContext && a.nilCtx && b.nilCtx
}

// covers reports whether joining b into a leaves a unchanged, without building
// the join. Once the worklist settles almost every join is such a no-op, and
// joinValue allocates fresh location sets before equalValue can tell.
func covers(a, b abstractValue) bool {
	if b.isZero() {
		return true
	}
	if a.isZero() {
		return false
	}
	if a.kind != b.kind {
		return equalValue(a, topValue)
	}
	state := joinState(a.state, b.state)
	if state != a.state || (b.partialLocs && !a.partialLocs) || joinNilCtx(a, b, state) != a.nilCtx {
		return false
	}
	for loc := range b.locs {
		if _, ok := a.locs[loc]; !ok {
			return false
		}
	}
	for loc := range b.memLocs {
		if _, ok := a.memLocs[loc]; !ok {
			return false
		}
	}
	// joinValue keeps elements only when both sides have the same number.
	return len(a.elems) == 0 || slices.EqualFunc(a.elems, b.elems, covers)
}

func unknownLike(value abstractValue) abstractValue {
	if len(value.elems) != 0 {
		result := abstractValue{elems: make([]abstractValue, len(value.elems))}
		for idx, element := range value.elems {
			result.elems[idx] = unknownLike(element)
		}
		return result
	}
	return unknownValue(value.kind)
}

func equalValue(a, b abstractValue) bool {
	return a.kind == b.kind && a.state == b.state && a.nilCtx == b.nilCtx && a.partialLocs == b.partialLocs &&
		maps.Equal(a.locs, b.locs) && maps.Equal(a.memLocs, b.memLocs) && slices.EqualFunc(a.elems, b.elems, equalValue)
}

// eventState is what is known about one zerolog Event object. state is a
// may-fact joined at a merge; nilCtx is a must-fact intersected at a merge,
// because a nil final Ctx may only be reported when every path passed nil.
// They live in one cell so that no writer can update one and forget the other.
type eventState struct {
	state  ctxState
	nilCtx bool
}

func joinEventState(a, b eventState) eventState {
	return eventState{state: joinState(a.state, b.state), nilCtx: a.nilCtx && b.nilCtx}
}

// frame is the dataflow state at one program point.
//
// values is keyed by SSA value and needs no widening for a key absent from a
// predecessor, because a value dominates its uses; memory is keyed by location
// and does need it, because a location absent from one predecessor is unknown,
// not unchanged.
//
// escapedRoots and escapedEvents hold what has left the analyzer's sight: the
// variables and parameters any part of which had its address taken somewhere
// the analyzer does not follow, and the events reachable from there. Code it
// does not follow may hold an alias and write through it whenever it runs.
// Escape is a may-fact, so both are united at a merge, and they only grow.
type frame struct {
	values        map[ssa.Value]abstractValue
	memory        map[memoryLocation]abstractValue
	events        map[eventLocation]eventState
	writes        map[*ssa.Parameter]bool
	escapedRoots  map[ssa.Value]struct{}
	escapedEvents map[eventLocation]struct{}
}

func newFrame() *frame {
	return &frame{
		values:        make(map[ssa.Value]abstractValue),
		memory:        make(map[memoryLocation]abstractValue),
		events:        make(map[eventLocation]eventState),
		writes:        make(map[*ssa.Parameter]bool),
		escapedRoots:  make(map[ssa.Value]struct{}),
		escapedEvents: make(map[eventLocation]struct{}),
	}
}

// clone copies each map whole rather than re-inserting entry by entry. Values
// are copied shallowly: their locs and memLocs are shared, which is why
// abstractValue forbids mutating them.
func (f *frame) clone() *frame {
	return &frame{
		values:        maps.Clone(f.values),
		memory:        maps.Clone(f.memory),
		events:        maps.Clone(f.events),
		writes:        maps.Clone(f.writes),
		escapedRoots:  maps.Clone(f.escapedRoots),
		escapedEvents: maps.Clone(f.escapedEvents),
	}
}

// resetFrom refills the frame from src, reusing the maps it already allocated.
// A block is re-entered many times before the worklist settles and almost
// every visit ends up changing nothing, so allocating a frame per visit would
// be the analyzer's largest source of garbage.
func (f *frame) resetFrom(src *frame) {
	clear(f.values)
	clear(f.memory)
	clear(f.events)
	clear(f.writes)
	clear(f.escapedRoots)
	clear(f.escapedEvents)
	maps.Copy(f.values, src.values)
	maps.Copy(f.memory, src.memory)
	maps.Copy(f.events, src.events)
	maps.Copy(f.writes, src.writes)
	maps.Copy(f.escapedRoots, src.escapedRoots)
	maps.Copy(f.escapedEvents, src.escapedEvents)
}

func joinFrame(dst, src *frame) bool {
	changed := false
	// covers is exact, so a join it does not cover always changes the value.
	for k, v := range src.values {
		if current := dst.values[k]; !covers(current, v) {
			dst.values[k], changed = joinValue(current, v), true
		}
	}
	for k, v := range src.memory {
		current, ok := dst.memory[k]
		if !ok {
			dst.memory[k], changed = joinValue(unknownLike(v), v), true
		} else if !covers(current, v) {
			dst.memory[k], changed = joinValue(current, v), true
		}
	}
	for k, current := range dst.memory {
		if _, ok := src.memory[k]; ok {
			continue
		}
		if unknown := unknownLike(current); !covers(current, unknown) {
			dst.memory[k], changed = joinValue(current, unknown), true
		}
	}
	for k, v := range src.events {
		n := joinEventState(dst.events[k], v)
		if dst.events[k] != n {
			dst.events[k], changed = n, true
		}
	}
	for k, v := range dst.events {
		if _, ok := src.events[k]; ok {
			continue
		}
		// A path that never mentions this event cannot uphold a must-fact
		// about it, so the nil-context claim does not survive the merge.
		if n := (eventState{state: v.state}); v != n {
			dst.events[k], changed = n, true
		}
	}
	for k, v := range src.writes {
		if v && !dst.writes[k] {
			dst.writes[k], changed = true, true
		}
	}
	changed = unite(dst.escapedRoots, src.escapedRoots) || changed
	changed = unite(dst.escapedEvents, src.escapedEvents) || changed
	return changed
}

// unite adds every member of src to dst and reports whether dst grew.
func unite[K comparable](dst, src map[K]struct{}) bool {
	grew := false
	for k := range src {
		if _, ok := dst[k]; !ok {
			dst[k], grew = struct{}{}, true
		}
	}
	return grew
}

func framesEqual(a, b *frame) bool {
	return maps.EqualFunc(a.values, b.values, equalValue) && maps.EqualFunc(a.memory, b.memory, equalValue) &&
		maps.Equal(a.events, b.events) && maps.Equal(a.writes, b.writes) &&
		maps.Equal(a.escapedRoots, b.escapedRoots) && maps.Equal(a.escapedEvents, b.escapedEvents)
}

func equalSummary(a, b functionSummary) bool {
	return slices.Equal(a.Results, b.Results) && slices.Equal(a.ResultParams, b.ResultParams) &&
		slices.Equal(a.ParamEffects, b.ParamEffects) && slices.Equal(a.ParamEscapes, b.ParamEscapes)
}

// sinkFinding is what the dataflow proves about one output operation. Naming
// the call, deciding suppression and deciding fixability are reporting policy
// and live in reporting.go.
type sinkFinding struct {
	pos    token.Pos
	state  ctxState
	spec   sinkSpec
	nilCtx bool
}

// collector accumulates the findings of one function. A nil *collector puts
// the transfer functions in summary-only mode, which makes "collecting" and
// "having somewhere to collect into" the same condition. Every call reaches it
// at most once: each reachable block is replayed once, and deferred and
// concurrent calls are judged once, after the straight-line flow.
type collector struct {
	findings []sinkFinding
}

// observe is what current proves about the context of an output with this
// receiver, and whether its final Ctx argument was nil. A nil receiver means
// nothing can be proven.
func (e *engine) observe(receiver ssa.Value, current *frame) (ctxState, bool) {
	if receiver == nil {
		return stateUnknown, false
	}
	value := e.value(current, receiver)
	return value.effectiveState(current), value.finalCtxWasNil(current)
}

type engine struct {
	pass     *analysis.Pass
	srcFuncs []*ssa.Function
	// findings are collected as each component settles, when its callees'
	// summaries are already final, so collecting never re-solves a function.
	findings map[*ssa.Function][]sinkFinding
	later    map[*ssa.Function][]ssa.CallInstruction
	// local holds the summary of every source function in this package. It is
	// the single store; the *types.Func view exists only inside
	// exportSummaries, where the analysis framework requires it.
	local map[*ssa.Function]functionSummary
	// imported caches the fact of every function object looked up, nil when it
	// has none. Facts are final, so one import per object is enough.
	imported map[*types.Func]*functionSummary
	// addresses holds the one location set of every local address read so far.
	// Location sets are never mutated, so one per location can be shared.
	addresses map[memoryLocation]map[memoryLocation]struct{}
}

func newEngine(pass *analysis.Pass, srcFuncs []*ssa.Function) *engine {
	return &engine{
		pass: pass, srcFuncs: srcFuncs,
		findings: make(map[*ssa.Function][]sinkFinding), later: make(map[*ssa.Function][]ssa.CallInstruction),
		local: make(map[*ssa.Function]functionSummary), imported: make(map[*types.Func]*functionSummary),
		addresses: make(map[memoryLocation]map[memoryLocation]struct{}),
	}
}

func (e *engine) solveSummaries() {
	for _, fn := range e.srcFuncs {
		e.local[fn] = emptySummary(fn)
	}
	components, selfCalls := summarySCCs(e.srcFuncs)
	for _, component := range components {
		recursive := len(component) > 1 || selfCalls[component[0]]
		if !recursive && !touchesZerolog(component[0]) {
			// Solving it would return the empty summary it already has and
			// find nothing, so it is not solved.
			continue
		}
		frames := e.solveComponent(component, recursive)
		for _, fn := range component {
			e.findings[fn] = e.collectFindings(fn, frames[fn])
		}
	}
}

// touchesZerolog reports whether fn has a body whose instructions use a zerolog
// value, or call into zerolog's log package, whose sinks take no zerolog value.
// A value that nothing uses can matter to no sink and no caller. A function
// that does neither can prove nothing, change nothing a caller tracks, and hold
// no sink. A function without a body counts as touching zerolog, since nothing
// about it is known.
func touchesZerolog(fn *ssa.Function) bool {
	if len(fn.Blocks) == 0 {
		return true
	}
	var operands []*ssa.Value
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok {
				if callee := call.Common().StaticCallee(); callee != nil {
					if object, _ := callee.Object().(*types.Func); inZerologLogPackage(object) {
						return true
					}
				}
			}
			for _, operand := range instruction.Operands(operands[:0]) {
				if *operand != nil && kindOf((*operand).Type()) != kindOther {
					return true
				}
			}
		}
	}
	return false
}

// blockFrames are the per-block dataflow states of one solved function.
type blockFrames struct {
	in  map[*ssa.BasicBlock]*frame
	out map[*ssa.BasicBlock]*frame
}

// solveComponent iterates the component to a fixed point and returns the
// frames of the settling pass. Those frames are final: every callee outside the
// component was solved first, and on the last pass either nothing inside it
// changed or nothing inside it reads what did.
//
// Termination does not depend on the transfer functions being monotone; it
// depends on the accumulator being. Each round joins its result into what is
// already known, so every summary slot can move at most twice (bottom, then a
// proof, then the top) and the loop is bounded by the lattice, not by a cap.
// Joining is also the honest answer when two rounds disagree: the top means
// nothing is proven, which reports.
//
// A component that is not recursive never reads its own summaries, so a
// second round would recompute the first exactly; its first round is final.
func (e *engine) solveComponent(component []*ssa.Function, recursive bool) map[*ssa.Function]blockFrames {
	frames := make(map[*ssa.Function]blockFrames, len(component))
	for {
		changed := false
		for _, fn := range component {
			summary, solved := e.solve(fn)
			frames[fn] = solved
			merged := joinSummary(e.local[fn], summary)
			if !equalSummary(e.local[fn], merged) {
				e.local[fn], changed = merged, true
			}
		}
		if !changed || !recursive {
			return frames
		}
	}
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

// summarySCCs returns callees before callers and recursive groups as a single
// component. Each group can therefore be solved to a local fixpoint without
// repeatedly re-analyzing unrelated functions. selfCalls marks the functions
// that reach their own summary directly, the one way a singleton component is
// recursive.
func summarySCCs(functions []*ssa.Function) ([][]*ssa.Function, map[*ssa.Function]bool) {
	local := make(map[*ssa.Function]bool, len(functions))
	for _, fn := range functions {
		local[fn] = true
	}
	edges := make(map[*ssa.Function][]*ssa.Function, len(functions))
	selfCalls := make(map[*ssa.Function]bool)
	for _, fn := range functions {
		seen := make(map[*ssa.Function]bool)
		addEdge := func(target *ssa.Function) {
			if target != nil && local[target] && !seen[target] {
				edges[fn] = append(edges[fn], target)
				seen[target] = true
				selfCalls[fn] = selfCalls[fn] || target == fn
			}
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				common := call.Common()
				addEdge(common.StaticCallee())
				for _, arg := range common.Args {
					addEdge(functionValue(arg))
					_, marshaler := marshalerMethod(arg)
					addEdge(marshaler)
				}
			}
		}
	}

	index := 0
	indices := make(map[*ssa.Function]int, len(functions))
	lowlinks := make(map[*ssa.Function]int, len(functions))
	onStack := make(map[*ssa.Function]bool, len(functions))
	stack := make([]*ssa.Function, 0, len(functions))
	components := make([][]*ssa.Function, 0, len(functions))
	var visit func(*ssa.Function)
	visit = func(fn *ssa.Function) {
		index++
		indices[fn], lowlinks[fn] = index, index
		stack, onStack[fn] = append(stack, fn), true
		for _, callee := range edges[fn] {
			if indices[callee] == 0 {
				visit(callee)
				lowlinks[fn] = min(lowlinks[fn], lowlinks[callee])
			} else if onStack[callee] {
				lowlinks[fn] = min(lowlinks[fn], indices[callee])
			}
		}
		if lowlinks[fn] != indices[fn] {
			return
		}
		component := make([]*ssa.Function, 0, 1)
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == fn {
				break
			}
		}
		components = append(components, component)
	}
	for _, fn := range functions {
		if indices[fn] == 0 {
			visit(fn)
		}
	}
	return components, selfCalls
}

func functionValue(value ssa.Value) *ssa.Function {
	switch value := value.(type) {
	case *ssa.Function:
		return value
	case *ssa.MakeClosure:
		fn, _ := value.Fn.(*ssa.Function)
		return fn
	default:
		return nil
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

// effectiveState is the single place the analysis fails closed. A tracked
// value sitting at the lattice bottom means no path established anything about
// it, which is unknown - not "safe". Every sink verdict goes through here.
func (v abstractValue) effectiveState(f *frame) ctxState {
	state := v.latticeState(f)
	if state == stateUnreachable && v.kind != kindOther {
		return stateUnknown
	}
	return state
}

// latticeState joins everything the value's context may come from: its own
// state, the events it may denote, and what every location it may point to
// holds now. A pointer is read through, not remembered: a later store to its
// pointee changes what it denotes, so the state copied when it was formed is
// only one contributor.
func (v abstractValue) latticeState(f *frame) ctxState {
	return v.stateWithin(f, nil)
}

func (v abstractValue) stateWithin(f *frame, visiting map[memoryLocation]bool) ctxState {
	state := v.state
	if v.kind == kindOther {
		return state
	}
	if v.kind == kindEvent {
		for loc := range v.locs {
			state = joinState(state, f.events[loc].state)
		}
	}
	for loc := range v.memLocs {
		stored, ok := f.memory[loc]
		if !ok || visiting[loc] {
			// Nothing is known there, or the pointer reaches itself.
			return stateUnknown
		}
		if visiting == nil {
			visiting = make(map[memoryLocation]bool)
		}
		visiting[loc] = true
		state = joinState(state, stored.stateWithin(f, visiting))
		delete(visiting, loc)
	}
	return state
}

// finalCtxWasNil reports whether every path to this value ended its context
// with Ctx(nil). It is a must-fact, so every contributor latticeState joins
// has to agree.
func (v abstractValue) finalCtxWasNil(f *frame) bool {
	if v.effectiveState(f) != stateNoContext {
		return false
	}
	if v.kind == kindEvent && len(v.locs) != 0 {
		for loc := range v.locs {
			if !f.events[loc].nilCtx {
				return false
			}
		}
	} else if !v.nilCtx {
		return false
	}
	// effectiveState above already read every location the value points to,
	// so none of them is missing and none reaches itself.
	for loc := range v.memLocs {
		if !f.memory[loc].finalCtxWasNil(f) {
			return false
		}
	}
	return true
}

func (e *engine) value(f *frame, value ssa.Value) abstractValue {
	if got, ok := f.values[value]; ok {
		return got
	}
	if location, ok := untrackedAddress(value); ok {
		result := topValue
		result.memLocs = e.addressSet(location)
		return result
	}
	if location, ok := localLocation(value); ok && trackedPointerKind(value.Type()) != kindOther {
		result, exists := f.memory[location]
		if !exists {
			result = unknownValue(kindOf(value.Type()))
		}
		result.memLocs = e.addressSet(location)
		return result
	}
	return unknownValue(kindOf(value.Type()))
}

// untrackedAddress returns the local location an address of any type but a
// tracked zerolog pointer denotes, looking through the interfaces it may be
// wrapped in. Such an address still reaches whatever is stored under it, so
// its value carries that location wherever it flows. The value depends on the
// syntax alone, so remember never stores it.
func untrackedAddress(value ssa.Value) (memoryLocation, bool) {
	switch wrapped := value.(type) {
	case *ssa.MakeInterface:
		value = wrapped.X
	case *ssa.ChangeInterface:
		return untrackedAddress(wrapped.X)
	}
	location, ok := localLocation(value)
	if !ok || trackedPointerKind(value.Type()) != kindOther {
		return memoryLocation{}, false
	}
	return location, true
}

func (e *engine) addressSet(location memoryLocation) map[memoryLocation]struct{} {
	set, ok := e.addresses[location]
	if !ok {
		set = map[memoryLocation]struct{}{location: {}}
		e.addresses[location] = set
	}
	return set
}

// solve runs the intraprocedural worklist to a fixed point and derives the
// function's postconditions from the frames it produced.
func (e *engine) solve(fn *ssa.Function) (functionSummary, blockFrames) {
	summary := emptySummary(fn)
	if len(fn.Blocks) == 0 {
		// An assembly or //go:linkname declaration has no body to read, so
		// nothing about it is proven.
		return unknownSummary(fn), blockFrames{}
	}

	entry := newFrame()
	for _, param := range fn.Params {
		kind := kindOf(param.Type())
		if kind == kindOther {
			continue
		}
		value := unknownValue(kind)
		if kind == kindEvent {
			value = e.freshEvent(entry, eventLocation{value: param}, eventState{state: stateUnknown})
		}
		if trackedPointerKind(param.Type()) == kindOther {
			entry.values[param] = value
			continue
		}
		// A tracked pointer parameter is a memory location and e.value reads it
		// from there, adding the location to what it returns. Seeding it into
		// values as well would freeze the entry state and hide every later
		// store.
		entry.memory[memoryLocation{root: param}] = value
	}
	in := make(map[*ssa.BasicBlock]*frame)
	out := make(map[*ssa.BasicBlock]*frame)
	e.propagate(fn.Blocks[0], entry, in, out)
	if e.mayRecover(fn) {
		e.propagate(fn.Recover, recoverEntry(fn, out), in, out)
	}
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
	for idx := range summary.ResultParams {
		for _, exit := range exits {
			summary.ResultParams[idx] = joinParam(summary.ResultParams[idx], returnedParam(fn, e.value(exit.terminal, exit.ret.Results[idx])))
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
			written[idx] = written[idx] || terminal.writes[param]
			if kind == kindEvent {
				summary.ParamEffects[idx] = joinState(summary.ParamEffects[idx], terminal.events[eventLocation{value: param}].state)
			} else {
				summary.ParamEffects[idx] = joinState(summary.ParamEffects[idx], terminal.memory[memoryLocation{root: param}].latticeState(terminal))
			}
		}
		// A result leaves the function with everything it reaches. What a
		// parameter's target holds is already escaped: the target counts as
		// escaped from entry, so every store into it marks what it stores.
		for idx, value := range exit.ret.Results {
			if summary.ResultParams[idx] < 0 {
				e.invalidateEscape(terminal, value)
			}
		}
		for idx, param := range fn.Params {
			_, event := terminal.escapedEvents[eventLocation{value: param}]
			_, memory := terminal.escapedRoots[param]
			summary.ParamEscapes[idx] = summary.ParamEscapes[idx] || event || memory
		}
	}
	// A parameter no path wrote to is preserved, whatever it holds at exit.
	for idx := range fn.Params {
		if !written[idx] {
			summary.ParamEffects[idx] = stateUnreachable
		}
	}
	return summary, blockFrames{in: in, out: out}
}

// propagate runs the worklist from one entry block to a fixed point.
func (e *engine) propagate(start *ssa.BasicBlock, entry *frame, in, out map[*ssa.BasicBlock]*frame) {
	in[start] = entry
	queue := []*ssa.BasicBlock{start}
	queued := map[*ssa.BasicBlock]bool{start: true}
	scratch := newFrame()
	for len(queue) > 0 {
		block := queue[0]
		queue = queue[1:]
		queued[block] = false
		scratch.resetFrom(in[block])
		for _, instruction := range block.Instrs {
			e.transfer(block, instruction, scratch, out, nil)
		}
		if previous := out[block]; previous != nil && framesEqual(previous, scratch) {
			continue
		}
		// The block's state changed, so this frame is kept and the scratch is
		// needed again for the next visit.
		current := scratch.clone()
		out[block] = current
		for _, successor := range block.Succs {
			if in[successor] == nil {
				in[successor] = current.clone()
				queue = append(queue, successor)
				queued[successor] = true
				continue
			}
			if joinFrame(in[successor], current) && !queued[successor] {
				queue = append(queue, successor)
				queued[successor] = true
			}
		}
	}
}

// mayRecover reports whether control may resume at fn's Recover block. Only a
// deferred function that calls recover itself can stop a panic, so a function
// whose every deferred callee is a builtin, or a body the analyzer can read
// without such a call, never resumes there. A dynamic callee, a body it cannot
// read, or a synthetic wrapper may.
func (e *engine) mayRecover(fn *ssa.Function) bool {
	if fn.Recover == nil {
		return false
	}
	for _, later := range e.laterCalls(fn) {
		if _, deferred := later.(*ssa.Defer); !deferred {
			continue
		}
		if _, builtin := later.Common().Value.(*ssa.Builtin); builtin {
			continue
		}
		callee := later.Common().StaticCallee()
		if callee == nil || callee.Synthetic != "" || len(callee.Blocks) == 0 || callsRecover(callee) {
			return true
		}
	}
	return false
}

// returnedParam is the 1-based position of the parameter value certainly is:
// the event a *zerolog.Event parameter denotes, or the address a tracked
// pointer parameter holds. It is notAParam for anything else.
func returnedParam(fn *ssa.Function, value abstractValue) int {
	for idx, param := range fn.Params {
		if trackedPointerKind(param.Type()) == kindOther {
			continue
		}
		if location, ok := value.mustEvent(); ok && location == (eventLocation{value: param}) {
			return idx + 1
		}
		if _, ok := value.memLocs[memoryLocation{root: param}]; ok && len(value.memLocs) == 1 && !value.partialLocs && len(value.locs) == 0 {
			return idx + 1
		}
	}
	return notAParam
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

// pointsAtZerologValue reports whether t points directly at a zerolog Logger,
// Event or Context, rather than at a pointer to one.
func pointsAtZerologValue(t types.Type) bool {
	pointer, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	_, deeper := types.Unalias(pointer.Elem()).(*types.Pointer)
	return !deeper && kindOf(pointer.Elem()) != kindOther
}

func callsRecover(fn *ssa.Function) bool {
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok {
				if builtin, ok := call.Call.Value.(*ssa.Builtin); ok && builtin.Name() == "recover" {
					return true
				}
			}
		}
	}
	return false
}

// recoverEntry is the state control resumes in after a recovered panic. The
// panic may happen at any instruction once a deferred call is registered, so
// nothing tracked survives it: every parameter location is unknown, and any
// write or escape some path made may have happened.
func recoverEntry(fn *ssa.Function, out map[*ssa.BasicBlock]*frame) *frame {
	entry := newFrame()
	for _, reached := range out {
		maps.Copy(entry.writes, reached.writes)
		maps.Copy(entry.escapedRoots, reached.escapedRoots)
		maps.Copy(entry.escapedEvents, reached.escapedEvents)
	}
	for _, param := range fn.Params {
		kind := trackedPointerKind(param.Type())
		if kind == kindOther {
			continue
		}
		if kind == kindEvent {
			entry.events[eventLocation{value: param}] = eventState{state: stateUnknown}
		}
		entry.memory[memoryLocation{root: param}] = unknownValue(kind)
	}
	return entry
}

func (e *engine) collectFindings(fn *ssa.Function, frames blockFrames) []sinkFinding {
	sink := &collector{}
	var later []pendingSink
	e.replay(fn, frames, sink, func(block *ssa.BasicBlock, index int, instruction ssa.Instruction, current *frame) {
		switch instruction.(type) {
		case *ssa.Defer, *ssa.Go:
			common := instruction.(ssa.CallInstruction).Common()
			if spec, receiver := e.sinkOf(common, current); spec.isSink() {
				later = append(later, pendingSink{
					sinkFinding: sinkFinding{pos: common.Pos(), state: stateUnreachable, spec: spec, nilCtx: true},
					common:      common, receiver: receiver, block: block, index: index,
				})
			}
		}
	})
	if len(later) != 0 {
		e.observeLaterSinks(fn, frames, later)
		for _, pending := range later {
			sink.findings = append(sink.findings, pending.sinkFinding)
		}
	}
	return sink.findings
}

// replay runs the transfer functions once more over every reachable block of
// a solved function, from its settled entry state, and hands visit the state
// after each instruction.
func (e *engine) replay(fn *ssa.Function, frames blockFrames, sink *collector, visit func(block *ssa.BasicBlock, index int, instruction ssa.Instruction, current *frame)) {
	scratch := newFrame()
	for _, block := range fn.Blocks {
		entry := frames.in[block]
		if entry == nil {
			continue
		}
		scratch.resetFrom(entry)
		for index, instruction := range block.Instrs {
			e.transfer(block, instruction, scratch, frames.out, sink)
			visit(block, index, instruction, scratch)
		}
	}
}

// pendingSink is a deferred or concurrent output operation, with its finding
// joined over every state it may run against so far. It runs later, against
// other states, but whether it is a sink is decided at its statement, where
// its receiver is known: only its context is read from those states.
type pendingSink struct {
	sinkFinding
	common   *ssa.CallCommon
	receiver ssa.Value
	block    *ssa.BasicBlock
	index    int
}

// observeLaterSinks judges each deferred or concurrent sink against every state
// from its statement onward. A goroutine may run at any of them, and a deferred
// call runs at any of them too, because a runtime panic or Goexit can happen
// anywhere, not only at an explicit exit. On every such path the function's
// other deferred calls may run first, so their effects are applied to each
// state before it is read — at the cost of a copy only where one of them
// reaches anything.
func (e *engine) observeLaterSinks(fn *ssa.Function, frames blockFrames, later []pendingSink) {
	sinks := make(map[*ssa.CallCommon]bool, len(later))
	after := make(map[*ssa.BasicBlock]map[*ssa.BasicBlock]bool)
	for _, pending := range later {
		sinks[pending.common] = true
		if after[pending.block] == nil {
			after[pending.block] = reachableFrom(pending.block.Succs)
		}
	}
	effects := e.laterEffects(fn, func(common *ssa.CallCommon) bool { return sinks[common] })
	var operands []ssa.Value
	for _, common := range effects {
		_, _, args, dispatched := resolvedCall(common)
		if dispatched != nil {
			operands = append(operands, dispatched)
		}
		operands = append(operands, args...)
	}
	observed := newFrame()
	e.replay(fn, frames, nil, func(block *ssa.BasicBlock, index int, _ ssa.Instruction, current *frame) {
		state := current
		for idx := range later {
			pending := &later[idx]
			if !after[pending.block][block] && (block != pending.block || index < pending.index) {
				continue
			}
			if state == current && slices.ContainsFunc(operands, func(operand ssa.Value) bool { return e.reaches(current, operand) }) {
				observed.resetFrom(current)
				e.invalidateCalls(observed, effects)
				state = observed
			}
			observedState, nilCtx := e.observe(pending.receiver, state)
			pending.state, pending.nilCtx = joinState(pending.state, observedState), pending.nilCtx && nilCtx
		}
	})
}

// reachableFrom returns every block reachable from the given ones, themselves
// included.
func reachableFrom(blocks []*ssa.BasicBlock) map[*ssa.BasicBlock]bool {
	reached := make(map[*ssa.BasicBlock]bool)
	stack := slices.Clone(blocks)
	for len(stack) > 0 {
		block := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reached[block] {
			continue
		}
		reached[block] = true
		stack = append(stack, block.Succs...)
	}
	return reached
}

// laterCalls caches the calls a function makes outside its straight-line flow:
// defers, which run when it exits, and goroutines, which run at an unknown
// moment. The list is consulted at every returning block of every solve round
// and at every RunDefers; rescanning every instruction each time would be
// quadratic in the size of the function.
func (e *engine) laterCalls(fn *ssa.Function) []ssa.CallInstruction {
	if cached, ok := e.later[fn]; ok {
		return cached
	}
	var later []ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.Defer:
				later = append(later, instruction)
			case *ssa.Go:
				later = append(later, instruction)
			}
		}
	}
	e.later[fn] = later
	return later
}

// boundMethodCalledInPlace reports whether the closure is a method value that
// never leaves this function, so that every call of it is folded back into an
// ordinary call at the site.
func boundMethodCalledInPlace(closure *ssa.MakeClosure) bool {
	target, ok := closure.Fn.(*ssa.Function)
	if !ok || !strings.HasPrefix(target.Synthetic, "bound method wrapper") {
		return false
	}
	referrers := closure.Referrers()
	if referrers == nil {
		return false
	}
	for _, referrer := range *referrers {
		call, ok := referrer.(ssa.CallInstruction)
		if !ok || call.Common().Value != closure {
			return false
		}
	}
	return true
}

// remember records what is known about an SSA register, but only when it says
// something a plain lookup would not. A frame is copied once per block visit,
// so entries that read back identically are pure weight: in generated code a
// single function can hold thousands of conversions and phis over types the
// analyzer does not track at all.
func remember(current *frame, value ssa.Value, known abstractValue) {
	if _, derived := untrackedAddress(value); derived {
		delete(current.values, value)
		return
	}
	if known.kind == kindOther && len(known.elems) == 0 && len(known.locs) == 0 && len(known.memLocs) == 0 {
		delete(current.values, value)
		return
	}
	current.values[value] = known
}

func (e *engine) transfer(block *ssa.BasicBlock, instruction ssa.Instruction, current *frame, predecessorOut map[*ssa.BasicBlock]*frame, sink *collector) {
	switch instruction := instruction.(type) {
	case *ssa.Store:
		location, ok := localLocation(instruction.Addr)
		if !ok {
			// The destination is not rooted at a local or a tracked pointer
			// parameter: the write is real, it may land in any escaped
			// location, and it carries the stored value somewhere the
			// analyzer cannot follow, so both ends leave its sight.
			forgetAliased(current)
			e.invalidateEscape(current, instruction.Addr)
			e.invalidateEscape(current, instruction.Val)
			return
		}
		stored := e.value(current, instruction.Val)
		if memoryEscaped(current, location) {
			// A parameter's target may be another parameter's, so writing it
			// may write every escaped location; and whatever lands in an
			// escaped location is reachable through the alias that reaches it.
			if _, parameter := location.root.(*ssa.Parameter); parameter {
				forgetAliased(current)
			}
			markReachable(current, stored)
		}
		storeValue(current, location, stored)
		if parameter, ok := location.root.(*ssa.Parameter); ok && location.path == "" && trackedPointerKind(parameter.Type()) == kindEvent && pointsAtZerologValue(parameter.Type()) {
			writeEvent(current, eventLocation{value: parameter}, eventState{
				state: stored.effectiveState(current), nilCtx: stored.finalCtxWasNil(current),
			})
		}
	case *ssa.UnOp:
		if instruction.Op == token.MUL {
			if location, ok := localLocation(instruction.X); ok {
				value, ok := current.memory[location]
				if !ok {
					value = loadAggregate(current, location, instruction.Type())
				}
				remember(current, instruction, value)
			}
		}
	case *ssa.Phi:
		value := abstractValue{}
		for idx, edge := range instruction.Edges {
			if predecessorOut[block.Preds[idx]] != nil {
				value = joinValue(value, e.value(predecessorOut[block.Preds[idx]], edge))
			}
		}
		if value.kind == kindOther && kindOf(instruction.Type()) != kindOther {
			value = unknownValue(kindOf(instruction.Type()))
		}
		remember(current, instruction, value)
	case *ssa.ChangeType:
		remember(current, instruction, e.value(current, instruction.X))
	case *ssa.Convert:
		remember(current, instruction, e.value(current, instruction.X))
	case *ssa.ChangeInterface:
		remember(current, instruction, e.value(current, instruction.X))
	case *ssa.MakeInterface:
		remember(current, instruction, e.value(current, instruction.X))
	case *ssa.TypeAssert:
		asserted := e.value(current, instruction.X)
		if len(asserted.elems) != 0 {
			// The operand is an aggregate, so it is not the asserted value.
			asserted = unknownValue(kindOf(instruction.AssertedType))
		}
		if instruction.CommaOk {
			remember(current, instruction, abstractValue{elems: []abstractValue{asserted, topValue}})
			return
		}
		remember(current, instruction, asserted)
	case *ssa.Extract:
		tuple := e.value(current, instruction.Tuple)
		if instruction.Index < len(tuple.elems) {
			remember(current, instruction, tuple.elems[instruction.Index])
		} else if kindOf(instruction.Type()) != kindOther {
			remember(current, instruction, unknownValue(kindOf(instruction.Type())))
		}
	case *ssa.Field:
		aggregate := e.value(current, instruction.X)
		if instruction.Field < len(aggregate.elems) {
			remember(current, instruction, aggregate.elems[instruction.Field])
		} else if kindOf(instruction.Type()) != kindOther {
			remember(current, instruction, unknownValue(kindOf(instruction.Type())))
		}
	case *ssa.Call:
		e.handleCall(instruction, instruction.Common(), current, sink)
	case *ssa.MakeClosure:
		// A closure's effects on its captures are not summarised — the body is
		// summarised over its parameters, and a free variable has no parameter
		// slot — so a capture escapes where it is taken: it is widened here,
		// and again by every later call, since any of them may run the
		// closure. A method value whose every use here is a call of it is the
		// one exception: resolvedCall folds its bound receiver back into the
		// argument list, so those calls are analysed exactly.
		if !boundMethodCalledInPlace(instruction) {
			for _, binding := range instruction.Bindings {
				e.invalidateEscape(current, binding)
			}
		}
	case *ssa.Defer:
		// The receiver and arguments are already SSA values captured here, but
		// the call's effects happen only when the function exits.
	case *ssa.MapUpdate:
		e.invalidateEscape(current, instruction.Key)
		e.invalidateEscape(current, instruction.Value)
	case *ssa.Send:
		e.invalidateEscape(current, instruction.X)
	case *ssa.Select:
		for _, state := range instruction.States {
			if state.Dir == types.SendOnly {
				e.invalidateEscape(current, state.Send)
			}
		}
	case *ssa.RunDefers:
		e.invalidateLaterEffects(block.Parent(), current)
	case *ssa.Go:
		// A goroutine runs at an unknown moment: it establishes nothing here,
		// and a sink it carries is judged by observeLaterSinks against every
		// later state, next to deferred sinks.
		if !e.isSinkCall(instruction.Common(), current) {
			e.invalidateCallArguments(instruction.Common(), current)
		}
	}
}

// sinkAt is the single place a resolved call is judged to be a zerolog output.
// It returns the receiver whose provenance must be proven, or nil when the sink
// is a package-level log function that has no receiver to prove.
func (e *engine) sinkAt(fn *types.Func, args []ssa.Value, invokeReceiver ssa.Value, current *frame) (sinkSpec, ssa.Value) {
	if spec := classifySink(fn); spec.isSink() {
		if inZerologLogPackage(fn) {
			return spec, nil
		}
		// classifySink keys on the receiver's zerolog kind, and no interface
		// type has one, so a match here is never an invoke: a static call
		// carries its receiver as the first argument.
		return spec, args[0]
	}
	if invokeReceiver == nil || fn == nil {
		return sinkSpec{}, nil
	}
	if spec := sinkForKind(e.value(current, invokeReceiver).kind, fn.Name()); spec.isSink() {
		return spec, invokeReceiver
	}
	return sinkSpec{}, nil
}

// sinkOf resolves the call and judges it with sinkAt.
func (e *engine) sinkOf(common *ssa.CallCommon, current *frame) (sinkSpec, ssa.Value) {
	fn, _, args, invokeReceiver := resolvedCall(common)
	return e.sinkAt(fn, args, invokeReceiver, current)
}

func (e *engine) isSinkCall(common *ssa.CallCommon, current *frame) bool {
	spec, _ := e.sinkOf(common, current)
	return spec.isSink()
}

func (e *engine) withLaterEffects(fn *ssa.Function, current *frame) *frame {
	terminal := current.clone()
	e.invalidateLaterEffects(fn, terminal)
	return terminal
}

func (e *engine) invalidateLaterEffects(fn *ssa.Function, current *frame) {
	e.invalidateCalls(current, e.laterEffects(fn, func(common *ssa.CallCommon) bool { return e.isSinkCall(common, current) }))
}

// laterEffects is every deferred or concurrent call of fn that is not a sink:
// the calls whose effects happen after their statement.
func (e *engine) laterEffects(fn *ssa.Function, isSink func(*ssa.CallCommon) bool) []*ssa.CallCommon {
	var effects []*ssa.CallCommon
	for _, later := range e.laterCalls(fn) {
		if !isSink(later.Common()) {
			effects = append(effects, later.Common())
		}
	}
	return effects
}

func (e *engine) invalidateCalls(current *frame, calls []*ssa.CallCommon) {
	for _, common := range calls {
		e.invalidateCallArguments(common, current)
	}
}

// reaches reports whether invalidating value could change anything in current:
// whether it reaches an event, a memory location or an aggregate. e.value
// gives every local address its location, so this mirrors the routes
// invalidateEscape follows.
func (e *engine) reaches(current *frame, value ssa.Value) bool {
	known := e.value(current, value)
	return len(known.locs) != 0 || len(known.memLocs) != 0 || len(known.elems) != 0
}

// invalidateCallArguments widens everything a callee could reach through the
// call: every escaped location, and everything reachable from its arguments,
// the receiver or the function value it dispatches on.
func (e *engine) invalidateCallArguments(common *ssa.CallCommon, current *frame) {
	forgetEscaped(current)
	_, _, args, invokeReceiver := resolvedCall(common)
	if invokeReceiver != nil {
		e.invalidateEscape(current, invokeReceiver)
	}
	for _, arg := range args {
		e.invalidateEscape(current, arg)
	}
}

// invalidateEscape widens every state reachable through value, because the
// code it escapes to may overwrite any of it, and marks all of it escaped,
// because that code may keep an alias and write through it later.
//
// An abstract value reaches state four ways and all four have to be followed:
// a syntactic address, the event identities in locs, the memory locations in
// memLocs, and everything nested inside an aggregate. Following fewer of them
// silently preserves a proof the escape destroyed.
func (e *engine) invalidateEscape(current *frame, value ssa.Value) {
	if location, ok := localLocation(value); ok {
		forgetMemory(current, location)
	}
	forgetReachable(current, e.value(current, value))
}

func forgetReachable(current *frame, value abstractValue) {
	for location := range value.locs {
		current.escapedEvents[location] = struct{}{}
		forgetEvent(current, location)
	}
	for location := range value.memLocs {
		forgetMemory(current, location)
	}
	for _, element := range value.elems {
		forgetReachable(current, element)
	}
}

// writeEvent records everything known about one event and notes that a
// parameter-rooted event was mutated, which is what makes the effect visible
// to callers.
func writeEvent(current *frame, location eventLocation, state eventState) {
	current.events[location] = state
	markParamWrite(current, location.value)
}

// markParamWrite notes that state rooted at a parameter changed, which is what
// makes the change visible to callers as a parameter effect.
func markParamWrite(current *frame, root ssa.Value) {
	if parameter, ok := root.(*ssa.Parameter); ok {
		current.writes[parameter] = true
	}
}

// forgetEvent widens one event. It records no parameter write: a widening is
// either an escape, which the summary reports as one, or a write through an
// alias the caller already accounts for.
func forgetEvent(current *frame, location eventLocation) {
	current.events[location] = eventState{state: stateUnknown}
}

// forgetEscaped widens every location that escaped from this function. It
// runs wherever code the analyzer does not follow runs: that code may reach
// any of them through an alias it never saw. A parameter's target is not among
// them. An alias of an argument is the caller's, and code this function calls
// is assumed to reach an argument only through what it is passed - the same
// assumption every summary makes.
func forgetEscaped(current *frame) {
	widenEvents(current, eventEscapedHere)
	widenMemoryWhere(current, escapedHere)
}

// forgetAliased widens every location a write this function makes through a
// pointer it cannot resolve may land on: everything escaped, and every
// parameter's target, since the caller may have passed the same object twice
// or one this pointer also reaches.
func forgetAliased(current *frame) {
	widenEvents(current, eventEscaped)
	widenMemoryWhere(current, memoryEscaped)
}

// widenEvents and widenMemoryWhere widen what the predicate selects. Escaping
// widened a location already, so one that still holds an unknown has nothing
// left to lose and is skipped: forgetting runs at nearly every call, and
// almost always finds nothing new.
func widenEvents(current *frame, selected func(*frame, eventLocation) bool) {
	for location, state := range current.events {
		if selected(current, location) && state != (eventState{state: stateUnknown}) {
			forgetEvent(current, location)
		}
	}
}

func widenMemoryWhere(current *frame, selected func(*frame, memoryLocation) bool) {
	for location, stored := range current.memory {
		if selected(current, location) && !equalValue(stored, unknownValue(stored.kind)) {
			widenMemory(current, location)
		}
	}
}

// escapedHere reports whether location escaped from this function.
func escapedHere(current *frame, location memoryLocation) bool {
	_, ok := current.escapedRoots[location.root]
	return ok
}

func eventEscapedHere(current *frame, location eventLocation) bool {
	_, ok := current.escapedEvents[location]
	return ok
}

// memoryEscaped reports whether a write through an alias the analyzer never
// saw may reach location: one that escaped from this function, or a
// parameter's target.
func memoryEscaped(current *frame, location memoryLocation) bool {
	_, parameter := location.root.(*ssa.Parameter)
	return parameter || escapedHere(current, location)
}

// eventEscaped is memoryEscaped for an event.
func eventEscaped(current *frame, location eventLocation) bool {
	_, parameter := location.value.(*ssa.Parameter)
	return parameter || eventEscapedHere(current, location)
}

// markReachable marks escaped everything value reaches, without widening it:
// a store into an escaped location makes the stored value reachable through
// the same unseen alias, but changes nothing it holds.
func markReachable(current *frame, value abstractValue) {
	for location := range value.locs {
		current.escapedEvents[location] = struct{}{}
	}
	for location := range value.memLocs {
		if _, marked := current.escapedRoots[location.root]; marked {
			continue
		}
		current.escapedRoots[location.root] = struct{}{}
		for nested, stored := range current.memory {
			if nested.startsAt(location) && !holdsZerologValue(nested) {
				markReachable(current, stored)
			}
		}
	}
	for _, element := range value.elems {
		markReachable(current, element)
	}
}

// updateEvent writes a proven postcondition when the value denotes exactly one
// event, and widens every candidate otherwise: a may-alias set carries no
// postcondition. A write through a value that may denote an untracked event,
// or into an escaped one, may change an escaped event some untracked alias
// denotes, so every escaped event is widened first.
func updateEvent(current *frame, value abstractValue, state eventState) {
	location, must := value.mustEvent()
	if !must || eventEscaped(current, location) {
		widenEvents(current, eventEscaped)
	}
	if must {
		writeEvent(current, location, state)
		return
	}
	for location := range value.locs {
		forgetEvent(current, location)
		markParamWrite(current, location.value)
	}
}

// writeMemory records a value at a location and notes a parameter-rooted write.
func writeMemory(current *frame, location memoryLocation, value abstractValue) {
	current.memory[location] = value
	markParamWrite(current, location.root)
}

// forgetMemory widens a location, every field nested inside it, and everything
// the values stored there could reach, and marks all of it escaped. Each
// location is widened before its contents are followed, which is what
// terminates the walk: a location reached again already holds an unknown that
// reaches nothing.
func forgetMemory(current *frame, root memoryLocation) {
	current.escapedRoots[root.root] = struct{}{}
	widenMemory(current, root)
}

// widenMemory widens a location, every field nested inside it, and everything
// the values stored there could reach, which escapes with it. Like
// forgetEvent it records no parameter write.
func widenMemory(current *frame, root memoryLocation) {
	for location, stored := range current.memory {
		if !location.startsAt(root) || holdsEvent(location) {
			continue
		}
		current.memory[location] = unknownValue(stored.kind)
		if !holdsZerologValue(location) {
			forgetReachable(current, stored)
		}
	}
}

// holdsEvent reports whether location is an event pointer's target. What it
// holds is the event's identity, which no write through an alias changes: the
// event's context lives in frame.events and is widened there.
func holdsEvent(location memoryLocation) bool {
	return holdsZerologValue(location) && kindOf(location.root.Type()) == kindEvent
}

// holdsZerologValue reports whether location is the target of a pointer
// straight at a zerolog value. What it holds is that value itself - for an
// event parameter, the event's own identity - and reaches nothing else.
func holdsZerologValue(location memoryLocation) bool {
	return location.path == "" && pointsAtZerologValue(location.root.Type())
}

func storeValue(current *frame, location memoryLocation, value abstractValue) {
	// Overwriting an aggregate discards whatever was proven about its fields.
	// Deleting them is safe: an absent location reads back as unknown.
	for stale := range current.memory {
		if stale != location && stale.startsAt(location) {
			delete(current.memory, stale)
		}
	}
	writeMemory(current, location, value)
	for idx, element := range value.elems {
		fieldLocation := location
		fieldLocation.path = fieldPath(fieldLocation.path, idx)
		storeValue(current, fieldLocation, element)
	}
}

type memoryLocation struct {
	root ssa.Value
	path string
}

// startsAt reports whether l is root itself or a field nested inside it.
// fieldPath separates components with ".", so comparing raw prefixes would
// make field 10 look like a child of field 1.
func (l memoryLocation) startsAt(root memoryLocation) bool {
	if l.root != root.root {
		return false
	}
	return l.path == root.path || strings.HasPrefix(l.path, root.path+".")
}

type eventLocation struct {
	value ssa.Value
	index int
}

func localLocation(value ssa.Value) (memoryLocation, bool) {
	switch value := value.(type) {
	case *ssa.Alloc:
		return memoryLocation{root: value}, true
	case *ssa.Parameter:
		if trackedPointerKind(value.Type()) != kindOther {
			return memoryLocation{root: value}, true
		}
		return memoryLocation{}, false
	case *ssa.FieldAddr:
		location, ok := localLocation(value.X)
		if !ok {
			return memoryLocation{}, false
		}
		location.path = fieldPath(location.path, value.Field)
		return location, true
	case *ssa.ChangeType:
		return localLocation(value.X)
	case *ssa.Convert:
		return localLocation(value.X)
	default:
		return memoryLocation{}, false
	}
}

func trackedPointerKind(t types.Type) valueKind {
	if _, ok := types.Unalias(t).(*types.Pointer); !ok {
		return kindOther
	}
	return kindOf(t)
}

// loadAggregate reads a location nothing was stored at whole. A zerolog value
// is a struct too, but the analyzer tracks it as one value, never as fields.
func loadAggregate(current *frame, location memoryLocation, t types.Type) abstractValue {
	structure, ok := deref(t).Underlying().(*types.Struct)
	if !ok || kindOf(t) != kindOther {
		return unknownValue(kindOf(t))
	}
	value := abstractValue{elems: make([]abstractValue, structure.NumFields())}
	for idx := 0; idx < structure.NumFields(); idx++ {
		fieldLocation := location
		fieldLocation.path = fieldPath(fieldLocation.path, idx)
		field, ok := current.memory[fieldLocation]
		if !ok {
			field = unknownValue(kindOf(structure.Field(idx).Type()))
		}
		value.elems[idx] = field
	}
	return value
}

func fieldPath(parent string, field int) string {
	return parent + "." + strconv.Itoa(field)
}

// resolvedCall returns the callee object, its SSA body when statically known,
// the call arguments, and the value dispatched on: the receiver of an
// interface invoke, or the function value of a dynamic call.
//
// The returned args are not uniformly aligned with the callee's parameters: an
// invoke strips the receiver into the fourth result, a synthetic closure has
// its bindings prepended, and a static call keeps the receiver at args[0].
// Anything indexing a summary by parameter position must account for that.
func resolvedCall(common *ssa.CallCommon) (*types.Func, *ssa.Function, []ssa.Value, ssa.Value) {
	if common.IsInvoke() {
		return common.Method, nil, common.Args, common.Value
	}
	callee := common.StaticCallee()
	if callee == nil {
		return nil, nil, common.Args, common.Value
	}
	object, _ := callee.Object().(*types.Func)
	args := common.Args
	if closure, ok := common.Value.(*ssa.MakeClosure); ok && callee.Synthetic != "" {
		args = slices.Concat(closure.Bindings, args)
	}
	return object, callee, args, nil
}

// handleCall applies one call. result is the call instruction itself; a call
// with no results has an empty tuple type, which binds nothing.
func (e *engine) handleCall(result ssa.Value, common *ssa.CallCommon, current *frame, sink *collector) {
	fn, callee, args, invokeReceiver := resolvedCall(common)
	if spec, receiver := e.sinkAt(fn, args, invokeReceiver, current); spec.isSink() {
		if sink != nil {
			state, nilCtx := e.observe(receiver, current)
			sink.findings = append(sink.findings, sinkFinding{pos: common.Pos(), state: state, spec: spec, nilCtx: nilCtx})
		}
		return
	}
	// Summaries describe only zerolog-typed parameters. Anything else the
	// callee receives may be written through or kept, so it escapes whatever
	// the callee is.
	for _, arg := range args {
		if kindOf(arg.Type()) == kindOther {
			e.invalidateEscape(current, arg)
		}
	}

	// Only zerolog's own code has known semantics. A zerolog interface method
	// dispatches to whatever implements it, which is not zerolog's code.
	if callee != nil && fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == zerologPkgPath {
		e.handleZerologCall(result, fn, args, current)
		return
	}
	// A summary describes what the callee does through its parameters, not
	// what it does through an alias the caller let escape.
	forgetEscaped(current)
	if summary, ok := e.summaryOf(fn, callee); ok {
		e.applySummary(result, args, summary, current)
		return
	}

	e.invalidateCallArguments(common, current)
	e.bindResult(current, result, func(int) ctxState { return stateUnknown })
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

func (e *engine) handleZerologCall(result ssa.Value, fn *types.Func, args []ssa.Value, current *frame) {
	declaredReceiver := receiverType(fn)
	var receiver abstractValue
	if declaredReceiver != nil && len(args) > 0 {
		receiver = e.value(current, args[0])
	}
	resultKind := kindOf(result.Type())

	switch kindOf(declaredReceiver) {
	case kindEvent:
		e.handleEventCall(result, fn.Name(), receiver, args, current)
		return
	case kindBuilder:
		state, nilCtx := receiver.effectiveState(current), receiver.finalCtxWasNil(current)
		if fn.Name() == "Ctx" {
			state, nilCtx = ctxArgState(args)
		}
		if resultKind != kindOther {
			current.values[result] = abstractValue{kind: resultKind, state: state, nilCtx: nilCtx}
		}
		return
	case kindLogger:
		// UpdateContext copies only the fields its callback adds back into the
		// logger, never the callback's context. The callback itself is user
		// code, which may write any escaped location.
		if fn.Name() == "UpdateContext" {
			forgetEscaped(current)
			return
		}
		state, nilCtx := receiver.effectiveState(current), receiver.finalCtxWasNil(current)
		if fn.Name() == "Output" {
			// Output rebuilds the logger with New and does not copy its context.
			state, nilCtx = stateNoContext, false
		}
		switch resultKind {
		case kindEvent:
			current.values[result] = e.freshEvent(current, eventLocation{value: result}, eventState{state: state, nilCtx: nilCtx})
		case kindBuilder, kindLogger:
			current.values[result] = abstractValue{kind: resultKind, state: state, nilCtx: nilCtx}
		}
		return
	}

	state := stateUnknown
	if fn.Name() == "New" || fn.Name() == "Nop" {
		state = stateNoContext
	}
	e.bindResult(current, result, func(int) ctxState { return state })
}

// handleEventCall applies a zerolog Event method. Every one of them returns its
// receiver except CreateDict, which returns a new event seeded with the
// receiver's context. Ctx sets the context; Func, Object and EmbedObject hand
// the receiver itself to user code, whose summary decides what it does.
func (e *engine) handleEventCall(result ssa.Value, name string, receiver abstractValue, args []ssa.Value, current *frame) {
	switch name {
	case "Ctx":
		state, nilCtx := ctxArgState(args)
		e.mutateEvent(current, result, receiver, eventState{state: state, nilCtx: nilCtx})
		return
	case "Func":
		if len(args) > 1 && !isNilValue(args[1]) {
			summary, ok := e.summaryForFunctionValue(args[1])
			e.applyCallback(current, result, args[0], summary, ok, boundParams(args[1]))
			return
		}
	case "Object", "EmbedObject":
		if marshaler := args[len(args)-1]; !isNilValue(marshaler) {
			summary, ok := e.summaryOf(marshalerMethod(marshaler))
			e.applyCallback(current, result, args[0], summary, ok, 1)
			return
		}
	case "CreateDict":
		current.values[result] = e.freshEvent(current, eventLocation{value: result}, eventState{
			state: receiver.effectiveState(current), nilCtx: receiver.finalCtxWasNil(current),
		})
		return
	}
	if kindOf(result.Type()) == kindEvent {
		current.values[result] = receiver
	}
}

// applyCallback applies what user code zerolog hands the receiver event to
// does to it, from that code's summary at the event's parameter position. The
// code runs like any call: it may write every escaped location, and without a
// summary it may keep the event.
func (e *engine) applyCallback(current *frame, result ssa.Value, receiverArg ssa.Value, summary functionSummary, ok bool, param int) {
	forgetEscaped(current)
	receiver := e.value(current, receiverArg)
	switch {
	case !ok || param >= len(summary.ParamEffects) || summary.ParamEscapes[param]:
		e.invalidateEscape(current, receiverArg)
		e.mutateEvent(current, result, e.value(current, receiverArg), eventState{state: stateUnknown})
	case summary.ParamEffects[param] == stateUnreachable:
		current.values[result] = receiver
	default:
		// As in applySummary, the effect is not written into an event that
		// escaped here: code the callback ran may have reached it that way.
		state := eventState{state: summary.ParamEffects[param]}
		if location, must := receiver.mustEvent(); must && eventEscapedHere(current, location) {
			state = eventState{state: stateUnknown}
		}
		e.mutateEvent(current, result, receiver, state)
	}
}

// mutateEvent writes a postcondition into the receiver of a zerolog call that
// returns it. A receiver the analyzer has no identity for came through an
// alias it does not track, so the result gets a fresh identity of its own
// that is escaped from the start: that alias still exists.
func (e *engine) mutateEvent(current *frame, result ssa.Value, receiver abstractValue, state eventState) {
	updateEvent(current, receiver, state)
	if len(receiver.locs) == 0 {
		location := eventLocation{value: result}
		current.values[result] = e.freshEvent(current, location, state)
		current.escapedEvents[location] = struct{}{}
		return
	}
	current.values[result] = receiver
}

// marshalerMethod returns the MarshalZerologObject method a marshaler argument
// dispatches to, when its dynamic type is visible at the call.
func marshalerMethod(value ssa.Value) (*types.Func, *ssa.Function) {
	boxed, ok := value.(*ssa.MakeInterface)
	if !ok {
		return nil, nil
	}
	program := boxed.Parent().Prog
	selection := program.MethodSets.MethodSet(boxed.X.Type()).Lookup(nil, "MarshalZerologObject")
	if selection == nil {
		return nil, nil
	}
	object, _ := selection.Obj().(*types.Func)
	return object, program.FuncValue(object)
}

// ctxArgState is what a Ctx call proves, and whether its argument was nil: a
// missing or nil argument proves there is no context.
func ctxArgState(args []ssa.Value) (ctxState, bool) {
	if len(args) < 2 || isNilValue(args[1]) {
		return stateNoContext, true
	}
	return stateHasContext, false
}

// boundParams is how many of a function value's summarised parameters are
// already bound, so that its first argument is the parameter at that position:
// the receiver of a bound method value.
func boundParams(value ssa.Value) int {
	if closure, ok := value.(*ssa.MakeClosure); ok {
		if fn, ok := closure.Fn.(*ssa.Function); ok && fn.Synthetic != "" {
			return len(closure.Bindings)
		}
	}
	return 0
}

func (e *engine) summaryForFunctionValue(value ssa.Value) (functionSummary, bool) {
	fn := functionValue(value)
	if fn == nil {
		return functionSummary{}, false
	}
	object, _ := fn.Object().(*types.Func)
	return e.summaryOf(object, fn)
}

// applySummary writes a callee's parameter effects into the caller. An effect
// is a postcondition, so it is written only into the one location the
// argument certainly denotes; every other written or escaping argument is
// widened. The callee widened its parameters at every write of its own through
// an alias, but not at the calls it makes, so an effect is not written into a
// location that escaped here either: code the callee ran may have reached it
// through that escape.
func (e *engine) applySummary(result ssa.Value, args []ssa.Value, summary functionSummary, current *frame) {
	for idx, effect := range summary.ParamEffects {
		if idx >= len(args) || (effect == stateUnreachable && !summary.ParamEscapes[idx]) {
			continue
		}
		value := e.value(current, args[idx])
		// The effect describes what the parameter's target holds. Through a
		// pointer to a pointer that target is a new object, not the one the
		// caller's value denotes, so only a direct pointer receives it.
		if !summary.ParamEscapes[idx] && pointsAtZerologValue(args[idx].Type()) {
			switch value.kind {
			case kindEvent:
				if location, ok := value.mustEvent(); ok && !eventEscapedHere(current, location) {
					writeEvent(current, location, eventState{state: effect})
					continue
				}
			case kindLogger, kindBuilder:
				if location, ok := localLocation(args[idx]); ok && !escapedHere(current, location) {
					writeMemory(current, location, abstractValue{kind: value.kind, state: effect})
					continue
				}
			}
		}
		e.invalidateEscape(current, args[idx])
	}
	e.bindResult(current, result, func(idx int) ctxState {
		if idx < len(summary.Results) {
			return summary.Results[idx]
		}
		return stateUnknown
	})
	// A result that is one of the arguments is bound to it. Any other result
	// may reach an argument the callee let escape, so it escapes too.
	bound := current.values[result]
	for idx, param := range summary.ResultParams {
		switch {
		case param > 0 && param <= len(args):
			argument := e.value(current, args[param-1])
			if len(bound.elems) != 0 {
				bound.elems[idx] = argument
			} else {
				bound = argument
			}
		case slices.Contains(summary.ParamEscapes, true):
			if len(bound.elems) != 0 {
				forgetReachable(current, bound.elems[idx])
			} else {
				forgetReachable(current, bound)
			}
		}
	}
	remember(current, result, bound)
}

// bindResult gives a call's result, or each element of its tuple, the context
// state stateAt proves for that position. An event result is a new event
// object, identified by the call and the position; any other tracked result
// has no identity of its own.
func (e *engine) bindResult(current *frame, result ssa.Value, stateAt func(idx int) ctxState) {
	bind := func(t types.Type, idx int) abstractValue {
		switch kind := kindOf(t); kind {
		case kindEvent:
			return e.freshEvent(current, eventLocation{value: result, index: idx}, eventState{state: stateAt(idx)})
		case kindOther:
			return topValue
		default:
			value := unknownValue(kind)
			value.state = stateAt(idx)
			return value
		}
	}
	if tuple, ok := result.Type().(*types.Tuple); ok {
		value := abstractValue{elems: make([]abstractValue, tuple.Len())}
		for idx := range tuple.Len() {
			value.elems[idx] = bind(tuple.At(idx).Type(), idx)
		}
		current.values[result] = value
		return
	}
	if kindOf(result.Type()) != kindOther {
		current.values[result] = bind(result.Type(), 0)
	}
}

func isNilValue(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	return ok && constant.IsNil()
}
