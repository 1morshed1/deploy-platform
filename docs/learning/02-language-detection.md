# 02 — Language detection

**Code:** `internal/github/detect.go`, `internal/github/detect_test.go`

Before the platform can build a repo, it must know *what kind* of repo it is: Go?
Node? Python? A static site? That decision picks the Dockerfile template
(learning doc 03).

## The trick: don't clone, just list the root

Cloning a repo to inspect it would be slow and would run on the API request
path — exactly what we're trying to avoid. Instead we make **one GitHub REST API
call** to list the files at the repository root:

```
GET https://api.github.com/repos/{owner}/{repo}/contents/?ref={branch}
```

The response is a JSON array of `{name, type}` entries. We look for **marker
files** — the conventional files that identify an ecosystem:

| Marker file | Language | Why it's the marker |
|---|---|---|
| `go.mod` | `go` | Every modern Go module has one at its root. |
| `package.json` | `node` | The manifest of every Node/npm project. |
| `requirements.txt`, `Pipfile`, `pyproject.toml` | `python` | The three common Python dependency manifests. |
| `index.html` | `static` | A site with no build step — just files to serve. |

## Precedence matters

Read the `switch` in `Detect`. The order is deliberate:

```
go > node > python > static
```

A Node app that serves HTML may have *both* `package.json` and `index.html`. If
we checked `index.html` first we'd wrongly treat it as a static site and never
run `npm run build`. By checking richer ecosystems first, the most specific match
wins. This is the kind of decision that's obvious once stated and mysterious if
undocumented — hence the comment in the code and this table.

## Error handling is a feature

`Detect` distinguishes three failure modes, and each maps to a helpful outcome:

- **`ErrUnknownLanguage`** — none of the markers were found → the user gets
  "could not detect language," telling them to add a recognized manifest.
- **`ErrRepoNotAccessible`** (HTTP 404) — the repo is private or the branch is
  wrong → "repository or branch not accessible."
- **Rate-limited (HTTP 403/429)** — unauthenticated GitHub calls are capped at 60
  per hour → the error tells the operator to set `GITHUB_TOKEN` (5000/hour).

Surfacing these at deploy time (a fast `400`) is far better than accepting the
deploy and failing three minutes into a build.

## Why the code is shaped for testing

`Detector` holds its `baseURL` and `httpClient` as **fields**, not package
globals. That one choice is what makes `detect_test.go` possible: the tests spin
up an `httptest.Server` that returns canned directory listings and point the
detector at it. We test every branch of the precedence logic and the 404 path
**without ever touching the real GitHub API** — the tests are fast, hermetic, and
run offline. This is dependency injection in miniature, and it's a pattern you'll
see throughout the codebase.

Run them:

```bash
go test ./internal/github/ -v
```
