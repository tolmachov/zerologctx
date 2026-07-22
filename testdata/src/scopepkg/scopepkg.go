// Package scopepkg pins the availability gate: a missing-Ctx diagnostic is
// emitted only when a context is reachable at the call site — as a scope
// variable declared before the call or as a context-typed receiver field.
// Package-level context availability is covered by testpkg's aa_order_use.go
// and zz_order_decl.go; this package deliberately has no package-level
// context so the negative cases below stay negative.
package scopepkg

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// noCtxAnywhere: nothing to pass — the missing-Ctx diagnostic is suppressed.
func noCtxAnywhere() {
	log.Info().Msg("no context available - must not trigger")
}

// createdMidFunction: the requirement kicks in at the point the context is
// created — calls before it stay silent, calls after it are reported.
func createdMidFunction() {
	log.Info().Msg("before ctx exists - must not trigger")
	ctx := context.Background()
	_ = ctx
	log.Info().Msg("after ctx exists - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// closureSeesOuterParam: a context parameter of the enclosing function is
// visible inside a FuncLit.
func closureSeesOuterParam(ctx context.Context) {
	f := func() {
		log.Info().Msg("outer param visible - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	}
	f()
}

// uninitializedOnly: `var ctx context.Context` (nil) is not a usable
// candidate, so the call is not reported.
func uninitializedOnly() {
	var ctx context.Context
	_ = ctx
	log.Info().Msg("only nil var - must not trigger")
}

type withCtxField struct {
	ctx    context.Context
	logger zerolog.Logger
}

// logWithField: no scope variable, but the receiver carries a context field.
func (s *withCtxField) logWithField() {
	s.logger.Info().Msg("receiver has ctx field - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// logFromClosure: the receiver field is still reachable from a FuncLit
// nested in the method.
func (s *withCtxField) logFromClosure() {
	f := func() {
		s.logger.Info().Msg("receiver field via closure - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	}
	f()
}

type withoutCtxField struct {
	logger zerolog.Logger
}

// logWithoutField: neither scope variables nor receiver fields provide a
// context — stay silent.
func (s *withoutCtxField) logWithoutField() {
	s.logger.Info().Msg("receiver has no ctx field - must not trigger")
}
