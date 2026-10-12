# 员工完整名单刷新 Product Design QA

Source visual truth: 用户提供的生产员工列表截图 `/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-b41b85a2-8c4a-447b-bdf5-0a65964a9dac.png` 与本仓当前已挂载 admin_access。该截图为 2314×1080 内容截取；不以它与本机视口做像素级相等判断。

Same-viewport reference: `/Users/qianlan/Downloads/新CRM/release-control/staff-directory-refresh-20261012/screenshots/access-governance-1440.png`。Implementation: 同目录 `access-governance-directory-refreshed.png`。两张均为 Chromium 1440×900、CSS 1440×900、1x，无缩放；已在同一比较输入打开。前者是同一 V4 页面初始三名已授权夹具，后者是刷新后五名夹具；新增行、提示和“未开通”是本次明确行为变化，并非设计漂移。

字体/层级、字号、行高：沿用 admin_base/admin_access，标题、员工姓名与账号层级一致。间距/布局：同一表格与六列结构，新增行正常撑开，提示出现后的垂直位移符合现有状态组件。色彩：复用原权限蓝色及不可登录状态色，无新 token。图像/图标：复用原导航与品牌资产，无新增/替换资产。文案：完整刷新反馈包含发现、新增、姓名更新数量；新员工明确未开通，操作为“开通权限”，不是“管理”或“可登录”。1440 图中的新行和状态已清楚可读，无需另裁局部图；390/420 的行布局和无横向溢出由真实浏览器旅程验证。

交互：真实 Access + WeCom Adapter + 隔离 PostgreSQL 的 Chromium 旅程验证刷新、重复刷新、两个新员工未开通、现有角色选择与确认开通弹窗、同员工记录开通读回，随后覆盖原管理/转移/无权限旅程。新 DOM 合同覆盖 Provider/列表失败不显示成功、缺完整刷新收据不冒称成功、403清除敏感列表。未进行全套 WCAG 合规测试。

Comparison history: 首轮视觉对照，无 P0/P1/P2 差异，没有为视觉问题改动 CSS。构建输入和测试夹具准备修正属于验证准备，不计为设计迭代。安装二进制/Linux与真实生产新增员工读回由工作台独立验收；本报告不把源旅程当安装验收。

Findings: 无需要本次修复的 P0/P1/P2。P3：未开通状态沿用现有不可登录状态颜色，可在未来统一状态色设计时评估；本次不改公共样式。

final result: passed
