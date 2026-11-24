#!/usr/bin/env python3
"""Accept work-record merge, pruning and restore in one isolated Mac window."""
import argparse
from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.request

import native_macos_smoke as smoke
from native_work_records_fixture import PHASES, REFERENCE, STATUSES, TITLE, WorkFixture, call_id


runner = smoke.runner
SCENARIO_SECONDS = 80
CASE_SECONDS = 100
CONTROL_SOURCE = Path(__file__).with_name("native_generation_interruption_ax.m")
CONTROLS = {"AXButton", "AXRadioButton", "AXCheckBox"}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify_controls(path, expected):
    if not expected or digest(path) != expected or not os.access(path, os.X_OK):
        raise ValueError("the prepared native control helper identity differs")
    return path


def api(origin, path, deadline, body=None, status=200):
    if not path.startswith("/api/"):
        raise ValueError("the native fixture may only address its private engine API")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), smoke.NoRedirect())
    raw = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(smoke.engine_origin(origin) + path, data=raw, headers={
        "Accept": "application/json", "Content-Type": "application/json",
    })
    with opener.open(request, timeout=min(3, deadline.remaining("HTTP " + path))) as response:
        if response.status != status:
            raise RuntimeError("unexpected private API HTTP status " + str(response.status))
        value = json.load(response)
    if not isinstance(value, dict):
        raise RuntimeError("private API response is not an object")
    return value


def preseed_engine(stack, environment, processes, scope, receipt, work_deadline):
    data = Path(environment["ZWAI_NATIVE_DATA"])
    evidence = Path(environment["ZWAI_NATIVE_EVIDENCE"])
    executable = Path(environment["ZWAI_NATIVE_APP"]) / "Contents/MacOS/zwai"
    deadline = smoke.Deadline(min(20, work_deadline - time.monotonic()))
    with runner.private_binary(evidence / "fixture-engine.stdout.log") as out, \
            runner.private_binary(evidence / "fixture-engine.stderr.log") as err:
        engine = subprocess.Popen([str(executable), "engine", "--addr", "127.0.0.1:0", "--data-dir", str(data)],
                                  cwd=environment["ZWAI_NATIVE_WORKSPACE"], env=environment, stdout=out, stderr=err,
                                  start_new_session=True)
        processes.append(engine)
        scope.groups.add(engine.pid)
    while True:
        deadline.remaining("fixture engine ready")
        if engine.poll() is not None:
            raise RuntimeError("private fixture engine exited before readiness")
        try:
            lease = json.loads((data / "engine.json").read_text())
        except FileNotFoundError:
            deadline.pause("fixture engine lease")
            continue
        if lease.get("pid") != engine.pid or lease.get("mock") is not False:
            raise RuntimeError("fixture engine lease has another owner or model mode")
        argv, marker = runner.process_identity(engine.pid)
        if marker != receipt["case_id"] or not argv or Path(argv[0]).resolve() != executable.resolve():
            raise RuntimeError("headless fixture engine process identity differs")
        origin = smoke.engine_origin(lease["url"])
        try:
            meta = api(origin, "/api/meta", deadline)
        except urllib.error.URLError as error:
            if not isinstance(error.reason, (ConnectionError, TimeoutError)):
                raise
            deadline.pause("fixture engine listener")
            continue
        if meta.get("mock") is not False or meta.get("data_dir") != str(data):
            raise RuntimeError("fixture engine HTTP identity differs")
        if meta.get("startup") != "ready":
            deadline.pause("fixture engine restoration")
            continue
        break
    # The parent owns the stream until the runner finishes. The scenario
    # additionally requires an acknowledged presence from the actual desktop PID.
    presence = api(origin, "/api/presence", deadline, {"surface": "tui"})
    presence_id = presence.get("id")
    if not isinstance(presence_id, str) or not presence_id or "/" in presence_id:
        raise RuntimeError("private setup presence has no identity")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), smoke.NoRedirect())
    held = stack.enter_context(opener.open(origin + "/api/presence/" + presence_id,
                               timeout=min(3, deadline.remaining("fixture setup presence"))))
    if held.status != 200 or held.readline() != b": held\n":
        raise RuntimeError("private setup presence was not acknowledged")

    project_name, thread_title, peer_title = "Native evidence", "Source conversation", "Peer conversation"
    project = api(origin, "/api/projects", deadline, {"name": project_name, "memory_enabled": False}, 201)["project"]
    if project.get("name") != project_name or project.get("nuwa_enabled") is not False:
        raise RuntimeError("work fixture must be an ordinary project")
    def create(title):
        row = api(origin, "/api/threads", deadline, {"title": title, "project_id": project["id"]}, 201)["thread"]
        if row.get("project_id") != project["id"] or row.get("title") != title:
            raise RuntimeError("fixture conversation was not created in the ordinary project")
        return row["id"]
    thread_id, peer_id = create(thread_title), create(peer_title)
    if not thread_id or not peer_id or thread_id == peer_id:
        raise RuntimeError("fixture needs two distinct conversation identities")
    runner.save_json(evidence / "native-work-fixture.json", {
        "case_id": receipt["case_id"], "project_id": project["id"], "thread_id": thread_id,
        "peer_id": peer_id, "project_name": project_name, "thread_title": thread_title,
        "fixture_engine_pid": engine.pid, "origin": origin, "candidate_executable_sha256": digest(executable),
    })


def prepare_fixture(stack, lock, receipt, environment, processes, scope, work_deadline):
    del lock
    fixture = stack.enter_context(WorkFixture())
    data = Path(environment["ZWAI_NATIVE_DATA"])
    evidence = Path(environment["ZWAI_NATIVE_EVIDENCE"])
    config = {
        "product": {"mode": "local"},
        "ui": {"locale": "en"},
        "swarm": {"auto_title": False, "composer_predictions": False, "work_ledger": True},
        "memory": {"enabled": False, "auto_review": False},
        "models": {"default": "native-work", "providers": [{
            "id": "native-work", "label": "Private native work fixture", "base_url": fixture.url,
            "model": "native-work", "api_key": "", "context_window": 128000,
        }]},
    }
    with runner.private_binary(data / "config.yaml") as stream:
        stream.write(json.dumps(config).encode())
    def save_fixture():
        runner.save_json(evidence / "native-work-provider.json",
                         {"requests": fixture.requests, "receipts": fixture.receipts, "errors": fixture.errors})
    stack.callback(save_fixture)
    receipt["fixture_product_mode"] = "local"
    receipt["fixture_scope"] = "case-private engine and a keyless numeric-loopback model"
    return runner.CasePreparation(
        lambda: preseed_engine(stack, environment, processes, scope, receipt, work_deadline),
        lambda process, deadline: None,
    )


def tree_text(row, needle):
    return any(needle in node.get(key, "") for node in row["tree"]
               for key in ("AXTitle", "AXDescription", "AXValue"))


def named_controls(row, label):
    nodes = [node for node in row["tree"] if node.get("AXRole") in CONTROLS]
    exact = [node for node in nodes if label in (node.get("AXTitle"), node.get("AXDescription"))]
    if exact:
        return exact
    return [node for node in nodes if any(label in node.get(key, "") for key in ("AXTitle", "AXDescription"))]


def owned_lease(context):
    lease = json.loads((context["data"] / "engine.json").read_text())
    if (lease.get("url") != context["origin"] or lease.get("pid") != context["engine_pid"]
            or lease.get("mock") is not False):
        raise RuntimeError("private engine identity changed after native admission")
    smoke.assert_process(context, lease["pid"], "engine")


def fresh_window(context, deadline):
    owned_lease(context)
    row = smoke.wait_window(context, deadline)
    if row["window_id"] != context["window_id"]:
        raise RuntimeError("native work scenario lost its original window")
    return row


def wait_native(context, deadline, stage, present=(), absent=(), control=None):
    while True:
        row = fresh_window(context, deadline)
        matches = named_controls(row, control) if control else []
        if len(matches) > 1:
            raise RuntimeError("native control is ambiguous: " + control)
        if (all(tree_text(row, item) for item in present)
                and not any(tree_text(row, item) for item in absent)
                and (control is None or (len(matches) == 1 and matches[0].get("AXEnabled") is True))):
            return row
        deadline.pause(stage)


def press(context, deadline, label):
    window = wait_native(context, deadline, "available native control " + label, control=label)
    verify_controls(context["controls"], context["control_sha256"])
    result = smoke.observed_command(context, deadline,
        [str(context["controls"]), str(context["app_pid"]), "press", label], "work_records_press", 8,
        {"expected_native_label": label, "window_id": window["window_id"]})
    if result.returncode:
        raise RuntimeError("native work-record control refused: " + label)
    row = json.loads(result.stdout)
    windows = row.get("windows", [])
    if (row.get("action") != "press" or row.get("pid") != context["app_pid"]
            or Path(row["bundle"]).resolve(strict=True) != context["app"]
            or row.get("ax_error") != 0 or row.get("matches") != 1 or len(windows) != 1
            or windows[0].get("id") != context["window_id"]):
        raise RuntimeError("native press could not prove the original case window")
    context.setdefault("native_presses", []).append(label)


def snapshot(context, deadline, thread=None):
    owned_lease(context)
    path = ("/api/threads/" + thread + "/work") if thread else "/api/projects/" + context["project_id"] + "/nuwa"
    value = api(context["origin"], path, deadline)
    if value.get("project_id") != context["project_id"] or (thread and value.get("thread_id") != thread):
        raise RuntimeError("work snapshot identity differs from the opened project or thread")
    return value


def work_node(view, title=TITLE):
    matches = [item for item in view["nodes"] if item.get("title") == title]
    if len(matches) != 1:
        raise RuntimeError("work snapshot does not have exactly one " + title)
    return matches[0]


def wait_turn(context, deadline, phase):
    message = PHASES[phase]
    while True:
        collection = api(context["origin"], "/api/threads/" + context["thread_id"] + "/turns", deadline)
        matches = [turn for turn in collection["turns"] if turn.get("user_text") == message]
        if len(matches) > 1:
            raise RuntimeError("one native Send created duplicate work fixture turns")
        if matches and matches[0]["status"] in ("error", "cancelled"):
            trace = api(context["origin"], "/api/trace/" + matches[0]["id"], deadline)
            runner.save_json(context["evidence"] / ("failed-trace-" + phase + ".json"), trace)
            raise RuntimeError("work fixture turn failed: " + phase)
        if matches and matches[0]["status"] == "done":
            return verify_trace(context, deadline, phase, matches[0]["id"])
        deadline.pause("native " + phase + " turn")


def verify_trace(context, deadline, phase, turn_id):
    trace = api(context["origin"], "/api/trace/" + turn_id, deadline)
    runner.save_json(context["evidence"] / ("trace-" + phase + ".json"), trace)
    turn = trace["turn"]
    count = 2 if phase == "create" else 1
    expected = {call_id(phase, index) for index in range(count)}
    if (turn.get("id") != turn_id or turn.get("thread_id") != context["thread_id"] or turn.get("status") != "done"
            or turn.get("user_text") != PHASES[phase] or turn.get("error")):
        raise RuntimeError("real tool turn identity or outcome differs")
    for kind in ("tool_call", "tool_result"):
        matching = [event for event in trace["events"] if event.get("kind") == kind]
        if (len(matching) != count or {event.get("tool_call_id") for event in matching} != expected
                or any(event.get("err") or event.get("thread_id") != context["thread_id"]
                       or event.get("turn_id") != turn_id for event in matching)):
            raise RuntimeError("actual record_work tool evidence is not paired for " + phase)
    return turn_id


def submit(context, deadline, phase):
    smoke.submit_native(context, deadline, PHASES[phase])
    return wait_turn(context, deadline, phase)


def capture(context, deadline, name, present, absent=(), control=None):
    row = wait_native(context, deadline, name, present, absent, control)
    image = smoke.capture_window(context, deadline, row, name + ".png")
    if image.get("content_visible") is not True:
        raise RuntimeError("native " + name + " screenshot has no visible content")
    witness = fresh_window(context, deadline)
    if not all(tree_text(witness, item) for item in present) or any(tree_text(witness, item) for item in absent):
        raise RuntimeError("native screen changed while capturing " + name)
    if control is not None and len(named_controls(witness, control)) != 1:
        raise RuntimeError("native control changed while capturing " + name)
    runner.save_json(context["evidence"] / (name + ".json"), {"window": row, "pixels": image, "after_pixels": witness})
    return image


def wait_merge(context, deadline, prior):
    while True:
        peer = snapshot(context, deadline, context["peer_id"])
        if peer["nodes"]:
            node = work_node(peer)
            check_version(node, prior, "reported", "project")
            return peer, node
        deadline.pause("one native merge to the private project")


def check_version(node, prior, status, scope):
    if (node.get("id") != prior.get("id") or node.get("thread_id") != prior.get("thread_id")
            or node.get("turn_id") != prior.get("turn_id") or node.get("version") != prior["version"] + 1
            or node.get("scope") != scope or node.get("status") != status):
        raise RuntimeError("work record did not retain identity while advancing one version")


def run_scenario(ax, controls, control_sha256):
    context = smoke.case_context(os.environ)
    context.update(ax=ax.resolve(strict=True), ax_identity=smoke.validate_helper(ax),
                   controls=controls, control_sha256=control_sha256, expected_mock=False)
    deadline = smoke.Deadline(min(SCENARIO_SECONDS, float(os.environ["ZWAI_NATIVE_SCENARIO_TIMEOUT_SECONDS"])))
    result = {"case_id": context["case_id"], "passed": False}
    primary = None
    try:
        fixture = json.loads((context["evidence"] / "native-work-fixture.json").read_text())
        if fixture.get("case_id") != context["case_id"]:
            raise RuntimeError("prelaunch work fixture belongs to another case")
        origin, lease, admitted = smoke.wait_initial_ready(context, deadline)
        if origin != fixture.get("origin") or lease["pid"] != fixture.get("fixture_engine_pid"):
            raise RuntimeError("the native desktop did not attach to its pre-seeded engine")
        context.update(project_id=fixture["project_id"], thread_id=fixture["thread_id"], peer_id=fixture["peer_id"],
                       origin=origin, engine_pid=lease["pid"], window_id=admitted["window_id"])
        if not named_controls(admitted, fixture["thread_title"]):
            press(context, deadline, fixture["project_name"])
        press(context, deadline, fixture["thread_title"])
        press(context, deadline, "Work records")
        wait_native(context, deadline, "ordinary live work panel", present=("No work records in this conversation yet.",))

        created_turn = submit(context, deadline, "create")
        before = snapshot(context, deadline, context["thread_id"])
        original = work_node(before)
        if (len(before["nodes"]) != 2 or work_node(before, REFERENCE).get("scope") != "thread"
                or original.get("scope") != "thread" or original.get("status") != "reported" or original.get("version") != 1
                or original.get("turn_id") != created_turn or snapshot(context, deadline, context["peer_id"])["nodes"]
                or snapshot(context, deadline)["nodes"]):
            raise RuntimeError("new work must be visible only inside the source conversation")
        runner.save_json(context["evidence"] / "snapshot-private.json", before)
        press(context, deadline, "Facts, 2 not on the path")
        press(context, deadline, TITLE)
        capture(context, deadline, "work-private", (TITLE, "Conversation record", "Reported"), control="Merge into project")

        press(context, deadline, "Merge into project")
        promoted_snapshot, promoted = wait_merge(context, deadline, original)
        source_after = snapshot(context, deadline, context["thread_id"])
        if (promoted.get("updated_by") != "human" or len(promoted_snapshot["nodes"]) != 1
                or source_after["thread_revision"] <= before["thread_revision"]
                or source_after["project_revision"] <= before["project_revision"]
                or work_node(snapshot(context, deadline))["id"] != original["id"]):
            raise RuntimeError("one native merge did not commit both revisions and project visibility")
        runner.save_json(context["evidence"] / "snapshot-promoted-peer.json", promoted_snapshot)
        capture(context, deadline, "work-promoted", (TITLE, "Project record", "Reported"), absent=("Merge into project",))
        press(context, deadline, "Entire project")

        retracted_turn = submit(context, deadline, "retract")
        retracted_view = snapshot(context, deadline, context["thread_id"])
        retracted = work_node(retracted_view)
        check_version(retracted, promoted, STATUSES["retract"], "project")
        if retracted_view["project_revision"] <= promoted_snapshot["project_revision"]:
            raise RuntimeError("refutation did not advance the shared revision")
        runner.save_json(context["evidence"] / "snapshot-retracted.json", retracted_view)
        capture(context, deadline, "work-pruned", ("Pruned 1",), absent=(TITLE,), control="Invalidated: 1")
        press(context, deadline, "Invalidated: 1")
        capture(context, deadline, "work-pruned-expanded", (TITLE, "Refuted"))
        press(context, deadline, "Invalidated: 1")

        restored_turn = submit(context, deadline, "restore")
        restored_view = snapshot(context, deadline, context["thread_id"])
        restored = work_node(restored_view)
        check_version(restored, retracted, STATUSES["restore"], "project")
        if (restored_view["project_revision"] <= retracted_view["project_revision"]
                or work_node(snapshot(context, deadline, context["peer_id"]))["version"] != restored["version"]):
            raise RuntimeError("restoration did not publish the recovered version to the peer")
        runner.save_json(context["evidence"] / "snapshot-restored.json", restored_view)
        capture(context, deadline, "work-restored", (TITLE, "Reported", "Shared work collaboration graph"),
                absent=("Invalidated: 1", "Pruned 1"))
        result.update(passed=True, thread_id=context["thread_id"], project_id=context["project_id"], node_id=original["id"],
                      versions=[original["version"], promoted["version"], retracted["version"], restored["version"]],
                      turns=[created_turn, retracted_turn, restored_turn], native_presses=context["native_presses"],
                      app_pid=context["app_pid"], engine_pid=context["engine_pid"], window_id=context["window_id"],
                      native_helper=context["ax_identity"], control_helper=str(controls), control_sha256=control_sha256,
                      control_source_sha256=digest(CONTROL_SOURCE), shared_deadline_monotonic=deadline.ends)
        return 0
    except BaseException as error:
        primary = error
        result["failure"] = type(error).__name__ + ": " + str(error)
        raise
    finally:
        try:
            runner.save_json(context["evidence"] / "native-work-records.json", result)
        except BaseException as error:
            if primary is None:
                raise
            primary.__notes__ = [*getattr(primary, "__notes__", []), "terminal evidence also failed: " + type(error).__name__]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ax", type=Path, required=True)
    parser.add_argument("--controls", type=Path, required=True)
    parser.add_argument("--control-sha256", required=True)
    parser.add_argument("--case", action="store_true")
    parser.add_argument("--app", type=Path)
    parser.add_argument("--evidence", type=Path)
    args = parser.parse_args(argv)
    ax = args.ax.resolve(strict=True)
    controls = verify_controls(args.controls.resolve(strict=True), args.control_sha256)
    if not args.case:
        runner.require_guest()
        return run_scenario(ax, controls, args.control_sha256)
    if args.app is None or args.evidence is None:
        parser.error("--case requires --app and --evidence")
    with ExitStack() as stack:
        code = runner.main(["--app", str(args.app), "--evidence", str(args.evidence),
                            "--timeout", str(SCENARIO_SECONDS), "--budget", str(CASE_SECONDS),
                            "--", sys.executable, "-B", str(Path(__file__).resolve()),
                            "--ax", str(ax), "--controls", str(controls), "--control-sha256", args.control_sha256],
                           mock=False, prepare=lambda *values: prepare_fixture(stack, *values),
                           preflight=lambda lock, receipt, evidence, deadline:
                           smoke.case_preflight(ax, receipt, evidence, deadline))
    if code == 0:
        provider = json.loads((args.evidence / "native-work-provider.json").read_text())
        expected = {call_id("create", 0), call_id("create", 1), call_id("retract"), call_id("restore")}
        if provider["errors"] or set(provider["receipts"]) != expected or len(provider["requests"]) != len(expected):
            raise RuntimeError("keyless provider did not observe exactly the four native fixture writes")
    return code


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (KeyError, OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(type(error).__name__ + ": " + str(error), file=sys.stderr)
        raise SystemExit(2)
