# `pr-verify.yml`

Pull-request verification for this repository. It runs on every pull request
whose target branch is `ci/verify` (opened, synchronize, reopened, drafts
included). The token is read-only (`contents: read`), no job uses a secret, and
the checkout writes no credentials into `.git/config`. A newer push to the same
pull request on the same base revision cancels the older run; when the base SHA
changes, the earlier run keeps running so its cloud results survive a separate
CI-only revision. Every job checks out `github.event.pull_request.head.sha`, so
a failure names the contributor's own tree rather than the synthetic merge
commit.

| job | what it runs |
|---|---|
| Phone bundle | `cd mobile && npm ci`, `npm run build`, `npm test`. Asserts `frontend/node_modules` is absent before and after, so the phone bundle must pass with no PC install on the runner. |
| PC/Web checks | `cd frontend && npm ci`, `npm run lint`, `make frontend`, `npm test`. The production bundle is built before the unit tests so a bundle failure reports on its own. |
| PC/Web offline E2E | A separate checkout: `npm ci`, `make frontend`, `go build -o "$RUNNER_TEMP/zwai" ./cmd/zwai`, `npx playwright install --with-deps chromium`, `npm run e2e`. |
| Go checks | `make fmt-check`, `make vet`, `go build ./...`, `make test-go` (race and coverage, 25-minute package timeout). |
| Documentation and Python tools | `make docs-check`. |
| Upstream merge check | One `git fetch` of `LubyRuffy/eino-swarm` `refs/heads/main`; records `git rev-parse HEAD` and the fetched `FETCH_HEAD`, then runs `git merge-tree --write-tree --name-only <upstream-base> HEAD`. Nonzero status, both revisions and the complete `merge-tree` output go to the step summary and the job fails. It never merges, commits or pushes. |

## Runners

| image | jobs | why |
|---|---|---|
| `ubuntu-24.04` | Go checks, PC/Web offline E2E | Some packages still link the native shell's Gtk 4 / WebKit 6 libraries even with the `server` tag, so both jobs install `libgtk-4-dev libwebkitgtk-6.0-dev libx11-dev` (their dependencies provide glib and libsoup) before the first Go build or test. The image is pinned because the dev package names follow the Ubuntu release. |
| `ubuntu-latest` | Phone bundle, PC/Web checks, Upstream merge check | These jobs link no native shell package. |
| `macos-15-intel` | Documentation and Python tools | Two tool regressions exercise macOS process identity (`libc.sysctl`, and `/proc/1/exe` being refused), so `make docs-check` needs the matching system symbols rather than a Linux-only environment. The Intel image is pinned because GitHub's macOS ARM hosted capacity currently cancels these runs; the full tool suite still runs there unchanged. |

The Go and offline E2E jobs set `GOFLAGS=-tags=server`, which selects the
headless server entry; Playwright's web server inherits it. CGO stays enabled
because the race tests need it. The E2E job compiles `./cmd/zwai` before
Playwright starts so the suite's 180-second web-server start budget is not
spent on a cold build.

The Node jobs use Node 24, the version checked against both lockfiles. The Go
jobs read the version from `go.mod`; `make docs-check` needs Python 3, which the
macOS image provides.

## Reading a failure

The six jobs are independent, so one red result does not suppress the others'
results. Job logs are ordinary. When the offline E2E suite fails, the run also
keeps `frontend/test-results/` (traces and error context) as the
`e2e-evidence-…` artifact; inspect a trace with
`cd frontend && npx playwright show-trace <file>`.

## Running the same checks locally

On a Linux machine the Go lines need `libgtk-4-dev libwebkitgtk-6.0-dev
libx11-dev` from `apt-get` first, exactly as the two pinned jobs install them.

```bash
make fmt-check vet test-go docs-check
(cd frontend && npm ci && npm run lint)
make frontend
(cd frontend && npm test)
GOFLAGS=-tags=server go build -o "$TMPDIR/zwai" ./cmd/zwai
(cd frontend && npx playwright install chromium && GOFLAGS=-tags=server npm run e2e)
(cd mobile && npm ci && npm run build && npm test)
```

Not part of this workflow, because each needs a toolchain the Linux runner does
not have: `make model-check` (Java and the pinned TLC jar), `make native-smoke`
(a macOS guest), the Xcode and Gradle phone suites, and `make release-check` /
`make release`. They run from their own documented entry points.
