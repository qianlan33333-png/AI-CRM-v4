# 渠道表单保存保留未展示字段 PRD

## 业务判断与根因

运营人员编辑既有渠道的可见配置（客服、欢迎语、标签或状态）时，保存必须保留该表单未展示的既有渠道配置。此次生产恢复中，普通二维码渠道的标准供体表单未渲染二维码地址、State 与溢出策略；其脚本仍提交 `qr_url: ""`、不提交 `scene_value` 和 `overflow_policy`。V3 Catalog 的 PATCH 是完整配置替换，因此合法 CAS 保存把三个值写成空。

服务端 DTO 已完整支持 JSON `qr_url`、`scene_value`、`overflow_policy`，其中 `qr_url` 持久化为 `qrcode_url`。缺口仅在 V3 Channel Form Host 的保存适配层。旧 V2 的渠道提交会从可见控件完整发送 `scene_value`、`qr_url` 与 `overflow_policy`；它证明完整字段 round-trip 的行为，但不能直接复用到当前冻结的标准供体表单。参考：[`web/src/admin/controller.ts`](https://github.com/qianlan33333-png/AI-CRM-v3/blob/580bceb80651677465c775df988f4fbe550b1a83/web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/admin/controller.ts#L2297-L2304)。

分类：OneID 不涉及；持久化复用现有 Catalog PostgreSQL UoW、ETag/CAS、幂等收据和审计；不创建任务、Provider 写入或 External Effect。

## 最小方案

仅修改 `web/v3/channelAdmissionHost.ts` 与其浏览器旅程测试：

1. 编辑页读取时冻结三个未展示字段的初始值；读取兼容 `qr_url` 和历史别名 `qrcode_url`，出站只发送 Catalog 认可的 `qr_url`。
2. 由 Host 在调用冻结供体脚本前标注哪些字段在当前载体确实有表单控件。保存时只为“当前没有控件”的字段回填初始值：普通二维码保留 `qr_url`、`scene_value`；所有未展示的 `overflow_policy` 保留。
3. 对存在控件的字段照常发送其当前值，包括空值；链接渠道的 `customer_channel/scene_value` 仍可明确清空。现有直接 Catalog API 客户端的显式空值也不被 Host 改写。
4. 保持原 PATCH 的 If-Match、同一幂等键重试、409 不自动重试及 channel code 不可变规则；不改 source mapping、资产/历史快照或 callback/effect 行为。

## 验收

- 普通二维码编辑任一可见字段后，PATCH 保留初始 `qr_url`、`scene_value`、`overflow_policy`，只发送 `qr_url`，不发送 `qrcode_url`。
- 链接渠道编辑并清空其可见渠道参数后，PATCH 的 `scene_value/customer_channel` 为空；未展示的溢出策略仍保留。
- 现有 CAS/CSRF/幂等键、409 草稿保留和严格 DTO 测试继续通过；新增旅程断言只产生一次 PATCH。
- 本轮不声称 Provider 二维码生成、欢迎语或标签外部执行成功；它们均不在本修复内。

