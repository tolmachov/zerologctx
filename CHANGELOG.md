# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - Initial Release

### Features
- Static analysis linter for zerolog that ensures events include context via Ctx()
- Support for all terminal methods: Msg(), Msgf(), MsgFunc(), and Send()
- Custom context type detection (types embedding context.Context)
- Logger with embedded context tracking
- Event variable context tracking through assignments
- Type-safe context validation using method set introspection
- Recursive chain analysis for fluent interfaces with memoization for performance
- nolint directive support with multiple formats
- golangci-lint plugin integration
- Standalone CLI tool
- Comprehensive test suite with 92%+ coverage
- Performance benchmarks
- Enhanced CI pipeline with go vet, staticcheck, and coverage reporting

### Technical Details
- Go version requirement: 1.25.0 (toolchain 1.25.4)
- Zero external dependencies (except golang.org/x/tools)
- Optimized performance with AST caching
- Handles edge cases: struct fields, function returns, custom types, global loggers
- Support for multiple integration patterns: standalone, golangci-lint, library import