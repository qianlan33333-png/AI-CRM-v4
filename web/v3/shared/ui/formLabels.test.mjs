import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
// Exported-only helper needs a tiny entry to exercise its actual code.
const { build } = await import('esbuild');
const result = await build({stdin:{contents:"import {associateVisibleFieldLabels} from './web/v3/shared/ui/formLabels'; window.associate = associateVisibleFieldLabels;",resolveDir:process.cwd()},bundle:true,format:'iife',write:false});
const dom = new JSDOM('<main id="stage"><div><label>名称</label><input id="name"></div><div><label for="explicit">既有标签</label><input id="explicit"></div><div><label>歧义</label><input id="a"><input id="b"></div><div><label>隐藏值</label><input id="hidden" type="hidden"></div><div><label>重复</label><input id="dup"></div><input id="dup"><div><label>说明</label><textarea id="description"></textarea></div></main>',{runScripts:'outside-only',pretendToBeVisual:true});
dom.window.eval(result.outputFiles[0].text);
const d=dom.window.document, stage=d.getElementById('stage');
dom.window.associate(stage);
assert.equal(d.getElementById('name').labels[0].textContent,'名称');
let activations=0;d.getElementById('name').addEventListener('click',()=>activations++);
d.getElementById('name').labels[0].click();assert.equal(activations,1,'label must activate its associated field');
assert.equal(d.getElementById('explicit').labels[0].htmlFor,'explicit');
for(const id of ['a','b','hidden','dup'])assert.equal(d.getElementById(id).labels?.length || 0,0,`${id} must not receive a guessed label`);
assert.equal(d.getElementById('description').labels[0].textContent,'说明');
const before=stage.innerHTML;dom.window.associate(stage);assert.equal(stage.innerHTML,before,'replay mutated labels');
const replacement=d.createElement('div');replacement.innerHTML='<label>重绘名称</label><input id="name">';d.getElementById('name').parentElement.replaceWith(replacement);dom.window.associate(stage);assert.equal(d.getElementById('name').labels[0].textContent,'重绘名称');
dom.window.close();console.log('form-labels: association, native activation, idempotent replay, rerender and ambiguous/hidden/duplicate rejection passed');
