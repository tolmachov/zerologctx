package fixpkg

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func fix(ctx context.Context) {
	log.Info().Msg("fix me") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func noCandidate() {
	log.Info().Msg("diagnostic without fix") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func unsafeFixForms(ctx context.Context) {
	event := log.Info()
	(*zerolog.Event).Msg(event, "method expression") // want `zerolog output is not proven to carry context before Msg\(\)`
	emit := event.Msg
	emit("method value")      // want `zerolog output is not proven to carry context before Msg\(\)`
	log.Print("direct print") // want `zerolog output is not proven to carry context before Print\(\)`
}

func knownNilCandidate() {
	var ctx context.Context
	log.Info().Msg("nil candidate") // want `zerolog output is not proven to carry context before Msg\(\)`
	_ = ctx
}

func knownNilShortCandidate() {
	ctx := context.Context(nil)
	log.Info().Msg("nil short candidate") // want `zerolog output is not proven to carry context before Msg\(\)`
	_ = ctx
}

type pointerContext struct{ inner context.Context }

func (c *pointerContext) Deadline() (time.Time, bool) { return c.inner.Deadline() }
func (c *pointerContext) Done() <-chan struct{}       { return c.inner.Done() }
func (c *pointerContext) Err() error                  { return c.inner.Err() }
func (c *pointerContext) Value(key any) any           { return c.inner.Value(key) }

func preferCtxName(appCtx, ctx context.Context) {
	_ = appCtx
	log.Info().Msg("ctx wins over a candidate whose name sorts earlier") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func fallbackCandidate(request context.Context) {
	log.Info().Msg("the only candidate is not named ctx") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func nearestScopeWinsOverCtxName(ctx context.Context) {
	_ = ctx
	{
		inner := context.Background()
		_ = inner
		log.Info().Msg("the nearer declaration wins over the name ctx") // want `zerolog output is not proven to carry context before Msg\(\)`
	}
}

func shadowedCtx(ctx context.Context) {
	_ = ctx
	{
		ctx := "not a context"
		_ = ctx
		log.Info().Msg("shadowed: reported without a fix") // want `zerolog output is not proven to carry context before Msg\(\)`
	}
}

func addressOfPointerReceiverContext() {
	value := pointerContext{inner: context.Background()}
	_ = value
	log.Info().Msg("only *pointerContext implements context.Context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func assignedAfterDeclaration() {
	var ctx context.Context
	ctx = context.Background()
	_ = ctx
	log.Info().Msg("declared nil but reassigned before the sink") // want `zerolog output is not proven to carry context before Msg\(\)`
}
