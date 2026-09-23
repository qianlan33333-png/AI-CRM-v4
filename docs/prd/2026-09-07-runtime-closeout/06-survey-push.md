# 问卷外推生产接通
沿已有PRD02/SurveyCompletionProvider及旧问卷外推行为。OneID由现有身份Port读取受信外部标识；Survey完成收据/任务同事务，外推归outbound/External Effects。定位旧生产受保护的目标、签名和字段契约，映射配置引用，不能把商品外推密钥/载荷想当然共用。未配置目标时不得把接收成功等同已投递。确认真实接收方契约后启用已开发Provider；不重发历史问卷、不制造用户未批准的真实业务推送。必要协议适配由执行任务独立PR，本文补测试/目标引用/生效证明，不含实际秘密或PII。


生产目标配置准备（2026-09-07）：旧启用问卷共 7 份配置，对应 2 个去重 HTTPS 目标，鉴权只在 V3 `/etc/aicrm/integrations/survey-completion-targets-prepared-20260907.json`（root 0600）。`hxc-questionnaire-production-v1-1` 对应旧配置 ID 20/21/29/37/52/54；`hxc-questionnaire-production-v1-2` 对应旧配置 ID 19。该映射只供新问卷选择同一目标时参考，不表示已导入旧问卷或旧提交。目标已包含实际旧运行 HMAC signer 和 V3 作用域内 `unionid` 身份合同，未复制旧问卷的 day/frequency/expiry 业务配置；这些仍由新问卷管理员明确设置。当前仅准备，未应用/未实际推送。


## 2026-09-08 已部署与启用

PR #187 已合并并实际安装为 `7085192b0f6510ba71da119ffa2989849066e1cd`。启用前核查 completion snapshots=0、test snapshots=0，未补发历史任务。现有两个目标配置已安全应用，API 与 effects-worker 实际进程逐值回读一致，ready 正常。正常管理员登录后读取现有问卷 operations：HTTP 200、provider_enabled=true、local_only=false、target_catalog_available=true、target_count=2，目录只返回不透明引用。未修改现有问卷的目标绑定、业务参数或历史提交；生产业务外推测试 0，接收方真实业务验收仍由人工进行。

完整主线测试通过，但该次 CI 的部署后 HXC 核对步骤失败，不能称整条发布流水线全绿；该失败不影响已验证的问卷程序安装及上述配置应用。具体发布缺陷单独收口。


### 2026-09-08 生产问卷级绑定恢复

全局启用后的第二次核对发现，7 个旧版已启用的现有问卷尚未设置 V3 operation configuration。按已有 migration source_map 和真实 endpoint 摘要双重核对：source 19→V3 3→受控目标 `hxc-questionnaire-production-v1-2`；source 54→10、21→5、37→7、29→6、20→4、52→8→受控目标 `hxc-questionnaire-production-v1-1`。旧版 day / expires_at_ts / type / remark 参数逐问卷保留，未凭空添加 legacy questionnaire_id，未复制密钥到 metadata。

已通过正常管理员 Session、CSRF、稳定 Idempotency-Key 和 configuration_version=0 CAS，逐条 PUT `/api/admin/questionnaires/{id}/operations/external-push`，逐条 GET 回读 version=1、enabled=true、目标和 metadata 与来源一致。7 个全部完成；其余旧版未启用问卷保持原状态。已在生产受保护备份位置保存操作前配置。前后 completion snapshots / test snapshots / 非历史 receipts 均为 0；未调用 test 接口、未扫描或补推历史。配置保存仅同事务写配置、审计与配置事件，新提交才触发 completion 接纳。外部 Provider 实际业务收件仍待人工正常新提交验收。
