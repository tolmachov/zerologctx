package summaryprovider

import (
	"context"
	"io"

	"github.com/rs/zerolog"
)

func ContextLogger(ctx context.Context) zerolog.Logger { // want ContextLogger:"zerolog context summary results=\\[has-context\\] returns=\\[-1\\] effects=\\[preserved\\] escapes=\\[false\\]"
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

func Attach(event *zerolog.Event, ctx context.Context) { // want Attach:"zerolog context summary results=\\[\\] returns=\\[\\] effects=\\[has-context preserved\\] escapes=\\[false false\\]"
	event.Ctx(ctx)
}

func AttachLogger(logger *zerolog.Logger, ctx context.Context) { // want AttachLogger:"zerolog context summary results=\\[\\] returns=\\[\\] effects=\\[has-context preserved\\] escapes=\\[false false\\]"
	*logger = logger.With().Ctx(ctx).Logger()
}

func AttachBuilder(builder *zerolog.Context, ctx context.Context) { // want AttachBuilder:"zerolog context summary results=\\[\\] returns=\\[\\] effects=\\[has-context preserved\\] escapes=\\[false false\\]"
	*builder = builder.Ctx(ctx)
}

func AttachBackground(builder zerolog.Context) zerolog.Context { // want AttachBackground:"zerolog context summary results=\\[has-context\\] returns=\\[-1\\] effects=\\[preserved\\] escapes=\\[false\\]"
	return builder.Ctx(context.Background())
}

func ResetAny(value any) {
	logger := value.(*zerolog.Logger)
	*logger = zerolog.New(io.Discard)
}

// Outer is declared before its callee on purpose: a verdict must not depend on
// declaration order within the package.
func Outer(ctx context.Context) zerolog.Logger { // want Outer:"zerolog context summary results=\\[has-context\\] returns=\\[-1\\] effects=\\[preserved\\] escapes=\\[false\\]"
	return Inner(ctx)
}

func Inner(ctx context.Context) zerolog.Logger { // want Inner:"zerolog context summary results=\\[has-context\\] returns=\\[-1\\] effects=\\[preserved\\] escapes=\\[false\\]"
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

// PlainLogger proves the negative: "no context" is a postcondition too, and it
// crosses the package boundary just like the positive one.
func PlainLogger() zerolog.Logger { // want PlainLogger:"zerolog context summary results=\\[no-context\\] returns=\\[-1\\] effects=\\[\\] escapes=\\[\\]"
	return zerolog.New(io.Discard)
}

// Kept is where KeepLogger leaves the pointer it is given.
var Kept *zerolog.Logger

// KeepLogger proves its result and lets its parameter escape: callers must not
// trust what their logger holds after anything else runs.
func KeepLogger(logger *zerolog.Logger, ctx context.Context) zerolog.Logger { // want KeepLogger:"zerolog context summary results=\\[has-context\\] returns=\\[-1\\] effects=\\[preserved preserved\\] escapes=\\[true false\\]"
	Kept = logger
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

func ResetKept() {
	*Kept = zerolog.New(io.Discard)
}

// Clearer's method clears the event it is handed; as a bound method value its
// event is the second summarised parameter, after the receiver.
type Clearer struct{}

func (Clearer) Clear(event *zerolog.Event) { // want Clear:"zerolog context summary results=\\[\\] returns=\\[\\] effects=\\[preserved no-context\\] escapes=\\[false false\\]"
	event.Ctx(nil)
}
