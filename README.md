# zerologctx

[![Go Reference](https://pkg.go.dev/badge/github.com/tolmachov/zerologctx.svg)](https://pkg.go.dev/github.com/tolmachov/zerologctx)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`zerologctx` is a strict Go analyzer for `github.com/rs/zerolog`. Every
statically identifiable output operation must be proven to carry a
`context.Context`. Missing and unknown provenance are both violations.

```text
zerolog output is not proven to carry context before Msg()
```

The analyzer is intentionally fail-closed. A context variable does not need to
exist in scope for a diagnostic to be emitted. Silence has exactly three
causes: the context was proven, the sink was suppressed with `//nolint`, or the
sink is unreachable.

## Install and run

The module has no tags yet, so both commands below resolve to the current
`main` branch rather than to a release. See [RELEASING.md](RELEASING.md).

Project-local tool dependency (recommended):

```bash
go get -tool github.com/tolmachov/zerologctx/cmd/zerologctx
go tool zerologctx ./...
```

Global installation:

```bash
go install github.com/tolmachov/zerologctx/cmd/zerologctx@latest
zerologctx ./...
```

The public library entry point remains `zerologctx.Analyzer` for analysis
drivers that compose analyzers directly.

## Strict contract

The analyzer follows zerolog values through SSA control flow, aliases, local
memory, stores and loads, `Phi` nodes, reassignment, promoted methods, method
expressions, method values, statically resolved interface calls, and function
summaries within and across packages. At control-flow joins, context is proven
only when every reachable path is proven safe.

A value a closure captures is followed *into* the closure through parameters
and return values, but a captured variable the closure may rewrite is not: the
capture invalidates it, and every later use is reported. A method value is
exact while it stays put and invalidating once it escapes. A `go` statement
establishes nothing where it is spawned, and a sink a goroutine carries is
judged against the state at function exit.

Writing a zerolog value into memory the analyzer cannot name — a slice, a map,
a channel, a global, a struct passed by value — is an escape, and the value is
unknown from that point on.

These zerolog v1.35.1 sinks are checked:

- `(*zerolog.Event).Msg`, `Msgf`, `MsgFunc`, and `Send`;
- `zerolog.Logger.Print`, `Printf`, `Println`, and `Write`;
- package-level `log.Print` and `log.Printf`.

`Ctx` is order-sensitive and overwrites the previous state:

```go
log.Info().Ctx(ctx).Ctx(nil).Msg("reported: final context is nil")
log.Info().Ctx(nil).Ctx(ctx).Msg("safe: final context is attached")
```

`zerolog.Ctx(ctx)` and `log.Ctx(ctx)` retrieve a logger *from* a context; they
do not attach one, and zerolog does not set the logger's context. Output from
the logger they return is therefore reported like any other. Attach explicitly:

```go
log.Ctx(ctx).Info().Ctx(ctx).Msg("safe")
```

Logger derivations such as `With`, `Logger`, `Level`, `Output`, `Sample`, and
`Hook` preserve the proof. Local and imported function summaries carry only
proven result and pointer-mutation postconditions. Unknown calls and
`UpdateContext` callbacks without a provable summary invalidate the proof.
Mutable globals, receiver fields, and opaque values are never made safe by an
unrelated assignment elsewhere in the package.

Examples:

```go
log.Info().Msg("reported")
log.Info().Ctx(ctx).Msg("safe")

logger := zerolog.New(os.Stdout).With().Ctx(ctx).Logger()
logger.Info().Msg("safe: logger carries context")
```

For a normal Event selector call, the diagnostic may include a suggested fix
that inserts `.Ctx(expr)`. The expression is chosen from the nearest enclosing
scope that offers a usable candidate, preferring one named `ctx`; a candidate
must be declared before the sink, must not be shadowed there, must satisfy
`context.Context` (directly or via its address), and must not be provably nil.
Method expressions, method values, promoted receivers and the direct
Print/Write APIs are diagnosed without an unsafe automatic edit.

Suppress an intentional sink at that sink:

```go
log.Info().Msg("intentional") //nolint:zerologctx // explain why
```

Bare `//nolint`, `//nolint:all`, and a standalone directive immediately above
the sink are also supported.

## Static-analysis boundary

Reflection and fully dynamic calls that cannot be statically linked to a
zerolog method are outside the analyzer's boundary — a sink it cannot recognize
is a sink it cannot report. `emit := event.Msg; emit("x")` is recognized;
storing that same method value in a struct field and calling it through the
field is not. Statically resolved interface dispatch is supported. Unreachable code is not analyzed, so an output operation that can
never execute is never reported.

Once a sink is recognized, unknown provenance is reported. The only way to
silence a recognized sink is the `//nolint` directive above.

## golangci-lint v2 module plugin

Before the first release, build from a local checkout:

```yaml
# .custom-gcl.yml
version: v2.13.2
name: golangci-lint-zerologctx
plugins:
  - module: github.com/tolmachov/zerologctx
    import: github.com/tolmachov/zerologctx/plugin
    path: /absolute/path/to/zerologctx
```

After the first tag, replace `path` with the released version:

```yaml
plugins:
  - module: github.com/tolmachov/zerologctx
    import: github.com/tolmachov/zerologctx/plugin
    version: v1.0.0
```

Declare and enable the module plugin in `.golangci.yml`:

```yaml
version: "2"
linters:
  default: none
  enable:
    - zerologctx
  settings:
    custom:
      zerologctx:
        type: module
        description: Requires proven context on every zerolog output
```

Then build and run the custom binary:

```bash
golangci-lint custom
./golangci-lint-zerologctx run ./...
```

The analyzer has no settings. A plugin-specific `settings` block is rejected.
See the [golangci-lint module plugin documentation](https://golangci-lint.run/docs/plugins/module-plugins/)
for the host configuration format.

## Development

The module requires Go 1.26. CI builds and tests on 1.26.1 and 1.27.1.
See [CONTRIBUTING.md](CONTRIBUTING.md)
for the complete verification commands and [RELEASING.md](RELEASING.md) for the
first-release checklist.
