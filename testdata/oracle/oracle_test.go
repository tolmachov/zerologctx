// Package oracle checks the analyzer's model of zerolog against zerolog
// itself. Every fixture asserts what the analyzer concludes; only this package
// asserts that those conclusions describe what the pinned zerolog really does.
// A row that fails here means the transfer rule for that operation in
// dataflow.go is wrong, whatever the fixtures say.
package oracle

import (
	"context"
	"io"
	"testing"

	"github.com/rs/zerolog"
)

type markerKey struct{}

var marked = context.WithValue(context.Background(), markerKey{}, true)

func carries(ctx context.Context) bool {
	return ctx != nil && ctx.Value(markerKey{}) != nil
}

// recorder notes, for every event zerolog writes, whether it carried the
// marked context when it was written.
type recorder struct{ seen []bool }

func (r *recorder) Run(event *zerolog.Event, _ zerolog.Level, _ string) {
	r.seen = append(r.seen, carries(event.GetCtx()))
}

type clearOnMarshal struct{}

func (clearOnMarshal) MarshalZerologObject(event *zerolog.Event) { event.Ctx(nil) }

type keepOnMarshal struct{}

func (keepOnMarshal) MarshalZerologObject(event *zerolog.Event) { event.Str("k", "v") }

func TestZerologSemantics(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(plain, contextual zerolog.Logger)
		want  bool
	}{
		{"New has no context", func(plain, _ zerolog.Logger) { plain.Info().Msg("") }, false},
		{"Context.Ctx attaches", func(plain, _ zerolog.Logger) { l := plain.With().Ctx(marked).Logger(); l.Info().Msg("") }, true},
		{"Event.Ctx attaches", func(plain, _ zerolog.Logger) { plain.Info().Ctx(marked).Msg("") }, true},
		{"the last Ctx wins", func(_, contextual zerolog.Logger) { contextual.Info().Ctx(nil).Msg("") }, false},
		{"With keeps", func(_, contextual zerolog.Logger) { l := contextual.With().Str("k", "v").Logger(); l.Info().Msg("") }, true},
		{"Level keeps", func(_, contextual zerolog.Logger) { l := contextual.Level(zerolog.InfoLevel); l.Info().Msg("") }, true},
		{"Sample keeps", func(_, contextual zerolog.Logger) { l := contextual.Sample(nil); l.Info().Msg("") }, true},
		{"Hook keeps", func(_, contextual zerolog.Logger) { l := contextual.Hook(); l.Info().Msg("") }, true},
		{"Output drops", func(_, contextual zerolog.Logger) { l := contextual.Output(io.Discard); l.Info().Msg("") }, false},
		{"UpdateContext never attaches", func(plain, _ zerolog.Logger) {
			plain.UpdateContext(func(c zerolog.Context) zerolog.Context { return c.Ctx(marked) })
			plain.Info().Msg("")
		}, false},
		{"UpdateContext never drops", func(_, contextual zerolog.Logger) {
			contextual.UpdateContext(func(c zerolog.Context) zerolog.Context { return c.Ctx(nil) })
			contextual.Info().Msg("")
		}, true},
		{"level methods keep", func(_, contextual zerolog.Logger) { contextual.WithLevel(zerolog.WarnLevel).Msg("") }, true},
		{"Err keeps", func(_, contextual zerolog.Logger) { contextual.Err(nil).Msg("") }, true},
		{"Print keeps", func(_, contextual zerolog.Logger) { contextual.Print("") }, true},
		{"Write keeps", func(_, contextual zerolog.Logger) { _, _ = contextual.Write([]byte("{}")) }, true},
		{"Func hands over the event", func(_, contextual zerolog.Logger) {
			contextual.Info().Func(func(event *zerolog.Event) { event.Ctx(nil) }).Msg("")
		}, false},
		{"EmbedObject hands over the event", func(_, contextual zerolog.Logger) {
			contextual.Info().EmbedObject(clearOnMarshal{}).Msg("")
		}, false},
		{"Object hands over the event", func(_, contextual zerolog.Logger) {
			contextual.Info().Object("k", clearOnMarshal{}).Msg("")
		}, false},
		{"Object with a marshaler that keeps", func(_, contextual zerolog.Logger) {
			contextual.Info().Object("k", keepOnMarshal{}).Msg("")
		}, true},
		{"Objects marshals into a scratch event", func(_, contextual zerolog.Logger) {
			contextual.Info().Objects("k", []zerolog.LogObjectMarshaler{clearOnMarshal{}}).Msg("")
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			hook := &recorder{}
			plain := zerolog.New(io.Discard).Hook(hook)
			contextual := plain.With().Ctx(marked).Logger()
			test.write(plain, contextual)
			if len(hook.seen) != 1 {
				t.Fatalf("zerolog wrote %d events, want 1", len(hook.seen))
			}
			if got := hook.seen[0]; got != test.want {
				t.Errorf("event carried the context = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCreateDictIsANewEventSeededFromItsParent(t *testing.T) {
	logger := zerolog.New(io.Discard)
	parent := logger.Info().Ctx(marked)
	dict := parent.CreateDict()
	if !carries(dict.GetCtx()) {
		t.Fatal("CreateDict did not copy the parent's context")
	}
	parent.Ctx(nil)
	if !carries(dict.GetCtx()) {
		t.Error("clearing the parent cleared the dict: they are the same event")
	}
	dict.Ctx(nil)
	parent.Ctx(marked)
	if carries(dict.GetCtx()) {
		t.Error("attaching to the parent attached to the dict: they are the same event")
	}
}

func TestCtxRetrievesTheStoredLogger(t *testing.T) {
	plain := zerolog.New(io.Discard)
	hook := &recorder{}
	plain = plain.Hook(hook)
	ctx := plain.WithContext(marked)
	zerolog.Ctx(ctx).Info().Msg("")
	if len(hook.seen) != 1 || hook.seen[0] {
		t.Errorf("zerolog.Ctx(ctx) attached the context it was given: %v", hook.seen)
	}
}
