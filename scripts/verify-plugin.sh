#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build_dir="$(mktemp -d)"
golangci_lint_bin="${GOLANGCI_LINT_BIN:-golangci-lint}"
trap 'rm -rf "$build_dir"' EXIT
mkdir -p "$build_dir/bin"

sed "s|REPOSITORY_PATH|$repo_dir|" "$repo_dir/testdata/pluginfixture/custom-gcl.yml" > "$build_dir/.custom-gcl.yml"
(
  cd "$build_dir"
  "$golangci_lint_bin" custom
)

set +e
output="$({
  cd "$repo_dir/testdata/pluginfixture"
  "$build_dir/bin/golangci-lint-zerologctx" run --config .golangci.yml ./...
} 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  printf '%s\n' "$output"
  echo "expected the integration fixture to fail with one zerologctx diagnostic" >&2
  exit 1
fi
grep -F "zerolog output is not proven to carry context before Msg()" <<<"$output"
