import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from rawr.project import Projects
from rawr.state import Error, Store, atomic


class StateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="rawr-state-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.store = Store(self.root / "config", self.root / "runtime")
        # Creating a project is intentionally independent of any Rex backend.
        self.projects = Projects(self.store, None)
        self.project = self.projects.create("sample", self.root)
        self.file = self.store.config / (self.project["id"] + ".json")

    def test_stale_writer_cannot_discard_newer_revision(self):
        first = copy.deepcopy(self.project)
        stale = copy.deepcopy(self.project)
        first.update(revision=1, vm="persistent-vm")
        stale.update(revision=1, vm="wrong-vm")
        with self.store.lock(first["id"]):
            self.store.write(first, expected=0)
        before = self.file.read_bytes()
        with self.store.lock(stale["id"]):
            with self.assertRaisesRegex(Error, "Stale save"):
                self.store.write(stale, expected=0)
        self.assertEqual(self.file.read_bytes(), before)
        self.assertEqual(self.store.resolve(first["id"])["vm"], "persistent-vm")
        self.assertEqual(json.loads(self.file.with_suffix(".previous").read_text())["revision"], 0)

    def test_failed_fsync_before_replace_preserves_old_valid_json_and_cleans_temporary(self):
        before = self.file.read_bytes()
        with patch("rawr.state.os.fsync", side_effect=OSError("simulated interrupted flush")):
            with self.assertRaises(OSError):
                atomic(self.file, {"new": "candidate"})
        self.assertEqual(self.file.read_bytes(), before)
        self.assertEqual(json.loads(self.file.read_text())["revision"], 0)
        self.assertEqual(list(self.file.parent.glob("." + self.file.name + "*")), [])

    def test_failed_directory_fsync_after_replace_leaves_complete_new_json(self):
        original = {"revision": 1, "marker": "new complete content"}
        count = 0

        def fsync(_fd):
            nonlocal count
            count += 1
            if count == 2:
                raise OSError("simulated crash after replacement")

        with patch("rawr.state.os.fsync", side_effect=fsync):
            with self.assertRaises(OSError):
                atomic(self.file, original)
        self.assertEqual(json.loads(self.file.read_text()), original)
        self.assertEqual(list(self.file.parent.glob("." + self.file.name + "*")), [])

    def test_failed_config_replace_retains_previous_setup_and_backup(self):
        import os
        replace = os.replace
        old = self.file.read_bytes()
        candidate = copy.deepcopy(self.project)
        candidate["revision"] = 1

        def interrupted(source, destination):
            if Path(destination) == self.file:
                raise OSError("simulated replacement failure")
            return replace(source, destination)

        with self.store.lock(candidate["id"]), patch("rawr.state.os.replace", side_effect=interrupted):
            with self.assertRaises(OSError):
                self.store.write(candidate, expected=0)
        self.assertEqual(self.file.read_bytes(), old)
        self.assertEqual(json.loads(self.file.with_suffix(".previous").read_text()), self.project)
        self.assertEqual(list(self.file.parent.glob("." + self.file.name + "*")), [])

    def test_invalid_candidate_never_replaces_valid_state(self):
        before = self.file.read_bytes()
        candidate = copy.deepcopy(self.project)
        candidate["schema"] = 100
        with self.assertRaisesRegex(Error, "Unsupported project schema"):
            self.store.write(candidate, expected=0)
        self.assertEqual(self.file.read_bytes(), before)

    def test_runtime_and_deliberate_configuration_remain_separate(self):
        before = self.file.read_bytes()
        self.store.record(self.project["id"], {
            "session_id": "disposable-session", "instance_id": "one",
            "roles": {"work-main": {"block_id": "current-pane", "label": "owned-working-pane"}},
        })
        self.assertEqual(self.file.read_bytes(), before)
        self.assertEqual(self.store.observed(self.project["id"])["roles"]["work-main"]["block_id"], "current-pane")
        self.assertNotIn("session_id", self.file.read_text())

    def test_future_and_truncated_runtime_mapping_have_actionable_error(self):
        runtime_file = self.store.runtime / (self.project["id"] + ".json")
        self.store.runtime.mkdir(parents=True)
        for value in ["{truncated", "[]", "null", "42", '"scalar"', json.dumps({"schema": 2, "roles": {}}),
                      json.dumps({"schema": 1, "roles": {"work-main": "invalid"}}),
                      json.dumps({"schema": 1, "roles": {}, "pending": []}),
                      json.dumps({"schema": 1, "roles": {}, "pending": {"kind": "unknown"}}),
                      json.dumps({"schema": 1, "roles": {}, "pending": {"kind": "split"}}),
                      json.dumps({"schema": 1, "roles": {}, "pending": {"kind": "split", "role": "files", "label": 123}})]:
            with self.subTest(value=value):
                runtime_file.write_text(value)
                with self.assertRaises(Error):
                    self.store.observed(self.project["id"])


if __name__ == "__main__":
    unittest.main()
