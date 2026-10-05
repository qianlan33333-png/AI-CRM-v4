# 订单导出精简统一表 Product Design QA · 2026-10-05

final result: passed（本次订单筛选/导出入口视觉与交互范围；共享预发及生产安装待唯一工作台）

源目标：当前 V4 基线 `641d757` 的真实订单 Host 与管理后台壳，本地只替换订单 Host 为该基线的构建字节；实现目标：本候选的实际构建页面。两者均为同一隔离 PostgreSQL 的管理员空订单态，亮色、1600×1000 CSS viewport、DPR 1。截图由 CUA 浏览器取得。初始工具截图2500×1563与后续1590×994不一致，因此重新采集并读回源与实现的实际viewport/DPR；最后两张均1590×994，在同一组合图比较，不以密度差异提出视觉问题。页面与源码范围保持订单筛选区、列表和侧栏。

证据目录：`/Users/qianlan/.codex/visualizations/2026/10/05/01a109fe-cdea-72a1-acf9-c9b70f1ef6ba`。最终源 `order-export-source-final.jpg`（初始源 `order-export-source.jpg`），首轮 `order-export-implementation.jpg` / `order-export-comparison.jpg`，最后 `order-export-final.jpg` / `order-export-comparison-final.jpg` / `order-export-preview.jpg`。已在同一组合图查看源与实现。

- 首轮 P1 已修：共享反馈将新增导出按钮识别为未接入动作，成功后出现错误 toast。按现有真实能力控件约定设置绑定标记，并覆盖反馈层回归；最终真实页面无该错误。
- 第二轮 P2 已修：成功提示与按钮同行，挤压筛选框。提示放入筛选卡片独立整行，初始/错误时隐藏。最后组合截图确认输入、日期、按钮没有被成功文本挤压。
- 最后无本次 P0/P1/P2 视觉差异；合并两个 CSV 按钮为一个 XLSX 是已授权差异。既有字体、输入、颜色、圆角与导航保持现有组件。未开展移动后台重设计。

实际构建页面点击导出，看到成功提示；Chrome 下载 `/Users/qianlan/Downloads/订单明细 (1).xlsx`，独立 openpyxl 只读打开确认1行27列空结果表头。CUA 的 download 事件在此环境超时，采用实际下载文件读回确认。IAB 用于视觉，Chrome用于下载。最终 IAB 控制台 error 列表为空。隔离本地页面仅预置合成管理员会话便利截图，不等于生产鉴权验收；正式 RBAC/CSRF拒绝用 Handler 测试，真实 Access 登录和 CLI/HTTP 由 PostgreSQL 组合测试覆盖。加载/计数/超限/权限与身份失败由真实 Host JSDOM旅程覆盖。本次无真实 Provider、外推或发货效果。

---

# 周期商品购买信息收集 Product Design QA · 2026-10-02

final result: passed（本次视觉范围；本地真实 Chromium 保存与付款三档通过；已安装合同由工作台核验，真实支付及真实外推未验证）

## 视觉依据与比较

源为用户图2普通商品，以及当前生产登录后的普通商品68/周期商品12。证据目录 `/Users/qianlan/.codex/visualizations/2026/10/02/01a0fd0c-59bd-7150-b8dc-fb78fdefaca7`：`01-production-ordinary.png`、`02-production-periodic.png`、`06-local-periodic-final.png`。实现截图来自隔离PostgreSQL16与当前V4真实Host，合成周期商品1，通过认证HTTP保存并重新加载，非静态原型。

生产截图1613×1002，DOM viewport1612×1007；本地截图1603×998，临时viewport1613×1004/实际DOM宽1603。亮色、售卖信息、shipping_address选中，按内容区对齐比较，原始设备密度未知，不声称整页像素相等。全图并排`04-design-comparison.png`与控件裁取`05-control-comparison.png`保留首轮差异；最终截图及DOM字体读回确认修复结果。周期原有商品编码/价格位置与普通商品不同，保留本页面排列，只复用同款控件。

## Findings 与迭代

- P1（已修）：首次真实浏览器检查发现新控件在隐藏的购买后动作区；移至可见售卖信息价格旁，并加入closest('#sp-sale')回归断言。
- P2（已修）：首轮组合比较发现周期提示字12px且灰色，比普通商品14px/22px暗淡；统一到既有text颜色rgb(31,35,41)和14px/22px，最终浏览器计算样式已读回。
- 最终无剩余本次P0/P1/P2视觉问题。三档原生select、字段标签、边框/圆角、行距及蓝色保存按钮沿用现有壳；无新图片/图标或布局重设计。未开展移动管理后台重设计。

## 功能证据边界

JSDOM合同验证新建/编辑、三档序列化、权威读回、其他维度隔离、CAS及幂等头；隔离库真实认证HTTP保存并在Host回读shipping_address/version2/enabled。本地HTTP浏览器POST被现有HTTPS Origin校验拒绝，没有削弱安全检查；已安装HTTPS浏览器保存旅程由发布工作台补齐。周期公开付款页在Provider disabled及非微信身份时正确提示微信打开；三档付款字段及地区数据由Handler/领域测试验证。没有进行生产商品保存、支付或外推启用/真实发送。


---

**Findings**

- Prior P1: one-by-one input hint wrapper was incorrectly presented as a question border, nesting borders around the textarea. Fixed by giving the real question the same title/required/hint/input structure and padding as all-in-one; the hint wrapper has no border. Both modes retain one textarea border.
- Prior P1: fixed demo navigation and generated screen labels appeared in mobile questionnaire pages. Removed before template mount; actual authenticated definition title now supplies document.title. Neutral initial HTML title is 问卷.
- Prior P1: canceled authorization added a red authorization warning. Removed in the Controller; answers and original submit remain. Every user submit checks the current Owner session; successful official authorization auto-continues once with the original key.

**Visual Truth and Evidence**

- Source: /var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-2ff82c77-ef86-4513-b2e4-c73868aa4736.png (1170×2532, normalized to 390×844, source density3). Browser-owned navigation/status/home areas excluded (content crop y91–822).
- Selected truth is existing all-in-one card/input styling; the user keeps all/one behavior and requests no new design. Pagination/progress/footer differences are intentional.
- Implementation: /tmp/crm-survey-diagnosis-20261002/revision-mobile/one-choice-iab.png and one-text-iab.png; each390×744, CSSviewport390×744, density1 verified before capture. Local visual fixture serves actual built H5/Host/CSS with two reference-content questions, local-only simulated reads; it is not business acceptance.
- Full comparison: /tmp/crm-survey-diagnosis-20261002/revision-mobile/comparison-full.png (780×744), source left / one selected choice right. Same source choice text and selected third option; source all-in-one shows the next card while one-by-one intentionally shows one card.
- Focused comparison: /tmp/crm-survey-diagnosis-20261002/revision-mobile/comparison-input.png (724×230), source left / one textarea right. Same question and synthetic answer text; current right screenshot intentionally has input focus. Its existing blue focus ring and Chromium resize handle differ from the unfocused iOS source; no second wrapper border exists.
- All-in-one local capture: all-iab.png; DOM/title/choice/text interaction inspected. In-app browser later changed viewport on navigation (433×826 observed), so this capture is supplemental and not used for exact scale assertions. Real Host browser journeys independently use390×844 mobile emulation.

**Required Fidelity Surfaces**

- Typography: existing H5 system font and18px title/16px control hierarchy retained; Chinese wrapping aligns in normalized question and text regions. OS antialiasing differs as expected.
- Spacing/layout: shared16px question padding, existing12px card and option radius, hint-before-input hierarchy preserved; one page's progress and previous/next controls are intentional. No fake mobile nav and no viewport-hidden submit control.
- Colors/tokens: existing Survey gray borders, white panels, blue selection/focus and primary action reused. Focus/unfocused states are distinguished.
- Image quality/assets: no new visual assets; native browser chrome is excluded from app content. No generated phone frame or icons introduced.
- Copy/content: real questionnaire title observed in tab, required indicator now retained in one mode. No authorization banner in canceled submit flow, no demo growth header. Next/previous instructions remain relevant only in one mode.

**Interaction Evidence and Limits**

In-app browser: choice selection, next-question, textarea editing, title readback, no console errors in the functioning preview. Initial stale tab sync failed; fresh tab worked. Temporary local fixture404 bug corrected without changing app source. The in-app viewport drift is recorded above.

Actual Host+independent PG16 regression adds assertions for title/no-demo-navigation, outer card border, unbordered hint, cancellation without error or separate login action, and unchanged answer/key replay. Existing full presentation journey covers all/one valid submission,503 retry and double click. Final exact-head receipts are handed off separately. Real WeChat official UI remains Provider-controlled and not validated by synthetic browser.

**Comparison History**

1. User supplied before images show nested borders, fixed growth title/navigation and red auth warning (P1 findings above).
2. Current screenshots opened alongside source in full and focused comparison canvases after fixes. No remaining actionable P0/P1/P2 issue in the requested card/nav/copy scope. The blue focus ring is the existing native interaction state, not extra wrapping.

**Implementation Checklist**

- Shared question card hierarchy and supported/unsupported title preservation implemented.
- Demo navigation removed; real browser title bound to authenticated definition.
- Cancel/error401 handled by original submit flow, no authorization reminder/extra login button.
- Exact-head checks/build/real Host regression and release handoff recorded outside source.

**Follow-up Polish**

No new design polish added. Stability follow-up reuses the same error/retry control for stalled reads or a changed definition and the auth gate's existing action for read retry. Ordinary draft restoration does not add a banner or automatic submission. Actual mobile Host journeys additionally reload a filled questionnaire and recover a gateway failure after a real Owner commit. Real iOS/WeChat title refresh and official consent UI require production business acceptance.

final result: passed

---

# 人群包发送历史修复：Product Design 验证

视觉目标：用户提供的生产详情截图 `/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-a616b370-fdbb-490d-b994-382e4e73e3e8.png`，以及现有 V4 `admin_audience_detail` 页面。实现采用既有组件和 CSS，只接入已存在的分页、增加口径说明和修正数据标签；不是页面重设计或原型发布。

实际实现截图：`/Users/qianlan/Downloads/新CRM/release-control/audience-send-history-20261003/screenshots/audience-history-first.png`、`audience-history-last.png`、`audience-history-390.png`。实际登录、真实 V4 Composition、隔离 PostgreSQL 16、最终 Host 资源；记录是合成测试数据，Provider disabled。

## 对照证据和边界

用户截图 2324×1478 物理像素，原始 CSS 视口/密度未提供；本地浏览器 1440×900 CSS/物理像素，deviceScaleFactor=1，移动补充为 390×900。两张图在同一次工具输出中打开，按详情内容区域对照。原图只含侧栏局部，本地含完整侧栏；数据、暂停/运行状态与预检警告不同，来自隔离 fixture，不属于视觉偏差。本次不声称逐像素克隆或生产状态相同。

全图对照：仍为四列摘要、左侧六步导航、右侧发送记录表。新说明占一个文本行，是明确的预期变更。焦点对照：累计运行数胶囊、运行行/收件人按钮、既有分页控件；最后一页 5 条历史运行，总数仍为 105。首尾页截图与真实 HTTP/数据库读回共同验证，没有用图片或 mock 代替持久业务证据。

## 五项表面

- 字体：使用既有管理端字体、字号、字重及 ai-label；新增说明清楚可读。
- 间距/布局：复用原 ai-summary-grid、ai-pagination、按钮、表格和导航，不新增布局系统；说明行是预期增加。390 视口保持现有表格横向滚动。
- 颜色：复用原灰色统计胶囊、语义状态和按钮颜色，无新增 token。
- 图像/资产：本改动无新增图像或图标，既有壳资产保持原样。
- 文案：明确“当前人数”不是累计发送人数，“累计运行数”不是送达人数；接口推送明确是最近最多 100 条，读取失败不伪报为零。

## 交互和结果

真实 Chromium 已验证本包筛选、首/末页、上一页、刷新回第一页、总数独立于分页、无发送/刷新写入、无 runtime exception。JSDOM 补充请求失败不推进页码，以及旧请求不会覆盖较新刷新结果；hourly 配置展示正确，保存其他字段保留原计划。

没有发现此修复范围内的 P0/P1/P2 问题。尚未执行 Linux 预发安装旅程、生产页面读回或真实 Provider/收件人验收；这些是发布证据，不能由本地验证替代。

final result: passed
