# zerologctx

[![Go Reference](https://pkg.go.dev/badge/github.com/tolmachov/zerologctx.svg)](https://pkg.go.dev/github.com/tolmachov/zerologctx)
[![Go Report Card](https://goreportcard.com/badge/github.com/tolmachov/zerologctx)](https://goreportcard.com/report/github.com/tolmachov/zerologctx)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A static analysis tool (linter) for Go that ensures all [zerolog](https://github.com/rs/zerolog) logging events include context via the `.Ctx(ctx)` method.

## Motivation

Including the request context in logs is essential for:

- **Distributed Tracing**: Propagating trace IDs across service boundaries
- **Request Correlation**: Connecting logs from the same request
- **Debugging**: Linking related log entries in complex systems
- **Observability**: Enhancing monitoring and alerting capabilities

This linter helps enforce this best practice by ensuring all zerolog logs include context.

## Installation

### Direct Installation

```bash
go install github.com/tolmachov/zerologctx/cmd/zerologctx@latest
```

### As a Project Dependency

```bash
# Add to your project
go get -u github.com/tolmachov/zerologctx

# Create a tools.go file (if you don't have one)
cat > tools.go << EOF
//go:build tools
// +build tools

package tools

import (
    _ "github.com/tolmachov/zerologctx/cmd/zerologctx"
)
EOF
```

## Usage

### Standalone

```bash
# Run on specific packages
zerologctx ./pkg/...

# Run on all packages in your module
zerologctx ./...

# Run with verbose output
zerologctx -v ./...
```

### With golangci-lint

`zerologctx` is a golangci-lint **v2 module plugin**. golangci-lint builds a
custom binary with the plugin linked in. The v1 `-buildmode=plugin` path is not
supported: it required compiling against golangci-lint's exact toolchain and
dependency versions, and v2 replaced it.

1. Declare the plugin in `.custom-gcl.yml`, next to your `go.mod`:

```yaml
version: v2.13.2
name: golangci-lint-zerologctx
plugins:
  - module: github.com/tolmachov/zerologctx
    import: github.com/tolmachov/zerologctx/plugin
    version: v0.1.0
```

The `import` line is required. Registration lives in the `plugin` subpackage so
that importing the analyzer as a library does not drag in golangci-lint's
plugin registry.

2. Build the custom binary:

```bash
golangci-lint custom
```

3. Enable the linter in `.golangci.yml`:

```yaml
version: "2"
linters:
  enable:
    - zerologctx
  settings:
    custom:
      zerologctx:
        type: module
        description: Ensures zerolog events include context via Ctx()
```

4. Run the binary produced in step 2:

```bash
./golangci-lint-zerologctx run
```

The analyzer has no settings. Supplying any is an error rather than a silent
no-op, so a stale or typo'd configuration block is reported instead of ignored.

### In CI/CD Pipelines

Example GitHub Actions workflow:

```yaml
name: Code Quality

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Go
      uses: actions/setup-go@v5
      with:
        go-version: '1.26'
    
    - name: Install zerologctx linter
      run: go install github.com/tolmachov/zerologctx/cmd/zerologctx@latest
    
    - name: Run linter
      run: zerologctx ./...
```

## What It Checks

This linter detects when zerolog events use terminal methods like `.Msg()`, `.Msgf()`, `.MsgFunc()`, or `.Send()` without first calling `.Ctx(ctx)` in the method chain.

A diagnostic is emitted only when a context is actually available at the call site:

- a `context.Context` function parameter,
- a local variable declared **before** the call (a context created mid-function makes the calls after it require `.Ctx()`, while calls before it stay silent),
- a package-level context variable,
- a `context.Context`-typed field of the method's receiver (e.g. `s.ctx`).

A reachable context that cannot be *named* at the call site — because a local
declaration shadows it — is still reported, but without a suggested fix, since
inserting the name would reference the shadowing declaration instead.

Context-bearing loggers are recognised across package boundaries: an exported
package-level logger or an exported struct field that was assigned a logger
with an embedded context is published as an analysis fact and honoured by
importing packages.

Code that has no context to pass is not reported:

```go
// ✅ Not flagged - there is no context anywhere in scope
func startup() {
    log.Info().Msg("initializing")
}

// The requirement kicks in once a context exists
func process() {
    log.Info().Msg("no context yet - not flagged")
    ctx := context.Background()
    log.Info().Msg("flagged - ctx is now available") // ❌
    log.Info().Ctx(ctx).Msg("correct")               // ✅
}
```

### Important Distinction

The linter correctly distinguishes between:
- `log.Ctx(ctx)` - Returns a `Logger` extracted from context (does NOT add context to events)
- `event.Ctx(ctx)` - Adds context to a specific `Event` (correct usage)

```go
// ❌ WRONG - log.Ctx(ctx) doesn't add context to the event
log.Ctx(ctx).Error().Msg("error")

// ✅ CORRECT - event.Ctx(ctx) adds context to the event
log.Error().Ctx(ctx).Msg("error")
```

### ✅ Correct Usage Patterns

```go
// Basic usage with context
log.Info().Ctx(ctx).Msg("Message with context")

// With additional fields
log.Error().Ctx(ctx).Str("key", "value").Msg("Error with context")

// Using Send() instead of Msg()
log.Info().Ctx(ctx).Str("action", "test").Send()

// With custom loggers
logger := zerolog.New(os.Stdout)
logger.Info().Ctx(ctx).Msg("Custom logger with context")

// With derived context
childCtx := context.WithValue(ctx, "key", "value")
log.Info().Ctx(childCtx).Msg("Using child context")
```

### ❌ Incorrect Usage Patterns (Flagged by Linter)

```go
// Missing context
log.Info().Msg("Message without context")

// Missing context with other fields
log.Error().Str("key", "value").Msg("Error without context")

// Missing context with Send()
log.Info().Str("action", "test").Send()

// Missing context with custom logger
logger := zerolog.New(os.Stdout)
logger.Info().Str("key", "value").Msg("Custom logger without context")
```

## Advanced Features

### Custom Context Types

The linter supports custom context types that embed `context.Context`:

```go
type CustomContext struct {
    context.Context
    userID int64
}

func processRequest(ctx *CustomContext) {
    // ✅ Works with custom context types
    log.Info().Ctx(ctx).Msg("Processing request")
}
```

### Loggers with Embedded Context

The linter recognizes loggers created with embedded context:

```go
// Create logger with context
ctxLogger := log.With().Ctx(ctx).Logger()

// ✅ Events from this logger don't need .Ctx() again
ctxLogger.Info().Msg("This is fine - context already in logger")
```

### Variable Tracking

The linter tracks context through variable assignments:

```go
// ✅ Context tracked through variables
event := log.Info().Ctx(ctx)
event.Str("key", "value")
event.Msg("Message with context")

// Also works with chained variables
event2 := event.Str("another", "field")
event2.Msg("Still has context")
```

### Suppressing False Positives

Use `//nolint:zerologctx` to suppress warnings for specific cases:

```go
// Single line suppression
log.Info().Msg("Startup message") //nolint:zerologctx

// Multi-line suppression (comment on line before)
//nolint:zerologctx
log.Info().
    Str("version", "1.0.0").
    Msg("Application started")

// Multiple linters
log.Info().Msg("message") //nolint:zerologctx,anotherlinter
```

## Integration with Editors

### VS Code

The stock `golangci-lint` binary knows nothing about custom plugins, so point
the Go extension at the binary produced by `golangci-lint custom`:

```json
{
  "go.lintTool": "golangci-lint",
  "go.alternateTools": {
    "golangci-lint": "${workspaceFolder}/golangci-lint-zerologctx"
  }
}
```

The linter is enabled through `.golangci.yml`, so no extra flags are needed.

Alternatively, run the standalone binary directly — see the GoLand recipe
below, which works the same way in any editor that can run a command on save.

### GoLand/IntelliJ IDEA

1. Go to Preferences/Settings → Tools → File Watchers
2. Add a new File Watcher with:
   - Program: `$GoBinDirs$/zerologctx`
   - Arguments: `$FilePath$`
   - Working directory: `$ProjectFileDir$`

## Limitations

The analysis is flow-insensitive by design, which keeps it quiet rather than
noisy:

- An assignment inside a conditional branch counts as unconditional.
- Struct fields are tracked per field declaration, not per instance, so
  `a.logger = ctxLogger` also covers `b.logger`. This is deliberate: keying
  facts per instance would report the common shape where a constructor fills
  the field on one variable and methods read it through their own receiver.
- Across package boundaries only exported objects carry facts, and only as
  "was ever assigned a context", without position ordering.
- Loggers and events returned by helper functions, or received as parameters,
  are not recognised; attach the context to the event at the call site.
- Only the canonical `github.com/rs/zerolog` import path is recognised.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT