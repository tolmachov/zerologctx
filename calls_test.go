package zerologctx

import (
	"go/constant"
	"slices"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCallInterpretationAlignsReceivers(t *testing.T) {
	fn := fixtureFunction(t, "callShapes")
	for _, label := range []string{"static", "expression", "bound", "invoke"} {
		t.Run(label, func(t *testing.T) {
			common := fixtureCall(t, fn, func(common *ssa.CallCommon) bool {
				for _, arg := range common.Args {
					if c, ok := arg.(*ssa.Const); ok && c.Value != nil && c.Value.Kind() == constant.String && constant.StringVal(c.Value) == label {
						return true
					}
				}
				return false
			})
			call := resolveCall(common)
			inputs := slices.Collect(call.inputs())
			if len(inputs) != 3 {
				t.Fatalf("reachable inputs = %d, want receiver and two arguments", len(inputs))
			}
			if label == "invoke" {
				if call.body != nil || call.receiver != fn.Params[1] || inputs[0] != fn.Params[1] {
					t.Fatal("opaque invoke lost dispatch receiver or acquired a static body")
				}
				if _, ok := call.summary(newEngine(nil, nil)); ok {
					t.Fatal("opaque invoke acquired a summary")
				}
			} else if call.body == nil || call.receiver != fn.Params[0] || call.args[0] != fn.Params[0] || inputs[0] != fn.Params[0] {
				t.Fatal("static method arguments are not aligned with receiver at parameter 0")
			}
		})
	}
}

func TestCallInterpretationPreservesDynamicInputs(t *testing.T) {
	fn := fixtureFunction(t, "callShapes")
	common := fixtureCall(t, fn, func(common *ssa.CallCommon) bool { return common.Value == fn.Params[2] })
	call := resolveCall(common)
	if call.body != nil || call.object != nil {
		t.Fatal("dynamic call acquired a static target")
	}
	if inputs := slices.Collect(call.inputs()); len(inputs) != 2 || inputs[0] != fn.Params[2] || inputs[1] != fn.Params[0] {
		t.Fatalf("dynamic call inputs = %v, want function value then event", inputs)
	}
}

func TestCallInterpretationKeepsCapturesOutOfParameters(t *testing.T) {
	fn := fixtureFunction(t, "callShapes")
	common := fixtureCall(t, fn, func(common *ssa.CallCommon) bool {
		callee := common.StaticCallee()
		return callee != nil && callee.Parent() == fn
	})
	call := resolveCall(common)
	if len(call.body.FreeVars) != 1 || len(call.args) != 1 || call.args[0] != fn.Params[0] {
		t.Fatal("ordinary closure captures became summary parameters")
	}
}

func TestCallbackInterpretationAccountsForBoundReceiver(t *testing.T) {
	fn := fixtureFunction(t, "boundMethodCallback")
	common := fixtureCall(t, fn, func(common *ssa.CallCommon) bool {
		callee := common.StaticCallee()
		return callee != nil && callee.Name() == "Func"
	})
	callback := resolveCallback(common.Args[1])
	if callback.firstParameter != 1 || callback.body == nil {
		t.Fatalf("bound callback = %+v, want event after receiver", callback)
	}
}

func fixtureCall(t *testing.T, fn *ssa.Function, match func(*ssa.CallCommon) bool) *ssa.CallCommon {
	t.Helper()
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok && match(call.Common()) {
				return call.Common()
			}
		}
	}
	t.Fatalf("matching call missing in %s", fn.Name())
	return nil
}
