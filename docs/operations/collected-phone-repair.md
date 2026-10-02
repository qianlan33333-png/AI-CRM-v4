# 已采集手机号入库补录

适用商品下单冻结联系人与问卷 mobile 类型的冻结答案。用户的采集资料与之后支付/退款、商品/问卷状态无关。手机号绑定到原记录已确认的客户，声明手机号不作为 Provider 验证，不按手机号合并客户。来源原值、Identity 加密 Phone Vault、来源收据共同保留事实。

## 候选范围

统一 Customer 采集 Port 在下单/答卷的既有 PostgreSQL UoW 内调用 OneID 并更新目录。商品只要保存联系方式即绑定，支持 mobile 与 shipping_address。问卷每个已填写的 mobile 题都使用相同入口。Provider 不在资料补录中执行。历史补录复用原生来源键 `order-phone:<order_id>`、`survey-phone:<submission_id>:<answer_id>`；重复执行保留原绑定或冲突结果。

新增可执行文件 `bin/migrate-collected-phones` 由现有 release builder 构建并随应用包交付。无 schema 迁移。普通应用安装不运行此命令，不自动启动定时器。生产数据补录由唯一路由指定的发布工作台在准确候选部署后按已有授权与生产迁移备份规则执行，单独保存结果，不绕过失败队首。

## 查看与执行

保持运行目录与 EnvironmentFile 指向准确已安装候选。以下命令仅在工作台核对包、环境、来源范围与授权后执行；参数与输出不含明文手机号或密钥。

```sh
sudo systemd-run --wait --pipe --collect \
  --property=EnvironmentFile=/etc/aicrm/aicrm.env \
  --property=WorkingDirectory=/opt/aicrm/current \
  /opt/aicrm/current/bin/migrate-collected-phones --mode=inspect --source=all
```

`inspect` 使用真正的只读 UoW，仅清点源记录，不尝试绑定、不以回滚写入模拟只读。输出每个来源的 candidates、last_completed_id；该数是采集记录数量，不能称为待绑定用户数。

对每个来源绑定 inspect 的最高 ID，分别 apply：

```sh
# 将 ORDER_WATERMARK / ANSWER_WATERMARK 替换为本次 inspect 的 last_completed_id。
sudo systemd-run --wait --pipe --collect \
  --property=EnvironmentFile=/etc/aicrm/aicrm.env \
  --property=WorkingDirectory=/opt/aicrm/current \
  /opt/aicrm/current/bin/migrate-collected-phones \
  --mode=apply --source=order --through-id=ORDER_WATERMARK

sudo systemd-run --wait --pipe --collect \
  --property=EnvironmentFile=/etc/aicrm/aicrm.env \
  --property=WorkingDirectory=/opt/aicrm/current \
  /opt/aicrm/current/bin/migrate-collected-phones \
  --mode=apply --source=survey --through-id=ANSWER_WATERMARK
```

每条采集记录一个事务；错误时报告 failed_source_id 和 last_completed_id 后停止。用同一 source / through-id / 原来源键重新执行，或用 `--after-id=last_completed_id` 续跑，不能换手机号归属或制造新幂等键。密文损坏或数据库错误不得跳过冒称完成。

结果分开统计 attached、already_linked、conflict、invalid 及 replayed 原结果。历史补录仅填补空的目录主手机号，不覆盖现有资料。未知客户归属的历史问卷保持原值与未归属状态，不创建客户。冲突仍留在来源和收据中，不覆盖其他客户号码；重复冲突不得更新目录。

## 独立读回

- 原订单/答卷加密内容、订单状态和问卷状态不变。
- 成功补录有 OneID 声明手机号、Phone Vault 和源收据；用户档案现有手机号接口/目录可读，不将 declared 冒称 verified。
- 同范围重复 apply 不新增身份/收据，不改变目录新版资料，不新增支付、退款、问卷提交、Outbox 营销效果。
- 订单 1206 / 用户编号 1023763 是本次实例验收对象，需在实际补录后从认证后台读取档案和订单；本地合成测试通过不等于该用户已在生产修好。

2026-10-02 本次只读盘点：订单联系人 247 条（paid 203、pending_payment 33、refunded 11），最高 order_id=1208；问卷 mobile 加密答案 1539 条（resolved 1012、unresolved 504、anonymous 23），最高 answer_id=6778。此为盘点时快照，执行前重新 inspect；归属未知的 527 条原始采集数据已在 Survey 保存，不能按手机号猜测归属。此时用户 1023763 档案与 OneID 手机号仍为空，未执行生产写入。
