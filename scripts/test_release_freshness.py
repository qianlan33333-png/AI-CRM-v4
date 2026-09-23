import json, shutil, subprocess, sys, tempfile, unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
sys.path.insert(0,str(Path(__file__).parent))
from release_freshness import build_attestation, read_public_main, sign, verify

@unittest.skipUnless(shutil.which('ssh-keygen'),'ssh-keygen required')
class FreshnessTests(unittest.TestCase):
 def fixture(self,root):
  repo=root/'repo'; repo.mkdir()
  def git(*args,**kw): return subprocess.check_output(['git','-C',str(repo),*args],text=True,**kw).strip()
  subprocess.run(['git','-C',str(repo),'init','-q'],check=True); git('config','user.name','test'); git('config','user.email','test@example.invalid')
  (repo/'base').write_text('base'); git('add','base'); git('commit','-qm','base'); base=git('rev-parse','HEAD'); base_tree=git('rev-parse','HEAD^{tree}'); git('branch','-M','main')
  git('checkout','-qb','feature'); (repo/'feature').write_text('feature'); git('add','feature'); git('commit','-qm','feature'); head=git('rev-parse','HEAD'); tree=git('rev-parse','HEAD^{tree}')
  preview=git('commit-tree',tree,'-p',base,'-p',head,input='preview\n'); git('update-ref','refs/candidates/preview',preview)
  bundle=root/'candidate.bundle'; subprocess.run(['git','-C',str(repo),'bundle','create',str(bundle),'--all'],check=True)
  key=root/'signing'; subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(key)],check=True)
  identity='aicrm-release-command-center'; allowed=root/'allowed_signers'; allowed.write_text(identity+' '+(root/'signing.pub').read_text())
  return repo,bundle,key,allowed,identity,base,base_tree,head,tree,preview
 def test_public_main_query_requires_public_and_returns_tree(self):
  calls=[]
  def fetch(url,token):
   calls.append((url,token))
   if url.endswith('/o/r'): return {'private':False}
   if '/branches/main' in url: return {'commit':{'sha':'a'*40}}
   return {'tree':{'sha':'b'*40}}
  sha,tree,_=read_public_main('o/r',token='read-only',fetch=fetch)
  self.assertEqual((sha,tree),('a'*40,'b'*40)); self.assertEqual(len(calls),3)
  def private(url,token): return {'private':True}
  with self.assertRaisesRegex(ValueError,'not public'): read_public_main('o/r',fetch=private)
 def test_signed_source_attestation_verifies_and_binds_bundle(self):
  with tempfile.TemporaryDirectory() as t:
   root=Path(t); repo,bundle,key,allowed,identity,main,main_tree,head,tree,preview=self.fixture(root); now=datetime.now(timezone.utc)
   value=build_attestation(repository='o/r',branch='main',stage='source',main_sha=main,main_tree=main_tree,pr_head_sha=head,pr_head_tree=tree,merge_preview_sha=preview,candidate_tree_sha=tree,bundle=bundle,package=None,identity=identity,queried_at=now,now=now)
   att=root/'attestation.json'; sig=root/'attestation.sig'; sign(value,att,sig,key)
   expected={k:value[k] for k in ('repository','branch','stage','main_sha','main_tree','pr_head_sha','pr_head_tree','merge_preview_sha','candidate_tree_sha')}
   self.assertEqual(verify(attestation_path=att,signature_path=sig,allowed_signers=allowed,identity=identity,expected=expected,bundle=bundle,package=None,git_repository=repo,now=now)['main_sha'],main)
   bundle.write_bytes(bundle.read_bytes()+b'changed')
   with self.assertRaisesRegex(ValueError,'bundle digest'): verify(attestation_path=att,signature_path=sig,allowed_signers=allowed,identity=identity,expected=expected,bundle=bundle,package=None,git_repository=repo,now=now)
 def test_tamper_expiry_and_expected_repository_fail_closed(self):
  with tempfile.TemporaryDirectory() as t:
   root=Path(t); repo,bundle,key,allowed,identity,main,main_tree,head,tree,preview=self.fixture(root); issued=datetime.now(timezone.utc)-timedelta(minutes=20)
   value=build_attestation(repository='o/r',branch='main',stage='source',main_sha=main,main_tree=main_tree,pr_head_sha=head,pr_head_tree=tree,merge_preview_sha=preview,candidate_tree_sha=tree,bundle=bundle,package=None,identity=identity,queried_at=issued,now=issued,ttl_seconds=60)
   att=root/'attestation.json'; sig=root/'attestation.sig'; sign(value,att,sig,key); expected={k:value[k] for k in ('repository','branch','stage','main_sha','main_tree','pr_head_sha','pr_head_tree','merge_preview_sha','candidate_tree_sha')}
   with self.assertRaisesRegex(ValueError,'expired'): verify(attestation_path=att,signature_path=sig,allowed_signers=allowed,identity=identity,expected=expected,bundle=bundle,package=None,git_repository=repo)
   changed=dict(value); changed['repository']='other/repo'; att.write_bytes((json.dumps(changed,sort_keys=True,separators=(',',':'))+'\n').encode())
   with self.assertRaisesRegex(ValueError,'signature'): verify(attestation_path=att,signature_path=sig,allowed_signers=allowed,identity=identity,expected=expected,bundle=bundle,package=None,git_repository=repo,now=issued)
 def test_promotion_attestation_binds_exact_package(self):
  with tempfile.TemporaryDirectory() as t:
   root=Path(t); repo,bundle,key,allowed,identity,main,main_tree,head,tree,preview=self.fixture(root); now=datetime.now(timezone.utc); package=root/'aicrm.tar.gz'; package.write_bytes(b'accepted-package')
   value=build_attestation(repository='o/r',branch='main',stage='promotion',main_sha=main,main_tree=main_tree,pr_head_sha=head,pr_head_tree=tree,merge_preview_sha=preview,candidate_tree_sha=tree,bundle=bundle,package=package,identity=identity,queried_at=now,now=now)
   att=root/'promotion.json'; sig=root/'promotion.sig'; sign(value,att,sig,key); expected={k:value[k] for k in ('repository','branch','stage','main_sha','main_tree','pr_head_sha','pr_head_tree','merge_preview_sha','candidate_tree_sha')}
   verify(attestation_path=att,signature_path=sig,allowed_signers=allowed,identity=identity,expected=expected,bundle=bundle,package=package,git_repository=repo,now=now)
   package.write_bytes(b'different')
   with self.assertRaisesRegex(ValueError,'package digest'): verify(attestation_path=att,signature_path=sig,allowed_signers=allowed,identity=identity,expected=expected,bundle=bundle,package=package,git_repository=repo,now=now)
if __name__=='__main__': unittest.main()
