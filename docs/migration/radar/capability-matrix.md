# 内容雷达能力迁移矩阵

| 能力 | v2 冻结证据 | v2 状态 | v3 交付要求 | 计划 PR |
|---|---|---|---|---|
| 列表/搜索/类型/状态筛选 | `web/src/admin/sections/radar.ts` | 真实 UI，本地数据 | 真实 API、分页、指标一致 | R1-R2 |
| 新建/编辑链接 | Radar UI + CRUD | 真实本地能力 | HTTPS、版本/CAS、审计/UoW | R1-R2 |
| 图片雷达 | UI + cover media 引用 | 管理面真实；公开 viewer 不完整 | 受控 Media Port + viewer + loaded 事件 | R3 |
| PDF 雷达 | UI + attachment upload | 管理面真实；公开 viewer 不完整 | Range/MIME/viewer + opened 事件 | R3 |
| 启用/停用 | `enable`/`disable` | 真实本地能力 | 幂等、CAS、停用 410 | R1-R3 |
| 分享链接/二维码 | share projection + QR helper | 真实本地投影 | 正式 v3 域名、无身份参数 | R2 |
| 公开 `/r/{code}` | 302 + landing/redirect receipt | 链接本地闭环 | 三类型内容与 auth 分支 | R3 |
| 公共事件 | 八个 stage + digest 幂等 | 真实匿名事件 | server token、规范阶段、去重 | R3/R5 |
| 微信授权 | 无 OAuth/Provider 实现 | 缺失 | Provider read 严格 UnionID | R4 |
| OneID 归因 | `identity_attributed=false` | 缺失 | Resolve/显式 Provision；冲突失败 | R4 |
| UnionID 关联其他 ID | 无 | 缺失 | 仅由 Identity 领域处理 | R4 |
| PV | landing stats | 局部真实 | 统一时间窗口/幂等 | R5 |
| 授权用户/UV | DTO 固定 0 | 占位 | distinct verified `identity_id` | R5 |
| 真实查看次数 | DTO 固定 0 | 占位 | 类型终态事件 | R5 |
| 事件详情 | 本地 receipt/stage/time | 匿名真实 | 归因状态 + Customer 安全投影 | R5 |
| CSV | 身份列固定为空 | 占位兼容 | 安全客户投影；无原始外部 ID | R5 |
| 历史雷达定义 | v1 importer | 有迁移参考 | CLI 快照、默认 disabled/draft | R6 |
| 历史点击归因 | 旧 history | 不可证明 OneID | legacy 隔离，不推断客户 | R6 |
| 冻结 UI 接入 | PR01 全量 donor gate | 文件已冻结 | 窄 host Adapter，不改 donor | R0/R2 |
| 发布证据 | v2 本地/acceptance | 不代表 v3 上线 | 页面/API/PG16/Provider/release SHA | R7 |

## 范围外

- 自动打标签、自动发消息、自动建任务或自动推进客户阶段。
- 公众号菜单、企微写入、群发、支付或其他 External Effects。
- 身份自动合并、人工合并工作台、Customer 360。

这些能力若未来需要，必须另立 PRD/ADR，不得借 Radar 事件接口隐式加入。
