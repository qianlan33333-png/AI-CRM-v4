import os,subprocess,sys,tempfile,unittest
from pathlib import Path
sys.path.insert(0,str(Path(__file__).parent))
import domestic_main_release as release
class GoPatchTransition(unittest.TestCase):
 def fixture(self,base='1.26.6',selected='1.26.9',declared=None):
  temporary=tempfile.TemporaryDirectory();self.addCleanup(temporary.cleanup);root=Path(temporary.name);policy=root/'policy';candidate=root/'candidate';policy.mkdir();candidate.mkdir()
  for p,v in ((policy,base),(candidate,selected)):(p/'go.mod').write_text('module fixture\n\ngo '+v+'\n')
  (candidate/'.tool-versions').write_text('golang '+(declared or selected)+'\n')
  return policy,candidate
 def test_patch_upgrade_requires_exact_candidate_version(self):
  policy,candidate=self.fixture();self.assertEqual(release._candidate_go_patch_version(policy,candidate),'go1.26.9')
  stub=policy/'scripts/ci';stub.mkdir(parents=True)
  (stub/'quality_lanes.py').write_text("""def exact_version(command,expected):
 import os
 return expected == os.environ.get('ACTUAL_GO','go1.26.9')
def commands(*args):return []
def focused_commands(*args):return []
def run_recorded(*args):return None
def main():
 assert exact_version(['go','version'],'go1.26.6')
 assert not exact_version(['node','--version'],'v24.18.0')
 return 0
""")
  r=subprocess.run([sys.executable,'-c',release._trusted_runner_code(),str(policy),str(candidate)],capture_output=True,text=True);self.assertEqual(r.returncode,0,r.stderr)
  r=subprocess.run([sys.executable,'-c',release._trusted_runner_code(),str(policy),str(candidate)],env=dict(os.environ,ACTUAL_GO='go1.26.6'),capture_output=True,text=True);self.assertNotEqual(r.returncode,0)
 def test_rejects_version_mismatch_downgrade_and_series_change(self):
  for selected,declared in [('1.26.9','1.26.6'),('1.26.5','1.26.5'),('1.27.0','1.27.0')]:
   with self.subTest(selected=selected,declared=declared):
    policy,candidate=self.fixture(selected=selected,declared=declared)
    with self.assertRaises(ValueError):release._candidate_go_patch_version(policy,candidate)
 def test_no_transition_retains_baseline_exact_check(self):
  policy,candidate=self.fixture(selected='1.26.6');self.assertIsNone(release._candidate_go_patch_version(policy,candidate))
 def test_generated_runner_compiles(self):compile(release._trusted_runner_code(),'<runner>','exec')

def load_tests(loader, tests, pattern):
 from test_domestic_release_controller import DomesticMainReleaseTests
 for name in ('test_generated_trusted_runner_is_valid_python','test_trusted_runner_passes_only_lane_and_preflight_arguments','test_trusted_runner_raises_only_complete_backend_suite_timeout_and_receipts_actual_command'):
  tests.addTest(DomesticMainReleaseTests(name))
 return tests
