# 问卷二维码、手机号与 OAuth 上线实施记录

## 开发前分类

- OneID：涉及。OAuth Provider 验证后只通过 Identity Port 执行 Resolve/显式 Provision；手机号仅通过 `AttachDeclaredPhoneToCustomer` 附着到既有 Customer，不建客、不合并、不升级 verified。
- 持久化：涉及。答卷、加密答案、手机号绑定收据、Identity 收据、审计、Outbox 与 Customer 时间线加入同一个 PostgreSQL Unit of Work。
- Provider：仅微信公众号 OAuth 换码读取；不涉及 Provider 写入、Outbound 或 External Effects，生产外部效果开关保持原状。

## 交付合同

1. 发布闭包从 manifest 的 source input 精确定位 Survey QR 动态 chunk，并同时校验 manifest 元数据、文件字节和依赖。
2. 分享地址统一为同源 HTTPS `/q/{slug}`，最大 2,048 字节；SVG 生成失败必须显示错误且保留复制链接。
3. 新手机号只接受 `^1[3-9][0-9]{9}$`。Survey 使用自身 AES-GCM 密钥保存答案；Identity 使用独立主密钥经 HKDF 派生 AES-GCM 与 HMAC 密钥。
4. `customer_identities.normalized_value` 不保存新手机号明文；`phone:cn11` 只保存 lookup digest，密文与掩码归 Identity 表。
5. `/q/{slug}` 是唯一入口。微信外禁止填写；微信内由用户点击发起 `snsapi_userinfo`，直接访问定义与提交 API 均要求 resolved session。
6. 历史匿名结果 token 只读兼容；新结果必须由同一 Customer session 查询。

## 上线与回滚门禁

1. 合并后自动发布先生成独立手机号密钥并应用 schema；服务先以 OAuth disabled 双读运行。
2. 备份目标库后运行 `migrate-identity-phone-vault inspect/apply/reconcile`；要求源数=迁移数=密文数、明文数=0、投影缺失=0、Customer 数不变。
3. 问卷历史迁移批次不得重跑，迁移前后问卷、提交、答案数量必须保持不变。
4. 只有完整回调域名已在微信公众平台保留配置且开放平台 scope 可确认时，才写入 OAuth 配置并灰度真实问卷。
5. OAuth 配置失败时保持 disabled；不得回退为匿名提交，不得依赖 v2 OAuth。
