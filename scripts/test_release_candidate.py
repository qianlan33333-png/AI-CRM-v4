import json, subprocess, tempfile, unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
SCRIPT=ROOT/'scripts/release_candidate.py'
class CandidateTests(unittest.TestCase):
 def test_preview_has_merge_tree_and_modules(self):
  with tempfile.TemporaryDirectory() as t:
   out=Path(t)/'candidate.json'; subprocess.run(['python3',str(SCRIPT),'preview','--base','origin/main','--head','HEAD','--pr','0','--out',str(out)],check=True)
   v=json.loads(out.read_text()); self.assertEqual(v['schema'],1); self.assertEqual(len(v['tree_sha']),40); self.assertEqual(len(v['merge_preview_sha']),40)
 def test_stale_main_rejected(self):
  with tempfile.TemporaryDirectory() as t:
   out=Path(t)/'candidate.json'; subprocess.run(['python3',str(SCRIPT),'preview','--base','origin/main','--head','HEAD','--pr','0','--out',str(out)],check=True)
   value=json.loads(out.read_text()); value['base_main_sha']='0'*40; out.write_text(json.dumps(value)); self.assertNotEqual(subprocess.run(['python3',str(SCRIPT),'validate',str(out),'--main-sha','HEAD'],capture_output=True).returncode,0)
if __name__=='__main__': unittest.main()
