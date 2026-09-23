import { unzipSync, zipSync, strToU8 } from 'fflate';
import { readSheet } from 'read-excel-file/browser';
import { DOMParser, type Document as XmlDocument, type Element as XmlElement } from '@xmldom/xmldom';

const MAX_FILE_SIZE = 1024 * 1024;
const MAX_ZIP_ENTRIES = 128;
const MAX_ZIP_ENTRY_SIZE = 4 * MAX_FILE_SIZE;
const MAX_ZIP_TOTAL_SIZE = 16 * MAX_FILE_SIZE;
const XML_DECODER = new TextDecoder();
const UTF8_DECODER = new TextDecoder("utf-8", { fatal: true });
const ZIP_MAGIC = [0x50, 0x4b];
const BIFF_MAGIC = [0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1];
const SPREADSHEET_NS = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main';
const OFFICE_RELATIONSHIP_NS = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
const PACKAGE_RELATIONSHIP_NS = 'http://schemas.openxmlformats.org/package/2006/relationships';

const nonEmpty = (value: unknown): boolean => value !== null && value !== undefined && String(value).trim() !== '';
const blankRow = (row: unknown[]): boolean => row.every(value => !nonEmpty(value));
const parseXML = (xml: string): XmlDocument => {
  const document = new DOMParser({ locator: false, onError: () => { throw new Error('invalid XML'); } }).parseFromString(xml, 'application/xml');
  if (!document.documentElement) throw new Error('invalid XML');
  return document;
};
const firstElement = (parent: XmlElement, localName: string, namespaceURI?: string): XmlElement | undefined => {
  for (let child = parent.firstChild; child; child = child.nextSibling) {
    if (child.nodeType === 1 && ((child as XmlElement).localName || child.nodeName) === localName && (namespaceURI === undefined || (child as XmlElement).namespaceURI === namespaceURI)) return child as XmlElement;
  }
  return undefined;
};
const hasFormula = (element: XmlElement): boolean => {
  if ((element.localName || element.nodeName) === 'f') return true;
  for (let child = element.firstChild; child; child = child.nextSibling) if (child.nodeType === 1 && hasFormula(child as XmlElement)) return true;
  return false;
};
const resolveZipPath = (target: string): string => {
  const parts: string[] = [];
  const path = target.startsWith('/') ? target.slice(1) : `xl/${target}`;
  for (const part of path.split('/')) {
    if (!part || part === '.') continue;
    if (part === '..') { if (!parts.length) throw new Error('invalid worksheet relationship'); parts.pop(); } else parts.push(part);
  }
  const resolved = parts.join('/');
  if (!resolved.startsWith('xl/')) throw new Error('invalid worksheet relationship');
  return resolved;
};
const firstWorksheetPath = (archive: Record<string, Uint8Array>): string => {
  const workbookRoot = parseXML(XML_DECODER.decode(archive['xl/workbook.xml'])).documentElement;
  if (!workbookRoot || workbookRoot.localName !== 'workbook' || workbookRoot.namespaceURI !== SPREADSHEET_NS) throw new Error('missing workbook');
  const sheets = firstElement(workbookRoot, 'sheets', SPREADSHEET_NS);
  const firstSheet = sheets ? firstElement(sheets, 'sheet', SPREADSHEET_NS) : undefined;
  const relationshipID = firstSheet?.getAttributeNS(OFFICE_RELATIONSHIP_NS, 'id') || firstSheet?.getAttribute('r:id');
  if (!relationshipID) throw new Error('missing first worksheet relationship');
  const relationshipsRoot = parseXML(XML_DECODER.decode(archive['xl/_rels/workbook.xml.rels'])).documentElement;
  if (!relationshipsRoot || relationshipsRoot.localName !== 'Relationships' || relationshipsRoot.namespaceURI !== PACKAGE_RELATIONSHIP_NS) throw new Error('missing worksheet relationships');
  const relationship = [...relationshipsRoot.childNodes].find(child => child.nodeType === 1 && (child as XmlElement).localName === 'Relationship' && (child as XmlElement).namespaceURI === PACKAGE_RELATIONSHIP_NS && (child as XmlElement).getAttribute('Id') === relationshipID) as XmlElement | undefined;
  const target = relationship?.getAttribute('Target');
  if (!target) throw new Error('missing first worksheet');
  return resolveZipPath(target);
};
const rejectFormulas = async (file: File): Promise<void> => {
  let entries = 0;
  let total = 0;
  const archive = unzipSync(new Uint8Array(await file.arrayBuffer()), { filter: ({ name, originalSize }) => {
    entries += 1;
    if (entries > MAX_ZIP_ENTRIES || !Number.isSafeInteger(originalSize) || originalSize < 0 || originalSize > MAX_ZIP_ENTRY_SIZE || total > MAX_ZIP_TOTAL_SIZE - originalSize) throw new Error('Excel ZIP 解压大小超出限制');
    total += originalSize;
    return name.endsWith('.xml') || name.endsWith('.xml.rels');
  } });
  const worksheet = archive[firstWorksheetPath(archive)];
  if (!worksheet || hasFormula(parseXML(XML_DECODER.decode(worksheet)).documentElement as XmlElement)) throw new Error('Excel 文件不能包含公式');
};

const csvRows = (source: string): string[][] => {
  const rows: string[][] = []; let row: string[] = []; let cell = ''; let quoted = false;
  const data = source.replace(/^\uFEFF/, '');
  for (let index = 0; index < data.length; index += 1) {
    const char = data[index];
    if (quoted) {
      if (char === '"' && data[index + 1] === '"') { cell += '"'; index += 1; continue; }
      if (char === '"') { quoted = false; continue; }
      cell += char; continue;
    }
    if (char === '"') { if (cell) throw new Error('CSV 引号位置无效'); quoted = true; }
    else if (char === ',') { row.push(cell.trim()); cell = ''; }
    else if (char === '\r' || char === '\n') { if (char === '\r' && data[index + 1] === '\n') index += 1; row.push(cell.trim()); cell = ''; if (!blankRow(row)) rows.push(row); row = []; }
    else cell += char;
  }
  if (quoted) throw new Error('CSV 引号未闭合');
  if (cell || row.length) { row.push(cell.trim()); if (!blankRow(row)) rows.push(row); }
  return rows;
};

const startsWith = (value: Uint8Array, magic: number[]): boolean => magic.every((byte, index) => value[index] === byte);
const decodedCSV = async (file: File): Promise<string[][]> => {
  try { return csvRows(UTF8_DECODER.decode(await file.arrayBuffer())); } catch (error) {
    if (error instanceof TypeError) throw new Error("CSV 文件必须是 UTF-8 编码");
    throw error;
  }
};

export async function ownerMigrationRowsFromFile(file: File): Promise<string[][]> {
  const filename = file.name.toLowerCase();
  if (file.size > MAX_FILE_SIZE) throw new Error("上传文件不能超过 1 MiB");
  if (!filename.endsWith(".xlsx") && !filename.endsWith(".xls") && !filename.endsWith(".csv")) throw new Error("仅支持 CSV、XLSX 或旧扩展名 XLS 文件");
  const prefix = new Uint8Array(await file.slice(0, 8).arrayBuffer());
  const xlsx = filename.endsWith(".xlsx") || (filename.endsWith(".xls") && startsWith(prefix, ZIP_MAGIC));
  if (!xlsx) {
    if (filename.endsWith(".xls") && startsWith(prefix, BIFF_MAGIC)) throw new Error("不支持二进制 BIFF .xls，请另存为 CSV 或 XLSX");
    return decodedCSV(file);
  }
  let rows: unknown[][];
  try { await rejectFormulas(file); rows = await readSheet(file, { trim: false }) as unknown[][]; } catch (error) {
    if (error instanceof Error && error.message === "Excel 文件不能包含公式") throw error;
    throw new Error("Excel 文件无法解析");
  }
  if (!rows.length) throw new Error("Excel 第一张工作表不能为空");
  return rows.filter(row => !blankRow(row)).map(row => row.map(value => value === null || value === undefined ? "" : String(value).trim()));
}

const xmlText = (value: string): string => value.replace(/[&<>'"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&apos;', '"': '&quot;' }[char] || char));
const column = (index: number): string => { let value = index + 1; let out = ''; while (value) { const remainder = (value - 1) % 26; out = String.fromCharCode(65 + remainder) + out; value = Math.floor((value - 1) / 26); } return out; };

export function ownerMigrationWorkbookXLSX(headers: string[], rows: string[][]): Uint8Array {
  const cells = (values: string[], row: number) => values.map((value, index) => `<c r="${column(index)}${row}" t="inlineStr"><is><t>${xmlText(value)}</t></is></c>`).join('');
  const sheetRows = [headers, ...rows].map((values, index) => `<row r="${index + 1}">${cells(values, index + 1)}</row>`).join('');
  const sheet = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>${sheetRows}</sheetData></worksheet>`;
  return zipSync({
    '[Content_Types].xml': strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>'),
    '_rels/.rels': strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>'),
    'xl/workbook.xml': strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="owner_migration" sheetId="1" r:id="rId1"/></sheets></workbook>'),
    'xl/_rels/workbook.xml.rels': strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>'),
    'xl/worksheets/sheet1.xml': strToU8(sheet),
  });
}

export function ownerMigrationTemplateXLSX(): Uint8Array { return ownerMigrationWorkbookXLSX(['external_userid', '是否迁移', '当前负责人userid', '客户备注名', '备注'], []); }
