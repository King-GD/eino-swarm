#!/usr/bin/env python3
"""One lane of the Go race test split used by .github/workflows/pr-verify.yml.

`go test -race -cover ./...` no longer fits one job: internal/engine alone is most
of it. Splitting a package is only safe when a lost or duplicated test is loud, so
the plan comes from the package's own `go test -list` output and the `-json`
stream of the run is checked against it.

Usage: go-race.py <engine-a|engine-b|cli|rest> <evidence-dir>

The working directory must be the candidate checkout. Only the standard library is
used and nothing is read from the candidate tree, so the workflow runs it from a
separate read-only checkout of the revision that owns the workflow. Its own plan
and raw run files are written 0600, which leaves the umask the Go child inherits
untouched.
"""

import json
import os
import re
import subprocess
import sys
import time
from collections import Counter

ENGINE = "./internal/engine"
CLI = "./cmd/zwai"
INVENTORY = "^(Test|Fuzz|Example)"
# `go test -list` also prints diagnostics ("program not built with -cover") and a
# trailing "ok <pkg>" line; a name is one token with a test prefix and no slash.
# \w keeps the Unicode letters and digits Go allows in an identifier.
NAME = re.compile(r"(Test|Fuzz|Example)\w*")
TIMEOUT = "25m"
LANES = ("engine-a", "engine-b", "cli", "rest")


def say(message):
    print("go-race: " + message, flush=True)


def capture(args):
    """Run one go command and return its stdout, or stop with its diagnostics."""
    proc = subprocess.run(["go", *args], capture_output=True, text=True)
    if proc.returncode != 0:
        raise SystemExit("go " + " ".join(args) + " failed: " + (proc.stderr.strip() or proc.stdout.strip())[-2000:])
    return proc.stdout


def plan(lane):
    """Resolve package ownership and, for the engine, this lane's test names."""
    packages = [line for line in capture(["list", "./..."]).splitlines() if line]
    anchors = [line for line in capture(["list", CLI, ENGINE]).splitlines() if line]
    cli = [pkg for pkg in anchors if pkg.endswith("/" + CLI.strip("./"))]
    engine = [pkg for pkg in anchors if pkg.endswith("/" + ENGINE.strip("./"))]
    if len(cli) != 1 or len(engine) != 1:
        raise SystemExit("anchors do not name exactly one %s and one %s: %r" % (CLI, ENGINE, anchors))
    for pkg in cli + engine:
        if pkg not in packages:
            raise SystemExit("anchor package %s is missing from go list ./..." % pkg)

    info = {"lane": lane, "packages": [], "engine": engine[0], "inventory": [], "tests": [], "filter": None}
    if lane in ("engine-a", "engine-b"):
        start = time.monotonic()
        listing = capture(["test", "-race", "-cover", "-list", INVENTORY, engine[0]])
        names = [line.strip() for line in listing.splitlines() if NAME.fullmatch(line.strip())]
        inventory = sorted(names)
        if not inventory:
            raise SystemExit("go test -list %s reported no Test/Fuzz/Example names" % engine[0])
        if len(inventory) != len(set(inventory)):
            raise SystemExit("go test -list %s reported a duplicated test name" % engine[0])
        # Alternating sorted positions: deterministic, disjoint and complete without
        # a per-test timing model, which the old whole-package log never produced.
        # Both lanes derive the same split from the same source.
        assigned = inventory[0::2] if lane == "engine-a" else inventory[1::2]
        if not assigned:
            raise SystemExit("lane %s owns no tests of %s" % (lane, engine[0]))
        info.update(packages=[engine[0]], inventory=inventory, tests=assigned)
        # Anchored: an unanchored name would also select tests that merely start
        # with it, and a duplicated test would hide a broken plan.
        info["filter"] = "^(" + "|".join(re.escape(name) for name in assigned) + ")$"
        say("lane=%s phase=inventory package=%s names=%d assigned=%d duration_ms=%d" % (
            lane, engine[0], len(inventory), len(assigned), int((time.monotonic() - start) * 1000)))
    elif lane == "cli":
        info["packages"] = [cli[0]]
    else:
        info["packages"] = [pkg for pkg in packages if pkg not in (cli[0], engine[0])]
        if not info["packages"]:
            raise SystemExit("the rest lane has no packages")
    return info


def consume(line, stats):
    try:
        event = json.loads(line)
    except json.JSONDecodeError:
        stats["unparsed"] += 1
        return
    action, package, test = event.get("Action"), event.get("Package"), event.get("Test")
    if action in ("output", "build-output"):
        if action == "build-output":
            package = event.get("ImportPath")
        if test is None or action == "build-output":
            stats["pkg_out"].setdefault(package, []).append(event.get("Output", ""))
        else:  # subtests inherit their parent's result, so the output stays with it
            stats["test_out"].setdefault((package, test.split("/")[0]), []).append(event.get("Output", ""))
    elif action == "run" and test and "/" not in test:
        stats["ran"][(package, test)] += 1
    elif action in ("pass", "fail", "skip") and test and "/" not in test:
        stats["done"][(package, test)] += 1
        stats["actions"][(package, test)] = action
    elif action in ("pass", "fail", "skip") and test is None:
        # Only a record without a Test is a package conclusion; a nested subtest
        # terminal event must never stand in for it.
        stats["pkg"].setdefault(package, (action, event.get("Elapsed") or 0.0))
        stats["pkg_events"][package] += 1


def verify(info, stats, status):
    """Every check assumes Go succeeded; a failing run keeps Go's own verdict."""
    if status != 0:
        return []
    planned, reported = set(info["packages"]), set(stats["pkg"])
    findings = ["package %s has no terminal pass/fail/skip record" % pkg for pkg in sorted(planned - reported)]
    findings += ["package %s reported a result but is not in this lane" % pkg for pkg in sorted(reported - planned)]
    findings += ["package %s reported %d terminal records" % (pkg, n)
                 for pkg, n in sorted(stats["pkg_events"].items()) if n > 1]
    if info["tests"]:
        assigned = set(info["tests"])
        ran = {name: n for (pkg, name), n in stats["ran"].items() if pkg == info["engine"]}
        done = {name: n for (pkg, name), n in stats["done"].items() if pkg == info["engine"]}
        findings += ["planned test %s never started" % name for name in sorted(assigned - set(ran))]
        findings += ["planned test %s has no terminal record" % name for name in sorted(assigned - set(done))]
        findings += ["test %s ran but is not in this lane" % name for name in sorted(set(ran) - assigned)]
        findings += ["test %s reported a result but is not in this lane" % name for name in sorted(set(done) - assigned)]
        findings += ["test %s started %d times" % (name, n) for name, n in sorted(ran.items()) if n > 1]
        findings += ["test %s reported %d terminal records" % (name, n) for name, n in sorted(done.items()) if n > 1]
    return findings


def private(path):
    """Create or truncate one evidence file 0600; the Go child's umask stays as it is."""
    return os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w")


def main(argv):
    if len(argv) != 2 or argv[0] not in LANES:
        raise SystemExit("usage: go-race.py <%s> <evidence-dir>" % "|".join(LANES))
    lane, evidence = argv
    os.makedirs(evidence, exist_ok=True)
    info = plan(lane)
    plan_path = os.path.join(evidence, "plan.json")
    with private(plan_path) as handle:
        handle.write(json.dumps(info, indent=2, sort_keys=True) + "\n")
    say("lane=%s phase=plan packages=%d inventory=%d tests=%d filter_len=%d plan=%s" % (
        lane, len(info["packages"]), len(info["inventory"]), len(info["tests"]), len(info["filter"] or ""), plan_path))

    cover = os.path.join(evidence, "cover-" + lane + ".out")
    args = ["test", "-race", "-cover", "-count=1", "-timeout", TIMEOUT, "-coverprofile", cover]
    if info["filter"]:
        args += ["-run", info["filter"]]
    args += ["-json", *info["packages"]]
    stats = {"pkg": {}, "pkg_events": Counter(), "ran": Counter(), "done": Counter(), "actions": {},
             "pkg_out": {}, "test_out": {}, "unparsed": 0}
    start = time.monotonic()
    stderr_path = os.path.join(evidence, "go-test-stderr.log")
    with private(os.path.join(evidence, "go-test-json.log")) as log, private(stderr_path) as stderr_log:
        proc = subprocess.Popen(["go", *args], stdout=subprocess.PIPE, stderr=stderr_log, text=True)
        for line in proc.stdout:
            log.write(line)
            consume(line, stats)
        status = proc.wait()
    if status < 0:  # killed by a signal: a failure, never a success
        status = 1
    elapsed = int((time.monotonic() - start) * 1000)
    if os.path.isfile(cover):
        os.chmod(cover, 0o600)  # `go test` created it; a coverage profile is evidence too
    if os.path.getsize(stderr_path):
        with open(stderr_path) as handle:
            sys.stdout.write(handle.read())

    # Package completion times, then failure output verbatim; the JSON log keeps
    # everything else.
    for package, (action, seconds) in sorted(stats["pkg"].items()):
        say("lane=%s package=%s result=%s elapsed_s=%.1f" % (lane, package, action, seconds))
        if action == "fail":
            sys.stdout.write("".join(stats["pkg_out"].get(package, [])))
    for (package, name), action in sorted(stats["actions"].items()):
        if action == "fail":
            sys.stdout.write("".join(stats["test_out"].get((package, name), [])))

    findings = verify(info, stats, status)
    if status == 0 and not os.path.isfile(cover):
        findings.append("coverage profile %s is missing" % cover)
    counted = "packages_reported=%d/%d" % (len(stats["pkg"]), len(info["packages"]))
    if info["tests"]:  # only a filtered lane has a planned test count to compare
        counted += " tests_reported=%d/%d" % (len(stats["done"]), len(info["tests"]))
    say("lane=%s phase=done exit=%d duration_ms=%d %s unparsed_events=%d findings=%d" % (
        lane, status, elapsed, counted, stats["unparsed"], len(findings)))
    for finding in findings[:20]:
        print("::error title=go-race %s verification::%s" % (lane, finding), flush=True)
    if len(findings) > 20:
        say("lane=%s phase=done suppressed_findings=%d" % (lane, len(findings) - 20))
    if status:  # a real Go status is the lane's status, never replaced
        return status
    return 3 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
