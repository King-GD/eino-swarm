"""Offline harness integrity tests; these never measure real-model effects."""
import argparse
from contextlib import closing
import copy
import json
import os
from pathlib import Path
import sqlite3
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from unittest.mock import patch

import memory_experiment_study as study


def fixture():
    return {"version": 1, "kind": "synthetic-control", "cutoff": "2026-01-02T00:00:00Z", "cases": [{
        "id": "case", "prompt": "Read project state and create result.json.", "notes": ["A durable constraint."],
        "skills": [{"name": "procedure", "description": "When the procedure applies", "body": "Read then verify."}],
        "sources": [{"project_id": "project", "thread_id": "original", "seq": 1, "kind": "human",
                     "text": "A durable constraint.", "observed_at": "2026-01-01T00:00:00Z"}],
        "files": {"input.txt": "source"}, "checks": [{"name": "result", "type": "file_exists", "path": "result.json"}],
    }]}


def initialize_db(data):
    with closing(sqlite3.connect(data / "zwai.db")) as db, db:
        db.executescript("""
            CREATE TABLE turns(id,thread_id,seq,status,user_text,final,started_at,ended_at);
            CREATE TABLE messages(thread_id,turn_id,seq,role,agent_id,source,content,created_at);
            CREATE TABLE events(thread_id,turn_id,seq,kind,agent_id,tool_call_id,text,err,created_at);
            CREATE TABLE search_docs(body);
        """)


class FixtureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.path = self.root / "fixture.json"

    def tearDown(self):
        self.temp.cleanup()

    def load(self, value):
        self.path.write_text(json.dumps(value))
        return study.load_fixture(self.path)

    def test_fixture_requires_frozen_prior_history_and_independent_checks(self):
        original = fixture()
        self.assertEqual(self.load(original), original)
        changes = [lambda x: x.update(version=2), lambda x: x.update(kind="benchmark"),
                   lambda x: x.update(cases=[]), lambda x: x["cases"].append(x["cases"][0]),
                   lambda x: x["cases"][0].update(id="../case"), lambda x: x["cases"][0].update(checks=[]),
                   lambda x: x["cases"][0].update(notes=[""]), lambda x: x["cases"][0].update(timeout_s=0),
                   lambda x: x["cases"][0]["skills"].append(x["cases"][0]["skills"][0]),
                   lambda x: x["cases"][0]["skills"][0].update(name="../escape"),
                   lambda x: x["cases"][0]["skills"][0].update(body=""),
                   lambda x: x["cases"][0].update(files={"../escape": "bad"}),
                   lambda x: x["cases"][0]["sources"].append(x["cases"][0]["sources"][0]),
                   lambda x: x["cases"][0]["sources"][0].update(seq=True),
                   lambda x: x["cases"][0]["sources"][0].update(observed_at=x["cutoff"]),
                   lambda x: x["cases"][0]["sources"][0].update(text=None),
                   lambda x: x["cases"][0]["sources"].append({**x["cases"][0]["sources"][0], "seq": 2, "project_id": "other"}),
                   lambda x: x.update(kind="historical-transfer")]
        for index, change in enumerate(changes):
            value = copy.deepcopy(original)
            change(value)
            with self.subTest(index=index), self.assertRaises(ValueError):
                self.load(value)
        value = copy.deepcopy(original)
        value.update(kind="historical-transfer")
        value["cases"][0]["origin"] = "Identified historical source"
        self.assertEqual(self.load(value), value)

    def test_timezone_and_paths_are_explicit(self):
        with self.assertRaises(ValueError):
            study.timestamp("2026-01-01")
        for name in ("", "/absolute", "../escape", "nested/../escape", "bad\x00name"):
            self.assertFalse(study.safe_relative(name))
        self.assertTrue(study.safe_relative("nested/source.txt"))

    def test_controlled_tools_are_explicit_and_legacy_human_kind_is_not_relabelled(self):
        value = fixture()
        value["kind"] = "prospective-memory-curation"
        value["cases"][0]["disabled_tools"] = ["exec", "web_search", "web_fetch", "edit"]
        value["cases"][0]["sources"][0]["kind"] = "user_message"
        self.assertEqual(self.load(value), value)
        for invalid in ("exec", [""], [None], [" exec"], ["exec", "exec"]):
            changed = copy.deepcopy(value)
            changed["cases"][0]["disabled_tools"] = invalid
            with self.assertRaises(ValueError):
                self.load(changed)
        initialize_db(self.root)
        (self.root / study.OWNER).write_text("owner")
        sources = value["cases"][0]["sources"]
        sources += [{**sources[0], "seq": 2, "kind": "human"}]
        mapped = study.seed_history(self.root, sources, "live", {"original": "thread"})
        self.assertEqual(mapped[0]["kind"], "user_message")
        with closing(sqlite3.connect(self.root / "zwai.db")) as db:
            self.assertEqual(db.execute("SELECT source FROM messages ORDER BY seq").fetchall(), [(None,), ("human",)])

    def test_memory_preservation_distinguishes_exact_bytes_and_complete_retained_text(self):
        notes = ["Preserve negation and all conditions.", "First step.\nSecond step."]
        skills = [{"name": "original", "description": "When needed", "body": "Known procedure."}]
        final = {"notes": [notes[0]], "skills": skills + [{"name": "moved", "description": "When needed", "body": "First step. Second step."}]}
        report = study.memory_preservation(notes, skills, final)
        self.assertTrue(report["preserved"])
        self.assertEqual(report["moved_note_indexes"], [1])
        self.assertEqual(report["semantic_status"], "unreviewed")
        changed = copy.deepcopy(final)
        changed["skills"][1]["body"] = "First step. Second step. Extra guidance."
        self.assertTrue(study.memory_preservation(notes, skills, changed)["full_original_text_retained"])
        changed = copy.deepcopy(final)
        changed["skills"][0]["description"] += " Changed"
        self.assertFalse(study.memory_preservation(notes, skills, changed)["preserved"])
        changed = copy.deepcopy(final)
        changed["notes"] = ["Preserve all conditions."]
        self.assertFalse(study.memory_preservation(notes, skills, changed)["preserved"])
        self.assertFalse(study.memory_preservation(notes, skills, {})["complete"])
        appended = {"notes": [], "skills": [{**skills[0], "body": skills[0]["body"] + "\n\n" + "\n".join(notes)}]}
        report = study.memory_preservation(notes, skills, appended)
        self.assertTrue(report["full_original_text_retained"])
        self.assertFalse(report["exact_unchanged"])
        self.assertEqual(report["changed_skill_names"], ["original"])
        self.assertEqual(report["moved_note_indexes"], [0, 1])
        # Text retention alone cannot establish that an obsolete/negating
        # wrapper preserved its applicability; independent review is mandatory.
        appended["skills"][0]["body"] = "Obsolete guidance:\n" + appended["skills"][0]["body"]
        self.assertTrue(study.memory_preservation(notes, skills, appended)["full_original_text_retained"])
        self.assertEqual(study.memory_preservation(notes, skills, appended)["semantic_status"], "unreviewed")

    def test_api_snapshot_reads_every_full_skill_without_usage_writes(self):
        calls = []
        skill = {"name": "procedure", "description": "Applicability", "body": "Complete procedure", "views": 4}

        def http(base, method, path, body=None):
            calls.append((method, path))
            return ({"memory": {"memory": {"entries": ["Original note."]}, "skills": [{"name": "procedure", "views": 4}]}}
                    if path.endswith("/memory") else {"skill": skill})

        with patch.object(study.engine_harness, "http_json", side_effect=http):
            state = study.capture_memory_state("http://private", "project")
        self.assertEqual(state["notes"], ["Original note."])
        self.assertEqual(state["skills"], [skill])
        self.assertEqual(calls, [("GET", "/api/projects/project/memory"), ("GET", "/api/projects/project/skills/procedure")])

    def test_historical_archive_is_digest_bound_and_rejects_link_members(self):
        source = self.root / "source.py"
        source.write_text("print('offline')")
        archive = self.root / "source.tar"
        with tarfile.open(archive, "w") as output:
            output.add(source, arcname="source.py")
        case = fixture()["cases"][0]
        case["source_archive"] = {"path": str(archive), "sha256": study.protocol.file_digest(archive)}
        workspace = self.root / "workspace"
        workspace.mkdir()
        study.prepare_workspace(case, workspace)
        self.assertEqual((workspace / "source.py").read_text(), source.read_text())
        value = fixture()
        value["cases"][0]["source_archive"] = {**case["source_archive"], "path": "source.tar"}
        self.assertEqual(self.load(value)["cases"][0]["source_archive"]["path"], str(archive.resolve()))
        with tarfile.open(archive, "w") as output:
            member = tarfile.TarInfo("link")
            member.type = tarfile.SYMTYPE
            member.linkname = "/outside"
            output.addfile(member)
        with self.assertRaisesRegex(ValueError, "changed"):
            study.prepare_workspace(case, workspace)
        case["source_archive"]["sha256"] = study.protocol.file_digest(archive)
        with self.assertRaisesRegex(ValueError, "unsafe"):
            study.prepare_workspace(case, workspace)

    def test_private_memory_and_configuration_preserve_source(self):
        case = fixture()["cases"][0]
        study.seed_memory(self.root / "memory", case["notes"], case["skills"])
        memory = self.root / "memory" / "MEMORY.md"
        self.assertEqual(memory.read_text(), "A durable constraint.\n")
        self.assertEqual(stat.S_IMODE(memory.stat().st_mode), 0o600)
        before = study.memory_hashes(self.root / "memory")
        self.assertEqual(len(before), 2)
        self.assertEqual(study.memory_hashes(self.root / "missing"), {})
        provider = {"base_url": "http://fixture.invalid", "model": "fixture", "api_key": "private"}
        config = study.model_config(provider, Path("/helper"), self.root / "memory", 100, False)
        self.assertIn("api_key: \"private\"", config)
        self.assertIn("transport: stdio", config)
        self.assertIn("auto_review: true", config)
        self.assertIn("max_completion_tokens: 100", config)
        self.assertNotIn("mcp:", study.model_config({}, Path("/helper"), None, 0, True))
        self.assertEqual(study.memory_hashes(self.root / "memory"), before)

    def test_multiline_catalog_description_refuses_before_any_seed_write(self):
        for description in ("first\nsecond", "first\rsecond"):
            case = fixture()["cases"][0]
            case["skills"][0]["description"] = description
            target = self.root / "invalid-memory"
            with self.assertRaisesRegex(ValueError, "single-line"):
                study.seed_memory(target, case["notes"], case["skills"])
            self.assertFalse(target.exists())

    def test_history_seed_binds_every_original_record_and_refuses_live_data(self):
        initialize_db(self.root)
        sources = fixture()["cases"][0]["sources"]
        mapping = {"original": "live-thread"}
        with self.assertRaisesRegex(ValueError, "owner"):
            study.seed_history(self.root, sources, "live-project", mapping)
        (self.root / study.OWNER).write_text("owner")
        sources += [{**sources[0], "seq": 2, "kind": "tool_call", "tool_call_id": "call", "text": "read(file)"},
                    {**sources[0], "seq": 3, "kind": "tool_result", "tool_call_id": "call", "text": "observed"},
                    {**sources[0], "seq": 4, "kind": "assistant", "text": "unverified"}]
        mapped = study.seed_history(self.root, sources, "live-project", mapping)
        self.assertEqual({s["project_id"] for s in mapped}, {"live-project"})
        self.assertEqual({s["thread_id"] for s in mapped}, {"live-thread"})
        with closing(sqlite3.connect(self.root / "zwai.db")) as db:
            self.assertEqual(db.execute("SELECT kind FROM events ORDER BY seq").fetchall(),
                             [("user_message",), ("tool_call",), ("tool_result",), ("agent_message",)])
            self.assertEqual(db.execute("SELECT role FROM messages ORDER BY seq").fetchall(),
                             [("user",), ("assistant",), ("tool",), ("assistant",)])
        with self.assertRaisesRegex(ValueError, "nonempty"):
            study.seed_history(self.root, sources, "live-project", mapping)


class RunTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.args = argparse.Namespace(out=self.root / "out", helper=Path("/helper"), binary=Path("/binary"),
                                       mock=False, max_completion_tokens=100, boot_timeout=1,
                                       dream_timeout=1, review_wait=1)
        self.args.out.mkdir()

    def tearDown(self):
        self.temp.cleanup()

    def memory_reply(self, path):
        case = fixture()["cases"][0]
        if path.endswith("/memory"):
            return {"memory": {"memory": {"entries": case["notes"]}, "skills": [{"name": skill["name"]} for skill in case["skills"]]}}
        return {"skill": next(skill for skill in case["skills"] if path.endswith("/" + skill["name"]))}

    def test_dream_preserves_helper_evidence_and_requires_success(self):
        data = self.root / "data"
        evidence = self.root / "evidence"
        data.mkdir()
        evidence.mkdir()
        dream = evidence / "dream"
        dream.mkdir()
        result = {"notes": ["Corrected"], "cost_complete": True, "calls": [{"total_tokens": 10}]}
        (dream / "dream.json").write_text(json.dumps(result))
        child = subprocess_result = argparse.Namespace(returncode=0, stdout="done", stderr="")
        with patch.object(study.subprocess, "run", return_value=child) as run:
            notes, details = study.dream_notes(self.args, fixture()["cases"][0], fixture()["cutoff"], [], "project", data, evidence)
        self.assertEqual(notes, ["Corrected"])
        self.assertEqual(details, result)
        self.assertEqual(run.call_args.kwargs["timeout"], 31)
        self.assertTrue((evidence / "dream-stdout.txt").exists())
        child.returncode = 2
        with patch.object(study.subprocess, "run", return_value=child), self.assertRaises(study.engine_harness.EvalError):
            study.dream_notes(self.args, {}, fixture()["cutoff"], [], "project", data, evidence)
        self.args.mock = True
        notes, details = study.dream_notes(self.args, {"notes": ["original"]}, "", [], "", data, evidence)
        self.assertEqual(notes, ["original"])
        self.assertTrue(details["mock_stub"])
        self.assertFalse(details["cost_complete"])

    def fake_engine(self):
        class Engine:
            instances = []

            def __init__(inner, binary, data, mock, timeout):
                inner.data_dir, inner.base, inner.proc = data, "http://private", None
                inner.log_path = data / "engine-stdout.log"
                inner.stopped = False
                Engine.instances.append(inner)

            def start(inner):
                inner.config_at_start = (inner.data_dir / "config.yaml").read_text()
                if not (inner.data_dir / "zwai.db").exists():
                    initialize_db(inner.data_dir)
                inner.log_path.write_text("normal exit")

            def stop(inner):
                inner.stopped = True

        return Engine

    def test_actual_http_lifecycle_all_arms_keep_fresh_state_and_complete_cost(self):
        fake = self.fake_engine()

        def http(base, method, path, body=None):
            if method == "GET" and path.startswith("/api/projects/"):
                return self.memory_reply(path)
            data = fake.instances[-1].data_dir
            if path == "/api/projects":
                return {"project": {"id": "live-project", "memory_dir": str(data / "projects/live-project/memory")}}
            if path == "/api/threads":
                return {"thread": {"id": "live-target" if body["title"].startswith("Memory transfer") else "live-original"}}
            (data / "workspace/result.json").write_text("{}")
            return {}

        traces = [{"turn": {"status": "done"}, "events": []}]
        with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                patch.object(study.engine_harness, "http_json", side_effect=http), \
                patch.object(study.engine_harness, "drive_conversation", return_value={"state": "done"}), \
                patch.object(study, "wait_for_expected_review", return_value=traces), \
                patch.object(study.engine_harness, "settled_outcome", side_effect=lambda o, t: o), \
                patch.object(study.grade, "review_settled", return_value=True), \
                patch.object(study, "final_durable_trace", return_value=traces), \
                patch.object(study.grade, "extract_metrics", return_value={"usage_facts": {"total_tokens": 100}}), \
                patch.object(study, "dream_notes", return_value=(["corrected"], {"cost_complete": True, "calls": [{"total_tokens": 20}]})):
            frozen = fixture()
            frozen["cases"][0]["disabled_tools"] = ["exec", "web_search", "web_fetch", "edit"]
            for arm in study.ARMS:
                row = study.run_attempt(self.args, frozen, frozen["cases"][0], arm, 1,
                                        {"api_key": "PRIVATE", "base_url": "http://fixture.invalid", "model": "fixture"})
                self.assertTrue(row["passed"], row)
                self.assertTrue(row["cleanup_complete"])
                self.assertTrue(row["cost_complete"])
                self.assertEqual(row["total_tokens"], 120 if arm in ("dream", "combined") else 100)
            self.assertTrue(all('disabled: ["exec", "web_search", "web_fetch", "edit"]' in engine.config_at_start for engine in fake.instances))
            frozen["kind"] = "prospective-memory-curation"
            pending = study.run_attempt(self.args, frozen, frozen["cases"][0], "baseline", 2,
                                        {"base_url": "http://fixture.invalid", "model": "fixture"})
            self.assertTrue(pending["mechanical_passed"])
            self.assertFalse(pending["passed"])
            self.assertEqual(pending["semantic_status"], "unreviewed")
            self.assertTrue(pending["memory_preservation"]["preserved"])
        self.assertEqual(len(fake.instances), 10)
        self.assertTrue(all(engine.stopped for engine in fake.instances))
        self.assertEqual(len({engine.data_dir for engine in fake.instances}), 5)
        self.assertTrue(all(not engine.data_dir.exists() for engine in fake.instances))

    def test_failure_stops_owned_engine_and_retains_missing_usage_as_unknown(self):
        fake = self.fake_engine()
        with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                patch.object(study.engine_harness, "http_json", side_effect=ValueError("PRIVATE failure")):
            row = study.run_attempt(self.args, fixture(), fixture()["cases"][0], "baseline", 1,
                                    {"api_key": "PRIVATE", "base_url": "http://fixture.invalid", "model": "fixture"})
        self.assertIn("<redacted>", row["harness_error"])
        self.assertNotIn("PRIVATE", json.dumps(row))
        self.assertFalse(row["passed"])
        self.assertFalse(row["cost_complete"])
        self.assertIsNone(row["total_tokens"])
        self.assertTrue(fake.instances[0].stopped)

    def test_saved_provider_echoes_redact_escaped_and_short_keys_without_changing_schema(self):
        for repeat, secret in enumerate(('synthetic"\\credential', 'abc'), start=20):
            with self.subTest(key_shape="escaped" if repeat == 20 else "short"):
                fake = self.fake_engine()
                original_start = fake.start

                def start(inner):
                    original_start(inner)
                    inner.log_path.write_text("provider echo " + secret)

                fake.start = start

                def http(base, method, path, body=None):
                    if method == "GET" and path.startswith("/api/projects/"):
                        return self.memory_reply(path)
                    data = fake.instances[-1].data_dir
                    if path == "/api/projects":
                        return {"project": {"id": "p", "memory_dir": str(data / "memory")}}
                    if path == "/api/threads":
                        return {"thread": {"id": "t"}}
                    (data / "workspace/result.json").write_text("{}")
                    return {}

                traces = [{"turn": {"status": "done", "final": "provider echo " + secret},
                           "events": [{"kind": "memory_review"}],
                           "llm_calls": [{"err": "provider echo " + secret, "total_tokens": 123}],
                           "nested": {"abc": [secret, 123, True, None], "total_tokens": 123}}]
                with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                        patch.object(study.engine_harness, "http_json", side_effect=http), \
                        patch.object(study.engine_harness, "drive_conversation", return_value={"state": "done"}), \
                        patch.object(study, "wait_for_expected_review", return_value=traces), \
                        patch.object(study.engine_harness, "settled_outcome", side_effect=lambda o, t: o), \
                        patch.object(study, "final_durable_trace", return_value=traces):
                    study.run_attempt(self.args, fixture(), fixture()["cases"][0], "baseline", repeat,
                                      {"api_key": secret, "base_url": "http://fixture.invalid", "model": "fixture"})
                evidence = self.args.out / "runs/case" / f"repeat-{repeat}/baseline"
                for name in ("traces.json", "traces-post-shutdown.json"):
                    saved = json.loads((evidence / name).read_text())[0]
                    self.assertEqual(saved["llm_calls"][0]["err"], "provider echo <redacted>")
                    self.assertEqual(saved["nested"], {"abc": ["<redacted>", 123, True, None], "total_tokens": 123})
                for name in ("answer.md", "engine.log"):
                    self.assertEqual((evidence / name).read_text(), "provider echo <redacted>")
                self.assertEqual(traces[0]["nested"]["abc"][0], secret)

    def test_helper_streams_redact_any_nonempty_selected_key(self):
        for index, secret in enumerate(('synthetic"\\credential', 'abc')):
            data, evidence = self.root / f"stream-data-{index}", self.root / f"stream-evidence-{index}"
            data.mkdir()
            (evidence / "dream").mkdir(parents=True)
            (evidence / "dream/dream.json").write_text(json.dumps({"notes": [], "calls": []}))
            child = argparse.Namespace(returncode=0, stdout="provider echo " + secret, stderr="provider echo " + secret)
            with patch.object(study.subprocess, "run", return_value=child):
                study.dream_notes(self.args, fixture()["cases"][0], fixture()["cutoff"], [], "project", data, evidence, [secret])
            for name in ("dream-stdout.txt", "dream-stderr.txt"):
                self.assertEqual((evidence / name).read_text(), "provider echo <redacted>")

    def test_unproven_cleanup_cannot_claim_complete_cost(self):
        fake = self.fake_engine()

        def http(base, method, path, body=None):
            if method == "GET" and path.startswith("/api/projects/"):
                return self.memory_reply(path)
            data = fake.instances[-1].data_dir
            if path == "/api/projects":
                return {"project": {"id": "p", "memory_dir": str(data / "memory")}}
            if path == "/api/threads":
                return {"thread": {"id": "t"}}
            (data / "workspace/result.json").write_text("{}")
            return {}

        receipts = [{"complete": True}, {"complete": False, "survivors": [123]}]
        with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                patch.object(study.engine_harness, "http_json", side_effect=http), \
                patch.object(study.engine_harness, "drive_conversation", return_value={"state": "done"}), \
                patch.object(study, "wait_for_expected_review", return_value=[]), \
                patch.object(study.engine_harness, "settled_outcome", side_effect=lambda o, t: o), \
                patch.object(study.grade, "review_settled", return_value=True), \
                patch.object(study.grade, "extract_metrics", return_value={"usage_facts": {"total_tokens": 100}}), \
                patch.object(study, "stop_owned", side_effect=receipts):
            row = study.run_attempt(self.args, fixture(), fixture()["cases"][0], "baseline", 2,
                                    {"base_url": "http://fixture.invalid", "model": "fixture"})
        try:
            self.assertFalse(row["cleanup_complete"])
            self.assertFalse(row["cost_complete"])
            self.assertIsNone(row["total_tokens"])
        finally:
            import shutil
            shutil.rmtree(fake.instances[0].data_dir)

    def test_summary_never_converts_mock_or_partial_cost_to_adoption(self):
        rows = [{"arm": "baseline", "passed": True, "cleanup_complete": True, "cost_complete": True, "total_tokens": 100},
                {"arm": "dream", "passed": False, "cleanup_complete": True, "cost_complete": False, "total_tokens": None}]
        summary = study.summarize(rows, "synthetic-control", 4)
        self.assertFalse(summary["adoption_accepted"])
        self.assertFalse(summary["schedule_complete"])
        self.assertEqual(summary["arms"]["baseline"]["total_tokens"], 100)
        self.assertIsNone(summary["arms"]["dream"]["total_tokens"])

    def test_cancelled_turn_does_not_require_a_review_the_product_skips(self):
        fake = self.fake_engine()

        def http(base, method, path, body=None):
            if method == "GET" and path.startswith("/api/projects/"):
                return self.memory_reply(path)
            data = fake.instances[-1].data_dir
            if path == "/api/projects":
                return {"project": {"id": "p", "memory_dir": str(data / "memory")}}
            if path == "/api/threads":
                return {"thread": {"id": "t"}}
            return {}

        traces = [{"turn": {"id": "turn", "status": "cancelled"}, "events": [],
                   "llm_calls": [{"id": 1, "agent_id": "manager", "total_tokens": 100},
                                 {"id": 2, "agent_id": "session-memory", "total_tokens": 25}]}]
        with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                patch.object(study.engine_harness, "http_json", side_effect=http), \
                patch.object(study.engine_harness, "drive_conversation", return_value={"state": "iteration_cap"}), \
                patch.object(study, "wait_for_expected_review", return_value=traces), \
                patch.object(study.engine_harness, "settled_outcome", side_effect=lambda o, t: o), \
                patch.object(study, "final_durable_trace", return_value=traces):
            row = study.run_attempt(self.args, fixture(), fixture()["cases"][0], "baseline", 5,
                                    {"base_url": "http://fixture.invalid", "model": "fixture"})
        self.assertFalse(row["passed"])
        self.assertEqual(row["harness_error"], "")
        self.assertFalse(row["review_settled"])
        self.assertFalse(row["review_expected"])
        self.assertTrue(row["cost_complete"])
        self.assertEqual(row["total_tokens"], 125)

    def test_review_status_boundary_keeps_done_error_gates_and_skips_cancelled_wait(self):
        for status, expected in (("done", True), ("error", True), ("cancelled", False), ("running", None)):
            traces = [{"turn": {"status": status}, "events": []}]
            self.assertIs(study.review_expected(traces), expected)
            self.assertFalse(study.grade.review_settled(traces))
        self.assertIsNone(study.review_expected([]))
        cancelled = [{"turn": {"status": "cancelled"}, "events": []}]
        with patch.object(study.engine_harness, "fetch_traces", return_value=cancelled), \
                patch.object(study.time, "sleep", side_effect=AssertionError("waited for nonexistent reviewer")):
            self.assertEqual(study.wait_for_expected_review("http://private", "thread", 120), cancelled)

    def test_unjoined_auxiliary_call_keeps_cancelled_cost_incomplete(self):
        fake = self.fake_engine()

        def stop(inner):
            inner.stopped = True
            inner.log_path.write_text('level=WARN msg="a session briefing was still running at shutdown"')
        fake.stop = stop

        def http(base, method, path, body=None):
            if method == "GET" and path.startswith("/api/projects/"):
                return self.memory_reply(path)
            data = fake.instances[-1].data_dir
            if path == "/api/projects":
                return {"project": {"id": "p", "memory_dir": str(data / "memory")}}
            if path == "/api/threads":
                return {"thread": {"id": "t"}}
            return {}

        traces = [{"turn": {"status": "cancelled"}, "events": [],
                   "llm_calls": [{"id": 1, "agent_id": "manager", "total_tokens": 100}]}]
        with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                patch.object(study.engine_harness, "http_json", side_effect=http), \
                patch.object(study.engine_harness, "drive_conversation", return_value={"state": "iteration_cap"}), \
                patch.object(study, "wait_for_expected_review", return_value=traces), \
                patch.object(study.engine_harness, "settled_outcome", side_effect=lambda o, t: o), \
                patch.object(study, "final_durable_trace", return_value=traces):
            row = study.run_attempt(self.args, fixture(), fixture()["cases"][0], "baseline", 7,
                                    {"base_url": "http://fixture.invalid", "model": "fixture"})
        self.assertTrue(row["cleanup_complete"])
        self.assertFalse(row["auxiliary_shutdown_joined"])
        self.assertEqual(row["auxiliary_cut_off"], ["session_memory"])
        self.assertEqual(row["task_tokens"], 100)
        self.assertFalse(row["cost_complete"])
        self.assertIsNone(row["total_tokens"])

    @unittest.skipUnless(os.environ.get("ZWAI_MEMORY_TEST_BINARY"), "opt-in identified offline engine binary")
    def test_actual_engine_cancelled_cap_has_joined_usage_without_review(self):
        args = argparse.Namespace(**vars(self.args))
        args.binary = Path(os.environ["ZWAI_MEMORY_TEST_BINARY"]).resolve()
        args.mock = True
        args.boot_timeout = 120
        args.review_wait = 10
        frozen = fixture()
        case = frozen["cases"][0]
        case["skills"][0]["description"] = 'Use <threshold> & "quoted"\tpaths C:\\work\\n 中文\u2028context'
        case["timeout_s"] = 20
        # Cross the product's event-token gate so the actual background
        # briefing must finish during owned shutdown after cancellation.
        case["prompt"] = "Inspect this private fixture.\n" + "Synthetic observation retained for briefing. " * 1500
        original_config = study.model_config
        original_capture = study.capture_memory_state

        def config(*values):
            return original_config(*values).replace("manager_max_iterations: 24", "manager_max_iterations: 1")

        def capture_without_usage_write(base, project_id):
            before = study.engine_harness.http_json(base, "GET", f"/api/projects/{project_id}/memory")["memory"]["skills"]
            state = original_capture(base, project_id)
            self.assertEqual(state["skills"][0]["description"], case["skills"][0]["description"])
            after = study.engine_harness.http_json(base, "GET", f"/api/projects/{project_id}/memory")["memory"]["skills"]
            self.assertEqual([(s["name"], s["views"]) for s in before], [(s["name"], s["views"]) for s in after])
            self.assertEqual(len(state["skills"]), len(before))
            return state

        def finish_at_cap(base, target, task):
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                status = study.engine_harness.http_json(base, "GET", f"/api/threads/{target}")["status"]
                traces = study.engine_harness.fetch_traces(base, target)
                events = [e for t in traces for e in t.get("events", [])]
                started = {e["agent_id"] for e in events if e["kind"] == "spawned"}
                finished = {e["agent_id"] for e in events if e["kind"] == "finished"}
                if status.get("awaiting_continue") and started <= finished:
                    study.engine_harness.interrupt(base, target)
                    return {"state": "iteration_cap", "iteration_cap": True}
                time.sleep(.1)
            raise AssertionError("offline engine did not reach its actual cap with joined workers")

        with patch.object(study, "model_config", side_effect=config), \
                patch.object(study, "capture_memory_state", side_effect=capture_without_usage_write), \
                patch.object(study.engine_harness, "drive_conversation", side_effect=finish_at_cap):
            row = study.run_attempt(args, frozen, case, "baseline", 6, {})
        self.assertEqual(row["harness_error"], "", row)
        self.assertFalse(row["review_expected"])
        self.assertFalse(row["review_settled"])
        self.assertTrue(row["cleanup_complete"])
        self.assertTrue(row["auxiliary_shutdown_joined"])
        self.assertIsNotNone(row["task_tokens"])
        self.assertGreater(row["task_tokens"], 0)
        # Scripted provider usage remains infrastructure evidence only.
        self.assertFalse(row["cost_complete"])
        trace_path = args.out / "runs/case/repeat-6/baseline/traces-post-shutdown.json"
        traces = json.loads(trace_path.read_text())
        self.assertEqual(traces[-1]["turn"]["status"], "cancelled")
        self.assertFalse(any(e["kind"] == "memory_review" for t in traces for e in t["events"]))
        calls = [call for trace in traces for call in trace["llm_calls"]]
        self.assertTrue(all(type(call.get("total_tokens")) is int and call["total_tokens"] > 0
                            and not call.get("err") for call in calls))
        self.assertTrue(any(call["agent_id"] == "session-memory" for call in calls))

    def test_final_trace_includes_real_delayed_durable_auxiliary_call(self):
        data = self.root / "data"
        data.mkdir()
        initialize_db(data)
        with closing(sqlite3.connect(data / "zwai.db")) as db, db:
            db.execute("CREATE TABLE llm_calls(id,thread_id,turn_id,agent_id,total_tokens,err)")
            db.execute("INSERT INTO turns VALUES ('turn','thread',1,'done','','','at','at')")
            db.execute("INSERT INTO llm_calls VALUES (1,'thread','turn','manager',100,'')")
        early = study.final_durable_trace(data, "thread")
        self.assertEqual(len(early[0]["llm_calls"]), 1)
        script = ("import sqlite3,sys,time; time.sleep(.05); db=sqlite3.connect(sys.argv[1]); "
                  "db.execute(\"INSERT INTO llm_calls VALUES (2,'thread','turn','composer-prediction',25,'')\"); "
                  "db.commit(); db.close()")
        with subprocess.Popen([sys.executable, "-c", script, str(data / "zwai.db")]) as child:
            child.wait(timeout=5)
        final = study.final_durable_trace(data, "thread")
        self.assertEqual(sum(call["total_tokens"] for call in final[0]["llm_calls"]), 125)
        self.assertEqual(final[0]["llm_calls"][-1]["agent_id"], "composer-prediction")

    def test_cleanup_checks_separate_descendant_group_and_pid_birth(self):
        def process():
            calls = iter([None, 0])
            return argparse.Namespace(pid=100, poll=lambda: next(calls, 0))
        proc = process()
        engine = argparse.Namespace(proc=proc, stop=lambda: None)
        before = {100: {"pid": 100, "parent": 1, "group": 100, "birth": "original"},
                  101: {"pid": 101, "parent": 100, "group": 101, "birth": "helper"}}
        with patch.object(study, "process_inventory", side_effect=[before, {101: before[101]}]):
            receipt = study.stop_owned(engine)
        self.assertFalse(receipt["complete"])
        self.assertEqual(receipt["survivors"][0]["pid"], 101)
        reused = {101: {**before[101], "birth": "new process"}}
        engine.proc = process()
        with patch.object(study, "process_inventory", side_effect=[before, reused]):
            self.assertTrue(study.stop_owned(engine)["complete"])
        del engine._memory_cleanup_receipt
        engine.proc = process()
        stopped = []
        engine.stop = lambda: stopped.append(True)
        with patch.object(study, "process_inventory", side_effect=[TimeoutError("inventory"), {}]):
            self.assertFalse(study.stop_owned(engine)["complete"])
        self.assertEqual(stopped, [True])

    def test_engine_crash_before_first_sample_refuses_descendant_cleanup(self):
        stopped = []
        engine = argparse.Namespace(proc=argparse.Namespace(pid=100, poll=lambda: 1), stop=lambda: stopped.append(True))
        with patch.object(study, "process_inventory", side_effect=AssertionError("unknown ancestry")):
            receipt = study.stop_owned(engine)
        self.assertFalse(receipt["complete"])
        self.assertTrue(receipt["engine_exited"])
        self.assertEqual(stopped, [True])

    def test_actual_private_process_group_is_observed_exited(self):
        with subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"], start_new_session=True) as child:
            def stop():
                child.terminate()
                child.wait(timeout=5)
            engine = argparse.Namespace(proc=child, stop=stop)
            receipt = study.stop_owned(engine)
        self.assertTrue(receipt["complete"], receipt)
        self.assertTrue(receipt["engine_exited"])

    def test_failed_dream_retains_paid_partial_call_receipt(self):
        fake = self.fake_engine()
        partial = {"notes": [], "cost_complete": False, "calls": [{"total_tokens": 20, "error": "failure"}]}

        def http(base, method, path, body=None):
            if method == "GET" and path.startswith("/api/projects/"):
                return self.memory_reply(path)
            data = fake.instances[-1].data_dir
            return ({"project": {"id": "p", "memory_dir": str(data / "memory")}} if path == "/api/projects"
                    else {"thread": {"id": "t"}})

        with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                patch.object(study.engine_harness, "http_json", side_effect=http), \
                patch.object(study, "dream_notes", side_effect=study.DreamFailure(partial)):
            row = study.run_attempt(self.args, fixture(), fixture()["cases"][0], "dream", 1,
                                    {"base_url": "http://fixture.invalid", "model": "fixture"})
        self.assertFalse(row["cost_complete"])
        self.assertEqual(row["dream"], partial)
        self.assertEqual(row["known_dream_tokens"], 20)
        self.assertIsNone(row["dream_tokens"])

    def test_fully_observed_dream_candidate_failure_never_starts_target_or_fallback(self):
        for index, failure in enumerate(("output_limit", "proposal_invalid"), start=10):
            fake = self.fake_engine()
            observed = {"notes": [], "cost_complete": True, "failure_kind": failure,
                        "error": "candidate produced no admissible proposal", "calls": [{"total_tokens": 10883}]}
            paths = []

            def http(base, method, path, body=None):
                if method == "GET" and path.startswith("/api/projects/"):
                    return self.memory_reply(path)
                paths.append(path)
                data = fake.instances[-1].data_dir
                return ({"project": {"id": "p", "memory_dir": str(data / "memory")}} if path == "/api/projects"
                        else {"thread": {"id": "t"}})

            with patch.object(study.project_history_eval, "PrivateEngine", fake), \
                    patch.object(study.engine_harness, "http_json", side_effect=http), \
                    patch.object(study, "dream_notes", side_effect=study.DreamFailure(observed)), \
                    patch.object(study.engine_harness, "drive_conversation", side_effect=AssertionError("fallback target")):
                row = study.run_attempt(self.args, fixture(), fixture()["cases"][0], "dream", index,
                                        {"base_url": "http://fixture.invalid", "model": "fixture"})
            self.assertFalse(row["passed"])
            self.assertEqual(row["harness_error"], "")
            self.assertEqual(row["candidate_error"], failure)
            self.assertFalse(row["task_started"])
            self.assertEqual(row["task_tokens"], 0)
            self.assertEqual(row["total_tokens"], 10883)
            self.assertTrue(row["cost_complete"])
            self.assertEqual(len(fake.instances), 1)
            self.assertFalse(any(path.endswith("/turns") for path in paths))

    def test_candidate_failure_keeps_four_arm_denominator_but_provider_failure_stops(self):
        binary, helper, source, config = (self.root / name for name in ("binary", "helper", "fixture.json", "config.yaml"))
        binary.write_text("binary")
        helper.write_text("helper")
        source.write_text(json.dumps(fixture()))
        config.write_text("private fixture configuration")
        provider = {"base_url": "http://fixture.invalid", "model": "fixture"}
        for kind in ("candidate", "provider"):
            calls = []

            def attempt(args, frozen, case, arm, repeat, selected):
                calls.append(arm)
                return {"case": case["id"], "arm": arm, "repeat": repeat, "passed": False,
                        "candidate_error": "output_limit" if kind == "candidate" and len(calls) == 1 else "",
                        "harness_error": "provider transport failed" if kind == "provider" else "",
                        "cleanup_complete": True, "cost_complete": kind == "candidate",
                        "task_started": False, "known_dream_tokens": 10883, "total_tokens": 10883 if kind == "candidate" else None}

            out = self.root / kind
            argv = ["--binary", str(binary), "--helper", str(helper), "--fixture", str(source), "--out", str(out),
                    "--config", str(config), "--provider", "fixture"]
            with patch.object(study.grade, "extract_provider", return_value=provider), \
                    patch.object(study, "run_attempt", side_effect=attempt):
                code = study.main(argv)
            summary = json.loads((out / "summary.json").read_text())
            self.assertEqual(code, 0 if kind == "candidate" else 2)
            self.assertEqual(len(calls), 4 if kind == "candidate" else 1)
            self.assertEqual(summary["schedule_complete"], kind == "candidate")
            if kind == "candidate":
                self.assertEqual(sum(a["candidate_failures"] for a in summary["arms"].values()), 1)

    def test_dry_run_freezes_schedule_and_binaries_without_provider_access(self):
        binary, helper, source = self.root / "binary", self.root / "helper", self.root / "fixture.json"
        binary.write_text("identified binary")
        helper.write_text("identified helper")
        source.write_text(json.dumps(fixture()))
        out = self.root / "dry"
        argv = ["--binary", str(binary), "--helper", str(helper), "--fixture", str(source), "--out", str(out), "--dry-run", "--repeats", "2"]
        with patch.object(study, "run_attempt", side_effect=AssertionError("dry run contacted engine")):
            self.assertEqual(study.main(argv), 0)
        manifest = json.loads((out / "manifest.json").read_text())
        self.assertEqual(len(manifest["schedule"]), 8)
        self.assertEqual(stat.S_IMODE((out / "manifest.json").stat().st_mode), 0o600)
        self.assertEqual(manifest["fixture_sha256"], study.protocol.file_digest(source))
        snapshots = manifest["source_snapshots"]
        self.assertEqual(len(snapshots), 7)
        for snapshot in snapshots:
            copied = out / snapshot["snapshot"]
            self.assertEqual(copied.read_bytes(), Path(snapshot["original"]).read_bytes())
            self.assertEqual(study.protocol.file_digest(copied), snapshot["sha256"])
            self.assertEqual(stat.S_IMODE(copied.stat().st_mode), 0o600)
        self.assertIn("not binary build provenance", manifest["source_snapshot_scope"])
        with self.assertRaises(SystemExit):
            study.parse_args(argv[:-3] + ["--repeats", "0"])


if __name__ == "__main__":
    unittest.main()
