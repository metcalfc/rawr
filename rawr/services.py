"""Bounded, read-only service adapters for project-anchored native tools.

All process boundaries use argv. This module never controls a VM lifecycle,
copies credentials, or depends on the herdr runtime.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import urlparse


class ServiceError(RuntimeError):
    """An actionable service/dependency error suitable for display in a pane."""


def _run(argv, *, cwd=None, timeout=20):
    try:
        return subprocess.run(
            argv, cwd=cwd, capture_output=True, text=True,
            encoding="utf-8", errors="replace", timeout=timeout, check=False,
        )
    except FileNotFoundError as exc:
        raise ServiceError(f"Missing executable: {argv[0]}; install it and refresh.") from exc
    except subprocess.TimeoutExpired as exc:
        raise ServiceError(f"{argv[0]} timed out; check connectivity and refresh.") from exc
    except OSError as exc:
        raise ServiceError(f"Could not run {argv[0]}: {exc.strerror or type(exc).__name__}") from exc


def _detail(value):
    # Do not pass terminal control sequences through a service error.
    value = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", str(value))
    return " ".join(value.split())[:350]


def _json(stdout, label):
    try:
        return json.loads(stdout)
    except (ValueError, TypeError) as exc:
        raise ServiceError(f"{label} returned malformed JSON; refresh or check the tool version.") from exc


_REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")


def _repository_name(value):
    if not isinstance(value, str) or not _REPOSITORY.fullmatch(value):
        raise ServiceError("Repository must be an explicit GitHub OWNER/NAME.")
    owner, name = value.split("/")
    if owner in {".", ".."} or name in {".", ".."}:
        raise ServiceError("Repository must be an explicit GitHub OWNER/NAME.")
    return value


def _remote_repository(url):
    """Recognize GitHub remotes without exposing URLs containing credentials."""
    if url.startswith("git@github.com:"):
        path = url[len("git@github.com:"):]
    else:
        try:
            parsed = urlparse(url)
        except ValueError:
            return None
        if parsed.scheme not in {"https", "http", "ssh", "git"} or parsed.hostname != "github.com":
            return None
        path = parsed.path.lstrip("/")
    path = path.removesuffix(".git").rstrip("/")
    return path if _REPOSITORY.fullmatch(path) else None


def resolve_repository(root, repository=None):
    root = Path(root)
    if not root.is_dir():
        raise ServiceError(f"Project root is inaccessible: {root}")
    if repository is not None:
        return _repository_name(repository)
    result = _run(["git", "-C", str(root), "config", "--get-regexp", r"^remote\..*\.url$"])
    if result.returncode not in {0, 1}:
        raise ServiceError("Cannot inspect Git remotes; check that the project root is a Git checkout.")
    repositories = {}
    for line in result.stdout.splitlines():
        fields = line.split(None, 1)
        if len(fields) != 2:
            continue
        found = _remote_repository(fields[1])
        if found:
            repositories[found.casefold()] = found
    if len(repositories) != 1:
        reason = "Multiple GitHub repositories in remotes" if repositories else "No GitHub repository in remotes"
        raise ServiceError(f"{reason}; declare OWNER/NAME explicitly for this project.")
    return next(iter(repositories.values()))


def _gh_error(result):
    detail = _detail(result.stderr or result.stdout)
    lower = detail.lower()
    if result.returncode == 4 or any(word in lower for word in ("authentication", "gh auth login", "bad credentials", "token has expired", "not logged")):
        return "GitHub authentication is unavailable or expired; run gh auth login, then refresh."
    if any(word in lower for word in ("could not resolve", "error connecting", "network", "connection refused", "timeout", "timed out", "offline")):
        return "GitHub is unreachable; check connectivity and refresh."
    return f"GitHub request failed (exit {result.returncode})" + (f": {detail}" if detail else ".")


def github_status(root, repository=None):
    """Return display data; errors do not escape and never change Rex focus.

    All Git/gh commands remain anchored to root and the chosen repository.
    Checks with pending/failing exit status are still valid JSON data.
    """
    status = {"state": "error", "prs": [], "checks": []}
    try:
        root = Path(root)
        repo = resolve_repository(root, repository)
        status["repository"] = repo
        branch_result = _run(["git", "-C", str(root), "symbolic-ref", "--quiet", "--short", "HEAD"])
        if branch_result.returncode == 1:
            status.update(state="detached", message="Detached HEAD; select a branch to inspect its pull requests.")
            return status
        if branch_result.returncode:
            raise ServiceError("Cannot read project branch; check that the project root is a Git checkout.")
        branch = branch_result.stdout.strip()
        if not branch:
            raise ServiceError("Git returned an empty branch name.")
        status["branch"] = branch
        result = _run([
            "gh", "pr", "list", "--repo", repo, "--head", branch,
            "--state", "all", "--limit", "10", "--json", "number,title,url,state",
        ], cwd=root)
        if result.returncode:
            raise ServiceError(_gh_error(result))
        data = _json(result.stdout, "gh pr list")
        if not isinstance(data, list):
            raise ServiceError("gh pr list returned an unexpected response; expected a PR array.")
        for item in data:
            if not isinstance(item, dict) or type(item.get("number")) is not int or item["number"] <= 0:
                raise ServiceError("gh pr list returned an invalid PR number.")
            url = item.get("url")
            expected_url = f"https://github.com/{repo}/pull/{item['number']}"
            if not isinstance(url, str) or url.casefold() != expected_url.casefold():
                raise ServiceError("gh pr list returned a PR URL outside the declared repository.")
            pr = {
                "number": item["number"], "title": str(item.get("title", "")),
                "url": url, "state": str(item.get("state", "UNKNOWN")), "checks": [],
            }
            if pr["state"] == "OPEN":
                checks_result = _run([
                    "gh", "pr", "checks", str(pr["number"]), "--repo", repo,
                    "--json", "name,bucket,state,link",
                ], cwd=root)
                try:
                    checks = _json(checks_result.stdout, "gh pr checks")
                    if not isinstance(checks, list) or any(not isinstance(c, dict) for c in checks):
                        raise ServiceError("gh pr checks returned an unexpected response; expected a checks array.")
                    if checks_result.returncode not in {0, 1, 8}:
                        raise ServiceError(_gh_error(checks_result))
                    pr["checks"] = [{k: str(c.get(k, "")) for k in ("name", "bucket", "state", "link")} for c in checks]
                except ServiceError as exc:
                    pr["checks_error"] = _gh_error(checks_result) if checks_result.returncode not in {0, 1, 8} else str(exc)
            status["prs"].append(pr)
            status["checks"].extend(pr["checks"])
        status["state"] = "ready" if data else "no-pr"
        if not data:
            status["message"] = f"No pull request found for {branch} in {repo}."
        return status
    except ServiceError as exc:
        status["error"] = str(exc)
        return status


_SSH_DEST = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.+@:\[\]-]*\Z")
_VM_NAME = re.compile(r"[a-z0-9][a-z0-9-]*\Z")


def _ssh_options():
    options = ["-o", "ConnectTimeout=15"]
    key = os.environ.get("EXE_SSH_KEY")
    if key:
        options += ["-i", os.path.expanduser(key), "-o", "IdentitiesOnly=yes"]
    return options


def _ssh_destination(value):
    if not isinstance(value, str) or not _SSH_DEST.fullmatch(value):
        raise ServiceError("exe.dev returned an invalid SSH destination; refresh before opening a shell.")
    return value


def exe_list():
    """Discover existing VMs using the user's SSH key/agent; no credential copies."""
    host = _ssh_destination(os.environ.get("EXE_HOST", "exe.dev"))
    result = _run(["ssh", "-o", "BatchMode=yes", *_ssh_options(), host, "ls", "--json"], timeout=25)
    if result.returncode:
        raise ServiceError("Cannot reach exe.dev; check the SSH key/agent (ssh-add), EXE_HOST and connectivity. " + _detail(result.stderr))
    data = _json(result.stdout, "exe.dev ls")
    if not isinstance(data, dict):
        raise ServiceError("exe.dev ls returned an unexpected response; expected an object with vms.")
    if data.get("error"):
        raise ServiceError(f"exe.dev rejected the request: {_detail(data['error'])}")
    vms = data.get("vms")
    if not isinstance(vms, list):
        raise ServiceError("exe.dev ls returned an unexpected response; expected a vms array.")
    seen = set()
    for vm in vms:
        if not isinstance(vm, dict) or not isinstance(vm.get("vm_name"), str) or not _VM_NAME.fullmatch(vm["vm_name"]):
            raise ServiceError("exe.dev ls returned an invalid VM identity.")
        if vm["vm_name"] in seen:
            raise ServiceError("exe.dev ls returned duplicate VM identities; refresh before choosing a VM.")
        seen.add(vm["vm_name"])
    return vms


def exe_find(name):
    if not isinstance(name, str) or not _VM_NAME.fullmatch(name):
        raise ServiceError("A valid exe.dev VM name is required.")
    for vm in exe_list():
        if vm["vm_name"] == name:
            return vm
    raise ServiceError(f"Linked VM {name} was not found; the saved project link is unchanged.")


def _vm_name(vm):
    name = vm.get("vm_name") if isinstance(vm, dict) else None
    if not isinstance(name, str) or not _VM_NAME.fullmatch(name):
        raise ServiceError("A valid exe.dev VM identity is required.")
    return name


def exe_shell_argv(vm):
    name = _vm_name(vm)
    destination = _ssh_destination(vm.get("ssh_dest") or f"{name}.exe.xyz")
    return ["ssh", "-o", "StrictHostKeyChecking=accept-new", *_ssh_options(), destination]


def exe_browser_url(vm):
    name = _vm_name(vm)
    url = vm.get("https_url") or f"https://{name}.exe.xyz"
    _validate_url(url)
    return url


def _validate_url(url):
    if not isinstance(url, str) or any(ord(c) < 32 for c in url):
        raise ServiceError("Expected an HTTP(S) URL.")
    try:
        parsed = urlparse(url)
    except ValueError as exc:
        raise ServiceError("Expected a valid HTTP(S) URL.") from exc
    if parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username or parsed.password:
        raise ServiceError("Expected an HTTP(S) URL without embedded credentials.")


def open_url(url):
    _validate_url(url)
    command = ["open", url] if sys.platform == "darwin" else ["xdg-open", url]
    result = _run(command)
    if result.returncode:
        raise ServiceError(f"Could not open browser; open this URL manually: {url}")
