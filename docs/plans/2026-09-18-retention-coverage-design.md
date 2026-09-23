# 生命周期覆盖缺口可见

业务判断：已启用的九项清理策略近期成功，只能证明这个白名单的执行健康；不能证明全系统过程数据已完成30天清理。混合业务载荷、身份授权状态、协调水位和未证实来源继续禁止通用删除，但必须让治理页面读到负责领域、不删原因、已有入口和接入缺口。

OneID：不涉及，不查询客户或外部身份。Persistence：无状态的注册表元数据读取；不新增表、任务、Provider调用或删除入口。安全TTL的原始较短授权期限不变，物理清理未接入不代表过期授权仍可用。复用现有 AdminOps 超管鉴权与HTTP Handler；不创建第二个配置或清理执行器。

GitHub参考：[Prometheus target discovery API](https://github.com/prometheus/prometheus/blob/main/docs/querying/api.md#targets) 分别呈现发现目录与活动目标健康。本实现借鉴这种区分，仅复用现有治理链路，无新监控依赖。

## 来源与状态契约

唯一来源是 `docs/governance/retention-registry.json`。覆盖元数据的 `coverage_bindings` 只绑定精确资源身份和代码入口，不参与清理策略执行或扩大白名单。生成内容编译进Go程序，不依赖生产机器上的文档文件，也不读取任何业务表或宿主文件。运行态只补充当前启用开关及Owner/宿主读取Port绑定状态，不调用这些Port。

生成命令：`python3 scripts/check-retention-registry.py --write-runtime-snapshot`。先完成既有表/路径/策略校验，再生成 `internal/adminops/retention_resources.generated.json`。默认checker与fast要求生成内容和注册表精确一致，包括原始文件SHA256。Go测试另核对所有资源的Owner、规则、理由及摘要。新增表/目录、失效入口或未重生目录均失败；生成不触碰供体。

固定只读 `GET /api/admin/ops-retention/resources`，仅superadmin，无参数、无写接口，`Cache-Control: private, no-store`。DTO带 `inventory_scope=committed_registry`，明确这是已提交的代码清单，不是现场数据库/文件系统盘点。表、逻辑资源、目录前缀分别计数；逻辑资源及目录可能覆盖相同实体，禁止把总条数当作唯一物理资源或可清字节。

每项含 `kind/name/owner/policy/reason/source/cleanup_entrypoint/policy_id/coverage_status/gap_code/authorization_expiry`。状态：

- `protected`：已审定永久或禁止通用删除的资源；无执行入口。
- `gap`：混合载荷待Owner分离、来源未证实、安全TTL物理清理未接入，或已明确待Owner清理合同；给固定缺口码。
- `owner_managed`：Owner更新投影或管理协调状态，不把当前水位、游标、锁误称缓存或清理成功。
- `scheduled` / `disabled` / `owner_not_bound`：已实现精确策略的绑定/启用状态；scheduled不等于实际清理成功。
- `native_unobserved` / `host_unobserved`：原生River或宿主清理需独立观测，不能由九项数据库策略成功代证。

当前449表中19 mixed、1 unknown、11 security TTL仍明确为gap，另有2协调和2投影Owner合同缺口，共35项静态缺口。已绑定的完整服务有9项可调度表策略；OFF仍显示9 disabled。两个额外Owner未绑定、宿主读取Port缺失会追加运行时缺口。13项协调、11项投影全量保留各自理由，不能将剩余Owner管理状态算作“已30天清理”。CPU采样最小收据永久，采样文件独立归 `process-diagnostics` 的宿主720小时目录，二者不能混算。

现有RetentionHealth继续只判断执行白名单，成功码明确为 `allowlist_policies_fresh`。`coverage_*` 聚合指标始终保留，包括OFF时；全九项成功不会使覆盖缺口归零。页面应把“白名单执行健康”与“资源覆盖缺口”分栏；宿主/River观测仍独立，不提供误导性的全系统健康布尔值。

## 验证与集成范围

Python校验器覆盖摘要漂移、新资源、缺入口、受保护资源无法绑定执行策略。Go专项覆盖精确九项映射、CPU收据/产物分离、Owner缺失、OFF、鉴权、只读无参数，以及真实PostgreSQL九项成功时35项缺口仍可见、历史记录不变。保持<32项脱敏巡查指标。

不修改Composition、OpenAPI、source index/lock或前端；根集成统一补API规范、生成源绑定和页面。现有Host已将 `/api/admin/ops-retention/` 子树绑定到同Handler。没有新增删除、TTL延长、外部写入或生产变更；本地fast/compile/专项与完整Linux CI、部署及真实页面验收分开汇报。
