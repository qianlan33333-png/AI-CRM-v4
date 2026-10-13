import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { JSDOM } from 'jsdom';
import qrcode from 'qrcode-generator';

const lock = JSON.parse(readFileSync('package-lock.json', 'utf8'));
const maps = Object.entries(lock.packages).filter(([name]) => name.endsWith('/source-map-js'));
assert.ok(maps.length > 0, 'locked source-map implementation missing');
for (const [name, value] of maps) assert.equal(value.version, '1.2.2', `${name} is not patched`);

// Run untrusted indexed maps in a child so regression cannot stall the suite.
const result = spawnSync(process.execPath, ['--input-type=module', '-e', `
import assert from 'node:assert/strict';
import sourceMap from 'source-map-js';
const {SourceMapConsumer, SourceNode} = sourceMap;
const leaf = {version:3,sources:['fixture.ts'],names:[],mappings:'AAAA',sourcesContent:['safe']};
const indexed = (line,column=0,map=leaf) => ({version:3,sections:[{offset:{line,column},map}]});
for (const value of [-1, 0.5, Infinity, NaN, Number.MAX_SAFE_INTEGER + 1]) {
  assert.throws(() => new SourceMapConsumer(indexed(value)), /offset/i);
  assert.throws(() => new SourceMapConsumer(indexed(0,value)), /offset/i);
}
assert.throws(() => new SourceMapConsumer(indexed(10000001)), /offset/i);
assert.throws(() => new SourceMapConsumer(indexed(6000000,0,indexed(6000000))), /offset/i);
const node = SourceNode.fromStringWithSourceMap('safe', new SourceMapConsumer(indexed(9000000)));
assert.equal(node.toString(), 'safe');
const ordinary = new SourceMapConsumer(leaf);
assert.equal(ordinary.originalPositionFor({line:1,column:0}).source, 'fixture.ts');
assert.equal(SourceNode.fromStringWithSourceMap('safe', ordinary).toString(), 'safe');
`], { encoding: 'utf8', timeout: 3000, maxBuffer: 65536 });
assert.ifError(result.error);
assert.equal(result.status, 0, result.stderr);

// jsdom's CSS parser is the direct affected development dependency chain.
const dom = new JSDOM('<style>input { color: rgb(1, 2, 3); }</style><label for="fixture">商品名称</label><input id="fixture">');
assert.equal(dom.window.document.querySelector('input').labels[0].textContent, '商品名称');
assert.equal(dom.window.getComputedStyle(dom.window.document.querySelector('input')).color, 'rgb(1, 2, 3)');
dom.window.close();
const qr = qrcode(0, 'M');
qr.addData('https://fixture.invalid/receipt');
qr.make();
assert.match(qr.createSvgTag(), /<svg\b/);
console.log('dependency-security: locked patch, indexed-map rejection/bounded conversion, DOM/CSS and QR contracts passed');
