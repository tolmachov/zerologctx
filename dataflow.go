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
// A non-empty elems makes the value an aggregate, and kind and state are then
// unused.
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

// freshEvent creates the one event object a location denotes and seeds what
// is known about it. The value is a complete singleton, so it is a must-alias
// and may receive postconditions. Its own state stays at the bottom: an event
// with locs keeps its context in frame.events. Seeding is not a write, so no
// parameter write is recorded.
func freshEvent(current *frame, location eventLocation, state eventState) abstractValue {
	current.events[location] = state
	return abstractValue{kind: kindEvent, locs: map[eventLocation]struct{}{location: {}}}
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
// may-fact joined at a merge; ctxWasNil is a must-fact intersected at a merge,
// because a nil final Ctx may only be reported when every path passed nil.
// They live in one cell so that no writer can update one and forget the other.
type eventState struct {
	state     ctxState
	ctxWasNil bool
}

func joinEventState(a, b eventState) eventState {
	return eventState{state: joinState(a.state, b.state), ctxWasNil: a.ctxWasNil && b.ctxWasNil}
}

// frame is the dataflow state at one program point.
//
// values is keyed by SSA value and needs no widening at a merge because a
// value dominates its uses; memory is keyed by location and does need it,
// because a location absent from one predecessor is unknown, not unchanged.
type frame struct {
	values map[ssa.Value]abstractValue
	memory map[memoryLocation]abstractValue
	events map[eventLocation]eventState
	writes map[*ssa.Parameter]bool
}

func newFrame() *frame {
	return &frame{
		values: make(map[ssa.Value]abstractValue),
		memory: make(map[memoryLocation]abstractValue),
		events: make(map[eventLocation]eventState),
		writes: make(map[*ssa.Parameter]bool),
	}
}

// clone copies each map whole rather than re-inserting entry by entry. Values
// are copied shallowly: their locs and memLocs are shared, which is why
// abstractValue forbids mutating them.
func (f *frame) clone() *frame {
	return &frame{
		values: maps.Clone(f.values),
		memory: maps.Clone(f.memory),
		events: maps.Clone(f.events),
		writes: maps.Clone(f.writes),
	}
}

// resetFrom refills the frame from src, reusing the maps it already allocated.
// A block is re-entered many times before the worklist settles and almost
// every visit ends up changing nothing, so allocating a frame per visit was
// the analyzer's single largest source of garbage.
func (f *frame) resetFrom(src *frame) {
	clear(f.values)
	clear(f.memory)
	clear(f.events)
	clear(f.writes)
	maps.Copy(f.values, src.values)
	maps.Copy(f.memory, src.memory)
	maps.Copy(f.events, src.events)
	maps.Copy(f.writes, src.writes)
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
	return changed
}

func framesEqual(a, b *frame) bool {
	return maps.EqualFunc(a.values, b.values, equalValue) && maps.EqualFunc(a.memory, b.memory, equalValue) &&
		maps.Equal(a.events, b.events) && maps.Equal(a.writes, b.writes)
}

type functionSummary struct {
	results []ctxState
	effects []ctxState // unreachable means preserve
}

func equalSummary(a, b functionSummary) bool {
	return slices.Equal(a.results, b.results) && slices.Equal(a.effects, b.effects)
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
// no sink.
func touchesZerolog(fn *ssa.Function) bool {
	if len(fn.Blocks) == 0 {
		return true
	}
	var operands []*ssa.Value
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok {
				if callee := call.Common().StaticCallee(); callee != nil {
					if object, _ := callee.Object().(*types.Func); isPackageLevelLogSink(object) {
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

func joinSummary(a, b functionSummary) functionSummary {
	merged := functionSummary{results: slices.Clone(a.results), effects: slices.Clone(a.effects)}
	for idx := range merged.results {
		merged.results[idx] = joinState(merged.results[idx], b.results[idx])
	}
	for idx := range merged.effects {
		merged.effects[idx] = joinState(merged.effects[idx], b.effects[idx])
	}
	return merged
}

// unknownSummary claims nothing about a function: no result and no parameter
// effect is proven, so every caller falls through to reporting.
func unknownSummary(fn *ssa.Function) functionSummary {
	summary := emptySummary(fn)
	for idx := range summary.results {
		summary.results[idx] = stateUnknown
	}
	for idx := range summary.effects {
		summary.effects[idx] = stateUnknown
	}
	return summary
}

func emptySummary(fn *ssa.Function) functionSummary {
	return functionSummary{
		results: make([]ctxState, fn.Signature.Results().Len()),
		effects: make([]ctxState, len(fn.Params)),
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
		e.pass.ExportObjectFact(obj, &functionSummaryFact{
			Results:      slices.Clone(summary.results),
			ParamEffects: slices.Clone(summary.effects),
		})
	}
}

func (s functionSummary) hasProof() bool {
	for _, state := range s.results {
		if state == stateHasContext || state == stateNoContext {
			return true
		}
	}
	for _, state := range s.effects {
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

func (v abstractValue) latticeState(f *frame) ctxState {
	if v.kind != kindEvent || len(v.locs) == 0 {
		return v.state
	}
	state := v.state
	for loc := range v.locs {
		state = joinState(state, f.events[loc].state)
	}
	return state
}

func (v abstractValue) finalCtxWasNil(f *frame) bool {
	if v.kind != kindEvent || len(v.locs) == 0 {
		return v.state == stateNoContext && v.nilCtx
	}
	if v.effectiveState(f) != stateNoContext {
		return false
	}
	for loc := range v.locs {
		if !f.events[loc].ctxWasNil {
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

	in := make(map[*ssa.BasicBlock]*frame)
	out := make(map[*ssa.BasicBlock]*frame)
	entry := newFrame()
	for _, param := range fn.Params {
		kind := kindOf(param.Type())
		if kind == kindOther {
			continue
		}
		value := unknownValue(kind)
		if kind == kindEvent {
			value = freshEvent(entry, eventLocation{value: param}, eventState{state: stateUnknown})
		}
		if trackedPointerKind(param.Type()) == kindOther {
			entry.values[param] = value
			continue
		}
		// A tracked pointer parameter is a memory location and e.value reads it
		// from there. Seeding it into values as well would freeze the entry
		// state and hide every later store.
		location := memoryLocation{root: param}
		value.memLocs = map[memoryLocation]struct{}{location: {}}
		entry.memory[location] = value
	}
	in[fn.Blocks[0]] = entry
	queue := []*ssa.BasicBlock{fn.Blocks[0]}
	queued := map[*ssa.BasicBlock]bool{fn.Blocks[0]: true}
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
	// Results and parameter effects are both read at the same moment — after
	// the deferred and concurrent calls have taken effect — so each returning
	// block's terminal frame is built once and both are derived from it.
	results := fn.Signature.Results()
	written := make([]bool, len(fn.Params))
	for _, block := range fn.Blocks {
		current := out[block]
		ret, returns := block.Instrs[len(block.Instrs)-1].(*ssa.Return)
		if current == nil || !returns {
			continue
		}
		terminal := e.withLaterEffects(fn, current)
		for idx, value := range ret.Results {
			if kindOf(results.At(idx).Type()) == kindOther {
				continue
			}
			summary.results[idx] = joinState(summary.results[idx], e.value(terminal, value).latticeState(terminal))
		}
		for idx, param := range fn.Params {
			kind := trackedPointerKind(param.Type())
			if kind == kindOther {
				continue
			}
			written[idx] = written[idx] || terminal.writes[param]
			if kind == kindEvent {
				summary.effects[idx] = joinState(summary.effects[idx], terminal.events[eventLocation{value: param}].state)
			} else {
				summary.effects[idx] = joinState(summary.effects[idx], terminal.memory[memoryLocation{root: param}].latticeState(terminal))
			}
		}
	}
	// A parameter no path wrote to is preserved, whatever it holds at exit.
	for idx := range fn.Params {
		if !written[idx] {
			summary.effects[idx] = stateUnreachable
		}
	}
	return summary, blockFrames{in: in, out: out}
}

func (e *engine) collectFindings(fn *ssa.Function, frames blockFrames) []sinkFinding {
	sink := &collector{}
	var later []pendingSink
	e.replay(fn, frames, sink, func(block *ssa.BasicBlock, index int, instruction ssa.Instruction, current *frame) {
		switch instruction.(type) {
		case *ssa.Defer, *ssa.Go:
			common := instruction.(ssa.CallInstruction).Common()
			if spec, receiver := e.sinkOf(common, current); spec.kind != sinkNone {
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
// and at every RunDefers, and rescanning every instruction each time made that
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
			// The destination is a phi, a global or an element: the write is
			// real, and it carries the stored value somewhere the analyzer
			// cannot follow, so both ends leave its sight.
			e.invalidateEscape(current, instruction.Addr)
			e.invalidateEscape(current, instruction.Val)
			return
		}
		stored := e.value(current, instruction.Val)
		storeValue(current, location, stored)
		if parameter, ok := location.root.(*ssa.Parameter); ok && trackedPointerKind(parameter.Type()) == kindEvent {
			writeEvent(current, eventLocation{value: parameter}, eventState{
				state: stored.effectiveState(current), ctxWasNil: stored.finalCtxWasNil(current),
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
		// slot — so a capture must be widened where it is taken. A method
		// value whose every use here is a call of it is the one exception:
		// resolvedCall folds its bound receiver back into the argument list,
		// so those calls are analysed exactly.
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
	if spec := classifySink(fn); spec.kind != sinkNone {
		if isPackageLevelLogSink(fn) {
			return spec, nil
		}
		// classifySink matches on a declared receiver type, which an interface
		// method never has, so this call is not a dispatch and carries its
		// receiver as the first argument.
		return spec, args[0]
	}
	if invokeReceiver == nil || fn == nil {
		return sinkSpec{}, nil
	}
	if spec := sinkForKind(e.value(current, invokeReceiver).kind, fn.Name()); spec.kind != sinkNone {
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
	return spec.kind != sinkNone
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
// whether it is an address or reaches an event, a memory location or an
// aggregate. It mirrors the routes invalidateEscape follows.
func (e *engine) reaches(current *frame, value ssa.Value) bool {
	if _, ok := localLocation(value); ok {
		return true
	}
	known := e.value(current, value)
	return len(known.locs) != 0 || len(known.memLocs) != 0 || len(known.elems) != 0
}

// invalidateCallArguments widens everything a callee could reach through the
// call, including the receiver or the function value it dispatches on.
func (e *engine) invalidateCallArguments(common *ssa.CallCommon, current *frame) {
	_, _, args, invokeReceiver := resolvedCall(common)
	if invokeReceiver != nil {
		e.invalidateEscape(current, invokeReceiver)
	}
	for _, arg := range args {
		e.invalidateEscape(current, arg)
	}
}

// invalidateEscape widens every state reachable through value, because the
// code it escapes to may overwrite any of it.
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

func forgetEvent(current *frame, location eventLocation) {
	writeEvent(current, location, eventState{state: stateUnknown})
}

// updateEvent writes a proven postcondition when the value denotes exactly one
// event, and widens every candidate otherwise: a may-alias set carries no
// postcondition.
func updateEvent(current *frame, value abstractValue, state eventState) {
	if location, ok := value.mustEvent(); ok {
		writeEvent(current, location, state)
		return
	}
	for location := range value.locs {
		forgetEvent(current, location)
	}
}

// writeMemory records a value at a location and notes a parameter-rooted write.
func writeMemory(current *frame, location memoryLocation, value abstractValue) {
	current.memory[location] = value
	markParamWrite(current, location.root)
}

// forgetMemory widens a location, every field nested inside it, and everything
// the values stored there could reach. Each location is widened before its
// contents are followed, which is what terminates the walk: a location reached
// again already holds an unknown that reaches nothing.
func forgetMemory(current *frame, root memoryLocation) {
	for location, stored := range current.memory {
		if !location.startsAt(root) {
			continue
		}
		current.memory[location] = unknownValue(stored.kind)
		forgetReachable(current, stored)
	}
	markParamWrite(current, root.root)
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

func loadAggregate(current *frame, location memoryLocation, t types.Type) abstractValue {
	structure, ok := deref(t).Underlying().(*types.Struct)
	if !ok {
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

func (e *engine) handleCall(result ssa.Value, common *ssa.CallCommon, current *frame, sink *collector) {
	fn, callee, args, invokeReceiver := resolvedCall(common)
	if spec, receiver := e.sinkAt(fn, args, invokeReceiver, current); spec.kind != sinkNone {
		if sink != nil {
			state, nilCtx := e.observe(receiver, current)
			sink.findings = append(sink.findings, sinkFinding{pos: common.Pos(), state: state, spec: spec, nilCtx: nilCtx})
		}
		return
	}
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
	if summary, ok := e.summaryOf(fn, callee); ok {
		e.applySummary(result, args, summary, current)
		return
	}

	e.invalidateCallArguments(common, current)
	if result != nil {
		bindResult(current, result, func(int) ctxState { return stateUnknown })
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
		var fact functionSummaryFact
		if e.pass.ImportObjectFact(object, &fact) {
			summary := summaryFromFact(fact)
			imported = &summary
		}
		e.imported[object] = imported
	}
	if imported == nil {
		return functionSummary{}, false
	}
	return *imported, true
}

// summaryFromFact clamps anything outside the lattice to unknown. Facts are
// cached on disk and may have been written by a different build, and unknown
// is the answer that keeps reporting.
func summaryFromFact(fact functionSummaryFact) functionSummary {
	return functionSummary{results: clampStates(fact.Results), effects: clampStates(fact.ParamEffects)}
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
	receiverKind := kindOf(declaredReceiver)
	var receiver abstractValue
	if declaredReceiver != nil && len(args) > 0 {
		receiver = e.value(current, args[0])
	}

	switch receiverKind {
	case kindEvent:
		if fn.Name() == "Func" {
			state := stateUnknown
			if len(args) > 1 && isNilValue(args[1]) {
				state = receiver.effectiveState(current)
			} else if len(args) > 1 {
				if summary, ok := e.summaryForFunctionValue(args[1]); ok && len(summary.effects) > 0 {
					state = summary.effects[0]
					if state == stateUnreachable {
						state = receiver.effectiveState(current)
					}
				}
			}
			updateEvent(current, receiver, eventState{state: state})
			if len(receiver.locs) == 0 {
				receiver.state = state
			}
		}
		if fn.Name() == "Ctx" {
			var state ctxState
			state, receiver.nilCtx = ctxArgState(args)
			updateEvent(current, receiver, eventState{state: state, ctxWasNil: receiver.nilCtx})
			if len(receiver.locs) == 0 {
				receiver.state = state
			} else {
				receiver.state = stateUnreachable
			}
		}
		if result != nil {
			current.values[result] = receiver
		}
		return
	case kindBuilder:
		state := receiver.effectiveState(current)
		if fn.Name() == "Ctx" {
			state, receiver.nilCtx = ctxArgState(args)
		}
		if result != nil {
			kind := kindOf(result.Type())
			if kind == kindOther {
				kind = kindBuilder
			}
			current.values[result] = abstractValue{kind: kind, state: state, nilCtx: receiver.nilCtx}
		}
		return
	case kindLogger:
		state := receiver.effectiveState(current)
		if fn.Name() == "UpdateContext" {
			state = stateUnknown
			if len(args) > 1 {
				if summary, ok := e.summaryForFunctionValue(args[1]); ok && len(summary.results) > 0 {
					state = summary.results[0]
					if state == stateUnreachable {
						state = stateUnknown
					}
				}
			}
			if len(args) > 0 {
				if location, ok := localLocation(args[0]); ok {
					writeMemory(current, location, abstractValue{kind: kindLogger, state: state})
				} else {
					e.invalidateEscape(current, args[0])
				}
			}
			return
		}
		if result == nil {
			return
		}
		switch kind := kindOf(result.Type()); kind {
		case kindEvent:
			current.values[result] = freshEvent(current, eventLocation{value: result}, eventState{state: state, ctxWasNil: receiver.nilCtx})
		case kindBuilder, kindLogger:
			current.values[result] = abstractValue{kind: kind, state: state, nilCtx: receiver.nilCtx}
		}
		return
	}

	if result == nil {
		return
	}
	state := stateUnknown
	if fn.Name() == "New" || fn.Name() == "Nop" {
		state = stateNoContext
	}
	bindResult(current, result, func(int) ctxState { return state })
}

// ctxArgState is what a Ctx call proves, and whether its argument was nil: a
// missing or nil argument proves there is no context.
func ctxArgState(args []ssa.Value) (ctxState, bool) {
	if len(args) < 2 || isNilValue(args[1]) {
		return stateNoContext, true
	}
	return stateHasContext, false
}

func (e *engine) summaryForFunctionValue(value ssa.Value) (functionSummary, bool) {
	fn := functionValue(value)
	if fn == nil {
		return functionSummary{}, false
	}
	object, _ := fn.Object().(*types.Func)
	return e.summaryOf(object, fn)
}

func (e *engine) applySummary(result ssa.Value, args []ssa.Value, summary functionSummary, current *frame) {
	for idx, effect := range summary.effects {
		if effect == stateUnreachable || idx >= len(args) {
			continue
		}
		value := e.value(current, args[idx])
		if value.kind == kindEvent {
			updateEvent(current, value, eventState{state: effect})
			continue
		}
		if value.kind != kindLogger && value.kind != kindBuilder {
			continue
		}
		location, ok := localLocation(args[idx])
		if !ok {
			// Only a syntactic address names one location for certain; a
			// may-alias set carries no postcondition, so widen instead.
			e.invalidateEscape(current, args[idx])
			continue
		}
		value.state = effect
		writeMemory(current, location, value)
	}
	if result == nil {
		return
	}
	bindResult(current, result, func(idx int) ctxState {
		if idx < len(summary.results) {
			return summary.results[idx]
		}
		return stateUnknown
	})
}

// bindResult gives a call's result, or each element of its tuple, the context
// state stateAt proves for that position. An event result is a new event
// object, identified by the call and the position; any other tracked result
// has no identity of its own.
func bindResult(current *frame, result ssa.Value, stateAt func(idx int) ctxState) {
	bind := func(t types.Type, idx int) abstractValue {
		switch kind := kindOf(t); kind {
		case kindEvent:
			return freshEvent(current, eventLocation{value: result, index: idx}, eventState{state: stateAt(idx)})
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
