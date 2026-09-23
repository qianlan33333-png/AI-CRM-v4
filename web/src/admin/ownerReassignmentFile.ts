import { unzipSync } from 'fflate';
import { readSheet } from 'read-excel-file/browser';
import { DOMParser, type Document as XmlDocument, type Element as XmlElement } from '@xmldom/xmldom';

const MAX_FILE_SIZE = 1024 * 1024;
const MAX_ZIP_ENTRIES = 128;
const MAX_ZIP_ENTRY_SIZE = 4 * MAX_FILE_SIZE;
const MAX_ZIP_TOTAL_SIZE = 16 * MAX_FILE_SIZE;
const COLUMNS = ['customer_id', 'expected_owner_staff_id', 'expected_updated_at', 'target_owner_staff_id'];
const ID_COLUMNS = [0, 1, 3];
const XML_DECODER = new TextDecoder();
const SPREADSHEET_NS = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main';
const OFFICE_RELATIONSHIP_NS = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
const PACKAGE_RELATIONSHIP_NS = 'http://schemas.openxmlformats.org/package/2006/relationships';

const csvCell = (value: unknown): string => {
  const text = value === null ? '' : value instanceof Date ? value.toISOString() : String(value);
  return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
};

const nonEmpty = (value: unknown): boolean => value !== null && value !== undefined && String(value).trim() !== '';
const blankRow = (row: unknown[]): boolean => row.every((value) => !nonEmpty(value));

const parseXml = (xml: string): XmlDocument => {
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

const hasElement = (element: XmlElement, localName: string): boolean => {
  if ((element.localName || element.nodeName) === localName) return true;
  for (let child = element.firstChild; child; child = child.nextSibling) {
    if (child.nodeType === 1 && hasElement(child as XmlElement, localName)) return true;
  }
  return false;
};

const resolveZipPath = (target: string): string => {
  const parts: string[] = [];
  const path = target.startsWith('/') ? target.slice(1) : `xl/${target}`;
  for (const part of path.split('/')) {
    if (!part || part === '.') continue;
    if (part === '..') {
      if (!parts.length) throw new Error('invalid worksheet relationship');
      parts.pop();
    } else {
      parts.push(part);
    }
  }
  const resolved = parts.join('/');
  if (!resolved.startsWith('xl/')) throw new Error('invalid worksheet relationship');
  return resolved;
};

const firstWorksheetPath = (archive: Record<string, Uint8Array>): string => {
  const workbook = parseXml(XML_DECODER.decode(archive['xl/workbook.xml']));
  const workbookRoot = workbook.documentElement;
  if (!workbookRoot || workbookRoot.localName !== 'workbook' || workbookRoot.namespaceURI !== SPREADSHEET_NS) throw new Error('missing workbook');
  const sheets = firstElement(workbookRoot, 'sheets', SPREADSHEET_NS);
  const firstSheet = sheets ? firstElement(sheets, 'sheet', SPREADSHEET_NS) : undefined;
  const relationshipId = firstSheet?.getAttributeNS(OFFICE_RELATIONSHIP_NS, 'id') || firstSheet?.getAttribute('r:id');
  if (!relationshipId) throw new Error('missing first worksheet relationship');

  const relationships = parseXml(XML_DECODER.decode(archive['xl/_rels/workbook.xml.rels']));
  const relationshipsRoot = relationships.documentElement;
  if (!relationshipsRoot || relationshipsRoot.localName !== 'Relationships' || relationshipsRoot.namespaceURI !== PACKAGE_RELATIONSHIP_NS) throw new Error('missing worksheet relationships');
  const relationship = [...relationshipsRoot.childNodes]
    .find((child) => child.nodeType === 1 && (child as XmlElement).localName === 'Relationship' && (child as XmlElement).namespaceURI === PACKAGE_RELATIONSHIP_NS && (child as XmlElement).getAttribute('Id') === relationshipId) as XmlElement | undefined;
  const target = relationship?.getAttribute('Target');
  if (!target) throw new Error('missing first worksheet');
  return resolveZipPath(target);
};

const unzipArchive = (data: Uint8Array): Record<string, Uint8Array> => {
  let entryCount = 0;
  let totalOriginalSize = 0;
  return unzipSync(data, {
    filter: ({ name, originalSize }) => {
      entryCount += 1;
      if (entryCount > MAX_ZIP_ENTRIES || !Number.isSafeInteger(originalSize) || originalSize < 0 || originalSize > MAX_ZIP_ENTRY_SIZE || totalOriginalSize > MAX_ZIP_TOTAL_SIZE - originalSize) {
        throw new Error('Excel ZIP 解压大小超出限制');
      }
      totalOriginalSize += originalSize;
      return name.endsWith('.xml') || name.endsWith('.xml.rels');
    },
  });
};

const rejectFormulaCells = async (file: File): Promise<void> => {
  const archive = unzipArchive(new Uint8Array(await file.arrayBuffer()));
  const worksheet = archive[firstWorksheetPath(archive)];
  if (!worksheet) throw new Error('missing first worksheet');
  const worksheetRoot = parseXml(XML_DECODER.decode(worksheet)).documentElement;
  if (!worksheetRoot) throw new Error('missing first worksheet');
  if (hasElement(worksheetRoot, 'f')) {
    throw new Error('Excel 文件不能包含公式');
  }
};

export async function ownerReassignmentCsvFromFile(file: File): Promise<string> {
  const filename = file.name.toLowerCase();
  if (file.size > MAX_FILE_SIZE) throw new Error('上传文件不能超过 1 MiB');
  if (filename.endsWith('.csv')) return file.text();
  if (!filename.endsWith('.xlsx')) throw new Error('仅支持 CSV 或 XLSX 文件');

  let rows: unknown[][];
  try {
    await rejectFormulaCells(file);
    rows = await readSheet(file, { trim: false }) as unknown[][];
  } catch (error) {
    if (error instanceof Error && error.message === 'Excel 文件不能包含公式') throw error;
    throw new Error('Excel 文件无法解析');
  }
  if (!rows.length) throw new Error('Excel 第一张工作表不能为空');
  const header = rows[0];
  if (header.length !== COLUMNS.length || header.some((cell, index) => cell !== COLUMNS[index])) {
    throw new Error(`Excel 第一行必须且只能是：${COLUMNS.join(',')}`);
  }
  const csvRows = [COLUMNS.map((column) => csvCell(column)).join(',')];
  for (const [index, row] of rows.slice(1).entries()) {
    if (blankRow(row)) continue;
    if (row.slice(COLUMNS.length).some(nonEmpty)) throw new Error(`Excel 第 ${index + 2} 行存在额外列`);
    for (const column of ID_COLUMNS) {
      if (typeof row[column] === 'number' && !Number.isSafeInteger(row[column])) {
        throw new Error(`Excel 第 ${index + 2} 行的 ID 必须是安全整数`);
      }
    }
    csvRows.push(COLUMNS.map((_, column) => csvCell(row[column] ?? null)).join(','));
  }
  const csv = csvRows.join('\n') + '\n';
  if (new Blob([csv]).size > MAX_FILE_SIZE) throw new Error('Excel 转换后的 CSV 不能超过 1 MiB');
  return csv;
}
