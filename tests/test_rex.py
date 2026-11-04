import json
import subprocess
import unittest
from unittest.mock import patch

from rawr.rex import Rex, TESTED_VERSION, blocks
from rawr.state import Error


class RexContractTests(unittest.TestCase):
    def result(self, value, code=0, stderr=''):
        return subprocess.CompletedProcess([], code, json.dumps(value), stderr)

    def identity(self, **changes):
        return {'host_id':'host-test','instance_id':'instance-test',
                'endpoint':'unix:///tmp/rawr-test.sock','rex_version':TESTED_VERSION, **changes}

    @patch('rawr.rex.subprocess.run')
    def test_identity_checks_version_and_local_endpoint(self, run):
        rex=Rex('/fixture/rex')
        run.return_value=self.result(self.identity())
        self.assertEqual(rex.identity()['instance_id'],'instance-test')
        for update, message in (({'rex_version':'future-version'},'Unsupported Rex version'),
                                ({'host_id':None},'lacks host'),
                                ({'endpoint':'https://another-host'},'Remote Rex')):
            run.return_value=self.result(self.identity(**update))
            with self.assertRaisesRegex(Error,message):
                rex.identity()
        self.assertEqual(run.call_args.args[0][1],'--autostart=false')

    @patch('rawr.rex.subprocess.run')
    def test_cli_errors_and_timeouts_never_retry(self, run):
        rex=Rex('/fixture/rex')
        for response in (subprocess.CompletedProcess([],0,'not json',''),
                         self.result([],0),
                         self.result({},1,'disconnected')):
            run.reset_mock(); run.return_value=response
            with self.assertRaises(Error):
                rex.call('session.create',label='literal')
            self.assertEqual(run.call_count,1)
        run.reset_mock(); run.side_effect=subprocess.TimeoutExpired(['rex'],12)
        with self.assertRaisesRegex(Error,'creation may have succeeded'):
            rex.call('session.create',label='literal')
        self.assertEqual(run.call_count,1)

    @patch('rawr.rex.subprocess.run')
    def test_creation_arguments_remain_literal_json(self, run):
        run.return_value=self.result({'session_id':'session:test'})
        tricky='雪 spaces $(touch SHOULD_NOT_EXIST); `date` " \\ '
        Rex('/fixture/rex').call('session.create',label=tricky)
        argv=run.call_args.args[0]
        self.assertEqual(json.loads(argv[-1]),{'label':tricky})
        self.assertNotIn('shell',run.call_args.kwargs)

    @patch('rawr.rex.subprocess.run')
    def test_client_selection_accepts_documented_null_result(self, run):
        run.side_effect=[self.result({'revision':1}),
                         self.result({'clients':[{'client_id':'client:test','info':{'kind':'app','label':'test'}}]}),
                         self.result(None)]
        self.assertEqual(Rex('/fixture/rex').select('session:test','block:test'),'selected in Rex app')

    @patch('rawr.rex.subprocess.run')
    def test_malformed_session_rows_have_actionable_error(self, run):
        rex=Rex('/fixture/rex')
        for session_list in ([None],['session:test'],[{}],[{'session_id':None}]):
            run.return_value=self.result({'sessions':session_list})
            with self.assertRaisesRegex(Error,'Malformed Rex session'):
                rex.sessions()

    @patch('rawr.rex.subprocess.run')
    def test_stale_view_revision_cannot_replace_newer_observation(self, run):
        rex=Rex('/fixture/rex')
        def view(sid, revision):
            return self.result({'session_id':sid,'revision':revision,'windows':[]})
        run.side_effect=[view('session:a',5), view('session:a',4),
                         view('session:a',5), view('session:a',6), view('session:b',1)]
        self.assertEqual(rex.view('session:a')['revision'],5)
        with self.assertRaisesRegex(Error,'[Ss]tale|[Oo]ut.of.order|revision'):
            rex.view('session:a')
        # Equal revisions are valid reads; a rejected stale observation must
        # not lower the remembered revision. Sessions have separate counters.
        self.assertEqual(rex.view('session:a')['revision'],5)
        self.assertEqual(rex.view('session:a')['revision'],6)
        self.assertEqual(rex.view('session:b')['revision'],1)

    @patch('rawr.rex.subprocess.run')
    def test_exited_process_nulls_normalize_and_malformed_metadata_stops_safely(self, run):
        rex=Rex('/fixture/rex')
        run.return_value=self.result({'child':None,'foreground':None,'ancestors':None,
                                      'last_exit':{'code':1}})
        exited=rex.process('session:test','block:exited')
        self.assertEqual(exited['child'],{})
        self.assertEqual(exited['foreground'],{})
        self.assertEqual(exited['ancestors'],[])
        self.assertEqual(exited['last_exit'],{'code':1})
        for malformed in ({'child':[]}, {'child':{'pid':'123'}},
                          {'foreground':'invalid'}, {'ancestors':{}},
                          {'ancestors':[None]}, {'last_exit':'failed'}):
            with self.subTest(malformed=malformed):
                run.reset_mock(); run.return_value=self.result(malformed)
                with self.assertRaisesRegex(Error,'Malformed Rex process.*pane preserved'):
                    rex.process('session:test','block:exited')
                self.assertEqual(run.call_count,1)

    @patch('rawr.rex.subprocess.run')
    def test_malformed_clients_do_not_attempt_app_selection(self, run):
        rex=Rex('/fixture/rex')
        for malformed in ({}, {'clients':None}, {'clients':[None]},
                          {'clients':[{'client_id':None,'info':{}}]},
                          {'clients':[{'client_id':'client:test','info':None}]}):
            with self.subTest(malformed=malformed):
                run.reset_mock(); run.return_value=self.result(malformed)
                with self.assertRaisesRegex(Error,'Malformed Rex client list.*session retained'):
                    rex.select('session:test')
                self.assertEqual(run.call_count,1)
                self.assertEqual(run.call_args.args[0][-2:],[
                    'client.list','{}'])

    def test_duplicate_and_malformed_layout_blocks_rejected(self):
        block={'block_id':'block:test'}
        view={'windows':[{'window_id':'window:test','layers':[{'kind':'tiled','blocks':[block,block]}]}]}
        with self.assertRaisesRegex(Error,'Duplicate block ID'):
            blocks(view)
        for malformed in ({'windows':None}, {'windows':[None]},
                          {'windows':[{'window_id':'window:test','layers':[None]}]}):
            with self.assertRaisesRegex(Error,'Malformed Rex layout'):
                blocks(malformed)
        for invalid_id in ('',None,123,{}):
            with self.subTest(invalid_id=invalid_id):
                view={'windows':[{'window_id':'window:test','layers':[
                    {'kind':'tiled','blocks':[{'block_id':invalid_id}]}]}]}
                with self.assertRaisesRegex(Error,'Malformed Rex block ID'):
                    blocks(view)


if __name__=='__main__':
    unittest.main()
