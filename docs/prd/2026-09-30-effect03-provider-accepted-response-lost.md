# EFFECT-03 平台接收后响应丢失的隔离回归

## 业务判断

```mermaid
flowchart LR
  A[业务效果与 River 任务同事务入库] --> B[Worker 发起一次 HTTP 写入]
  B --> C[隔离平台记录请求已接收]
  C --> D[连接关闭，客户端未收到响应]
  D --> E[本地记录 outcome_unknown]
  E --> F[重复任务及人工重试均不得再发]
```

平台接受与客户端收到响应是两个事实。此用例用独立 loopback HTTP 服务记录接受事实，再无响应地断开连接；随后从 PostgreSQL 读取本地尝试、任务和效果状态。测试只证明隔离模拟器上的不重复发送，不证明真实企微收件或平台查单能力。

## 复用与参考

- 复用现有 External Effects PostgreSQL repository、River 入队与 `RunAttempt` 状态机；不另建队列或效果表。
- [Go `httptest.Server`](https://pkg.go.dev/net/http/httptest) 提供真实 loopback HTTP 客户端/服务端边界；[Go `http.Hijacker`](https://pkg.go.dev/net/http#Hijacker) 允许服务端接收请求后关闭连接而不写响应。
- [Stripe 的幂等请求文档](https://docs.stripe.com/api/idempotent_requests) 是“连接错误发生在服务端处理之后”的市场参考；其可重试语义依赖平台幂等键，不能套用到没有同等保证的企微写入。本仓在未知结果时保守停止自动重试。

## 范围与验收

同一合成效果进入队列；模拟平台恰好接收一次；客户端观察网络错误；本地 effect 与 attempt 均为 `outcome_unknown` 且记录 `call_attempted=true`；同一 River job 重放与人工 retry 均不能产生第二次平台请求。首次失败与环境错误保留独立日志。无真实外发、身份或资金操作；不新增长度、数量或权限限制。

OneID 不涉及：仅使用合成 effect 摘要。持久化和内部任务涉及：复用现有 PostgreSQL/River。External Effects 涉及：通过现有 Port 与状态机完成。对外合同、业务实现、页面和数据库结构均不修改；只新增跨 HTTP 与 PostgreSQL 的回归证据。
