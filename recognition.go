package zerologctx

import "go/types"

const (
	zerologPkgPath = "github.com/rs/zerolog"
	zerologLogPath = "github.com/rs/zerolog/log"
)

// sinkSpec describes an output operation: the kind of value whose context it
// writes, kindOther when the call is not an output, and the method name the
// diagnostic quotes.
type sinkSpec struct {
	receiver valueKind
	name     string
}

func (s sinkSpec) isSink() bool { return s.receiver != kindOther }

func deref(t types.Type) types.Type {
	for {
		t = types.Unalias(t)
		p, ok := t.(*types.Pointer)
		if !ok {
			return t
		}
		t = p.Elem()
	}
}

func zerologNamed(t types.Type, name string) bool {
	n, ok := deref(t).(*types.Named)
	if !ok || n.Obj() == nil || n.Obj().Pkg() == nil {
		return false
	}
	return n.Obj().Pkg().Path() == zerologPkgPath && n.Obj().Name() == name
}

func kindOf(t types.Type) valueKind {
	switch {
	case zerologNamed(t, "Logger"):
		return kindLogger
	case zerologNamed(t, "Event"):
		return kindEvent
	case zerologNamed(t, "Context"):
		return kindBuilder
	default:
		return kindOther
	}
}

// receiverType returns the declared receiver type of fn, or nil when fn is a
// package-level function.
func receiverType(fn *types.Func) types.Type {
	if fn == nil {
		return nil
	}
	if recv := fn.Signature().Recv(); recv != nil {
		return recv.Type()
	}
	return nil
}

// sinkForKind is the one lookup deciding whether a method call on a zerolog
// value ends an output operation. Both the statically typed and the
// dynamically resolved path go through it, so there is one answer.
func sinkForKind(kind valueKind, name string) sinkSpec {
	switch {
	case kind == kindEvent && (name == "Msg" || name == "Msgf" || name == "MsgFunc" || name == "Send"),
		kind == kindLogger && (name == "Print" || name == "Printf" || name == "Println" || name == "Write"):
		return sinkSpec{receiver: kind, name: name}
	default:
		return sinkSpec{}
	}
}

// inZerologLogPackage reports whether fn is declared in zerolog's log package.
// Its Print and Printf write through the global logger and take no receiver,
// so a sink there has no provenance to prove.
func inZerologLogPackage(fn *types.Func) bool {
	return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == zerologLogPath
}

func classifySink(fn *types.Func) sinkSpec {
	if fn == nil || fn.Pkg() == nil {
		return sinkSpec{}
	}
	if recv := receiverType(fn); recv != nil {
		return sinkForKind(kindOf(recv), fn.Name())
	}
	if inZerologLogPackage(fn) && (fn.Name() == "Print" || fn.Name() == "Printf") {
		return sinkSpec{receiver: kindLogger, name: fn.Name()}
	}
	return sinkSpec{}
}
