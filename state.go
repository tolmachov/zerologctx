package zerologctx

import (
	"go/types"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// stateDomain owns SSA value interpretation and shared immutable address sets.
// Frames own the mutable proof state; callers use semantic operations below.
type stateDomain struct {
	addresses map[memoryLocation]map[memoryLocation]struct{}
}

func (e *stateDomain) load(current *frame, result, address ssa.Value) {
	if location, ok := localLocation(address); ok {
		value, exists := current.memory[location]
		if !exists {
			value = loadAggregate(current, location, result.Type())
		}
		remember(current, result, value)
	}
}

// store owns the ordering of alias invalidation, escape propagation and writes.
// An unknown destination can alias any parameter target or escaped location.
func (e *stateDomain) store(current *frame, address, source ssa.Value) {
	location, ok := localLocation(address)
	if !ok {
		forgetAliased(current)
		e.invalidateEscape(current, address)
		e.invalidateEscape(current, source)
		return
	}
	stored := e.value(current, source)
	if memoryEscaped(current, location) {
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
}

// applyPostcondition applies a callee's effect only to a certain, unescaped
// target. It differs from a direct Ctx write: arbitrary code in the callee may
// also have reached any location that had already escaped here.
func (e *stateDomain) applyPostcondition(current *frame, argument ssa.Value, effect parameterEffect) {
	if effect.preserves() {
		return
	}
	value := e.value(current, argument)
	if !effect.escapes && pointsAtZerologValue(argument.Type()) {
		switch value.kind {
		case kindEvent:
			if location, ok := value.mustEvent(); ok && !eventEscapedHere(current, location) {
				writeEvent(current, location, eventState{state: effect.state})
				return
			}
		case kindLogger, kindBuilder:
			if location, ok := localLocation(argument); ok && !escapedHere(current, location) {
				writeMemory(current, location, abstractValue{kind: value.kind, state: effect.state})
				return
			}
		}
	}
	e.invalidateEscape(current, argument)
}

// seedParameter establishes unknown entry state without recording writes or
// escapes. Parameter aliases are accounted for separately from local escapes.
func (f *frame) seedParameter(param *ssa.Parameter) {
	kind := kindOf(param.Type())
	if kind == kindOther {
		return
	}
	value := unknownValue(kind)
	if kind == kindEvent {
		value = f.seedEvent(eventLocation{value: param}, eventState{state: stateUnknown}, false)
	}
	if trackedPointerKind(param.Type()) == kindOther {
		f.values[param] = value
	} else {
		f.memory[memoryLocation{root: param}] = value
	}
}

type parameterState struct {
	state            ctxState
	written, escaped bool
}

func (f *frame) parameterState(param *ssa.Parameter) parameterState {
	_, event := f.escapedEvents[eventLocation{value: param}]
	_, memory := f.escapedRoots[param]
	post := parameterState{written: f.writes[param], escaped: event || memory}
	if trackedPointerKind(param.Type()) == kindEvent {
		post.state = f.events[eventLocation{value: param}].state
	} else {
		post.state = f.memory[memoryLocation{root: param}].latticeState(f)
	}
	return post
}

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

// seedEvent creates the event a location denotes and seeds what is known
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
func (current *frame) seedEvent(location eventLocation, state eventState, held bool) abstractValue {
	value := abstractValue{kind: kindEvent, locs: map[eventLocation]struct{}{location: {}}}
	if previous, revisited := current.events[location]; revisited && held {
		current.events[location] = joinEventState(previous, state)
		value.partialLocs = true
		return value
	}
	current.events[location] = state
	return value
}

func reachesEvent(value abstractValue, location eventLocation) bool {
	if _, ok := value.locs[location]; ok {
		return true
	}
	return slices.ContainsFunc(value.elems, func(element abstractValue) bool { return reachesEvent(element, location) })
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

// observe is what current proves about the context of an output with this
// receiver, and whether its final Ctx argument was nil. A nil receiver means
// nothing can be proven.
func (e *stateDomain) observe(receiver ssa.Value, current *frame) (ctxState, bool) {
	if receiver == nil {
		return stateUnknown, false
	}
	value := e.value(current, receiver)
	return value.effectiveState(current), value.finalCtxWasNil(current)
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

func (e *stateDomain) value(f *frame, value ssa.Value) abstractValue {
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

func (e *stateDomain) addressSet(location memoryLocation) map[memoryLocation]struct{} {
	set, ok := e.addresses[location]
	if !ok {
		set = map[memoryLocation]struct{}{location: {}}
		e.addresses[location] = set
	}
	return set
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

// invalidateEscape widens every state reachable through value, because the
// code it escapes to may overwrite any of it, and marks all of it escaped,
// because that code may keep an alias and write through it later.
//
// An abstract value reaches state four ways and all four have to be followed:
// a syntactic address, the event identities in locs, the memory locations in
// memLocs, and everything nested inside an aggregate. Following fewer of them
// silently preserves a proof the escape destroyed.
func (e *stateDomain) invalidateEscape(current *frame, value ssa.Value) {
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

func (e *engine) bindSummaryResults(current *frame, result ssa.Value, args []ssa.Value, summary functionSummary) {
	// A result that is one of the arguments is bound to it. Any other result
	// may reach an argument the callee let escape, so it escapes too.
	bound := current.values[result]
	escapes := summary.anyEscapes()
	for idx, proof := range summary.results() {
		param, returned := proof.returnedParameter()
		switch {
		case returned && param < len(args):
			argument := e.value(current, args[param])
			if len(bound.elems) != 0 {
				bound.elems[idx] = argument
			} else {
				bound = argument
			}
		case escapes:
			if len(bound.elems) != 0 {
				forgetReachable(current, bound.elems[idx])
			} else {
				forgetReachable(current, bound)
			}
		}
	}
	remember(current, result, bound)
}

func (e *engine) applyCallbackEffect(current *frame, result, receiverArg ssa.Value, effect parameterEffect) {
	forgetEscaped(current)
	receiver := e.value(current, receiverArg)
	switch {
	case effect.escapes:
		e.invalidateEscape(current, receiverArg)
		e.mutateEvent(current, result, e.value(current, receiverArg), eventState{state: stateUnknown})
	case effect.preserves():
		current.values[result] = receiver
	default:
		// As in applySummary, the effect is not written into an event that
		// escaped here: code the callback ran may have reached it that way.
		state := eventState{state: effect.state}
		if location, must := receiver.mustEvent(); must && eventEscapedHere(current, location) {
			state = eventState{state: stateUnknown}
		}
		e.mutateEvent(current, result, receiver, state)
	}
}

// reaches reports whether invalidating value could change anything in current:
// whether it reaches an event, a memory location or an aggregate. e.value
// gives every local address its location, so this mirrors the routes
// invalidateEscape follows.
func (e *stateDomain) reaches(current *frame, value ssa.Value) bool {
	known := e.value(current, value)
	return len(known.locs) != 0 || len(known.memLocs) != 0 || len(known.elems) != 0
}

type valueKind uint8

const (
	kindOther valueKind = iota
	kindLogger
	kindEvent
	kindBuilder
)

// ctxState is the four-point lattice the whole analysis rests on:
// stateUnreachable is the bottom, stateHasContext and stateNoContext are the
// two incomparable proofs, and stateUnknown is the top. Ordering matters:
// the bottom must be the zero value so that an absent map entry reads as
// "nothing known yet" rather than as a proof.
type ctxState uint8

const (
	stateUnreachable ctxState = iota
	stateHasContext
	stateNoContext
	stateUnknown
)

// joinState is the least upper bound. The bottom is a two-sided identity and
// the top absorbs, so the join is commutative, associative and idempotent -
// properties the worklist relies on for its result to be independent of the
// order in which predecessors are merged.
//
// The bottom means "no path contributed": an unreachable path, an event whose
// state lives in frame.events rather than in the value, a preserved parameter,
// or a summary slot not yet solved. It is never read as a proof:
// effectiveState turns it into unknown for every tracked value before a sink
// is judged.
func joinState(a, b ctxState) ctxState {
	if a == stateUnreachable {
		return b
	}
	if b == stateUnreachable || a == b {
		return a
	}
	return stateUnknown
}

func (s ctxState) String() string {
	switch s {
	case stateUnreachable:
		return "unreachable"
	case stateHasContext:
		return "has-context"
	case stateNoContext:
		return "no-context"
	default:
		return "unknown"
	}
}

// returnedParam identifies the zero-based parameter value certainly is:
// the event a *zerolog.Event parameter denotes, or the address a tracked
// pointer parameter holds. Other results have no certain parameter identity.
func returnedParam(fn *ssa.Function, value abstractValue) (int, bool) {
	for idx, param := range fn.Params {
		if trackedPointerKind(param.Type()) == kindOther {
			continue
		}
		if location, ok := value.mustEvent(); ok && location == (eventLocation{value: param}) {
			return idx, true
		}
		if _, ok := value.memLocs[memoryLocation{root: param}]; ok && len(value.memLocs) == 1 && !value.partialLocs && len(value.locs) == 0 {
			return idx, true
		}
	}
	return 0, false
}

func (f *frame) hasEvent(location eventLocation) bool {
	_, exists := f.events[location]
	return exists
}

// valueHoldsEvent observes a retained register without exposing frame storage.
func (f *frame) valueHoldsEvent(value ssa.Value, location eventLocation) bool {
	return reachesEvent(f.values[value], location)
}

// holdsEvent reads state retention without interpreting control flow: memory
// retains its contents, and the solver decides which registers are still live.
func (f *frame) holdsEvent(location eventLocation, live func(ssa.Value) bool) bool {
	for _, stored := range f.memory {
		if reachesEvent(stored, location) {
			return true
		}
	}
	for value, known := range f.values {
		if reachesEvent(known, location) && live(value) {
			return true
		}
	}
	return false
}
