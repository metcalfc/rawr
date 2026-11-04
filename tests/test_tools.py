import os
import curses
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from rawr.tools import Tree, GitHubView, VMView, safe_text, _vm_loop, _vm_mouse_row


class TreeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        (self.root / "folder").mkdir()
        (self.root / "folder" / "nested.txt").write_text("nested")
        (self.root / "a.txt").write_text("hello\n\x1b[31munsafe")
        self.tree = Tree(self.root)

    def test_expand_refresh_and_anchor(self):
        self.assertEqual([r.name for r in self.tree.rows()], ["folder", "a.txt"])
        self.tree.toggle(self.root / "folder")
        self.assertEqual([r.name for r in self.tree.rows()], ["folder", "nested.txt", "a.txt"])
        other = Path(tempfile.gettempdir())
        prior = Path.cwd()
        try:
            os.chdir(other)
            self.assertEqual(self.tree.root, self.root)
            self.assertEqual(self.tree.preview("folder/nested.txt"), ["nested"])
        finally:
            os.chdir(prior)
        (self.root / "z.txt").touch()
        self.assertNotIn("z.txt", [r.name for r in self.tree.rows()])
        self.tree.refresh()
        self.assertIn("z.txt", [r.name for r in self.tree.rows()])

    def test_preview_is_inert_and_bounded(self):
        self.assertEqual(self.tree.preview("a.txt"), ["hello", "�[31munsafe"])
        (self.root / "large").write_text("x" * 100)
        self.assertIn("truncated", self.tree.preview("large", limit=20)[-1])
        (self.root / "binary").write_bytes(b"a\0b")
        self.assertIn("Binary file", self.tree.preview("binary")[0])

    def test_no_traversal_or_symlink_cycles(self):
        (self.root / "folder" / "loop").symlink_to(self.root, target_is_directory=True)
        self.tree.toggle(self.root / "folder")
        self.tree.toggle(self.root / "folder" / "loop")
        self.assertEqual(len(self.tree.rows()), 4)
        (self.root / "outside").symlink_to("/etc/hosts")
        with self.assertRaisesRegex(ValueError, "outside"):
            self.tree.preview("outside")
        with self.assertRaisesRegex(ValueError, "outside"):
            self.tree.preview("../")

    @patch("rawr.tools.subprocess.run")
    def test_open_literal_path(self, run):
        name = "space α ; $(touch bad) ' file.txt"
        path = self.root / name
        path.write_text("hello")
        run.return_value.returncode = 0
        self.tree.open_file(path)
        self.assertEqual(run.call_args.args[0][-1], str(path))
        self.assertNotIn("shell", run.call_args.kwargs)

    def test_terminal_safety(self):
        self.assertEqual(safe_text("a\x1b]0;owned\a\nb"), "a�]0;owned��b")
        self.assertNotIn("\u202e", safe_text("x\u202eevil"))
        self.assertEqual(safe_text("é α 文件"), "é α 文件")


class GitHubViewTests(unittest.TestCase):
    @patch("rawr.tools.services.open_url")
    @patch("rawr.tools.services.github_status")
    def test_context_and_open_selected_pr(self, status, opener):
        status.return_value = {"state": "ready", "prs": [
            {"number": 1, "url": "https://github.com/a/b/pull/1"},
            {"number": 2, "url": "https://github.com/a/b/pull/2"}]}
        model = GitHubView({"root": "/project A", "repository": "a/b"})
        model.refresh()
        model.selected = 1
        model.open_pr()
        status.assert_called_once_with("/project A", "a/b")
        opener.assert_called_once_with("https://github.com/a/b/pull/2")
        status.return_value = {"state": "no-pr", "prs": []}
        model.refresh()
        with self.assertRaisesRegex(ValueError, "no pull request"):
            model.open_pr()
        self.assertEqual(opener.call_count, 1)


class VMViewTests(unittest.TestCase):
    @patch("rawr.tools.services.exe_list")
    def test_activate_selected_switches_host_without_separate_link_or_shell(self, listing):
        listing.return_value = [{"vm_name": "one"}, {"vm_name": "two"}]
        switches, links, shells = [], [], []
        project = {"root": "/A", "vm": "one"}
        model = VMView(project, {"switch_vm": switches.append, "link_vm": links.append,
                                 "vm_shell": shells.append})
        model.refresh()
        model.selected = 1
        self.assertEqual(switches, [])
        self.assertEqual(model.project["vm"], "one")
        model.activate_selected()
        self.assertEqual(switches, [{"vm_name": "two"}])
        self.assertEqual(model.project["vm"], "two")
        self.assertEqual(project["vm"], "one")
        self.assertEqual(links, [])
        self.assertEqual(shells, [])

    @patch("rawr.tools.services.exe_list")
    def test_failed_switch_preserves_displayed_link(self, listing):
        listing.return_value = [{"vm_name": "one"}, {"vm_name": "two"}]
        def fail(vm):
            raise RuntimeError("old shell could not be archived")
        model = VMView({"vm": "one"}, {"switch_vm": fail})
        model.refresh()
        model.selected = 1
        with self.assertRaisesRegex(RuntimeError, "archived"):
            model.activate_selected()
        self.assertEqual(model.project["vm"], "one")
        model.callbacks["switch_vm"] = lambda vm: {"result": "partial", "conflicts": ["layout changed"]}
        with self.assertRaisesRegex(RuntimeError, "layout changed"):
            model.activate_selected()
        self.assertEqual(model.project["vm"], "one")

    def test_empty_or_unavailable_switch_is_actionable(self):
        model = VMView({"vm": "one"}, {})
        with self.assertRaisesRegex(ValueError, "No existing VM"):
            model.activate_selected()
        model.vms = [{"vm_name": "one"}]
        with self.assertRaisesRegex(ValueError, "switching is unavailable"):
            model.activate_selected()

    @patch("rawr.tools.services.exe_list")
    def test_refresh_retains_cursor_and_starts_at_linked_host(self, listing):
        listing.return_value = [{"vm_name": "one"}, {"vm_name": "two"}]
        switches = []
        model = VMView({"vm": "two"}, {"switch_vm": switches.append})
        model.refresh()
        self.assertEqual(model.selected, 1)
        listing.return_value = [{"vm_name": "two"}, {"vm_name": "one"}]
        model.refresh()
        self.assertEqual(model.selected, 0)
        self.assertEqual(switches, [])

    @patch("rawr.tools.services.exe_list")
    def test_explicit_link_actions_target_linked_vm(self, listing):
        listing.return_value = [{"vm_name": "one"}, {"vm_name": "two"}]
        links, shells = [], []
        project = {"root": "/A", "vm": "one"}
        model = VMView(project, {"link_vm": links.append, "vm_shell": shells.append})
        model.refresh()
        model.selected = 1
        model.shell()
        self.assertEqual(shells, [{"vm_name": "one"}])
        self.assertEqual(links, [])
        model.link_selected()
        model.shell()
        self.assertEqual(links, ["two"])
        self.assertEqual(shells[-1], {"vm_name": "two"})
        self.assertEqual(project["vm"], "one")

    @patch("rawr.tools.services.exe_list")
    def test_unavailable_vm_preserves_link_and_no_shell(self, listing):
        listing.return_value = []
        shells = []
        model = VMView({"vm": "saved"}, {"vm_shell": shells.append})
        model.refresh()
        with self.assertRaisesRegex(ValueError, "unavailable"):
            model.shell()
        self.assertEqual(model.project["vm"], "saved")
        self.assertEqual(shells, [])

    @patch("rawr.tools.services.exe_list")
    def test_link_failure_keeps_original(self, listing):
        listing.return_value = [{"vm_name": "other"}]
        def fail(name):
            raise RuntimeError("conflicting setup write")
        model = VMView({"vm": "saved"}, {"link_vm": fail})
        model.refresh()
        with self.assertRaisesRegex(RuntimeError, "conflicting"):
            model.link_selected()
        self.assertEqual(model.project["vm"], "saved")


class FakeVMScreen:
    def __init__(self, keys, height=12, width=100):
        self.keys = iter(keys)
        self.height, self.width = height, width
        self.drawn = {}

    def erase(self):
        self.drawn.clear()

    def getmaxyx(self):
        return self.height, self.width

    def addnstr(self, row, col, text, length, attr=0):
        self.drawn[row] = text[:length]

    def refresh(self):
        pass

    def getch(self):
        return next(self.keys)


class VMLoopTests(unittest.TestCase):
    @patch("rawr.tools.curses.getmouse")
    def test_mouse_targets_only_visible_vm_rows_with_scroll_offset(self, mouse):
        screen = FakeVMScreen([])
        mouse.return_value = (0, 5, 4, 0, curses.BUTTON1_RELEASED)
        self.assertEqual(_vm_mouse_row(screen, 6, 3), (7, 5, 4))
        for x, y, button in [(5, 2, curses.BUTTON1_CLICKED), (5, 6, curses.BUTTON1_CLICKED),
                             (5, 10, curses.BUTTON1_CLICKED), (-1, 4, curses.BUTTON1_CLICKED),
                             (100, 4, curses.BUTTON1_CLICKED), (5, 4, curses.BUTTON3_CLICKED)]:
            mouse.return_value = (0, x, y, 0, button)
            self.assertIsNone(_vm_mouse_row(screen, 6, 3))

    @patch("rawr.tools.curses.mousemask", return_value=(1, 17))
    @patch("rawr.tools.services.exe_list")
    def test_keyboard_browsing_is_read_only_until_enter(self, listing, mask):
        listing.return_value = [{"vm_name": "one"}, {"vm_name": "two"}]
        switches = []
        screen = FakeVMScreen([curses.KEY_DOWN, curses.KEY_UP, curses.KEY_DOWN, 10, ord("q")])
        _vm_loop(screen, {"name": "project", "vm": "one"}, {"switch_vm": switches.append})
        self.assertEqual(switches, [{"vm_name": "two"}])
        self.assertEqual(mask.call_args_list[-1].args, (17,))

    @patch("rawr.tools.time.monotonic", side_effect=[10, 10.05])
    @patch("rawr.tools.curses.getmouse")
    @patch("rawr.tools.curses.mousemask", return_value=(1, 17))
    @patch("rawr.tools.services.exe_list")
    def test_mouse_release_click_pair_switches_once_even_when_viewport_moves(self, listing, mask, mouse, clock):
        listing.return_value = [{"vm_name": f"host-{i}"} for i in range(12)]
        switches = []
        mouse.side_effect = [(0, 5, 3, 0, curses.BUTTON1_RELEASED),
                             (0, 5, 3, 0, curses.BUTTON1_CLICKED)]
        screen = FakeVMScreen([curses.KEY_DOWN] * 8 + [curses.KEY_MOUSE, curses.KEY_MOUSE, ord("q")], height=10)
        _vm_loop(screen, {"name": "project", "vm": "host-0"}, {"switch_vm": switches.append})
        self.assertEqual(switches, [{"vm_name": "host-5"}])
        self.assertEqual(mask.call_args_list[-1].args, (17,))

    @patch("rawr.tools.curses.mousemask", side_effect=curses.error("no mouse"))
    @patch("rawr.tools.services.exe_list")
    def test_keyboard_still_works_without_terminal_mouse_support(self, listing, mask):
        listing.return_value = [{"vm_name": "one"}]
        switches = []
        _vm_loop(FakeVMScreen([ord("s"), ord("q")]), {"name": "project", "vm": "one"},
                 {"switch_vm": switches.append})
        self.assertEqual(switches, [{"vm_name": "one"}])


if __name__ == "__main__":
    unittest.main()
