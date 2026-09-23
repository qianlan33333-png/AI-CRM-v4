# PRD：裂变活动普通商品类型适配修复

状态：已确认冻结｜实现中
负责人：Codex
板块：Referral 管理端设置
分支/worktree：`codex/referral-product-type-adapter` / `referral-product-type-fix`
预计上线窗口：2026-09-20，CI 通过并合入后进入单一发布队列

## 1. 业务判断

- 用户与场景：管理员在 `/admin/referral/settings` 新建“购买指定商品”的裂变活动，并从共享商品选择器选择普通商品。
- 生产失败事实：生产 release `c7588a6bafa0039a0ccdf7d95d3d3ed9a55f5391` 的选择器实际值为 `1:standard`；提交后接口返回 `400 invalid_request`，页面显示“提交内容无效，未执行操作”。
- 根因：Product 稳定 Port 的普通商品枚举为 `standard`，Referral 写命令的领域枚举为 `standard_product`；页面组装请求时缺少显式转换。
- 核心规则：普通商品必须映射为 `standard_product`；周期商品继续使用 `service_period`；未知类型不得猜测或降级提交；封面 URL 可为空。
- 成功标准：真实 Product 选项 `{product_type:"standard"}` 在界面内部形成 `id:standard_product`，创建/保存请求写出 `product_type:"standard_product"`，且周期商品和既有活动回显不回归。

## 2. 市场与 GitHub 调研

| 来源 | 类型 | 可借鉴能力 | 采用/舍弃 | 原因 |
| --- | --- | --- | --- | --- |
| [Microsoft Azure REST API Guidelines](https://github.com/microsoft/api-guidelines/blob/vNext/azure/Guidelines.md) | 成熟公开 API 方案 | 对字符串/枚举建立明确、可验证的转换合同；写入端拒绝未知枚举 | 采用显式转换与拒绝未知值；不引入其 SDK/版本体系 | 防止跨上下文字符串看似相近却直接透传 |
| [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md) | 成熟公开 API 方案 | 外部/内部类型保持各自合同，在边界做转换 | 采用边界转换思想；不引入生成器 | Referral 与 Product 是不同 bounded context，不应修改任一领域枚举来迎合另一个 |
| [ddd-by-examples/factory](https://github.com/ddd-by-examples/factory) | 高质量 GitHub 参考 | Ports/Adapters 隔离领域模型与外部模型 | 采用调用方 adapter 转换；不复制框架 | 与本仓“跨领域仅稳定 Port”规则一致 |

## 3. 复用与架构分类

- 已有领域/模块：继续复用 Product `ProductOptionReader` 与 Referral 现有创建 API，不新增跨领域读取。
- 共享组件与标准组件：继续使用 `AICRMSearchSelect`；不修改共享选择器，不创建第二套商品选择组件。
- OneID：不涉及；活动配置不解析、建立、关联或合并客户身份。
- Persistence：本地事务；创建成功仍由 Referral 既有 PostgreSQL UoW、幂等收据、审计与 Outbox 原子提交。本修复只纠正事务前的请求值。
- External Effects：不涉及；不触发 Provider、支付、退款或企微写入。
- 复用方案：在 `web/v3/referralAdmin.ts` 的 Referral 页面 adapter 将 Product `standard` 映射为 Referral `standard_product`，`service_period` 原样保留，未知值不进入可选项。

## 4. 产品与技术边界

- 用户流程与终端基线：管理端现有完整设置页；视觉、字段、按钮、反馈文案均不改变。
- Product Design/一致性：按现有页面缺陷审查处理；沿用生产截图和当前 admin shell，不做视觉探索。`artifact-template-crm` 不适用，因为不生成或重设计界面图。
- 标准组件：`AICRMSearchSelect` 只负责搜索和选择；枚举转换由 Referral 调用方 adapter 负责。
- API/Port：不改 Product Port、不放宽 Referral `CampaignConfig.Valid()`，不改 HTTP 写合同。
- 数据 Owner/迁移：无表结构、数据迁移或历史活动修改。
- 权限/审计/错误：沿用现有管理员会话、CSRF、幂等键与 `invalid_request` 语义。
- 并行依赖：仅 `web/v3/referralAdmin.ts`、`web/v3/referralSettings.test.mjs` 和本 PRD；不触碰 Composition、共享组件、迁移或 Provider。

## 5. 测试与验收

| 测试层 | 是否适用 | 命令/旅程 | 预期 |
| --- | --- | --- | --- |
| 回归测试 | 是 | `node web/v3/referralSettings.test.mjs` | 真实 `standard` 选项映射并提交 `standard_product`；周期商品保持 `service_period` |
| 前端合同 | 是 | `node web/v3/referralAdmin.test.mjs`、`npm run typecheck`、`npm run build` | 设置页、共享选择器装配与构建通过 |
| fast | 是 | `python3 scripts/dev_preflight.py fast` | 首推前通过 |
| compile | 否 | 无 Go 改动；若范围变化再执行 | 不冒称编译证据 |
| PostgreSQL/迁移 | 否 | 无持久层或迁移改动 | 依赖既有 Referral UoW 合同 |
| 生产验收 | 是 | 部署后认证页面创建一条明确命名的验收草稿并读取列表/详情 | HTTP 创建成功、字段读回一致；验收数据处理需遵循用户授权 |

## 6. 并行与发布快照

```text
起始 origin/main：c7588a6bafa0039a0ccdf7d95d3d3ed9a55f5391
生产 health/ready：同为 c7588a6b，alive/ready
活跃相关 PR：未发现 Referral 设置重叠 PR；长期历史 PR 不作为依赖
其他 worktree：AI 上下文、课程媒体导入等，与本修复文件不重叠
迁移/API/共享入口：均不改
本次合并需要重新运行：fast、Referral JSDOM、typecheck、build、GitHub required checks
```

## 7. 上线、监控与回滚

- 合并后只从准确 merge SHA 的干净 checkout 构建 Linux amd64 发布包。
- SSH 前核验 `124.220.53.183` Host Key；只使用仓外既有私钥与现有安装脚本。
- 部署后分开核验 health、ready、release SHA、页面商品值、创建响应和服务器读回。
- 回滚触发：普通商品仍返回 `invalid_request`、周期商品回归、选择器无法回显或其他管理端构建/旅程失败。
- 回滚方式：回滚该单一提交并按完整发布流程部署；不以生产数据补丁掩盖代码合同错误。

## 8. 确认与变更

- 用户确认：在生产只读诊断结论后明确要求“解决掉这个问题”，据此冻结上述最小修复合同。
- 确认时间：2026-09-20。
- 未验证项：GitHub CI、合并 SHA、SSH 部署、生产创建与读回在实现完成后补录。
