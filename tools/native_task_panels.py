#!/usr/bin/env python3
"""Accept the bundled HTML map through one isolated ci-macOS native case.

This synthetic fixture never runs FOFA or HTTP probes. Their real CLI boundary
is tested independently; this scenario accepts native panel rendering/updates.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

import native_macos_smoke as smoke
import native_macos_resilience as resilience

runner = smoke.runner
CALLS = {"panel-fixture-index", "panel-fixture-open", "panel-fixture-read",
         "panel-fixture-initial", "panel-fixture-final", "panel-fixture-verify"}


def prepare_observer(directory):
    helper, _ = resilience.compile_helpers(directory)
    source = Path(__file__).with_name("native_macos_resilience_ax.m")
    receipt = dict(schema_version=1, source_name=source.name,
                   source_sha256=hashlib.sha256(source.read_bytes()).hexdigest(),
                   binary_sha256=hashlib.sha256(helper.read_bytes()).hexdigest(),
                   builder="native_macos_resilience.compile_helpers")
    runner.save_json(Path(str(helper) + ".json"), receipt)
    return dict(receipt, binary=str(helper))


def validate_observer(binary):
    source = Path(__file__).with_name("native_macos_resilience_ax.m")
    receipt = json.loads(Path(str(binary) + ".json").read_text())
    if (receipt.get("schema_version") != 1 or receipt.get("source_name") != source.name
            or receipt.get("source_sha256") != hashlib.sha256(source.read_bytes()).hexdigest()
            or receipt.get("binary_sha256") != hashlib.sha256(binary.read_bytes()).hexdigest()
            or receipt.get("builder") != "native_macos_resilience.compile_helpers"
            or not os.access(binary, os.X_OK)):
        raise RuntimeError("read-only observer preparation identity differs")
    return receipt


def observe(context, deadline, action):
    # This adapter never grants the secondary helper an input/activation action.
    if action not in ("identity", "snapshot"):
        raise ValueError("panel observer is read-only")
    if validate_observer(context["observer"]) != context["observer_identity"]:
        raise RuntimeError("read-only observer changed during the case")
    executable = smoke.assert_process(context, context["app_pid"], "desktop")
    command = [str(context["observer"]), action, str(context["app_pid"])]
    result = smoke.observed_command(context, deadline, command, "panel_" + action, 8)
    if result.returncode:
        raise RuntimeError("read-only native observation refused: " + result.stderr[:500])
    row = json.loads(result.stdout)
    identity = row if action == "identity" else row.get("identity")
    if (not isinstance(identity, dict) or identity.get("pid") != context["app_pid"]
            or not isinstance(identity.get("birth"), list) or len(identity["birth"]) != 2
            or any(type(value) is not int or value < 0 for value in identity["birth"])
            or identity["birth"][0] <= 0 or not isinstance(identity.get("executable"), str)
            or Path(identity.get("executable", "")).resolve() != executable):
        raise RuntimeError("read-only observer lost kernel process identity")
    if "initial_kernel_identity" in context and identity != context["initial_kernel_identity"]:
        raise RuntimeError("native process identity changed after initial admission")
    if action == "snapshot" and (row.get("case_id") != context["case_id"]
            or row.get("pid") != context["app_pid"]
            or type(row.get("window_id")) is not int or row["window_id"] != context["initial_window_id"]
            or row.get("has_web_area") is not True or type(row.get("textareas")) is not int or row["textareas"] != 1
            or not isinstance(row.get("text"), str)):
        raise RuntimeError("read-only observer lost its admitted native window")
    return row


def wait_observed(context, deadline, expected):
    while True:
        lease = json.loads((context["data"] / "engine.json").read_text())
        if lease.get("url") != context["origin"] or lease.get("pid") != context["initial_engine_pid"]:
            raise RuntimeError("native main binding changed after initial admission")
        smoke.assert_process(context, lease["pid"], "engine")
        row = observe(context, deadline, "snapshot")
        if expected in row["text"]:
            return row
        deadline.pause("nested panel text")


def capture_observed(context, deadline, row, name):
    image = context["evidence"] / name
    with runner.private_binary(image):
        pass
    try:
        result = smoke.observed_command(context, deadline,
            ["/usr/sbin/screencapture", "-x", "-l", str(row["window_id"]), str(image)], "panel_pixels", 8)
        if result.returncode:
            raise RuntimeError("native panel screenshot failed")
    finally:
        smoke.secure_screenshot(image, sys.exc_info()[1])
    pixels = smoke.readiness.pixel_observation(image)
    if min(smoke.png_dimensions(image)) < 100 or pixels.get("content_visible") is not True:
        raise RuntimeError("native panel image lacks visible content")
    observe(context, deadline, "snapshot")
    return dict(pixels, path=str(image), sha256=hashlib.sha256(image.read_bytes()).hexdigest(), window_id=row["window_id"])


def prepare_fixture(lock, receipt, environment, processes, scope, deadline):
    smoke.prepare_local_fixture(lock, receipt, environment, processes, scope, deadline)
    config = Path(environment["ZWAI_NATIVE_DATA"]) / "config.yaml"
    with os.fdopen(os.open(config, os.O_WRONLY | os.O_APPEND | os.O_NOFOLLOW), "w") as stream:
        stream.write("ui:\n  locale: en\nswarm:\n  auto_title: false\n  composer_predictions: false\nmemory:\n  auto_review: false\n")
    environment["ZWAI_MOCK_TASK_PANELS"] = "1"
    environment["ZWAI_MOCK_TASK_PANEL_GATE"] = str(Path(environment["ZWAI_NATIVE_WORKSPACE"]) / "panel-continue")
    receipt["fixture_scope"] = "synthetic map render and incremental update; no external collection/probe"


def find_turn(context, message, deadline):
    while True:
        matches = []
        for thread in smoke.request_json(context["origin"], "/api/threads", deadline).get("threads", []):
            path = "/api/threads/" + thread["id"] + "/turns"
            for turn in smoke.request_json(context["origin"], path, deadline).get("turns", []):
                if turn.get("user_text") == message:
                    matches.append((thread["id"], turn["id"]))
        if len(matches) > 1:
            raise RuntimeError("one native Send created duplicate fixture turns")
        if matches:
            return matches[0]
        deadline.pause("native fixture turn")


def wait_panel(context, thread_id, final, deadline):
    while True:
        data = smoke.request_json(context["origin"], "/api/threads/" + thread_id + "/panels", deadline)
        panels = data.get("panels", [])
        if len(panels) > 1:
            raise RuntimeError("fixture created multiple panels")
        if panels:
            panel = panels[0]
            state = panel.get("state", {})
            if state.get("collection", {}).get("discovered") == 3:
                assert_panel(panel, thread_id, final)
                return panel
        deadline.pause("native map state")


def assert_panel(panel, thread_id, final):
    state = panel["state"]
    if (panel.get("thread_id") != thread_id or panel.get("plugin_id") != "fofa-world-map"
            or len(state.get("assets_by_id", {})) != 3
            or state.get("collection", {}).get("reason") != "synthetic_fixture"
            or {asset.get("country") for asset in state["assets_by_id"].values()} != {"CN", "US"}):
        raise RuntimeError("map state does not match this synthetic fixture")
    probe = state.get("probe", {})
    expected = dict(pending=0, running=0, responded=2, no_response=1, skipped=0, interrupted=0) if final else dict(pending=2, running=1, responded=0, no_response=0, skipped=0, interrupted=0)
    if probe != expected or (final and len(state.get("results_by_id", {})) != 3):
        raise RuntimeError("map progress is not the expected fixture phase")


def verify_trace(trace, thread_id, turn_id, message):
    turn = trace.get("turn", {})
    if (turn.get("id") != turn_id or turn.get("thread_id") != thread_id
            or turn.get("user_text") != message or turn.get("status") != "done" or turn.get("error")):
        raise RuntimeError("native fixture terminal identity differs")
    events = trace.get("events", [])
    for kind in ("tool_call", "tool_result"):
        selected = [event for event in events if event.get("kind") == kind]
        if len(selected) != len(CALLS) or {event.get("tool_call_id") for event in selected} != CALLS:
            raise RuntimeError("fixture lacks exactly paired real panel tools")
    if any(event.get("err") or event.get("thread_id") != thread_id or event.get("turn_id") != turn_id for event in events):
        raise RuntimeError("fixture Trace contains foreign or failed events")
    if "Synthetic map fixture complete" not in turn.get("final", ""):
        raise RuntimeError("fixture did not verify its final read receipt")


def scenario(binary, observer):
    context = smoke.case_context(os.environ)
    context["ax"] = binary.resolve(strict=True)
    context["ax_identity"] = smoke.validate_helper(context["ax"])
    context["observer"] = observer.resolve(strict=True)
    context["observer_identity"] = validate_observer(context["observer"])
    deadline = smoke.Deadline(context["timeout"])
    result = dict(passed=False, case_id=context["case_id"], scope="native synthetic HTML map", external_assets_contacted=False)
    primary = None
    try:
        origin, lease, admitted = smoke.wait_initial_ready(context, deadline)
        context["origin"] = origin
        result["engine_pid"] = lease["pid"]
        context["initial_engine_pid"] = lease["pid"]
        context["initial_window_id"] = admitted["window_id"]
        context["initial_kernel_identity"] = observe(context, deadline, "identity")
        message = "Show the synthetic map fixture " + context["case_id"]
        smoke.submit_native(context, deadline, message)
        thread_id, turn_id = find_turn(context, message, deadline)
        first = wait_panel(context, thread_id, False, deadline)
        initial_window = wait_observed(context, deadline, "HTTP checks: 0 / 3")
        initial_pixels = capture_observed(context, deadline, initial_window, "map-initial.png")
        if initial_pixels.get("content_visible") is not True:
            raise RuntimeError("native initial map screenshot has no visible content")
        runner.save_json(context["evidence"] / "panel-initial.json", first)
        runner.save_json(context["evidence"] / "window-initial.json", initial_window)
        gate = context["workspace"] / "panel-continue"
        with runner.private_binary(gate) as stream:
            stream.write(context["case_id"].encode())
        while True:
            trace = smoke.request_json(origin, "/api/trace/" + turn_id, deadline)
            if trace.get("turn", {}).get("status") in ("error", "cancelled"):
                raise RuntimeError("native fixture ended unsuccessfully")
            if trace.get("turn", {}).get("status") == "done":
                break
            deadline.pause("fixture final read and completion")
        verify_trace(trace, thread_id, turn_id, message)
        final = wait_panel(context, thread_id, True, deadline)
        if final["id"] != first["id"] or final["revision"] <= first["revision"]:
            raise RuntimeError("incremental update replaced the panel or reused its version")
        final_window = wait_observed(context, deadline, "HTTP checks: 3 / 3")
        final_pixels = capture_observed(context, deadline, final_window, "map-final.png")
        if final_pixels.get("content_visible") is not True:
            raise RuntimeError("native final map screenshot has no visible content")
        runner.save_json(context["evidence"] / "panel-final.json", final)
        runner.save_json(context["evidence"] / "window-final.json", final_window)
        runner.save_json(context["evidence"] / "panel-trace.json", trace)
        result.update(passed=True, thread_id=thread_id, turn_id=turn_id, panel_id=final["id"],
                      initial_revision=first["revision"], final_revision=final["revision"],
                      initial_pixels=initial_pixels, final_pixels=final_pixels,
                      native_helper=context["ax_identity"], input_policy="one native fill and Send")
        result["read_only_observer"] = context["observer_identity"]
        return 0
    except BaseException as error:
        primary = error
        result["failure"] = type(error).__name__ + ": " + str(error)
        raise
    finally:
        try:
            runner.save_json(context["evidence"] / "native-task-panels.json", result)
        except BaseException as error:
            if primary is None:
                raise
            primary.__notes__ = [*getattr(primary, "__notes__", []), "terminal evidence also failed: " + type(error).__name__]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ax", type=Path)
    parser.add_argument("--observe-ax", type=Path)
    parser.add_argument("--prepare-observer", type=Path)
    parser.add_argument("--case", action="store_true")
    parser.add_argument("--app", type=Path)
    parser.add_argument("--evidence", type=Path)
    args = parser.parse_args(argv)
    if args.prepare_observer:
        print(json.dumps(prepare_observer(args.prepare_observer)))
        return 0
    if args.ax is None or args.observe_ax is None:
        parser.error("--ax and --observe-ax are required")
    validate_observer(args.observe_ax)
    if args.case:
        if args.app is None or args.evidence is None:
            parser.error("--case requires --app and --evidence")
        return runner.main(["--app", str(args.app), "--evidence", str(args.evidence),
                            "--timeout", "50", "--budget", "60", "--", sys.executable,
                            str(Path(__file__).resolve()), "--ax", str(args.ax.absolute()), "--observe-ax", str(args.observe_ax.absolute())],
                          prepare=prepare_fixture,
                          preflight=lambda lock, receipt, evidence, deadline:
                          smoke.case_preflight(args.ax.absolute(), receipt, evidence, deadline))
    return scenario(args.ax, args.observe_ax)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (KeyError, OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(type(error).__name__ + ": " + str(error), file=sys.stderr)
        raise SystemExit(2)
