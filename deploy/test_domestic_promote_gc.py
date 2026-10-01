"""Deletion contracts: current/previous/live references and unknown contents."""
import importlib.util
import os
import tempfile
import unittest
from contextlib import nullcontext
from pathlib import Path
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('package_gc',Path(__file__).with_name('domestic-promote.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class PackageGC(unittest.TestCase):
 def test_current_previous_and_unknown_are_retained(self):
  with tempfile.TemporaryDirectory() as d:
   r=Path(d);releases=r/'releases';releases.mkdir();names=[f'{i:040x}' for i in range(1,5)]
   for n in names:(releases/n).mkdir()
   (releases/names[3]/'uploads').mkdir()
   (releases/'unexpected').mkdir();(releases/'linked').symlink_to(r)
   receipt={'previous_sha':names[1]};cursor={'installed_app_sha':names[0]}
   with patch.object(m,'RELEASES',releases),patch.object(m,'require_host_role'),patch.object(m,'_production_release_lock',return_value=nullcontext()),patch.object(m,'_read_main_state',return_value=cursor),patch.object(m,'_reverify_main_cursor',return_value=(cursor,receipt)),patch.object(m,'current_sha',return_value=names[0]),patch.object(m,'readiness'):
    result=m.reclaim_obsolete_releases()
   self.assertEqual(result['removed'],[names[2]])
   self.assertTrue((releases/names[0]).exists());self.assertTrue((releases/names[1]).exists());self.assertTrue((releases/names[3]).exists());self.assertTrue((releases/'linked').is_symlink())
 def test_cursor_mismatch_never_deletes(self):
  with patch.object(m,'require_host_role'),patch.object(m,'_production_release_lock',return_value=nullcontext()),patch.object(m,'_read_main_state',return_value={'installed_app_sha':'a'*40}),patch.object(m,'_reverify_main_cursor',return_value=({},{})),patch.object(m,'current_sha',return_value='b'*40),patch.object(m.shutil,'rmtree') as rm:
   with self.assertRaisesRegex(RuntimeError,'differs'):m.reclaim_obsolete_releases()
   rm.assert_not_called()
