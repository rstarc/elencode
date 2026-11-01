# Development

## Versioning

Releases use [semantic versioning](https://semver.org) with a `v` prefix: `v0.3.1`.

`elencode version` prints the version as one line, along with the commit, build time, Go
version and platform:

```
$ elencode version
elencode v0.3.1 (commit d793654e6c56, built 2026-08-06T07:21:00Z, go1.26.1, linux/amd64)
```

### Releasing

A release is a tag on `main` that you push by hand. There is no schedule: tag when a set of
changes feels finished. `make build` and the version badge in the README read the tag, so
nothing else needs updating.

Until `v1.0.0`, choose the next version like this:

- New features, or changes that break how elencode is used: raise the middle number
  (`v0.2.0` to `v0.3.0`).
- Fixes only: raise the last number (`v0.3.0` to `v0.3.1`).

To release:

1. Switch to an up-to-date `main` and check that the last CI run passed:

   ```
   git switch main && git pull
   gh run list --workflow ci.yml --branch main --limit 1
   ```

2. List what the release contains:

   ```
   git log --oneline "$(git describe --tags --abbrev=0)"..HEAD
   ```

3. Tag and push:

   ```
   git tag -a v0.3.0 -m "v0.3.0"
   git push origin v0.3.0
   ```

4. Create the GitHub release:

   ```
   gh release create v0.3.0 --generate-notes
   ```

   Generated notes list merged pull requests only. Commits pushed straight to `main`
   appear only behind the "Full Changelog" link, so edit the notes to summarise them.
