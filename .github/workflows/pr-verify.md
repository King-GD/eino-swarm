# `pr-verify.yml`

Pull-request verification for this repository. It runs on every pull request
whose target branch is `ci/verify` (opened, synchronize, reopened, drafts
included). The token is read-only (`contents: read`), no job uses a secret, and
the checkout writes no credentials into `.git/config`. Runs are grouped by pull
request number. A new run cancels any older active run for the same pull request,
including after the base advances. Each job checks out the contributor's tree at
`github.event.pull_request.head.sha`. The Go suite runs as one static job plus
four race lanes. A red lane leaves its siblings running (`fail-fast: false`).

| job | what it runs |
|---|---|
| Phone bundle | `cd mobile && npm ci`, `npm run build`, `npm test`. Asserts `frontend/node_modules` is absent before and after, so the phone bundle must pass with no PC install on the runner. |
| PC/Web checks | `cd frontend && npm ci`, `npm run lint`, `make frontend`, `npm test`. The production bundle is built before the unit tests so a bundle failure reports on its own. |
| PC/Web offline E2E | A separate checkout: `npm ci`, `make frontend`, `go build -o "$RUNNER_TEMP/zwai" ./cmd/zwai`, `npx playwright install chromium`, `npm run e2e`. The complete default suite runs unfiltered; no spec is skipped or narrowed. |
| Go static and build checks | `make fmt-check`, `make vet`, `go build ./...`. |
| Go race tests | Four lanes on one image. `engine-a`/`engine-b` split `internal/engine` by its own test inventory; `cli` owns `cmd/zwai`; `rest` owns every other package. Each lane records Go's JSON output and its coverage profile. The `cli` lane prewarms the test fixture's race build. |
| Model checks (TLC) | `python3 -u tools/model_check.py --fetch --out "$RUNNER_TEMP/model-check"`: every case the tool lists, with its pinned `tla2tools.jar` SHA-256 check. No case filter, skip or allowed failure. |
| Documentation and Python tools | `make docs-check`. |
| Upstream merge check | One `git fetch` of `LubyRuffy/eino-swarm` `refs/heads/main`; records `git rev-parse HEAD` and the fetched `FETCH_HEAD`, then runs `git merge-tree --write-tree --name-only <upstream-base> HEAD`. Nonzero status, both revisions and the complete `merge-tree` output go to the step summary and the job fails. It never merges, commits or pushes. |

## Runners

| image | jobs | why |
|---|---|---|
| `macos-15-intel` | Go static and build checks, Go race tests | The suite asserts the project's own desktop command set, macOS update/error behavior and lockfile timing, so it runs the untagged macOS build on the image that supplies the native libraries. Node 24 is installed for the frontend entry's toolchain, and every race lane keeps the existing 25-minute package timeout and 60-minute job timeout. |
| `macos-15-intel` | PC/Web offline E2E | The suite asserts a macOS settings titlebar (`render-probe-cover.spec.ts`) that the baseline asserts under the same platform condition, so Linux Chromium cannot satisfy it. The macOS image supplies the native libraries and installs only Chromium. The Intel image also has capacity while macOS ARM is short, and the full E2E suite runs there unchanged. |
| `macos-15` | Documentation and Python tools | `make docs-check` has two tool regressions that read macOS process-identity symbols (`libc.sysctl`, and `/proc/1/exe` being refused). On this ARM64 image the linker ad-hoc signs the test's own freshly built fixtures, which the real-code-signature assertion checks. The job also installs `go` on PATH for the real-writer compile test, and runs the full tool suite unchanged. |
| `ubuntu-latest` | Phone bundle, PC/Web checks, Upstream merge check | These jobs link no native shell package. |
| `ubuntu-24.04` | Model checks (TLC) | Java 21 (Temurin) and Python 3 run `tools/model_check.py`; the tool fetches and SHA-256-checks the pinned `tla2tools.jar` itself, so no other action downloads it. |

Only the offline E2E job sets `GOFLAGS=-tags=server`, which selects the
headless server entry; Playwright's web server inherits it, and the job compiles
`./cmd/zwai` before Playwright starts so the suite's 180-second web-server start
budget is not spent on a cold build. The Go jobs deliberately run the untagged
build: the desktop command dispatch, the macOS update error and the lockfile
timing regressions describe the project's own desktop build, not the server
variant. macOS supplies the native libraries, so neither job installs Linux
packages; the E2E job installs only Chromium.

The phone, frontend and Go jobs use Node 24, the version checked against both
lockfiles; the Go jobs cache against `frontend/package-lock.json` for the
frontend entry's toolchain. The Go jobs and the docs job read the version from
`go.mod`; the docs job also needs Python 3 from the image and `go` on PATH for
the real-writer compile test.

## Go race jobs

| lane | work |
|---|---|
| `engine-a`, `engine-b` | Split the sorted `internal/engine` inventory by alternating positions. |
| `cli` | All tests in `cmd/zwai`. |
| `rest` | All other packages returned by `go list ./...`. |

The engine inventory comes from `go test -race -cover -list '^(Test|Fuzz|Example)'`.
The exact-name filters include each assigned test, its subtests, fuzz seeds, and
examples. The first split balances test counts because the old job did not record
individual durations. Each engine lane starts a separate package process.

Every lane keeps `-race -cover -timeout 25m` and adds `-count=1` to run the
tests even when Go has cached a result. The driver preserves Go's failure status.
For a successful Go command, the driver requires one terminal result per planned
package and, for engine, one start and one terminal result per planned top-level
case. Missing, extra, or duplicate results fail the lane. Expected skips remain
visible.

The driver runs from a sibling checkout of `github.workflow_sha`. The working
directory is the clean candidate checkout at the pull request's exact head SHA.
Each lane uploads its plan, raw Go JSON, stderr, and coverage profile for seven
days as `go-race-<lane>-pr-<number>-<attempt>`. Engine coverage is partial per
lane. Use the blocks from both profiles for the same commit to compute combined
coverage.

The CLI warm-up runs `go build -race -trimpath` from `cmd/zwai`, matching the
nested test fixture's build flags. On a cache miss, a successful warm-up saves
the Go build and module caches before testing. A warm-up failure is logged and
the test suite still runs. The cache key includes runner OS and architecture,
Go version, and the dependency hash. GitHub scopes PR caches to their own
merge refs, so reuse is available on later runs of the same validation PR.

## Reading a failure

The job groups are independent, so one red result does not suppress the others'
results: a failing static check never blocks a race lane, and the four race
lanes run with `fail-fast: false` so a red lane cannot cancel its siblings. Job
logs are ordinary; each race lane prints package completion times and failure
output, and keeps the full JSON stream in its artifact. When the offline E2E
suite fails, the run also keeps `frontend/test-results/` (traces and error
context) as the `e2e-evidence-…` artifact; inspect a trace with
`cd frontend && npx playwright show-trace <file>`. A failed TLC run keeps every
failing case's `tlc.out` and `case.cfg` as the `model-check-evidence-…`
artifact; the per-case state graphs and copied modules stay out of it.

A race lane that reports `::error title=go-race <lane> verification::…` did not
run the tests it planned, even if Go itself exited 0. Open its
`go-race-<lane>-…` artifact and compare `plan.json` with the `run`/`pass` events
in `go-test-json.log`; the annotation names the missing or extra package or
test. A lane that exhausts the 25-minute package timeout reports Go's own panic
and keeps everything it produced up to that point.

## Running the same checks locally

On a Linux machine the local Go lines need `libgtk-4-dev libwebkitgtk-6.0-dev
libx11-dev` from `apt-get`; CI runs the Go jobs on macOS, where the system
libraries are enough. The model checks run exactly the same script as the job,
with `--fetch --out "$RUNNER_TEMP/model-check"` there so a failed case keeps its
`tlc.out` and `case.cfg`.

```bash
make fmt-check vet test-go docs-check
(cd frontend && npm ci && npm run lint)
make frontend
(cd frontend && npm test)
GOFLAGS=-tags=server go build -o "$TMPDIR/zwai" ./cmd/zwai
(cd frontend && npx playwright install chromium && GOFLAGS=-tags=server npm run e2e)
(cd mobile && npm ci && npm run build && npm test)
make model-check
python3 .github/ci/go-race.py engine-a "$TMPDIR/go-race-engine-a"
```

Not part of this workflow, because each needs a toolchain the hosted runners do
not have: `make native-smoke` (a macOS guest), the Xcode and Gradle phone suites,
and `make release-check` / `make release`. They run from their own documented
entry points.
