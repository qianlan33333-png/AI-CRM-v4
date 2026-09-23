import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import * as esbuild from 'esbuild';

const result = await esbuild.build({
  entryPoints: ['internal/webshell/static_src/admin_console/owner_migration_file.ts'],
  bundle: true,
  format: 'esm',
  platform: 'browser',
  target: 'es2022',
  write: false,
  logLevel: 'silent',
});
const directory = await mkdtemp(join(tmpdir(), 'aicrm-owner-migration-file-'));
try {
  const modulePath = join(directory, 'owner_migration_file.mjs');
  await writeFile(modulePath, result.outputFiles[0].text);
  const { ownerMigrationRowsFromFile, ownerMigrationWorkbookXLSX } = await import(pathToFileURL(modulePath).href);
  const file = (name, value) => new File([value], name);
  const headers = ['external_userid', '是否迁移', '当前负责人userid', '客户备注名', '备注'];
  const expected = [headers, ['legacy-csv', '是', 'legacy-source', 'CSV 客户', '保留旧扩展名']];
  assert.deepEqual(
    await ownerMigrationRowsFromFile(file('legacy-owner-list.xls', `${headers.join(',')}\n${expected[1].join(',')}\n`)),
    expected,
    'a UTF-8 CSV carrying the donor .xls extension remains importable',
  );
  const xlsxExpected = [headers, ['legacy-xlsx', '是', 'legacy-source', 'XLSX 客户', 'PK signature']];
  assert.deepEqual(
    await ownerMigrationRowsFromFile(file('legacy-owner-list.xls', ownerMigrationWorkbookXLSX(headers, [xlsxExpected[1]]))),
    xlsxExpected,
    'a PK-signature XLSX carrying the donor .xls extension remains importable',
  );
  await assert.rejects(
    ownerMigrationRowsFromFile(file('binary.xls', new Uint8Array([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 0x00]))),
    /二进制 BIFF/,
    'actual BIFF must be rejected because the donor did not parse it',
  );
  process.stdout.write('owner-migration-file-contract: PASS\n');
} finally {
  await rm(directory, { recursive: true, force: true });
}
