# 客户最小档案完整性与微信资料接收

## 业务判断

订单 `v3pay_NST72F4O3DGEOZKG3KAE4VLB44` 已正确归属 canonical Customer `23503`，其 OA OpenID、UnionID 和手机号身份均存在；异常来自 Customer 目录投影缺失。订单页因此只能回退为“客户 #23503”，客户中心又只读取目录投影，最终表现为 `CID-23503` 存在但查不到。H5 OAuth 已读取微信 `/sns/userinfo`，但原实现只保留 OpenID 和 UnionID，主动丢弃了昵称、头像。

必须分别治理：OneID 决定“是谁”，Customer Profile 决定“如何展示”，Customer Directory 决定“能否搜索”。昵称和头像永远不能参与身份匹配或自动合并。

## 行业参考

- [Apache Unomi data model](https://github.com/apache/unomi/blob/master/manual/src/main/asciidoc/datamodel.adoc)：身份/Profile Alias 与可变画像属性分离。
- [Apache Unomi recipes](https://github.com/apache/unomi/blob/master/manual/src/main/asciidoc/recipes.adoc)：多个外部标识指向统一 Profile，事件属性通过明确规则进入画像。
- [RudderStack dbt ID Resolution](https://github.com/rudderlabs/dbt-id-resolution)：Identity Graph stitching 与下游 Profile 生成分层。
- [mParticle IDSync](https://docs.mparticle.com/guides/idsync/introduction/)：以受治理的身份映射形成统一客户视图。

## 架构分类

```text
OneID: provisions customer and links verified OAuth identities; customers.id remains the only canonical key
Persistence: Provider read outside transaction; local identity, minimum profile and payment session in one PostgreSQL UoW
Internal durable job: not added; historical repair is one forward-only idempotent migration
Provider write/external effect: not involved
```

## 实现约束

1. Identity 创建新 canonical Customer 时，通过稳定观察端口在调用者既有事务内建立 Customer 最小目录投影。
2. 最小档案使用“微信用户”和派生的 `CID-<customer_id>`，不保存、猜测或复制外部身份值。
3. H5 OAuth Provider read 返回昵称和头像作为展示事实；Payment Session 在同一事务内交给 Customer Port。
4. 微信资料只能填充空白、最小档案或先前微信资料，不覆盖企微/人工等更高优先级姓名。
5. `0175` 为所有既有 Customer 幂等补齐最小投影，不调用 Provider、不改变身份、不自动合并。
6. readiness 检测任何 active Customer 缺少最小投影并阻止不完整版本继续对外服务。

## 验收

- 新建 canonical Customer、最小档案、身份绑定和支付会话同事务成功或回滚。
- OAuth 昵称/头像能到达 Customer 投影；缺失或非法资料降级为最小档案，不影响身份验证。
- 企微/人工姓名不被后续 OAuth 覆盖。
- 历史回填重复执行仍为单行。
- `CID-23503` 可在客户中心检索并展示；历史真实昵称仅在新的合法授权或可靠资料源出现后更新。
- 唯一生产服务器 `124.220.53.183` 完成迁移、部署、readiness 和生产读回后才算上线。
