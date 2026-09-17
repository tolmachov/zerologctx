# Release checklist

The first public release is `v1.0.0`. Perform these steps only after the release
commit has merged to `main`.

- [ ] Confirm the full GitHub Actions workflow is green on Go 1.26.1 and 1.27.1.
- [ ] Run every command in `CONTRIBUTING.md` from a clean checkout. They are
      all non-mutating, so the tree stays clean.
- [ ] Confirm `./scripts/verify-plugin.sh` builds and executes the custom
      golangci-lint v2.13.2 binary.
- [ ] Confirm `git status --short` is empty.
- [ ] Move the `[Unreleased]` contents to `[1.0.0] - YYYY-MM-DD` and recreate an
      empty `[Unreleased]` section.
- [ ] Create and push the annotated `v1.0.0` tag.
- [ ] Create the GitHub Release from the `v1.0.0` changelog.
- [ ] Verify `go install github.com/tolmachov/zerologctx/cmd/zerologctx@v1.0.0`.
- [ ] Verify the README's `version: v1.0.0` custom-plugin configuration.
- [ ] Close issue #2 ("Linter ignores '//nolint:zerologctx' when followed by
      inline trailing comment") with links to the release and to the change
      that fixed it. First release only; delete this line afterwards.

Do not tag, publish a GitHub Release, push, or close the issue from an
implementation branch.
