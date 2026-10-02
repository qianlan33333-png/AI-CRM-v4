# 已安装周期商品信息收集合约

唯一工作台在新候选准确 SHA 的干净源码目录运行；使用本次已安装包的绝对 binary/root 与真实二进制 SHA256，禁止重新编译被测服务。PostgreSQL 16 使用 loopback、`aicrm_test_*_acceptance_test` 独立库；测试建临时合成库并清理。真实生产产品和外推设置不作为写入夹具。

```sh
AICRM_DATABASE_URL='postgres://TEST_USER@127.0.0.1:5432/aicrm_test_periodic_contact_acceptance_test?sslmode=disable' \
bash scripts/run-installed-periodic-contact.sh \
  --binary /absolute/installed/bin/aicrm \
  --root /absolute/installed/package \
  --sha EXACT_CANDIDATE_SHA \
  --binary-sha256 INSTALLED_BINARY_SHA256
```

驱动校验源码、Host manifest 与安装二进制绑定，独立启动实际包服务，通过真实 HTTPS 代理执行全部页面和 HTTP 写入；源码 Composition 只准备合成夹具、签发可信测试身份和读回数据库，不代替被测服务。

- Chromium 登录、新建周期商品三档、编辑周期/普通商品三档，保存后真实 API 读回、刷新恢复、跨维度草稿隔离及版本推进。
- 每档独立可信合成客户，真实付款页显示对应手机号/地址面板。空、非法手机号及缺地址显示字段错误且零 checkout POST；合法输入产生一次真实 202，读取不可变订单联系人/地址快照。
- 独立 HTTP 矩阵：过期商品版本 409；手机号缺失/非法及收货缺失/缺区名称均遵循现有 Payment 合同返回 409 conflict，拒绝后订单/支付数量不增加。不能将该业务拒绝误写为 400；不改变应用错误合同来迎合测试。合法 202，同幂等键重放返回同一订单。
- 真实签名的合成支付宝回调通过已安装服务验签/结算，读取真实 outbound 冻结载荷中的订单手机号及 type/day/frequency/remark 固定参数。

不启动效果 worker，不执行真实支付或外推接收方网络请求。该收据证明配置→付款→订单快照→冻结外推载荷，不能宣称真实 Provider/接收方已执行。Linux Chromium 和本驱动必须有具名 pass、无 fail/skip；另运行累计包继承的问卷七项已安装合约。旧失败日志保留，最终绿灯只绑定新候选 SHA/tree。
