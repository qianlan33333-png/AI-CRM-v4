# Formal cutover origins and direct acceptance links

Read-only audit: active process environment + protected credential candidate + protected grouped origin override. No settings changed. Active Config release contains no values pointing at either old test/auth domain. Only the following origin keys need active configuration change; their staged overrides are already ready.

| Key | Active needs change | Candidate needs change |
|---|---|---|
| AICRM_PUBLIC_ORIGIN | yes | no |
| AICRM_H5_PUBLIC_ORIGIN | yes | no |
| AICRM_WECHAT_PAY_PRIVATE_KEY_PATH | no; existing file readable by service | preserve existing verified path or install candidate correctly |
| AICRM_WECHAT_PAY_PLATFORM_CERT_PATH | no; existing file readable by service | preserve existing verified path or install candidate correctly |

`audit-origins-readonly.py` prints names and booleans only, never values. It checks known candidate/process settings, not every arbitrary URL inside business payloads. Source credential staging is not an activated EnvironmentFile. Current protected grouped override must actually participate in API and worker service configuration at cutover.

Origin code: `internal/platform/config/config.go:341,569` defaults PublicOrigin and falls H5PublicOrigin back to it. `cmd/aicrm/composition.go:154` derives admin/sidebar OAuth callbacks;715 survey OAuth callback uses H5 origin;1123 derives new payment/refund notify from PublicOrigin;1168 derives H5 payment OAuth callback;1476 sidebar sharing uses PublicOrigin;1742 applies H5 entry origin redirect. Public products return relative `/p/{code}` and `/s/{code}` paths; surveys use `/q/{slug}`; coupons use `/c/{public_slug}`. Relative paths follow the formal origin once the service/edge switch is active.

Old coupon GET `/c/{public_slug}` matches V3 `/c/` exactly, so no extra alias needed. Preserve original slug spelling with0133. Coupon OAuth uses H5 payment OAuth and returns to the same relative path. No order-specific customer URL is published in this checklist.

## Credential installation gate

Both staged payment PEMs are root0600. Current service is `aicrm`; both existing active file paths were checked readable by that account. If content matches intended source credential, keep the existing paths. If installing staged credentials: create a dedicated directory root:aicrm0750, install private key/certificate root:aicrm0640, set the two path keys to those installed files, verify `sudo -u aicrm test -r` for each and validate key/certificate metadata without printing contents. Validate any platform public-key/certificate serial mode as a complete credential group. Never chmod the root staging directory world-readable or point the service to `/root/...`. Back up previous paths/files before activation; do not restart only half of the API/worker credential group.

## Manual checks — only after formal DNS/edge switch

These are actual public resource paths read from target, not invented IDs. Current target local readback: product and period pages200, survey303 to authorization, coupon200. This confirms routes/resources, not WeChat authorization or payment completion. Current formal DNS still serves the old environment until switching.

- [问卷](https://www.youcangogogo.com/q/ai)：微信里打开，确认授权后出现问卷；本轮不用提交。
- [普通商品](https://www.youcangogogo.com/p/subscription_trial_month)：看名称、价格与报名按钮；不要付款。
- [周期商品](https://www.youcangogogo.com/s/lianmeng)：微信里看有效期与会员状态；不要续费。
- [优惠券](https://www.youcangogogo.com/c/cp-e6307b9111d4640a1012cc8d)：看券名、金额、有效期；不要领取。

订单记录涉及个人信息，没有无身份公开分享链接；正式后台登录后进入“交易管理”，按本人测试订单查询。要验证新支付成功/退款/领券，需要另外安排一次明确授权的真实业务验收，不能将上述页面打开成功当作资金链路验收。

历史问卷验收使用实际已迁移的 `/q/ai`（演练Owner读回enabled=true）；`launch0908-basic-survey` 是既有测试入口，不用它证明历史问卷迁移完整。额外测试问卷12仍published，须正式上线前处理；额外券16为draft、不可领。源36商品=普通34+周期2，目标普通35包含额外测试商品1，分类读回无漏项。
