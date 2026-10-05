# 订单导出精简统一表（用户已确认）

```mermaid
flowchart LR
 A[当前筛选条件] --> B[现有管理员权限与CSRF]
 B --> C[跨页读取订单与全部商品明细]
 C --> D[原始收货快照与OneID买家信息]
 D --> E[固定27列 缺失留空]
 E --> F[XLSX与同事务导出收据]
 F --> G[本机下载]
```

统一列：线上订单号、下单时间、买家帐号、买家昵称、支付单号、付款方式、付款时间、应付金额、币种、退款金额、收货人姓名、手机、省份、城市、区县、详细地址、商品编码、商品名称、数量、商品单价、状态、订单来源、分销员编号、分销员名称、原平台状态、原平台售后状态、导入时间。

每商品一行，订单级金额与公共字段重复，字段精度保持实际事实。无地址或联系方式留空；读取/解密失败报错；异常原始收货字段不自动纠正。没有来源与重复的原模板列删除。默认XLSX，CSV兼容调用共用相同映射。当前筛选跨页导出，手机/外部联系人使用现有解析；不按前端分页截断。不筛选状态时导出各种状态。文本单元格保留长编号、前导零和公式样式原文。

OneID：读取已关联客户、现有身份筛选解析；复用Customer canonical、Identity public-number/RevealPhone及Customer display Port，不建客、不合并、不自建匹配。Persistence：现有本地事务导出收据，格式/模板版本纳入请求摘要，禁止明文PII进入收据和日志。External Effects：不涉及，无Provider调用或自动外传，无队列/Worker。不涉及新增限制；沿用10000明细行/5MiB边界，明确超限而非截断。无需迁移。

参考：[Shopify](https://help.shopify.com/en/manual/fulfillment/managing-orders/exporting-orders)的逐商品明细及历史事实；[Excelize](https://xuri.me/excelize/en/cell.html)的文本单元格与XLSX生成。使用Excelize v2.10.1。父PRD复用已确认方案，不再重复请求审批。

Product Design：index/user-context/audit/image-to-code，已运行用户上下文preflight并观察生产/admin/orders（现有筛选区、两颗CSV按钮）。现有管理端页面为选定视觉目标，仅合并导出按钮和添加状态提示，不重建壳、不新引入UI框架。复用web/v3/orderAdapter与现有apiRequestOptions/提示。实现后浏览器验证空态/加载/错误/实际文件下载。

接口：现有导出与预览URL、operationId不变，格式xlsx/csv；预览total/order_count/row_count/truncated，下载MIME/文件名/receipt/digest/no-store。微信兼容导出入口只导出微信订单。CLI目录随OpenAPI生成。新Order export事实Port由Composition适配，只调用所属领域Port，读取共享UoW；付款信息由Payment新增窄只读Port。分销按订单ID和商品行号读取真实记录，编号取分销员公开编号，不暴露内部ID。

验收：固定27列、有地址/仅手机/无联系混合、多商品/优惠/退款/历史/支付渠道、长编号/前导零/公式文本、异常原始字段保留、解密失败、筛选身份无匹配/冲突/错误、跨页、权限、空结果、边界、收据同key重放/格式冲突、后台和CLI可打开下载。相关包与具名检查登记release-checks；不声称全仓回归。生产三个异常订单使用先前只读快照比较，不修改生产订单。源码候选推送国内后交唯一工作台；生产安装与实际发货外部接入不在本次授权内。回退使用工作台上一完整应用包，不改变历史订单数据。
