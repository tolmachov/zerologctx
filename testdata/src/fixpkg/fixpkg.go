// Package fixpkg pins the suggested-fix machinery end-to-end: which context
// expression reachableCtx selects and where the TextEdit inserts the Ctx
// call. fixpkg.go.golden holds the expected post-fix source, and
// TestSuggestedFixesCompile type-checks that golden, so a fix that would not
// compile fails the build instead of silently shipping.
package fixpkg

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// preferCtxName: a variable literally named ctx wins over other candidates.
func preferCtxName(ctx, reqCtx context.Context) {
	_ = reqCtx
	log.Info().Msg("fix must insert ctx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// skipNilVar: `var c context.Context` that is never assigned holds nil, so it
// is neither a fix candidate nor evidence that a context is reachable; reqCtx
// is used instead.
func skipNilVar() {
	reqCtx := context.Background()
	_ = reqCtx
	var c context.Context
	_ = c
	log.Info().Msg("fix must insert reqCtx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// assignedAfterDeclaration: a variable declared without an initializer but
// assigned afterwards holds a real context and must be offered as the fix.
func assignedAfterDeclaration() {
	var ctx context.Context
	ctx = context.Background()
	_ = ctx
	log.Info().Msg("fix must insert ctx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// nearestPreceding: the innermost scope's nearest preceding candidate wins
// over an outer-scope parameter.
func nearestPreceding(outer context.Context) {
	_ = outer
	inner := context.Background()
	_ = inner
	log.Info().Msg("fix must insert inner") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// ptrCtx satisfies context.Context only through *ptrCtx: its methods use
// pointer receivers, so a value of this type must have its address taken.
type ptrCtx struct{ inner context.Context }

func (c *ptrCtx) Deadline() (time.Time, bool) { return c.inner.Deadline() }
func (c *ptrCtx) Done() <-chan struct{}       { return c.inner.Done() }
func (c *ptrCtx) Err() error                  { return c.inner.Err() }
func (c *ptrCtx) Value(key any) any           { return c.inner.Value(key) }

// addressOfPointerReceiverCtx: passing the value itself would not compile, so
// the fix must insert its address.
func addressOfPointerReceiverCtx() {
	pc := ptrCtx{inner: context.Background()}
	_ = pc
	log.Info().Msg("fix must insert &pc") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// server carries a context field used as the fix candidate when no scope
// variable is available.
type server struct {
	ctx context.Context
}

// receiverField: with no context variable in scope, the receiver's
// context-typed field is inserted.
func (s *server) receiverField() {
	log.Info().Msg("fix must insert s.ctx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// ptrServer holds a context reachable only through its address.
type ptrServer struct {
	pc ptrCtx
}

// receiverFieldAddress: the receiver field needs its address taken too.
func (s *ptrServer) receiverFieldAddress() {
	log.Info().Msg("fix must insert &s.pc") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// shadowedOuterCtx: the enclosing function's context is shadowed at the call
// site by a value of another type. The context is still reachable — renaming
// the shadow makes it usable — so the diagnostic stands, but inserting the
// name would silently retarget it, so no fix is offered.
func shadowedOuterCtx(ctx context.Context) {
	_ = ctx
	{
		ctx := "shadows the outer context"
		_ = ctx
		log.Info().Msg("reported without a fix") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	}
}

// shadowedFallbackCandidate: the same holds for a candidate that is not named
// "ctx" and is reached through the nearest-preceding search.
func shadowedFallbackCandidate(reqCtx context.Context) {
	_ = reqCtx
	{
		reqCtx := 42
		_ = reqCtx
		log.Info().Msg("reported without a fix") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	}
}

// shadowedReceiver: the receiver name is taken by a local, so "r.ctx" would
// no longer denote the field.
type shadowServer struct {
	ctx context.Context
}

func (r *shadowServer) shadowedReceiver() {
	{
		r := "shadows the receiver"
		_ = r
		log.Info().Msg("reported without a fix") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	}
}

// blankCtxField: a blank field can never be referenced, so its context is not
// reachable and the call is not reported at all.
type blankFieldServer struct {
	_ context.Context
}

func (b *blankFieldServer) blankCtxField() {
	log.Info().Msg("not reported - the context can never be named")
}
