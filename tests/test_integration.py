import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from rawr.integration import (ACTION_NAMES, BEGIN, DEFAULT_BINDINGS, END,
                              IntegrationError, install, lua_quote, render_actions, uninstall)


class IntegrationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config = Path(self.temp.name) / "rex" / "init.lua"
        self.config.parent.mkdir()

    def test_install_update_uninstall_preserves_unrelated_bytes(self):
        original = b'-- personal\r\nrex.bind("cmd+d", "pane.split")\n-- no final newline'
        self.config.write_bytes(original)
        self.config.chmod(0o640)
        self.assertTrue(install(self.config, ["/a b/python", "/repo/rawr"])['changed'])
        first = self.config.read_bytes()
        self.assertTrue(first.startswith(original))
        self.assertEqual(first.count(BEGIN), 1)
        for role, key in DEFAULT_BINDINGS.items():
            self.assertEqual(first.count(('rex.bind("' + key + '", "rawr_toggle_' + role + '")').encode()), 1)
        self.assertFalse(install(self.config, ["/a b/python", "/repo/rawr"])['changed'])
        self.assertTrue(install(self.config, ["/other/python", "/repo/rawr"])['changed'])
        self.assertEqual(self.config.stat().st_mode & 0o777, 0o640)
        # User edits outside our block are retained too.
        self.config.write_bytes(self.config.read_bytes() + b'-- extra\n')
        self.assertTrue(uninstall(self.config)['changed'])
        self.assertEqual(self.config.read_bytes(), original + b'-- extra\n')
        self.assertFalse(uninstall(self.config)['changed'])

    def test_duplicate_and_malformed_markers_do_not_write(self):
        for content in (BEGIN + b'missing end', END, BEGIN + END + BEGIN + END,
                        END + BEGIN, b'-- BEGIN RAWR PROJECTS manually edited\n' + END):
            self.config.write_bytes(content)
            with self.assertRaises(IntegrationError):
                install(self.config)
            self.assertEqual(self.config.read_bytes(), content)
            with self.assertRaises(IntegrationError):
                uninstall(self.config)
            self.assertEqual(self.config.read_bytes(), content)

    def test_conflicting_existing_action_is_preserved(self):
        for name in ACTION_NAMES:
            with self.subTest(name=name):
                original = ('rex.action { name = "' + name + '", run = function() end }').encode()
                self.config.write_bytes(original)
                with self.assertRaises(IntegrationError):
                    install(self.config)
                self.assertEqual(self.config.read_bytes(), original)

    def test_conflicting_literal_shortcuts_and_aliases_are_not_overwritten(self):
        for key in ('cmd+alt+1', 'alt+cmd+1', 'COMMAND+OPTION+2',
                    'opt + cmd + 3', 'cmd+alt+1 > x'):
            with self.subTest(key=key):
                original = ('-- personal\nrex.bind("' + key + '", "personal_action")\n').encode()
                self.config.write_bytes(original)
                with self.assertRaisesRegex(IntegrationError, 'already binds'):
                    install(self.config)
                self.assertEqual(self.config.read_bytes(), original)

    def test_custom_bindings_or_no_bindings_preserve_personal_shortcuts(self):
        original = b'rex.bind("cmd+alt+1", "personal_action")\n'
        self.config.write_bytes(original)
        result = install(self.config, bindings={"files": "ctrl+alt+f"})
        self.assertEqual(result['bindings'], {"files": "ctrl+alt+f"})
        self.assertTrue(self.config.read_bytes().startswith(original))
        self.assertIn(b'rex.bind("ctrl+alt+f", "rawr_toggle_files")', self.config.read_bytes())
        self.assertNotIn(b'rex.bind("cmd+alt+2"', self.config.read_bytes())
        install(self.config, bindings={})
        self.assertTrue(self.config.read_bytes().startswith(original))
        self.assertEqual(self.config.read_bytes().count(b'rex.bind('), 1)

    def test_invalid_or_duplicate_toggle_bindings_leave_config_untouched(self):
        self.config.write_bytes(b'-- original\n')
        for bindings in ({'unknown':'cmd+alt+1'}, {'files':''},
                         {'files':'bad\0key'},
                         {'files':'cmd+alt+1','github':'option+command+1'}):
            with self.subTest(bindings=bindings):
                with self.assertRaises(IntegrationError):
                    install(self.config, bindings=bindings)
                self.assertEqual(self.config.read_bytes(), b'-- original\n')

    def test_validation_failure_and_interrupted_replace_leave_old(self):
        original = b'-- old valid config\n'
        self.config.write_bytes(original)
        def reject(proposal):
            self.assertIn(b'rawr_projects', proposal.read_bytes())
            raise IntegrationError('bad Lua')
        with self.assertRaises(IntegrationError):
            install(self.config, validate=reject)
        self.assertEqual(self.config.read_bytes(), original)
        with patch('rawr.integration.os.replace', side_effect=OSError('interrupted')):
            with self.assertRaises(OSError):
                install(self.config)
        self.assertEqual(self.config.read_bytes(), original)
        self.assertFalse(list(self.config.parent.glob('.init.lua.rawr-*')))

    def test_noncooperating_edit_during_validation_is_detected(self):
        self.config.write_bytes(b'-- old\n')
        def concurrent_edit(proposal):
            self.config.write_bytes(b'-- new unrelated edit\n')
        with self.assertRaisesRegex(IntegrationError, 'changed during'):
            install(self.config, validate=concurrent_edit)
        self.assertEqual(self.config.read_bytes(), b'-- new unrelated edit\n')

    def test_symlink_is_not_replaced(self):
        target = self.config.parent / 'actual.lua'
        target.write_text('-- original')
        self.config.symlink_to(target)
        with self.assertRaisesRegex(IntegrationError, 'symlink'):
            install(self.config)
        self.assertTrue(self.config.is_symlink())
        self.assertEqual(target.read_text(), '-- original')

    def test_launcher_is_literal_lua_argv(self):
        tricky = '/repo/Unicode 雪 " \\ $(touch nope)\n123'
        lua = render_actions(['/usr/bin/python3', tricky])
        self.assertIn(lua_quote(tricky), lua)
        self.assertIn('shell = "none"', lua)
        self.assertNotIn('initial_input', lua)
        self.assertIn('rex.bind("cmd+alt+1", "rawr_toggle_files")', lua)
        self.assertIn('rex.exec({ "/usr/bin/python3", ' + lua_quote(tricky) + ', "toggle-session", "files", "--session", ctx.session_id }', lua)
        self.assertIn('if not result.ok then', lua)
        self.assertIn('rex.json.decode(result.stdout)', lua)
        self.assertIn('decoded.result ~= "complete"', lua)
        self.assertNotIn('rex.block.call', lua)
        self.assertIn('client.sidebar.activity.by_session', lua)
        with self.assertRaises(IntegrationError):
            render_actions([])
        with self.assertRaises(IntegrationError):
            render_actions(['bad\0arg'])


if __name__ == '__main__':
    unittest.main()
