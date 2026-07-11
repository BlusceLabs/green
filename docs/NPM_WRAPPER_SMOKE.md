# npm Wrapper Smoke Checklist

Run this checklist when a PR changes npm distribution files such as
`package.json`, `bin/green.js`, Go build or release tooling, or the npm `bin`
wrapper.

## Required Checks

```bash
go test ./internal/npmwrapper ./internal/release
go run ./cmd/green-release build
go run ./cmd/green-release smoke
```

Also run the Go checks when the PR changes Go entrypoint, CLI, or release
artifact behavior:

```bash
go test ./...
go run ./cmd/green-release build
go run ./cmd/green-release smoke
```

## Checklist

- `package.json` has the expected package name (`@BlusceLabs/green`), version,
  `bin.green` entry, and exactly one `scripts` entry — the `postinstall` hook.
- `scripts/postinstall.mjs` resolves the correct release asset name/URL per
  platform (`green_INSTALL_DRY_RUN=1` prints the plan), verifies the downloaded
  archive's SHA-256, and extracts only the known binary basenames into place.
  `green_SKIP_DOWNLOAD=1` opts out cleanly (exit 0) and an unsupported
  platform/arch is a non-fatal skip.
- The wrapper binary resolves through the package `bin` entry and
  `node_modules/.bin` in a package-install smoke test.
- The built binary exits 0 for `green --version` or `green --help`.
- `green --version` reports `green <package.json version>`.
- Release packaging still emits the expected archive and checksum names when
  package release files change.
