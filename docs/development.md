# Development

## Versioning

Releases use [semantic versioning](https://semver.org) with a `v` prefix: `v0.3.1`.
The prefix is not cosmetic — Go's module system only recognises tags in that form.

An annotated git tag is the only source of truth. No version is written to a file:

```sh
git tag -a v0.3.1 -m v0.3.1
git push origin v0.3.1
```

While the major version is `0`, the API is unstable and minor bumps may break things.

`make build` stamps the version into the binary with
`git describe --tags --always --dirty`, so what it reports depends on where the
build sits relative to the nearest tag:

| Build point | Reported as |
| --- | --- |
| on a tag | `v0.3.1` |
| 4 commits past a tag | `v0.3.1-4-gd793654` |
| no tags in history | `d793654` — the short commit |
| uncommitted changes | the above plus `-dirty` |

`elencode version` prints it as one line, along with the commit, build time, Go
version and platform:

```
$ elencode version
elencode v0.3.1 (commit d793654e6c56, built 2026-08-06T07:21:00Z, go1.26.1, linux/amd64)
```

Those details come from `runtime/debug.ReadBuildInfo`, which Go stamps into any
binary built inside a checkout. A binary built without the Makefile therefore still
reports its commit rather than `unknown`.

## Checks

`make test`, `make lint` and `make vuln` are what CI runs; `make fmt` applies the
formatting `make lint` checks for. The lint and vuln targets install their pinned
tool into `./bin` on first use.

CI (`.github/workflows/ci.yml`) calls the Go toolchain directly rather than going
through the Makefile, so the two need to be kept in step when a tool version
changes.

golangci-lint is built from source against this module's toolchain instead of using
`golangci-lint-action`: the released binaries are built with an older Go than
`go.mod` targets, and golangci-lint refuses to run in that case.

The `go` directive in `go.mod` is what CI installs, so it doubles as the toolchain
floor. govulncheck reports standard library vulnerabilities against that version,
which means a Go patch release is sometimes the fix for a red `vuln` job.
