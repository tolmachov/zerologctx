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

Add to your `.golangci.yml` configuration:

```yaml
linters-settings:
  custom:
    zerologctx:
      path: $(go env GOPATH)/bin/zerologctx
      description: Ensures zerolog events include context
      original-url: github.com/tolmachov/zerologctx

linters:
  enable:
    - zerologctx
```

Then run:

```bash
golangci-lint run
```

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
      uses: actions/setup-go@v4
      with:
        go-version: '1.25'
    
    - name: Install zerologctx linter
      run: go install github.com/tolmachov/zerologctx/cmd/zerologctx@latest
    
    - name: Run linter
      run: zerologctx ./...
```

## What It Checks

This linter detects when zerolog events use terminal methods like `.Msg()` or `.Send()` without first calling `.Ctx(ctx)` in the method chain.

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

## Integration with Editors

### VS Code

Add to your `.vscode/settings.json`:

```json
{
  "go.lintTool": "golangci-lint",
  "go.lintFlags": [
    "--fast",
    "--enable=zerologctx"
  ]
}
```

### GoLand/IntelliJ IDEA

1. Go to Preferences/Settings → Tools → File Watchers
2. Add a new File Watcher with:
   - Program: `$GoBinDirs$/zerologctx`
   - Arguments: `$FilePath$`
   - Working directory: `$ProjectFileDir$`

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT