# npm 本地入口完整性实施计划

> 适用于 AI-CRM-v4 的隔离分支；按 V4 开发门禁记录精确 tree 和适用验证。

**目标：** 删除三个被证实无目标文件的 npm 别名，并用轻量回归避免 npm scripts 再引用不存在的仓内脚本。  
**方法：** 在 `scripts/qa/` 用 Python 标准库解析 package.json 中静态仓内脚本路径；把测试接入既有 `npm test`。保留 Go/PostgreSQL/Chromium 及 GitHub quality lanes 原样。  
**架构：** OneID 不涉及；Persistence 为 stateless；External Effects 不涉及。

---

### Task 1：加入脚本目标回归

**文件：**

- 新建：`scripts/qa/test_package_script_targets.py`

**步骤：**

1. 测试 package.json 中有效 `scripts/` 和 `web/scripts/` 引用全部是现存文件。
2. 用临时根目录测试缺失脚本能返回 script 名与路径。
3. 验证不带仓内路径的 npm、node 和 shell 命令不误报。
4. 运行 `python3 scripts/qa/test_package_script_targets.py`。

### Task 2：移除失效入口并接入 npm 测试

**文件：**

- 修改：`package.json`
- 修改：`scripts/qa/test_package_script_targets.py`（保留针对当前仓库的检查）

**步骤：**

1. 删除 `edge:contract`、`release:contract`、`deploy:check` 三个指向不存在文件的命令。
2. 新增 `qa:script-targets`，运行 Python 回归；把它放在 `npm test` 的第一步。
3. 从 `ci` 命令中移除已经删除的 `edge:contract` 与 `release:contract` 调用；其它命令不变。
4. 运行 `npm run qa:script-targets`、`python3 -m unittest discover -s scripts/ci -p 'test_*.py'`、`python3 scripts/dev_preflight.py fast`。
5. 确认 `git diff --check`、入口目标静态审计和 HEAD/tree；记录 Node/npm 锁定版本阻塞。不得因该阻塞降低版本或冒称 `npm test`/完整质量 lane 通过。

## 完成定义

- 三个现行坏目标不再声明或被 `ci` 调用。
- 本地回归对现存和缺失目标都有确定性断言。
- 现有测试入口不被重写；GitHub shared CI 不变。
- PR 与提交准确绑定 `origin/main` 基线及 tree；未满足的 Node/npm、浏览器、PG 和真实业务证据在 handoff 明示。
