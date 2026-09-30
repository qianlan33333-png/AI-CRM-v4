# 企微原生群邀请 Product Design QA

final result: passed

## 视觉依据和实现证据

- Source visual truth：用户提供的表格截图 `/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-837a7c21-4d39-4cdf-82d2-0e78e952debd.png`，2282×194；同一 V4 Host 原有表单布局截图 `/Users/qianlan/Downloads/新CRM/release-control/evidence/group-native-20261001/screenshots/invitation-editor.png`，1440×900。
- Implementation：同目录 `invitation-native-editor.png`（1440×1600）、`invitation-native-plans.png`（1440×900），真实 Host 登录后 Chromium 渲染及 API 保存/数据库读回。
- Full-view comparison：同目录 `design-full-comparison.png`，原有表单和新原生表单并排；表单内容区宽度相同，截图高度不同以展示新增参数，其额外高度是授权功能导致，不比较画布空白。
- Focused comparison：同目录 `design-table-comparison.png`，源表格裁取内容区2128×174并归一化到1144×94，新列表内容区1144×277，组合在同一张图。原图设备密度未知，按内容宽度归一化；新截图CSS viewport与图片1:1、deviceScaleFactor=1。
- State：亮色管理后台，邀请计划页、新建表单；旧顺序轮替与新原生分流均有两个合成群，源图是固定群未同步。模式、状态、行高因业务内容有意不同，不主张像素一致。
- 辅助交互预览：通过 cua_repl 的 IAB 打开本机只读模拟API预览 `http://127.0.0.1:8796/admin/group-invitations`，检查默认原生模式、自动开关及900宽度，错误日志为空。该预览不作为持久化证据；持久化由真实Host旅程证明。

## Findings

没有剩余的新增 P0/P1/P2 问题。保留现有管理后台桌面布局与群目录分页；移动端后台未做重设计。900宽度沿用原管理壳的最小宽度及滚动，新增字段未覆盖其他控件。公共H5布局没有修改。

- 字体：继续使用既有管理壳字体、14px正文、15px分区标题、现有字段/表格字重；原生字段与旧字段标签左边缘、输入框左边缘对齐。群ID用既有等宽字体；中文未截断。
- 间距：复用110px标签列/420px输入列、12px间距与原有分区分隔线。新增四个参数自然增加表单长度，保存按钮可通过现有页面滚动访问。原生双群行比源图单群行高是信息量变化。
- 颜色：白色内容区、浅灰表头、蓝色操作链接/选中导航/checkbox维持原有token；未确认群码下载保持禁用浅蓝，未误显示成功。
- 图片与图标：无新增图片资产；真实Host截图保留管理壳原图标，未用CSS/SVG/emoji重造已有资产；本地只读mock预览不作为资产完整性证据。
- 文案：明确“企微分配 · 初始关联群”“企微原生 · 自动建群”及容量以企微结果为准；群码准备中不冒充已建群，实际200人及群目录过期不等于原生入口不可用。

## Comparison history

在正式组合比较前，实际浏览器复核发现新建表单残留上一笔保存提示，已在进入编辑时清空；原生列表初始群增加明确标签。修改后重跑完整Host旅程，重新捕获并组合上述 full-view 与 focused 图片。最终组合比较未发现需要继续修改的视觉差异。

## Implementation checklist

- 已通过：原生默认、自动开/关字段禁用、中文前缀/序号/备注/state输入、选群、保存、列表显示、重新打开回读、旧顺序模式和未确认下载禁用。
- 已通过：真实Host浏览器runtime exception为0；IAB本地预览error log为0；PostgreSQL持久配置读回及200人/过期初始群公共JSON入口仍active。
- 未验证：真实企微扫码、满群自动建出新群、第六个及以后实际群生命周期。这些需要真实Provider行为，不由合成验收替代。

## Follow-up polish

无本次必须处理的P3项。
