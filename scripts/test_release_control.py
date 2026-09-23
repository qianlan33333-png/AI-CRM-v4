import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import sys
sys.path.insert(0,str(Path(__file__).parent))
from release_control import verify_pr, validate_development_checkpoint, locked_state
from release_coordinator import record_development_checkpoint, register
from release_handoff import validate
SCRIPT = Path(__file__).with_name('release_control.py')

class ControlTests(unittest.TestCase):
    def test_development_checkpoint_is_durable_idempotent_and_only_handoff_closes_it(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); state_path = root/'state.json'
            value = {'candidate_id':'pr13-code-complete','work_item':'group-invite-qr',
                     'origin_thread_id':'origin','owner_thread_id':'owner','branch':'codex/qr',
                     'pr_url':'https://github.com/o/r/pull/13','commit_sha':'a'*40,
                     'tree_sha':'b'*40,'base_main_sha':'c'*40,
                     'blocked_reason':'staging receipt missing','required_action':'build exact preview',
                     'resubmit_conditions':['accepted receipt'],'evidence':['https://github.com/o/r/pull/13']}
            with self.assertRaisesRegex(ValueError,'does not exist'):
                locked_state(state_path,lambda state: record_development_checkpoint(state,value,'coordinator'),require_existing=True)
            state_path.write_text(json.dumps({'schema':2,'items':[],'returns':[],'events':[]}))
            for _ in range(2):
                locked_state(state_path,lambda state: record_development_checkpoint(state,value,'coordinator'),require_existing=True)
            state=json.loads(state_path.read_text())
            self.assertEqual(len(state['items']),1)
            self.assertEqual(len(state['events']),1)
            self.assertEqual(state['items'][0]['status'],'blocked_development')
            self.assertEqual(state['events'][0]['payload']['owner_thread_id'],'owner')
            self.assertEqual(state['events'][0]['delivery']['status'],'pending')
            changed=dict(value,blocked_reason='different')
            with self.assertRaisesRegex(ValueError,'reused'):
                locked_state(state_path,lambda state: record_development_checkpoint(state,changed,'coordinator'),require_existing=True)
            self.assertEqual(json.loads(state_path.read_text())['items'][0]['blocked_reason'],'staging receipt missing')
            handoff={'change_class':'runtime','staging_acceptance':{'candidate_id':'accepted-pr13','receipt_sha256':'d'*64},
                     'origin_thread_id':'successor-task','work_item':'group-invite-qr','branch':'codex/qr',
                     'pr_url':'https://github.com/o/r/pull/13',
                     'commit_sha':'a'*40,'tree_sha':'b'*40,'candidate_tree_sha':'b'*40,
                     'base_main_sha':'c'*40,'merge_preview_sha':'e'*40,'package_sha256':'f'*64}
            with self.assertRaisesRegex(ValueError,'explicitly supersede'):
                locked_state(state_path,lambda state: register(state,handoff,'accepted-pr13','coordinator'),require_existing=True)
            self.assertEqual(json.loads(state_path.read_text())['items'][0]['status'],'blocked_development')
            wrong=dict(handoff,supersedes_checkpoint_id='unrelated')
            with self.assertRaisesRegex(ValueError,'unique active checkpoint'):
                locked_state(state_path,lambda state: register(state,wrong,'accepted-pr13','coordinator'),require_existing=True)
            handoff['supersedes_checkpoint_id']='pr13-code-complete'
            locked_state(state_path,lambda state: register(state,handoff,'accepted-pr13','coordinator'),require_existing=True)
            state=json.loads(state_path.read_text())
            self.assertEqual(state['items'][0]['status'],'superseded_by_handoff')
            self.assertEqual(state['items'][0]['events'][-1]['successor_thread_id'],'successor-task')
            self.assertEqual(state['items'][1]['status'],'handoff_ready')
            self.assertEqual(len(state['events']),2)
    def test_checkpoint_cannot_be_closed_by_unrelated_pr_or_source(self):
        state={'schema':2,'items':[],'events':[],'returns':[]}
        value={'candidate_id':'old','work_item':'qr','origin_thread_id':'origin','owner_thread_id':'owner',
               'branch':'codex/qr','pr_url':'https://github.com/o/r/pull/13',
               'commit_sha':'a'*40,'tree_sha':'b'*40,'base_main_sha':'c'*40,
               'blocked_reason':'receipt missing','required_action':'build preview',
               'resubmit_conditions':['receipt'],'evidence':['PR']}
        record_development_checkpoint(state,value,'coordinator')
        handoff={'change_class':'runtime','staging_acceptance':{'candidate_id':'new'},
                 'origin_thread_id':'successor','work_item':'qr','branch':'codex/qr',
                 'pr_url':'https://github.com/o/r/pull/other','worktree':'/tmp/nonexistent',
                 'commit_sha':'a'*40,'tree_sha':'b'*40,'candidate_tree_sha':'b'*40,
                 'base_main_sha':'c'*40,'merge_preview_sha':'e'*40,'package_sha256':'f'*64,
                 'supersedes_checkpoint_id':'old'}
        with self.assertRaisesRegex(ValueError,'unique active checkpoint'):
            register(state,handoff,'new','coordinator')
        handoff['pr_url']=value['pr_url']; handoff['commit_sha']='d'*40
        with self.assertRaisesRegex(ValueError,'does not descend'):
            register(state,handoff,'new','coordinator')
        self.assertEqual(state['items'][0]['status'],'blocked_development')

    def test_governance_only_handoff_uses_local_evidence_without_runtime_package(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp); repo=root/'repo'; repo.mkdir()
            def run(*args,**kwargs): return subprocess.check_output(['git','-C',str(repo),*args],text=True,**kwargs).strip()
            run('init','-q'); run('config','user.name','test'); run('config','user.email','test@example.invalid'); (repo/'base').write_text('base'); run('add','base'); run('commit','-qm','base'); base=run('rev-parse','HEAD'); run('update-ref','refs/remotes/origin/main',base); run('branch','-m','codex/governance'); run('branch','main',base); run('remote','add','origin',str(repo)); (repo/'governance').write_text('rules'); run('add','governance'); run('commit','-qm','governance'); head=run('rev-parse','HEAD'); tree=run('rev-parse','HEAD^{tree}'); preview=run('commit-tree',tree,'-p',base,'-p',head,input='preview\n')
            evidence=root/'tests.log'; evidence.write_text('41 tests passed\n'); import hashlib
            handoff=root/'handoff.json'; handoff.write_text(json.dumps({'change_class':'governance_only','work_item':'release-governance','origin_thread_id':'origin','branch':'codex/governance','worktree':str(repo),'commit_sha':head,'tree_sha':tree,'scope':'release metadata','affected_modules':['scripts'],'dependencies':[],'local_tests':['unit'],'known_risks':[],'release_ready':True,'pr_url':'https://github.com/o/r/pull/1','oneid_decision':'not involved','persistence_decision':'local files','external_effects_decision':'GitHub read only','rollback_point':base,'base_main_sha':base,'merge_preview_sha':preview,'candidate_tree_sha':tree,'governance_acceptance':{'status':'accepted','checks':[{'name':'release unit tests','command':'python3 -m unittest','passed':True,'evidence_path':str(evidence),'evidence_sha256':hashlib.sha256(evidence.read_bytes()).hexdigest()}]}}))
            with patch('release_handoff.live_remote_main',return_value=base): self.assertEqual(validate(handoff)['change_class'],'governance_only')
            state=root/'state.json'; registered=subprocess.run(['python3',str(Path(__file__).with_name('release_coordinator.py')),'--state',str(state),'register',str(handoff),'governance-1'],capture_output=True,text=True)
            self.assertEqual(registered.returncode,0,registered.stderr); self.assertIsNone(json.loads(state.read_text())['items'][0]['package_sha256'])
            evidence.write_text('changed')
            with patch('release_handoff.live_remote_main',return_value=base),self.assertRaisesRegex(ValueError,'evidence'): validate(handoff)
    def test_preview_lineage_and_no_source_change(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); repo = root/'repo'; repo.mkdir()
            def run(*args, **kwargs): return subprocess.run(['git','-C',str(repo),*args],text=True,capture_output=True,check=True,**kwargs).stdout.strip()
            run('init','-q'); run('config','user.name','test'); run('config','user.email','test@example.invalid')
            (repo/'base').write_text('base\n'); run('add','base'); run('commit','-qm','base')
            base=run('rev-parse','HEAD'); run('update-ref','refs/remotes/origin/main',base); run('branch','-m','codex/test'); run('branch','main',base); run('remote','add','origin',str(repo))
            (repo/'feature').write_text('feature\n'); run('add','feature'); run('commit','-qm','feature')
            head=run('rev-parse','HEAD'); output=root/'manifest.json'
            result=subprocess.run(['python3',str(SCRIPT),'release','candidate-preview',str(repo),'codex/test',base,str(output)],text=True,capture_output=True)
            self.assertEqual(result.returncode,0,result.stderr)
            manifest=json.loads(output.read_text())
            self.assertEqual(manifest['pr_head_sha'],head)
            self.assertEqual(run('rev-list','--parents','-n','1',manifest['merge_preview_sha']).split()[1:],[base,head])
            self.assertEqual(run('status','--porcelain'),'')
            self.assertEqual(run('rev-parse','HEAD'),head)
            valid=subprocess.run(['python3',str(SCRIPT),'release','verify-lineage',str(repo),base,head,manifest['merge_preview_sha'],'--head-sha',head],text=True,capture_output=True)
            self.assertEqual(valid.returncode,0,valid.stderr)
            unrelated=run('commit-tree',run('rev-parse','HEAD^{tree}'),'-p',base,input='unrelated\n')
            bad_ancestor=subprocess.run(['python3',str(SCRIPT),'release','verify-lineage',str(repo),base,unrelated,manifest['merge_preview_sha']],text=True,capture_output=True)
            self.assertNotEqual(bad_ancestor.returncode,0)
            moved=run('rev-parse','HEAD')
            run('update-ref','refs/remotes/origin/main',moved)
            bad=subprocess.run(['python3',str(SCRIPT),'release','verify-lineage',str(repo),base,base,manifest['merge_preview_sha']],text=True,capture_output=True)
            self.assertNotEqual(bad.returncode,0)
    def test_promote_fails_closed(self):
        result=subprocess.run(['python3',str(SCRIPT),'release','promote'],text=True,capture_output=True)
        self.assertNotEqual(result.returncode,0)
        self.assertIn('choose exactly one',result.stderr)
        missing=subprocess.run(['python3',str(SCRIPT),'release','promote','--prepare'],text=True,capture_output=True)
        self.assertNotEqual(missing.returncode,0)
        self.assertIn('prepare requires',missing.stderr)
    def test_submit_pr_must_match_origin_head_and_base(self):
        value={'pr_url':'https://github.com/o/r/pull/7','worktree':'/tmp/example','commit_sha':'a'*40,'base_main_sha':'b'*40}
        pr={'head':{'sha':'a'*40},'base':{'sha':'b'*40,'repo':{'full_name':'o/r'}}}
        with patch('release_control.git',return_value='git@github.com:o/r.git'),patch('release_control.subprocess.check_output',return_value=json.dumps(pr)):
            verify_pr(value)
            pr['head']['sha']='c'*40
            with patch('release_control.subprocess.check_output',return_value=json.dumps(pr)):
                with self.assertRaisesRegex(ValueError,'head or base moved'): verify_pr(value)
    def test_checkpoint_requires_current_clean_pr_tree(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp); repo=root/'repo'; repo.mkdir()
            def run(*args): return subprocess.check_output(['git','-C',str(repo),*args],text=True).strip()
            run('init','-q'); run('config','user.name','test'); run('config','user.email','test@example.invalid')
            (repo/'source').write_text('current'); run('add','source'); run('commit','-qm','current')
            run('branch','-m','codex/qr'); run('remote','add','origin','https://github.com/o/r.git')
            head=run('rev-parse','HEAD'); tree=run('rev-parse','HEAD^{tree}')
            value={'candidate_id':'pr13-code-complete','work_item':'group-invite-qr',
                   'origin_thread_id':'origin','owner_thread_id':'owner','branch':'codex/qr',
                   'worktree':str(repo),'pr_url':'https://github.com/o/r/pull/13',
                   'commit_sha':head,'tree_sha':tree,'base_main_sha':'c'*40,
                   'blocked_reason':'receipt missing','required_action':'build preview',
                   'resubmit_conditions':['accepted receipt'],'evidence':['PR check']}
            manifest=root/'checkpoint.json'; manifest.write_text(json.dumps(value))
            pr={'head':{'sha':head},'base':{'sha':'c'*40,'repo':{'full_name':'o/r'}}}
            original_output=subprocess.check_output
            with patch('release_control.subprocess.check_output') as command:
                # Only the remote PR API is mocked; git evidence is read from the real worktree.
                def output(argv,*args,**kwargs):
                    if argv[:2]==['gh','api']: return json.dumps(pr)
                    return original_output(argv,*args,**kwargs)
                command.side_effect=output
                self.assertEqual(validate_development_checkpoint(manifest)['tree_sha'],tree)
                pr['head']['sha']='d'*40
                with self.assertRaisesRegex(ValueError,'head or base moved'): validate_development_checkpoint(manifest)
                pr['head']['sha']=head
                (repo/'source').write_text('dirty')
                with self.assertRaisesRegex(ValueError,'dirty'): validate_development_checkpoint(manifest)
if __name__=='__main__': unittest.main()
