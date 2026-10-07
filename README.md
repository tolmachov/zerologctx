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

Apply the suggested fixes described below with `-fix`; add `-diff` to print
them as a unified diff instead of rewriting files:

```bash
go tool zerologctx -fix -diff ./...
go tool zerologctx -fix ./...
```

The public library entry point remains `zerologctx.Analyzer` for analysis
drivers that compose analyzers directly.

## Strict contract

The analyzer follows zerolog values through SSA control flow, aliases, local
memory, stores and loads, `Phi` nodes, reassignment, promoted methods, method
expressions, method values, statically resolved interface calls, and function
summaries within and across packages. At control-flow joins, context is proven
only when every reachable path is proven safe.

Values passed to a closure as arguments, and its results, are followed through
its summary. A variable it captures is not: capturing it is an escape. A method
value is exact while it stays put and invalidating once it escapes. A `go`
statement establishes nothing where it is spawned. A sink a goroutine or a
deferred call carries is judged against every state from its statement through
function exit, with the function's other deferred and goroutine calls applied:
the goroutine may run at any of them, and the deferred call runs on a panic at
any of them. When a deferred call may recover a panic, the function may also
return from wherever the panic happened.

Writing a zerolog value, or its address, into memory the analyzer cannot name —
a slice, a map, a channel, a global, a closure, an opaque call, a callee that
keeps or returns it — is an escape. The value is unknown from that point on,
and so is anything reached through it. A variable whose address escaped can be
proven again by assigning it, but that proof lasts only until the next call or
the next write through a pointer the analyzer does not track: either may reach
it through the alias that escaped. The same holds for an event reached through
memory the analyzer does not track, such as a struct field.

Whatever a pointer or event parameter denotes may be the same object as
another parameter's, or one a global reaches: the caller may pass it so. A
proof about it therefore lasts only until the function itself writes through
another parameter, a global or a pointer the analyzer does not track. Code the
function calls is assumed to reach a parameter's target only through the
arguments it is passed, as every summary assumes; an alias the caller sets up
behind a call's back is outside the analyzer's boundary.

A call site in a loop creates a new event on every iteration. While an event an
earlier iteration created is still held — through a variable carried around the
loop, memory, or a deferred or concurrent call — the two share one identity, and
neither receives a proof meant for the other.

These zerolog v1.35.1 sinks are checked:

- Event methods `Msg`, `Msgf`, `MsgFunc`, and `Send`;
- Logger methods `Print`, `Printf`, `Println`, and `Write`;
- package-level `log.Print` and `log.Printf`.

Package-level `log.Print` and `log.Printf` write through the global logger and
take no context, so they are always reported. Replace them with an event that
attaches one, such as `log.Info().Ctx(ctx).Msg(...)`, or suppress them.

`Ctx` is order-sensitive and overwrites the previous state:

```go
log.Info().Ctx(ctx).Ctx(nil).Msg("reported: final context is nil")
log.Info().Ctx(nil).Ctx(ctx).Msg("safe: final context is attached")
```

A final `Ctx(nil)` has its own message and no suggested fix:

```text
zerolog output's final Ctx() argument is nil before Msg()
```

`zerolog.Ctx(ctx)` and `log.Ctx(ctx)` retrieve a logger *from* a context; they
do not attach one, and zerolog does not set the logger's context. Output from
the logger they return is therefore reported like any other. Attach explicitly:

```go
log.Ctx(ctx).Info().Ctx(ctx).Msg("safe")
```

The analyzer models zerolog v1.35.1 as it behaves, and `testdata/oracle` checks
each rule against the real library:

- `With`, `Logger`, `Level`, `Sample`, and `Hook` preserve the proof, and the
  level methods (`Info`, `Err`, `WithLevel`, …) create an event that carries
  the logger's context.
- `Output` rebuilds the logger with `New` and drops its context.
- `Logger.UpdateContext` copies back only the fields its callback adds, never
  the callback's context: it neither attaches nor removes one.
- `Event.CreateDict` returns a new event seeded with its parent's context.
- `Event.Func`, `Event.Object` and `Event.EmbedObject` hand the event itself to
  user code. That code's summary decides what it does to the context; without
  a provable summary the proof is lost. `Objects` and `Array` marshal into a
  separate event or array and never hand this one over.

Local and imported function summaries carry only proven result and
pointer-mutation postconditions, which parameter a result returns, and whether
a pointer parameter escapes. A helper that returns the event it was given
returns that same event, not a new one. A callee's effect through a pointer to
a pointer is not applied: the argument is widened. Unknown calls invalidate
everything they can reach.
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

The directive may sit at the end of any line the call spans, or on a line of
its own directly above the call. Bare `//nolint` and `//nolint:all` also
suppress.

## Static-analysis boundary

Reflection and fully dynamic calls that cannot be statically linked to a
zerolog method are outside the analyzer's boundary — a sink it cannot recognize
is a sink it cannot report. `emit := event.Msg; emit("x")` is recognized;
storing that same method value in a struct field and calling it through the
field is not. A logger handed to code that only sees an `io.Writer`, such as
`fmt.Fprintln(logger, …)` or `log.New(logger, "", 0)`, is written by that code,
so its output is not recognized either. Statically resolved interface dispatch
is supported. Unreachable code is not analyzed, so an output operation that can
never execute is never reported.

Code zerolog runs on its own while it builds or writes an event — writers,
hooks, samplers, and methods such as `Error` or `String` of the values it
formats — is not followed: it is assumed not to change the context of any
logger or event the program holds. The callbacks zerolog hands an event or a
`Context` to (`Func`, `Object`, `EmbedObject`, `UpdateContext`) are followed.
Data races are out of scope: a goroutine is assumed to write shared state only
where the program synchronizes with it.

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
