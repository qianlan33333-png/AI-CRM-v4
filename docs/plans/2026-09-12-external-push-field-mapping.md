# 外部推送字段映射交付合同

OneID: reads canonical customer; 只通过 Customer Port 读取付款人显示名，订单手机号只通过 Order Port 读取加密冻结联系人，不匹配或创建身份。
Persistence: local transaction + existing Provider write/external effect. Product配置沿原revision/UoW/receipt；Outbound创建不可变加密报文并提交原EER。无新队列/重试机制。

## 已确认行为

- 商品新模式只发送配置的顶层JSON字段，值可为固定文本、数字、布尔、null、已有复杂JSON或白名单变量。
- 变量：order.mobile（本次下单手机号）、payer.nickname（付款人档案显示名）、order.paid_amount_minor（实付整数分）。真实缺失输出null，读取失败阻止创建残缺意图，零元输出0。
- 配置与变量值在原发送意图中冻结；重试不重新取值，不重放历史任务。
- 旧配置保持legacy，UI先展示协议前后差异，明确转换草稿后保存才进入新协议。
- 签名/投递标识留请求头，映射正文中的同名字段不能覆盖系统投递header。
- 问卷答案JSON与原发送协议不变，仅统一固定参数编辑视觉；不加入订单变量。
- 编辑器左右布局：字段表/示例JSON；窄屏单列；重复/无效key就地提示；普通/周期商品复用组件。
- 预览无Provider请求无真实客户数据，使用与正式发送相同纯编译器。

## 验收

配置保存/回显/预览/实际报文一致；大整数/复杂JSON不损失；missing/null/0；历史任务不变；元分无混淆；同幂等任务改名或改配置后不变；问卷字段冲突不覆盖；渠道新增缺陷另PR独立修复。

## 发布

GitHub PR及CI通过后本地完整打包经SSH到已授权生产服务器，发布SHA、迁移、服务与页面逐项验收。真实外部系统测试仅在明确指定的测试目标进行，不使用实际客户订单制造付款或外推。
