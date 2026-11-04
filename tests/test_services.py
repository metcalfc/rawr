import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from rawr.services import (
    ServiceError, exe_browser_url, exe_find, exe_list, exe_shell_argv,
    github_status, open_url, resolve_repository,
)


def result(out="", code=0, err=""):
    return subprocess.CompletedProcess([], code, stdout=out, stderr=err)


def as_json(value, code=0):
    return result(json.dumps(value), code)


class ServicesTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="rawr services ; λ ")
        self.root = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)

    def github(self, responses, repository="chad/project"):
        with patch("rawr.services.subprocess.run", side_effect=responses) as run:
            value = github_status(self.root, repository)
        return value, run

    def test_github_anchors_cwd_and_literal_branch_argv(self):
        branch = "feature/λ;$(touch-do-not-run)"
        status, run = self.github([
            result(branch + "\n"), as_json([{
                "number": 9, "title": "A pull request", "state": "OPEN",
                "url": "https://github.com/chad/project/pull/9",
            }]), as_json([{"name": "CI", "bucket": "pending", "state": "PENDING"}], 8),
        ])
        self.assertEqual(status["state"], "ready")
        self.assertEqual(status["checks"][0]["bucket"], "pending")
        args = run.call_args_list[1]
        self.assertEqual(args.args[0][6], branch)
        self.assertEqual(args.kwargs["cwd"], self.root)
        for call in run.call_args_list:
            self.assertNotIn("shell", call.kwargs)

    def test_failing_checks_are_valid_data(self):
        value, _ = self.github([
            result("main"), as_json([{"number": 1, "url": "https://github.com/chad/project/pull/1", "state": "OPEN"}]),
            as_json([{"bucket": "fail", "name": "tests"}], 1),
        ])
        self.assertEqual(value["checks"][0]["bucket"], "fail")

    def test_no_pr_and_closed_pr_do_not_query_checks(self):
        value, run = self.github([result("main"), as_json([])])
        self.assertEqual(value["state"], "no-pr")
        self.assertEqual(run.call_count, 2)
        value, run = self.github([result("main"), as_json([{
            "number": 2, "url": "https://github.com/chad/project/pull/2", "state": "MERGED",
        }])])
        self.assertEqual(value["prs"][0]["state"], "MERGED")
        self.assertEqual(run.call_count, 2)

    def test_detached_head_is_actionable(self):
        value, run = self.github([result(code=1)])
        self.assertEqual(value["state"], "detached")
        self.assertEqual(run.call_count, 1)

    def test_authentication_and_offline_errors_distinct_from_no_pr(self):
        for code, err, expected in [
            (4, "", "gh auth login"),
            (1, "HTTP 401 Bad credentials", "authentication"),
            (1, "error connecting to api.github.com", "unreachable"),
        ]:
            with self.subTest(err=err):
                value, _ = self.github([result("main"), result(code=code, err=err)])
                self.assertEqual(value["state"], "error")
                self.assertIn(expected, value["error"])

    def test_missing_gh_timeout_and_malformed_responses(self):
        for failure in [FileNotFoundError(), subprocess.TimeoutExpired("gh", 20), result("invalid JSON"), as_json({"number": 1}), as_json([{"number": "1"}])]:
            with self.subTest(failure=failure):
                value, _ = self.github([result("main"), failure])
                self.assertEqual(value["state"], "error")
                self.assertTrue(value["error"])

    def test_pr_url_must_belong_to_declared_repository(self):
        value, _ = self.github([result("main"), as_json([{"number": 1, "url": "https://github.com/other/repo/pull/1"}])])
        self.assertEqual(value["state"], "error")

    def test_checks_failure_retains_pr_and_shows_error(self):
        value, _ = self.github([
            result("main"), as_json([{"number": 1, "state": "OPEN", "url": "https://github.com/chad/project/pull/1"}]),
            result("not json", 4, "auth required"),
        ])
        self.assertEqual(value["state"], "ready")
        self.assertIn("authentication", value["prs"][0]["checks_error"])

    def test_remote_resolution_collapses_same_repo_and_rejects_ambiguity(self):
        with patch("rawr.services.subprocess.run", return_value=result(
            "remote.origin.url git@github.com:chad/project.git\n"
            "remote.backup.url https://github.com/chad/project.git\n"
        )):
            self.assertEqual(resolve_repository(self.root), "chad/project")
        with patch("rawr.services.subprocess.run", return_value=result(
            "remote.origin.url git@github.com:chad/project.git\n"
            "remote.upstream.url https://github.com/other/project.git\n"
        )):
            with self.assertRaisesRegex(ServiceError, "Multiple.*explicitly"):
                resolve_repository(self.root)

    def test_remote_errors_do_not_expose_credentials(self):
        with patch("rawr.services.subprocess.run", return_value=result(
            "remote.origin.url https://SECRET@gitlab.com/chad/project.git\n"
        )):
            with self.assertRaises(ServiceError) as error:
                resolve_repository(self.root)
        self.assertNotIn("SECRET", str(error.exception))

    def test_explicit_repository_and_invalid_root_fail_before_process(self):
        with patch("rawr.services.subprocess.run") as run:
            self.assertEqual(resolve_repository(self.root, "owner/repo"), "owner/repo")
            for repo in ["-Rbad", "x/y/z", "../."]:
                with self.assertRaises(ServiceError):
                    resolve_repository(self.root, repo)
            self.assertEqual(github_status(self.root / "missing")["state"], "error")
            run.assert_not_called()

    def test_exe_list_honors_ssh_configuration_and_literal_key_path(self):
        key = str(self.root / "key ; λ $(literal)")
        with patch.dict(os.environ, {"EXE_HOST": "user@exe.example", "EXE_SSH_KEY": key}), patch(
            "rawr.services.subprocess.run", return_value=as_json({"vms": [{"vm_name": "devbox", "status": "running"}]})
        ) as run:
            self.assertEqual(exe_list()[0]["vm_name"], "devbox")
        argv = run.call_args.args[0]
        self.assertEqual(argv, ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-i", key, "-o", "IdentitiesOnly=yes", "user@exe.example", "ls", "--json"])
        self.assertNotIn("shell", run.call_args.kwargs)

    def test_exe_zero_exit_errors_and_invalid_shapes_rejected(self):
        for data in [{"error": "unauthorized"}, [], {}, {"vms": [{}]}, {"vms": [{"vm_name": "a"}, {"vm_name": "a"}]}]:
            with self.subTest(data=data), patch("rawr.services.subprocess.run", return_value=as_json(data)):
                with self.assertRaises(ServiceError):
                    exe_list()

    def test_exe_network_error_and_missing_link_are_actionable(self):
        with patch("rawr.services.subprocess.run", return_value=result(code=255, err="connection refused")):
            with self.assertRaisesRegex(ServiceError, "SSH key/agent"):
                exe_list()
        with patch("rawr.services.subprocess.run", return_value=as_json({"vms": []})):
            with self.assertRaisesRegex(ServiceError, "saved project link is unchanged"):
                exe_find("gone")

    def test_shell_destination_preserved_and_no_lifecycle_commands(self):
        with patch.dict(os.environ, {"EXE_SSH_KEY": ""}), patch("rawr.services.subprocess.run") as run:
            self.assertEqual(exe_shell_argv({"vm_name": "routed", "ssh_dest": "vm+routed@vm.exe.xyz"}), [
                "ssh", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=15", "vm+routed@vm.exe.xyz",
            ])
            self.assertEqual(exe_browser_url({"vm_name": "routed"}), "https://routed.exe.xyz")
            run.assert_not_called()
        with patch("rawr.services.subprocess.run", return_value=as_json({"vms": [{"vm_name": "routed"}]})) as run:
            exe_list()
            exe_find("routed")
        for call in run.call_args_list:
            argv = call.args[0]
            self.assertEqual(argv[-2:], ["ls", "--json"])
            self.assertFalse(set(argv) & {"new", "cp", "rm", "stop", "restart"})

    def test_untrusted_host_or_destination_cannot_become_ssh_option(self):
        with patch.dict(os.environ, {"EXE_HOST": "-oProxyCommand=bad"}), patch("rawr.services.subprocess.run") as run:
            with self.assertRaises(ServiceError):
                exe_list()
            run.assert_not_called()
        with self.assertRaises(ServiceError):
            exe_shell_argv({"vm_name": "devbox", "ssh_dest": "-oProxyCommand=bad"})

    def test_browser_uses_argv_and_rejects_credentials_or_non_web_protocols(self):
        url = "https://devbox.exe.xyz/a?x=$(literal)&y=λ"
        with patch("rawr.services.sys.platform", "darwin"), patch("rawr.services.subprocess.run", return_value=result()) as run:
            open_url(url)
            self.assertEqual(run.call_args.args[0], ["open", url])
        with patch("rawr.services.subprocess.run") as run:
            for url in ["file:///tmp/a", "javascript:alert(1)", "https://user:SECRET@example.com", "https://good.example\nmalicious"]:
                with self.assertRaises(ServiceError):
                    open_url(url)
            run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
