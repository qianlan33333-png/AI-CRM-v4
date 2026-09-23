#!/usr/bin/env python3
"""从合并后的原型 dc.html 中抽取每个屏幕为独立页面模板。"""
import json
import pathlib
import re

SRC = pathlib.Path('/Users/qianlan/Documents/kimi/ai-crm-src/ui-preview/design/AI-CRM 后台.dc.html')
OUT = pathlib.Path('/Users/qianlan/Documents/kimi/ai-crm-frontend/src/admin/templates')
OUT.mkdir(parents=True, exist_ok=True)

s = SRC.read_text(encoding='utf-8')
script = re.search(r'<script type="text/x-dc"[^>]*>([\s\S]*?)</script>', s).group(1)

# 导航顺序（一级页）
nav_m = re.search(r'const NAV = \[([\s\S]*?)\];', script)
nav_order = re.findall(r"'(\w+)'", nav_m.group(1))

# PARENT: key -> 归属导航键（二级页归属其一级页）
parent_m = re.search(r'const PARENT = \{([\s\S]*?)\};', script)
parent = dict(re.findall(r"(\w+): '(\w+)'", parent_m.group(1)))

# ---------- 按深度切出 stage 直属屏幕块 ----------
stage_open = re.search(r'<div style="\{\{ stage \}\}">', s)
pos = stage_open.end()
depth = 1
blocks = []
cur_start = None
cur_key = None
for m in re.finditer(r'<div\b|</div>', s[pos:]):
    tok = m.group(0)
    abs_start = pos + m.start()
    abs_end = pos + m.end()
    if tok == '<div':
        if depth == 1 and cur_start is None:
            head = s[abs_start:abs_start + 60]
            km = re.match(r'<div style="\{\{ f\.(\w+) \}\}">', head)
            if km:
                cur_start = abs_start
                cur_key = km.group(1)
        depth += 1
    else:
        depth -= 1
        if depth == 1 and cur_start is not None:
            blocks.append((cur_key, cur_start, abs_end))
            cur_start = None
        if depth == 0:
            stage_end = abs_end
            break

registry = []
for key, bstart, bend in blocks:
    block = s[bstart:bend]
    capm = re.search(r'<div style="\{\{ cap \}\}">(\d+) · ([^<]+)</div>', block)
    if capm:
        num = int(capm.group(1))
        title = capm.group(2)
    else:
        num, title = 0, key
    openm = re.search(r'<div style="\{\{ box \}\}"><div style="\{\{ scale \}\}">', block)
    assert openm, f'{key}: box/scale not found'
    content = block[openm.end():]
    # 去掉尾部三个闭合（scale / box / 屏幕块自身）
    stripped = re.sub(r'(\s*</div>){3}\s*$', '', content)
    assert len(stripped) < len(content), f'{key}: tail strip failed'
    (OUT / f'{key}.html').write_text(stripped.rstrip() + '\n', encoding='utf-8')
    level = '二级' if '二级' in title else ('一级' if '一级' in title else '')
    label = re.sub(r'（[^）]*）', '', title)
    registry.append({
        'key': key, 'num': num, 'title': title, 'label': label, 'level': level,
        'nav': parent.get(key, key), 'isNav': key in nav_order,
    })

# 覆盖层
overlays = s[blocks[-1][2]:stage_end - len('</div>')].strip()
(OUT / '_overlays.html').write_text(overlays + '\n', encoding='utf-8')

(OUT.parent / 'registry.json').write_text(
    json.dumps({'navOrder': nav_order, 'screens': registry}, ensure_ascii=False, indent=2) + '\n',
    encoding='utf-8')
print(f'blocks={len(blocks)} overlays={len(overlays)}B')
for r in registry:
    print(f"  {r['num']:>3} {r['key']:<20} {r['label']:<14} {r['level']:<4} nav={r['nav']}{' *' if r['isNav'] else ''}")
