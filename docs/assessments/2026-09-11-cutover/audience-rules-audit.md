# 人群仅迁规则审计（2026-09-11）

遵循用户最新范围：不迁旧成员、不关联旧成员；旧375未解析成员不再是切流门槛。只读检查冻结 `audience.enc` 的38包/83版本与当前V3源码；未执行旧SQL，未建包、启用规则或恢复触达。冻结快照之后源规则是否变化仍需最终只读提取比对，不能称为当前实时源规则。

目标受保护证据位于 `/var/backups/aicrm/cutover-prep-20260911/`：

- `audience-rule-audit-complete.json`：38包全部定义与83版本规则，原snapshot/incremental/simple SQL、参数、依赖、版本状态及调度；不含成员表或样例成员。
- `audience-rules-reviewed.json`：6active包评估；仅37给可表达Definition，其余definition=null并记录原因。只供Preview，不直接启用。
- `audience-rule-audit.json`：当前版本简表；完整版本以complete文件为准。

## 6个active包

| 源包/当前版本 | 名称 | 精确旧条件 | V3等价性与缺口 |
|---|---|---|---|
|14/71|9.9渠道问卷激活AI|提交问卷21或37任一个；external非空；增量按submitted_at左闭右开水位及lookback。没有支付或active联系人限制。|现questionnaire_choice_answers要求首次完整提交且非空题目条件，不等价。需纯提交+多问卷OR，问卷ID映射经Survey Owner。|
|28/68|未注册黄小璨用户|指定负责人；联系人external非空；status为空或active；mobile_hash或unionid至少一个非空；HXC注册索引按两种身份LEFT JOIN均无命中。|现customer注册Port仅phone_masked非空即Registered，不是HXC索引匹配。需新系统HXC已知注册事实、未知状态及负责人映射，不能把Unknown当未注册。|
|30/83|首月体验已报名-HuangYouCan企微|paid+trade_state SUCCESS+paid_at非空；商品subscription_trial_month、prd_20260518095708_9f77db、prd_20260707050545_291025；external或mobile_hash关联任一active企微；微信支付与小店均计。|paid_order能表达商品/active条件，但旧裸mobilehash匹配不允许重建为身份旁路；必须新OneID已解析根及完整资金事实。禁止声称逐人等价。|
|31/74|黄小璨有效期内未进未报名高客单|HXC is_member=true且external非空；NOT指定群内external相同成员；NOT同external已支付SOAK-FDE-BUNDLE-S1、FDE-CAMP-S1、SOAK-QTR订单。|缺会员AND群排除AND订单排除封闭组合。GroupChatReader仅摘要/人数，不提供成员CustomerID事实。群引用保留受保护params，需同Corp核对群Owner数据。|
|37/81|报名商品编号202608121337的用户|商品202608121337，paid，paid_at>=2026-08-12T19:30:00+08:00，无上界，任一active企微联系人。|现paid_order可表达；owner_staff_ids=[]、owner_scope=all，paid_at_to为空串。仍需真实Preview证明历史订单、首次paid时间及新联系人事实覆盖。|
|38/82|填写55号问卷的用户|问卷55提交且external非空；submitted_at增量窗口，没有题目选项/联系人active要求。|需纯提交模板；55不能直接当V3主键，必须原source映射。|

28和31原每日02:00 Asia/Shanghai；增量包原间隔180秒，但事件水位语义不能仅改成每3分钟就宣称完全相同。新的重新圈选是客户集合，不重放旧事件、旧触达或旧event_source_key。

## 32归档包逐项

归档只保留逻辑与状态，不激活，不导入任何名单。下表简述包用途；**完整SQL/所有版本已逐包保留在受保护complete文件**，不是将自然语言当成SQL等价替代。

|源ID|名称|旧条件/版本情况|处理|
|---|---|---|---|
|1|生产测试：提交910001问卷且已加微-HuangYouCan|问卷910001提交且有external；5版本|保存原逻辑；纯提交模板及旧测试问卷来源待确认|
|2|生产全量测试生命周期|lifecycle测试；4版本|保存SQL，不推断生产条件|
|3|生产全量测试问卷910101|提交910101且已加微；2版本|保存逻辑，不激活|
|4|生产全量测试daily exited|每日退出测试；2版本|保存SQL，不重建历史退出名单|
|5|生产全量测试支付订单|支付成功且已加微；1版本|保存SQL，需支付Owner条件核对|
|6|生产全量测试渠道进入|渠道进入且已加微；1版本|保存SQL，旧渠道引用不可直接当新ID|
|7|missing secret|缺secret测试；1版本|仅保存原逻辑，不修造secret|
|8|PR1362回归：publish latest daily exit|发布/退出回归；23版本|全部保留，current指针保留|
|9|PR1362回归：subscription dedupe|订阅去重回归；5版本|保存逻辑，不恢复订阅效果|
|10|PR1368验证：每3分钟|无任何版本|明确空定义，不制造规则|
|11|PR1368验证：每日|无任何版本|明确空定义|
|12|PR1368验证：增量+每日|无任何版本|明确空定义|
|13|PR1368验证：归档不返回|无任何版本|明确空定义|
|15|提交 101 问卷且已加微|8版本但无current指向|保存全部历史版本，不选最新冒充生效|
|16|E2E questionnaire 自动发送测试|问卷+固定测试身份过滤|保留受保护逻辑，不移植身份过滤名单|
|17|E2E questionnaire 自动发送测试|同上|同上|
|18|E2E questionnaire 自动发送测试|同上|同上|
|19|E2E questionnaire 自动发送测试|同上|同上|
|20|E2E questionnaire 自动发送测试|同上|同上|
|21|E2E payment 自动发送测试|支付+固定测试身份过滤|保留逻辑，不恢复测试发送|
|22|E2E channel_entry 自动发送测试|渠道+固定测试身份过滤|保留逻辑，不恢复发送|
|23|E2E questionnaire 自动发送测试|问卷+固定测试身份过滤|保留逻辑，不恢复发送|
|24|E2E payment 自动发送测试|支付+固定测试身份过滤|保留逻辑，不恢复发送|
|25|E2E channel_entry 自动发送测试|渠道+固定测试身份过滤|保留逻辑，不恢复发送|
|26|E2E user_ops_batch_send 自动发送测试|批次发送测试+固定身份过滤|保留逻辑，不恢复批次/名单|
|27|指定企微群成员|1版本但无current指向；指定群外部联系人|保存原群引用/逻辑；缺封闭群成员事实模板|
|29|黄小璨会员已注册未使用人群|指定负责人、会员、手机号或union命中注册、无真实使用；2版本|member_usage_status近似字段存在，但HXC注册事实语义需严格对齐|
|32|AI助手单人任务临时入口 20260713100521|无版本；一次性单人任务已归档|空规则，不迁旧单人名单|
|33|生产验收-模板化人群包 canary|wecom_contact_registration、指定不存在负责人、active、注册any|结构有对应模板，但旧负责人不假映射，归档保存|
|34|E2E questionnaire 自动发送测试|问卷+固定测试身份过滤|保留受保护逻辑，不恢复发送|
|35|E2E questionnaire 自动发送测试|同上|同上|
|36|E2E questionnaire 自动发送测试|同上|同上|

## 现有Owner能力边界

- Segment封闭DSL有11种模板，不能运行旧任意SQL；业务旧模板入口为 `internal/segment/adapter/legacy_templates.go`，组合Customer/WeCom/Survey/Order/Channel/Radar/HXC稳定Port。
- `customer/store/postgres.go:AudienceRegistrationFacts`读取目录phone_masked，不能等同旧HXC注册索引。
- `order/store/audience_read.go:PaidAudienceOrders`只返回status=paid、payer非空及首次paid历史时间。新客户集合不能包含未归属资金的猜测客户。
- `wecom/port/group_directory.go:GroupChatReader`仅群摘要，没有可用于NOT成员判断的完整成员集合/新鲜度保证。
- 不迁名单后，源旧成员计数/375隔离数不再作为新规则Preview应相同的断言；验收应是条件正确、Owner数据来源明确、已知/未知区分、无旧名单复用、无触达效果。
