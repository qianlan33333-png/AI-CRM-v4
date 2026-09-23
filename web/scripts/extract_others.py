#!/usr/bin/env python3
"""抽取侧边栏与用户端 H5 原型为页面模板。"""
import json
import pathlib
import re

BASE = pathlib.Path('/Users/qianlan/Documents/kimi/ai-crm-src/ui-preview/design')
FE = pathlib.Path('/Users/qianlan/Documents/kimi/ai-crm-frontend/src')


def split_stage_blocks(s):
    """切出 stage 内直属 <div style='{{ f.key }}'> 块 -> [(key, block)]"""
    stage_open = re.search(r'<div style="\{\{ stage \}\}">', s)
    pos = stage_open.end()
    depth, blocks, cur_start, cur_key = 1, [], None, None
    for m in re.finditer(r'<div\b|</div>', s[pos:]):
        a = pos + m.start()
        if m.group(0) == '<div':
            if depth == 1 and cur_start is None:
                km = re.match(r'<div style="\{\{ f\.(\w+) \}\}">', s[a:a + 60])
                if km:
                    cur_start, cur_key = a, km.group(1)
            depth += 1
        else:
            depth -= 1
            if depth == 1 and cur_start is not None:
                blocks.append((cur_key, s[cur_start:pos + m.end()]))
                cur_start = None
            if depth == 0:
                break
    return blocks


# ---------------- 侧边栏（单页静态） ----------------
sb = (BASE / 'AI-CRM 企微侧边栏.dc.html').read_text(encoding='utf-8')
body = re.search(r'<helmet>[\s\S]*?</helmet>([\s\S]*?)</x-dc>', sb).group(1)
# 去掉最外层包裹 div：找到第一个根 div, 取其 inner
m = re.match(r'\s*<div[^>]*>', body)
root_open_end = m.end()
# 根 div 的闭合是最后一个 </div>
content = body[root_open_end:].rstrip()
assert content.endswith('</div>')
content = content[:-len('</div>')].rstrip()
(FE / 'sidebar').mkdir(parents=True, exist_ok=True)
(FE / 'sidebar' / 'templates').mkdir(exist_ok=True)
(FE / 'sidebar' / 'templates' / 'index.html').write_text(content + '\n', encoding='utf-8')
print('sidebar template bytes:', len(content))

# ---------------- H5（12 屏，去手机壳） ----------------
h5 = (BASE / 'AI-CRM 用户端 H5.dc.html').read_text(encoding='utf-8')
blocks = split_stage_blocks(h5)
(FE / 'h5' / 'templates').mkdir(parents=True, exist_ok=True)
registry = []
for key, block in blocks:
    capm = re.search(r'<div style="\{\{ cap \}\}">([^<]+)</div>', block)
    title = capm.group(1) if capm else key
    openm = re.search(r'<div style="\{\{ phone \}\}">', block)
    assert openm, f'{key}: phone not found'
    content = block[openm.end():]
    content = re.sub(r'(\s*</div>){3}\s*$', '', content)  # phone/box/屏幕块
    # 去手机状态栏（第一个 44px 高度、含 9:41 的 div）
    content = re.sub(
        r'^\s*<div style="display:flex;align-items:center;justify-content:space-between;height:44px;[^>]*>[\s\S]*?</svg>\s*</div>',
        '', content, count=1)
    slug = re.match(r'([QS]\d+)', title)
    registry.append({'key': key, 'title': title, 'group': title[0] if title else ''})
    (FE / 'h5' / 'templates' / f'{key}.html').write_text(content.strip() + '\n', encoding='utf-8')
(FE / 'h5' / 'registry.json').write_text(
    json.dumps(registry, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('h5 screens:', len(registry))
for r in registry:
    print('  ', r['key'], r['title'])
