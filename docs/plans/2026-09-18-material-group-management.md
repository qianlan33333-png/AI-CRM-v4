# 素材库完整分组管理 PRD

## 判断
OneID: not involved，素材元数据不标识客户。
Persistence: local transaction，Media owns 分组、归属、收据、审计和 Outbox。
External Effects: not involved，不调用 Provider、不新增队列。

## 业务合同
图片、附件、小程序各自独立一级分组，单素材一个组。空组持久保留；新增、改名、删除，删除后素材移入未分组，不删除内容或引用。全部/未分组为固定入口。上传、创建、编辑选择所属分组；列表展示归属和当前页批量移动，切页/筛选清空选择。新增默认当前组。组名同类型唯一。

## 实现
Media 分组稳定 ID、类型、名称、版本；三类素材 nullable group_id。迁移原 category，保留兼容文本映射，所有旧写入口通过 Media-owned 兼容映射与 canonical group_id 同步。分组删除、归零、批量移动及幂等收据/审计同事务，锁与 CAS 防并发部分提交。新命令使用 ID，不通过名称标识分组。

参考：现有企微标签管理和 MaterialGroupSidebar；GitHub https://github.com/directus/directus ，https://docs.directus.io/user-guide/file-library/folders （删除目录保留素材）。复用 pageHeaderActions、标准管理端表单及弹窗。Product Design skill 当前不可用，未使用该插件。冻结 donor 不修改。

## 验收与发布
三类型真实 PG/浏览器 CRUD、空组、删除保留引用、上传归组、编辑、批量原子移动、重复提交、版本冲突、权限、跨类型拒绝；原 category 迁移数量与归属校验。回归附件 tab、刷新、商品选择器。fast、compile、专项、完整 CI。main 合并后本地 SSH；迁移前备份，部署后真实回读。

兼容适配登记：Owner=Media；`category` 字符串适配只用于现有读取/内部生成与旧客户端，统一经 Media-owned trigger 映射到稳定 group_id。替代路径为 group_id 命令；所有旧 category 写调用与消费协议迁移完成后移除名称写适配，保留只读名称投影。

接口：三类 `/api/admin/{image|attachment|miniprogram}-library/groups` GET/POST；`groups/{id}` PUT/DELETE；`group-moves` POST 接收 group_id 和 items[{id,expected_version}]，上限 200，整批事务；`group-members?ids=...` GET 读取当前页版本和归属，上限 200。沿用会话、CSRF 与 Idempotency-Key。只读响应 can_write 控制管理入口，服务端每次独立验证权限。
