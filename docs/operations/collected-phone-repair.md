# 已采集手机号入库补录

适用商品下单冻结联系人与问卷 mobile 类型的冻结答案。用户的采集资料与之后支付/退款、商品/问卷状态无关。手机号绑定到原记录已确认的客户，声明手机号不作为 Provider 验证，不按手机号合并客户。来源原值、Identity 加密 Phone Vault、来源收据共同保留事实。

## 候选范围

统一 Customer 采集 Port 在下单/答卷的既有 PostgreSQL UoW 内调用 OneID 并更新目录。商品只要保存联系方式即绑定，支持 mobile 与 shipping_address。问卷每个已填写的 mobile 题都使用相同入口。Provider 不在资料补录中执行。历史补录复用原生来源键 `order-phone:<order_id>`、`survey-phone:<submission_id>:<answer_id>`；重复执行保留原绑定或冲突结果。

可执行文件 `bin/migrate-collected-phones` 由现有 release builder 构建并随应用包交付。b8 首次修复无 schema 迁移；当前 canonical 相关修复新增 `0220_identity_phone_canonical_recovery.sql` 的 Identity 恢复审计表，不修改原收据。普通应用安装不运行此补录命令，不自动启动定时器。生产迁移和数据补录由唯一路由指定的发布工作台按既有授权与备份规则执行，单独保存结果，不绕过失败队首。

## 查看与执行

保持运行目录与 EnvironmentFile 指向准确已安装候选。以下命令仅在工作台核对包、环境、来源范围与授权后执行；参数与输出不含明文手机号或密钥。

```sh
sudo systemd-run --wait --pipe --collect \
  --property=EnvironmentFile=/etc/aicrm/aicrm.env \
  --property=WorkingDirectory=/opt/aicrm/current \
  /opt/aicrm/current/bin/migrate-collected-phones --mode=inspect --source=all
```

`inspect` 使用真正的只读 UoW，清点源记录并通过 Owner 读取原收据与合并审计安全分类，不尝试绑定、不以回滚写入模拟只读。输出每个来源的 candidates、last_completed_id 与分类计数；该数是采集记录数量，不能称为待绑定用户数。

对每个来源绑定 inspect 的最高 ID，分别 apply：

```sh
# 将 ORDER_WATERMARK / ANSWER_WATERMARK 替换为本次 inspect 的 last_completed_id。
sudo systemd-run --wait --pipe --collect \
  --property=EnvironmentFile=/etc/aicrm/aicrm.env \
  --property=WorkingDirectory=/opt/aicrm/current \
  /opt/aicrm/current/bin/migrate-collected-phones \
  --mode=apply --source=order --through-id=ORDER_WATERMARK

sudo systemd-run --wait --pipe --collect \
  --property=EnvironmentFile=/etc/aicrm/aicrm.env \
  --property=WorkingDirectory=/opt/aicrm/current \
  /opt/aicrm/current/bin/migrate-collected-phones \
  --mode=apply --source=survey --through-id=ANSWER_WATERMARK
```

每条采集记录一个事务；错误时报告 failed_source_id 和 last_completed_id 后停止。用同一 source / through-id / 原来源键重新执行，或用 `--after-id=last_completed_id` 续跑，不能换手机号归属或制造新幂等键。密文损坏或数据库错误不得跳过冒称完成。

结果分开统计 attached、already_linked、conflict、invalid 及 replayed 原结果。历史补录仅填补空的目录主手机号，不覆盖现有资料。未知客户归属的历史问卷保持原值与未归属状态，不创建客户。冲突仍留在来源和收据中，不覆盖其他客户号码；重复冲突不得更新目录。

## 独立读回

- 原订单/答卷加密内容、订单状态和问卷状态不变。
- 成功补录有 OneID 声明手机号、Phone Vault 和源收据；用户档案现有手机号接口/目录可读，不将 declared 冒称 verified。
- 同范围重复 apply 不新增身份/收据，不改变目录新版资料，不新增支付、退款、问卷提交、Outbox 营销效果。
- 订单 1206 / 用户编号 1023763 是本次实例验收对象，需在实际补录后从认证后台读取档案和订单；本地合成测试通过不等于该用户已在生产修好。

2026-10-02 本次只读盘点：订单联系人 247 条（paid 203、pending_payment 33、refunded 11），最高 order_id=1208；问卷 mobile 加密答案 1539 条（resolved 1012、unresolved 504、anonymous 23），最高 answer_id=6778。此为盘点时快照，执行前重新 inspect；归属未知的 527 条原始采集数据已在 Survey 保存，不能按手机号猜测归属。此时用户 1023763 档案与 OneID 手机号仍为空，未执行生产写入。

## canonical 相关修复的固定范围与执行

上述末段是首次只读快照。b8 已上线并完成原补录，nova 已由工作台认证档案和订单读回验收。当前相关修复仅覆盖此前实际补录水位 **订单 1210、问卷答案 6914**；不重新扩大到最新 inspect 的最高 ID。已知 order invalid 105 是 merged 源客户，不是非法手机号；survey unknown 527 保持无归属，invalid 530 不能全部称未知。源状态与回放结果是不同维度，应分别统计。

父需求要求已审核别名可读到当前客户档案。恢复入口由源 Owner 在内存解密并提供原客户/键/事件，Identity 核对与旧 invalid 收据的精确 payload，再复用 canonical Port、有序根写锁与重读，对每一段核验强证据 confirmed merge 审计、未撤销及当前 lineage version。无原收据、未知客户、未审核链、环/断链/超过既有 Owner 写路径深度、非 active 终点均不恢复，不复活客户、不改源 ID/密文/财务、不按共享号码合并。

新增表永久关联原键/原 payload/原客户、绑定根/lineage version、链摘要/merge IDs、确定性恢复键。原 invalid 收据和 Survey 旧绑定收据保留；原 `apply` 继续回放旧结果。`recover-canonical` 是独立的、稳定的审计恢复语义，不是替换原 payload 或随机换键。恢复成功/冲突各只能接受一次；后来根变化只核验已经存在的身份随已确认合并移动，撤销或归属漂移保持例外，不再建第二身份。

准确候选迁移安装及备份已核对后，由工作台逐来源执行以下命令；使用和上节相同的 EnvironmentFile/WorkingDirectory 包装：

```sh
/opt/aicrm/current/bin/migrate-collected-phones --mode=inspect --source=order --through-id=1210
/opt/aicrm/current/bin/migrate-collected-phones --mode=inspect --source=survey --through-id=6914
/opt/aicrm/current/bin/migrate-collected-phones --mode=recover-canonical --source=order --through-id=1210
/opt/aicrm/current/bin/migrate-collected-phones --mode=recover-canonical --source=survey --through-id=6914
```

默认仍 inspect；恢复拒绝 `source=all` 或未指定正的 through-id。每源项事务中同时提交身份/Phone Vault、恢复审计与历史填空目录；不更新原 Survey 收据。失败停止后按原 source/through-id/after-id 续跑，不跳过错误。重跑相同固定水位核对所有身份/收据/审计/目录摘要不增，源与财务摘要不变；未恢复例外明确列数，不冒称全部成功。

只读分类：`original:attached|already_linked|conflict` 保留原结果（成功读取实际当前身份 Owner）；`recoverable` 是已审查链候选，`recoverable_conflict` 表示已有其他 Owner 占用该号码；`recovered` 成功恢复，`recovery_conflict` 保留冲突；`replayed:recovery_*` 是一次恢复的重放。`unknown_source`、`no_original_receipt`、`invalid_without_active_alias`、`unaudited_alias`、`unsafe_lineage`、`canonical_changed`、`recovery_lineage_changed`、`recovery_identity_drift`、`original_identity_unavailable|drift` 是需保留的例外。数据库/密文/原 payload 不符是错误停止，不伪装为可跳过分类。

## 安装字节合成验收入口

使用 `scripts/run-installed-phone-canonical-recovery.sh --binary /opt/aicrm/current/bin/migrate-collected-phones`，工作目录为最终准确候选源码，设置 `AICRM_DATABASE_URL` 为独立本机 PG16、数据库名以 `_acceptance_test` 结尾。脚本拒绝远程/业务库，沿已有 Composition fixture/迁移入口创建临时独立测试库并清理，不写共享预发业务库。它编译的 Go 测试仅作为合成数据和断言驱动；所有 apply/inspect/recover/失败重试均执行传入的**已安装维护二进制**，记录实际文件 sha256。工作台另行绑定最终 SHA/tree/manifest 与该执行收据，不能把本机自行 build 分支的默认测试当安装证明。

具名旅程 `TestPostgreSQLPhoneCanonicalRecoveryMaintenanceJourney` 覆盖原 invalid/问卷收据不变、合法多级 canonical 恢复与目录、未知/未审核/冲突 fail closed、相同源重放身份/恢复审计/目录不增、非空支付事实及源密文摘要不变、无 Provider 效果、数据库投影故障时完整回滚并原源重试。合成强证据只由 Owner 现有合并测试合同构造，位于独立数据库，不能在共享预发/生产仿造。真实客户生产恢复与认证读回是另一项验收。
