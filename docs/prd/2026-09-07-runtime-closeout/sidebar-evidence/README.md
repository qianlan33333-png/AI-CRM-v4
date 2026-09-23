# 侧边栏前置全仓核查与索引

完整文件读取不等于完整业务验收。本次固定旧 `dd8d60d` 与 V3 `3f5ea38`；全部 Git 跟踪 Blob 已读取与校验大小/SHA256，包含二进制和所有后缀；排除范围仅为非 Git 跟踪的构建缓存、运行配置及生产数据。旧 Python AST 解析失败为零。

## 能力对应关系

注册表以 14 个能力包组织 CRM，不将包数冒充用户功能数量。详细 JSON 包含 current_contexts、dependencies、source_files、commands_services、manifest_routes、page_assets 和 test_candidates；下面 V3 列是静态接入位置，不是已复刻声明。

| 旧能力包 | 旧来源/上下文 | manifest 路由条目 | V3 对应实现候选 |
|---|---|---:|---|
| core.platform | admin_auth, admin_config, admin_jobs, external_push, navigation_target, platform_foundation, release_governance, shared | 137 | internal/access, internal/config, internal/platform, internal/webshell |
| core.channels | auth_wecom, channel_entry, integration_gateway | 15 | internal/wecom, internal/channel, internal/openplatform |
| core.crm | customer_read_model, customer_tags, identity_contact, operation_members, owner_migration, sidebar_write | 73 | internal/customer, internal/identity, internal/sidebar |
| core.engagement | media_library, send_content, send_targets | 41 | internal/media, internal/outbound |
| core.automation | automation_engine, background_jobs, ops_enrollment | 94 | internal/automation, internal/groupops |
| core.insights | admin_read_model, data_health, delivery_lineage | 8 | internal/webshell |
| core.app | admin_console | 0 | internal/webshell |
| extension.ai | ai_assist, ai_audience_ops, automation_agents | 88 | internal/aiassistant |
| extension.commerce | commerce, public_product, service_period | 159 | internal/product, internal/order, internal/coupon |
| extension.forms | questionnaire | 42 | internal/survey |
| extension.archive | message_archive | 11 | internal/messagearchive |
| extension.radar | radar_links | 36 | internal/radar |
| extension.growth | cloud_orchestrator | 37 | internal/groupops |
| extension.hxc | class_user_management, hxc_dashboard, operation_cycles | 36 |  |

共享 manifest Owner `admin_shell` 两条归 core.app，`common_operation_members` 两条归 core.crm；已在 JSON 单列映射，不丢弃。领域页面可能在共享 admin_console，故另列 shared_frontend_candidates；按文件名/路由匹配的候选需运行装配确认。不能把零条“本领域目录模板”误判为无前端。

## 深入检查过的侧边栏链

1. 旧模板 data endpoints → workbench JS 渲染/请求 → sidebar_v2.py 读模型，以及 sidebar_write API/Application → profile repository 写入。
2. 旧 SDK config/agentConfig → getCurExternalContact、OAuth 自动回退 → owner token；V3 viewer session + scoped OneID bootstrap 与用户已确认的新版 SDK。
3. old source/industry/industry_description/needs_blockers_followup → 旧 unionid 画像表；V3 Customer SidebarProfileRead/Update 仅目录字段，目录同步会覆写 source，需独立画像业务字段。
4. old Questionnaire answer query by canonical UnionID → V3 CustomerSurveyAdapter → Survey CustomerHistory/Window；生产同客户记录有无及作用域已单独检查。
5. old claimable Coupon definitions → V3 CustomerCouponReader 领取记录：确认不是相同读取语义；领取副作用本 PR 不改。
6. old sendChatMessage news/image、封面与 Media thumbnail → V3 商品/素材会话动作；旧 JS 回调不等于外部送达，SDK 官方分享协议已由直接公开 HTML 正文补核（见 official-sendchat-verification.json），真实客户端另验。
7. old order/refund labels、periodic remarks、非聊天 timeline → V3 Order/Entitlement/readers；已按截图完整订单号匹配同一订单，确认 V3 历史记录退款事实缺失，不用前端猜测状态。
8. V3 chat_activity/other_staff_messages 的 tab 恢复、请求、渲染路径：正式 PRD 要求删除侧边栏完整链，不删 archive owner。

## 尚未完成的检查

- 本次未逐页运行旧仓全部 781 路由，也未宣称 V3 全仓业务等价。动态注册路由与测试候选仍须对应能力执行任务补运行证据。
- 截图订单已按完整订单号核准：旧全额退款 9900 分，V3 历史订单 refunded_minor=0。当前证据能确定事实缺口，尚未完成历史导入来源/时间截面追溯；此处不执行数据修复或退款。
- 官方分享文档提取工具失败后，2026-09-08 已直接读取公开 HTML 正文完成协议核对；客户端商品分享真实行为尚待受控企微验收。
- 不执行历史数据导入；旧 8 份答卷缺失与手机号 verified/declared 来源差异已隔离说明。

## 可复核文件

- legacy-tracked-inventory.json / v3-tracked-inventory.json：全文件路径、Git object、模式、后缀、字节、SHA256、binary 及失败列表。
- legacy-capability-route-map.json：14 包、所有 manifest 映射路由、704 处实际 Python 方法路由声明（含源码行），共享 Owner 和 V3 候选源码。
- sidebar-donor-manifest.json：17 个侧边栏具体供体/测试文件摘要。
- production-redacted-check.json：只读生产计数与可信等级，无客户原始标识、手机号或密钥。

扫描口径：git ls-tree -rz --full-tree 固定提交 → git cat-file --batch 读取每个 blob → SHA256/大小/扩展名；AST parse 旧全部 .py，提取 registry _spec 和 router decorators，静态 manifest 按 path/method/owner 归类。历史路由声明和最终组合路由不是同一分母。
