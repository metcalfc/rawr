"""Small project-anchored terminal views hosted by native Rex panes.

These views never inspect neighboring terminals or arrange panes. Rex Projects
supplies their root/repository/VM context and handles explicit native actions.
"""
from __future__ import annotations

import curses
from dataclasses import dataclass
from pathlib import Path
import subprocess
import sys
import time
import unicodedata

from . import services


def safe_text(value: object) -> str:
    """Make service output and filenames inert in the terminal, including ESC."""
    return "".join(c if c.isprintable() and not unicodedata.category(c).startswith("C") else "�"
                   for c in str(value))


@dataclass(frozen=True)
class Row:
    path: Path
    name: str
    depth: int
    is_dir: bool
    symlink: bool
    expanded: bool


class Tree:
    def __init__(self, root: str | Path):
        self._root = Path(root).resolve(strict=True)
        if not self._root.is_dir():
            raise ValueError(f"Project root is not a directory: {self._root}")
        self.expanded: set[Path] = set()
        self._cache: dict[Path, list[Path]] = {}
        self.errors: list[str] = []

    @property
    def root(self) -> Path:
        return self._root

    def _inside(self, path: str | Path) -> Path:
        path = Path(path)
        if not path.is_absolute():
            path = self.root / path
        actual = path.resolve(strict=True)
        if not actual.is_relative_to(self.root):
            raise ValueError("Selection points outside the project root")
        return path.parent.resolve(strict=True) / path.name if path.is_symlink() else actual

    def refresh(self) -> None:
        self._cache.clear()
        self.errors.clear()

    def toggle(self, path: str | Path) -> None:
        path = self._inside(path)
        # Do not follow directory symlinks: this also prevents recursive cycles.
        if path.is_symlink() or not path.is_dir():
            return
        if path in self.expanded:
            self.expanded.remove(path)
        else:
            self.expanded.add(path)

    def rows(self) -> list[Row]:
        rows: list[Row] = []
        self.errors.clear()

        def walk(directory: Path, depth: int) -> None:
            if directory not in self._cache:
                try:
                    self._cache[directory] = sorted(directory.iterdir(), key=lambda p: (not p.is_dir(), p.name.casefold(), p.name))
                except OSError as error:
                    self.errors.append(f"Cannot read {directory}: {error.strerror}")
                    return
            for path in self._cache[directory]:
                is_dir = path.is_dir()
                expanded = path in self.expanded and not path.is_symlink()
                rows.append(Row(path, path.name, depth, is_dir, path.is_symlink(), expanded))
                if expanded:
                    walk(path, depth + 1)
        walk(self.root, 0)
        return rows

    def preview(self, path: str | Path, limit: int = 131072) -> list[str]:
        path = self._inside(path)
        if not path.is_file():
            raise ValueError("Select a regular file to preview")
        with path.open("rb") as source:
            data = source.read(limit + 1)
        if b"\0" in data:
            return ["Binary file — use o to open it with the system application."]
        lines = data[:limit].decode("utf-8", errors="replace").splitlines()
        result = [safe_text(line.expandtabs(4)) for line in lines[:2000]]
        if len(data) > limit or len(lines) > 2000:
            result.append("… Preview truncated; use o to open the complete file.")
        return result or ["(empty file)"]

    def open_file(self, path: str | Path) -> None:
        path = self._inside(path)
        if not path.is_file():
            raise ValueError("Select a regular file to open")
        argv = ["open", "--", str(path)] if sys.platform == "darwin" else ["xdg-open", str(path)]
        result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                stderr=subprocess.PIPE, text=True, timeout=10)
        if result.returncode:
            raise RuntimeError(result.stderr.strip() or "The system file opener failed")


class GitHubView:
    def __init__(self, project: dict):
        self.root = project["root"]
        self.repository = project.get("repository")
        self.status: dict = {}
        self.selected = 0

    def refresh(self) -> dict:
        self.status = services.github_status(self.root, self.repository)
        self.selected = min(self.selected, max(0, len(self.status.get("prs", [])) - 1))
        return self.status

    def open_pr(self) -> None:
        prs = self.status.get("prs", [])
        if not prs:
            raise ValueError("There is no pull request to open")
        services.open_url(prs[self.selected]["url"])


class VMView:
    def __init__(self, project: dict, callbacks: dict):
        self.project = dict(project)
        self.callbacks = callbacks
        self.vms: list[dict] = []
        self.selected = 0

    def refresh(self) -> list[dict]:
        previous = self.vms[self.selected].get("vm_name") if self.vms else self.project.get("vm")
        self.vms = services.exe_list()
        self.selected = next((i for i, vm in enumerate(self.vms) if vm.get("vm_name") == previous), 0)
        return self.vms

    def linked(self) -> dict:
        name = self.project.get("vm")
        if not name:
            raise ValueError("No VM linked. Select an existing VM and press l to link it.")
        vm = next((v for v in self.vms if v.get("vm_name") == name), None)
        if vm is None:
            raise ValueError(f"Linked VM {name} is unavailable. Refresh or select another existing VM.")
        return vm

    def link_selected(self) -> None:
        if not self.vms:
            raise ValueError("No existing VM is available to link")
        name = self.vms[self.selected]["vm_name"]
        callback = self.callbacks.get("link_vm")
        if callback is None:
            raise ValueError("VM linking is unavailable in this invocation")
        callback(name)
        self.project["vm"] = name

    def activate_selected(self) -> None:
        """Use the chosen host in the project's native shell slot."""
        if not self.vms:
            raise ValueError("No existing VM is available to open")
        callback = self.callbacks.get("switch_vm")
        if callback is None:
            raise ValueError("Native VM switching is unavailable in this invocation")
        vm = self.vms[self.selected]
        result = callback(vm)
        if isinstance(result, dict) and result.get("result") in ("partial", "failed"):
            reason = "; ".join(result.get("conflicts", [])) or "The VM switch could not be completed"
            raise RuntimeError(reason)
        self.project["vm"] = vm["vm_name"]

    def shell(self) -> None:
        callback = self.callbacks.get("vm_shell")
        if callback is None:
            raise ValueError("Native VM shell action is unavailable in this invocation")
        callback(self.linked())

    def browser(self) -> None:
        services.open_url(services.exe_browser_url(self.linked()))


def _write(screen, y: int, x: int, text: object, attr: int = 0) -> None:
    height, width = screen.getmaxyx()
    if 0 <= y < height and 0 <= x < width - 1:
        try:
            screen.addnstr(y, x, safe_text(text), max(0, width - x - 1), attr)
        except curses.error:
            pass  # A resize or wide glyph at the right edge may consume the cell.


def _frame(screen, title: str, subtitle: str, hints: str, message: str) -> tuple[int, int]:
    screen.erase()
    height, width = screen.getmaxyx()
    _write(screen, 0, 0, title, curses.A_BOLD)
    _write(screen, 1, 0, subtitle)
    _write(screen, max(2, height - 2), 0, message, curses.A_BOLD)
    if width < 24:
        hints = 'Widen pane ≥24 cols'
    elif height < 8:
        hints = 'Grow pane to 8 rows'
    _write(screen, max(3, height - 1), 0, hints)
    return max(0, height - 5), width


def _tree_loop(screen, project: dict) -> None:
    model = Tree(project["root"])
    selected = 0
    preview: list[str] | None = None
    preview_path: Path | None = None
    scroll = 0
    message = ""
    while True:
        rows = model.rows()
        selected = min(selected, max(0, len(rows) - 1))
        hints = "↑↓ select  Enter expand/preview  o open  r refresh  q quit"
        if preview is not None:
            hints = "↑↓ scroll  o open  q/Esc back"
        visible, _ = _frame(screen, f"Files · {project['name']}", str(model.root), hints,
                            message or (model.errors[0] if model.errors else ""))
        if preview is not None:
            _write(screen, 2, 0, str(preview_path.relative_to(model.root)), curses.A_BOLD)
            for i, line in enumerate(preview[scroll:scroll + max(0, visible - 1)]):
                _write(screen, i + 3, 0, line)
        else:
            start = max(0, selected - max(0, visible - 1))
            for i, row in enumerate(rows[start:start + visible]):
                marker = ("▾ " if row.expanded else "▸ ") if row.is_dir else "  "
                text = "  " * row.depth + marker + row.name + (" →" if row.symlink else "")
                _write(screen, i + 2, 0, text, curses.A_REVERSE if start + i == selected else 0)
            if not rows:
                _write(screen, 2, 0, "(empty directory)")
        screen.refresh()
        key = screen.getch()
        message = ""
        try:
            if preview is not None:
                if key in (ord("q"), 27):
                    preview = None
                elif key in (curses.KEY_DOWN, ord("j")):
                    scroll = min(scroll + 1, max(0, len(preview) - max(1, visible - 1)))
                elif key in (curses.KEY_UP, ord("k")):
                    scroll = max(0, scroll - 1)
                elif key == ord("o"):
                    model.open_file(preview_path)
                    message = "Opened with system application"
                continue
            if key in (ord("q"), 27):
                return
            if key in (curses.KEY_DOWN, ord("j")):
                selected = min(selected + 1, max(0, len(rows) - 1))
            elif key in (curses.KEY_UP, ord("k")):
                selected = max(0, selected - 1)
            elif key == ord("r"):
                model.refresh()
                message = "Refreshed"
            elif rows and key in (10, 13, curses.KEY_ENTER, curses.KEY_RIGHT, ord("l")):
                row = rows[selected]
                if row.is_dir:
                    model.toggle(row.path)
                    if row.symlink:
                        message = "Directory symlinks are not expanded"
                else:
                    preview_path = row.path
                    preview = model.preview(row.path)
                    scroll = 0
            elif rows and key in (curses.KEY_LEFT, ord("h")):
                path = rows[selected].path
                if path in model.expanded:
                    model.expanded.remove(path)
                elif path.parent != model.root:
                    model.expanded.discard(path.parent)
            elif rows and key == ord("o"):
                model.open_file(rows[selected].path)
                message = "Opened with system application"
        except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
            message = str(error)


def _github_loop(screen, project: dict) -> None:
    model = GitHubView(project)
    message = ""
    scroll = 0
    try:
        model.refresh()
    except Exception as error:
        message = str(error)
    while True:
        status = model.status
        subtitle = status.get("repository") or project.get("repository") or project["root"]
        visible, _ = _frame(screen, f"GitHub · {project['name']}", subtitle,
                            "↑↓ PR  PgUp/PgDn scroll  r refresh  o open PR  q quit", message)
        lines = [f"Branch: {status.get('branch') or '(unknown)'}"]
        if status.get("error") or status.get("message"):
            lines.append(status.get("error") or status["message"])
        prs = status.get("prs", [])
        for i, pr in enumerate(prs):
            lines.append(f"{'›' if i == model.selected else ' '} #{pr['number']} {pr['title']}")
        if prs:
            pr = prs[model.selected]
            lines.extend(["", f"Selected PR #{pr['number']} · {pr.get('state', '')}", pr["url"]])
            checks = pr.get("checks", [])
            lines.extend(f"  {c.get('bucket') or c.get('state') or '?'}  {c.get('name', 'check')}" for c in checks)
            if pr.get("checks_error"):
                lines.append(pr["checks_error"])
            elif not checks:
                lines.append("No checks reported")
        elif not status.get("error"):
            lines.append("No pull request to open. Press r to refresh.")
        for i, line in enumerate(lines[scroll:scroll + visible]):
            _write(screen, i + 2, 0, line)
        screen.refresh()
        key = screen.getch()
        message = ""
        try:
            if key in (ord("q"), 27):
                return
            if key == ord("r"):
                model.refresh()
                message = "Refreshed"
                scroll = 0
            elif key in (ord("o"), 10, 13, curses.KEY_ENTER):
                model.open_pr()
                message = "Opened pull request"
            elif key in (curses.KEY_DOWN, ord("j")):
                model.selected = min(model.selected + 1, max(0, len(prs) - 1))
            elif key in (curses.KEY_UP, ord("k")):
                model.selected = max(0, model.selected - 1)
            elif key == curses.KEY_NPAGE:
                scroll = min(scroll + max(1, visible), max(0, len(lines) - visible))
            elif key == curses.KEY_PPAGE:
                scroll = max(0, scroll - max(1, visible))
        except Exception as error:
            message = str(error)


def _vm_mouse_row(screen, start: int, count: int) -> tuple[int, int, int] | None:
    """Translate a left click on a rendered row into its underlying VM index."""
    try:
        _, x, y, _, buttons = curses.getmouse()
    except curses.error:
        return None
    height, width = screen.getmaxyx()
    if not buttons & (curses.BUTTON1_CLICKED | curses.BUTTON1_RELEASED):
        return None
    if not 0 <= x < width or not 3 <= y < min(3 + count, height - 2):
        return None
    return start + y - 3, x, y


def _vm_loop(screen, project: dict, callbacks: dict) -> None:
    model = VMView(project, callbacks)
    message = ""
    try:
        model.refresh()
    except Exception as error:
        message = str(error)
    old_mousemask = None
    last_mouse_activation: tuple[tuple[int, int], float] | None = None
    try:
        try:
            _, old_mousemask = curses.mousemask(curses.BUTTON1_CLICKED | curses.BUTTON1_RELEASED)
        except curses.error:
            pass  # Keyboard activation remains available without mouse support.
        while True:
            linked = model.project.get("vm")
            visible, _ = _frame(screen, f"exe.dev · {project['name']}", f"Linked VM: {linked or '(none)'}",
                                "↑↓ browse  Enter/s use host  click use  l link only  b browser  r refresh  q quit", message)
            _write(screen, 2, 0, "Choose a host; the previous shell stays in its own tab")
            start = max(0, model.selected - max(0, visible - 2))
            shown = model.vms[start:start + max(0, visible - 1)]
            for i, vm in enumerate(shown):
                text = f"{'●' if vm.get('vm_name') == linked else ' '} {vm.get('vm_name', '?')}  {vm.get('status') or '(status not reported)'}"
                _write(screen, i + 3, 0, text, curses.A_REVERSE if start + i == model.selected else 0)
            if not model.vms:
                _write(screen, 3, 0, "No VMs found. Press r to retry discovery.")
            screen.refresh()
            key = screen.getch()
            message = ""
            if key != curses.KEY_MOUSE:
                last_mouse_activation = None
            try:
                if key in (ord("q"), 27):
                    return
                if key == ord("r"):
                    model.refresh()
                    message = "Refreshed"
                elif key in (curses.KEY_DOWN, ord("j")):
                    model.selected = min(model.selected + 1, max(0, len(model.vms) - 1))
                elif key in (curses.KEY_UP, ord("k")):
                    model.selected = max(0, model.selected - 1)
                elif key == ord("l"):
                    model.link_selected()
                    message = f"Linked {model.project['vm']}; Enter switches the shell"
                elif key in (ord("s"), 10, 13, curses.KEY_ENTER):
                    model.activate_selected()
                    message = f"Using {model.project['vm']}; previous shell kept in a tab"
                    last_mouse_activation = None
                elif key == curses.KEY_MOUSE:
                    hit = _vm_mouse_row(screen, start, len(shown))
                    now = time.monotonic()
                    # Some terminals send release and clicked for one gesture.
                    # Activate once, without consuming an unrelated later click.
                    if hit is not None and not (last_mouse_activation and
                            last_mouse_activation[0] == hit[1:] and now - last_mouse_activation[1] < .25):
                        row, x, y = hit
                        model.selected = row
                        last_mouse_activation = ((x, y), now)
                        model.activate_selected()
                        message = f"Using {model.project['vm']}; previous shell kept in a tab"
                elif key == ord("b"):
                    model.browser()
                    message = "Opened linked VM in browser"
            except Exception as error:
                message = str(error)
    finally:
        if old_mousemask is not None:
            try:
                curses.mousemask(old_mousemask)
            except curses.error:
                pass


def run_tool(kind: str, project: dict, callbacks: dict | None = None) -> None:
    """Run one independent view; only explicit keys invoke callbacks/actions."""
    if kind not in ("files", "github", "exedev"):
        raise ValueError(f"Unknown tool: {kind}")
    if not sys.stdin.isatty() or not sys.stdout.isatty():
        raise ValueError("Interactive tools need a terminal. Open this tool in a native Rex pane.")
    callbacks = callbacks or {}

    def main(screen):
        try:
            curses.curs_set(0)
        except curses.error:
            pass
        screen.keypad(True)
        if kind == "files":
            _tree_loop(screen, project)
        elif kind == "github":
            _github_loop(screen, project)
        else:
            _vm_loop(screen, project, callbacks)
    curses.wrapper(main)
