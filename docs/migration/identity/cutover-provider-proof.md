# 券与周期会员的身份补齐

分类：OneID resolves/provisions/links，全部通过Identity Port与ProviderHistory；
源capture和target dry-run是repeatable-read/read-only；apply在一个PostgreSQL事务中
提交Owner身份操作和既有identity_history_import_receipts。没有支付、发券、
会员开通、消息、Provider调用或后台任务。不接触手机号。

## 明确证据边界

只读取当前aicrm周期会员(active/expired/refunded)及券领取记录的unionid并集。
候选必须同时满足：

1. crm_user_identity.identity_status=active且有primary_external_userid；
2. wecom_external_contact_identity_map在已核相同Corp下，同external_userid、同unionid且active；
3. raw_profile.errcode是JSON数字0；external_contact下的unionid和external_userid都精确匹配；
4. 该primary_external_userid未被多个不同unionid主体共享。

缺关系、缺Provider成功原文、顶层fallback、inactive、跨Corp、重复主体关系均隔离。
不使用手机号、openid单列、名称、裸unionid或旧person_id猜客户根。
本轮不补公众号OpenID：crm.primary_openid本身不能证明App scope与Provider授权关联。

Corp配置与开放平台配置必须由操作者对比源/目标后显式确认。
Corp来自AICRM_WECOM_CORP_ID，身份namespace为wecom-corp:<Corp>；
UnionID必须显式提供wechat-open-platform:<开放平台ID>，不能用Corp或App替代。
缺失、不同或无前缀时拒绝。代码没有固定任何真实配置ID。

## 命令

只经受保护环境提供源/目标DSN，禁止把连接凭证写到命令行。
密钥是0600普通文件内的32字节无填充base64；输出是独占创建的0600 AES-GCM文件，
不会落盘明文。密钥和加密文件不要放git。目标已备份后才进入apply。

```sh
# 在源网络可达的受控环境，AICRM_SOURCE_DATABASE_URL只读源数据库。
# 显式设置AICRM_WECOM_CORP_ID，并令以下变量含完整namespace：
# AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID=wechat-open-platform:<已核开放平台ID>
bin/migrate-cutover-identities --mode=capture \
  --confirm-matched-provider-scopes \
  --snapshot=/secure/identity-proof.enc --snapshot-key-file=/secure/snapshot.key
bin/migrate-cutover-identities --mode=inspect \
  --snapshot=/secure/identity-proof.enc --snapshot-key-file=/secure/snapshot.key

# AICRM_DATABASE_URL指向目标；dry-run明确为READ ONLY。
bin/migrate-cutover-identities --mode=dry-run \
  --confirm-matched-provider-scopes --manifest-sha256=<inspect摘要> \
  --snapshot=/secure/identity-proof.enc --snapshot-key-file=/secure/snapshot.key
bin/migrate-cutover-identities --mode=apply --confirm-apply \
  --confirm-matched-provider-scopes --manifest-sha256=<inspect摘要> \
  --snapshot=/secure/identity-proof.enc --snapshot-key-file=/secure/snapshot.key
```

输出仅主体散列key、状态和数量，不含unionid/external_userid、姓名、手机号或客户ID。

- candidate_attach：有一个已解析根，先以已存在身份为锚，再链接另一身份。
- candidate_provision：两个身份均未找到，但具备上述完整Provider关系证据，可显式建客。
- already_linked：两个身份已属于同一根。
- quarantine_*：证据不足或跨根，只写隔离回执，不建客、不合并。

apply会重新在本事务内计算计划，使用HistoricalSubjectProvisioner；其中任何Owner
并发冲突导致整批回滚。隔离与成功回执一起提交；出现隔离必须继续报告，不能视作全量完成。
同一加密文件可重放，不新造根、不增重复回执；所有作用域和源证据进入摘要。
完成后必须重跑会员/券自身dry-run和对账，确认原not_found行真正消失。
尚未对生产执行，不能预先保证42行全部具备Provider原文证据。
