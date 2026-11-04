"""Native macOS launch metadata shapes and occupied-program regressions."""
import copy
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

from rawr.project import Projects, recipe


class NativeProcessIdentityTests(unittest.TestCase):
    def manager(self, proc):
        rex = SimpleNamespace(process=lambda sid, bid: copy.deepcopy(proc))
        app = Projects(None, rex)
        return app

    def binding(self, child):
        return {'block_id':'b','session_id':'s','label':'declared',
                'instance_id':'generation','pid':child['pid']}

    def healthy(self, app, project, binding):
        return app.healthy(project,'tool',binding,{'instance_id':'generation'}, {'b':{'label':'declared'}})

    def test_direct_recipe_disables_login_wrapper(self):
        app = Projects(None, None)
        p = {'roles':{'tool':recipe('command','/tmp',['/bin/sh','-c','while :; do sleep 1; done'])}}
        options = app.block_node(p,'tool','declared')['block']['options']
        self.assertIs(options['login'], False)
        self.assertEqual(options['command'], p['roles']['tool']['argv'])
        shell = {'roles':{'tool':recipe('shell','/tmp')}}
        self.assertNotIn('login', app.block_node(shell,'tool','declared')['block']['options'])

    def test_native_sh_implementation_matches_declared_argv0(self):
        child = {'pid':123,'path':'/bin/bash','invoked_path':'/bin/bash','argv0':'/bin/sh','name':'bash'}
        proc = {'child':child,'foreground':child,'last_exit':None}
        p = {'roles':{'tool':recipe('command','/tmp',['/bin/sh','-c','echo declared'])}}
        self.assertTrue(self.healthy(self.manager(proc),p,self.binding(child))[0])

    def test_builtin_homebrew_interpreter_rewrite_requires_exact_script(self):
        app = self.manager({})
        argv = ['/opt/homebrew/bin/python3', str(Path(__file__).resolve())]
        app.command = lambda project, role: argv
        child = {'pid':123,'path':'/opt/homebrew/Frameworks/Python.app/Python',
                 'argv0':'/opt/homebrew/Frameworks/Python.app/Python','name':'Python','script':argv[1]}
        proc = {'child':child,'foreground':child,'last_exit':None}
        app.rex.process = lambda sid,bid: copy.deepcopy(proc)
        p = {'roles':{'tool':{'kind':'files'}}}
        binding = self.binding(child)
        binding['program'] = {k:v for k,v in child.items() if k != 'pid'}
        self.assertTrue(self.healthy(app,p,binding)[0])
        # Same PID/interpreter with a different script is occupied work.
        proc['child']['script'] = '/tmp/somebody-elses-work.py'
        self.assertFalse(self.healthy(app,p,binding)[0])

    def test_unchanged_pid_and_spoofed_argv0_do_not_hide_changed_binary(self):
        child = {'pid':123,'path':'/bin/bash','argv0':'/bin/sh','name':'bash'}
        proc = {'child':child,'foreground':child,'last_exit':None}
        app = self.manager(proc)
        p = {'roles':{'tool':recipe('command','/tmp',['/bin/sh','-c','echo declared'])}}
        binding = self.binding(child)
        binding['program'] = {k:v for k,v in child.items() if k != 'pid'}
        child['path'] = '/bin/sleep'
        child['name'] = 'sleep'
        self.assertFalse(self.healthy(app,p,binding)[0])

    @patch('rawr.project.time.sleep')
    def test_startup_login_wrapper_is_never_bound_as_declared_program(self, sleep):
        child = {'pid':123,'path':'/usr/bin/login','name':'login'}
        app = self.manager({'child':child,'foreground':child,'last_exit':None})
        p = {'roles':{'tool':recipe('command','/tmp',['/bin/sh','-c','echo declared'])}}
        runtime = {'session_id':'s'}
        app.binding(p,runtime,{'instance_id':'generation'},'tool','b','declared')
        self.assertIsNone(runtime['roles']['tool']['pid'])
        self.assertNotIn('program', runtime['roles']['tool'])

    def interpreter_transition(self):
        """Homebrew execs the launcher in place, retaining its native PID."""
        script = str(Path(__file__).resolve())
        launcher = {'pid':123, 'path':'/opt/homebrew/bin/python3',
                    'argv0':'/opt/homebrew/bin/python3', 'name':'python3'}
        mature = {'pid':123, 'path':'/opt/homebrew/Frameworks/Python.app/Python',
                  'argv0':'/opt/homebrew/Frameworks/Python.app/Python',
                  'invoked_path':'/opt/homebrew/Frameworks/Python.app/Python',
                  'name':'Python', 'script':script}
        snapshots = [{'child':copy.deepcopy(child), 'foreground':copy.deepcopy(child),
                      'last_exit':None} for child in (launcher,mature)]
        app = self.manager({})
        app.command = lambda project, role: ['/opt/homebrew/bin/python3', script]
        project = {'roles':{'tool':{'kind':'files'}}}
        return app, project, snapshots

    def test_launcher_observation_cannot_be_blessed_by_a_later_mature_read(self):
        app, project, (launcher,mature) = self.interpreter_transition()
        app.rex.process = Mock(return_value=mature)
        okay, _ = app.healthy(project,'tool',self.binding(launcher['child']),
            {'instance_id':'generation'}, {'b':{'label':'declared'}},
            observed_process=launcher)
        self.assertFalse(okay)
        # Validating an already observed launcher must not race a second read
        # that sees the same PID after it has exec'd the declared script.
        app.rex.process.assert_not_called()

    @patch('rawr.project.time.sleep')
    def test_binding_records_the_same_mature_snapshot_that_was_verified(self, sleep):
        app, project, (launcher,mature) = self.interpreter_transition()
        app.rex.process = Mock(side_effect=[launcher,mature])
        runtime = {'session_id':'s'}
        app.binding(project,runtime,{'instance_id':'generation'},'tool','b','declared')
        binding = runtime['roles']['tool']
        self.assertEqual(binding['pid'],mature['child']['pid'])
        self.assertEqual(binding['program'],{key:value for key,value in mature['child'].items()
                                           if key != 'pid'})
        self.assertEqual(app.rex.process.call_count,2)
        sleep.assert_called_once_with(.05)


if __name__ == '__main__':
    unittest.main()
