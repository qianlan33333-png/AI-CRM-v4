import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import sys
sys.path.insert(0,str(Path(__file__).parent))
from release_control import verify_pr
from release_handoff import validate
SCRIPT = Path(__file__).with_name('release_control.py')

class ControlTests(unittest.TestCase):
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
if __name__=='__main__': unittest.main()
