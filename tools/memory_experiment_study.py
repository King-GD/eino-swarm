#!/usr/bin/env python3
"""Opt-in four-arm memory experiment on the complete HTTP engine.

The experiment never edits the user's memory or provider configuration. Each
attempt owns a fresh engine, project, original-history database and workspace.
Synthetic controls test infrastructure; only independently graded transfer
tasks can provide task-effect observations. All Dream calls count in cost.
"""

from __future__ import annotations

import argparse
from contextlib import closing
import datetime as dt
import hashlib
import json
import os
import random
import shutil
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path

import agent_eval as engine_harness
import agent_eval_grade as grade
import agent_eval_protocol as protocol
import project_history_eval

ARMS = {"baseline": (False, False), "recall": (True, False),
        "dream": (False, True), "combined": (True, True)}
OWNER = ".memory-experiment-owner"
COMMON_INSTRUCTION = ("Use available project notes and recorded procedures when relevant; recorded procedure names "
                      "discovered through files, history or connected tools may be opened with skill_view. "
                      "Verify current state before relying on historical claims.")
AUXILIARY_CUTOFF_LOGS = {
    "session_memory": "a session briefing was still running at shutdown",
    "memory_review": "a memory review was still running at shutdown",
    "title": "a conversation was still being named at shutdown",
    "composer_prediction": "a next-step suggestion was still running at shutdown",
    "compact": "a compact was still running at shutdown",
}


class DreamFailure(engine_harness.EvalError):
    """Keep paid helper receipts even when the candidate cannot be applied."""

    def __init__(self, result):
        super().__init__("Dream candidate failed; retain its original helper evidence")
        self.result = result


def fully_observed_candidate_failure(result: dict) -> bool:
    """A bounded/invalid proposal is a measured candidate outcome, not transport loss."""
    calls = result.get("calls", [])
    return (result.get("failure_kind") in ("output_limit", "proposal_invalid")
            and result.get("cost_complete") is True and bool(calls)
            and all(type(call.get("total_tokens")) is int and call["total_tokens"] > 0
                    and not call.get("err") and not call.get("error") for call in calls))


def timestamp(value: str) -> dt.datetime:
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("timestamps must include a timezone")
    return parsed


def safe_relative(value: str) -> bool:
    path = Path(value)
    return bool(value) and not path.is_absolute() and ".." not in path.parts and "\x00" not in value


def load_fixture(path: Path) -> dict:
    fixture = json.loads(path.read_text())
    if fixture.get("version") != 1 or fixture.get("kind") not in ("synthetic-control", "historical-transfer", "prospective-memory-curation"):
        raise ValueError("fixture needs version 1 and an explicit measurement kind")
    cutoff = timestamp(fixture["cutoff"])
    cases = fixture.get("cases", [])
    if not cases or len({case["id"] for case in cases}) != len(cases):
        raise ValueError("fixture needs unique nonempty cases")
    for case in cases:
        if not case["id"] or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-_" for c in case["id"]):
            raise ValueError("case IDs must be lowercase path-safe names")
        if not case.get("prompt") or not case.get("checks") or case.get("timeout_s", 240) <= 0:
            raise ValueError("each case needs a task, independent checks and positive timeout")
        for check in case["checks"]:
            grade._validate_check(case["id"], check)
        disabled = case.get("disabled_tools", [])
        if (not isinstance(disabled, list) or not all(isinstance(name, str) and name and name == name.strip()
                                                     and "\x00" not in name for name in disabled)
                or len(set(disabled)) != len(disabled)):
            raise ValueError("disabled_tools must be a unique list of nonempty tool names")
        if not all(isinstance(note, str) and note.strip() and "\n\n" not in note.replace("\r\n", "\n")
                   for note in case.get("notes", [])):
            raise ValueError("notes must be nonempty canonical entries without blank paragraphs")
        names = [skill["name"] for skill in case.get("skills", [])]
        if len(set(names)) != len(names) or any(not name or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-" for c in name) for name in names):
            raise ValueError("skill names must be unique valid memory names")
        for skill in case.get("skills", []):
            if not skill.get("description") or not skill.get("body"):
                raise ValueError("skills need applicability and body")
        for name, content in case.get("files", {}).items():
            if not safe_relative(name) or not isinstance(content, str):
                raise ValueError("fixture files must be relative textual files")
        if case.get("source_archive"):
            archive = case["source_archive"]
            source = Path(archive["path"])
            if not source.is_absolute():
                source = path.parent / source
            if protocol.file_digest(source) != archive["sha256"]:
                raise ValueError("historical source archive changed")
            archive["path"] = str(source.resolve())
        seen = set()
        for source in case.get("sources", []):
            identity = (source["thread_id"], source["seq"])
            if identity in seen or not source.get("project_id") or not source["thread_id"] or type(source["seq"]) is not int or source["seq"] <= 0:
                raise ValueError("history source identities must be complete and unique")
            seen.add(identity)
            if timestamp(source["observed_at"]) >= cutoff:
                raise ValueError("source must precede the frozen history cutoff")
            if not isinstance(source.get("text"), str) or not source.get("kind"):
                raise ValueError("history sources need original text and kind")
        project_ids = {source["project_id"] for source in case.get("sources", [])}
        if len(project_ids) > 1:
            raise ValueError("a case must bind all Dream history to one project")
        if not case.get("sources") or not any(source["kind"] in ("human", "user", "user_message", "steer", "tool_result")
                                              for source in case["sources"]):
            raise ValueError("four-arm fixtures need potentially admissible original Dream sources")
        if any(source["kind"] not in ("human", "user", "user_message", "steer", "assistant", "runtime", "tool_call", "tool_result")
               for source in case["sources"]):
            raise ValueError("source kind is not supported by Dream")
        if fixture["kind"] == "historical-transfer" and not case.get("origin"):
            raise ValueError("historical transfer requires a reviewable origin description")
    return fixture


def redact_text(text: str, secrets: list[str] | None = None) -> str:
    for secret in secrets or []:
        if secret:
            text = text.replace(secret, "<redacted>")
    return text


def redact_values(value, secrets: list[str] | None = None):
    """Redact before encoding without modifying schema keys, numbers or input."""
    if isinstance(value, str):
        return redact_text(value, secrets)
    if isinstance(value, dict):
        return {key: redact_values(item, secrets) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [redact_values(item, secrets) for item in value]
    return value


def write_json(path: Path, value, secrets: list[str] | None = None) -> None:
    protocol.private_write(path, json.dumps(redact_values(value, secrets), indent=2, ensure_ascii=False) + "\n")


def snapshot_sources(out: Path, fixture: Path, manifest: dict) -> list[dict]:
    """Retain exact observed source bytes, independently of later workspace edits."""
    inputs = [(Path(name), Path("python") / Path(name).name, expected)
              for name, expected in manifest["harness_sources"].items()]
    inputs.append((fixture, Path("fixture.json"), manifest["fixture_sha256"]))
    snapshots = []
    for original, relative, expected in inputs:
        body = original.read_bytes()
        if hashlib.sha256(body).hexdigest() != expected:
            raise ValueError("source changed while freezing its byte snapshot")
        destination = out / "source-snapshot" / relative
        destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as output:
            output.write(body)
        snapshots.append({"original": str(original), "snapshot": str(destination.relative_to(out)), "sha256": expected})
    return snapshots


def model_config(provider: dict, helper: Path, memory_dir: Path | None, cap: int, mock: bool, disabled_tools: list[str] | None = None) -> str:
    # Preserve the selected route's provider settings; never copy another Key.
    body = "" if mock else grade.render_eval_config(provider, "eval", 0)
    body += "swarm:\n    auto_title: false\n    manager_max_iterations: 24\n"
    if cap:
        body += f"    max_completion_tokens: {cap}\n"
    body += "remote:\n    enabled: false\nmemory:\n    enabled: true\n    auto_review: true\n"
    if disabled_tools:
        body += "tools:\n    disabled: " + json.dumps(disabled_tools) + "\n"
    if memory_dir is not None:
        q = json.dumps
        body += ("mcp:\n    servers:\n        - id: memory_exp\n          name: Search recorded project procedures\n"
                 "          transport: stdio\n          enabled: true\n"
                 f"          command: {q(str(helper))}\n          args: {q(['serve', '--memory-dir', str(memory_dir)])}\n")
    return body


def seed_memory(path: Path, notes: list[str], skills: list[dict]) -> None:
    if any("\n" in skill["description"] or "\r" in skill["description"] for skill in skills):
        raise ValueError("skill descriptions must be single-line for the Store parser")
    protocol.private_write(path / "MEMORY.md", "\n\n".join(notes) + ("\n" if notes else ""))
    for skill in skills:
        # Match Store's small front-matter parser: it unescapes quotes and
        # backslashes, not JSON control-character or Unicode escapes.
        description = '"' + skill["description"].replace('\\', '\\\\').replace('"', '\\"') + '"'
        text = f"---\nname: {json.dumps(skill['name'])}\ndescription: {description}\n---\n\n{skill['body']}\n"
        protocol.private_write(path / "skills" / skill["name"] / "SKILL.md", text)


def seed_history(data: Path, sources: list[dict], project_id: str, mapping: dict[str, str]) -> list[dict]:
    if not (data / OWNER).is_file():
        raise ValueError("private owner marker missing before history seed")
    mapped = [{**source, "project_id": project_id, "thread_id": mapping[source["thread_id"]]} for source in sources]
    with closing(sqlite3.connect(data / "zwai.db")) as db, db:
        if db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]:
            raise ValueError("refusing to seed nonempty original history")
        for source in sorted(mapped, key=lambda row: (row["thread_id"], row["seq"])):
            turn = f"tn_memory_source_{source['thread_id']}_{source['seq']}"
            kind = source["kind"]
            role = "user" if kind in ("human", "user", "steer", "user_message") else "assistant"
            event_kind = "user_message" if role == "user" else kind
            if kind == "assistant":
                event_kind = "agent_message"
            db.execute("INSERT INTO turns (id,thread_id,seq,status,user_text,final,started_at,ended_at) VALUES (?,?,?,'done',?,?,?,?)",
                       (turn, source["thread_id"], source["seq"], source["text"] if role == "user" else "",
                        source["text"] if role == "assistant" else "", source["observed_at"], source["observed_at"]))
            # Canonical tool source text also remains queryable through events;
            # it must not masquerade as assistant prose in messages.
            if kind.startswith("tool_"):
                role = "tool" if kind == "tool_result" else "assistant"
            db.execute("INSERT INTO messages (thread_id,turn_id,seq,role,agent_id,source,content,created_at) VALUES (?,?,?,?,?,?,?,?)",
                       (source["thread_id"], turn, source["seq"], role, source.get("agent_id", "manager"),
                        None if kind == "user_message" else "human" if role == "user" else "", source["text"], source["observed_at"]))
            db.execute("INSERT INTO events (thread_id,turn_id,seq,kind,agent_id,tool_call_id,text,err,created_at) VALUES (?,?,?,?,?,?,?,?,?)",
                       (source["thread_id"], turn, source["seq"], event_kind, source.get("agent_id", "manager"),
                        source.get("tool_call_id", ""), source["text"], source.get("err", ""), source["observed_at"]))
        db.execute("DELETE FROM search_docs")
    return mapped


def dream_notes(args, case: dict, cutoff: str, sources: list[dict], project_id: str, data: Path, evidence: Path,
                secrets: list[str] | None = None) -> tuple[list[str], dict]:
    if args.mock:
        return case.get("notes", []), {"mock_stub": True, "calls": [], "cost_complete": False,
                                      "error": "Dream not executed in engine mock mode"}
    request = {"project_id": project_id, "cutoff": cutoff, "notes": case.get("notes", []),
               "skills": case.get("skills", []), "sources": sources}
    input_path = data / "dream-input.json"
    write_json(input_path, request)
    command = [str(args.helper), "dream", "--input", str(input_path), "--out", str(evidence / "dream"),
               "--config", str(data / "config.yaml"), "--provider", "eval", "--timeout-seconds", str(args.dream_timeout)]
    # The helper's own context owns cancellation; a second outer deadline adds
    # evidence/exit time, and subprocess.run always reaps this direct child.
    run = subprocess.run(command, capture_output=True, text=True, timeout=args.dream_timeout + 30)
    protocol.private_write(evidence / "dream-stdout.txt", redact_text(run.stdout, secrets))
    protocol.private_write(evidence / "dream-stderr.txt", redact_text(run.stderr, secrets))
    result = json.loads((evidence / "dream" / "dream.json").read_text())
    if run.returncode or result.get("error") or not isinstance(result.get("notes"), list):
        raise DreamFailure(result)
    return result["notes"], result


def memory_hashes(path: Path) -> dict:
    return grade.hash_tree(path) if path.exists() else {}


def memory_content_hashes(path: Path) -> dict:
    return {name: value for name, value in memory_hashes(path).items()
            if name == "MEMORY.md" or name.endswith("/SKILL.md")}


def capture_memory_state(base: str, project_id: str) -> dict:
    """Read the actual API's complete memory and every skill, without ViewSkill."""
    response = engine_harness.http_json(base, "GET", f"/api/projects/{project_id}/memory")
    view = response["memory"]
    notes = view["memory"]["entries"] or []
    catalog = view["skills"]
    if not isinstance(notes, list) or not all(isinstance(note, str) for note in notes) or not isinstance(catalog, list):
        raise ValueError("memory API returned malformed notes or catalog")
    skills, seen = [], set()
    for entry in catalog:
        name = entry["name"]
        if not isinstance(name, str) or not name or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-" for c in name) or name in seen:
            raise ValueError("memory API returned an invalid or duplicate skill name")
        seen.add(name)
        skill = engine_harness.http_json(base, "GET", f"/api/projects/{project_id}/skills/{name}")["skill"]
        if skill.get("name") != name or not all(isinstance(skill.get(key), str) for key in ("description", "body")):
            raise ValueError("memory API returned incomplete skill content")
        skills.append(skill)
    return {"project_id": project_id, "notes": notes, "skills": skills, "memory_response": response,
            "observation": "Full read-only API snapshot before shutdown; skill usage is not incremented by these GETs."}


def memory_preservation(original_notes: list[str], original_skills: list[dict], final_state: dict) -> dict:
    """Distinguish exact old content from complete retained text, never truth."""
    report = {"complete": False, "preserved": False, "exact_unchanged": False,
              "full_original_text_retained": False, "semantic_status": "unreviewed"}
    notes, skills = final_state.get("notes"), final_state.get("skills")
    if (not isinstance(notes, list) or not all(isinstance(note, str) for note in notes)
            or not isinstance(skills, list) or not all(isinstance(skill, dict) and all(isinstance(skill.get(key), str)
                for key in ("name", "description", "body")) for skill in skills)
            or len({skill["name"] for skill in skills}) != len(skills)):
        return {**report, "reason": "incomplete_or_ambiguous_final_state"}
    normalized_notes = {" ".join(note.split()) for note in notes}
    normalized_bodies = [" ".join(skill["body"].split()) for skill in skills]
    retained, moved, missing = [], [], []
    for index, note in enumerate(original_notes):
        normalized = " ".join(note.split())
        if normalized in normalized_notes:
            retained.append(index)
        elif any(" " + normalized + " " in " " + body + " " for body in normalized_bodies):
            moved.append(index)
        else:
            missing.append(index)
    by_name = {skill["name"]: skill for skill in skills}
    unchanged, changed, missing_skills, lost_bodies, changed_descriptions, retained_bodies = [], [], [], [], [], []
    for original in original_skills:
        found = by_name.get(original["name"])
        if found is None:
            missing_skills.append(original["name"])
        else:
            if any(found[key] != original[key] for key in ("description", "body")):
                changed.append(original["name"])
            else:
                unchanged.append(original["name"])
            if found["description"] != original["description"]:
                changed_descriptions.append(original["name"])
            old_body = " ".join(original["body"].split())
            new_body = " ".join(found["body"].split())
            if " " + old_body + " " in " " + new_body + " ":
                retained_bodies.append(original["name"])
            else:
                lost_bodies.append(original["name"])
    full_text_retained = not missing and not missing_skills and not lost_bodies and not changed_descriptions
    return {**report, "complete": True, "preserved": full_text_retained,
            "exact_unchanged": all(note in notes for note in original_notes) and not changed and not missing_skills,
            "full_original_text_retained": full_text_retained,
            "retained_note_indexes": retained, "moved_note_indexes": moved, "missing_note_indexes": missing,
            "unchanged_skill_names": unchanged, "changed_skill_names": changed, "missing_skill_names": missing_skills,
            "retained_original_skill_body_names": retained_bodies, "lost_original_skill_body_names": lost_bodies,
            "changed_skill_description_names": changed_descriptions,
            "scope": "Original notes must match a whole final note or complete contiguous text inside a skill body after whitespace collapse. Original skill bodies must be retained contiguously in the same-name skill, preserving description. Negating/obsolete wrappers, applicability and new-rule semantics require independent review; text retention alone is not semantic acceptance."}


def prepare_workspace(case: dict, workspace: Path) -> None:
    archive = case.get("source_archive")
    if archive:
        path = Path(archive["path"])
        if protocol.file_digest(path) != archive["sha256"]:
            raise ValueError("historical source archive changed before extraction")
        with tarfile.open(path) as source:
            for member in source.getmembers():
                if not safe_relative(member.name) or not (member.isdir() or member.isfile()) or ".git" in Path(member.name).parts:
                    raise ValueError("historical archive contains a link or unsafe member")
            source.extractall(workspace, filter="data")
    for name, content in case.get("files", {}).items():
        protocol.private_write(workspace / name, content)


def process_inventory() -> dict:
    run = subprocess.run(["ps", "-axo", "pid=,ppid=,pgid=,lstart="], capture_output=True, text=True, timeout=5, check=True)
    rows = {}
    for line in run.stdout.splitlines():
        fields = line.strip().split(None, 3)
        if len(fields) != 4:
            raise ValueError("process inventory contains an unreadable row")
        pid, parent, group = map(int, fields[:3])
        rows[pid] = {"pid": pid, "parent": parent, "group": group, "birth": fields[3]}
    return rows


def stop_owned(engine) -> dict:
    """Prove exit of the private group and sampled descendants, including MCP.

    MCP owns a separate group, so group emptiness alone is insufficient. The
    birth identity prevents a reused PID from being mistaken for a survivor.
    Failed inventory still stops/reaps the directly owned Popen child, but
    refuses data removal and the next attempt.
    """
    if engine.proc is None:
        engine.stop()
        return {"complete": True, "process_not_started": True}
    if engine.proc.poll() is not None:
        cached = getattr(engine, "_memory_cleanup_receipt", None)
        if cached and cached["complete"]:
            return cached
        engine.stop()
        return {"complete": False, "engine_exited": True,
                "inventory_error": "engine exited before its first descendant identity sample"}
    pid = engine.proc.pid
    receipt = {"engine_pid": pid, "owned_group": pid, "complete": False, "sampled_descendants": []}
    before = {}
    try:
        before = process_inventory()
        owned = {pid}
        while True:
            found = {p for p, row in before.items() if row["parent"] in owned}
            if found <= owned:
                break
            owned |= found
        receipt["sampled_descendants"] = [before[p] for p in sorted(owned) if p != pid and p in before]
    except Exception as error:
        receipt["inventory_error"] = str(error)
    finally:
        engine.stop()
    try:
        after = process_inventory()
        sampled = {row["pid"]: row for row in receipt["sampled_descendants"]}
        receipt["survivors"] = [row for p, row in after.items()
                                if row["group"] == pid or (p in sampled and row["birth"] == sampled[p]["birth"])]
        receipt["engine_exited"] = engine.proc.poll() is not None
        receipt["complete"] = receipt["engine_exited"] and not receipt["survivors"] and not receipt.get("inventory_error")
    except Exception as error:
        receipt["exit_inventory_error"] = str(error)
    if receipt["complete"]:
        engine._memory_cleanup_receipt = receipt
    return receipt


def final_durable_trace(data: Path, thread_id: str) -> list[dict]:
    """Read final original rows only after the directly owned engine exited."""
    with closing(sqlite3.connect((data / "zwai.db").as_uri() + "?mode=ro", uri=True)) as db:
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA query_only=ON")
        db.execute("BEGIN")
        turns = [dict(row) for row in db.execute("SELECT * FROM turns WHERE thread_id=? ORDER BY seq", (thread_id,))]
        traces = []
        for turn in turns:
            trace = {"turn": turn}
            for table in ("events", "messages", "llm_calls"):
                order = "id" if table == "llm_calls" else "seq"
                trace[table] = [dict(row) for row in db.execute(
                    f"SELECT * FROM {table} WHERE thread_id=? AND turn_id=? ORDER BY {order}", (thread_id, turn["id"]))]
            traces.append(trace)
        return traces


def review_expected(traces: list[dict]) -> bool | None:
    """Cancellation deliberately skips product memory review; other gates remain."""
    if not traces:
        return None
    status = traces[-1].get("turn", {}).get("status")
    if status == "cancelled":
        return False
    if status in ("done", "error"):
        return True
    return None


def wait_for_expected_review(base: str, thread_id: str, timeout_s: float) -> list[dict]:
    deadline = time.monotonic() + timeout_s
    while True:
        traces = engine_harness.fetch_traces(base, thread_id)
        if review_expected(traces) is False or grade.review_settled(traces) or time.monotonic() >= deadline:
            return traces
        time.sleep(engine_harness.POLL_S)


def run_attempt(args, fixture: dict, case: dict, arm: str, repeat: int, provider: dict) -> dict:
    recall, dreaming = ARMS[arm]
    evidence = args.out / "runs" / case["id"] / f"repeat-{repeat}" / arm
    evidence.mkdir(parents=True, mode=0o700)
    data = Path(tempfile.mkdtemp(prefix="zwai-memory-experiment-"))
    protocol.private_write(data / OWNER, "memory-experiment-v1\n")
    secret = provider.get("api_key", "")
    secrets = [secret] if secret else []
    result = {"case": case["id"], "arm": arm, "repeat": repeat, "passed": False,
              "measurement_kind": "offline-mock" if args.mock else fixture["kind"], "harness_error": "",
              "candidate_error": "", "task_started": False,
              "cleanup_complete": False, "cleanup_receipts": [], "cost_complete": False, "dream": None}
    engine = None
    started = time.monotonic()
    workspace = data / "workspace"
    traces = []

    def save(name: str, value) -> None:
        write_json(evidence / name, value, secrets)

    try:
        protocol.private_write(data / "config.yaml", model_config(provider, args.helper, None, args.max_completion_tokens,
                                                                  args.mock, case.get("disabled_tools")))
        workspace.mkdir(mode=0o700)
        prepare_workspace(case, workspace)
        initial = grade.hash_tree(workspace)
        save("initial-files.json", initial)
        engine = project_history_eval.PrivateEngine(args.binary, data, args.mock, args.boot_timeout)
        engine.start()
        project = engine_harness.http_json(engine.base, "POST", "/api/projects", {
            "name": "Memory experiment " + case["id"], "memory_enabled": True, "workdir": str(workspace),
            "system_prompt": "\n\n".join(filter(None, [case.get("project_instruction", ""), COMMON_INSTRUCTION])),
        })["project"]
        mapping = {}
        for thread in sorted({s["thread_id"] for s in case.get("sources", [])}):
            mapping[thread] = engine_harness.http_json(engine.base, "POST", "/api/threads", {
                "title": "Original memory source " + thread, "project_id": project["id"],
            })["thread"]["id"]
        target = engine_harness.http_json(engine.base, "POST", "/api/threads", {
            "title": "Memory transfer " + case["id"], "project_id": project["id"],
        })["thread"]["id"]
        result["cleanup_receipts"].append(stop_owned(engine))
        if not result["cleanup_receipts"][-1]["complete"]:
            raise ValueError("private engine or observed descendants did not prove exit before history seed")
        memory_dir = Path(project["memory_dir"])
        if not memory_dir.resolve().is_relative_to(data.resolve()):
            raise ValueError("engine returned memory outside this attempt's private data")
        sources = seed_history(data, case.get("sources", []), project["id"], mapping)
        save("source-map.json", {"mapping": mapping, "sources": sources, "cutoff": fixture["cutoff"]})
        notes = case.get("notes", [])
        seed_memory(memory_dir, notes, case.get("skills", []))
        save("memory-before-dream.json", memory_hashes(memory_dir))
        if dreaming:
            notes, result["dream"] = dream_notes(args, case, fixture["cutoff"], sources, project["id"], data, evidence, secrets)
            seed_memory(memory_dir, notes, case.get("skills", []))
        save("memory-task-start.json", {"hashes": memory_hashes(memory_dir), "notes": notes})
        protocol.private_write(data / "config.yaml", model_config(provider, args.helper, memory_dir if recall else None,
                                                                    args.max_completion_tokens, args.mock, case.get("disabled_tools")))
        engine = project_history_eval.PrivateEngine(args.binary, data, args.mock, args.boot_timeout)
        engine.start()
        task_started = time.monotonic()
        result["task_started"] = True
        engine_harness.http_json(engine.base, "POST", f"/api/threads/{target}/turns", {"text": case["prompt"]})
        outcome = engine_harness.drive_conversation(engine.base, target, {**case, "timeout_s": case.get("timeout_s", 240)})
        result["task_s"] = time.monotonic() - task_started
        traces = wait_for_expected_review(engine.base, target, args.review_wait)
        outcome = engine_harness.settled_outcome(outcome, traces)
        answer = grade.final_answer(traces)
        final_memory = capture_memory_state(engine.base, project["id"])
        save("project-memory-final.json", final_memory)
        result["memory_preservation"] = memory_preservation(case.get("notes", []), case.get("skills", []), final_memory)
        api_content_hashes = memory_content_hashes(memory_dir)
        checks = grade.run_checks(case["checks"], workspace, answer, traces, initial)
        mechanical_passed = outcome["state"] == "done" and all(c["ok"] for c in checks)
        semantic_pending = fixture["kind"] == "prospective-memory-curation"
        result.update({"outcome": outcome, "checks": checks, "metrics": grade.extract_metrics(traces),
                       "review_settled": grade.review_settled(traces),
                       "review_expected": review_expected(traces),
                       "mechanical_passed": mechanical_passed, "semantic_status": "unreviewed" if semantic_pending else "not_gated_by_this_harness",
                       "passed": mechanical_passed and not semantic_pending})
        protocol.private_write(evidence / "answer.md", redact_text(answer, secrets))
        save("traces.json", traces)
        save("memory-task-end.json", memory_hashes(memory_dir))
        engine_harness.copy_workspace(workspace, evidence / "workspace")
    except BaseException as exc:
        if isinstance(exc, DreamFailure):
            result["dream"] = exc.result
        if isinstance(exc, DreamFailure) and fully_observed_candidate_failure(exc.result):
            result["candidate_error"] = exc.result["failure_kind"]
            result["outcome"] = {"state": "candidate_failed", "stage": "dream", "failure_kind": result["candidate_error"]}
        else:
            result["harness_error"] = redact_text(f"{type(exc).__name__}: {exc}", secrets)
        result["passed"] = False
        if result["task_started"] and engine is not None and engine.base:
            try:
                if "target" in locals():
                    traces = engine_harness.fetch_traces(engine.base, target)
                    save("traces-on-error.json", traces)
            except Exception as capture_error:
                result["trace_capture_error"] = redact_text(str(capture_error), secrets)
        if not isinstance(exc, Exception):
            result["interrupted"] = True
            raise
    finally:
        # Saving a provisional receipt must never skip directly-owned cleanup.
        try:
            if engine is not None:
                result["cleanup_receipts"].append(stop_owned(engine))
            result["cleanup_complete"] = all(receipt["complete"] for receipt in result["cleanup_receipts"])
            if result["cleanup_complete"] and "target" in locals() and "task_started" in locals():
                traces = final_durable_trace(data, target)
                save("traces-post-shutdown.json", traces)
                result["metrics"] = grade.extract_metrics(traces)
                result["review_expected"] = review_expected(traces)
                result["review_settled"] = grade.review_settled(traces)
                if "memory_dir" in locals():
                    save("memory-post-shutdown.json", memory_hashes(memory_dir))
                    if "api_content_hashes" in locals() and api_content_hashes != memory_content_hashes(memory_dir):
                        result["memory_preservation"].update(complete=False, preserved=False, reason="content_changed_after_api_snapshot")
                        raise ValueError("memory content changed after its before-shutdown API snapshot")
            if engine is not None and engine.log_path.exists():
                log = engine.log_path.read_text(errors="replace")
                protocol.private_write(evidence / "engine.log", redact_text(log, secrets))
                result["review_cut_off"] = grade.review_cut_off(log)
                result["auxiliary_cut_off"] = [kind for kind, warning in AUXILIARY_CUTOFF_LOGS.items() if warning in log]
                result["auxiliary_shutdown_joined"] = not result["auxiliary_cut_off"]
        except Exception as cleanup_error:
            result["cleanup_error"] = redact_text(str(cleanup_error), secrets)
            result["evidence_complete"] = False
            result["passed"] = False
            if not result["harness_error"]:
                result["harness_error"] = result["cleanup_error"]
        result["wall_s"] = time.monotonic() - started
        task_tokens = result.get("metrics", {}).get("usage_facts", {}).get("total_tokens")
        if not result["task_started"] and result["candidate_error"]:
            # No target request or fallback occurred. This zero is established
            # by control flow, not inferred from absent provider usage.
            task_tokens = 0
            result["metrics"] = {"usage_facts": {"calls": [], "total_tokens": 0, "known_total_tokens": 0}}
        dream = result.get("dream") or {}
        dream_tokens = sum(int(call.get("total_tokens", 0)) for call in dream.get("calls", []))
        task_calls = [call for trace in traces for call in trace.get("llm_calls", [])]
        failed_calls = any(call.get("err") or call.get("error") for call in task_calls)
        dream_failed_calls = any(call.get("err") or call.get("error") for call in dream.get("calls", []))
        task_cost_complete = ((not result["task_started"] and bool(result["candidate_error"]))
                              or (result["task_started"] and task_tokens is not None
                                  and (result.get("review_expected") is False or result.get("review_settled") is True)
                                  and not result.get("review_cut_off")))
        complete = (task_cost_complete and not failed_calls and not dream_failed_calls
                    and (not dreaming or dream.get("cost_complete") is True) and not args.mock
                    and result.get("auxiliary_shutdown_joined") is True
                    and result["cleanup_complete"] and result.get("evidence_complete", True) and not result["harness_error"])
        result.update({"cost_complete": complete, "task_tokens": task_tokens,
                       "known_dream_tokens": dream_tokens,
                       "dream_tokens": (dream_tokens if dream.get("cost_complete") is True else None) if dreaming else 0,
                       "total_tokens": task_tokens + dream_tokens if complete else None})
        save("result.json", result)
        if result["cleanup_complete"] and result.get("evidence_complete", True):
            shutil.rmtree(data)
        else:
            save("retained-runtime.json", {"data_dir": str(data)})
    return result


def summarize(rows: list[dict], kind: str, expected: int) -> dict:
    arms = {}
    for arm in ARMS:
        selected = [row for row in rows if row["arm"] == arm]
        arms[arm] = {"runs": len(selected), "passed": sum(row["passed"] for row in selected),
                     "harness_errors": sum(bool(row.get("harness_error")) for row in selected),
                     "candidate_failures": sum(bool(row.get("candidate_error")) for row in selected),
                     "targets_not_started": sum(row.get("task_started") is False for row in selected),
                     "known_partial_tokens": sum((row.get("metrics", {}).get("usage_facts", {}).get("known_total_tokens") or 0)
                                                 + row.get("known_dream_tokens", 0) for row in selected),
                     "total_tokens": sum(row["total_tokens"] for row in selected) if selected and all(row.get("cost_complete") for row in selected) else None}
    complete = len(rows) == expected and all(row.get("cleanup_complete") for row in rows)
    return {"measurement_kind": kind, "schedule_complete": complete, "expected_runs": expected,
            "arms": arms, "adoption_accepted": False,
            "interpretation": "Descriptive observations; synthetic/mock controls never establish task effectiveness. Independent transfer review and a preregistered adoption gate remain required."}


def parse_args(argv: list[str]):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--helper", required=True, type=Path)
    parser.add_argument("--fixture", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--config", type=Path, default=engine_harness.DEFAULT_CONFIG)
    parser.add_argument("--provider", default="")
    parser.add_argument("--model", default="")
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--seed", type=int, default=0)
    parser.add_argument("--max-completion-tokens", type=int, default=8192)
    parser.add_argument("--boot-timeout", type=float, default=60)
    parser.add_argument("--dream-timeout", type=float, default=120)
    parser.add_argument("--review-wait", type=float, default=120)
    parser.add_argument("--mock", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    if args.repeats < 1 or args.max_completion_tokens < 0 or min(args.boot_timeout, args.dream_timeout, args.review_wait) <= 0:
        parser.error("invalid repeat count or budgets")
    if not args.mock and not args.dry_run and not args.provider:
        parser.error("real-model runs require --provider")
    for name in ("binary", "helper", "fixture", "out", "config"):
        setattr(args, name, getattr(args, name).resolve())
    return args


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    fixture = load_fixture(args.fixture)
    schedule = [{"case": case["id"], "repeat": repeat, "arm": arm}
                for case in fixture["cases"] for repeat in range(1, args.repeats + 1) for arm in ARMS]
    random.Random(args.seed).shuffle(schedule)
    provider = {} if args.mock or args.dry_run else grade.select_model(
        grade.extract_provider(args.config.read_text(), args.provider), args.model)
    args.out.mkdir(mode=0o700)
    manifest = {"version": 4, "fixture_sha256": protocol.file_digest(args.fixture),
                "failure_accounting": "observed_output_limit_or_invalid_proposal_is_candidate_failure_without_target_fallback",
                "review_accounting": "cancelled_turn_skips_review_but_requires_final_joined_calls_and_owned_cleanup",
                "binary_sha256": protocol.file_digest(args.binary), "helper_sha256": protocol.file_digest(args.helper),
                "fixture_kind": fixture["kind"], "cutoff": fixture["cutoff"], "schedule": schedule,
                "provider_id": args.provider, "model": provider.get("model"), "mock": args.mock,
                "common_instruction": COMMON_INSTRUCTION,
                "disabled_tools_by_case": {case["id"]: case.get("disabled_tools", []) for case in fixture["cases"]},
                "controlled_tools_scope": "Explicit common tool-catalog deviation for all arms; not kernel confinement. Query/history and memory tool semantics remain the product's.",
                "memory_preservation_oracle": "exact_unchanged_and_complete_contiguous_original_text_retained_v2_semantics_unreviewed",
                "harness_sources": {str(Path(module.__file__).resolve()): protocol.file_digest(Path(module.__file__))
                                    for module in (sys.modules[__name__], engine_harness, grade, protocol,
                                                   project_history_eval, grade.agent_eval_usage)},
                "budgets": {key: getattr(args, key) for key in ("max_completion_tokens", "dream_timeout", "review_wait")}}
    manifest["source_snapshots"] = snapshot_sources(args.out, args.fixture, manifest)
    manifest["source_snapshot_scope"] = "Observed Python harness and fixture source bytes at freeze; not binary build provenance."
    write_json(args.out / "manifest.json", manifest)
    if args.dry_run:
        print(args.out / "manifest.json")
        return 0
    rows = []
    cases = {case["id"]: case for case in fixture["cases"]}
    for item in schedule:
        for name, expected in manifest["harness_sources"].items():
            if protocol.file_digest(Path(name)) != expected:
                raise ValueError("frozen harness source changed before next attempt")
        for path, key in ((args.fixture, "fixture_sha256"), (args.binary, "binary_sha256"), (args.helper, "helper_sha256")):
            if protocol.file_digest(path) != manifest[key]:
                raise ValueError("frozen experiment input changed before next attempt: " + key)
        print(f"{item['case']} repeat {item['repeat']} {item['arm']}", flush=True)
        row = run_attempt(args, fixture, cases[item["case"]], item["arm"], item["repeat"], provider)
        rows.append(row)
        write_json(args.out / "summary.json", summarize(rows, "offline-mock" if args.mock else fixture["kind"], len(schedule)))
        # Infrastructure/cost failures stop the schedule; task failures stay in
        # the denominator and are never silently retried or replaced.
        if row["harness_error"] or not row["cleanup_complete"] or (not args.mock and not row["cost_complete"]):
            return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
