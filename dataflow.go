package zerologctx

import (
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

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

type engine struct {
	stateDomain
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
}

func newEngine(pass *analysis.Pass, srcFuncs []*ssa.Function) *engine {
	return &engine{
		pass: pass, srcFuncs: srcFuncs,
		findings: make(map[*ssa.Function][]sinkFinding), later: make(map[*ssa.Function][]ssa.CallInstruction),
		local: make(map[*ssa.Function]functionSummary), imported: make(map[*types.Func]*functionSummary),
		stateDomain: stateDomain{addresses: make(map[memoryLocation]map[memoryLocation]struct{})},
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
					addEdge(resolveMarshaler(arg).body)
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

// solve runs the intraprocedural worklist to a fixed point and derives the
// function's postconditions from the frames it produced.
func (e *engine) solve(fn *ssa.Function) (functionSummary, blockFrames) {
	if len(fn.Blocks) == 0 {
		// An assembly or //go:linkname declaration has no body to read, so
		// nothing about it is proven.
		return unknownSummary(fn), blockFrames{}
	}

	entry := newFrame()
	for _, param := range fn.Params {
		entry.seedParameter(param)
	}
	in := make(map[*ssa.BasicBlock]*frame)
	out := make(map[*ssa.BasicBlock]*frame)
	e.propagate(fn.Blocks[0], entry, in, out)
	if e.mayRecover(fn) {
		e.propagate(fn.Recover, recoverEntry(fn, out), in, out)
	}
	return e.summarizeExits(fn, out), blockFrames{in: in, out: out}
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
		for operand := range resolveCall(common).inputs() {
			operands = append(operands, operand)
		}
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

func (e *engine) transfer(block *ssa.BasicBlock, instruction ssa.Instruction, current *frame, predecessorOut map[*ssa.BasicBlock]*frame, sink *collector) {
	switch instruction := instruction.(type) {
	case *ssa.Store:
		e.store(current, instruction.Addr, instruction.Val)
	case *ssa.UnOp:
		if instruction.Op == token.MUL {
			e.load(current, instruction, instruction.X)
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
		// one exception: resolveCall folds its bound receiver back into the
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

// sinkOf uses the same call interpretation as transfer and escape handling.
func (e *engine) sinkOf(common *ssa.CallCommon, current *frame) (sinkSpec, ssa.Value) {
	return resolveCall(common).sink(&e.stateDomain, current)
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

// invalidateCallArguments widens everything a callee could reach through the
// call: every escaped location, and everything reachable from its arguments,
// the receiver or the function value it dispatches on.
func (e *engine) invalidateCallArguments(common *ssa.CallCommon, current *frame) {
	e.invalidateResolvedCall(resolveCall(common), current)
}

// handleCall applies one call. result is the call instruction itself; a call
// with no results has an empty tuple type, which binds nothing.
func (e *engine) handleCall(result ssa.Value, common *ssa.CallCommon, current *frame, sink *collector) {
	call := resolveCall(common)
	fn, callee, args := call.object, call.body, call.args
	if spec, receiver := call.sink(&e.stateDomain, current); spec.isSink() {
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
	if summary, ok := call.summary(e); ok {
		e.applySummary(result, args, summary, current)
		return
	}

	e.invalidateResolvedCall(call, current)
	e.bindResult(current, result, func(int) ctxState { return stateUnknown })
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
			remember(current, result, abstractValue{kind: resultKind, state: state, nilCtx: nilCtx})
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
			remember(current, result, e.freshEvent(current, eventLocation{value: result}, eventState{state: state, nilCtx: nilCtx}))
		case kindBuilder, kindLogger:
			remember(current, result, abstractValue{kind: resultKind, state: state, nilCtx: nilCtx})
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
			callback := resolveCallback(args[1])
			summary, ok := callback.summary(e)
			e.applyCallback(current, result, args[0], summary, ok, callback.firstParameter)
			return
		}
	case "Object", "EmbedObject":
		if marshaler := args[len(args)-1]; !isNilValue(marshaler) {
			callback := resolveMarshaler(marshaler)
			summary, ok := callback.summary(e)
			e.applyCallback(current, result, args[0], summary, ok, callback.firstParameter)
			return
		}
	case "CreateDict":
		remember(current, result, e.freshEvent(current, eventLocation{value: result}, eventState{
			state: receiver.effectiveState(current), nilCtx: receiver.finalCtxWasNil(current),
		}))
		return
	}
	if kindOf(result.Type()) == kindEvent {
		remember(current, result, receiver)
	}
}

// applyCallback applies what user code zerolog hands the receiver event to
// does to it, from that code's summary at the event's parameter position. The
// code runs like any call: it may write every escaped location, and without a
// summary it may keep the event.
func (e *engine) applyCallback(current *frame, result ssa.Value, receiverArg ssa.Value, summary functionSummary, ok bool, param int) {
	effect := parameterEffect{state: stateUnknown, escapes: true}
	if ok {
		effect = summary.parameter(param)
	}
	e.applyCallbackEffect(current, result, receiverArg, effect)
}

// ctxArgState is what a Ctx call proves, and whether its argument was nil: a
// missing or nil argument proves there is no context.
func ctxArgState(args []ssa.Value) (ctxState, bool) {
	if len(args) < 2 || isNilValue(args[1]) {
		return stateNoContext, true
	}
	return stateHasContext, false
}

// applySummary writes a callee's parameter effects into the caller. An effect
// is a postcondition, so it is written only into the one location the
// argument certainly denotes; every other written or escaping argument is
// widened. The callee widened its parameters at every write of its own through
// an alias, but not at the calls it makes, so an effect is not written into a
// location that escaped here either: code the callee ran may have reached it
// through that escape.
func (e *engine) applySummary(result ssa.Value, args []ssa.Value, summary functionSummary, current *frame) {
	for idx, effect := range summary.parameters() {
		if idx < len(args) {
			e.applyPostcondition(current, args[idx], effect)
		}
	}
	e.bindResult(current, result, func(idx int) ctxState { return summary.result(idx).state })
	e.bindSummaryResults(current, result, args, summary)
}

func isNilValue(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	return ok && constant.IsNil()
}

// freshEvent asks the solver whether an earlier event at this site is held;
// state creation receives only that decision, independent of SSA scheduling.
func (e *engine) freshEvent(current *frame, location eventLocation, state eventState) abstractValue {
	held := current.hasEvent(location) && e.eventHeld(current, location)
	return current.seedEvent(location, state, held)
}

// eventHeld uses SSA availability and later-call registration to decide which
// register holders may still be observed when an event site runs again.
func (e *engine) eventHeld(current *frame, location eventLocation) bool {
	site, ok := location.value.(ssa.Instruction)
	if !ok {
		return true
	}
	if current.holdsEvent(location, func(value ssa.Value) bool { return availableAt(value, site) }) {
		return true
	}
	var operands []*ssa.Value
	for _, later := range e.laterCalls(site.Parent()) {
		operands = later.Operands(operands[:0])
		for _, operand := range operands {
			if *operand != nil && current.valueHoldsEvent(*operand, location) {
				return true
			}
		}
	}
	return false
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
