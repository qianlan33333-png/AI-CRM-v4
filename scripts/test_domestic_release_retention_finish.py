import copy
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parent))
import domestic_main_release as m
import test_domestic_release_controller as original


class RetentionFinishTests(unittest.TestCase):
    def run_finish(self, stage=None, production=None, state_error=False):
        complete={'status':'completed','remaining_candidate_count':0,'removed':[]}
        stage=json.dumps(complete) if stage is None else stage
        production=json.dumps(complete) if production is None else production
        state={'last_release':{'package_cleanup':{'status':'pending'},'stage_package_cleanup':{'status':'pending'}}}
        writes=[]
        def update(_path,value,**changes):
            if state_error and len(writes)==1: raise OSError('synthetic receipt write failed')
            value.update(changes);writes.append(copy.deepcopy(value))
        with patch.object(m,'_reclaim_completed_build_copies',return_value={'status':'completed','retained':[]}), patch.object(m,'_update_state',side_effect=update), patch.object(m.legacy,'command',return_value=stage) as local, patch.object(m,'_production_ssh',return_value=production) as remote:
            result=m._finish_success_cleanup({'stage_helper':'/stage-helper','prod_helper':'/prod-helper'},Path('/synthetic-state'),state)
        return result,state,writes,local,remote

    def test_two_hosts_complete_only_with_zero_candidates(self):
        result,state,writes,local,remote=self.run_finish()
        self.assertEqual(result['status'],'completed')
        self.assertEqual(state['last_release']['stage_package_cleanup']['remaining_candidate_count'],0)
        self.assertEqual(json.loads(state['last_release']['package_cleanup'])['remaining_candidate_count'],0)
        local.assert_called_once_with('sudo','-n','/stage-helper','--reclaim-obsolete-releases',timeout=120)
        self.assertEqual(remote.call_args.args[1:],('sudo','-n','/prod-helper','--reclaim-obsolete-releases'))
        self.assertEqual(len(writes),3)

    def test_stage_failed_unknown_or_nonzero_stops_production_cleanup(self):
        for value in ('invalid-json',json.dumps({'status':'maintenance-pending'}),json.dumps({'status':'completed'}),json.dumps({'status':'completed','remaining_candidate_count':1}),json.dumps({'status':'completed','remaining_candidate_count':False})):
            with self.subTest(value=value):
                result,_,_,_,remote=self.run_finish(stage=value)
                self.assertEqual(result['status'],'readback_required');remote.assert_not_called()

    def test_production_failed_unknown_or_nonzero_never_claims_completed(self):
        for value in ('invalid-json',json.dumps({'status':'maintenance-pending'}),json.dumps({'status':'completed','remaining_candidate_count':2})):
            with self.subTest(value=value):
                result,_,_,_,remote=self.run_finish(production=value)
                self.assertEqual(result['status'],'readback_required');remote.assert_called_once()

    def test_stage_receipt_write_failure_stops_more_cleanup(self):
        result,state,_,_,remote=self.run_finish(state_error=True)
        self.assertEqual(result['status'],'readback_required')
        self.assertEqual(state['last_release']['stage_package_cleanup'],{'status':'pending'})
        remote.assert_not_called()

    def test_confirmed_publication_survives_cleanup_failure_and_unknown_write(self):
        original.DomesticMainReleaseTests('test_finalizer_cleanup_cannot_reclassify_confirmed_success').test_finalizer_cleanup_cannot_reclassify_confirmed_success()


if __name__=='__main__': unittest.main()
