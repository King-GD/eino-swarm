"""The dedicated case reuses the real runner and checks actual panel receipts."""
import json
import os
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest import mock

import native_task_panels as case


class NativeTaskPanelTests(unittest.TestCase):
    def panel(self, final=False):
        return {"thread_id": "thread", "plugin_id": "fofa-world-map", "state": {
            "collection": {"discovered": 3, "reason": "synthetic_fixture"},
            "assets_by_id": {"a": {"country": "CN"}, "b": {"country": "CN"}, "c": {"country": "US"}},
            "probe": dict(pending=0, running=0, responded=2, no_response=1, skipped=0, interrupted=0) if final else dict(pending=2, running=1, responded=0, no_response=0, skipped=0, interrupted=0),
            "results_by_id": {"a": {}, "b": {}, "c": {}} if final else {}}}

    def test_fixture_setup_is_private_and_never_overwrites_case_config(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            data, workspace = root / "data", root / "workspace"
            data.mkdir(); workspace.mkdir()
            environment = {"ZWAI_NATIVE_DATA": str(data), "ZWAI_NATIVE_WORKSPACE": str(workspace)}
            receipt = {}
            case.prepare_fixture(None, receipt, environment, [], None, None)
            self.assertEqual(environment["ZWAI_MOCK_TASK_PANELS"], "1")
            self.assertEqual(environment["ZWAI_MOCK_TASK_PANEL_GATE"], str(workspace / "panel-continue"))
            self.assertEqual((data / "config.yaml").stat().st_mode & 0o777, 0o600)
            self.assertIn("locale: en", (data / "config.yaml").read_text())
            with self.assertRaises(FileExistsError):
                case.prepare_fixture(None, receipt, environment, [], None, None)

    def test_native_case_entry_keeps_existing_runner_preflight_and_cleanup(self):
        with mock.patch.object(case.runner, "main", return_value=0) as run, mock.patch.object(case, "validate_observer", return_value={}):
            self.assertEqual(case.main(["--case", "--ax", "/private/helper", "--observe-ax", "/private/observer", "--app", "/private/candidate.app", "--evidence", "/private/new-evidence"]), 0)
        args = run.call_args
        self.assertIn("--budget", args.args[0])
        self.assertIs(args.kwargs["prepare"], case.prepare_fixture)
        self.assertTrue(callable(args.kwargs["preflight"]))

    def test_read_only_adapter_uses_real_helper_cli_shape_and_rejects_identity_changes(self):
        executable = Path("/private/case/app.app/Contents/MacOS/zwai")
        identity = dict(pid=501, birth=[1234, 56], executable=str(executable))
        row = dict(case_id="case", pid=501, window_id=42, has_web_area=True, textareas=1, text="HTTP checks: 0 / 3", identity=identity)
        context = dict(observer=Path("/private/observer"), observer_identity={}, app_pid=501,
                       case_id="case", initial_window_id=42, initial_kernel_identity=identity)
        completed = subprocess.CompletedProcess([], 0, json.dumps(row), "")
        with mock.patch.object(case, "validate_observer", return_value={}), \
                mock.patch.object(case.smoke, "assert_process", return_value=executable), \
                mock.patch.object(case.smoke, "observed_command", return_value=completed) as command:
            self.assertEqual(case.observe(context, case.smoke.Deadline(1), "snapshot"), row)
            self.assertEqual(command.call_args.args[2], ["/private/observer", "snapshot", "501"])
            with self.assertRaises(ValueError): case.observe(context, case.smoke.Deadline(1), "activate")
            for field, value in [("case_id", "foreign"), ("window_id", 43), ("pid", 777), ("textareas", True), ("has_web_area", False), ("identity", dict(identity, birth=[1234, 57]))]:
                completed.stdout = json.dumps(dict(row, **{field: value}))
                with self.assertRaises(RuntimeError): case.observe(context, case.smoke.Deadline(1), "snapshot")
            completed.returncode, completed.stderr = 2, "guard refused"
            with self.assertRaises(RuntimeError): case.observe(context, case.smoke.Deadline(1), "snapshot")

    def test_progress_and_paired_trace_refuse_foreign_or_incomplete_data(self):
        case.assert_panel(self.panel(), "thread", False)
        case.assert_panel(self.panel(True), "thread", True)
        for changed in ("thread_id", "plugin_id"):
            panel = self.panel()
            panel[changed] = "foreign"
            with self.assertRaises(RuntimeError): case.assert_panel(panel, "thread", False)
        with self.assertRaises(RuntimeError): case.assert_panel(self.panel(), "thread", True)
        trace = {"turn": {"id": "turn", "thread_id": "thread", "user_text": "input", "status": "done", "final": "Synthetic map fixture complete"}, "events": []}
        for call in case.CALLS:
            for kind in ("tool_call", "tool_result"):
                trace["events"].append(dict(thread_id="thread", turn_id="turn", kind=kind, tool_call_id=call))
        case.verify_trace(trace, "thread", "turn", "input")
        trace["events"].pop()
        with self.assertRaises(RuntimeError): case.verify_trace(trace, "thread", "turn", "input")


if __name__ == "__main__":
    unittest.main()
