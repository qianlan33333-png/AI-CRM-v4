import unittest
from release_queue import change
class QueueTests(unittest.TestCase):
 def setUp(self):
  self.q={'items':[]}
  for cid in ['a','b']:change(self.q,'enqueue',manifest={'candidate_id':cid,'base_main_sha':'base'})
 def transition(self,cid,status,main='base'):change(self.q,'transition',candidate_id=cid,status=status,main=main)
 def test_fifo(self):
  with self.assertRaises(ValueError):self.transition('b','preview_building')
 def test_single_owner(self):
  self.transition('a','preview_building')
  with self.assertRaises(ValueError):self.transition('b','preview_building')
 def test_no_skipping(self):
  with self.assertRaises(ValueError):self.transition('a','production')
 def test_stale_main(self):
  with self.assertRaises(ValueError):self.transition('a','preview_building','changed')
  self.transition('a','stale_candidate');self.transition('b','preview_building')
 def test_adopt_accepted_is_waiting_merge(self):
  import json, subprocess, tempfile
  with tempfile.TemporaryDirectory() as d:
   receipt=__import__('pathlib').Path(d)/'r.json'; queue=__import__('pathlib').Path(d)/'q.json'
   receipt.write_text(json.dumps({'candidate_id':'x','status':'accepted'}))
   subprocess.run(['python3',str(__import__('pathlib').Path(__file__).parent/'release_queue.py'),'--file',str(queue),'adopt-accepted',str(receipt)],check=True)
   self.assertEqual(json.loads(queue.read_text())['items'][0]['status'],'waiting_merge')
 def test_merged_keeps_ownership(self):
  for s in ['preview_building','staging_acceptance','frozen','waiting_merge','merged']:self.transition('a',s)
  self.transition('b','preview_building')
  self.transition('b','staging_acceptance')
  with self.assertRaisesRegex(ValueError,'owns'):self.transition('b','frozen')
 def test_observing_allows_staging_but_not_promotion(self):
  for s in ['preview_building','staging_acceptance','frozen','waiting_merge','merged','production','observing']:self.transition('a',s)
  self.transition('b','preview_building')
  self.transition('b','staging_acceptance')
  with self.assertRaisesRegex(ValueError,'owns'):self.transition('b','frozen')
  self.assertEqual(self.q['items'][0]['status'],'observing')
 def test_adopt_accepted_does_not_bypass_production_owner(self):
  import json, subprocess, tempfile
  from pathlib import Path
  with tempfile.TemporaryDirectory() as d:
   root=Path(d); receipt=root/'r.json'; queue=root/'q.json'
   receipt.write_text(json.dumps({'candidate_id':'new','status':'accepted'}))
   queue.write_text(json.dumps({'schema':1,'items':[{'candidate_id':'old','status':'observing'}]}))
   result=subprocess.run(['python3',str(Path(__file__).parent/'release_queue.py'),'--file',str(queue),'adopt-accepted',str(receipt)],capture_output=True,text=True)
   self.assertNotEqual(result.returncode,0)
   self.assertEqual(len(json.loads(queue.read_text())['items']),1)
if __name__=='__main__':unittest.main()
