# 裂变海报 Product Design QA

final result: passed

## 视觉证据与范围

Source visual truth: /var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-f95b9840-9bb1-4a84-bf76-1b346126280d.png（844×1400像素，设备密度未提供）。当前活动原图1250×2426，保存于 /private/tmp/aicrm-referral-poster-evidence/source-poster.jpg。

Implementation screenshots: /private/tmp/aicrm-referral-poster-evidence/visual-375.png（375×850 CSS px）、visual-430.png（430×850）、visual-430-actions.png、visual-422.png（422×700）。浏览器截图密度1。均由Codex in-app Browser捕获真实构建前端，使用当前公开原图和合成昵称/邀请链接；此视觉fixture不冒充真实业务身份。真实API/可信会话/PG链路另见 browser-oneid.jsonl。

Full-view comparison: comparison-full.png 将源截图按375px宽归一，与375px实现同宽并列；源设备密度未知，所以不声称外层框架1:1匹配，垂直居中和背景数据差异不计为设计漂移。Focused comparisons: comparison-qr.png、comparison-nickname.png；从源截图提取实际海报内容并归一到1250×2426，与实际导出的同尺寸图片比较，检查二维码净空及新昵称，源截图缩放导致的模糊不当作原图质量问题。

## 必检表面

- Typography: 海报原图文字/品牌/讲师保持；昵称为常规30px/1250宽、右对齐、完整字素省略，无额外标签框；米白色及轻微棕色阴影呈低调压印效果。按钮为“生成溯源码”，保留原字号与资格状态文案。
- Spacing/layout: 375视口实际图像与预览同为297px宽，无左右浅色余量；430也填满宽度，图像比例完整，原生纵向滚动可到复制/关闭。默认及目标码框370px，中心与原330框一致。
- Colors: 复用原橙色按钮、浅色次按钮和既有页面token；昵称#F7F0DF加轻微棕色阴影，不与主标题争夺注意。
- Image quality: 原图1250×2426，保存图同尺寸JPEG95；二维码整数像素边界，缩小额外白边、保留四模块净空。五张实际导出由zxing-cpp独立解码为唯一且准确的期待链接。
- Copy/content: 邀请入口更名，话术原文、原生保存提示、复制/关闭保留；没有新增页面、解释性技术文案或新素材。

## 交互与限制

IAB验证375/430生成、复制成功反馈、关闭重开、430滚动到达动作区；console error/warn为空。PG Chromium旅程验证多张切换、话术emoji与空文本、三次点击仅一次签发、当前配置读回、两用户/活动昵称隔离及真实导出。独立扫码证明技术导出，不证明真实微信归因。

Comparison history: 首次有效全图及二维码/昵称局部比较，无P0/P1/P2视觉问题；未因此修改视觉代码。早先npm缓存、沙箱Chrome启动和旧邀请明细测试断言修正属于环境/合同对齐，不算视觉QA迭代。

Follow-up P3: 昵称按批准的30px原图规格在小屏非常低调；保持当前设计。真实微信iOS/Android长按保存和扫码归因尚未执行；截图不宣称完整无障碍合规。
