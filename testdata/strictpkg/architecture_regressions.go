package strictpkg

import "github.com/rs/zerolog"

// stateGraph supplies real, typed SSA storage for the state module's cycle test.
func stateGraph(event *zerolog.Event, consume func(any), opaque any) {
	var holder struct {
		Next  any
		Event *zerolog.Event
	}
	holder.Next = &holder
	holder.Event = event
	consume(&holder)
}

func callShapes(event *zerolog.Event, target interface {
	Str(string, string) *zerolog.Event
}, dynamic func(*zerolog.Event)) {
	event.Str("static", "")
	(*zerolog.Event).Str(event, "expression", "")
	method := event.Str
	method("bound", "")
	target.Str("invoke", "")
	dynamic(event)
	captured := event
	closure := func(argument *zerolog.Event) {
		captured.Str("capture", "")
		argument.Str("argument", "")
	}
	closure(event)
}

// Event-derived registers from a previous iteration are stale at Info. The
// unrelated defers must not keep them alive or be rescanned for each register.
func eventHeldAliases(logger zerolog.Logger, n int, later func(int)) {
	for i := 0; i < n; i++ {
		event := logger.Info()
		event.Str("a", "")
		event.Str("b", "")
		event.Str("c", "")
		event.Str("d", "")
		event.Str("e", "")
		event.Str("f", "")
		event.Str("g", "")
		event.Str("h", "")
		defer later(1)
		defer later(2)
		defer later(3)
		defer later(4)
		defer later(5)
		defer later(6)
		defer later(7)
		defer later(8)
	}
}
