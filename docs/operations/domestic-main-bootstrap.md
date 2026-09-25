# CRM v4 国内主仓：一次性主机准备与切换

> **切换操作当前禁用。** 旧发布队列必须按候选顺序处理到选定的 GitHub `main` SHA，并逐项确认运行时变化已安装、健康。随后停止旧入口，把最新 `main` 普通合入 PR #46，确认从两机已安装应用到该 `main` 的分类结果为 `runtime_changed=false`。stage 与 production 已安装应用的 SHA/tree/manifest 须相互一致且健康；源码 `main` 以该 app SHA 为 first-parent 祖先；旧发布 service/timer 已停止，所有结果不明项已只读对账。源码 `main` 与已安装应用 SHA 不要求相等；新 baseline 会绑定准确的 source main SHA/tree 和独立记录的 app identity。未满足这些条件时不得执行任何写命令、基线初始化、构建演练或 timer 激活。新流程的日常入口见[国内主仓发布](domestic-main-release.md)。

## 一次性主机准备与切换

以下步骤仅供一次性切换，且必须按此顺序：旧发布器处理至选定的 GitHub `main` SHA，并按候选顺序逐项确认所有运行时变化已安装；再停止旧 timer/service；把最新 `main` 普通合入 PR #46，确认 app→main 分类为 `runtime_changed=false`。之后分别读回准确的 GitHub `main` 源码 SHA/tree 和 stage、production 已安装 app SHA/tree/manifest 及健康状态，并确认两台机器的 app identity 相等。源码 `main` 可晚于 app，但必须以 app SHA 为 first-parent 祖先且 app→main 仅含无运行时影响的工具/文档改动。所有旧发布任务已停止且结果不明项已对账后，才由单一执行者依次操作。任何一项未满足都保持新 timer disabled。执行前将命令中的所有尖括号占位符替换为已核实的值，不要原样粘贴。GitHub 凭据只留在开发者电脑；预备机不保存 GitHub 凭据。完整步骤见本文件顶部的禁用说明。`deploy/domestic-main-release-example.json` 是脱敏模板，必须按真实合成数据库访问方式复核；初始 `production_enabled` 保持 `false`。

1. **确认旧发布器空闲，再停用旧入口。** 先只读检查，不得有运行中的旧 service 或未知发布结果：

   ```sh
   sudo systemctl is-enabled aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.service || true
   sudo systemctl list-jobs
   ```

   仅确认没有任务在跑、账本没有待对账结果后执行：

   ```sh
   sudo systemctl disable --now aicrm-domestic-release.timer
   sudo systemctl stop aicrm-domestic-release.service
   sudo systemctl is-enabled aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.service || true
   ```

   结果须为 disabled/inactive。新 `aicrm-domestic-main-release.timer` 也必须 disabled/inactive。

2. **准备最小权限账号与目录。** 先只读核对 `/opt/aicrm` 和 `/opt/aicrm/domestic` 均为真实目录、root 拥有且不允许组或其他用户写入；核对现有子目录、账户和组，不对 `/opt/aicrm/domestic` 内容递归改权。

   在 stage 和 production 分别执行这项父目录只读预检。stage 会在 source bundle/cursor 操作前校验该条件，production helper 也会在首次写入前校验。若检查不通过先停下盘点，不递归修复，也不继续安装工具：

   ```sh
   sudo test -d /opt/aicrm && sudo test ! -L /opt/aicrm
   sudo test -d /opt/aicrm/domestic && sudo test ! -L /opt/aicrm/domestic
   sudo stat -c '%U:%G %a %n' /opt/aicrm /opt/aicrm/domestic
   sudo find /opt/aicrm/domestic -maxdepth 2 -mindepth 1 -printf '%y %u:%g %m %p\n'
   getent group aicrm-release-push
   id aicrm-release-push
   id aicrm-build
   ```

   只有不存在时才创建专用组/用户，并锁定密码；若对象已存在但身份、主组或 home 不符，停止人工核对，不自动改造：

   ```sh
   if ! getent group aicrm-release-push >/dev/null; then sudo groupadd --system aicrm-release-push; fi
   if ! id -u aicrm-release-push >/dev/null 2>&1; then
     sudo useradd --system --gid aicrm-release-push --create-home \
       --home-dir /var/lib/aicrm-release-push --shell /bin/sh aicrm-release-push
   fi
   sudo passwd --lock aicrm-release-push
   id aicrm-release-push
   id aicrm-build
   ```

   若 `/opt/aicrm/domestic` 缺失，经确认目标不存在后仅创建该父目录；若它或 `/opt/aicrm` 是链接、属主不为 `root:root` 或组/其他用户可写，停止并盘点，不能递归改权。已满足上述条件时保留父目录，不重复 chown/chmod。后续只创建本流程明确列出的子目录：

   ```sh
   if ! sudo test -e /opt/aicrm/domestic && ! sudo test -L /opt/aicrm/domestic; then
     sudo install -d -o root -g root -m 0755 /opt/aicrm/domestic
   fi
   sudo install -d -o root -g root -m 0755 /opt/aicrm/domestic/control
   sudo install -d -o root -g root -m 0755 /opt/aicrm/domestic/control/work
   sudo install -d -o aicrm-build -g aicrm-build -m 0750 /opt/aicrm/domestic/build-worker
   sudo install -d -o aicrm-build -g aicrm-build -m 0750 /opt/aicrm/domestic/build-worker/tmp
   sudo install -d -o aicrm-build -g aicrm-build -m 0750 /opt/aicrm/domestic/build-worker/cache/go-build
   sudo install -d -o aicrm-build -g aicrm-build -m 0750 /opt/aicrm/domestic/build-worker/cache/go-mod
   sudo install -d -o aicrm-build -g aicrm-build -m 0750 /opt/aicrm/domestic/build-worker/cache/npm
   ```

   `aicrm-release-push` 是单独的系统组和锁定密码的专用账号，主组为该组；不要把 `aicrm-build` 加入推送组。仅给推送账号一把来自开发者电脑的公钥，`authorized_keys` 文件由该账号拥有、模式 `0600`，`.ssh` 目录模式 `0700`。公钥行格式如下，先替换尖括号占位符：

   ```text
   no-agent-forwarding,no-port-forwarding,no-pty,no-user-rc,no-X11-forwarding,command="/usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py restricted-ssh" ssh-ed25519 <MAC_PUBLIC_KEY_BASE64> <KEY_LABEL>
   ```

   该强制命令只接受固定裸仓的 Git fetch/push、`domestic-submit` 和格式固定的 `domestic-archive-ack --sha <40位小写 SHA>`；归档 SHA 经 stdin 交给 root controller，不会进入 sudo 命令参数。仓库钩子再限制开发者只能快进更新 `refs/heads/codex/*`。按下列命令安装已审查的公钥文件；不要在该文件中加入其他 key：

   ```sh
   sudo install -d -o aicrm-release-push -g aicrm-release-push -m 0700 /var/lib/aicrm-release-push/.ssh
   sudo install -o aicrm-release-push -g aicrm-release-push -m 0600 <REVIEWED_AUTHORIZED_KEYS_FILE> /var/lib/aicrm-release-push/.ssh/authorized_keys
   ```

   stage 的 `/usr/bin/sudo` 是 sudo-rs。使用 sudo-rs 支持的可执行文件和参数精确匹配：归档 SHA 不进入 argv，root endpoint 只接受最多 512 字节、只含一个 `sha` 字段且没有重复 JSON 键的 stdin 请求。sudoers 中只授权下面两条固定命令；不要加参数正则、通配符或命令别名续行。先确认正式规则不存在；将下列文件安装为 `root:root 0440`，运行 `visudo -cf` 检查临时文件和完整 sudoers 配置；两次检查都通过后才原子改名，再用 `sudo -l -U aicrm-release-push` 读回，确认只列出这两条固定命令。失败就删除临时文件并停止，不安装规则：

   ```sh
   set -euo pipefail
   sudo test ! -e /etc/sudoers.d/aicrm-domestic-release
   sudo test ! -e /etc/sudoers.d/aicrm-domestic-release.new
   sudo sh -c 'umask 077; cat > /etc/sudoers.d/aicrm-domestic-release.new' <<'EOF'
   aicrm-release-push ALL=(root) NOPASSWD: /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py submit-stdin --config /etc/aicrm/domestic-main-release.json
   aicrm-release-push ALL=(root) NOPASSWD: /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py archive-ack-stdin --config /etc/aicrm/domestic-main-release.json
   EOF
   sudo chown root:root /etc/sudoers.d/aicrm-domestic-release.new
   sudo chmod 0440 /etc/sudoers.d/aicrm-domestic-release.new
   sudo visudo -cf /etc/sudoers.d/aicrm-domestic-release.new
   sudo visudo -cf /etc/sudoers
   sudo mv /etc/sudoers.d/aicrm-domestic-release.new /etc/sudoers.d/aicrm-domestic-release
   sudo visudo -cf /etc/sudoers
   sudo -l -U aicrm-release-push
   sudo -l -U aicrm-release-push /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py archive-ack-stdin --config /etc/aicrm/domestic-main-release.json
   if sudo -l -U aicrm-release-push /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py archive-ack-stdin --config /etc/aicrm/domestic-main-release.json --sha aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; then
     echo 'unexpected sudo grant for extra arguments' >&2
     exit 1
   fi
   ```

3. **安装并核验固定工具字节。** 在包含 `<EXACT_MAIN_SHA>` 的 V4 工作树先记录每个文件的源 SHA-256。stage 安装 `scripts/domestic_main_release.py`、`scripts/domestic_release.py`、`scripts/domestic_release_build.py`、`deploy/domestic-promote.py` 和两个 unit；production 只安装 `deploy/domestic-promote.py`。安装前把现有同路径文件复制到 root-only 回滚副本；用同目录 `.new` 文件安装、比对摘要后再原子改名。禁止在服务运行中替换 helper。

   ```sh
   set -euo pipefail
   MAIN_SHA=<EXACT_MAIN_SHA>
   test "$(git rev-parse HEAD)" = "$MAIN_SHA"
   test -z "$(git status --porcelain)"
   for path in scripts/domestic_main_release.py scripts/domestic_release.py \
     scripts/domestic_release_build.py deploy/domestic-promote.py \
     deploy/aicrm-domestic-main-release.service deploy/aicrm-domestic-main-release.timer; do
     digest=$(git show "$MAIN_SHA:$path" | sha256sum | cut -d ' ' -f 1)
     printf '%s  %s\n' "$digest" "$path"
   done
   sudo systemd-analyze verify deploy/aicrm-domestic-main-release.service deploy/aicrm-domestic-main-release.timer
   ```

   stage 工具先备份再安装为同目录 `.new`，比较文件摘要完全相同后才 `mv` 到正式路径：

   ```sh
   set -euo pipefail
   sudo install -d -o root -g root -m 0755 /usr/local/libexec/aicrm
   for path in /usr/local/libexec/aicrm/domestic_main_release.py \
     /usr/local/libexec/aicrm/domestic_release.py \
     /usr/local/libexec/aicrm/domestic_release_build.py \
     /usr/local/libexec/aicrm/domestic-promote.py \
     /etc/systemd/system/aicrm-domestic-main-release.service \
     /etc/systemd/system/aicrm-domestic-main-release.timer; do
     if sudo test -e "$path"; then
       sudo test ! -e "$path.pre-domestic-main"
       sudo install -o root -g root -m 0600 "$path" "$path.pre-domestic-main"
     fi
   done
   for path in /usr/local/libexec/aicrm/domestic_main_release.py.new \
     /usr/local/libexec/aicrm/domestic_release.py.new \
     /usr/local/libexec/aicrm/domestic_release_build.py.new \
     /usr/local/libexec/aicrm/domestic-promote.py.new \
     /etc/systemd/system/aicrm-domestic-main-release.service.new \
     /etc/systemd/system/aicrm-domestic-main-release.timer.new; do
     sudo test ! -e "$path"
   done
   sudo install -o root -g root -m 0755 scripts/domestic_main_release.py /usr/local/libexec/aicrm/domestic_main_release.py.new
   sudo install -o root -g root -m 0755 scripts/domestic_release.py /usr/local/libexec/aicrm/domestic_release.py.new
   sudo install -o root -g root -m 0755 scripts/domestic_release_build.py /usr/local/libexec/aicrm/domestic_release_build.py.new
   sudo install -o root -g root -m 0755 deploy/domestic-promote.py /usr/local/libexec/aicrm/domestic-promote.py.new
   sudo install -o root -g root -m 0644 deploy/aicrm-domestic-main-release.service /etc/systemd/system/aicrm-domestic-main-release.service.new
   sudo install -o root -g root -m 0644 deploy/aicrm-domestic-main-release.timer /etc/systemd/system/aicrm-domestic-main-release.timer.new
   ```

   在替换正式文件前，对全部 `.new` 文件逐个比较准确 `MAIN_SHA` 的 Git blob 摘要；任一不符即停止，不能执行后续 `mv`：

   ```sh
   set -euo pipefail
   while IFS=' ' read -r source target; do
     expected=$(git show "$MAIN_SHA:$source" | sha256sum | awk '{print $1}')
     actual=$(sudo sha256sum "$target" | awk '{print $1}')
     test "$actual" = "$expected" || { printf 'hash mismatch: %s\n' "$target" >&2; exit 1; }
   done <<'EOF'
   scripts/domestic_main_release.py /usr/local/libexec/aicrm/domestic_main_release.py.new
   scripts/domestic_release.py /usr/local/libexec/aicrm/domestic_release.py.new
   scripts/domestic_release_build.py /usr/local/libexec/aicrm/domestic_release_build.py.new
   deploy/domestic-promote.py /usr/local/libexec/aicrm/domestic-promote.py.new
   deploy/aicrm-domestic-main-release.service /etc/systemd/system/aicrm-domestic-main-release.service.new
   deploy/aicrm-domestic-main-release.timer /etc/systemd/system/aicrm-domestic-main-release.timer.new
   EOF
   ```

   比对 `.new` 文件与准确 main 源摘要后，才原子替换并读回：

   ```sh
   sudo mv /usr/local/libexec/aicrm/domestic_main_release.py.new /usr/local/libexec/aicrm/domestic_main_release.py
   sudo mv /usr/local/libexec/aicrm/domestic_release.py.new /usr/local/libexec/aicrm/domestic_release.py
   sudo mv /usr/local/libexec/aicrm/domestic_release_build.py.new /usr/local/libexec/aicrm/domestic_release_build.py
   sudo mv /usr/local/libexec/aicrm/domestic-promote.py.new /usr/local/libexec/aicrm/domestic-promote.py
   sudo mv /etc/systemd/system/aicrm-domestic-main-release.service.new /etc/systemd/system/aicrm-domestic-main-release.service
   sudo mv /etc/systemd/system/aicrm-domestic-main-release.timer.new /etc/systemd/system/aicrm-domestic-main-release.timer
   sudo systemd-analyze verify /etc/systemd/system/aicrm-domestic-main-release.service /etc/systemd/system/aicrm-domestic-main-release.timer
   sudo systemctl daemon-reload
   sudo systemctl is-enabled aicrm-domestic-main-release.timer || true
   ```

   **预备机 npm 工具树的固定权限。** isolated `build_path` 必须包含 `/opt/aicrm/toolchain/npm/bin`，并由 `_verify_build_toolchain` 检查实际解析到的工具和父链；不得删除或放宽这项所有权检查。已知预备机的 npm 入口当前为 `ubuntu:ubuntu 775`，该状态会被检查拒绝。变更权限前，从官方 `npm@11.12.1` tarball 独立复核已安装内容：tarball 地址为 `https://registry.npmjs.org/npm/-/npm-11.12.1.tgz`，固定 SRI 为 `sha512-zcoUuF1kezGSAo0CqtvoLXX3mkRqzuqYdL6Y5tdo8g69NVV3CkjQ6ZBhBgB4d7vGkPcV6TcvLi3GRKPDFX+xTA==`。在 stage 以 root 运行以下只读脚本；它先校验官方 tarball 的 SHA-512 SRI，再按每个 `package/` 常规文件的相对路径和 SHA-256 对比已安装树，并检查树内全部 symlink 均指向该树内常规文件。任一摘要、文件数、路径集合或链接不符就停止，不能 chown：

   ```sh
   sudo /usr/bin/python3 - <<'PY'
   import base64, hashlib, hmac, io, json, pathlib, tarfile, urllib.request

   root = pathlib.Path('/opt/aicrm/toolchain/npm')
   package = root / 'lib/node_modules/npm'
   if root.is_symlink() or package.is_symlink() or not package.is_dir():
       raise SystemExit('fixed npm package directory is missing or linked')
   url = 'https://registry.npmjs.org/npm/-/npm-11.12.1.tgz'
   expected_sri = 'sha512-zcoUuF1kezGSAo0CqtvoLXX3mkRqzuqYdL6Y5tdo8g69NVV3CkjQ6ZBhBgB4d7vGkPcV6TcvLi3GRKPDFX+xTA=='
   with urllib.request.urlopen(url, timeout=60) as response:
       tarball = response.read()
   actual_sri = 'sha512-' + base64.b64encode(hashlib.sha512(tarball).digest()).decode('ascii')
   if not hmac.compare_digest(actual_sri, expected_sri):
       raise SystemExit('npm tarball SRI mismatch')

   expected = {}
   with tarfile.open(fileobj=io.BytesIO(tarball), mode='r:gz') as archive:
       for member in archive.getmembers():
           if member.isdir():
               continue
           path = pathlib.PurePosixPath(member.name)
           if (not member.isfile() or not path.parts or path.parts[0] != 'package'
                   or len(path.parts) < 2 or any(part in {'', '.', '..'} for part in path.parts)):
               raise SystemExit('unexpected npm tarball entry')
           relative = pathlib.PurePosixPath(*path.parts[1:]).as_posix()
           stream = archive.extractfile(member)
           if stream is None or relative in expected:
               raise SystemExit('invalid or duplicate npm tarball file')
           expected[relative] = hashlib.sha256(stream.read()).hexdigest()

   actual = {}
   for path in package.rglob('*'):
       if path.is_symlink():
           continue
       if path.is_file():
           actual[path.relative_to(package).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
   if actual != expected:
       raise SystemExit('installed npm package files differ from verified tarball')
   manifest_sha = hashlib.sha256(json.dumps(actual, sort_keys=True).encode()).hexdigest()
   if len(actual) != 1774 or manifest_sha != 'b029ca86a35722ec0ae0eb1b21698f4dfe8b5508fb94de460c115694604f7cc1':
       raise SystemExit('npm package manifest differs from reviewed stage evidence')

   links = [path for path in root.rglob('*') if path.is_symlink()]
   root_real = root.resolve(strict=True)
   if len(links) != 9:
       raise SystemExit('unexpected npm symlink count')
   for link in links:
       target = link.resolve(strict=True)
       try:
           target.relative_to(root_real)
       except ValueError:
           raise SystemExit('npm symlink escapes its fixed tool tree')
       if not target.is_file():
           raise SystemExit('npm symlink target is not a regular file')
   print(json.dumps({'npm_version': '11.12.1', 'regular_files': len(actual),
                     'manifest_sha256': manifest_sha, 'tree_symlinks': len(links)}, sort_keys=True))
   PY
   ```

   只读核验通过后，才把**固定 npm 工具树**设为 root 所有并去掉组/其他用户写权限；这个变更仅限该 npm 树，不涉及 `/opt/aicrm/domestic` 或共享 cache。随后用隔离账号读回版本，控制器完整 toolchain probe 仍须通过：

   ```sh
   sudo chown -hR root:root /opt/aicrm/toolchain/npm
   sudo find -P /opt/aicrm/toolchain/npm -type d -exec chmod go-w {} +
   sudo find -P /opt/aicrm/toolchain/npm -type f -exec chmod go-w {} +
   sudo stat -c '%U:%G %a %n' /opt/aicrm/toolchain/npm /opt/aicrm/toolchain/npm/bin /opt/aicrm/toolchain/npm/bin/npm /opt/aicrm/toolchain/npm/lib/node_modules/npm/bin/npm-cli.js
   sudo -u aicrm-build -H env -i PATH=/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/npm/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin npm --version
   ```

   若官方内容核验或权限读回不符，停止；不得仅凭 `npm --version` 修复所有权。配置样例和下面的完整构建命令都固定包含该 npm 路径。

   production 安装 `deploy/domestic-promote.py` 时使用已 pinned 的 SSH key/known_hosts，从预备机复制到 ubuntu 的临时文件，先按同一源摘要比对后再由 root 原子安装；示例中的 key 与 known_hosts 路径必须对应真实固定文件：

   ```sh
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'test ! -e /home/ubuntu/domestic-promote.py.new && sudo test ! -e /usr/local/libexec/aicrm/domestic-promote.py.new && sudo install -d -o root -g root -m 0755 /usr/local/libexec/aicrm'
   scp -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     deploy/domestic-promote.py ubuntu@10.0.4.13:/home/ubuntu/domestic-promote.py.new
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo install -o root -g root -m 0755 /home/ubuntu/domestic-promote.py.new /usr/local/libexec/aicrm/domestic-promote.py.new && sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py.new'
   ```

   只有 production `.new` 摘要与 `git show "$MAIN_SHA:deploy/domestic-promote.py" | sha256sum` 完全相同才复制现有 helper 到 root-only 回滚文件并切换；存在旧回滚文件时停止，不能覆盖：

   ```sh
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo test ! -e /usr/local/libexec/aicrm/domestic-promote.py.pre-domestic-main && sudo install -o root -g root -m 0600 /usr/local/libexec/aicrm/domestic-promote.py /usr/local/libexec/aicrm/domestic-promote.py.pre-domestic-main && sudo mv /usr/local/libexec/aicrm/domestic-promote.py.new /usr/local/libexec/aicrm/domestic-promote.py && sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py'
   ```

   摘要不符时停止并从回滚文件恢复；任何 host write 都须等到旧队列按序处理到选定的 GitHub `main` SHA、旧 timer/service 已停止、最新 `main` 已普通合入 PR #46 且 app→main `runtime_changed=false`、两机 app identity 相等、源码关系验证通过且无不明发布结果。

   安装后在 stage 对 `/usr/local/libexec/aicrm/domestic_main_release.py`、`domestic_release.py`、`domestic_release_build.py`、`domestic-promote.py` 和两个 `/etc/systemd/system/aicrm-domestic-main-release.*` 文件运行 `sha256sum`；在 production 对 `/usr/local/libexec/aicrm/domestic-promote.py` 运行 `sha256sum`。逐一与上面的同一 `MAIN_SHA` 源摘要比较；只有全等才继续。用 `systemd-analyze verify` 核对落盘 unit，并确认新 timer 仍 disabled/inactive。任何摘要不符都停下，使用回滚文件恢复旧字节并复核。

4. **准备受保护配置与合成数据库。** 将脱敏样例复制到固定位置，再由 root 按真实环境填写 production SSH key 路径、持久 Host Key pin、合成数据库连接及固定工具 PATH；配置文件必须 `root:root 0600`。不得将密码、私钥或生产数据库连接放进样例、命令历史或报告。state 路径必须精确为 `/opt/aicrm/domestic/control/state.json`，控制器会拒绝其他值。核验 PostgreSQL 为本机 16，检查库为 `aicrm_ci` 或 `aicrm_test_*`，并且只含合成数据。

   ```sh
   sudo install -o root -g root -m 0600 deploy/domestic-main-release-example.json /etc/aicrm/domestic-main-release.json
   sudo stat -c '%U:%G %a %n' /etc/aicrm/domestic-main-release.json
   ```

   首次安装后用只校验配置的命令确认字段和固定路径有效；输出不得打印配置内容。样例的 `production_enabled` 先保持 `false`：

   ```sh
   sudo /usr/bin/python3 -c 'import sys; sys.path.insert(0, "/usr/local/libexec/aicrm"); import domestic_main_release as m; c=m.load_config(m.Path(m.DEFAULT_CONFIG)); print("config_valid", c["production_enabled"])'
   ```

   固定工具和配置安装完成后，从已核对 V4 seed 仓库建立裸仓。以下 `<EXACT_MAIN_SHA>` 必须是旧发布队列按序处理完成、最新 `main` 已普通合入 PR #46 后的准确 GitHub `main` SHA；初始化期间若该 `main` 前进，停止并重新核对 app→main 关系。仓库路径必须尚不存在。若路径已存在，停止并盘点，禁止覆盖：

   ```sh
   sudo test ! -e /opt/aicrm/domestic/source.git
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py \
     --config /etc/aicrm/domestic-main-release.json bootstrap \
     --seed-repo <VERIFIED_LOCAL_V4_SEED_REPOSITORY> --baseline-sha <EXACT_MAIN_SHA>
   ```

   裸仓由 root 创建，推送组只写 Git objects 和 `refs/heads/codex/*`；`main`、candidate pins、钩子、配置均不可由推送账号改写。运行 `verify_bare_repository` 前核对 `aicrm-build` 能读仓库且不属于推送组。

   5. **在 2 核、2GB 预备机运行 build-only 容量演练。** 仅在旧发布队列已按序处理到选定的 GitHub main SHA、源码 main 与两机已安装 app 的身份关系读回并验证完成、旧 timer/service disabled/inactive 且所有结果不明项已对账后进行。此前记录的完整构建用时不包含峰值内存，不能据此认定 2GB 容量稳定。该演练会使用现有 build-worker 的 Go/npm 共享缓存，因此旧队列活动期间严禁运行；不得清空或重置共享缓存。先确认 stage 当前 `/readyz` 健康、磁盘有足够空间、无运行发布任务。演练只运行 `domestic_release_build.py build`；**不要以 `poll` 作为演练命令**，因为开启生产配置后它会真实晋级生产。以下用隔离的本地 clone，`--base-release none` 明确强制完整构建，不安装、不迁移、不连接生产。为输出填写生产已安装 SHA 与当前准确 `main`：

   ```sh
   REPO=/opt/aicrm/domestic/source.git
   MAIN_SHA=<EXACT_MAIN_SHA>
   APP_SHA=<EXACT_INSTALLED_APP_SHA>
   WORK=/opt/aicrm/domestic/build-worker/benchmark-$MAIN_SHA
   sudo -u aicrm-build -H mkdir -m 0750 "$WORK"
   sudo -u aicrm-build -H git clone -q --no-checkout --local --no-hardlinks "$REPO" "$WORK/source"
   sudo -u aicrm-build -H git -C "$WORK/source" fetch -q --no-tags "$REPO" "$MAIN_SHA"
   sudo -u aicrm-build -H git -C "$WORK/source" checkout --detach "$MAIN_SHA"
   sudo -u aicrm-build -H env -i \
     PATH=/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/npm/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin \
     HOME=/opt/aicrm/domestic/build-worker \
     TMPDIR=/opt/aicrm/domestic/build-worker/tmp \
     GOCACHE=/opt/aicrm/domestic/build-worker/cache/go-build \
     GOMODCACHE=/opt/aicrm/domestic/build-worker/cache/go-mod \
     npm_config_cache=/opt/aicrm/domestic/build-worker/cache/npm \
     GOOS=linux GOARCH=amd64 GITHUB_SHA="$MAIN_SHA" \
     /usr/bin/time -v -o "$WORK/time.txt" /usr/bin/python3 "$WORK/source/scripts/domestic_release_build.py" build \
       --repo "$WORK/source" --base "$APP_SHA" --target "$MAIN_SHA" \
       --validation-scope-base "$APP_SHA" --base-release none --out "$WORK/out"
   (cd "$WORK/out/release" && sha256sum -c release-files.sha256)
   python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); assert m["source_sha"]==sys.argv[2] and m["full_build"] is True and m["build_mode"]=="full"; print(json.dumps({"source_sha":m["source_sha"],"tree":m["source_tree"],"manifest":m["release_files_sha256"],"full_build":m["full_build"],"seconds":m["phase_timings_seconds"]},sort_keys=True))' "$WORK/out/domestic-release.json" "$MAIN_SHA"
   ```

   `--base-release none` 在 builder 中强制 `full_build=true` 并运行完整 `release-fast` 构建，不依赖路径分类；`--validation-scope-base "$APP_SHA"` 只记录 app→main 的变更范围，不能缩小该完整构建。此演练只测构建/清单，不代替独立的测试合同。

   演练前后记录 UTC 起止时间、`free -h`、`vmstat 1`、`df -h /opt /var`、`/usr/bin/time -v` 最大 RSS、内核 OOM 记录和 stage 健康；`vmstat 1` 可在另一终端观察并以 Ctrl-C 结束：

   ```sh
   date -u --iso-8601=seconds
   free -h
   df -h /opt /var
   vmstat 1
   sudo journalctl -k --since '<BENCHMARK_START_UTC>' --no-pager
   ```

   不要清空共享 Go/npm cache 来制造冷启动。记录 cache 起始状态；成功后再跑一次并标为热缓存。若发生 OOM、持续 swap in/out、stage 健康下降、构建/摘要不完整、磁盘空间不足或资源表现不稳定，停止切换并扩容或保留旧流程。构建输出只作证据，不能替代完整受影响检查和已安装预发合同。

   6. **只在全部门禁过后写一次 baseline 并激活。** 先确认旧发布队列已按序安装并读回所有运行时变更；随后旧 timer/service 已停止，最新 `main` 已普通合入 PR #46 且 app→main 分类为 `runtime_changed=false`。再以旧流程收据分别读回 GitHub `main` 的准确 source SHA/tree 和 stage、production 的 app SHA/tree/manifest；两台机器的 app identity 必须相等并健康。源码 main 可晚于 app，不要求 SHA 相等。`prepare-baseline` 仅在源码 app SHA 位于 `main` 的 first-parent 链、记录的 app/main tree 匹配，且 app→main 分类结果 `runtime_changed=false` 时才继续；之后将 baseline 绑定确切 main SHA/tree，并单独记录 app identity。复制配置时确认两台计时器仍 disabled/inactive；新 timer 处于 disabled，ledger 尚不存在。再由 root 显式编辑配置，将 `production_enabled` 从 `false` 改为 `true`，保持文件 `root:root 0600`，并重新运行只输出 `config_valid`/布尔值的配置校验命令：

   ```sh
   sudoedit /etc/aicrm/domestic-main-release.json
   sudo chown root:root /etc/aicrm/domestic-main-release.json
   sudo chmod 0600 /etc/aicrm/domestic-main-release.json
   sudo /usr/bin/python3 -c 'import sys; sys.path.insert(0, "/usr/local/libexec/aicrm"); import domestic_main_release as m; c=m.load_config(m.Path(m.DEFAULT_CONFIG)); print("config_valid", c["production_enabled"])'
   ```

   只有配置校验输出 `config_valid True`、旧/新 timer 仍 disabled 且 ledger 不存在时，再依次执行以下动作：

   ```sh
   sudo systemctl is-enabled aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.service || true
   sudo systemctl is-enabled aicrm-domestic-main-release.timer || true
   sudo systemctl is-active aicrm-domestic-main-release.service || true
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json prepare-baseline
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json activate
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json verify
   ```

   `prepare-baseline`、`activate`、`verify` 读回的 source main SHA/tree 与生产 cursor 必须完全一致；stage 与 production app SHA/tree/manifest 必须彼此一致，并与独立的 installed-app 字段一致。若 app 之后只有工具/文档提交，`main_sha` 与 `installed_app_sha` 预期不同。初始队列须为空。`prepare-baseline` 会在生产保存 Git bundle 并初始化源码游标，是一次性状态变更；若响应/读回不明，不重跑，转只读对账。读回完成后最后才启用新 timer：

   ```sh
   sudo systemctl enable --now aicrm-domestic-main-release.timer
   sudo systemctl is-enabled aicrm-domestic-main-release.timer
   sudo systemctl list-timers --all aicrm-domestic-main-release.timer
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json verify
   ```

   验证旧 timer/service 始终停用，新 timer 工作正常且无双重写入口后，再按归档流程撤销旧 GitHub deploy key；此后人工同步仍只在开发者电脑显式执行。任何阶段 identity、摘要、服务或健康不匹配，保持新 timer disabled，回滚固定工具文件并继续只读核对。
