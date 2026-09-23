# 旧资金回调与外部监控：只读核查

核查日期：2026-09-18。范围是旧机残留资金回调及现有腾讯云监控证据，不修改旧业务、不部署端点、不发送支付/退款请求、不安装或购买监控。SSH 运维身份可读取不等于新 CRM 运行身份已获得可持续读取权限。

## 结论

`legacy.payment_callbacks` 尚缺可信的只读资金回调聚合入口；`runtime.external_monitor` 尚缺外部拨测任务、告警绑定及实际送达证据。两项不能改成 covered。已安装云 Agent、HTTP 405 或旧域名健康页均不足以填补它们。

## 旧机实际入口与现有权限

旧运行目录 `/home/ubuntu/极简 crm` 指向 `releases/aicrm-laohuang-20260426144637`，`openclaw-wecom-postgres.service` 为 active/running；应用端口只监听 `127.0.0.1:5001`。

`/etc/nginx/sites-enabled/youcangogogo.conf` 的两个精确 location 把 `/api/h5/wechat-pay/notify` 和 `/api/h5/wechat-pay/refund/notify` 送到旧应用。其 `location /` 转发到新机。因此旧域名普通健康/管理路径已不代表旧进程。

| 只读探测 | 结果 | 能说明什么 |
|---|---|---|
| 旧机本地 GET `/health` | 200，通用 health 字段 | 进程及通用数据库配置状态；没有支付回调积压指标 |
| 旧机本地 GET `/api/system/health` | 4 秒超时 | 未取得 readiness 证据，不能视为健康或零积压 |
| 旧机本地 GET `/api/admin/webhook-inbox/metrics?provider=wechat_pay` | 401 | 受旧管理员会话保护 |
| 旧机本地 GET `/api/admin/jobs/callbacks` | 401 | 受旧管理员会话保护；返回合同包含明细 items |
| 新机以 `aicrm` 用户、固定旧 IP、验证原域名 TLS，GET 两个资金通知路径 | 两者 405 | 公网 TLS/精确路由存在；未执行资金回调，不能证明处理成功 |
| 同样请求 `/health`、上述 admin metrics | 两者 404 | 请求走了新系统，不能拿来监控旧进程 |
| 新机直连旧 `:5001/health` | 连接超时 | 现有网络不能直接访问旧 loopback 服务 |

新机运行用户没有 `.ssh` 目录；应用环境变量**键名**中未发现 legacy/monitor/qcloud/tencent/old-host/API 监控配置。未读取或输出凭据值。未登录旧管理会话、复用 Cookie、传入回调正文、发起退款或关闭鉴权。

## 原巡查合同与支付真实事实源

旧 `aicrm_next/platform/admin_jobs/operational_inspection.py:30` 的 `collect_operational_inspection` 是 Python 内部函数，非独立只读 HTTP 接口。它读取 systemd 调度、`external_effect_job`、`data_health_snapshot`、`internal_event_outbox`、`internal_event_consumer_run`，按窗口汇总 overdue/stalled/unknown/terminal；并非资金回调专用探针。小时报告由 `notification_settings.py` 组合这些结果。

三个原 timer 已确认 disabled/inactive：`openclaw-broadcast-hourly-feishu-report.timer`、`aicrm-data-health-snapshot.timer`、`aicrm-queue-invariant-check.timer`。不能依赖旧小时报或旧 snapshot 继续新鲜更新。

资金接收链明确不同于通用 webhook inbox：

- `public_product/api.py:204` → `h5_wechat_pay.py:1446`：POST 通知先验签/解密，再 `_apply_transaction`；事务内写 `wechat_pay_orders` 并确保支付成功 `internal_event_outbox`，commit 后才返回成功。
- `commerce/api.py:391` → `admin_transactions.py:828/697`：退款通知验签/解密后锁定并更新 `wechat_pay_refunds`，更新订单退款状态、写 `wechat_pay_order_events`；符合条件时在同一事务写退款事件 Outbox。
- `webhook_inbox/repository.py:662` 的 metrics 虽支持 provider/route/time 过滤，但这些资金处理函数没有写入 `webhook_inbox`。不能把 inbox 为零推断成资金回调正常。
- 通用 metrics 还包含 `recent_errors.error_message`，不是可以原样进入新巡查/飞书的聚合白名单；jobs callbacks 则包含 callback_logs 明细。

已用只读 PostgreSQL 会话核对真实表/字段存在，没有读取订单标识、付款人或回调正文：

| 事实源 | 可用聚合 | 限制 |
|---|---|---|
| `wechat_pay_orders` | status/trade_state 分类、provider_unknown_at、reconciliation_last_checked_at、状态更新时间 | 业务事实；只有旧业务 Owner 可决定遗留待支付是否仍需处理，不能按总量全部报警 |
| `wechat_pay_refunds` | status 分类、更新时间、处理中最老年龄 | 保留原始资金事实，不自动重试退款 |
| `wechat_pay_order_events` | event_type/trade_state 时间窗计数及最近事件时间 | 无事件不等于 Provider 不可达；不能导出 payload/headers/交易号 |
| `internal_event_outbox` | 严格限定两个资金 `source_route` 的状态、到期/超时/terminal/held 分类 | 应联查内部消费者聚合；不得混入已退役的旧业务任务 |
| systemd/loopback/nginx 固定路径 | 服务 active、HTTP 只读可达、仅两条 POST 回调状态聚合 | GET 405 必须单列“路由存在”，不能标“资金链验收通过” |

本次两次有界聚合查询分别受 3 秒和 2 秒 `statement_timeout` 终止，未取得计数。结果为 **unknown/query_timeout**，不是 0。接入前需检查对应过滤索引/执行计划与资源压力；不能把全表扫描复制为每小时巡查。

## 最小后续接入边界

现有路由不能直接完成覆盖。可接入的事实已列出，但需要运维侧的窄只读传输能力：固定受限命令/固定聚合接口、固定 SQL 白名单、短只读事务超时、只允许新巡查身份、固定主机校验，输出带 observed_at 和稳定 code 的计数。旧数据库连接与管理员凭据留在旧机。只读采集是旧能力的运维适配，不恢复旧业务主线或旧调度。

新 CRM 通过稳定只读 Port 消费该聚合结果，现有 River 小时调度负责执行；不在旧机新增队列、业务 Worker、自动重试资金动作或第二套小时报告。当前不存在此受限权限/端点的证据，因此这一步仅提出合同，没有实施。

## 腾讯云现有证据

新旧两机均存在 `/usr/local/qcloud/monitor`、`stargate`、`tat_agent`、`YunJing` 目录，并观察到 `barad_agent`、`tat_agent` 进程；新机 `tat_agent.service` active。这证明宿主 Agent 存在，不证明公网独立探测或告警送达。

已搜索旧发布包 `docs/`、`deploy/` 及新仓 `docs/`、`deploy/`、`.github/`：未找到已配置的拨测任务 ID、目标、告警策略、通知绑定或测试送达回执。新 PRD 中“复用已有免费腾讯云监控”是方案意图，不是配置证据。没有访问腾讯云控制台/API 的当前账号配置，也没有核验免费额度或任何费用状态，不能声称账号中没有监控或现有监控免费。

收口 `runtime.external_monitor` 需要已有控制台/API 的只读配置证据：探测任务 enabled、外部节点/协议/目标、失败阈值、告警策略与接收渠道绑定、近期告警或受控测试的送达结果；并确认现有套餐/额度无需新增付费。直到这些证据具备，保持 uncovered，Agent 存在可作为单独宿主指标展示。

## 当日晚间控制台补充核验

随后通过已有浏览器登录态只读进入当前腾讯云账户，更新了此前“没有控制台访问证据”的边界：云产品监控策略列表仅有一条启用的轻量服务器流量包余量预设告警，关联 8 个实例；这不是服务可用性或 CRM 外部 HTTP 拨测证明。云拨测告警列表为 0，拨测任务列表也为 0，账户显示试用版、剩余 15 天、最多 5 个任务。

[官方计费说明](https://cloud.tencent.com/document/product/280/70804)说明试用从创建首个任务开始，15 天后任务自动暂停，继续需专家版按量计费。因此现有试用不能作为长期免费外部监控交付；本次未创建试用任务、升级套餐、购买资源、修改告警或传入飞书凭据。`runtime.external_monitor` 继续保持未覆盖。此结论仅涉及当前账户这些已完成加载的列表，不推断其他账号或未查询产品。
