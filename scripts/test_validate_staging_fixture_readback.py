import json,subprocess,tempfile,unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
class FixtureTests(unittest.TestCase):
 def test_requires_facts_and_business_assertion(self):
  with tempfile.TemporaryDirectory() as d:
   p=Path(d);f=p/'fixture.json';r=p/'readback.json';f.write_text((ROOT/'scripts/staging-fixtures/customer-sync-and-research.json').read_text())
   value={'fixture_id':'customer-sync-and-research-v1','synthetic':True,'customer_sync_run_status':'succeeded','customer_projection_count':1,'customer_profile_count':1,'research_material_count':1,'research_mapping_count':1,'business_verified':True};r.write_text(json.dumps(value))
   subprocess.run(['python3',str(ROOT/'scripts/validate-staging-fixture-readback.py'),str(r),'--fixture',str(f)],check=True)
   value['research_material_count']=0;r.write_text(json.dumps(value));self.assertNotEqual(subprocess.run(['python3',str(ROOT/'scripts/validate-staging-fixture-readback.py'),str(r),'--fixture',str(f)],capture_output=True).returncode,0)
if __name__=='__main__':unittest.main()
