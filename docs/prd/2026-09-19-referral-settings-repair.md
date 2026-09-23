# 裂变活动设置、无战队参与与完整设置页修复

## 生产事实与业务判断
2026-09-19 只读核验生产 release 1e8c501：活动 1（123）已停用且有 2 个参与事实；活动 2（测试i23）进行中且无参与事实。两者配置均为 team/free_signup/无商品。新增及更新 SQL 漏写配置字段是回退根因，不能猜测并自动补上历史商品。

关闭战队不影响报名、购买资格、邀请关系或个人排行。零战队在 Go 中用 0，在 SQL 中用 NULL。资格仍由既有可信购买校验完成。草稿与未开始活动可改核心规则；开始后规则锁定并明确提示，活动文案可以编辑，开始时间与历史事实不可重写。历史错误配置单独依据用户确认的商品修复，不猜测恢复。

## 架构分类
OneID: reads canonical customer through existing verified session; no identity matching/provisioning change.
Persistence: local PostgreSQL transaction in Referral UoW. Reuse audit/outbox/idempotency and optimistic version checks. Product options read via Product stable Port.
External Effects: no new Provider writes, payment effects or durable jobs.

## 页面合同
管理端继续使用 admin_base 壳，/admin/referral/settings 为完整页。四个白色分区：基本信息、参与条件、战队与排行、介绍与奖励。页头右侧取消与保存。商品按名称选择，保留原有商品，即使不在第一页；加载失败不得覆盖现有配置。失败重试保持输入，成功后重新读取服务器数据。Product Design image-to-code 使用用户 1.png 和 artifact-template-crm retained reference；复用共享选择/表单样式，不新增框架。

## GitHub 参考
已检索 GitHub referral/loyalty purchase enrollment；不采用搜索结果中的营销规则作为业务合同。共享组件治理参考 https://github.com/ant-design/ant-design/blob/master/DESIGN.md ，只借鉴语义状态与一致性，不引入依赖。实际行为以本仓稳定 Port、用户规则及生产证据为准。

## 验收
真实 PostgreSQL 验证新增/更新后配置读回、无战队直接加入及邀请加入、个人排行；管理页面验证创建/编辑/刷新、失败重试、核心配置锁定、已开始文案编辑。预检、编译、专项、GitHub CI、部署、生产读回分开记录。

## 本轮验证与边界
- 生产确认商品编号 122331 对应 products.id=51、名称测试、价格 990 分。尚未改写活动 2 的历史配置。
- PostgreSQL 专项包含个人直接加入、无战队接受邀请、个人积分排行，以及商品配置新增/更新后读回。
- JSDOM 验证完整页直接访问、商品 ID/编号映射、已开始文案保存时原始时间秒不变。
- fast、compile、Referral/同壳专项、共享商品选择测试、TypeScript 与前端构建已通过局部检查。未宣称完整 CI 或生产验收。
- 主干 package.json 缺少已被 dataWorkspace 引用的 echarts/tabulator 依赖；补齐固定版本及类型定义以恢复干净安装后的构建。
- 实际 CRM 壳视觉预览使用本地只读商品 fixture，不连接生产支付；与真实 PostgreSQL 业务验证分别计证据。
