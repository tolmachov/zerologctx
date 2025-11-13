# Contributing to zerologctx

Thank you for your interest in contributing to zerologctx! This document provides guidelines and instructions for contributing.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Testing](#testing)
- [Submitting Changes](#submitting-changes)
- [Coding Standards](#coding-standards)
- [Performance Considerations](#performance-considerations)

## Code of Conduct

This project adheres to a code of conduct. By participating, you are expected to uphold this code. Please be respectful and constructive in all interactions.

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally
3. Create a new branch for your changes
4. Make your changes
5. Test your changes
6. Submit a pull request

## Development Setup

### Prerequisites

- Go 1.25.0 or later
- Git

### Setup

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/zerologctx.git
cd zerologctx

# Add upstream remote
git remote add upstream https://github.com/tolmachov/zerologctx.git

# Install dependencies
go mod download

# Install development tools
go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Building

```bash
# Build the CLI tool
go build ./cmd/zerologctx

# Install locally
go install ./cmd/zerologctx
```

## Making Changes

### Branch Naming

Use descriptive branch names:
- `feature/your-feature-name` - for new features
- `fix/bug-description` - for bug fixes
- `docs/documentation-update` - for documentation changes
- `refactor/code-improvement` - for refactoring

### Commit Messages

Write clear commit messages that describe what changed and why:

```
Add support for custom terminal methods

- Implement configurable terminal methods via flags
- Update documentation with examples
- Add tests for new functionality

Fixes #123
```

Guidelines:
- Use present tense ("Add feature" not "Added feature")
- Keep first line under 72 characters
- Add detailed description if needed
- Reference issues when applicable

## Testing

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with race detector
go test -race ./...

# Run specific test
go test -v -run TestAnalyzer

# Run benchmarks
go test -bench=. -benchmem
```

### Writing Tests

- Add test cases to `testdata/src/testpkg/` for integration tests
- Use `// want "..."` comments to specify expected diagnostics
- Add unit tests for helper functions in `zerologctx_test.go`
- Include edge cases and error conditions
- Ensure new features have corresponding tests

Example test case:

```go
// testdata/src/testpkg/my_test.go
func TestNewFeature() {
    ctx := context.Background()

    // Should trigger - missing context
    log.Info().Msg("test") // want "zerolog event missing .Ctx\\(ctx\\).*"

    // Should not trigger - has context
    log.Info().Ctx(ctx).Msg("test")
}
```

### Test Coverage

Maintain test coverage above 90%. Check coverage with:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Submitting Changes

### Before Submitting

1. **Run all tests**: `go test ./...`
2. **Check formatting**: `gofmt -s -w .`
3. **Run linters**:
   ```bash
   go vet ./...
   staticcheck ./...
   ```
4. **Update documentation** if needed
5. **Add/update tests** for your changes
6. **Update CHANGELOG.md** with your changes

### Pull Request Process

1. Update your branch with the latest from `main`:
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. Push your changes to your fork:
   ```bash
   git push origin your-branch-name
   ```

3. Create a pull request on GitHub with:
   - Clear title describing the change
   - Detailed description of what changed and why
   - Link to any related issues
   - Screenshots/examples if applicable

4. Address review feedback:
   - Make requested changes
   - Push new commits (don't force push during review)
   - Respond to comments

5. After approval:
   - Maintainer will merge your PR

### Pull Request Checklist

- [ ] Tests pass locally
- [ ] Code is formatted (`gofmt -s`)
- [ ] No linter warnings (`go vet`, `staticcheck`)
- [ ] Tests added/updated for changes
- [ ] Documentation updated if needed
- [ ] CHANGELOG.md updated
- [ ] Branch is up to date with main

## Coding Standards

### Go Style

Follow standard Go conventions:
- Use `gofmt` for formatting
- Follow [Effective Go](https://golang.org/doc/effective_go)
- Follow [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)

### Documentation

- Add godoc comments to all exported symbols
- Explain complex logic with inline comments
- Document edge cases and assumptions
- Keep comments up to date with code

### Code Organization

- Keep functions focused and small (< 50 lines preferred)
- Use descriptive variable names
- Avoid deep nesting (max 3-4 levels)
- Extract complex conditions into well-named functions

### Error Handling

- Check errors explicitly
- Use defensive programming for AST traversal
- Handle nil cases appropriately
- Provide clear error messages to users

## Performance Considerations

### Optimization Guidelines

- Profile before optimizing
- Run benchmarks to measure impact
- Consider algorithmic improvements first
- Use caching/memoization where appropriate
- Avoid premature optimization

### Running Benchmarks

```bash
# Run all benchmarks
go test -bench=. -benchmem

# Compare before/after
go test -bench=. -benchmem > old.txt
# make changes
go test -bench=. -benchmem > new.txt
benchcmp old.txt new.txt
```

### Performance Goals

- Analyzer should complete in < 1s for typical packages
- Memory usage should be reasonable for large codebases
- No significant performance regression in PRs

## Architecture Notes

### Key Components

1. **Analyzer** (`zerologctx.go`): Main analysis engine
   - Uses two-pass approach
   - Tracks loggers and events with context
   - Reports diagnostics for missing context

2. **Type System**: Type checking and validation
   - `isContextType()`: Validates context types
   - `implementsContextInterface()`: Method set checking
   - Handles custom types and pointers

3. **Chain Analysis**: AST traversal
   - `hasCtxInChain()`: Recursive chain walking
   - Distinguishes Logger.Ctx() from Event.Ctx()
   - Tracks context through variable assignments

4. **Comment Parsing**: nolint directive support
   - `isNoLintComment()`: Parses suppression directives
   - Handles multiple formats and linters

### Adding New Features

When adding features, consider:
- Does it fit the analyzer's purpose?
- Can it be configurable?
- What's the performance impact?
- How to test it comprehensively?
- Does documentation need updates?

## Questions?

If you have questions:
- Open an issue on GitHub
- Check existing issues and PRs
- Read the documentation

Thank you for contributing to zerologctx!