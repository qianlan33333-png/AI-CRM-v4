import os, subprocess, sys, tempfile, unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from scan_exact_duplicates import scan

class ExactScannerTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory(); self.base=Path(self.tmp.name); self.repo=self.base/'repo';self.repo.mkdir()
        self.g('init','-q');self.g('config','user.name','audit-test');self.g('config','user.email','audit@example.invalid')
    def tearDown(self):self.tmp.cleanup()
    def g(self,*a):return subprocess.check_output(['git','-C',str(self.repo),*a],stderr=subprocess.STDOUT).decode().strip()
    def commit(self):self.g('add','-A');self.g('commit','-qm','fixture');return self.g('rev-parse','HEAD')
    def write(self,p,b):
        f=self.repo/p;f.parent.mkdir(parents=True,exist_ok=True);f.write_bytes(b)
    def test_all_extensions_binary_empty_paths_modes_and_locked_ref(self):
        for p in ['a.ts','nested/file with spaces.js']:self.write(p,b'same\n')
        os.chmod(self.repo/'a.ts',0o755)
        for p in ['one.png','two.bin']:self.write(p,b'\x00\xffsame\n')
        for p in ['zero.sql','zero.md']:self.write(p,b'')
        self.write('line\nbreak.json',b'unique\n')
        os.symlink('a.ts',self.repo/'link')
        sha=self.commit(); self.write('untracked.ts',b'same\n');self.write('a.ts',b'dirty\n')
        r=scan(self.repo,sha,self.base/'out')
        self.assertTrue(r['full_payload_inventory_complete'])
        self.assertEqual(r['total_entries'],8);self.assertEqual(r['verified_blob_paths'],8)
        self.assertEqual(r['regular_duplicate_groups'],3)
        self.assertNotIn('untracked.ts',[x['path'] for x in r['inventory']])
        same=next(g for g in r['duplicates'] if 'a.ts' in g['paths'])
        self.assertEqual(same['mode_variants'],['100644','100755'])
        self.assertEqual(same['bytes_per_copy'],5)
        self.assertEqual(r['excess_path_bytes'],12)
        self.assertFalse(r['semantic_duplicate_audit_complete'])
    def test_lfs_pointer_is_explicitly_incomplete(self):
        self.write('large.bin',b'version https://git-lfs.github.com/spec/v1\noid sha256:'+b'a'*64+b'\nsize 999\n')
        r=scan(self.repo,self.commit(),self.base/'out');self.assertTrue(r['git_blob_verification_complete']);self.assertFalse(r['full_payload_inventory_complete'])
    def test_missing_ref_and_output_inside_repository_fail(self):
        self.write('x.go',b'package x\n');sha=self.commit()
        with self.assertRaises(RuntimeError):scan(self.repo,'f'*40,self.base/'a')
        with self.assertRaises(ValueError):scan(self.repo,sha,self.repo/'audit')
    def test_relative_output_inside_repository_fails(self):
        self.write('x.go', b'package x\n'); sha = self.commit()
        relative_out = Path(os.path.relpath(self.repo / 'audit', Path.cwd()))
        with self.assertRaises(ValueError): scan(self.repo, sha, relative_out)

    def test_existing_output_not_overwritten(self):
        self.write('x.go',b'package x\n');sha=self.commit();out=self.base/'out';out.mkdir()
        with self.assertRaises(ValueError):scan(self.repo,sha,out)
    def test_identical_names_but_different_bytes_not_duplicates(self):
        self.write('one/admin.test.ts',b'one\n');self.write('two/admin.test.ts',b'two\n')
        r=scan(self.repo,self.commit(),self.base/'out');self.assertEqual(r['regular_duplicate_groups'],0)
    def test_submodule_reference_not_silently_marked_complete(self):
        self.write('x',b'x');sha=self.commit();self.g('update-index','--add','--cacheinfo',f'160000,{sha},child')
        self.g('commit','-qm','gitlink fixture');r=scan(self.repo,self.g('rev-parse','HEAD'),self.base/'out')
        self.assertFalse(r['full_payload_inventory_complete']);self.assertEqual(len(r['submodules']),1)
if __name__=='__main__':unittest.main()
