import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import test from 'node:test';
import { applyMaterialization, verifySourceIndex, verifyMaterialization, DonorViewError } from './donor-source-views.mjs';
import { assertOperationIds } from './check-operation-ids.mjs';

const require=createRequire(import.meta.url),yaml=require('js-yaml');
test('fresh exact V4 Git checkout materializes the same OpenAPI with stable IDs and rejects drift',()=>{
  const repository=execFileSync('git',['rev-parse','--show-toplevel'],{encoding:'utf8'}).trim();
  const head=execFileSync('git',['rev-parse','HEAD'],{encoding:'utf8'}).trim();
  const directory=fs.mkdtempSync(path.join(os.tmpdir(),'aicrm-openapi-fresh-'));
  const root=path.join(directory,'source');
  try{
    execFileSync('git',['clone','--shared','--no-checkout',repository,root],{stdio:'pipe',timeout:60000});
    execFileSync('git',['-C',root,'checkout','--detach',head],{stdio:'pipe',timeout:60000});
    assert.equal(execFileSync('git',['-C',root,'status','--porcelain'],{encoding:'utf8'}),'','fresh candidate must be clean');
    const view=path.join(root,'internal/config/http/openapi.yaml');
    assert.equal(fs.existsSync(view),false,'exact checkout unexpectedly reused an old generated embed');
    verifySourceIndex(root);
    const applied=applyMaterialization(root);
    assert.ok(applied.created.includes('internal/config/http/openapi.yaml'),'API embed was not freshly created');
    verifyMaterialization(root);
    const canonical=fs.readFileSync(path.join(root,'api/openapi.yaml'));
    assert.deepEqual(fs.readFileSync(view),canonical,'embedded contract differs from current canonical API');
    const specification=yaml.load(canonical.toString('utf8'));
    assert.ok(assertOperationIds(specification)>0,'real operations must have unique stable IDs');
    assert.equal(specification.paths['/my/orders'].get.operationId,'getCustomerOrderCenterPage');
    assert.equal(specification.paths['/api/h5/me/invoices'].post.operationId,'applyCustomerInvoice');
    fs.appendFileSync(path.join(root,'api/openapi.yaml'),'\n# unregistered canonical drift\n');
    assert.throws(()=>verifySourceIndex(root),error=>error instanceof DonorViewError && error.code.startsWith('CANONICAL_'));
    fs.writeFileSync(path.join(root,'api/openapi.yaml'),canonical);
    fs.chmodSync(view,0o644);fs.appendFileSync(view,'\n# stale embedded view\n');
    assert.throws(()=>verifyMaterialization(root),error=>error instanceof DonorViewError);
    console.log(`fresh-openapi: source_sha=${head}; canonical/embed equality, stable IDs, canonical/view drift rejection passed`);
  }finally{
    fs.rmSync(directory,{recursive:true,force:true});
  }
});
