package summaryprovider

import (
	"context"
	"io"

	"github.com/rs/zerolog"
)

func ContextLogger(ctx context.Context) zerolog.Logger { // want ContextLogger:"zerolog context summary results=\\[has-context\\] effects=\\[preserved\\]"
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

func Attach(event *zerolog.Event, ctx context.Context) { // want Attach:"zerolog context summary results=\\[\\] effects=\\[has-context preserved\\]"
	event.Ctx(ctx)
}

func AttachLogger(logger *zerolog.Logger, ctx context.Context) { // want AttachLogger:"zerolog context summary results=\\[\\] effects=\\[has-context preserved\\]"
	*logger = logger.With().Ctx(ctx).Logger()
}

func AttachBuilder(builder *zerolog.Context, ctx context.Context) { // want AttachBuilder:"zerolog context summary results=\\[\\] effects=\\[has-context preserved\\]"
	*builder = builder.Ctx(ctx)
}

func AttachBackground(builder zerolog.Context) zerolog.Context { // want AttachBackground:"zerolog context summary results=\\[has-context\\] effects=\\[preserved\\]"
	return builder.Ctx(context.Background())
}

func ResetAny(value any) {
	logger := value.(*zerolog.Logger)
	*logger = zerolog.New(io.Discard)
}

// Outer is declared before its callee on purpose: a verdict must not depend on
// declaration order within the package.
func Outer(ctx context.Context) zerolog.Logger { // want Outer:"zerolog context summary results=\\[has-context\\] effects=\\[preserved\\]"
	return Inner(ctx)
}

func Inner(ctx context.Context) zerolog.Logger { // want Inner:"zerolog context summary results=\\[has-context\\] effects=\\[preserved\\]"
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

// PlainLogger proves the negative: "no context" is a postcondition too, and it
// crosses the package boundary just like the positive one.
func PlainLogger() zerolog.Logger { // want PlainLogger:"zerolog context summary results=\\[no-context\\] effects=\\[\\]"
	return zerolog.New(io.Discard)
}
