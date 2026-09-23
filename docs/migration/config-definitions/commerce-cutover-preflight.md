# 当前商品与券定义的切流预检

分类：不涉及 OneID（定义和发行汇总，不匹配客户、不写领券行）；源与预检为
repeatable-read/read-only，apply经Owner Port在单一PostgreSQL UoW内写入。
不涉及Provider、任务或外部效果。

使用显式 `--commerce-only`，支持 extract、inspect、dry-run、apply。
它不会抽取群计划或 Agent，数量来自本次 manifest，不要求旧 31/15 基线。
旧命令默认行为、旧加密文件及旧行摘要保持不变。

```sh
# DSN 只经受保护环境提供，禁止回显。源读 AICRM_SOURCE_DATABASE_URL。
bin/migrate-v2-config-definitions --commerce-only --mode=extract \
  --source-revision=<source-git-sha> --snapshot=/secure/commerce.enc \
  --snapshot-key-file=/secure/snapshot.key
bin/migrate-v2-config-definitions --commerce-only --mode=inspect \
  --snapshot=/secure/commerce.enc --snapshot-key-file=/secure/snapshot.key
# 目标读 AICRM_DATABASE_URL；本命令仍是数据库 READ ONLY。
bin/migrate-v2-config-definitions --commerce-only --mode=dry-run \
  --snapshot=/secure/commerce.enc --snapshot-key-file=/secure/snapshot.key \
  --manifest-sha256=<inspect-digest> --actor-admin-user-id=<active-admin>
```

加密文件和密钥均为 0600，输出文件存在即拒绝。新 scope 和券 public_slug、
issued_count 进入认证加密快照与摘要；旧 scope 不接受新券字段，新 scope
强制每张券都有这两项（零和空字符串也是显式事实），禁止静默缺省。
业务定义验证、引用完整性和 manifest 自洽检查沿用现有验证器。

dry-run 报告按已有 source_system/source_kind/source_key 查询映射：

- mapped_source_equal：旧源摘要一致，可以复用既有 target_id，不新增重复定义。
- new_source：没有旧映射；商品 code 还须未被占用。仅表示候选新增，不代表已新增。
- conflict_source_drift：源旧行已变，禁止覆盖。
- mapped_source_equal_target_version_changed：目标版本增长，apply还需Owner核对商业依赖事实；不会覆盖运营配置。
- candidate_coupon_delta_owner_check_required：已证明仅源更新时间/发行限额允许变化，还需Owner和计数校验。
- conflict_unmapped_product_code / conflict_target_missing / conflict_target_owner：需核查。

历史 Coupon 源摘要比较剔除旧快照未捕获的两个新字段，维持旧 source map 兼容。
这些新事实仍在完整快照里保留，不宣称旧目标已匹配。即使所有行 mapped，
dry-run尚未验证Owner业务字段和领取计数，因此apply_ready保守为false。
显式apply会在同一事务中完成这些检查；任一冲突均整批回滚。

## 安全 apply

```sh
bin/migrate-v2-config-definitions --commerce-only --mode=apply --confirm-apply \
  --snapshot=/secure/commerce.enc --snapshot-key-file=/secure/snapshot.key \
  --manifest-sha256=<inspect-digest> --actor-admin-user-id=<active-admin>
```

先部署0128_config_definition_commerce_revisions.sql。
在已准备目标备份、源短暂停写且目标尚未承接真实领取前运行。
本命令不建立群计划或Agent Owner实例。批次使用完整快照摘要作为键，
同一源代码版本可以提取不同当前快照，既有source mappings仍按旧稳定源键复用。
迁移通过事务级锁串行化；映射检查、Owner写入、批次收据原子提交。

- 旧源删除及不可证明的源摘要变化：报告冲突，不更新。
- 商品版本增长本身不阻断；Product Owner核对code、name、price、currency、周期duration相等后只复用ID，原描述/素材/库存/状态/跳转/标签等配置全部保留不写。
- 旧商品复用ID；新商品新增。同一code已有未映射商品由Owner唯一约束拒绝。
- 旧券核对名称、状态、金额、每人限额、领取时间、有效期、说明、适用商品。
- 源coupon仅updated_at及total_issue_limit单调增加时，可把当前源行的这两项替换成上次源事实（首次为目标原导入事实）重算旧摘要；精确吻合才允许delta，绝不只凭名字价格授权。
- 0128追加记录原/新源摘要、源更新时间、限额、发行量与批次；旧source maps保持append-only。
- 券public_slug为空时可补源值；已存在且不同则冲突，不能改写或清空公开链接。
- issued_count必须在0和发行总额之间且不低于目标实际claim行数。首次目标0可补源值；已有revision时，目标计数必须等于上次迁移值，源计数只可单调增加。目标真实领取改变计数则拒绝，不能覆盖。
- 已映射券不允许偷偷新增绑定；目标运营修改商业字段后拒绝整个批次。
- 新商品/券可新增；任何后续冲突会回滚此前新增的定义和批次。
- 重放仍检查目标定义、公开链接、计数和映射，不能仅凭批次存在返回成功。

这不是通用增量覆盖器：除严格证明的源时间/限额和审计计数增量外，其他旧源定义修改需另行明确对账，
不能换源键、抹零数量或绕过Owner。claims之后若另行导入，应再重放本批次
核对已领取总数；数字下界通过不等于每条客户领券已迁完，后者仍需独立对账。
verify模式尚未支持该scope，请使用inspect、只读dry-run以及受控apply重放检查。
源/目标实际schema和数量漂移必须报告，没有猜测今天delta，未操作生产。

### Explicitly reviewed source-authoritative coupon exception

If an imported coupon's historical timestamp was overwritten by a target
`coupon.public_shared` event, the original source row digest may be impossible
to reconstruct. Never label this as matching historical source evidence.

For one separately reviewed source coupon, `--mode=review-coupon
--review-coupon-source-id=<id>` captures a canonical Coupon Owner before digest.
The operator reviews the exact source/target delta, then uses
`--review-coupon-source-id=<id> --review-coupon-before-sha256=<digest>` with the
already confirmed full manifest SHA and apply command. This path requires 0132.
It requires zero target claims, no audit events except public sharing, unchanged
commercial terms and targets, monotone source limits/counters and a full Owner
CAS. A different existing test slug can be replaced by the formal source slug;
unique collisions remain errors. New source binding provenance is accepted only
when the complete target-ref set is already identical. All other source rows
still use the ordinary strict migration proof.

The old source mapping remains immutable. A separate append-only review records
`source_authoritative_review`, the full source row and manifest digests, and
before/after Owner digests in the same transaction. Coupon's dedicated audit
permits exactly one same-transaction slug replacement matching old/new slug,
old version and transaction ID; ordinary updates, cross-coupon reuse, later
transactions and second replacements stay forbidden. Immediate replay validates
the recorded after state and makes no writes. After importing historical claims,
perform aggregate/API reconciliation; the original reviewed-before command is
not a general authorization to overwrite later target activity.
