package zerologctx

import (
	"go/types"
	"iter"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// callSite separates statically aligned parameters from the dispatch input of
// an opaque call. Only a static target can supply a summary; inputs includes
// everything an opaque callee can reach, including its dispatch value.
type callSite struct {
	object               *types.Func
	body                 *ssa.Function
	args                 []ssa.Value
	receiver, dispatched ssa.Value
}

func resolveCall(common *ssa.CallCommon) callSite {
	call := callSite{args: common.Args}
	if common.IsInvoke() {
		call.object, call.receiver, call.dispatched = common.Method, common.Value, common.Value
		return call
	}
	call.body = common.StaticCallee()
	if call.body == nil {
		call.dispatched = common.Value
		return call
	}
	call.object, _ = call.body.Object().(*types.Func)
	if closure, ok := common.Value.(*ssa.MakeClosure); ok && call.body.Synthetic != "" {
		call.args = slices.Concat(closure.Bindings, call.args)
	}
	if receiverType(call.object) != nil && len(call.args) != 0 {
		call.receiver = call.args[0]
	}
	return call
}

func (c callSite) inputs() iter.Seq[ssa.Value] {
	return func(yield func(ssa.Value) bool) {
		if c.dispatched != nil && !yield(c.dispatched) {
			return
		}
		for _, argument := range c.args {
			if !yield(argument) {
				return
			}
		}
	}
}

func (c callSite) summary(e *engine) (functionSummary, bool) {
	if c.dispatched != nil {
		return functionSummary{}, false
	}
	return e.summaryOf(c.object, c.body)
}

func (c callSite) sink(domain *stateDomain, current *frame) (sinkSpec, ssa.Value) {
	if spec := classifySink(c.object); spec.isSink() {
		return spec, c.receiver
	}
	if c.dispatched != nil && c.object != nil {
		if spec := sinkForKind(domain.value(current, c.dispatched).kind, c.object.Name()); spec.isSink() {
			return spec, c.dispatched
		}
	}
	return sinkSpec{}, nil
}

func (e *engine) invalidateResolvedCall(call callSite, current *frame) {
	forgetEscaped(current)
	for input := range call.inputs() {
		e.invalidateEscape(current, input)
	}
}

// callbackTarget owns the parameter position at which zerolog supplies the
// event, accounting for bound method receivers and marshaler receivers.
type callbackTarget struct {
	object         *types.Func
	body           *ssa.Function
	firstParameter int
}

func resolveCallback(value ssa.Value) callbackTarget {
	target := callbackTarget{body: functionValue(value)}
	if target.body == nil {
		return target
	}
	target.object, _ = target.body.Object().(*types.Func)
	if closure, ok := value.(*ssa.MakeClosure); ok && target.body.Synthetic != "" {
		target.firstParameter = len(closure.Bindings)
	}
	return target
}

func (c callbackTarget) summary(e *engine) (functionSummary, bool) {
	return e.summaryOf(c.object, c.body)
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

// resolveMarshaler identifies the MarshalZerologObject method a marshaler argument
// dispatches to, when its dynamic type is visible at the call.
func resolveMarshaler(value ssa.Value) callbackTarget {
	boxed, ok := value.(*ssa.MakeInterface)
	if !ok {
		return callbackTarget{firstParameter: 1}
	}
	program := boxed.Parent().Prog
	selection := program.MethodSets.MethodSet(boxed.X.Type()).Lookup(nil, "MarshalZerologObject")
	if selection == nil {
		return callbackTarget{firstParameter: 1}
	}
	object, _ := selection.Obj().(*types.Func)
	return callbackTarget{object: object, body: program.FuncValue(object), firstParameter: 1}
}
