package zerologctx

import "go/types"

const (
	zerologPkgPath = "github.com/rs/zerolog"
	zerologLogPath = "github.com/rs/zerolog/log"
)

type sinkKind uint8

const (
	sinkNone sinkKind = iota
	sinkEvent
	sinkLogger
)

type sinkSpec struct {
	kind sinkKind
	name string
}

var eventSinks = map[string]bool{"Msg": true, "Msgf": true, "MsgFunc": true, "Send": true}
var loggerSinks = map[string]bool{"Print": true, "Printf": true, "Println": true, "Write": true}

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

// sinkForKind is the one table lookup deciding whether a method call on a
// zerolog value ends an output operation. Both the statically typed and the
// dynamically resolved path go through it, so there is one answer.
func sinkForKind(kind valueKind, name string) sinkSpec {
	switch kind {
	case kindEvent:
		if eventSinks[name] {
			return sinkSpec{kind: sinkEvent, name: name}
		}
	case kindLogger:
		if loggerSinks[name] {
			return sinkSpec{kind: sinkLogger, name: name}
		}
	}
	return sinkSpec{}
}

// isPackageLevelLogSink reports whether fn belongs to zerolog's log package.
// Those functions take the message as their first argument, not a receiver, so
// there is no provenance to prove.
func isPackageLevelLogSink(fn *types.Func) bool {
	return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == zerologLogPath
}

func classifySink(fn *types.Func) sinkSpec {
	if fn == nil || fn.Pkg() == nil {
		return sinkSpec{}
	}
	if recv := receiverType(fn); recv != nil {
		return sinkForKind(kindOf(recv), fn.Name())
	}
	if isPackageLevelLogSink(fn) && (fn.Name() == "Print" || fn.Name() == "Printf") {
		return sinkSpec{kind: sinkLogger, name: fn.Name()}
	}
	return sinkSpec{}
}
