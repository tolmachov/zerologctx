# Using zerologctx with golangci-lint

This document describes how to integrate the `zerologctx` linter with [golangci-lint](https://golangci-lint.run/).

## Configuration

To use `zerologctx` with golangci-lint, you need to configure it as a custom linter in your `.golangci.yml` file.

### Basic Configuration

Add the following to your `.golangci.yml` file:

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

### Project-specific Configuration

If you want to ensure all developers use the same version of the linter, you can include it as a development dependency and reference it from your tools directory:

1. First, add the linter to your project:

```bash
go get -u github.com/tolmachov/zerologctx
```

2. Create a `tools.go` file (if you don't have one already):

```go
//go:build tools
// +build tools

package tools

import (
    _ "github.com/tolmachov/zerologctx/cmd/zerologctx"
)
```

3. Update your `.golangci.yml` to use the local version:

```yaml
linters-settings:
  custom:
    zerologctx:
      path: ${GOPATH}/pkg/mod/github.com/tolmachov/zerologctx@v0.1.0/cmd/zerologctx/zerologctx
      description: Ensures zerolog events include context
      original-url: github.com/tolmachov/zerologctx

linters:
  enable:
    - zerologctx
```

## Running

Once configured, you can run golangci-lint as usual:

```bash
golangci-lint run
```

## Advanced Configuration

You can also add additional configuration in your `.golangci.yml` file:

```yaml
issues:
  # Exclude specific issues
  exclude:
    # Exclude some zerologctx issues in test files
    - path: _test\.go
      linters:
        - zerologctx
      
  # Add custom text into report
  text-templates:
    - linter: zerologctx
      text: "{{ .Text }} - Read more about context propagation in logs at https://our-company-docs.example.com/logging-standards.html"
```

## CI/CD Integration

For continuous integration, add golangci-lint with zerologctx to your CI workflow. Here's an example GitHub Actions workflow:

```yaml
name: Lint

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
        go-version: '1.24'
    
    - name: Install zerologctx
      run: go install github.com/tolmachov/zerologctx/cmd/zerologctx@latest
    
    - name: Run golangci-lint
      uses: golangci/golangci-lint-action@v4
      with:
        version: latest
        args: --timeout=5m
```

## Troubleshooting

### Common Issues

1. **Linter not found**
   
   Ensure the path to the zerologctx binary is correct. You can verify it by running:
   
   ```bash
   which zerologctx
   ```

2. **Version mismatch**
   
   If you're using a specific version in your configuration, make sure it matches the version installed:
   
   ```bash
   go list -m github.com/tolmachov/zerologctx
   ```

3. **Permissions issues**
   
   Make sure the binary is executable:
   
   ```bash
   chmod +x $(which zerologctx)
   ```

For more information on custom linters in golangci-lint, see the [official documentation](https://golangci-lint.run/usage/linters/#custom-linters).