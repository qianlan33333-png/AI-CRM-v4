import sys,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from contextlib import nullcontext
sys.path.insert(0,str(Path(__file__).parent))
import domestic_main_release as m
import domestic_release_build as b
class MinimalRelease(unittest.TestCase):
 def test_maintenance_entry_is_source_only_but_shared_runtime_is_not(self):
  self.assertFalse(b.classify_paths(['cmd/migrate-wecom-directory-retire/main.go']).runtime_changed)
  self.assertTrue(b.classify_paths(['cmd/migrate-wecom-directory-retire/main.go','internal/wecom/directory.go']).runtime_changed)
 def test_authorized_scope_cannot_absorb_another_head(self):
  state={'in_flight':{'head_sha':'b'*40},'status':'blocked'}
  with tempfile.TemporaryDirectory() as d,patch.object(m.os,'geteuid',return_value=0),patch.object(m,'_locked',return_value=nullcontext()),patch.object(m,'_check_config',side_effect=lambda c:c),patch.object(m,'_load_state',return_value=state),patch.object(m,'promote') as promote:
   c={'state':d+'/state','lock':d+'/lock'}
   with self.assertRaisesRegex(m.ReleaseError,'scope differs'):m.promote_authorized(c,'c'*40,'human command in thread')
   promote.assert_not_called()
 def test_one_authorization_uses_internal_digest_without_another_prompt(self):
  state={'in_flight':{'head_sha':'b'*40,'phase':'source-approval-pending'},'status':'blocked'}
  with tempfile.TemporaryDirectory() as d,patch.object(m.os,'geteuid',return_value=0),patch.object(m,'_locked',return_value=nullcontext()),patch.object(m,'_check_config',side_effect=lambda c:c),patch.object(m,'_load_state',return_value=state),patch.object(m,'_update_state'),patch.object(m,'_approval_wait_result',return_value={'approval_digest':'d'*64}),patch.object(m,'promote',return_value={'status':'completed'}) as promote:
   c={'state':d+'/state','lock':d+'/lock'};self.assertEqual(m.promote_authorized(c,'b'*40,'human command')['status'],'completed');promote.assert_called_once_with(c,'d'*64)
 def test_light_snapshot_never_reads_application_or_remote_host(self):
  state={'status':'ready','main':{},'installed_app':{},'queue':[],'in_flight':None,'batch':None}
  with patch.object(m,'_load_state',return_value=state),patch.object(m,'_verify_stage_app',side_effect=AssertionError()),patch.object(m,'_production_ssh',side_effect=AssertionError()):self.assertEqual(m.status_snapshot({'state':'unused'})['status'],'ready')
