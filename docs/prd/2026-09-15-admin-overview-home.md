# 管理端经营总览与统一导航

## 判断与边界

- **用户目标**：管理员从 `/admin` 直接查看同一北京时区范围内的经营摘要，并在私有管理页使用同一份七组导航；旧首页跳转与旧导航不能掩盖新的汇总或权限状态。
- **OneID**：不涉及新的解析、Provision 或 merge。页面只显示 `/api/admin/overview` 已计算的只读统计；该 API 内已有的 canonical payer 读取仍由 Payment／Identity Owner 协调。
- **持久化与外部效果**：无。Host 只发起管理员授权的 GET；不写汇总、不建任务、不读取或写入 Provider。
- **终端与组件**：管理端复用 `admin_base` 单壳、`overviewAdmin.ts`/`overview.css` 和 `navigationHost.ts`。生成的私有管理文档只注入导航 Host，不替换原页面正文或 donor runtime。

## 已核实参考

- [PR #293](https://github.com/qianlan33333-png/AI-CRM-v3/pull/293) 提供唯一的 `/api/admin/overview` Owner 只读合同：确认支付按 `paid_confirmed_at`、退款按已完成审计事实、客户按来源证据、分销按 Distribution Owner。
- [PR #301](https://github.com/qianlan33333-png/AI-CRM-v3/pull/301) 保留 provider-scoped 订单详情与结算确认读取；本首页不复制或重写该查询。
- 当前 #299 的 `overviewAdmin`、`navigationHost` 及 Chromium Journey 是页面和发布包接入参考。已支付记录明细仍由 #326 独立交付。

## 可观察行为

1. `/admin` 由受保护的 V3 overview shell 渲染四项核心指标：已确认支付、支付订单、支付客户、新增客户；两项辅助指标：完成退款、净收款。每个区块独立显示 ready、已确认零、来源待核实或读取失败以及最近读取时间，失败不会把未知数显示为零。
2. 今天、近 7 天、近 30 天和自定义半开日期范围均按 `Asia/Shanghai` 展示。趋势只基于原始确认支付时间；ready/zero 范围允许补齐零日期，未知范围不补造记录。
3. 分销同时显示期间已付销售额、初始佣金和笔数，以及不受期间限制的当前待结算／已结算余额；`distribution_not_configured` 明确显示未知，不伪造 `¥0` 或“暂无待办”。
4. `/admin` 与生成的私有管理文档从一份导航定义展示总览、客户、运营、交易、分销、内容素材、系统设置七组。导航只呈现既有服务端路由；安全配置及其超管边界仍由既有服务端授权决定，Host 不在客户端添加权限。
5. 旧 `/admin/index.html`、商品、分销、客户详情等已知链接保持同源可访问和正确 active state。支付记录、付款客户、退款、净收款和分销的进一步下钻不在本 PR 内。

## 验收

- 校验 V3 asset manifest、完整 release stage 与私有文档中 overview／navigation 资源的闭包。
- 真实 PostgreSQL Chromium Journey 在 1280 与 1440 宽度检查指标、北京范围、7/30 日趋势、发布资源 HTTP 状态、七组导航与旧链接 active state。
- 定向 TypeScript、webshell、overview composition／HTTP 和导航测试；P5 以最终 `origin/main → HEAD` 为准。
