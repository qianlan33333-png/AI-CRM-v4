# Context Center 侧栏集成候选

Owner：Context Center 既有 `daru-v3-warehouse-candidate` 运维/发布者；CRM 侧消费 Port 归 `internal/sidebar`，Composition Root 归 `cmd/aicrm`。本目录是版本化合同、可审查构建材料及合成测试，不会被 CRM 启动器自动安装或执行。

`build_candidate.py` 对55三个活动来源文件进行准确 SHA 校验，在独立目录构建 exporter、API route、refresh diagnostics 及4个新增模块；来源变化立即拒绝。准确来源和最终候选 SHA 见 `candidate-stage-receipt.json`。该收据表示隔离 staging 和合成测试，不表示服务重启/数据刷新/真实模型成功。实际启用及回滚计划见 [交接](../../../docs/operations/sidebar-ai-operations-handoff.md)。

## 两个固定合同

- GET `/api/local/v3/sidebar-ai-operations`：`crm-sidebar-ai-operations.v1`，三个可信范围字段 customer/corp/employee；读取一致的 published archive/production facts，要求 `crm-private-message-scope.v1`。
- POST `/api/local/v3/sidebar-manual-generation`：`crm-sidebar-manual-generation.v1`，冻结全部 Snapshot 和稳定 key；模型前后源核对，一次调用、排他落盘 claim，unknown 不重试，`automatic_send_allowed=false`。

两条路由保持既有顶层认证及 admin/operator 判断。不更改 agent_read/agent_operator allowlist、不复制浏览器登录/SSH凭据。旧 Agent 22个纯函数的逐项来源见 `native-migration-manifest.json`；没有迁入环境/客户语料/SQLite/CLI或批量 worker。

临时兼容层 `sidebar_native_copy.py` / `NativeEngine` Owner 为 Context Center native copy owner。替代路径为55既有 native runtime 提供等价的版本化单次手动生成入口；在冻结双向输入、provenance、质量判定、同 key 并发/重启/unknown 不重试和不可自动发送的 characterization 全部通过后，删除这层纯函数兼容代码。不以整目录复制或更换提示词冒称等价迁移。

## 配置名称（不含值）

|进程|既有/新增名称|启用影响|
|---|---|---|
|CRM|`AICRM_SIDEBAR_WAREHOUSE_ENABLED`，`AICRM_SIDEBAR_GENERATION_ENABLED`|分别默认 false；生成依赖 Reader 已启用|
|CRM|`AICRM_SIDEBAR_WAREHOUSE_ORIGIN`，`AICRM_SIDEBAR_WAREHOUSE_BASIC_USER`，`AICRM_SIDEBAR_WAREHOUSE_BASIC_PASSWORD`|受保护 HTTPS root origin，现有operator账号由人安全装配；不输出配置值|
|55|`DARU_SIDEBAR_GENERATION_ENABLED`|默认关闭手动模型调用|
|55 native|`COPY_MODEL_PROVIDER`，`ARK_API_KEY`/`KIMI_API_KEY` 与既有 endpoint/model/temperature 配置|仅支持一次 Ark/Kimi API，真实提供方未验证；不迁 CLI 授权|
|55 native|既有 `COPY_CACHE_DIR`|必须为当前服务用户拥有的私有目录；不自动修权限；保存 claim/结果而非原始请求或提示词|
|55 refresh|`DARU_CRM_READ_SSH_HOST`、`DARU_CRM_READ_SSH_KEY`、`DARU_CRM_READ_KNOWN_HOSTS`|沿现有固定 bridge；只核对名称，不读值；本任务不执行生产刷新|

已存在的 SSH 访问不等于已授权向新服务提供持久 API 访问。人通过现有安全配置入口接管秘密；新持久权限/安全设置需本次明确批准，不从聊天或工具搬运。

合成 Python 合同：`python3 -m unittest discover -s integrations/context-center/sidebar/tests -p 'test_*.py' -v`。SQL characterization 使用新建隔离 PostgreSQL 16；不运行 exporter against production。Linux 兼容收据使用真实 native v2 源码、合成资源及 stub Provider。完整真实业务验收另列。
