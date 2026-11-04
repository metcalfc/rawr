"""Behavioral reconciliation tests with a fault-injectable native Rex model."""
import copy
from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from rawr.project import Projects
from rawr.state import Error, Store


class FakeRex:
    """Models native surfaces separately from running programs, as Rex does.

    There is intentionally no terminal-input, stop, or close process API.
    Unsupported mutations fail the test instead of silently succeeding.
    """
    executable = sys.executable
    server = None
    client = None

    def __init__(self):
        self.host = "test-host"
        self.generation = "generation-1"
        self.data = {}
        self.surfaces = {}
        self.processes = {}
        self.calls = []
        self.counter = 0
        self.create_count = 0
        self.split_count = 0
        self.timeout_create = False
        self.timeout_split = False
        self.before_view = None
        self.balance_on_moves = False
        self.pending_client_balance = set()
        self.session_blocks = {}
        self.closed_ids = set()

    def identity(self):
        self.calls.append(("identity", {}))
        return {"host_id": self.host, "instance_id": self.generation, "endpoint": "unix:///tmp/fake-rex"}

    def sessions(self):
        self.calls.append(("sessions", {}))
        return [{"session_id": sid} for sid in self.data]

    def _ids(self, node):
        if "block_id" in node:
            return [node["block_id"]]
        return self._ids(node["split"]["before"]) + self._ids(node["split"]["after"])

    def _surface(self, specification):
        self.counter += 1
        bid = f"block-{self.counter}"
        self.surfaces[bid] = {"block_id": bid, "label": specification.get("label", ""), "host_id": self.host}
        argv = specification.get("options", {}).get("command", ["/bin/zsh"])
        child = {"pid": 10000 + self.counter, "path": os.path.realpath(argv[0]), "name": Path(argv[0]).name}
        if len(argv) > 1 and Path(argv[1]).is_absolute() and Path(argv[1]).is_file():
            child["script"] = argv[1]
        # Deliberately include forbidden-to-save observations in process replies.
        self.processes[bid] = {
            "child": child, "foreground": copy.deepcopy(child), "last_exit": None,
            "arbitrary_argv": ["SECRET-TOKEN-IN-PROCESS"],
            "transcript": "SECRET-TRANSCRIPT", "environment": {"TOKEN": "SECRET-ENV"},
        }
        return {"block_id": bid}

    def _layout(self, specification):
        if "block" in specification:
            return self._surface(specification["block"])
        value = specification["split"]
        return {"split": {"direction": value["direction"], "ratio": value["ratio"],
                          "before": self._layout(value["before"]), "after": self._layout(value["after"])}}

    def view(self, sid):
        self.calls.append(("view", {"session_id": sid}))
        if self.before_view:
            self.before_view(sid)
        value = copy.deepcopy(self.data[sid])
        for window in value["windows"]:
            for layer in window["layers"]:
                layer["blocks"] = [copy.deepcopy(self.surfaces[bid]) for bid in self._ids(layer["layout"])]
        return value

    def process(self, sid, bid):
        self.calls.append(("process", {"session_id": sid, "block_id": bid}))
        return copy.deepcopy(self.processes[bid])

    def select(self, sid, bid=None):
        self.calls.append(("select", {"session_id": sid, "block_id": bid}))
        if bid:
            self.call("session.focus_block", session_id=sid, block_id=bid)
        return "selected in fake Rex"

    def _remove(self, node, bid):
        if "block_id" in node:
            return None if node["block_id"] == bid else node
        branch = node["split"]
        before = self._remove(branch["before"], bid)
        after = self._remove(branch["after"], bid)
        if before is None:
            return after
        if after is None:
            return before
        return {"split": {**branch, "before": before, "after": after}}

    def _insert(self, node, anchor, addition, direction, side, ratio):
        if "block_id" in node:
            if node["block_id"] != anchor:
                return node
            before, after = (addition, node) if side == "before" else (node, addition)
            return {"split": {"direction": direction, "ratio": ratio, "before": before, "after": after}}
        branch = node["split"]
        return {"split": {**branch,
            "before": self._insert(branch["before"], anchor, addition, direction, side, ratio),
            "after": self._insert(branch["after"], anchor, addition, direction, side, ratio)}}

    def _resize(self, node, before, after, direction, ratio):
        if "block_id" in node:
            return False
        branch = node["split"]
        if branch["direction"] == direction and before in self._ids(branch["before"]) and after in self._ids(branch["after"]):
            branch["ratio"] = ratio
            return True
        return self._resize(branch["before"], before, after, direction, ratio) or self._resize(branch["after"], before, after, direction, ratio)

    def settle_client(self):
        """Apply a queued client-side balance after API insertion completes."""
        def balance(node):
            if "block_id" not in node:
                node["split"]["ratio"] = .5
                balance(node["split"]["before"])
                balance(node["split"]["after"])
        for sid in self.pending_client_balance:
            for window in self.data[sid]["windows"]:
                for layer in window["layers"]:
                    balance(layer["layout"])
            self.data[sid]["revision"] += 1
        self.pending_client_balance.clear()

    def call(self, method, **args):
        self.calls.append((method, copy.deepcopy(args)))
        if method == "session.create":
            self.create_count += 1
            sid = f"session-{self.create_count}"
            windows = []
            result = []
            for index, initial in enumerate(args["initial_windows"]):
                layout = self._layout(initial["layout"])
                ids = self._ids(layout)
                windows.append({"window_id": f"window-{sid}-{index}", "label": initial["window_label"],
                                "focused_block_id": ids[0], "layers": [{"kind": "tiled", "layout": layout}]})
                result.append({"block_ids": ids})
            self.data[sid] = {"session_id": sid, "label": args["label"], "revision": 1,
                              "active_window_id": windows[0]["window_id"], "windows": windows}
            self.session_blocks[sid] = {bid for window in result for bid in window['block_ids']}
            if self.timeout_create:
                self.timeout_create = False
                raise Error("Rex timed out after successful creation")
            return {"session_id": sid, "initial_windows": result}
        sid = args["session_id"]
        view = self.data[sid]
        if method == 'session.list_blocks':
            placements = {bid:window['window_id'] for window in view['windows']
                          for layer in window['layers'] if layer.get('layout')
                          for bid in self._ids(layer['layout'])}
            known = self.session_blocks.get(sid,set()) | set(placements)
            return {'blocks':[{**copy.deepcopy(self.surfaces[bid]),
                'placement':'placed' if bid in placements else 'detached',
                **({'window_id':placements[bid]} if bid in placements else {})}
                for bid in sorted(known-self.closed_ids)]}
        if method == "session.focus_block":
            for window in view["windows"]:
                if any(args["block_id"] in self._ids(l["layout"]) for l in window["layers"]):
                    window["focused_block_id"] = args["block_id"]
                    view["active_window_id"] = window["window_id"]
            return {}
        if method == "session.new_split":
            self.split_count += 1
            addition = self._layout(args["layout"])
            self.session_blocks.setdefault(sid,set()).update(self._ids(addition))
            for window in view["windows"]:
                for layer in window["layers"]:
                    layer["layout"] = self._insert(layer["layout"], args["anchor_id"], addition,
                        args["direction"], args["side"], args["ratio"])
            view["revision"] += 1
            if self.timeout_split:
                self.timeout_split = False
                raise Error("Rex timed out after successful split")
            return {"block_ids": self._ids(addition)}
        if method == "session.detach_block":
            for window in view["windows"]:
                for layer in window["layers"]:
                    layer["layout"] = self._remove(layer["layout"], args["block_id"])
            view["revision"] += 1
            return {}
        if method == "session.move_block":
            for window in view["windows"]:
                for layer in window["layers"]:
                    layer["layout"] = self._remove(layer["layout"], args["block_id"])
                    layer["layout"] = self._insert(layer["layout"], args["anchor_block_id"],
                        {"block_id": args["block_id"]}, args["direction"], args["side"], args["ratio"])
            view["revision"] += 1
            if self.balance_on_moves:
                self.pending_client_balance.add(sid)
            return {}
        if method == "session.resize_pane":
            resized = False
            for window in view["windows"]:
                for layer in window["layers"]:
                    resized |= self._resize(layer["layout"], args["before_block_id"],
                        args["after_block_id"], args["direction"], args["ratio"])
            if not resized:
                raise Error("Fake Rex: no matching native split boundary")
            view["revision"] += 1
            return {}
        raise AssertionError(f"Unsupported or destructive Rex method: {method}")

    def close_surface(self, sid, bid):
        self.closed_ids.add(bid)
        for window in self.data[sid]["windows"]:
            for layer in window["layers"]:
                layer["layout"] = self._remove(layer["layout"], bid)
        self.data[sid]["revision"] += 1

    def add_extra(self, sid, anchor):
        self.call("session.new_split", session_id=sid, anchor_id=anchor,
                  direction="vertical", side="after", ratio=.6,
                  layout={"block": {"label": "ordinary user work"}})
        return f"block-{self.counter}"


class ProjectTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="rawr-test-λ ; ")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.store = Store(self.root / "config", self.root / "runtime")
        self.rex = FakeRex()
        self.projects = Projects(self.store, self.rex)

    def create_open(self, tools=()):
        project = self.projects.create("Project", self.root, tools=tools)
        opened = self.projects.open(project["id"])
        return project, opened, self.store.observed(project["id"])

    def config_bytes(self, project):
        return (self.store.config / (project["id"] + ".json")).read_bytes()

    def test_concurrent_open_creates_exactly_one_session(self):
        project = self.projects.create("Project", self.root, tools=("files", "github", "exedev"))
        with ThreadPoolExecutor(max_workers=6) as executor:
            outcomes = list(executor.map(lambda _: self.projects.open(project["id"]), range(10)))
        self.assertEqual(self.rex.create_count, 1)
        self.assertEqual(sum(result["created"] for result in outcomes), 1)
        self.assertEqual({result["session_id"] for result in outcomes}, {"session-1"})
        self.assertEqual(set(self.store.observed(project["id"])["roles"]), {"work-main", "files", "github", "exedev"})

    def test_concurrent_restores_serialize_without_duplicate_programs_or_saved_changes(self):
        project, opened, runtime = self.create_open(("files", "github", "exedev"))
        saved = self.config_bytes(project)
        programs = copy.deepcopy(self.rex.processes)
        with ThreadPoolExecutor(max_workers=2) as executor:
            outcomes = list(executor.map(lambda _: self.projects.restore(project["id"]), range(2)))
        self.assertEqual([result["result"] for result in outcomes], ["complete", "complete"])
        self.assertEqual(self.rex.create_count, 1)
        self.assertEqual(self.rex.split_count, 0)
        self.assertEqual(self.rex.processes, programs)
        self.assertEqual(self.config_bytes(project), saved)
        self.assertEqual(self.store.observed(project["id"])["roles"], runtime["roles"])
        self.assertTrue(self.projects.plan(project["id"])["layout_matches_saved"])

    def test_timeout_after_session_creation_stops_blind_retry(self):
        project = self.projects.create("Project", self.root)
        self.rex.timeout_create = True
        with self.assertRaisesRegex(Error, "timed out"):
            self.projects.open(project["id"])
        with self.assertRaisesRegex(Error, "Ambiguous prior Rex creation"):
            self.projects.open(project["id"])
        self.assertEqual(self.rex.create_count, 1)
        self.assertEqual(len(self.rex.data), 1)
        self.assertEqual(self.store.observed(project["id"])["pending"]["kind"], "session")

    def test_timeout_after_split_is_rediscovered_without_duplicate(self):
        project, opened, runtime = self.create_open()
        self.projects.add_tool(project["id"], "files")
        self.rex.timeout_split = True
        with self.assertRaisesRegex(Error, "timed out"):
            self.projects.tool(project["id"], "files")
        self.assertIn("pending", self.store.observed(project["id"]))
        result = self.projects.tool(project["id"], "files")
        self.assertFalse(result["created"])
        self.assertEqual(self.rex.split_count, 1)
        self.assertNotIn("pending", self.store.observed(project["id"]))
        self.assertEqual(len(self.rex.view(opened["session_id"])["windows"][0]["layers"][0]["blocks"]), 2)

    def test_open_keeps_closed_tool_and_live_arrangement_and_saved_bytes(self):
        project, opened, runtime = self.create_open(("files", "github"))
        before = self.config_bytes(project)
        self.rex.close_surface(opened["session_id"], runtime["roles"]["github"]["block_id"])
        layer = self.rex.data[opened["session_id"]]["windows"][0]["layers"][0]
        layer["layout"]["split"]["ratio"] = .38
        live = copy.deepcopy(layer["layout"])
        result = self.projects.open(project["id"])
        self.assertFalse(result["created"])
        self.assertEqual(layer["layout"], live)
        self.assertEqual(self.config_bytes(project), before)
        self.assertEqual(self.rex.split_count, 0)

    def test_same_names_and_worktrees_have_distinct_stable_identity(self):
        other_root = self.root / "another worktree"
        other_root.mkdir()
        left = self.projects.create("same name", self.root)
        right = self.projects.create("same name", other_root)
        self.assertNotEqual(left["id"], right["id"])
        with self.assertRaisesRegex(Error, "ambiguous"):
            self.projects.open("same name")
        first = self.projects.open(left["id"])
        second = self.projects.open(right["id"])
        self.assertNotEqual(first["session_id"], second["session_id"])
        self.assertEqual(self.store.resolve(right["id"])["root"], str(other_root.resolve()))

    def test_explicit_save_excludes_process_secrets_and_creates_placeholder(self):
        project, opened, runtime = self.create_open(("files",))
        extra = self.rex.add_extra(opened["session_id"], runtime["roles"]["work-main"]["block_id"])
        saved = self.projects.save(project["id"], expected=0)
        self.assertEqual(saved["revision"], 1)
        self.assertTrue(any("shell placeholder" in notice for notice in saved["notices"]))
        data = self.config_bytes(project)
        for sentinel in [b"SECRET-TOKEN-IN-PROCESS", b"SECRET-TRANSCRIPT", b"SECRET-ENV", b"arbitrary_argv", b"transcript", b"environment"]:
            self.assertNotIn(sentinel, data)
        after = self.store.resolve(project["id"])
        self.assertIn("files", after["roles"])
        self.assertTrue(any(b["block_id"] == extra for b in self.store.observed(project["id"])["roles"].values()))

    def test_clean_restore_returns_saved_geometry_and_preserves_programs(self):
        project, opened, runtime = self.create_open(("files", "github", "exedev"))
        self.projects.save(project["id"])
        saved_bytes = self.config_bytes(project)
        programs = copy.deepcopy(self.rex.processes)
        layer = self.rex.data[opened["session_id"]]["windows"][0]["layers"][0]
        layer["layout"]["split"]["ratio"] = .37
        result = self.projects.restore(project["id"])
        self.assertEqual(result["result"], "complete", result)
        self.assertEqual(layer["layout"]["split"]["ratio"], .20)
        self.assertEqual(self.rex.processes, programs)
        self.assertEqual(self.config_bytes(project), saved_bytes)
        self.assertEqual(set(self.store.observed(project["id"])["roles"]), set(runtime["roles"]))

    def test_restore_reapplies_saved_ratios_after_asynchronous_client_balance(self):
        project, opened, runtime = self.create_open(("files", "github", "exedev"))
        self.projects.save(project["id"])
        before = self.config_bytes(project)
        programs = copy.deepcopy(self.rex.processes)
        self.rex.balance_on_moves = True
        sleeps = []

        def settle_after_wait(duration):
            sleeps.append(duration)
            self.rex.settle_client()

        with patch("rawr.project.time.sleep", side_effect=settle_after_wait):
            outcome = self.projects.restore(project["id"])
        self.assertEqual(outcome["result"], "complete", outcome)
        self.assertTrue(self.projects.plan(project["id"])["layout_matches_saved"])
        self.assertGreaterEqual(len(sleeps), 2)
        self.assertEqual(self.rex.processes, programs)
        self.assertEqual(self.config_bytes(project), before)
        self.assertNotIn("layout_pending", self.store.observed(project["id"]))

    def test_plan_reports_geometry_difference_without_mutation(self):
        project, opened, _ = self.create_open(("files", "github", "exedev"))
        self.assertTrue(self.projects.plan(project["id"])["layout_matches_saved"])
        saved = self.config_bytes(project)
        self.rex.data[opened["session_id"]]["windows"][0]["layers"][0]["layout"]["split"]["ratio"] = .38
        mutations = len([m for m, _ in self.rex.calls if m.startswith("session.")])
        self.assertFalse(self.projects.plan(project["id"])["layout_matches_saved"])
        self.assertEqual(self.config_bytes(project), saved)
        self.assertEqual(len([m for m, _ in self.rex.calls if m.startswith("session.")]), mutations)

    def test_changed_runtime_location_cannot_duplicate_existing_project_session(self):
        project, opened, runtime = self.create_open(("files",))
        saved = self.config_bytes(project)
        alternate = Projects(Store(self.store.config, self.root / "other-runtime"), self.rex)
        before = len(self.rex.calls)
        with self.assertRaisesRegex(Error, "config uses runtime state at"):
            alternate.open(project["id"])
        self.assertEqual(len(self.rex.calls), before)
        self.assertEqual(self.rex.create_count, 1)
        self.assertEqual(self.store.observed(project["id"])["session_id"], opened["session_id"])
        self.assertEqual(self.config_bytes(project), saved)
        self.assertFalse(self.projects.open(project["id"])["created"])

    def test_extra_running_pane_survives_partial_restore_without_layout_mutation(self):
        project, opened, runtime = self.create_open(("files",))
        self.projects.save(project["id"])
        before = self.config_bytes(project)
        extra = self.rex.add_extra(opened["session_id"], runtime["roles"]["work-main"]["block_id"])
        geometry = copy.deepcopy(self.rex.data[opened["session_id"]]["windows"])
        processes = copy.deepcopy(self.rex.processes)
        outcome = self.projects.restore(project["id"])
        self.assertEqual(outcome["result"], "partial")
        self.assertIn(extra, outcome["extra"])
        self.assertEqual(self.rex.data[opened["session_id"]]["windows"], geometry)
        self.assertEqual(self.rex.processes, processes)
        self.assertEqual(self.config_bytes(project), before)

    def test_occupied_tool_is_preserved_and_explicit_restore_creates_new_surface(self):
        project, opened, runtime = self.create_open(("files",))
        previous = runtime["roles"]["files"]["block_id"]
        self.rex.processes[previous]["child"] = {"pid": 90000, "path": "/bin/zsh", "name": "zsh"}
        self.rex.processes[previous]["foreground"] = {"pid": 90001, "path": "/bin/sleep", "name": "sleep"}
        preserved = copy.deepcopy(self.rex.processes[previous])
        before = self.config_bytes(project)
        result = self.projects.restore(project["id"])
        self.assertEqual(result["result"], "partial")
        current = self.store.observed(project["id"])["roles"]["files"]["block_id"]
        self.assertNotEqual(current, previous)
        self.assertEqual(self.rex.processes[previous], preserved)
        self.assertIn(previous, result["extra"])
        self.assertEqual(self.config_bytes(project), before)
        self.assertEqual(self.rex.split_count, 1)
        again = self.projects.restore(project["id"])
        self.assertEqual(again["result"], "partial")
        self.assertEqual(self.rex.split_count, 1)

    def test_restart_surface_generation_is_not_misreported_as_restored_tool(self):
        project, opened, runtime = self.create_open(("files",))
        self.rex.generation = "generation-2"
        previous = runtime["roles"]["files"]["block_id"]
        self.rex.processes[previous]["child"]["pid"] += 500
        process_before = copy.deepcopy(self.rex.processes[previous])
        plan = self.projects.plan(project["id"])
        self.assertEqual(plan["result"], "partial")
        self.assertIn("fresh shell", plan["issues"]["files"])
        result = self.projects.tool(project["id"], "files")
        self.assertEqual(result["result"], "partial")
        self.assertNotEqual(result["block_id"], previous)
        self.assertEqual(self.rex.processes[previous], process_before)

    def test_changed_host_or_reused_session_label_is_not_hijacked(self):
        project, opened, runtime = self.create_open()
        baseline = copy.deepcopy(self.rex.data)
        self.rex.host = "different-host"
        with self.assertRaisesRegex(Error, "host identity changed"):
            self.projects.open(project["id"])
        self.assertEqual(self.rex.data, baseline)
        self.rex.host = "test-host"
        self.rex.data[opened["session_id"]]["label"] = "unrelated reused ID"
        with self.assertRaisesRegex(Error, "association changed"):
            self.projects.open(project["id"])
        self.assertEqual(self.rex.create_count, 1)

    def test_corrupt_and_future_configs_fail_before_rex_inspection_or_mutation(self):
        project = self.projects.create("Project", self.root)
        file = self.store.config / (project["id"] + ".json")
        valid = file.read_text()
        for malformed in ["truncated{", valid.replace('"schema": 1', '"schema": 99')]:
            file.write_text(malformed)
            before = len(self.rex.calls)
            for action in (self.projects.open, self.projects.restore, self.projects.plan):
                with self.assertRaises(Error):
                    action(project["id"])
            self.assertEqual(len(self.rex.calls), before)

    def test_status_plan_does_not_change_saved_or_observed_bytes_or_focus(self):
        project, opened, runtime = self.create_open(("files",))
        saved = self.config_bytes(project)
        observed = (self.store.runtime / (project["id"] + ".json")).read_bytes()
        mutations = len([m for m, _ in self.rex.calls if m.startswith("session.") or m == "select"])
        self.projects.plan(project["id"])
        self.assertEqual(self.config_bytes(project), saved)
        self.assertEqual((self.store.runtime / (project["id"] + ".json")).read_bytes(), observed)
        self.assertEqual(len([m for m, _ in self.rex.calls if m.startswith("session.") or m == "select"]), mutations)

    def test_layout_revision_change_during_save_retains_previous_setup(self):
        project, opened, _ = self.create_open(("files",))
        saved = self.config_bytes(project)
        calls = 0

        def native_change(sid):
            nonlocal calls
            calls += 1
            if calls == 2:
                self.rex.data[sid]["revision"] += 1

        self.rex.before_view = native_change
        with self.assertRaisesRegex(Error, "layout changed during save"):
            self.projects.save(project["id"])
        self.assertEqual(self.config_bytes(project), saved)

    def test_stale_explicit_save_fails_before_rex_mutation(self):
        project, _, _ = self.create_open()
        self.projects.save(project["id"], expected=0)
        saved = self.config_bytes(project)
        before = len(self.rex.calls)
        with self.assertRaisesRegex(Error, "Stale save revision"):
            self.projects.save(project["id"], expected=0)
        self.assertEqual(self.config_bytes(project), saved)
        self.assertEqual(len(self.rex.calls), before)

    def test_partial_launch_failure_preserves_existing_work_and_saved_setup(self):
        project, opened, runtime = self.create_open(("files",))
        self.rex.close_surface(opened["session_id"], runtime["roles"]["files"]["block_id"])
        saved = self.config_bytes(project)
        processes = copy.deepcopy(self.rex.processes)
        with patch("rawr.project.shutil.which", return_value=None):
            outcome = self.projects.restore(project["id"])
        self.assertEqual(outcome["result"], "partial")
        self.assertIn("executable unavailable", outcome["conflicts"][0])
        self.assertEqual(self.config_bytes(project), saved)
        self.assertEqual(self.rex.processes, processes)
        self.assertEqual(self.rex.split_count, 0)
        self.assertNotIn("pending", self.store.observed(project["id"]))

    def test_save_without_a_working_role_is_rejected_before_config_replacement(self):
        project, opened, runtime = self.create_open(("files",))
        saved = self.config_bytes(project)
        self.rex.close_surface(opened["session_id"], runtime["roles"]["work-main"]["block_id"])
        with self.assertRaisesRegex(Error, "work-main"):
            self.projects.save(project["id"])
        self.assertEqual(self.config_bytes(project), saved)

    def test_incomplete_launch_verification_refuses_duplicate_surface(self):
        project, opened, runtime = self.create_open(("files",))
        runtime["roles"]["files"].pop("pid")
        self.store.record(project["id"], runtime)
        saved = self.config_bytes(project)
        programs = copy.deepcopy(self.rex.processes)
        geometry = copy.deepcopy(self.rex.data)
        with self.assertRaisesRegex(Error, "launch verification incomplete"):
            self.projects.tool(project["id"], "files")
        plan = self.projects.plan(project["id"])
        self.assertEqual(plan["result"], "failed")
        self.assertEqual(plan["actions"], [])
        outcome = self.projects.restore(project["id"])
        self.assertEqual(outcome["result"], "failed")
        self.assertEqual(self.rex.create_count, 1)
        self.assertEqual(self.rex.split_count, 0)
        self.assertEqual(self.rex.processes, programs)
        self.assertEqual(self.rex.data, geometry)
        self.assertEqual(self.config_bytes(project), saved)

    def test_interrupted_detach_or_move_journal_blocks_automatic_retry(self):
        for method in ("session.detach_block", "session.move_block"):
            with self.subTest(method=method):
                project, opened, runtime = self.create_open(("files", "github", "exedev"))
                saved = self.config_bytes(project)
                programs = copy.deepcopy(self.rex.processes)
                native_call = self.rex.call
                failed = False

                def interrupted(name, **args):
                    nonlocal failed
                    response = native_call(name, **args)
                    if name == method and not failed:
                        failed = True
                        raise Error("simulated timeout after native layout mutation")
                    return response

                with patch.object(self.rex, "call", side_effect=interrupted):
                    outcome = self.projects.restore(project["id"])
                self.assertEqual(outcome["result"], "partial")
                self.assertIn("timeout", outcome["conflicts"][0])
                observed = self.store.observed(project["id"])
                self.assertEqual(set(observed["layout_pending"]["blocks"]), {v["block_id"] for v in runtime["roles"].values()})
                self.assertEqual(self.projects.plan(project["id"])["result"], "failed")
                mutations = len([m for m, _ in self.rex.calls if m.startswith("session.")])
                for action in (self.projects.restore, self.projects.open):
                    with self.assertRaisesRegex(Error, "Interrupted layout operation"):
                        action(project["id"])
                with self.assertRaisesRegex(Error, "Interrupted layout operation"):
                    self.projects.tool(project["id"], "files")
                self.assertEqual(len([m for m, _ in self.rex.calls if m.startswith("session.")]), mutations)
                self.assertEqual(self.rex.processes, programs)
                self.assertEqual(self.config_bytes(project), saved)

    def test_malformed_creation_response_preserves_pending_and_refuses_retry(self):
        malformed = [
            {},
            {"session_id": "session-1", "initial_windows": []},
            {"session_id": "session-1", "initial_windows": None},
            {"session_id": "session-1", "initial_windows": [{"block_ids": None}]},
            {"session_id": "session-1", "initial_windows": [{"block_ids": [{}]}]},
            {"session_id": "session-1", "initial_windows": [{"block_ids": [123]}]},
        ]
        for index, response in enumerate(malformed):
            with self.subTest(response=response):
                # Use separate stores/backends so every faulty creation really
                # succeeds once in Rex before its response becomes unusable.
                store = Store(self.root / f"config-malformed-{index}", self.root / f"runtime-malformed-{index}")
                rex = FakeRex()
                projects = Projects(store, rex)
                project = projects.create("Malformed response", self.root)
                native_call = rex.call

                def malformed_call(method, **args):
                    result = native_call(method, **args)
                    return response if method == "session.create" else result

                with patch.object(rex, "call", side_effect=malformed_call):
                    with self.assertRaisesRegex(Error, "Malformed.*creation"):
                        projects.open(project["id"])
                self.assertIn("pending", store.observed(project["id"]))
                with self.assertRaisesRegex(Error, "Ambiguous prior Rex creation"):
                    projects.open(project["id"])
                self.assertEqual(rex.create_count, 1)
                self.assertEqual(len(rex.data), 1)

    def test_malformed_split_response_retains_token_for_safe_rediscovery(self):
        for index, response in enumerate([{}, {"block_ids": None}, {"block_ids": [{}]}, {"block_ids": [123]}]):
            with self.subTest(response=response):
                store = Store(self.root / f"split-config-{index}", self.root / f"split-runtime-{index}")
                rex = FakeRex()
                projects = Projects(store, rex)
                project = projects.create("Malformed split", self.root)
                projects.open(project["id"])
                projects.add_tool(project["id"], "files")
                native_call = rex.call

                def malformed_call(method, **args):
                    result = native_call(method, **args)
                    return response if method == "session.new_split" else result

                with patch.object(rex, "call", side_effect=malformed_call):
                    with self.assertRaisesRegex(Error, "Malformed.*split"):
                        projects.tool(project["id"], "files")
                self.assertIn("pending", store.observed(project["id"]))
                recovered = projects.tool(project["id"], "files")
                self.assertFalse(recovered["created"])
                self.assertNotIn("pending", store.observed(project["id"]))
                self.assertEqual(rex.split_count, 1)


if __name__ == "__main__":
    unittest.main()
