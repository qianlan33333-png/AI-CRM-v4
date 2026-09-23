import { build } from 'esbuild';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { zipSync } from 'fflate';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-admin-adapter-'));
await build({ entryPoints: { 'automationHistory.test': path.join(root, 'src/api/automationHistory.test.ts'), 'memberGridHistory.test': path.join(root, 'src/api/memberGridHistory.test.ts'), 'messageHistory.test': path.join(root, 'src/api/messageHistory.test.ts'), 'contactHistory.test': path.join(root, 'src/api/contactHistory.test.ts'), 'audienceHistory.test': path.join(root, 'src/api/audienceHistory.test.ts'), 'admin.test': path.join(root, 'src/api/admin.test.ts'), 'external_effects.test': path.join(root, 'src/api/external_effects.test.ts'), 'push_observability.test': path.join(root, 'src/api/push_observability.test.ts'), 'servicePeriodHistory.test': path.join(root, 'src/api/servicePeriodHistory.test.ts'), 'couponHistory.test': path.join(root, 'src/api/couponHistory.test.ts'), 'groupOpsHistory.test': path.join(root, 'src/api/groupOpsHistory.test.ts'), 'groupOpsDirectory.test': path.join(root, 'src/api/groupOpsDirectory.test.ts'), 'wecomAcquisitionLinks.test': path.join(root, 'src/api/wecomAcquisitionLinks.test.ts'), 'orvalBlobFetch.test': path.join(root, 'src/api/orvalBlobFetch.test.ts'), ownerReassignmentFile: path.join(root, 'src/admin/ownerReassignmentFile.ts') }, bundle: true, platform: 'node', format: 'esm', outdir, logLevel: 'warning' });
try {
  await build({ entryPoints: [path.join(root, 'src/api/campaignHistory.test.ts')], bundle: true, platform: 'node', format: 'esm', outdir, logLevel: 'warning' });
  await (await import(pathToFileURL(path.join(outdir, 'campaignHistory.test.js')).href)).runCampaignHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'groupOpsHistory.test.js')).href)).runGroupOpsHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'groupOpsDirectory.test.js')).href)).runGroupOpsDirectoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'messageHistory.test.js')).href)).runMessageHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'admin.test.js')).href)).runAdminAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'audienceHistory.test.js')).href)).runAudienceHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'external_effects.test.js')).href)).runExternalEffectsAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'push_observability.test.js')).href)).runPushObservabilityAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'servicePeriodHistory.test.js')).href)).runServicePeriodHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'couponHistory.test.js')).href)).runCouponHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'memberGridHistory.test.js')).href)).runMemberGridHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'contactHistory.test.js')).href)).runContactHistoryAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'wecomAcquisitionLinks.test.js')).href)).runWeComAcquisitionLinksAdapterTests();
  await (await import(pathToFileURL(path.join(outdir, 'orvalBlobFetch.test.js')).href)).runOrvalBlobFetchTests();
  await build({ entryPoints: { imageContract: path.join(root, 'src/api/admin.ts') }, bundle: true, platform: 'node', format: 'esm', outdir, logLevel: 'warning' });
  const { imagePageDto } = await import(pathToFileURL(path.join(outdir, 'imageContract.js')).href);
  const image = imagePageDto({ id: 7, original_url: '/api/admin/image-library/7/variants/original', thumb_320_url: '/api/admin/image-library/7/variants/thumb_320' });
  if (image.resourceId !== '7' || image.originalUrl !== '/api/admin/image-library/7/variants/original' || image.thumbnailUrl !== '/api/admin/image-library/7/variants/thumb_320') throw new Error('image adapter did not preserve validated original and thumbnail URLs');
  let mismatchedImageRejected = false;
  try {
    imagePageDto({ id: 7, original_url: '/api/admin/image-library/7/variants/original', thumb_320_url: '/api/admin/image-library/8/variants/thumb_320' });
  } catch (error) {
    mismatchedImageRejected = error instanceof Error && error.name === 'ImageMappingError' && error.message === 'imagePageDto: thumb_320_url must exactly match /api/admin/image-library/7/variants/thumb_320';
  }
  if (!mismatchedImageRejected) throw new Error('image adapter accepted mismatched image URLs');
  const pickerSource = fs.readFileSync(path.join(root, 'src/shared/ui/picker.ts'), 'utf8');
  if (!pickerSource.includes("c.status === 'active'")) throw new Error('channel picker does not require the raw active status');
  if (!pickerSource.includes('Number.isSafeInteger(c.resourceId)') || !pickerSource.includes('c.resourceId > 0')) throw new Error('channel picker does not validate a positive numeric resourceId');
  if (!pickerSource.includes('id: String(c.resourceId)')) throw new Error('channel picker does not use resourceId as the selected ID');
  if (pickerSource.includes("c.status === '启用'") || pickerSource.includes('id: c.code')) throw new Error('channel picker retained legacy status/code selection');
  if (!pickerSource.includes('url: m.originalUrl') || !pickerSource.includes('id: String(m.resourceId)')) throw new Error('image picker does not return resourceId and validated originalUrl');
  const { ownerReassignmentCsvFromFile } = await import(pathToFileURL(path.join(outdir, 'ownerReassignmentFile.js')).href);
  const fixture = (name) => {
    const xlsx = new Blob([fs.readFileSync(path.join(root, 'src/admin/fixtures', name))], { type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' });
    Object.defineProperty(xlsx, 'name', { value: name });
    return xlsx;
  };
  const csv = await ownerReassignmentCsvFromFile(fixture('owner-reassignment-valid.xlsx'));
  if (csv !== 'customer_id,expected_owner_staff_id,expected_updated_at,target_owner_staff_id\n7,3,2026-08-25T00:00:00Z,9\n') throw new Error('owner reassignment XLSX fixture did not normalize to the preview CSV contract');
  const blankRowCsv = await ownerReassignmentCsvFromFile(fixture('owner-reassignment-blank-row.xlsx'));
  if (blankRowCsv !== csv) throw new Error('owner reassignment XLSX blank row was not ignored');
  const expectRejected = async (name, message) => {
    try {
      await ownerReassignmentCsvFromFile(fixture(name));
    } catch (error) {
      if (error instanceof Error && error.message === message) return;
      throw new Error(`${name} rejected with an unexpected error`);
    }
    throw new Error(`${name} was unexpectedly accepted`);
  };
  const expectRejectedFile = async (file, message) => {
    try {
      await ownerReassignmentCsvFromFile(file);
    } catch (error) {
      if (error instanceof Error && error.message === message) return;
      throw new Error(`${file.name} rejected with an unexpected error`);
    }
    throw new Error(`${file.name} was unexpectedly accepted`);
  };
  await expectRejected('owner-reassignment-formula.xlsx', 'Excel 文件不能包含公式');
  await expectRejected('owner-reassignment-unsafe-id.xlsx', 'Excel 第 2 行的 ID 必须是安全整数');
  await expectRejected('owner-reassignment-strict-header.xlsx', 'Excel 第一行必须且只能是：customer_id,expected_owner_staff_id,expected_updated_at,target_owner_staff_id');

  const encode = (value) => new TextEncoder().encode(value);
  const spreadsheetNamespace = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main';
  const officeRelationshipNamespace = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
  const packageRelationshipNamespace = 'http://schemas.openxmlformats.org/package/2006/relationships';
  const worksheetRelationshipType = `${officeRelationshipNamespace}/worksheet`;
  const ownerWorksheet = (formula) => `<worksheet xmlns="${spreadsheetNamespace}"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>customer_id</t></is></c><c r="B1" t="inlineStr"><is><t>expected_owner_staff_id</t></is></c><c r="C1" t="inlineStr"><is><t>expected_updated_at</t></is></c><c r="D1" t="inlineStr"><is><t>target_owner_staff_id</t></is></c></row><row r="2"><c r="A2">${formula ? '<f>1+1</f><v>7</v>' : '<v>7</v>'}</c><c r="B2" t="n"><v>3</v></c><c r="C2" t="inlineStr"><is><t>2026-08-25T00:00:00Z</t></is></c><c r="D2" t="n"><v>9</v></c></row></sheetData></worksheet>`;
  const generatedXlsx = ({ prefix = '', firstFormula = false, secondFormula = false, paddedWorkbook = '' } = {}) => {
    const workbook = `<workbook xmlns="${spreadsheetNamespace}" xmlns:r="${officeRelationshipNamespace}">${prefix}<sheets><sheet name="First" sheetId="1" state="visible" r:id="rFirst"/><sheet name="Second" sheetId="2" state="visible" r:id="rSecond"/></sheets>${paddedWorkbook}</workbook>`;
    const relationships = `<Relationships xmlns="${packageRelationshipNamespace}"><Relationship Id="rFirst" Type="${worksheetRelationshipType}" Target="worksheets/first.xml"/><Relationship Id="rSecond" Type="${worksheetRelationshipType}" Target="worksheets/second.xml"/></Relationships>`;
    const bytes = zipSync({
      'xl/workbook.xml': encode(workbook),
      'xl/_rels/workbook.xml.rels': encode(relationships),
      'xl/worksheets/first.xml': encode(ownerWorksheet(firstFormula)),
      'xl/worksheets/second.xml': encode(ownerWorksheet(secondFormula)),
    });
    const file = new Blob([bytes], { type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' });
    Object.defineProperty(file, 'name', { value: 'generated-owner-reassignment.xlsx' });
    return file;
  };
  await expectRejectedFile(generatedXlsx({ prefix: '<!-- <sheet name="fake" r:id="rSecond"/> -->', firstFormula: true }), 'Excel 文件不能包含公式');
  await expectRejectedFile(generatedXlsx({ prefix: '<![CDATA[<sheet name="fake" r:id="rSecond"/>]]>', firstFormula: true }), 'Excel 文件不能包含公式');
  const secondSheetFormula = generatedXlsx({ secondFormula: true });
  const secondSheetFormulaCsv = await ownerReassignmentCsvFromFile(secondSheetFormula);
  if (secondSheetFormulaCsv !== 'customer_id,expected_owner_staff_id,expected_updated_at,target_owner_staff_id\n7,3,2026-08-25T00:00:00Z,9\n') throw new Error('formula on a non-first worksheet must not block the first-sheet import');
  const paddedWorkbook = generatedXlsx({ paddedWorkbook: `<!--${'x'.repeat(4 * 1024 * 1024 + 1)}-->` });
  if (paddedWorkbook.size >= 1024 * 1024) throw new Error('inflated workbook test archive must stay below the compressed file limit');
  await expectRejectedFile(paddedWorkbook, 'Excel 文件无法解析');
  console.log('admin-adapter-contract: PASS');
} finally { fs.rmSync(outdir, { recursive: true, force: true }); }
