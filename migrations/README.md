# Migrations

v3 从新的 Schema 基线开始，不复制 production 或 V2 的完整 migration 历史。每个领域迁移必须声明表 Owner、数据保留、回滚或 forward-only 策略，并通过真实 PostgreSQL 测试。

- `0063_identity_hxc_source_observations.sql`：Identity 所有的 HXC 主体、加密观察、不可变解析收据和人工冲突动作；forward-only，不自动建客或合并。
- `0064_hxc_dashboard_identity_v2.sql`：HXC 投影的双键匹配来源、原因码、Case/候选关联和 inspect/apply 运行模式；forward-only，升级时保留上一成功投影。
- `0065_channel_legacy_asset_retirement.sql`：Channel 旧资产在被新资产替换后保留已验证事实并允许标记退役；forward-only，不改变 Provider 验证状态。
- `0066_channel_welcome_intents.sql`：Channel 回调欢迎语意图与首次 20 秒发送期限；复用 External Effects/River，并仅扩展既有效果作业队列约束以隔离欢迎语。
- `0074_survey_external_operation_execution_facts.sql`：Survey 外推回执记录 External Effects sink 已知的调用、结果和尝试事实；旧/历史行保留 NULL（未知），forward-only，不以默认 false 覆盖历史事实。
- `0081_group_ops_webhook_unconfigured_reference.sql`：Group Ops 未配置 webhook 的空 reference 可由多个本地计划共享；非空 opaque reference 仍全局唯一；forward-only，不变更任何已配置 webhook。
- `0082_group_ops_history_import.sql`：Group Ops 的四类 V1 只读历史投影导入批次与逐行摘要/隔离收据；保留文本来源员工和群标识，不创建当前计划、任务或外部效果；forward-only。

- `0084_hxc_shared_facts.sql`：HXC既有发布投影的原登录、使用、学习、打开记录及会员来源字段；旧代明确未加载，不改变OneID归属或新增同步器。
- `0090_survey_oauth_state_redirect.sql`：修正 Survey OAuth state 的旧 redirect 正则，使既有 Host `/h5/all.html` 与 `/h5/one.html` 回跳可持久化；forward-only，不修改已保存 state 或身份事实。
- `0086_wecom_profile_primary_owner.sql`：WeCom 完整目录同步后从受信 follow 集合恢复主负责人事实；空集合保留旧值，旧存量在下一次完整同步前保持 unknown。
- `0091_survey_assessment_business_keys.sql`：修正 Survey 测评维度和类型业务键的 ASCII-only 约束，保留旧版中文、内部空格和斜杠键；forward-only，不转换既有键或放宽通用 opaque 标识。

- `0097_segment_audience_mutation_actor.sql`：Segment/Audience 将既有人工 `admin:<id>` 审计与幂等投影前向填充为 `actor_kind`/`actor_ref`，并支持可追溯的机器主体；不把 machine client 映射为管理员 ID。
- `0098_message_archive_historical_projection.sql`：Archive 保留旧 `archived_messages` 的 UnionID 和群名兼容投影；仅由授权 Archive 外部读取使用，源包装只保留 digest，不成为当前身份或第二消息流。
- `0099_survey_historical_external_projection.sql`：Survey 保留旧 `questionnaire_submissions.unionid` 的受保护历史读取投影及来源摘要；仅供授权外部问卷读取，绝不作为 OneID 匹配、建客或合并依据。

- `0100_ai_assistant_machine_actor.sql`：AI Assistant 为认证 machine:<client_id> 保留可审计创建者与审计引用；数值管理员投影只为兼容保留，机器主体绝不映射为管理员。

- `0150_channel_welcome_message_snapshots.sql`：Channel 拥有的欢迎语渲染密文快照与已接受 EER envelope 绑定；不增加队列或 Provider 写路径。
- `0168_survey_questionnaire_archive.sql`：Survey 将问卷归档作为保留定义、答卷、回执和审计事实的终态；默认列表和公共读取不再暴露归档问卷。
- `0169_survey_questionnaire_archive_receipts.sql`：Survey 为归档命令增加既有幂等收据类型；只扩展 Owner 生命周期，不改变任何历史或外部效果。
- `0175_customer_minimum_directory_projection.sql`：Customer 为所有既有 OneID 根补齐可搜索的最小目录投影；forward-only，不猜昵称、不读取 Provider、不改变身份归属或合并关系。
- `0176_survey_single_submission_claims.sql`：Survey 所有的问卷—canonical Customer 单次提交 claim；仅约束安装后新接受的提交，future-only，不回填、合并或删除历史答卷/claim。forward-only；如需修复只能新增迁移，不能通过回滚破坏已提交的答卷、审计、Outbox 或效果接受事实。
- `0177_survey_operation_legacy_parity.sql`：Survey 所有完成页文案/目标配置，Outbound 所有完成推送 endpoint；既有 `survey_operation_configurations` 行保留 opaque reference 并以安全默认值扩展，不回填或删除配置，新的 endpoint 表也不迁移历史 Provider 目标。forward-only；配置结构修复必须以追加迁移完成，不回滚或重写既有效果与运营配置事实。

- `0182_media_group_management.sql`：Media 独立分组、稳定归属 ID；回填三类素材旧 category，兼容写投影同事务同步，空组可保留。删除分组归零素材而不删除内容或引用。
