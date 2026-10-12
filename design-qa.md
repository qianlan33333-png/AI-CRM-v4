# 问卷图片控件设计 QA

Source visual truth：`/Users/qianlan/.codex/generated_images/01a12dad-c7da-72a0-9173-293fab637ed2/exec-a7279460-97c9-4802-a350-f9c3beb74578.png`；用户已选择第一款并要求去掉两处说明。

Implementation：`/Users/qianlan/Documents/Codex/2026-10-13/survey-image-preview/iab-selected.jpg`；空态 `iab-empty.jpg`。Codex IAB本机当前V4资源与H5Controller，CSS viewport390×844、DPR1、visualViewport scale1；已选态单图片、圆叉删除与已选择图片。source853×1844归一390×844；implementation390×844无需缩放。

Full-view evidence：`/Users/qianlan/Documents/Codex/2026-10-13/survey-image-preview/full-comparison.png`；focused picker evidence：`/Users/qianlan/Documents/Codex/2026-10-13/survey-image-preview/picker-comparison.png`，两个实际区域同输入对照，已打开核对。

Findings：无待修复P0/P1/P2。
- 字体：原H5中文sans-serif、标题与必答层级沿用；状态14px/22px，格式提示与既有问卷一致。
- 布局：保留原H5进度/问题/底部导航，只改变附件区域；112px左对齐方框、16px状态间距、12px圆角，格式提示和图之间有可读留白。概念图的额外顶部标题不新增为第二导航，真实微信原页面框架保留，属本次范围边界。
- 色彩：沿用Survey蓝色tokens与浅灰画布、白色题目卡；蓝色勾。圆叉有白圈，背景采用较深灰而非概念图浅灰，结合44px触控满足用户“更醒目、大一点”的意图。
- 图像：输入内容为用户这次截图，照片内容允许随所选文件变化，不作为产品固定资产；实际自然像素96×96，112px显示中心裁切，明显是小预览，未展示原图长串文件名。官方Heroicons三枚MIT图标，未手绘替代图标或添加网络图标源。
- 文案：保留“已选择图片”，去掉“刷新后需重新选择”和“提交问卷时上传”日常提示；空态加号、图片本身可重新选择、叉号accessible name为删除图片。

Interaction evidence：IAB实际选图、删除后回到加号、重新选择，read-only DOM自然宽度96、页面两处文案消失、status已选择图片；console errors/warnings为空。设计预览使用本机只读定义fixture、POST明确拒绝，不冒称真实提交；真实PG/Chromium两模式另有答卷/图片/核销1/1/1、自然图像加载、替换删除与拒绝保留、后台安全读取证据。

Comparison history：本次第一次比对无需要修改的P0/P1/P2。有意差异是圆叉对比度/触控面积、原框架与用户输入图片内容，已按本次用户意图和现有H5约束确认。

Implementation checklist：本地cropped96pxdata预览；原File提交；异步替换/删除/版本竞态拒绝；错误保留；既有CSP不扩大；真实两模式提交与安装包旅程。

Follow-up polish：无新增页面、图片中心或资源上传；正式Linux/最终安装包和生产手机微信读回由唯一工作台形成独立证据。

final result: passed
