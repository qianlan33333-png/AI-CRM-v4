# 海报弹窗 Product Design QA

final result: passed

范围：本次截图指定的海报弹窗精简；不涉及活动页整体重设计。读取 Product Design index、image-to-code、user-context（preflight已执行）及design-qa。

视觉证据：用户截图 /var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-25a0b0ad-c8a3-42ee-b7b5-89ee4299c8da.jpg；原弹窗移动截图 /Users/qianlan/Documents/Codex/2026-10-10/referral-paid-fix/mobile-poster.jpg 360×715；当前实现 preview-mobile.png 在 /Users/qianlan/Documents/Codex/2026-10-10/referral-poster-compact-release/，浏览器CSS视口实际400×794，截图444×882；同一弹窗及同一真实海报，合并 design-comparison.png 对照并检查，视口比例相同，浏览器外围填充不计为视觉差异。真实公共海报源复用，局部预览会话/API为合成fixture，不称生产认证。

字体/色彩：沿用referral.css原字体、橙色主按钮、浅色关闭及话术框；没有新图标或主题。
布局：可见标题/文件名/下载已移除；完整图片保持比例，二维码未裁切，提示位于图片下方，复制/关闭同一行各155.998CSSpx、同top667.795；海报高度476.667CSSpx（60vh）。
内容：提示与测试话术123保留；只复制原文，空话术隐藏复制，关闭继续可用。
交互：Browser实际点击邀请、复制（显示已复制）、关闭（返回活动、焦点回邀请）；jsdom相关旅程验证左右滑动、迟到图片不覆盖当前、复制拒绝/缺失后的手动选择、关闭重新打开及无图/空话术。
未覆盖：真实微信手机长按保存、真实分享扫码及生产可信用户会话；技术发布另绑定安装包资源与合成旅程。

没有P0/P1/P2视觉问题；本次不是全页面回归。
