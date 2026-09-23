# 03/05共用HXC事实恢复合同

本文件细化PRD03第14节和PRD05第14节已要求的旧字段，不是新增板块或看板设计。实现由一位执行者独占HXC Provider/domain/store文件，Product与Segment仅通过稳定读Port消费。根只写需求、审查和集成。

## 1. 可复用实现与真实来源

V3基线d6的internal/hxcdashboard/provider/mysql.go已有受控MySQL只读源、Preflight、批量ReadSnapshot，现有River刷新将带作用域身份经OneID解析后发布PostgreSQL快照。继续使用此链，不新建HXC同步器，不使用旧CRM作为运行数据源，不复制旧手机号/UnionID匹配SQL。

旧会员表事实来源为dd8 `aicrm_next/channels/integration_gateway/huangyoucan_usage_client.py`：first_login_at判正式登录；未删除消息total_tokens>0判Token使用；path progress在active/done/paused中active优先、updated_at及id倒序选最新，按lesson path item总数限制current_seq；card_open_log按上海七日窗口求次数/最近打开。sessions_7d/user_messages_7d/last_used_at不能充当这三个不同指标。

2026-09-05总控通过V3现有HXC只读连接执行information_schema元数据查询，确认这些源表/列均存在：users.first_login_at、messages.total_tokens/is_deleted、user_path_progress.id/user_id/path_id/current_seq/status/updated_at、lesson_path_items.path_id、card_open_log.user_id/opened_at。另确认memberships.status/start_date/end_date和subscriptions.tier/expires_at；subscriptions本身没有已证实的status列。仅查schema，无配置应用、原始身份或内容输出。

## 2. Owner与身份分类

涉及外部HXC身份与持久投影：只延用现有HXC Provider→Identity Port→canonical CustomerID的流程。禁止Product/Segment自己按外部身份串表匹配；pending/conflict继续不可归属。HXC拥有新字段及发布水位，消费者只读取已发布代、规范CustomerID及必要事实。一个Customer若对应多条不能明确选择的来源，返回明确未确定状态，不随机取一行或伪造为false/0。

0084归HXC，前向扩展现有投影以保留必要事实/可用性与来源证据，不改0028/0064历史迁移。旧投影没有新字段证据时为unavailable，不能用列默认false/0声称原事实已查明。新来源完成成功读取后才能区分真实false/0/null（例如没有学习计划）。发布与新摘要仍在现有原子代切换边界，批次失败保留上一可用代。

## 3. 03会员表读合同

按有界canonical CustomerID列表提供正式登录、Token使用、学习计划当前/总课数及状态、七日打开次数/最近打开、来源时间和可用性。只显示旧字段，原页面与排序筛选由03 Host/Owner适配；不新增HXC UI。Product不直接访问HXC表。续费次数由Order Owner单独交付，联盟来源由03按供体独立核对，不能由HXC猜测。

## 4. 05会员条件读合同

dd8 template_registry.py及hxc_projection_sql.py把会员、注册、真实使用分别建事实；dashboard stage是展示分类，不是原始membership_status。返回明确is_member、原membership_status、tier、expires_at、注册命中和使用命中/最近时间；来源缺失保留不可判定。保留现有Provider的明确用户/会员来源选择规则，先对照dd8会员来源字段，不把“free/无有效会员”标expired。

active需满足原is_member且未到期（原允许无到期但有明确会员依据），expired仅显式expired或expires_at<=reference。reference跨到期时不能沿用发布时stage。会员状态的原显式值不得统一改成active/expired；缺外部会员行不能伪造一条。注册存在与正式登录分开；Token使用与广义真实使用分开。原旧CRM可选历史事实需由已有历史Owner导入并通过Port协调，不能为复用旧代码而在线连旧CRM。

## 5. 验证与交付

- 旧字段逐项对照与受控源schema/必要EXPLAIN检查，Go Provider扫描/时区/空值/排序测试，PostgreSQL真实发布及读Port测试。
- 未登录、零Token、未删除Token记录、无计划、多计划优先顺序、current_seq边界、上海跨日七天、真实0与unavailable分别覆盖。
- active无到期但有明确证据、到期瞬间、free无会员、显式expired无到期、缺源/多源冲突、未匹配OneID不误出现在客户行。
- 已发布代→两消费者读相同事实；发布失败不部分暴露，旧代字段未加载不冒充已知。无Provider写入、无新增CRM客户、无生产刷新或迁移。
- 新Port、字段摘要/readiness、安装/迁移契约及准确PR HEAD交总控审核，再交03/05接线。

## 6. 首批源码与真实源复审

PR154@c6888a4未批准：同一现有只读连接中，MySQL8.0.43-34/+08:00，基线6b09查询EXPLAIN通过，新增查询返回1267文本collation不兼容；不修改生产源schema。共享会员状态/期限须来自同一已选会员来源，不以dashboard订阅期限混用。没有学习计划须保留nullable，不能默认0/0；读Port需要明确批量上限。实际SourceRow扫描、空值、来源排序与时区需可执行用例，不只检查SQL文字。所有修复仅代码与测试，未执行生产刷新。


后续真实源只读EXPLAIN：2846c79与b131fd8bac448a582f767e30d8aa3db095714721均通过（相同MySQL8.0.43-34、+08:00，无ANALYZE/刷新）。最终叶子方案按原始候选分别扫描mc、subscription、user_profile，Go内部以纯函数保留完整同源状态/期限：优先reference时有效候选，否则保留已有过期事实；原is_member证据与当前有效期判定分开。Token旧NULL删除标记按COALESCE(is_deleted,0)=0。纯函数反例覆盖过期mc不能压住有效订阅、只有用户会员、无期限与过期期限不得混用、free及未知不伪造会员。准确候选仍须通过CI及固定发布代保留/清理测试，方可交03/05消费。

分批共用读增加最小VersionedSharedFactsReader：先取得已发布代ID，再按同代每批≤500规范客户读；不能分批混入新代。旧代保留时允许继续读取，被现有保留策略删除事实后必须报明确版本不可用，不能将其当作真实无人匹配；不引入额外缓存、定时任务或长期引用保留机制。
