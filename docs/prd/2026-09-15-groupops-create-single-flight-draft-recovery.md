# 群运营：创建计划单飞与草稿恢复（窄 PRD）

**状态：已批准实施；实现基线：`33aff0cc549c6c2de891d09480999596848d51a4`（已审可见列表分页 B）。** 最终按队列串行合并在 B 之后；必须保留 B 的全局 write-readback 锁、#289 详情保存/回读保护和 提交 `93fdfb92` 的详情读取/CAS 行为。本 PR 不改它们。

## 业务判断与根因

管理员在群运营列表填写名称、类型并从既有负责人选择器选人后，连续点击“保存计划”可以并发进入 `createPlan()`：它没有创建中的锁，只在 `await` 后才离开；失败路径又重新 `renderList()`，而 `renderCreatePanel()` 把名称和类型写回默认值。源码见 `web/v3/groupOpsStandard.js:490-517`、`:795-805`。这会丢掉操作者已填草稿，也可能向同一业务意图发送两个创建请求；这是源码级风险，不是线上请求数复现。

现有 V3 Host 先 `POST /plans`，再按已创建计划的 revision `PUT /plans/{id}` 写计划类型与负责人；后一步是一个本地原子计划更新，却不与创建处于同一事务，见 `web/v3/groupOpsHostAdapter.ts:351-373` 和 `internal/groupops/app/service.go:129-151`。Host 对每次非 GET 都覆盖为新 `Idempotency-Key`（`:149-158`），所以现状的失败后人工重试无法复用创建收据。服务端的 `plan_create` 已在同一 Unit of Work 内保存计划、审计事件和完成收据；相同 actor/key/payload 可返回已完成快照，键同而载荷不同则冲突，见 `internal/groupops/app/service.go:112-126,442-483`。HTTP 创建端点只接受 `name`，保持该合同不变（`internal/groupops/http/handler.go:510-523`）。

## 范围、边界与方案

只修改 V3-owned `web/v3/groupOpsStandard.js`、`web/v3/groupOpsHostAdapter.ts`、`scripts/groupops-host-adapter-e2e.mjs` 和本 PRD；复用当前群运营卡片、按钮、`role=alert` 提示和冻结的 operation-member picker，不改 donor、不新增页面、API、后端表、队列或 Provider 调用。负责人继续是现有本地 staff ID 合同；不涉及 OneID 或外部身份解析。该能力进行既有 GroupOps 本地持久化及其收据/审计写入，不新增持久任务、Provider 读取或 Provider 写入。

1. 打开创建面板时保留既有默认值；可编辑阶段在任一列表加载或分页重绘前捕获名称、类型和负责人选择，避免 loading 壳覆盖尚未提交或被明确拒绝后重新编辑的草稿。点击保存前再把这些值和本次创建的不可变 payload 快照到 `createDraft`。待确认/未知阶段不得重新捕获，缺负责人时零 POST，以持久、可访问的字段提示保留全部草稿。
2. 引入仅覆盖创建流程的 `createInFlight` 与待确认状态。首次点击到结果确定前，保存、负责人选择、输入、取消和“新建”均呈现现有 disabled 状态；第二次点击不发请求。取消或新建不得静默抹掉 pending/unknown 状态。明确创建被拒后才允许修正草稿，开始一项新意图。
3. 创建恢复使用**私有** Host 选项，不扩大其他 `nativeRequest` 的语义。该选项仅接受符合既有 HTTP 长度/字符要求的键，规范化后才透传且绝不写日志或页面。一次意图保留两枚键：创建 POST 键与配置 PUT 键，并在内存冻结首次请求时的 CSRF 会话标记。Host 只对创建恢复尊重调用方给出的键，其他写仍生成原有键；每次恢复 POST、PUT 之前及 POST 回包转入 PUT 之前都重新核对该标记。键和会话标记都不进入 HTTP JSON body、不写日志或页面。
4. 明确拒绝（客户端验证、401、403、409）和网络、5xx、空/错 ID/非法 revision 的成功回包必须分开。明确创建被拒：解除锁、保留草稿、允许编辑并以新键开始新意图；401/403、会话标记缺失或变化：停止写、清楚提示重新核对已有计划，绝不跨登录身份自动重放，也不从员工目录猜测当前身份。未知 POST：冻结原快照，显式“重新确认创建”只能重放原 POST 键和同一 `name`，不读新状态、不自动重试、不允许编辑后沿用旧键。已知创建后，不论配置 PUT 是拒绝还是未知，都保存创建 ID 与原 `expected_revision/type/owner`；只可显式重放原 PUT，绝不再 POST，也不能回到“新建”。
5. 创建回包必须是同意图的 revision `1`、`draft`、原名称；配置回包必须是同 ID、原 revision 加一、`draft`、原名称、类型与负责人。只有该完整合同成立才导航到详情页；错 ID、错名称/状态、跳号或非法 revision 都不报成功。创建和配置是两个既有收据事务，不能描述为原子创建。同页恢复以首次 CSRF 会话标记未变为前提；页面离开后的跨会话恢复、收据查询 API、业务层自动重试均不在本 PR 内；未确认状态不得表述为成功或失败。

`#289`（提交 `c898eda23a030b3d29f1e3219d62b688583735b8`）是仓内参考：它为已有详情保存使用独立 in-flight 锁、草稿快照、真实读回失败提示和显式重新读取，而不把失败伪装成成功。这里仅将该可靠性交互收敛到创建流程，不能改变详情保存、CAS 或列表分页语义。

## 验收与证据

扩展 `scripts/groupops-host-adapter-e2e.mjs` 的既有 JSDOM 真实 Host + 冻结 picker 接缝（现有详情共挂载在 `:150-187`，列表 Host journey 在 `:432-455`），不用手造替代保存器。全部 fixture 使用合成计划/员工 ID。

- 双击：恰好一个 POST；忙碌/禁用可见。缺负责人零写且保留草稿。
- 使用合成总数 51 的两页列表，验证可编辑创建面板在翻页重绘后仍保留名称、类型和负责人；明确拒绝后修改名称再翻页也保留修改值。该 DOM fixture 只验证前端状态，不等同真实数据库记录。
- 明确拒绝分别覆盖验证、401、403、409；网络/5xx/空回包覆盖未知路径。草稿、单个 `role=alert`、锁与是否可新建须符合上述状态机；冻结期间负责人选择器既 disabled 又在事件处理器中拒绝打开。
- 首 POST 已接受而响应丢失：显式重试携同一 POST 键和同载荷，回放同一计划，不创建第二条；CSRF 会话标记变化后零重放。
- POST 已确认、PUT 已接受而响应丢失：不再 POST；显式重试携同一 PUT 键、计划 ID、revision、类型、负责人。错 ID、错名称/状态、跳号或非法 revision 不导航或报成功；只有完整成功链实际触发一次详情导航后才结束 fixture。
- POST 接受后至 PUT 前 CSRF 标记变化：保留已知计划 ID、零 PUT、停止恢复且提示重新核对；跨会话身份仍无自动恢复保证。
- 保持现有负责人只以 local staff ID 写入的 Host 合同与既有计划详情保存、归档、B 列表 write-readback 行为。定向 DOM 用例及 canonical consumer 链是源码验证；它们不等同真实 PostgreSQL 收据、部署或浏览器业务回读。

不在本 PR 内：创建后的自动启用、跨页/跨会话收据恢复、任何 OneID 映射、外部群/Provider 写入，以及创建 API 或数据库事务模型重构。
