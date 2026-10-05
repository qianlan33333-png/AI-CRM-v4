# 商品与分销技术上线

用户已授权读取“逐链路修复商品与分销”并处理上线。原任务 01a10772-aeae-76df-9ec8-2d9e090e2089；业务候选 fb676f417010eb85f131e81ab4ee9e6ef987f4dd / tree def0a86854500906e352504ea4ad3e268db1e9c6；基线 94d4b845f9d9ddcc30e7e05432032ba1677fac5a。

本交付调整发布清单及检查准备，不改应用行为。OneID/持久化/效果沿用父 brief 分类。macOS 双 WebKit 诊断复用开发侧已完成收据与核对后的日志哈希，从 Linux 清单移除，不冒称 Linux 执行。相关包及 HTTP/PG/任务恢复直接消费者在 Linux 执行，不全仓回归。

预发使用最终安装二进制/资源，在隔离合成 PG 执行 periodic-contact HTTP/Chromium 商品、支付签名回调与权益旅程；对当前预发进行商品、周期商品、裂变及分销认证只读读取。生产同包晋级，0222 迁移前备份，独立读取版本、服务、迁移和相关业务。真实 Provider、到账、真机及库存新语义不在技术成功声明内。

开发证据：/Users/qianlan/Documents/Codex/2026-10-04/task/commerce-evidence/final-receipt-phase2.json，原 Library 第 4 版。本机已核对最终收据 相关日志哈希及通过状态。最终应用构建一次，不额外编译 cmd/aicrm。

Linux attempt 1 因后端夹具 artifact 准备缺 esbuild 失败，未安装应用。取原开发任务共享工作区的两处 CI 修复：artifact cache miss 使用既有 npm_dependencies 准备根包与 web/v3；有效 cache hit 仍复用。该改动不涉及已安装固定控制器文件，不启用第二发布通道。相关准备合同和实际 Linux HTTP/PG 验证后重提原队首。未纳入原任务的 Survey 新审查。

4e9a150 的准备合同在 Linux 原进程中暴露两项测试隔离缺陷：独立锁模拟进程继承 AICRM_HEAVY_SLOT_HELD=1，跳过自己的临时命名空间锁。用同环境先红后绿验证，slot_worker 与 native-tool 两个独立子进程仅清除继承标记；继承父锁的专门子用例仍保留，生产锁代码不改。
