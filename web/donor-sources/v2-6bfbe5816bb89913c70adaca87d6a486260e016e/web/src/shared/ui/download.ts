/**
 * CSV 导出工具：带 BOM 前缀保证 Excel 中文不乱码。
 * 雷达访问明细、漏斗筛选结果导出共用。
 */
export function downloadCsv(filename: string, header: string[], rows: (string | number)[][]): void {
  const cell = (v: string | number): string => String(v ?? '').replace(/,/g, ' ');
  const csv = '﻿' + [header, ...rows].map((r) => r.map(cell).join(',')).join('\n');
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }));
  a.download = filename;
  a.click();
  // 释放 blob URL
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}
