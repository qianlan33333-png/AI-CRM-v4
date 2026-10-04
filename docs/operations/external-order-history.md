# 外部历史订单迁移（仅本批获批范围）

唯一发布工作台执行生产发布和数据迁移。原 CSV 不进 Git；传输目录 0700、文件 0600，不打印行值、手机号或 UnionID。当前已核验三文件摘要见父 PRD/本聊天，联合清单摘要为 `ec362a1ee0c6b1e9ce104f586845d227bec0ef4b9ac0f720ddb7faf010e7e12e`，真实 scope `wechat-open-platform:wx0ca836834b18e989`。

构件包含 `release/bin/migrate-external-orders`；由正常发布脚本构建。使用当前应用的 `AICRM_DATABASE_URL` 和 `AICRM_IDENTITY_PHONE_DATA_KEY`，通过受保护进程环境注入，不回显密钥。迁移 0221 必须先在预发验证，生产迁移前备份并验证备份成功，然后同包生产。

依次调用：

```sh
migrate-external-orders --mode inspect --union-scope "$SCOPE" --csv "$CSV1" --csv "$CSV2" --csv "$CSV3"
migrate-external-orders --mode apply --union-scope "$SCOPE" --csv "$CSV1" --csv "$CSV2" --csv "$CSV3" --manifest-sha256 "$MANIFEST" --plan-sha256 "$PLAN"
migrate-external-orders --mode reconcile --union-scope "$SCOPE" --csv "$CSV1" --csv "$CSV2" --csv "$CSV3" --manifest-sha256 "$MANIFEST"
```

`inspect` 和 `reconcile` 是只读、可重复读事务；`apply` 首先重新 inspect，计划不同先停，随后每笔客户锁、匹配复查、手机号补录、展示、历史订单、收据、审计共享事务。apply 返回的成功数只计入已提交事务。源订单内容变化不覆盖。无需更换幂等键重试；中断后保留原收据，重新 inspect，用新计划摘要继续原 run key，已提交行回放。

最新获批范围：只有支付时间非空、金额大于零、无退款的订单可以写入。全额/部分退款来源或成功退款金额大于零，仅参与手机号补录。其它零元/无时间行完全跳过。禁止恢复旧的 8441 笔范围。

新核查基线：10442 行、10417 唯一、25 重复；Union 4342、Phone 40、Skipped 1449，共 4382 笔实单；`Amount=489366640` 分、`Refund=0`。剔除无时间 835、非退款零元 2516；退款来源 1235 行，匹配 1152 行，只补手机号。所有选中来源合计待补 2087 位客户（同一客户去重），选中跨客户冲突 17，属于原已确认 26 笔；不补不移冲突号。执行前重读，任何新归属或歧义先联系原用户。`PhoneOnlyFilled` 见 JSON `refund_phone_only_filled`，是实际由退款来源首次补入的数量，包含其可能也有正常订单的客户。

订单与侧边栏完整资源合同 `web/v3/orderAdapter.test.mjs`、`web/v3/sidebarOneIDPresentation.test.mjs` 在最终安装包验收执行，检查名分别为 `order-presentation`、`sidebar-oneid-presentation`。准确私有验收 checkout 的 `web/dist` 使用该安装包的真实页面和资源，复用原 npm 依赖；源码测试 bundle 与安装资源证据分别记录。真实 Chromium 合同在本机准确源码已通过；正式 Linux 同一旅程使用准确安装 API/工具执行，不在源码阶段重复构建整套页面。

本地真实认证浏览器旅程已通过。安装包用 `scripts/run-installed-external-order-history.sh --binary <package>/bin/aicrm --root <package> --sha <sha> --binary-sha256 <digest> --tool <package>/bin/migrate-external-orders` 在隔离验收库复验；沿用当前工作台原 stage-evidence 的 external-order-history 检查名，不新建发布通道。

执行后保留 stdout 汇总与 exit code，在订单 Owner 的 `order_import_runs`/`order_import_receipts` 独立核对 run key、source_digest、imported/replayed、订单金额/退款总和、原时间未知数、external_history/no-effects；对照 Identity 的 declared/order 来源收据与加密号码记录、Customer 手机号展示核对实际补录数。逐笔 `reconcile` 核查客户归属、商品快照、金额、状态、原支付时间和来源摘要。

安装包旅程：用合成客户在隔离 PostgreSQL 中运行真实迁移工具，随后以后台认证会话查看列表、详情、客户关联入口、全部订单 CSV。本批原时间按北京时间，零元/无时间/退款不入订单；兼容未知历史记录显示“原时间未知”；详情分别标注导入时间和历史退款金额；无退款/重发执行控件。原生微信和支付宝行为继续执行相关回归。生产独立认证读回至少一笔已知时间、一笔组合商品，以及确认零元/无时间/退款来源没有导入订单以及一个已补手机号客户；工具 reconcile 不替代认证 UI 读回。

本批不生成 Payment/Refund 流水、开通权益、增加自动化资格或 Provider 调用。对外只复用现有 read API；`external_history` 不能走原生退款或重发。不要删除已导入历史记录作回滚。
