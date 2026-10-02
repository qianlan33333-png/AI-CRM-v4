# CLI 能力索引

由当前编译目录生成；绑定表示源码接口存在，权限、安装、Provider 就绪和验收分别核验。候选无可执行路径。

## 总览 / 经营总览

小板块：指标、付费明细。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.getAdminOverview | aicrm-cli overview overview get | human | customer | source_contract_only |
| admin.listAdminOverviewPaidRecords | aicrm-cli overview overview-paid-records list | human | customer | source_contract_only |

## 用户 / 用户激活 / 用户列表

小板块：列表、360详情、身份、敏感字段、同步。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.confirmOneIDMerge | aicrm-cli identity one-idmerge confirm | human | identity | source_contract_only |
| admin.createCustomerSyncRun | aicrm-cli customers customer-sync-run create | human | customer | source_contract_only |
| admin.getCustomer360 | aicrm-cli customers customer360 get | human | customer | source_contract_only |
| admin.getCustomerChatActivity | aicrm-cli customers customer-chat-activity get | human | customer | source_contract_only |
| admin.getCustomerDirectoryDetail | aicrm-cli customers customer-directory-detail get | human | customer | source_contract_only |
| admin.getCustomerOwners | aicrm-cli customers customer-owners get | human | customer | source_contract_only |
| admin.getCustomerSurveyAnswers | aicrm-cli customers customer-survey-answers get | human | customer | source_contract_only |
| admin.getCustomerSyncRun | aicrm-cli customers customer-sync-run get | human | customer | source_contract_only |
| admin.getCustomerTags | aicrm-cli customers customer-tags get | human | customer | source_contract_only |
| admin.getCustomerTimeline | aicrm-cli customers customer-timeline get | human | customer | source_contract_only |
| admin.getOneIDCustomer | aicrm-cli identity one-idcustomer get | human | identity | source_contract_only |
| admin.ignoreOneIDSourceConflict | aicrm-cli identity ignore-one-idsource-conflict invoke | human | identity | source_contract_only |
| admin.listCustomerSyncRuns | aicrm-cli customers customer-sync-runs list | human | customer | source_contract_only |
| admin.listCustomers | aicrm-cli customers customers list | human | customer | source_contract_only |
| admin.listOneIDConflicts | aicrm-cli identity one-idconflicts list | human | identity | source_contract_only |
| admin.listOneIDMergeCandidates | aicrm-cli identity one-idmerge-candidates list | human | identity | source_contract_only |
| admin.listOneIDSourceConflicts | aicrm-cli identity one-idsource-conflicts list | human | identity | source_contract_only |
| admin.resolveOneID | aicrm-cli identity one-id resolve | human | identity | source_contract_only |
| admin.revealCustomerPhone | aicrm-cli customers reveal-customer-phone invoke | human | customer | source_contract_only |
| admin.reverseOneIDMerge | aicrm-cli identity reverse-one-idmerge invoke | human | identity | source_contract_only |
| customer.activities.list | aicrm-cli customers customer-activities list | machine | customers | source_contract_only |
| customer.context.get | aicrm-cli customers customer-context get | machine | customers | source_contract_only |
| customer.detail.get | aicrm-cli customers customer-detail get | machine | customers | source_contract_only |
| customer.list | aicrm-cli customers customers list-customer-list | machine | customers | source_contract_only |
| customer.resolve | aicrm-cli customers customer resolve | machine | customers | source_contract_only |
| identity.get | aicrm-cli identity customer-identities get | machine | identity | source_contract_only |

## 用户 / 漏斗 / 数据看板

小板块：漏斗、分析、视图、分享、刷新。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.createHXCDashboardRefresh | aicrm-cli dashboard hxcdashboard-refresh create | human | hxc | source_contract_only |
| admin.deleteHXCWorkspaceShares | aicrm-cli dashboard hxcworkspace-shares delete | human | hxc | source_contract_only |
| admin.deleteHXCWorkspaceViews | aicrm-cli dashboard hxcworkspace-views delete | human | hxc | source_contract_only |
| admin.getHXCDashboardRefresh | aicrm-cli dashboard hxcdashboard-refresh get | human | hxc | source_contract_only |
| admin.getHXCDashboardSummary | aicrm-cli dashboard hxcdashboard-summary get | human | hxc | source_contract_only |
| admin.getHXCWorkspaceShares | aicrm-cli dashboard hxcworkspace-shares get | human | hxc | source_contract_only |
| admin.getHXCWorkspaceViews | aicrm-cli dashboard hxcworkspace-views get | human | hxc | source_contract_only |
| admin.postHXCWorkspaceShares | aicrm-cli dashboard post-hxcworkspace-shares invoke | human | hxc | source_contract_only |
| admin.postHXCWorkspaceViews | aicrm-cli dashboard post-hxcworkspace-views invoke | human | hxc | source_contract_only |
| admin.queryHXCDashboard | aicrm-cli dashboard query-hxcdashboard invoke | human | hxc | source_contract_only |

## 用户 / 企微标签管理

小板块：标签组、企业标签、客户标签、历史。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.archiveLegacyWecomTag | aicrm-cli wecom legacy-wecom-tag archive | human | wecom | source_contract_only |
| admin.archiveLegacyWecomTagGroup | aicrm-cli wecom legacy-wecom-tag-group archive | human | wecom | source_contract_only |
| admin.createLegacyWecomTag | aicrm-cli wecom legacy-wecom-tag create | human | wecom | source_contract_only |
| admin.createLegacyWecomTagGroup | aicrm-cli wecom legacy-wecom-tag-group create | human | wecom | source_contract_only |
| admin.createWeComContactDescriptionBackfill | aicrm-cli wecom we-com-contact-description-backfill create | human | wecom | source_contract_only |
| admin.createWeComCustomerAcquisitionLink | aicrm-cli wecom we-com-customer-acquisition-link create | human | wecom | source_contract_only |
| admin.createWeComUnionIDRefreshRun | aicrm-cli wecom we-com-union-idrefresh-run create | human | wecom | source_contract_only |
| admin.deleteWeComCustomerAcquisitionLink | aicrm-cli wecom we-com-customer-acquisition-link delete | human | wecom | source_contract_only |
| admin.getCustomerMessageArchiveMedia | aicrm-cli wecom customer-message-archive-media get | human | messagearchive | source_contract_only |
| admin.getLegacyWecomTag | aicrm-cli wecom legacy-wecom-tag get | human | wecom | source_contract_only |
| admin.getLegacyWecomTagExecutionGate | aicrm-cli wecom legacy-wecom-tag-execution-gate get | human | wecom | source_contract_only |
| admin.getLegacyWecomTagGroup | aicrm-cli wecom legacy-wecom-tag-group get | human | wecom | source_contract_only |
| admin.getLegacyWecomTagSyncStatus | aicrm-cli wecom legacy-wecom-tag-sync-status get | human | wecom | source_contract_only |
| admin.getWeComCallbackReceipt | aicrm-cli wecom we-com-callback-receipt get | human | wecom | source_contract_only |
| admin.getWeComContactDescriptionBackfill | aicrm-cli wecom we-com-contact-description-backfill get | human | wecom | source_contract_only |
| admin.getWeComContactDescriptionReadbackStatus | aicrm-cli wecom we-com-contact-description-readback-status get | human | wecom | source_contract_only |
| admin.getWeComCustomerAcquisitionLink | aicrm-cli wecom we-com-customer-acquisition-link get | human | wecom | source_contract_only |
| admin.getWeComCustomerProfile | aicrm-cli wecom we-com-customer-profile get | human | wecom | source_contract_only |
| admin.getWeComCustomerTagHistoryStatistics | aicrm-cli wecom we-com-customer-tag-history-statistics get | human | wecom | source_contract_only |
| admin.listCustomerMessageArchive | aicrm-cli wecom customer-message-archive list | human | messagearchive | source_contract_only |
| admin.listCustomerMessageArchiveStaff | aicrm-cli wecom customer-message-archive-staff list | human | messagearchive | source_contract_only |
| admin.listLegacyWecomTagGroups | aicrm-cli wecom legacy-wecom-tag-groups list | human | wecom | source_contract_only |
| admin.listLegacyWecomTags | aicrm-cli wecom legacy-wecom-tags list | human | wecom | source_contract_only |
| admin.listWeComCallbackReceipts | aicrm-cli wecom we-com-callback-receipts list | human | wecom | source_contract_only |
| admin.listWeComCustomerAcquisitionLinks | aicrm-cli wecom we-com-customer-acquisition-links list | human | wecom | source_contract_only |
| admin.listWeComCustomerTagHistory | aicrm-cli wecom we-com-customer-tag-history list | human | wecom | source_contract_only |
| admin.reconcileWeComCustomerAcquisitionLink | aicrm-cli wecom we-com-customer-acquisition-link reconcile | human | wecom | source_contract_only |
| admin.refreshWeComGroupMembership | aicrm-cli wecom we-com-group-membership refresh | human | wecom | source_contract_only |
| admin.requestLegacyWecomTagSync | aicrm-cli wecom legacy-wecom-tag-sync request | human | wecom | source_contract_only |
| admin.requestLegacyWecomTagSyncDue | aicrm-cli wecom legacy-wecom-tag-sync-due request | human | wecom | source_contract_only |
| admin.retryWeComCallbackReceipt | aicrm-cli wecom retry-we-com-callback-receipt invoke | human | wecom | source_contract_only |
| admin.scheduleWeComContactDescriptionReadback | aicrm-cli wecom schedule-we-com-contact-description-readback invoke | human | wecom | source_contract_only |
| admin.updateLegacyWecomTagGroupPatch | aicrm-cli wecom legacy-wecom-tag-group-patch update | human | wecom | source_contract_only |
| admin.updateLegacyWecomTagGroupPut | aicrm-cli wecom legacy-wecom-tag-group-put update | human | wecom | source_contract_only |
| admin.updateLegacyWecomTagPatch | aicrm-cli wecom legacy-wecom-tag-patch update | human | wecom | source_contract_only |
| admin.updateLegacyWecomTagPut | aicrm-cli wecom legacy-wecom-tag-put update | human | wecom | source_contract_only |
| admin.updateWeComCustomerAcquisitionLink | aicrm-cli wecom we-com-customer-acquisition-link update | human | wecom | source_contract_only |
| candidate.wecom.acquisition-customers | aicrm-cli wecom acquisition-customers invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.acquisition-quota | aicrm-cli wecom acquisition-quota invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.acquisition-statistics | aicrm-cli wecom acquisition-statistics invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.broadcast-remind-cancel | aicrm-cli wecom broadcast-remind-cancel invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.broadcast-results | aicrm-cli wecom broadcast-results invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.client-actions | aicrm-cli wecom client-actions invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.contact-statistics | aicrm-cli wecom contact-statistics invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.contact-ways | aicrm-cli wecom contact-ways invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.customer-broadcast | aicrm-cli wecom customer-broadcast invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.customer-strategies | aicrm-cli wecom customer-strategies invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.customer-welcome | aicrm-cli wecom customer-welcome invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.group-broadcast | aicrm-cli wecom group-broadcast invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.group-id-conversion | aicrm-cli wecom group-id-conversion invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.group-statistics | aicrm-cli wecom group-statistics invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.group-welcome | aicrm-cli wecom group-welcome invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.intercept-rules | aicrm-cli wecom intercept-rules invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.join-ways | aicrm-cli wecom join-ways invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.moment-records | aicrm-cli wecom moment-records invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.moment-strategies | aicrm-cli wecom moment-strategies invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.moments | aicrm-cli wecom moments invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.on-job-customer-transfer | aicrm-cli wecom on-job-customer-transfer invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.on-job-group-transfer | aicrm-cli wecom on-job-group-transfer invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.product-albums | aicrm-cli wecom product-albums invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.resigned-customer-transfer | aicrm-cli wecom resigned-customer-transfer invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.resigned-group-transfer | aicrm-cli wecom resigned-group-transfer invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.resigned-members | aicrm-cli wecom resigned-members invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.staff-external-profile | aicrm-cli wecom staff-external-profile invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.strategy-tags | aicrm-cli wecom strategy-tags invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| candidate.wecom.subscription-messages | aicrm-cli wecom subscription-messages invoke | 不可执行：Candidate capability; verify existing owner implementation and current Provider requirements before registering execution | wecom/outbound | research_only |
| chat.records.list | aicrm-cli wecom chat-records list | machine | wecom | source_contract_only |

## 运营 / 自动化运营

小板块：分组、人群包、规则、成员、刷新、核心产品、发送关联。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.archiveAIAudiencePackage | aicrm-cli audience aiaudience-package archive | human | audience | source_contract_only |
| admin.changeAdminCoreAssignment | aicrm-cli audience change-admin-core-assignment invoke | human | audience | source_contract_only |
| admin.copyAIAudiencePackage | aicrm-cli audience aiaudience-package copy | human | audience | source_contract_only |
| admin.createAIAudiencePackage | aicrm-cli audience aiaudience-package create | human | audience | source_contract_only |
| admin.createAIAudiencePackageGroup | aicrm-cli audience aiaudience-package-group create | human | audience | source_contract_only |
| admin.createAIAudienceRefresh | aicrm-cli audience aiaudience-refresh create | human | audience | source_contract_only |
| admin.createAIAudienceRefreshRun | aicrm-cli audience aiaudience-refresh-run create | human | audience | source_contract_only |
| admin.createAudienceRun | aicrm-cli audience audience-run create | human | audience | source_contract_only |
| admin.deleteAIAudienceAutomationBinding | aicrm-cli audience aiaudience-automation-binding delete | human | audience | source_contract_only |
| admin.deleteAIAudiencePackageGroup | aicrm-cli audience aiaudience-package-group delete | human | audience | source_contract_only |
| admin.getAIAudienceAutomationBinding | aicrm-cli audience aiaudience-automation-binding get | human | audience | source_contract_only |
| admin.getAIAudienceConfigurationVersion | aicrm-cli audience aiaudience-configuration-version get | human | audience | source_contract_only |
| admin.getAIAudiencePackage | aicrm-cli audience aiaudience-package get | human | audience | source_contract_only |
| admin.getAIAudiencePackageSenders | aicrm-cli audience aiaudience-package-senders get | human | audience | source_contract_only |
| admin.getAIAudienceRefreshRun | aicrm-cli audience aiaudience-refresh-run get | human | audience | source_contract_only |
| admin.getAdminCoreMemberOperations | aicrm-cli audience core-member-operations get | human | audience | source_contract_only |
| admin.getAdminCoreOperatingPrompt | aicrm-cli audience core-operating-prompt get | human | audience | source_contract_only |
| admin.getAdminCoreRecommendation | aicrm-cli audience core-recommendation get | human | audience | source_contract_only |
| admin.listAIAudiencePackageGroups | aicrm-cli audience aiaudience-package-groups list | human | audience | source_contract_only |
| admin.listAIAudiencePackageMembers | aicrm-cli audience aiaudience-package-members list | human | audience | source_contract_only |
| admin.listAIAudiencePackages | aicrm-cli audience aiaudience-packages list | human | audience | source_contract_only |
| admin.listAIAudienceTemplates | aicrm-cli audience aiaudience-templates list | human | audience | source_contract_only |
| admin.listAdminCoreMemberHistory | aicrm-cli audience core-member-history list | human | audience | source_contract_only |
| admin.listAdminCoreOperatingProducts | aicrm-cli audience core-operating-products list | human | audience | source_contract_only |
| admin.listAdminCorePromptHistory | aicrm-cli audience core-prompt-history list | human | audience | source_contract_only |
| admin.materializeAIAudienceConfiguration | aicrm-cli audience materialize-aiaudience-configuration invoke | human | audience | source_contract_only |
| admin.precheckAIAudiencePackage | aicrm-cli audience precheck-aiaudience-package invoke | human | audience | source_contract_only |
| admin.previewAIAudienceConfiguration | aicrm-cli audience aiaudience-configuration preview | human | audience | source_contract_only |
| admin.previewAudienceBroadcast | aicrm-cli audience audience-broadcast preview | human | audience | source_contract_only |
| admin.putAIAudienceAutomationBinding | aicrm-cli audience put-aiaudience-automation-binding invoke | human | audience | source_contract_only |
| admin.putAIAudienceConfigurationVersion | aicrm-cli audience put-aiaudience-configuration-version invoke | human | audience | source_contract_only |
| admin.recordAdminCorePush | aicrm-cli audience core-push record | human | audience | source_contract_only |
| admin.replaceAIAudiencePackageSenders | aicrm-cli audience replace-aiaudience-package-senders invoke | human | audience | source_contract_only |
| admin.requestAdminCoreRecommendations | aicrm-cli audience core-recommendations request | human | audience | source_contract_only |
| admin.saveAdminCoreOperatingProduct | aicrm-cli audience core-operating-product save | human | audience | source_contract_only |
| admin.saveAdminCoreOperatingPrompt | aicrm-cli audience core-operating-prompt save | human | audience | source_contract_only |
| admin.transitionAIAudiencePackage | aicrm-cli audience transition-aiaudience-package invoke | human | audience | source_contract_only |
| admin.updateAIAudiencePackage | aicrm-cli audience aiaudience-package update | human | audience | source_contract_only |
| admin.updateAIAudiencePackageGroup | aicrm-cli audience aiaudience-package-group update | human | audience | source_contract_only |
| audience.core_products.list | aicrm-cli audience audience-core-products list | machine | audience | source_contract_only |
| audience.member.history.list | aicrm-cli audience audience-member-history list | machine | audience | source_contract_only |
| audience.member.operations.get | aicrm-cli audience audience-member-operations get | machine | audience | source_contract_only |
| audience.members.list | aicrm-cli audience audience-members list | machine | audience | source_contract_only |
| audience.push.record | aicrm-cli audience audience-push record | machine | audience | source_contract_only |

## 运营 / 自动化话术

小板块：资产、提示词、固定内容、策略、运行。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.activateAutomationAgent | aicrm-cli automation automation-agent activate | human | automation | source_contract_only |
| admin.archiveAutomationAgent | aicrm-cli automation automation-agent archive | human | automation | source_contract_only |
| admin.cancelAutomationRun | aicrm-cli automation automation-run cancel | human | automation | source_contract_only |
| admin.copyAutomationAgent | aicrm-cli automation automation-agent copy | human | automation | source_contract_only |
| admin.createAutomationAgent | aicrm-cli automation automation-agent create | human | automation | source_contract_only |
| admin.createAutomationPolicy | aicrm-cli automation automation-policy create | human | automation | source_contract_only |
| admin.getAutomationAgent | aicrm-cli automation automation-agent get | human | automation | source_contract_only |
| admin.getAutomationPolicy | aicrm-cli automation automation-policy get | human | automation | source_contract_only |
| admin.getAutomationRun | aicrm-cli automation automation-run get | human | automation | source_contract_only |
| admin.getAutomationRunEffectReconciliationCandidate | aicrm-cli automation automation-run-effect-reconciliation-candidate get | human | automation | source_contract_only |
| admin.listAutomationAgents | aicrm-cli automation automation-agents list | human | automation | source_contract_only |
| admin.listAutomationPolicies | aicrm-cli automation automation-policies list | human | automation | source_contract_only |
| admin.listAutomationRunRecipients | aicrm-cli automation automation-run-recipients list | human | automation | source_contract_only |
| admin.listAutomationRuns | aicrm-cli automation automation-runs list | human | automation | source_contract_only |
| admin.pauseAutomationAgent | aicrm-cli automation automation-agent pause | human | automation | source_contract_only |
| admin.precheckAutomationAgent | aicrm-cli automation precheck-automation-agent invoke | human | automation | source_contract_only |
| admin.publishAutomationAgent | aicrm-cli automation automation-agent publish | human | automation | source_contract_only |
| admin.reconcileAutomationRunEffect | aicrm-cli automation automation-run-effect reconcile | human | automation | source_contract_only |
| admin.saveAutomationAgentFixedContent | aicrm-cli automation automation-agent-fixed-content save | human | automation | source_contract_only |
| admin.transitionAutomationPolicy | aicrm-cli automation transition-automation-policy invoke | human | automation | source_contract_only |
| admin.updateAutomationAgent | aicrm-cli automation automation-agent update | human | automation | source_contract_only |
| admin.updateAutomationPolicy | aicrm-cli automation automation-policy update | human | automation | source_contract_only |

## 运营 / 运营闭环

小板块：策略、提案、动作、批次、报告。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.approveExcelOperationBatch | aicrm-cli cycles approve-excel-operation-batch invoke | human | operationcycle | source_contract_only |
| admin.createOperationCycleStrategy | aicrm-cli cycles operation-cycle-strategy create | human | operationcycle | source_contract_only |
| admin.decideOperationCycleStrategyChangeProposal | aicrm-cli cycles decide-operation-cycle-strategy-change-proposal invoke | human | operationcycle | source_contract_only |
| admin.getExcelOperationBatch | aicrm-cli cycles excel-operation-batch get | human | operationcycle | source_contract_only |
| admin.getExcelOperationBatchReport | aicrm-cli cycles excel-operation-batch-report get | human | operationcycle | source_contract_only |
| admin.getExcelOperationCover | aicrm-cli cycles excel-operation-cover get | human | operationcycle | source_contract_only |
| admin.getOperationCycleActionResult | aicrm-cli cycles operation-cycle-action-result get | human | operationcycle | source_contract_only |
| admin.getOperationCycleCurrentAction | aicrm-cli cycles operation-cycle-current-action get | human | operationcycle | source_contract_only |
| admin.getOperationCycleRun | aicrm-cli cycles operation-cycle-run get | human | operationcycle | source_contract_only |
| admin.getOperationCycleRunByStableOrdinal | aicrm-cli cycles operation-cycle-run-by-stable-ordinal get | human | operationcycle | source_contract_only |
| admin.getOperationCycleStrategy | aicrm-cli cycles operation-cycle-strategy get | human | operationcycle | source_contract_only |
| admin.listExcelOperationBatches | aicrm-cli cycles excel-operation-batches list | human | operationcycle | source_contract_only |
| admin.listOperationCycleRunVersions | aicrm-cli cycles operation-cycle-run-versions list | human | operationcycle | source_contract_only |
| admin.listOperationCycleRuns | aicrm-cli cycles operation-cycle-runs list | human | operationcycle | source_contract_only |
| admin.listOperationCycleStrategies | aicrm-cli cycles operation-cycle-strategies list | human | operationcycle | source_contract_only |
| admin.listOperationCycleStrategyChangeProposals | aicrm-cli cycles operation-cycle-strategy-change-proposals list | human | operationcycle | source_contract_only |
| admin.listOperationCycleStrategyVersions | aicrm-cli cycles operation-cycle-strategy-versions list | human | operationcycle | source_contract_only |
| admin.listOperationExcelBatchStrategySummaries | aicrm-cli cycles operation-excel-batch-strategy-summaries list | human | operationcycle | source_contract_only |
| admin.startOperationCycleAction | aicrm-cli cycles start-operation-cycle-action invoke | human | operationcycle | source_contract_only |
| admin.transitionOperationCycleStrategy | aicrm-cli cycles transition-operation-cycle-strategy invoke | human | operationcycle | source_contract_only |
| admin.updateOperationCycleStrategy | aicrm-cli cycles operation-cycle-strategy update | human | operationcycle | source_contract_only |
| admin.uploadExcelOperationCover | aicrm-cli cycles excel-operation-cover upload | human | operationcycle | source_contract_only |

## 运营 / 群运营计划

小板块：计划、员工、群、节点、内容包、执行。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.acceptGroupOpsRunDue | aicrm-cli group-ops accept-group-ops-run-due invoke | human | groupops | source_contract_only |
| admin.activateGroupOpsPlan | aicrm-cli group-ops group-ops-plan activate | human | groupops | source_contract_only |
| admin.addGroupOpsPlanGroup | aicrm-cli group-ops group-ops-plan-group add | human | groupops | source_contract_only |
| admin.addGroupOpsPlanGroupAsset | aicrm-cli group-ops group-ops-plan-group-asset add | human | groupops | source_contract_only |
| admin.addGroupOpsPlanMember | aicrm-cli group-ops group-ops-plan-member add | human | groupops | source_contract_only |
| admin.addGroupOpsPlanNode | aicrm-cli group-ops group-ops-plan-node add | human | groupops | source_contract_only |
| admin.archiveGroupOpsPlan | aicrm-cli group-ops group-ops-plan archive | human | groupops | source_contract_only |
| admin.bindGroupOpsContentPackage | aicrm-cli group-ops group-ops-content-package bind | human | groupops | source_contract_only |
| admin.createGroupOpsContentPackage | aicrm-cli group-ops group-ops-content-package create | human | groupops | source_contract_only |
| admin.createGroupOpsContentPackageVersion | aicrm-cli group-ops group-ops-content-package-version create | human | groupops | source_contract_only |
| admin.createGroupOpsPlan | aicrm-cli group-ops group-ops-plan create | human | groupops | source_contract_only |
| admin.deleteGroupOpsPlan | aicrm-cli group-ops group-ops-plan delete | human | groupops | source_contract_only |
| admin.disableGroupOpsPlan | aicrm-cli group-ops group-ops-plan disable | human | groupops | source_contract_only |
| admin.enableGroupOpsPlan | aicrm-cli group-ops group-ops-plan enable | human | groupops | source_contract_only |
| admin.getGroupOpsContentPackageBinding | aicrm-cli group-ops group-ops-content-package-binding get | human | groupops | source_contract_only |
| admin.getGroupOpsPlan | aicrm-cli group-ops group-ops-plan get | human | groupops | source_contract_only |
| admin.getGroupOpsWebhook | aicrm-cli group-ops group-ops-webhook get | human | groupops | source_contract_only |
| admin.getGroupOpsWebhookDescriptor | aicrm-cli group-ops group-ops-webhook-descriptor get | human | groupops | source_contract_only |
| admin.listGroupOpsDirectoryGroups | aicrm-cli group-ops group-ops-directory-groups list | human | groupops | source_contract_only |
| admin.listGroupOpsExecutions | aicrm-cli group-ops group-ops-executions list | human | groupops | source_contract_only |
| admin.listGroupOpsGroupPicker | aicrm-cli group-ops group-ops-group-picker list | human | groupops | source_contract_only |
| admin.listGroupOpsHistoryDirectory | aicrm-cli group-ops group-ops-history-directory list | human | groupops | source_contract_only |
| admin.listGroupOpsHistoryGroups | aicrm-cli group-ops group-ops-history-groups list | human | groupops | source_contract_only |
| admin.listGroupOpsHistoryNodes | aicrm-cli group-ops group-ops-history-nodes list | human | groupops | source_contract_only |
| admin.listGroupOpsHistoryPlans | aicrm-cli group-ops group-ops-history-plans list | human | groupops | source_contract_only |
| admin.listGroupOpsOperationMembers | aicrm-cli group-ops group-ops-operation-members list | human | groupops | source_contract_only |
| admin.listGroupOpsPlanGroupAssets | aicrm-cli group-ops group-ops-plan-group-assets list | human | groupops | source_contract_only |
| admin.listGroupOpsPlanGroups | aicrm-cli group-ops group-ops-plan-groups list | human | groupops | source_contract_only |
| admin.listGroupOpsPlanMembers | aicrm-cli group-ops group-ops-plan-members list | human | groupops | source_contract_only |
| admin.listGroupOpsPlanNodes | aicrm-cli group-ops group-ops-plan-nodes list | human | groupops | source_contract_only |
| admin.listGroupOpsPlans | aicrm-cli group-ops group-ops-plans list | human | groupops | source_contract_only |
| admin.patchGroupOpsContentPackage | aicrm-cli group-ops group-ops-content-package patch | human | groupops | source_contract_only |
| admin.pauseGroupOpsPlan | aicrm-cli group-ops group-ops-plan pause | human | groupops | source_contract_only |
| admin.previewGroupOpsContentPackage | aicrm-cli group-ops group-ops-content-package preview | human | groupops | source_contract_only |
| admin.previewGroupOpsPlanContent | aicrm-cli group-ops group-ops-plan-content preview | human | groupops | source_contract_only |
| admin.previewGroupOpsRunDue | aicrm-cli group-ops group-ops-run-due preview | human | groupops | source_contract_only |
| admin.putGroupOpsPlan | aicrm-cli group-ops put-group-ops-plan invoke | human | groupops | source_contract_only |
| admin.putGroupOpsPlanNode | aicrm-cli group-ops put-group-ops-plan-node invoke | human | groupops | source_contract_only |
| admin.putGroupOpsWebhookDescriptor | aicrm-cli group-ops put-group-ops-webhook-descriptor invoke | human | groupops | source_contract_only |
| admin.readGroupOpsExecutionDelivery | aicrm-cli group-ops read-group-ops-execution-delivery invoke | human | groupops | source_contract_only |
| admin.readGroupOpsPlanExecutionDelivery | aicrm-cli group-ops read-group-ops-plan-execution-delivery invoke | human | groupops | source_contract_only |
| admin.reconcileGroupOpsExecution | aicrm-cli group-ops group-ops-execution reconcile | human | groupops | source_contract_only |
| admin.reconcileGroupOpsPlanExecution | aicrm-cli group-ops group-ops-plan-execution reconcile | human | groupops | source_contract_only |
| admin.removeGroupOpsPlanGroup | aicrm-cli group-ops group-ops-plan-group remove | human | groupops | source_contract_only |
| admin.removeGroupOpsPlanGroupAsset | aicrm-cli group-ops group-ops-plan-group-asset remove | human | groupops | source_contract_only |
| admin.removeGroupOpsPlanMember | aicrm-cli group-ops group-ops-plan-member remove | human | groupops | source_contract_only |
| admin.removeGroupOpsPlanNode | aicrm-cli group-ops group-ops-plan-node remove | human | groupops | source_contract_only |
| admin.syncGroupOpsDirectoryGroups | aicrm-cli group-ops sync-group-ops-directory-groups invoke | human | groupops | source_contract_only |
| admin.syncGroupOpsDirectoryGroupsAlias | aicrm-cli group-ops sync-group-ops-directory-groups-alias invoke | human | groupops | source_contract_only |
| admin.syncGroupOpsGroupPicker | aicrm-cli group-ops sync-group-ops-group-picker invoke | human | groupops | source_contract_only |
| admin.syncGroupOpsOperationMembers | aicrm-cli group-ops sync-group-ops-operation-members invoke | human | groupops | source_contract_only |
| admin.updateGroupOpsContentPackage | aicrm-cli group-ops group-ops-content-package update | human | groupops | source_contract_only |
| admin.updateGroupOpsPlan | aicrm-cli group-ops group-ops-plan update | human | groupops | source_contract_only |
| admin.updateGroupOpsPlanNode | aicrm-cli group-ops group-ops-plan-node update | human | groupops | source_contract_only |

## 运营 / 群邀请

小板块：计划、版本、二维码、群目录、入群方式。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.archiveMediaGroupInvite | aicrm-cli group-invitations media-group-invite archive | human | groupinvitation | source_contract_only |
| admin.createMediaGroupInvite | aicrm-cli group-invitations media-group-invite create | human | groupinvitation | source_contract_only |
| admin.getMediaGroupInvite | aicrm-cli group-invitations media-group-invite get | human | groupinvitation | source_contract_only |
| admin.listMediaGroupInvites | aicrm-cli group-invitations media-group-invites list | human | groupinvitation | source_contract_only |
| admin.updateMediaGroupInvite | aicrm-cli group-invitations media-group-invite update | human | groupinvitation | source_contract_only |

## 运营 / 渠道码中心

小板块：渠道、负责人、欢迎语、资产、归因、统计。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.createChannel | aicrm-cli channels channel create | human | channel | source_contract_only |
| admin.deleteChannelContactWay | aicrm-cli channels channel-contact-way delete | human | channel | source_contract_only |
| admin.downloadChannelQRCode | aicrm-cli channels channel-qrcode download | human | channel | source_contract_only |
| admin.generateChannelQRCode | aicrm-cli channels generate-channel-qrcode invoke | human | channel | source_contract_only |
| admin.getChannel | aicrm-cli channels channel get | human | channel | source_contract_only |
| admin.getChannelAcquisitionAsset | aicrm-cli channels channel-acquisition-asset get | human | channel | source_contract_only |
| admin.listChannelAcquisitionAssets | aicrm-cli channels channel-acquisition-assets list | human | channel | source_contract_only |
| admin.listChannelAcquisitionStaff | aicrm-cli channels channel-acquisition-staff list | human | channel | source_contract_only |
| admin.listChannelAssignees | aicrm-cli channels channel-assignees list | human | channel | source_contract_only |
| admin.listChannelContacts | aicrm-cli channels channel-contacts list | human | channel | source_contract_only |
| admin.listChannelHistory | aicrm-cli channels channel-history list | human | channel | source_contract_only |
| admin.listChannels | aicrm-cli channels channels list | human | channel | source_contract_only |
| admin.listUnassignedChannelEntrantReceipts | aicrm-cli channels unassigned-channel-entrant-receipts list | human | channel | source_contract_only |
| admin.previewChannelAcquisition | aicrm-cli channels channel-acquisition preview | human | channel | source_contract_only |
| admin.publishChannelAcquisitionAsset | aicrm-cli channels channel-acquisition-asset publish | human | channel | source_contract_only |
| admin.reconcileChannelAcquisitionAsset | aicrm-cli channels channel-acquisition-asset reconcile | human | channel | source_contract_only |
| admin.reconcileChannelEntrantReceipt | aicrm-cli channels channel-entrant-receipt reconcile | human | channel | source_contract_only |
| admin.updateChannel | aicrm-cli channels channel update | human | channel | source_contract_only |
| admin.updateChannelContactWay | aicrm-cli channels channel-contact-way update | human | channel | source_contract_only |

## 运营 / AI 助手

小板块：计划、名单、审阅、效果。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.approveAIAssistantPlan | aicrm-cli ai approve-aiassistant-plan invoke | human | aiplan | source_contract_only |
| admin.createAIAssistantPlan | aicrm-cli ai aiassistant-plan create | human | aiplan | source_contract_only |
| admin.createLegacyAIAssistantReviewPlan | aicrm-cli ai legacy-aiassistant-review-plan create | 不可执行：route requires an independent authentication protocol; human sessions cannot execute it; use the independently authorized machine V1 ai.review_plan.create operation | aiplan | source_contract_only |
| admin.getAIAssistantPlan | aicrm-cli ai aiassistant-plan get | human | aiplan | source_contract_only |
| admin.getAIAssistantRecipient | aicrm-cli ai aiassistant-recipient get | human | aiplan | source_contract_only |
| admin.listAIAssistantEffects | aicrm-cli ai aiassistant-effects list | human | aiplan | source_contract_only |
| admin.listAIAssistantPlans | aicrm-cli ai aiassistant-plans list | human | aiplan | source_contract_only |
| admin.listAIAssistantRecipients | aicrm-cli ai aiassistant-recipients list | human | aiplan | source_contract_only |
| admin.previewAIAssistantPlanApproval | aicrm-cli ai aiassistant-plan-approval preview | human | aiplan | source_contract_only |
| admin.reconcileAIAssistantEffect | aicrm-cli ai aiassistant-effect reconcile | human | aiplan | source_contract_only |
| admin.rejectAIAssistantPlan | aicrm-cli ai reject-aiassistant-plan invoke | human | aiplan | source_contract_only |
| admin.reviewAIAssistantRecipient | aicrm-cli ai review-aiassistant-recipient invoke | human | aiplan | source_contract_only |
| admin.updateAIAssistantRecipientContent | aicrm-cli ai aiassistant-recipient-content update | human | aiplan | source_contract_only |
| ai.review_plan.create | aicrm-cli ai ai-review-plan create | machine | ai | source_contract_only |

## 交易 / 交易管理

小板块：订单、支付、导入、退款、结账恢复。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.abandonInvalidLegacyCheckout | aicrm-cli payments abandon-invalid-legacy-checkout invoke | human | payment | source_contract_only |
| admin.allowReviewedLegacyCheckoutRestart | aicrm-cli payments allow-reviewed-legacy-checkout-restart invoke | human | payment | source_contract_only |
| admin.createAlipayRefundIntent | aicrm-cli payments alipay-refund-intent create | human | payment | source_contract_only |
| admin.createLegacyOrderExport | aicrm-cli orders legacy-order-export create | human | order | source_contract_only |
| admin.createLegacyWechatOrderExport | aicrm-cli orders legacy-wechat-order-export create | human | order | source_contract_only |
| admin.createLegacyWechatRefundIntent | aicrm-cli orders legacy-wechat-refund-intent create | human | order | source_contract_only |
| admin.createWechatShopRefundIntent | aicrm-cli refunds wechat-shop-refund-intent create | human | refund | source_contract_only |
| admin.executeOrderOnlyHistoryImport | aicrm-cli orders execute-order-only-history-import invoke | human | order | source_contract_only |
| admin.findRefundRecoveryReceipt | aicrm-cli refunds find-refund-recovery-receipt invoke | human | refund | source_contract_only |
| admin.getHistoricalPayment | aicrm-cli payments historical-payment get | human | payment | source_contract_only |
| admin.getLegacyOrder | aicrm-cli orders legacy-order get | human | order | source_contract_only |
| admin.getLegacyOrderItems | aicrm-cli orders legacy-order-items get | human | order | source_contract_only |
| admin.listLegacyAlipayTransactions | aicrm-cli payments legacy-alipay-transactions list | human | payment | source_contract_only |
| admin.listLegacyOrders | aicrm-cli orders legacy-orders list | human | order | source_contract_only |
| admin.listLegacyRefunds | aicrm-cli refunds legacy-refunds list | human | refund | source_contract_only |
| admin.listLegacyWechatOrderExternalEffects | aicrm-cli orders legacy-wechat-order-external-effects list | human | order | source_contract_only |
| admin.listLegacyWechatTransactions | aicrm-cli orders legacy-wechat-transactions list | human | order | source_contract_only |
| admin.previewLegacyOrderExport | aicrm-cli orders legacy-order-export preview | human | order | source_contract_only |
| admin.reconcileWechatPayPayment | aicrm-cli payments wechat-pay-payment reconcile | human | payment | source_contract_only |
| admin.reconcileWechatPayRefund | aicrm-cli refunds wechat-pay-refund reconcile | human | refund | source_contract_only |
| admin.reconcileWechatShopRefund | aicrm-cli refunds wechat-shop-refund reconcile | human | refund | source_contract_only |
| admin.requestWechatPaymentRefund | aicrm-cli payments wechat-payment-refund request | human | payment | source_contract_only |
| order.get | aicrm-cli orders order get | machine | orders | source_contract_only |
| order.list | aicrm-cli orders orders list | machine | orders | source_contract_only |

## 交易 / 商品管理

小板块：商品、字段、购买动作、分销规则、推送。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.copyLocalWechatPayProduct | aicrm-cli products local-wechat-pay-product copy | human | product | source_contract_only |
| admin.deleteLocalWechatPayProduct | aicrm-cli products local-wechat-pay-product delete | human | product | source_contract_only |
| admin.disableLocalWechatPayProduct | aicrm-cli products local-wechat-pay-product disable | human | product | source_contract_only |
| admin.enableLocalWechatPayProduct | aicrm-cli products local-wechat-pay-product enable | human | product | source_contract_only |
| admin.getWechatPayProductExternalPush | aicrm-cli products wechat-pay-product-external-push get | human | product | source_contract_only |
| admin.previewWechatPayProductExternalPush | aicrm-cli products wechat-pay-product-external-push preview | human | product | source_contract_only |
| admin.queueWechatPayProductExternalPushTest | aicrm-cli products queue-wechat-pay-product-external-push-test invoke | human | product | source_contract_only |
| admin.saveWechatPayProductExternalPush | aicrm-cli products wechat-pay-product-external-push save | human | product | source_contract_only |
| admin.shareLocalWechatPayProduct | aicrm-cli products share-local-wechat-pay-product invoke | human | product | source_contract_only |

## 交易 / 周期商品管理

小板块：商品、会员、期限、表格、视图、协作、分享。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.archiveServicePeriodProduct | aicrm-cli period-products service-period-product archive | human | serviceperiod | source_contract_only |
| admin.copyServicePeriodProduct | aicrm-cli period-products service-period-product copy | human | serviceperiod | source_contract_only |
| admin.createServicePeriodProduct | aicrm-cli period-products service-period-product create | human | serviceperiod | source_contract_only |
| admin.deleteProductWorkspaceShares | aicrm-cli period-products product-workspace-shares delete | human | serviceperiod | source_contract_only |
| admin.disableServicePeriodProduct | aicrm-cli period-products service-period-product disable | human | serviceperiod | source_contract_only |
| admin.enableServicePeriodProduct | aicrm-cli period-products service-period-product enable | human | serviceperiod | source_contract_only |
| admin.getProductWorkspaceShares | aicrm-cli period-products product-workspace-shares get | human | serviceperiod | source_contract_only |
| admin.getServicePeriodMemberGridAccessCompatibility | aicrm-cli period-products service-period-member-grid-access-compatibility get | human | serviceperiod | source_contract_only |
| admin.getServicePeriodMemberGridSchemaCompatibility | aicrm-cli period-products service-period-member-grid-schema-compatibility get | human | serviceperiod | source_contract_only |
| admin.getServicePeriodMemberGridShareSettingsCompatibility | aicrm-cli period-products service-period-member-grid-share-settings-compatibility get | human | serviceperiod | source_contract_only |
| admin.getServicePeriodProduct | aicrm-cli period-products service-period-product get | human | serviceperiod | source_contract_only |
| admin.getServicePeriodProductExternalPush | aicrm-cli period-products service-period-product-external-push get | human | serviceperiod | source_contract_only |
| admin.listServicePeriodMemberViewsCompatibility | aicrm-cli period-products service-period-member-views-compatibility list | human | serviceperiod | source_contract_only |
| admin.listServicePeriodMembersCompatibility | aicrm-cli period-products service-period-members-compatibility list | human | serviceperiod | source_contract_only |
| admin.listServicePeriodProducts | aicrm-cli period-products service-period-products list | human | serviceperiod | source_contract_only |
| admin.postProductWorkspaceShares | aicrm-cli period-products post-product-workspace-shares invoke | human | serviceperiod | source_contract_only |
| admin.previewServicePeriodProductExternalPush | aicrm-cli period-products service-period-product-external-push preview | human | serviceperiod | source_contract_only |
| admin.queueServicePeriodProductExternalPushTest | aicrm-cli period-products queue-service-period-product-external-push-test invoke | human | serviceperiod | source_contract_only |
| admin.saveServicePeriodProductExternalPush | aicrm-cli period-products service-period-product-external-push save | human | serviceperiod | source_contract_only |
| admin.shareServicePeriodProduct | aicrm-cli period-products share-service-period-product invoke | human | serviceperiod | source_contract_only |
| admin.updateServicePeriodMemberAlliance | aicrm-cli period-products service-period-member-alliance update | human | serviceperiod | source_contract_only |
| admin.updateServicePeriodProduct | aicrm-cli period-products service-period-product update | human | serviceperiod | source_contract_only |
| candidate.period-products.manual-renewal | aicrm-cli period-products manual-renewal invoke | 不可执行：No verified business write contract; ordinary field edits cannot implement this action | period-products | research_only |

## 交易 / 优惠券

小板块：配置、规则、领取、使用、统计。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.archiveCouponRule | aicrm-cli coupons coupon-rule archive | human | coupon | source_contract_only |
| admin.archiveCouponRuleLegacy | aicrm-cli coupons coupon-rule-legacy archive | human | coupon | source_contract_only |
| admin.copyCouponRule | aicrm-cli coupons coupon-rule copy | human | coupon | source_contract_only |
| admin.createCouponRule | aicrm-cli coupons coupon-rule create | human | coupon | source_contract_only |
| admin.getCouponRule | aicrm-cli coupons coupon-rule get | human | coupon | source_contract_only |
| admin.listCouponProductOptions | aicrm-cli coupons coupon-product-options list | human | coupon | source_contract_only |
| admin.listCouponRules | aicrm-cli coupons coupon-rules list | human | coupon | source_contract_only |
| admin.publishCouponRule | aicrm-cli coupons coupon-rule publish | human | coupon | source_contract_only |
| admin.stopCouponRule | aicrm-cli coupons coupon-rule stop | human | coupon | source_contract_only |
| admin.updateDraftCouponRule | aicrm-cli coupons draft-coupon-rule update | human | coupon | source_contract_only |

## 分销 / 分销管理

小板块：分销员、订单、佣金、结算、异常。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| candidate.distribution.payout | aicrm-cli distribution payout invoke | 不可执行：No verified business write contract; ordinary field edits cannot implement this action | distribution | research_only |
| owner.distribution.disable-distributor | aicrm-cli distribution distributor disable | human | distribution | source_handler_only |
| owner.distribution.distributor-orders | aicrm-cli distribution distributor-orders invoke | human | distribution | source_handler_only |
| owner.distribution.enable-distributor | aicrm-cli distribution distributor enable | human | distribution | source_handler_only |
| owner.distribution.get-distributors | aicrm-cli distribution distributors get | human | distribution | source_handler_only |
| owner.distribution.get-exceptions | aicrm-cli distribution exceptions get | human | distribution | source_handler_only |
| owner.distribution.get-orders | aicrm-cli distribution orders get | human | distribution | source_handler_only |
| owner.distribution.list-distributors | aicrm-cli distribution distributors list | human | distribution | source_handler_only |
| owner.distribution.list-exceptions | aicrm-cli distribution exceptions list | human | distribution | source_handler_only |
| owner.distribution.list-orders | aicrm-cli distribution orders list | human | distribution | source_handler_only |
| owner.distribution.merchant-liabilities | aicrm-cli distribution merchant-liabilities invoke | human | distribution | source_handler_only |
| owner.distribution.reconcile | aicrm-cli distribution reconcile invoke | human | distribution | source_handler_only |
| owner.distribution.recoveries | aicrm-cli distribution recoveries invoke | human | distribution | source_handler_only |

## 分销 / 裂变活动

小板块：活动、海报、团队、参与、邀请、奖励、纠错。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| owner.referral.create-campaign | aicrm-cli referral campaign create | human | referral | source_handler_only |
| owner.referral.create-reward | aicrm-cli referral reward create | human | referral | source_handler_only |
| owner.referral.create-team | aicrm-cli referral team create | human | referral | source_handler_only |
| owner.referral.export-campaign | aicrm-cli referral campaign export | human | referral | source_handler_only |
| owner.referral.get-campaign | aicrm-cli referral campaign get | human | referral | source_handler_only |
| owner.referral.list-campaigns | aicrm-cli referral campaigns list | human | referral | source_handler_only |
| owner.referral.list-invitations | aicrm-cli referral invitations list | human | referral | source_handler_only |
| owner.referral.list-participants | aicrm-cli referral participants list | human | referral | source_handler_only |
| owner.referral.list-product-options | aicrm-cli referral product-options list | human | referral | source_handler_only |
| owner.referral.list-referrals | aicrm-cli referral referrals list | human | referral | source_handler_only |
| owner.referral.list-relationship-history | aicrm-cli referral relationship-history list | human | referral | source_handler_only |
| owner.referral.list-rewards | aicrm-cli referral rewards list | human | referral | source_handler_only |
| owner.referral.participant-invitations | aicrm-cli referral participant-invitations invoke | human | referral | source_handler_only |
| owner.referral.reverse-participation | aicrm-cli referral reverse-participation invoke | human | referral | source_handler_only |
| owner.referral.revoke-invitation | aicrm-cli referral invitation revoke | human | referral | source_handler_only |
| owner.referral.set-posters | aicrm-cli referral posters set | human | referral | source_handler_only |
| owner.referral.set-state | aicrm-cli referral state set | human | referral | source_handler_only |
| owner.referral.update-campaign | aicrm-cli referral campaign update | human | referral | source_handler_only |

## 内容素材 / 问卷

小板块：设计、发布、答卷、分析、导出、联动。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.archiveSurveyQuestionnaire | aicrm-cli questionnaires survey-questionnaire archive | human | survey | source_contract_only |
| admin.createSurveyExternalPushTest | aicrm-cli questionnaires survey-external-push-test create | human | survey | source_contract_only |
| admin.createSurveyQuestionnaire | aicrm-cli questionnaires survey-questionnaire create | human | survey | source_contract_only |
| admin.createSurveySubmissionExternalPush | aicrm-cli questionnaires survey-submission-external-push create | human | survey | source_contract_only |
| admin.disablePublicSurveyDefinition | aicrm-cli questionnaires public-survey-definition disable | human | survey | source_contract_only |
| admin.disableSurveyQuestionnaire | aicrm-cli questionnaires survey-questionnaire disable | human | survey | source_contract_only |
| admin.duplicateSurveyQuestionnaire | aicrm-cli questionnaires duplicate-survey-questionnaire invoke | human | survey | source_contract_only |
| admin.enableSurveyQuestionnaire | aicrm-cli questionnaires survey-questionnaire enable | human | survey | source_contract_only |
| admin.exportSurveySubmissions | aicrm-cli questionnaires survey-submissions export | human | survey | source_contract_only |
| admin.getSurveyAnalysis | aicrm-cli questionnaires survey-analysis get | human | survey | source_contract_only |
| admin.getSurveyOperations | aicrm-cli questionnaires survey-operations get | human | survey | source_contract_only |
| admin.getSurveyPreflight | aicrm-cli questionnaires survey-preflight get | human | survey | source_contract_only |
| admin.getSurveyPublicAnalytics | aicrm-cli questionnaires survey-public-analytics get | human | survey | source_contract_only |
| admin.getSurveyQuestionnaire | aicrm-cli questionnaires survey-questionnaire get | human | survey | source_contract_only |
| admin.getSurveyResults | aicrm-cli questionnaires survey-results get | human | survey | source_contract_only |
| admin.getSurveySubmission | aicrm-cli questionnaires survey-submission get | human | survey | source_contract_only |
| admin.getSurveySubmissionExternalPush | aicrm-cli questionnaires survey-submission-external-push get | human | survey | source_contract_only |
| admin.getSurveyUnresolvedHistory | aicrm-cli questionnaires survey-unresolved-history get | human | survey | source_contract_only |
| admin.listSurveyQuestionnaires | aicrm-cli questionnaires survey-questionnaires list | human | survey | source_contract_only |
| admin.listSurveySubmissions | aicrm-cli questionnaires survey-submissions list | human | survey | source_contract_only |
| admin.listSurveyUnresolvedAnswers | aicrm-cli questionnaires survey-unresolved-answers list | human | survey | source_contract_only |
| admin.listSurveyUnresolvedHistory | aicrm-cli questionnaires survey-unresolved-history list | human | survey | source_contract_only |
| admin.previewSurveyExport | aicrm-cli questionnaires survey-export preview | human | survey | source_contract_only |
| admin.publishSurveyDefinition | aicrm-cli questionnaires survey-definition publish | human | survey | source_contract_only |
| admin.reconcileSurveySubmissionExternalPush | aicrm-cli questionnaires survey-submission-external-push reconcile | human | survey | source_contract_only |
| admin.replaceSurveyQuestionnaire | aicrm-cli questionnaires replace-survey-questionnaire invoke | human | survey | source_contract_only |
| admin.saveSurveyCompletionOperation | aicrm-cli questionnaires survey-completion-operation save | human | survey | source_contract_only |
| admin.saveSurveyExternalPushOperation | aicrm-cli questionnaires survey-external-push-operation save | human | survey | source_contract_only |
| questionnaire.submissions.list | aicrm-cli questionnaires questionnaire-submissions list | machine | questionnaires | source_contract_only |

## 内容素材 / 内容雷达

小板块：链接、访问、访客、统计、导出。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.createRadarLinkV3 | aicrm-cli radar radar-link-v3 create | human | radar | source_contract_only |
| admin.disableRadarLinkV3 | aicrm-cli radar radar-link-v3 disable | human | radar | source_contract_only |
| admin.enableRadarLinkV3 | aicrm-cli radar radar-link-v3 enable | human | radar | source_contract_only |
| admin.exportRadarEventsV3 | aicrm-cli radar radar-events-v3 export | human | radar | source_contract_only |
| admin.exportRadarVisitorsV3 | aicrm-cli radar radar-visitors-v3 export | human | radar | source_contract_only |
| admin.getRadarLinkV3 | aicrm-cli radar radar-link-v3 get | human | radar | source_contract_only |
| admin.getRadarOptionsV3 | aicrm-cli radar radar-options-v3 get | human | radar | source_contract_only |
| admin.getRadarShareV3 | aicrm-cli radar radar-share-v3 get | human | radar | source_contract_only |
| admin.getRadarStatsV3 | aicrm-cli radar radar-stats-v3 get | human | radar | source_contract_only |
| admin.listRadarEventsV3 | aicrm-cli radar radar-events-v3 list | human | radar | source_contract_only |
| admin.listRadarLinksV3 | aicrm-cli radar radar-links-v3 list | human | radar | source_contract_only |
| admin.listRadarVisitorsV3 | aicrm-cli radar radar-visitors-v3 list | human | radar | source_contract_only |
| admin.updateRadarLinkV3 | aicrm-cli radar radar-link-v3 update | human | radar | source_contract_only |
| radar.clicks.list | aicrm-cli radar radar-clicks list | machine | radar | source_contract_only |
| radar.links.list | aicrm-cli radar radar-links list | machine | radar | source_contract_only |

## 内容素材 / 素材库

小板块：图片、小程序、附件、分片上传、邀请素材、媒体租约。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.completeMediaAttachmentMultipartUpload | aicrm-cli materials complete-media-attachment-multipart-upload invoke | human | media | source_contract_only |
| admin.createMediaAttachment | aicrm-cli materials media-attachment create | human | media | source_contract_only |
| admin.createMediaImage | aicrm-cli materials media-image create | human | media | source_contract_only |
| admin.createMediaMiniProgram | aicrm-cli materials media-mini-program create | human | media | source_contract_only |
| admin.deleteMediaAttachment | aicrm-cli materials media-attachment delete | human | media | source_contract_only |
| admin.deleteMediaImage | aicrm-cli materials media-image delete | human | media | source_contract_only |
| admin.deleteMediaMiniProgram | aicrm-cli materials media-mini-program delete | human | media | source_contract_only |
| admin.downloadMediaAttachment | aicrm-cli materials media-attachment download | human | media | source_contract_only |
| admin.getMediaAttachment | aicrm-cli materials media-attachment get | human | media | source_contract_only |
| admin.getMediaImage | aicrm-cli materials media-image get | human | media | source_contract_only |
| admin.getMediaImageVariant | aicrm-cli materials media-image-variant get | human | media | source_contract_only |
| admin.getMediaMiniProgram | aicrm-cli materials media-mini-program get | human | media | source_contract_only |
| admin.getMediaPreparationRefreshRound | aicrm-cli materials media-preparation-refresh-round get | human | media | source_contract_only |
| admin.initiateMediaAttachmentMultipartUpload | aicrm-cli materials initiate-media-attachment-multipart-upload invoke | human | media | source_contract_only |
| admin.listAttachmentGroups | aicrm-cli materials attachment-groups list | human | media | source_contract_only |
| admin.listMediaAttachments | aicrm-cli materials media-attachments list | human | media | source_contract_only |
| admin.listMediaImageFacets | aicrm-cli materials media-image-facets list | human | media | source_contract_only |
| admin.listMediaImages | aicrm-cli materials media-images list | human | media | source_contract_only |
| admin.listMediaMiniPrograms | aicrm-cli materials media-mini-programs list | human | media | source_contract_only |
| admin.listMediaPreparations | aicrm-cli materials media-preparations list | human | media | source_contract_only |
| admin.listMiniProgramGroups | aicrm-cli materials mini-program-groups list | human | media | source_contract_only |
| admin.prepareMediaPreparation | aicrm-cli materials prepare-media-preparation invoke | human | media | source_contract_only |
| admin.putMediaAttachmentMultipartPart | aicrm-cli materials put-media-attachment-multipart-part invoke | human | media | source_contract_only |
| admin.refreshMediaPreparationRound | aicrm-cli materials media-preparation-round refresh | human | media | source_contract_only |
| admin.resolveLocalMiniProgramThumbnail | aicrm-cli materials local-mini-program-thumbnail resolve | human | media | source_contract_only |
| admin.setAttachmentGroup | aicrm-cli materials attachment-group set | human | media | source_contract_only |
| admin.setMiniProgramGroup | aicrm-cli materials mini-program-group set | human | media | source_contract_only |
| admin.updateMediaAttachment | aicrm-cli materials media-attachment update | human | media | source_contract_only |
| admin.updateMediaImage | aicrm-cli materials media-image update | human | media | source_contract_only |
| admin.updateMediaMiniProgram | aicrm-cli materials media-mini-program update | human | media | source_contract_only |
| admin.uploadLegacyMediaAttachment | aicrm-cli materials legacy-media-attachment upload | human | media | source_contract_only |
| admin.uploadLegacyMediaImage | aicrm-cli materials legacy-media-image upload | human | media | source_contract_only |

## 系统设置 / 负责人迁移

小板块：预览、确认、批次、CRM归属、企微接替。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.confirmCustomerOwnerHandoff | aicrm-cli owners customer-owner-handoff confirm | human | customer | source_contract_only |
| admin.getCustomerOwnerHandoffBatch | aicrm-cli owners customer-owner-handoff-batch get | human | customer | source_contract_only |
| admin.getCustomerOwnerHandoffPreview | aicrm-cli owners customer-owner-handoff-preview get | human | customer | source_contract_only |
| admin.getOwnerHandoffContext | aicrm-cli owners owner-handoff-context get | human | customer | source_contract_only |
| admin.previewCustomerOwnerHandoff | aicrm-cli owners customer-owner-handoff preview | human | customer | source_contract_only |
| admin.refreshCustomerOwnerHandoffTransferResult | aicrm-cli owners customer-owner-handoff-transfer-result refresh | human | customer | source_contract_only |

## 系统设置 / 运行治理

小板块：外部效果、任务、推送、巡检、问题、数据保留。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.attributeOpsGovernanceEpisode | aicrm-cli ops attribute-ops-governance-episode invoke | human | operations | source_contract_only |
| admin.cancelExternalEffect | aicrm-cli ops external-effect cancel | human | externaleffects | source_contract_only |
| admin.cancelPushCenterJob | aicrm-cli ops push-center-job cancel | human | pushcenter | source_contract_only |
| admin.captureOpsCPUProfile | aicrm-cli ops capture-ops-cpuprofile invoke | human | operations | source_contract_only |
| admin.downloadOpsCPUProfile | aicrm-cli ops ops-cpuprofile download | human | operations | source_contract_only |
| admin.getExternalEffect | aicrm-cli ops external-effect get | human | externaleffects | source_contract_only |
| admin.getExternalEffectDiagnostics | aicrm-cli ops external-effect-diagnostics get | human | externaleffects | source_contract_only |
| admin.getPushCenterJob | aicrm-cli ops push-center-job get | human | pushcenter | source_contract_only |
| admin.getPushCenterJobReconciliation | aicrm-cli ops push-center-job-reconciliation get | human | pushcenter | source_contract_only |
| admin.getPushCenterSections | aicrm-cli ops push-center-sections get | human | pushcenter | source_contract_only |
| admin.getPushCenterStats | aicrm-cli ops push-center-stats get | human | pushcenter | source_contract_only |
| admin.listExternalEffectJobs | aicrm-cli ops external-effect-jobs list | human | externaleffects | source_contract_only |
| admin.listExternalEffects | aicrm-cli ops external-effects list | human | externaleffects | source_contract_only |
| admin.listPushCenterJobs | aicrm-cli ops push-center-jobs list | human | pushcenter | source_contract_only |
| admin.opsCPUProfile | aicrm-cli ops ops-cpuprofile invoke | human | operations | source_contract_only |
| admin.opsCPUProfiles | aicrm-cli ops ops-cpuprofiles invoke | human | operations | source_contract_only |
| admin.opsDiagnosticClientEvent | aicrm-cli ops ops-diagnostic-client-event invoke | human | operations | source_contract_only |
| admin.opsDiagnostics | aicrm-cli ops ops-diagnostics invoke | human | operations | source_contract_only |
| admin.opsGovernanceEpisode | aicrm-cli ops ops-governance-episode invoke | human | operations | source_contract_only |
| admin.opsGovernanceEpisodes | aicrm-cli ops ops-governance-episodes invoke | human | operations | source_contract_only |
| admin.opsGovernanceOutcomes | aicrm-cli ops ops-governance-outcomes invoke | human | operations | source_contract_only |
| admin.opsInspectionCatalog | aicrm-cli ops ops-inspection-catalog invoke | human | operations | source_contract_only |
| admin.opsInspectionCommandDetail | aicrm-cli ops ops-inspection-command-detail invoke | human | operations | source_contract_only |
| admin.opsInspectionIssueUpdate | aicrm-cli ops ops-inspection-issue-update invoke | human | operations | source_contract_only |
| admin.opsInspectionOverview | aicrm-cli ops ops-inspection-overview invoke | human | operations | source_contract_only |
| admin.opsInspectionReports | aicrm-cli ops ops-inspection-reports invoke | human | operations | source_contract_only |
| admin.opsInspectionRun | aicrm-cli ops ops-inspection-run invoke | human | operations | source_contract_only |
| admin.opsInspectionRunDetail | aicrm-cli ops ops-inspection-run-detail invoke | human | operations | source_contract_only |
| admin.opsRetentionPolicies | aicrm-cli ops ops-retention-policies invoke | human | operations | source_contract_only |
| admin.opsRetentionPreview | aicrm-cli ops ops-retention-preview invoke | human | operations | source_contract_only |
| admin.opsRetentionResources | aicrm-cli ops ops-retention-resources invoke | human | operations | source_contract_only |
| admin.opsRetentionRuns | aicrm-cli ops ops-retention-runs invoke | human | operations | source_contract_only |
| admin.reconcileExternalEffect | aicrm-cli ops external-effect reconcile | human | externaleffects | source_contract_only |
| admin.retryExternalEffect | aicrm-cli ops retry-external-effect invoke | human | externaleffects | source_contract_only |
| admin.retryPushCenterJob | aicrm-cli ops retry-push-center-job invoke | human | pushcenter | source_contract_only |
| operation.get | aicrm-cli operations operation-status get | machine | operations | source_contract_only |

## 系统设置 / 配置

小板块：应用、模型、配置版本、账号权限。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| admin.activateOpenPlatformClient | aicrm-cli access open-platform-client activate | human | access | source_contract_only |
| admin.bindAccessUserWeComID | aicrm-cli access access-user-we-com-id bind | human | access | source_contract_only |
| admin.bindAccessUserWeComIDCompatibility | aicrm-cli access access-user-we-com-idcompatibility bind | human | access | source_contract_only |
| admin.checkConfigCategory | aicrm-cli config check-config-category invoke | human | config | source_contract_only |
| admin.createOpenPlatformClient | aicrm-cli access open-platform-client create | human | access | source_contract_only |
| admin.createRuntimeConfigReleaseDraft | aicrm-cli config runtime-config-release-draft create | human | config | source_contract_only |
| admin.disableAccessUserCompatibility | aicrm-cli access access-user-compatibility disable | human | access | source_contract_only |
| admin.disableOpenPlatformClient | aicrm-cli access open-platform-client disable | human | access | source_contract_only |
| admin.downloadCurrentOpenAPI | aicrm-cli config current-open-api download | human | config | source_contract_only |
| admin.enableOpenPlatformClient | aicrm-cli access open-platform-client enable | human | access | source_contract_only |
| admin.getConfigCategory | aicrm-cli config config-category get | human | config | source_contract_only |
| admin.getConfiguredAIModel | aicrm-cli config configured-aimodel get | human | config | source_contract_only |
| admin.getLegacyAppSettings | aicrm-cli config legacy-app-settings get | human | config | source_contract_only |
| admin.getOpenPlatformClient | aicrm-cli access open-platform-client get | human | access | source_contract_only |
| admin.getRuntimeConfigRelease | aicrm-cli config runtime-config-release get | human | config | source_contract_only |
| admin.getSetupWizard | aicrm-cli config setup-wizard get | human | config | source_contract_only |
| admin.listAccessEnterpriseEmployees | aicrm-cli access access-enterprise-employees list | human | access | source_contract_only |
| admin.listAdminAccessMembers | aicrm-cli access access-members list | human | access | source_contract_only |
| admin.listAdminDiagnostics | aicrm-cli config diagnostics list | human | config | source_contract_only |
| admin.listAdminUsers | aicrm-cli access users list | human | access | source_contract_only |
| admin.listConfigCategories | aicrm-cli config config-categories list | human | config | source_contract_only |
| admin.listOpenPlatformClientAudit | aicrm-cli access open-platform-client-audit list | human | access | source_contract_only |
| admin.listOpenPlatformClients | aicrm-cli access open-platform-clients list | human | access | source_contract_only |
| admin.listOpenPlatformRoutes | aicrm-cli access open-platform-routes list | human | access | source_contract_only |
| admin.listPushCapabilities | aicrm-cli config push-capabilities list | human | config | source_contract_only |
| admin.listReleaseProjections | aicrm-cli config release-projections list | human | config | source_contract_only |
| admin.listRuntimeConfigReleaseUsage | aicrm-cli config runtime-config-release-usage list | human | config | source_contract_only |
| admin.listRuntimeConfigReleases | aicrm-cli config runtime-config-releases list | human | config | source_contract_only |
| admin.patchOpenPlatformClient | aicrm-cli access open-platform-client patch | human | access | source_contract_only |
| admin.prepareLegacyRuntimeConfigRecovery | aicrm-cli config prepare-legacy-runtime-config-recovery invoke | human | config | source_contract_only |
| admin.provisionAccessEnterpriseEmployee | aicrm-cli access access-enterprise-employee provision | human | access | source_contract_only |
| admin.publishRuntimeConfigRelease | aicrm-cli config runtime-config-release publish | human | config | source_contract_only |
| admin.refreshAccessStaffNames | aicrm-cli access access-staff-names refresh | human | access | source_contract_only |
| admin.resetAccessUserPassword | aicrm-cli access access-user-password reset | human | access | source_contract_only |
| admin.resetAccessUserPasswordCompatibility | aicrm-cli access access-user-password-compatibility reset | human | access | source_contract_only |
| admin.rollbackRuntimeConfigRelease | aicrm-cli config runtime-config-release rollback | human | config | source_contract_only |
| admin.rotateOpenPlatformClient | aicrm-cli access open-platform-client rotate | human | access | source_contract_only |
| admin.saveAdminAccessMembers | aicrm-cli access access-members save | human | access | source_contract_only |
| admin.saveConfigCategorySettings | aicrm-cli config config-category-settings save | human | config | source_contract_only |
| admin.saveConfiguredAIModel | aicrm-cli config configured-aimodel save | human | config | source_contract_only |
| admin.saveLegacyAppSettings | aicrm-cli config legacy-app-settings save | human | config | source_contract_only |
| admin.saveSetupWizard | aicrm-cli config setup-wizard save | human | config | source_contract_only |
| admin.setAccessUserLoginEnabled | aicrm-cli access access-user-login-enabled set | human | access | source_contract_only |
| admin.setAccessUserRole | aicrm-cli access access-user-role set | human | access | source_contract_only |
| admin.setAccessUserRoleCompatibility | aicrm-cli access access-user-role-compatibility set | human | access | source_contract_only |
| admin.setConfigCategoryEnabled | aicrm-cli config config-category-enabled set | human | config | source_contract_only |
| admin.transferAccessSuperAdmin | aicrm-cli access transfer-access-super-admin invoke | human | access | source_contract_only |
| admin.validateRuntimeConfigRelease | aicrm-cli config runtime-config-release validate | human | config | source_contract_only |

## 系统设置 / API 文档

小板块：能力发现、Schema、版本、示例、诊断。

| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |
|---|---|---|---|---|
| platform.capabilities.list | aicrm-cli capabilities capabilities list | machine | capabilities | source_contract_only |
| 内置 | aicrm-cli schema get OPERATION_ID / schema cli / capabilities list / doctor | 本地目录或认证诊断 | clicatalog | 不授予业务权限 |
