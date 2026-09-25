# CRM v4 国内主仓：一次性主机准备与切换

> **切换操作当前禁用。** 旧发布队列必须按候选顺序处理完已知运行时变化，并逐项确认已安装、健康。随后只在 stage 停止旧入口；production 的旧/新 systemd units 均未安装，须读回为 `not-found` 且无相关发布进程。以准确审核过的 PR #46 head 作为国内主仓初始 `main`，确认从 stage 与 production 已安装应用到该 head 的分类结果为 `runtime_changed=false`。stage 与 production 已安装应用的 SHA/tree/manifest 须相互一致且健康；PR #46 head 必须以该 app SHA 为 first-parent 祖先；stage 旧 timer/service 已停止，所有结果不明项已只读对账。国内 baseline 绑定准确 PR #46 head 的 SHA/tree，并独立记录 app identity。GitHub `main` 当前停在 `6d3ee9c`，用于之后人工快进归档；不需要先把 PR #46 合入 GitHub `main`。未满足这些条件时不得执行任何写命令、基线初始化、构建演练或 timer 激活。新流程的日常入口见[国内主仓发布](domestic-main-release.md)。

## 一次性主机准备与切换

以下步骤仅供一次性切换，且必须按此顺序：旧发布器处理完已知运行时变化并逐项读回安装/健康证据；stage 旧 timer/service 停用并确认 inactive，production 的旧/新 units 读回 `not-found` 且无相关 job/process；将准确审核过的 PR #46 head 定为国内 cutover SHA，确认两机已安装 app→该 head 分类为 `runtime_changed=false`。之后分别读回 PR #46 head 的 SHA/tree 和 stage、production 已安装 app SHA/tree/manifest 及健康状态，并确认两台机器的 app identity 相等。候选 head 可晚于 app，但必须以 app SHA 为 first-parent 祖先且 app→head 仅含无运行时影响的工具/文档改动。所有旧发布任务已停止且结果不明项已对账后，才由单一执行者依次操作。任何一项未满足都保持 stage 新 timer disabled，production 不安装新 timer。执行前将命令中的所有尖括号占位符替换为已核实的值，不要原样粘贴。GitHub 凭据只留在开发者电脑；预备机不保存 GitHub 凭据。本次 GitHub `main` 暂停于 `6d3ee9c`，PR #46 不需要先合入 GitHub；初始国内 `main` 从 PR #46 准确 head 的本地 bundle seed。完整步骤见本文件顶部的禁用说明。`deploy/domestic-main-release-example.json` 是脱敏模板，必须按真实合成数据库访问方式复核；初始 `production_enabled` 保持 `false`。

1. **确认旧发布器空闲，再停用 stage 旧入口。** 旧 timer/service 只配置在 stage；先检查 stage 无运行中的旧 service、job 或未知发布结果：

   ```bash
   sudo systemctl is-enabled aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.service || true
   sudo systemctl list-jobs
   if pgrep -af '/usr/local/libexec/aicrm/(domestic_release|domestic_main_release|domestic-promote)\.py'; then
     echo 'unexpected domestic release process' >&2
     exit 1
   fi
   ```

   仅确认没有任务在跑、账本没有待对账结果后，在 stage 执行停用：

   ```bash
   sudo systemctl disable --now aicrm-domestic-release.timer
   sudo systemctl stop aicrm-domestic-release.service
   sudo systemctl is-enabled aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.timer || true
   sudo systemctl is-active aicrm-domestic-release.service || true
   ```

   stage 结果须为旧 timer disabled、旧 service inactive。production 不执行 disable/stop：分别检查旧 `aicrm-domestic-release.timer/service` 和新 `aicrm-domestic-main-release.timer/service` 的 `LoadState` 均为 `not-found`，并检查 `systemctl list-jobs` 与相关进程无发布任务。stage 的新 timer 尚未安装时也应为 `not-found`；安装后必须保持 disabled/inactive，直至基线激活。

   production 没有安装旧/新 timer 或 service。分别在两台机器核对：stage 旧 timer 停用前无运行 job/process；production 的四个 unit 必须都为 `not-found` 且无相关发布进程：

   ```bash
   set -euo pipefail
   for unit in aicrm-domestic-release.timer aicrm-domestic-release.service \
     aicrm-domestic-main-release.timer aicrm-domestic-main-release.service; do
     state=$(sudo systemctl show --property=LoadState --value "$unit")
     test "$state" = not-found || { printf 'unexpected production unit: %s %s\n' "$unit" "$state" >&2; exit 1; }
   done
   sudo systemctl list-jobs --no-legend
   if pgrep -af '/usr/local/libexec/aicrm/(domestic_release|domestic_main_release|domestic-promote)\.py'; then
     echo 'unexpected domestic release process' >&2
     exit 1
   fi
   ```

2. **准备最小权限账号与目录。** 先只读核对 `/opt/aicrm` 和 `/opt/aicrm/domestic` 均为真实目录、root 拥有且不允许组或其他用户写入；核对现有子目录、账户和组，不对 `/opt/aicrm/domestic` 内容递归改权。

   在 stage 和 production 分别执行这项父目录只读预检。stage 会在 source bundle/cursor 操作前校验该条件，production helper 也会在首次写入前校验。若检查不通过先停下盘点，不递归修复，也不继续安装工具：

   ```bash
   set -euo pipefail
   sudo /usr/bin/python3 - <<'PY'
   import os, stat

   for path in ('/opt/aicrm', '/opt/aicrm/domestic'):
       try:
           info = os.lstat(path)
       except FileNotFoundError:
           if path == '/opt/aicrm/domestic':
               print('missing (may be created after /opt/aicrm passes):', path)
               continue
           raise SystemExit('required parent is missing: ' + path)
       if (not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_gid != 0
               or stat.S_IMODE(info.st_mode) & 0o022):
           raise SystemExit('unsafe domestic parent: ' + path)
       print('safe root directory:', path, oct(stat.S_IMODE(info.st_mode)))
   PY
   sudo stat -c '%U:%G %a %n' /opt/aicrm
   if sudo test -e /opt/aicrm/domestic; then
     sudo stat -c '%U:%G %a %n' /opt/aicrm/domestic
     sudo find /opt/aicrm/domestic -maxdepth 2 -mindepth 1 -printf '%y %u:%g %m %p\n'
   fi
   ```

   下列账号、受限 SSH、公钥、sudoers、`control` 与 `build-worker` 目录准备只在 stage 执行；production 不创建这些账号/组或目录，只保留父目录核验、production incoming 父目录核验与 helper 安装。只有 stage 上不存在时才创建专用组/用户，并锁定密码；若对象已存在但身份、主组或 home 不符，停止人工核对，不自动改造：

   ```bash
   set -euo pipefail
   getent group aicrm-release-push || true
   id aicrm-release-push || true
   id aicrm-build
   if ! getent group aicrm-release-push >/dev/null; then sudo groupadd --system aicrm-release-push; fi
   if ! id -u aicrm-release-push >/dev/null 2>&1; then
     sudo useradd --system --gid aicrm-release-push --create-home \
       --home-dir /var/lib/aicrm-release-push --shell /bin/sh aicrm-release-push
   fi
   sudo passwd --lock aicrm-release-push
   id aicrm-release-push
   id aicrm-build
   ```

   若 `/opt/aicrm/domestic` 缺失，经确认目标不存在后仅创建该父目录；若它或 `/opt/aicrm` 是链接、属主不为 `root:root` 或组/其他用户可写，停止并盘点，不能递归改权。已满足上述条件时保留父目录，不重复 chown/chmod。每个既有子目录都必须是预期属主、组和模式的真实目录；发现链接或不同状态就停止盘点，不对现有路径执行会改权/改模式的 `install -d`。后续只创建本流程明确列出的缺失目录：

   ```bash
   set -euo pipefail
   ensure_dir() {
     local path=$1 owner=$2 group=$3 mode=$4 actual
     if sudo test -L "$path"; then
       printf 'refusing linked directory: %s\n' "$path" >&2
       return 1
     fi
     if sudo test -e "$path"; then
       actual=$(sudo stat -c '%F %U:%G %a' -- "$path")
       test "$actual" = "directory $owner:$group $mode" || {
         printf 'unexpected existing directory: %s (%s)\n' "$path" "$actual" >&2
         return 1
       }
     else
       sudo install -d -o "$owner" -g "$group" -m "$mode" "$path"
     fi
   }
   ensure_dir /opt/aicrm/domestic root root 755
   sudo /usr/bin/python3 - <<'PY'
   import os, stat
   for path in ('/opt/aicrm', '/opt/aicrm/domestic'):
       info = os.lstat(path)
       if (not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_gid != 0
               or stat.S_IMODE(info.st_mode) & 0o022):
           raise SystemExit('unsafe domestic parent: ' + path)
   PY
   ensure_dir /opt/aicrm/domestic/control root root 755
   ensure_dir /opt/aicrm/domestic/control/work root root 755
   ensure_dir /opt/aicrm/domestic/build-worker aicrm-build aicrm-build 750
   ensure_dir /opt/aicrm/domestic/build-worker/tmp aicrm-build aicrm-build 750
   ensure_dir /opt/aicrm/domestic/build-worker/cache aicrm-build aicrm-build 750
   ensure_dir /opt/aicrm/domestic/build-worker/cache/go-build aicrm-build aicrm-build 750
   ensure_dir /opt/aicrm/domestic/build-worker/cache/go-mod aicrm-build aicrm-build 750
   ensure_dir /opt/aicrm/domestic/build-worker/cache/npm aicrm-build aicrm-build 750
   ```

   **仅 production 的旧 incoming 目录所有权修复。** production helper 将 `/opt/aicrm/domestic-incoming` 用作 root-only bundle 收件目录，要求真实 `root:root 0700` 目录。本次只读盘点读到该父目录此前为 `ubuntu:ubuntu 0700`，含 16 个旧 JSON；stage 同名目录是旧流程遗留的 `ubuntu:ubuntu 0755` intake metadata，不执行下面的 production 修复、不递归改权或清理。production 上先由 root 检查父目录为真实目录、属主仅为已核实的 `ubuntu:ubuntu` 或已修复的 `root:root`、模式精确 `0700`；直接子项必须全部为可解析 JSON 常规文件且不得有 symlink。脚本只 `chown` 父目录本身，并比较前后每个子项的名称、inode、属主、模式、大小和 SHA-256；任一预检或复核失败就停止。此次修复已执行并读回 root:root 0700，16 个子项的 inode、owner、mode、size、SHA-256 保持一致；代码块供审计复核，不应再次在已修复 production 上运行：

   ```bash
   sudo /usr/bin/python3 - <<'PY'
   import grp, hashlib, json, os, pathlib, pwd, stat

   parent = pathlib.Path('/opt/aicrm')
   info = os.lstat(parent)
   if (not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_gid != 0
           or stat.S_IMODE(info.st_mode) & 0o022):
       raise SystemExit('unsafe /opt/aicrm parent')
   incoming = parent / 'domestic-incoming'

   def snapshot():
       directory = os.lstat(incoming)
       if not stat.S_ISDIR(directory.st_mode) or stat.S_IMODE(directory.st_mode) != 0o700:
           raise SystemExit('incoming path must be a real 0700 directory')
       owner = pwd.getpwuid(directory.st_uid).pw_name
       group = grp.getgrgid(directory.st_gid).gr_name
       if (owner, group) not in {('ubuntu', 'ubuntu'), ('root', 'root')}:
           raise SystemExit('unexpected incoming directory owner')
       children = []
       for path in sorted(incoming.iterdir()):
           item = os.lstat(path)
           if path.is_symlink() or not stat.S_ISREG(item.st_mode) or path.suffix != '.json':
               raise SystemExit('incoming directory contains an unexpected or linked entry')
           content = path.read_bytes()
           try:
               json.loads(content)
           except (UnicodeDecodeError, json.JSONDecodeError) as exc:
               raise SystemExit('incoming JSON metadata is invalid') from exc
           children.append((path.name, item.st_ino, item.st_uid, item.st_gid,
                            stat.S_IMODE(item.st_mode), item.st_size,
                            hashlib.sha256(content).hexdigest()))
       if len(children) != 16:
           raise SystemExit('incoming directory must contain the 16 reviewed legacy JSON files')
       return (owner, group, stat.S_IMODE(directory.st_mode)), children

   before = snapshot()
   if before[0][:2] == ('ubuntu', 'ubuntu'):
       os.chown(incoming, 0, 0, follow_symlinks=False)
   after = snapshot()
   if after[0] != ('root', 'root', 0o700) or before[1] != after[1]:
       raise SystemExit('incoming parent repair changed metadata children or failed ownership readback')
   print(json.dumps({'directory': 'root:root 700', 'children': len(after[1]),
                     'children_unchanged': True,
                     'child_manifest_sha256': hashlib.sha256(
                         json.dumps(after[1], separators=(',', ':')).encode()).hexdigest()},
                    sort_keys=True))
   PY
   ```

   `aicrm-release-push` 是单独的系统组和锁定密码的专用账号，主组为该组；不要把 `aicrm-build` 加入推送组。仅给推送账号一把来自开发者电脑的公钥，`authorized_keys` 文件由该账号拥有、模式 `0600`，`.ssh` 目录模式 `0700`。公钥行格式如下，先替换尖括号占位符：

   ```text
   no-agent-forwarding,no-port-forwarding,no-pty,no-user-rc,no-X11-forwarding,command="/usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py restricted-ssh" ssh-ed25519 <MAC_PUBLIC_KEY_BASE64> <KEY_LABEL>
   ```

   该强制命令只接受固定裸仓的 Git fetch/push、`domestic-submit` 和格式固定的 `domestic-archive-ack --sha <40位小写 SHA>`；归档 SHA 经 stdin 交给 root controller，不会进入 sudo 命令参数。仓库钩子再限制开发者只能快进更新 `refs/heads/codex/*`。按下列命令安装已审查的公钥文件；不要在该文件中加入其他 key：

   ```bash
   if sudo test -L /var/lib/aicrm-release-push/.ssh; then
     echo 'refusing linked .ssh directory' >&2
     exit 1
   elif sudo test -e /var/lib/aicrm-release-push/.ssh; then
     test "$(sudo stat -c '%F %U:%G %a' /var/lib/aicrm-release-push/.ssh)" = \
       'directory aicrm-release-push:aicrm-release-push 700'
   else
     sudo install -d -o aicrm-release-push -g aicrm-release-push -m 0700 /var/lib/aicrm-release-push/.ssh
   fi
   sudo test ! -e /var/lib/aicrm-release-push/.ssh/authorized_keys
   sudo test ! -L /var/lib/aicrm-release-push/.ssh/authorized_keys
   sudo install -o aicrm-release-push -g aicrm-release-push -m 0600 <REVIEWED_AUTHORIZED_KEYS_FILE> /var/lib/aicrm-release-push/.ssh/authorized_keys
   ```

   stage 的 `/usr/bin/sudo` 是 sudo-rs。使用 sudo-rs 支持的可执行文件和参数精确匹配：归档 SHA 不进入 argv，root endpoint 只接受最多 512 字节、只含一个 `sha` 字段且没有重复 JSON 键的 stdin 请求。sudoers 中只授权下面两条固定命令；不要加参数正则、通配符或命令别名续行。先确认正式规则不存在；将下列文件安装为 `root:root 0440`，运行 `visudo -cf` 检查临时文件和完整 sudoers 配置；两次检查都通过后才原子改名，再用 `sudo -l -U aicrm-release-push` 读回，确认只列出这两条固定命令。失败就删除临时文件并停止，不安装规则：

   ```bash
   # Run on stage only.
   set -euo pipefail
   sudo test ! -e /etc/sudoers.d/aicrm-domestic-release
   sudo test ! -L /etc/sudoers.d/aicrm-domestic-release
   sudo test ! -e /etc/sudoers.d/aicrm-domestic-release.new
   sudo test ! -L /etc/sudoers.d/aicrm-domestic-release.new
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

**先从 Mac 准备并验证准确 PR #46 head 的 seed bundle。** 旧 stage `/opt/aicrm/source` 不含 PR #46 head 且含旧 GitHub remote 配置，不能用作安装工具或 `--seed-repo` 来源。GitHub `main` 保持在 `6d3ee9c`，国内主仓从 PR #46 最终准确 head 初始化。bundle 只经现有运维 SSH（`/Users/qianlan/Downloads/zhengshi.pem`，通过 production `124.220.53.183` 跳板到 stage `10.0.4.6`）传到 `/var/tmp`；先核对 HostKey pin 与该 key `0600`。此身份只用于受控文件传输，不提供 GitHub 凭据，也不使用受限 archive-ack key。

   在 Mac 开发机的 clean 工作树上执行，`DOMESTIC_MAIN_SHA` 是 PR #46 最终准确 head，`APP_SHA` 是已安装应用 SHA，`DOMESTIC_MAIN_TREE` 是已审查的 head tree。临时 ref 不覆盖现有 branch，bundle 只包含该 commit 的完整可达历史：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   DOMESTIC_MAIN_TREE=<EXACT_PR46_HEAD_TREE>
   APP_SHA=<EXACT_INSTALLED_APP_SHA>
   SEED_REF="refs/domestic-main-seed/$DOMESTIC_MAIN_SHA"
   BUNDLE="${TMPDIR:-/tmp}/domestic-main-seed-$DOMESTIC_MAIN_SHA.bundle"
   test ! -e "$BUNDLE"
   test ! -L "$BUNDLE"
   test "$(git rev-parse HEAD)" = "$DOMESTIC_MAIN_SHA"
   test -z "$(git status --porcelain)"
   test "$(git rev-parse "$DOMESTIC_MAIN_SHA^{tree}")" = "$DOMESTIC_MAIN_TREE"
   git cat-file -e "$DOMESTIC_MAIN_SHA^{commit}"
   git merge-base --is-ancestor "$APP_SHA" "$DOMESTIC_MAIN_SHA"
   git rev-list --first-parent "$DOMESTIC_MAIN_SHA" | grep -Fx "$APP_SHA"
   if git show-ref --verify --quiet "$SEED_REF"; then
     echo 'seed ref already exists' >&2
     exit 1
   fi
   git update-ref "$SEED_REF" "$DOMESTIC_MAIN_SHA"
   git bundle create "$BUNDLE" "$SEED_REF"
   VERIFY=$(git bundle verify "$BUNDLE")
   printf '%s\n' "$VERIFY"
   grep -F 'The bundle records a complete history.' <<<"$VERIFY"
   git bundle list-heads "$BUNDLE" | grep -Fx "$DOMESTIC_MAIN_SHA $SEED_REF"
   git update-ref -d "$SEED_REF"
   sha256sum "$BUNDLE"
   ```

   使用当前已审查的运维 SSH 身份从 Mac 上传 bundle；示例固定生产跳板和 stage 内网地址，先用 `ssh -G` 与 `known_hosts` 读回路径。不得使用 GitHub key 或 `aicrm-release-push` 的强制 ACK key。将 Mac 输出的 bundle 摘要填入 `<BUNDLE_SHA256>`。stage 以下命令从空临时 bare seed 读回并核验准确对象：

   ```bash
   scp -i /Users/qianlan/Downloads/zhengshi.pem \
     -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes \
     -o UserKnownHostsFile=/Users/qianlan/.ssh/known_hosts \
     -o ProxyJump=ubuntu@124.220.53.183 \
     "$BUNDLE" "ubuntu@10.0.4.6:/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.bundle"
   ```

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   DOMESTIC_MAIN_TREE=<EXACT_PR46_HEAD_TREE>
   APP_SHA=<EXACT_INSTALLED_APP_SHA>
   BUNDLE_SHA256=<BUNDLE_SHA256>
   BUNDLE="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.bundle"
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   SEED_REF="refs/domestic-main-seed/$DOMESTIC_MAIN_SHA"
   sudo test -f "$BUNDLE"
   sudo test ! -L "$BUNDLE"
   test "$(sudo sha256sum "$BUNDLE" | awk '{print $1}')" = "$BUNDLE_SHA256"
   sudo test ! -e "$SEED_REPO"
   sudo test ! -L "$SEED_REPO"
   sudo install -d -o root -g root -m 0700 "$SEED_REPO"
   sudo /usr/bin/git -C "$SEED_REPO" init --bare --quiet
   VERIFY=$(sudo /usr/bin/git -C "$SEED_REPO" bundle verify "$BUNDLE")
   printf '%s\n' "$VERIFY"
   grep -F 'The bundle records a complete history.' <<<"$VERIFY"
   sudo /usr/bin/git bundle list-heads "$BUNDLE" | grep -Fx "$DOMESTIC_MAIN_SHA $SEED_REF"
   sudo /usr/bin/git -C "$SEED_REPO" fetch --no-tags "$BUNDLE" "$SEED_REF:refs/heads/main"
   sudo /usr/bin/git -C "$SEED_REPO" fsck --full --strict --no-reflogs
   test "$(sudo /usr/bin/git -C "$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   test "$(sudo /usr/bin/git -C "$SEED_REPO" rev-parse 'refs/heads/main^{tree}')" = "$DOMESTIC_MAIN_TREE"
   sudo /usr/bin/git -C "$SEED_REPO" merge-base --is-ancestor "$APP_SHA" "$DOMESTIC_MAIN_SHA"
   sudo /usr/bin/git -C "$SEED_REPO" rev-list --first-parent "$DOMESTIC_MAIN_SHA" | grep -Fx "$APP_SHA"
   ```

   从同一 verified seed bare repo 导出固定工具与脱敏配置到 root-owned 私有临时目录；不访问旧 clone。只有每个导出文件都与 bare repo 中该准确 commit 的 blob SHA-256 相等后，才继续安装：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   sudo test ! -e "$SOURCE_WORK"
   sudo test ! -L "$SOURCE_WORK"
   sudo install -d -o root -g root -m 0700 "$SOURCE_WORK"
   sudo /usr/bin/git --git-dir="$SEED_REPO" archive --format=tar "$DOMESTIC_MAIN_SHA" \
     scripts/domestic_main_release.py scripts/domestic_release.py scripts/domestic_release_build.py \
     deploy/domestic-promote.py deploy/aicrm-domestic-main-release.service \
     deploy/aicrm-domestic-main-release.timer deploy/domestic-main-release-example.json | \
     sudo /usr/bin/tar -xf - -C "$SOURCE_WORK"
   for path in scripts/domestic_main_release.py scripts/domestic_release.py \
     scripts/domestic_release_build.py deploy/domestic-promote.py \
     deploy/aicrm-domestic-main-release.service deploy/aicrm-domestic-main-release.timer \
     deploy/domestic-main-release-example.json; do
     expected=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$DOMESTIC_MAIN_SHA:$path" | sha256sum | awk '{print $1}')
     actual=$(sudo sha256sum "$SOURCE_WORK/$path" | awk '{print $1}')
     test "$actual" = "$expected" || { printf 'seed extract hash mismatch: %s\n' "$path" >&2; exit 1; }
   done
   ```

3. **安装并核验固定工具字节。** 在准确 PR #46 head `<EXACT_PR46_HEAD_SHA>` 的 clean V4 工作树先记录每个文件的源 SHA-256。该 head 会成为国内 cutover `main`，不要求先进入 GitHub `main`。随后在 stage 使用前一步从已核验 seed 导出的 root-only `SOURCE_WORK` 安装 `scripts/domestic_main_release.py`、`scripts/domestic_release.py`、`scripts/domestic_release_build.py`、`deploy/domestic-promote.py` 和两个 unit；production 只接收 stage 导出、且与同一 seed blob 摘要相等的 `deploy/domestic-promote.py`。安装前把现有同路径 regular file 复制到 root-only 回滚副本；用同目录 `.new` 文件安装、比对摘要后再原子改名。禁止在服务运行中替换 helper。

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   test "$(git rev-parse HEAD)" = "$DOMESTIC_MAIN_SHA"
   test -z "$(git status --porcelain)"
   for path in scripts/domestic_main_release.py scripts/domestic_release.py \
     scripts/domestic_release_build.py deploy/domestic-promote.py \
     deploy/aicrm-domestic-main-release.service deploy/aicrm-domestic-main-release.timer; do
     digest=$(git show "$DOMESTIC_MAIN_SHA:$path" | sha256sum | cut -d ' ' -f 1)
     printf '%s  %s\n' "$digest" "$path"
   done
   ```

   stage 工具先备份再安装为同目录 `.new`，比较文件摘要完全相同后才 `mv` 到正式路径：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   test -d "$SOURCE_WORK" && sudo test ! -L "$SOURCE_WORK"
   for directory in /usr/local/libexec /usr/local/libexec/aicrm; do
     if sudo test -L "$directory"; then
       printf 'refusing linked helper directory: %s\n' "$directory" >&2
       exit 1
     elif sudo test -e "$directory"; then
       test "$(sudo stat -c '%F %U:%G %a' -- "$directory")" = 'directory root:root 755' || {
         printf 'unexpected helper directory: %s\n' "$directory" >&2
         exit 1
       }
     else
       sudo install -d -o root -g root -m 0755 "$directory"
     fi
   done
   for path in /usr/local/libexec/aicrm/domestic_main_release.py \
     /usr/local/libexec/aicrm/domestic_release.py \
     /usr/local/libexec/aicrm/domestic_release_build.py \
     /usr/local/libexec/aicrm/domestic-promote.py \
     /etc/systemd/system/aicrm-domestic-main-release.service \
     /etc/systemd/system/aicrm-domestic-main-release.timer; do
     if sudo test -L "$path"; then
       printf 'refusing linked existing release file: %s\n' "$path" >&2
       exit 1
     elif sudo test -e "$path"; then
       sudo test -f "$path"
       test "$(sudo stat -c '%u:%g' -- "$path")" = '0:0'
       mode=$(sudo stat -c '%a' -- "$path")
       (( (8#$mode & 18) == 0 )) || { printf 'writable release file: %s\n' "$path" >&2; exit 1; }
       sudo test ! -e "$path.pre-domestic-main"
       sudo test ! -L "$path.pre-domestic-main"
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
     sudo test ! -L "$path"
   done
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/scripts/domestic_main_release.py" /usr/local/libexec/aicrm/domestic_main_release.py.new
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/scripts/domestic_release.py" /usr/local/libexec/aicrm/domestic_release.py.new
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/scripts/domestic_release_build.py" /usr/local/libexec/aicrm/domestic_release_build.py.new
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/deploy/domestic-promote.py" /usr/local/libexec/aicrm/domestic-promote.py.new
   sudo install -o root -g root -m 0644 "$SOURCE_WORK/deploy/aicrm-domestic-main-release.service" /etc/systemd/system/aicrm-domestic-main-release.service.new
   sudo install -o root -g root -m 0644 "$SOURCE_WORK/deploy/aicrm-domestic-main-release.timer" /etc/systemd/system/aicrm-domestic-main-release.timer.new
   ```

   在替换正式文件前，对全部 `.new` 文件逐个比较准确 `DOMESTIC_MAIN_SHA` 的 Git blob 摘要；任一不符即停止，不能执行后续 `mv`：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   while IFS=' ' read -r source target; do
     expected=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$DOMESTIC_MAIN_SHA:$source" | sha256sum | awk '{print $1}')
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

   ```bash
   set -euo pipefail
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

   **预备机 npm 工具树的固定权限。** isolated `build_path` 必须包含 `/opt/aicrm/toolchain/npm/bin`，并由 `_verify_build_toolchain` 检查实际解析到的工具和父链；不得删除或放宽这项所有权检查。本次 stage 核查最初读到 npm 入口为 `ubuntu:ubuntu 775`；在官方包字节、固定 manifest 和 symlink 全部校验后，已将 npm 树设为 `root:root` 并去掉目录/文件的组、其他写权限，隔离构建用户读回 `npm --version` 为 `11.12.1`。变更权限前，从官方 `npm@11.12.1` tarball 独立复核已安装内容：tarball 地址为 `https://registry.npmjs.org/npm/-/npm-11.12.1.tgz`，固定 SRI 为 `sha512-zcoUuF1kezGSAo0CqtvoLXX3mkRqzuqYdL6Y5tdo8g69NVV3CkjQ6ZBhBgB4d7vGkPcV6TcvLi3GRKPDFX+xTA==`。在 stage 以 root 运行以下只读脚本；它先校验官方 tarball 的 SHA-512 SRI，再按每个 `package/` 常规文件的相对路径和 SHA-256 对比已安装树，并检查全部 symlink 都指向固定 npm 树内的 regular file。另对 npm 包的 `node_modules/.bin` 单独要求恰有 9 条 symlink，且每个目标都是 npm 包内的 regular file。本次整树读回有 100 条 symlink；这个观察值不固定，也不是门禁。任一摘要、1774 个常规文件的路径集合、固定 manifest 或链接条件不符就停止，不能 chown：

   ```bash
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
   for link in links:
       target = link.resolve(strict=True)
       try:
           target.relative_to(root_real)
       except ValueError:
           raise SystemExit('npm symlink escapes its fixed tool tree')
       if not target.is_file():
           raise SystemExit('npm symlink target is not a regular file')

   bin_dir = package / 'node_modules/.bin'
   if bin_dir.is_symlink() or not bin_dir.is_dir():
       raise SystemExit('npm package .bin directory is missing or linked')
   bin_links = list(bin_dir.iterdir())
   if len(bin_links) != 9 or any(not link.is_symlink() for link in bin_links):
       raise SystemExit('npm package .bin must contain exactly 9 symlinks')
   package_real = package.resolve(strict=True)
   for link in bin_links:
       target = link.resolve(strict=True)
       try:
           target.relative_to(package_real)
       except ValueError:
           raise SystemExit('npm package .bin symlink escapes its package tree')
       if not target.is_file():
           raise SystemExit('npm package .bin symlink target is not a regular file')
   print(json.dumps({'npm_version': '11.12.1', 'regular_files': len(actual),
                     'manifest_sha256': manifest_sha, 'tree_symlinks': len(links),
                     'npm_package_bin_symlinks': len(bin_links)}, sort_keys=True))
   PY
   ```

   只读核验通过后，才把**固定 npm 工具树**设为 root 所有并去掉组/其他用户写权限；这个变更仅限该 npm 树，不涉及 `/opt/aicrm/domestic` 或共享 cache。随后用隔离账号读回版本，控制器完整 toolchain probe 仍须通过：

   ```bash
   sudo chown -hR root:root /opt/aicrm/toolchain/npm
   sudo find -P /opt/aicrm/toolchain/npm -type d -exec chmod go-w {} +
   sudo find -P /opt/aicrm/toolchain/npm -type f -exec chmod go-w {} +
   sudo stat -c '%U:%G %a %n' /opt/aicrm/toolchain/npm /opt/aicrm/toolchain/npm/bin /opt/aicrm/toolchain/npm/bin/npm /opt/aicrm/toolchain/npm/lib/node_modules/npm/bin/npm-cli.js
   sudo -u aicrm-build -H env -i PATH=/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/npm/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin npm --version
   ```

   若官方内容核验或权限读回不符，停止；不得仅凭 `npm --version` 修复所有权。配置样例和下面的完整构建命令都固定包含该 npm 路径。

   production 安装 `deploy/domestic-promote.py` 时，先在 stage 将 `SOURCE_WORK` 中已经与 verified seed blob SHA-256 相等的文件复制到 ubuntu 可读的唯一临时文件，再用已 pinned 的 SSH identity 和 known_hosts 传入 production 的唯一 `.new` 路径。执行前只读核对 `/usr/local/libexec` 为真实 `root:root 755` 目录；若 `/usr/local/libexec/aicrm` 已存在，也必须是同样安全的真实目录；只有末级目录缺失时才创建，不对已有目录重复 `install -d` 改模式。示例中的 key 与 known_hosts 路径必须对应真实固定文件：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   UPLOAD=/home/ubuntu/domestic-promote.py.upload
   sudo test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   test ! -e "$UPLOAD" && test ! -L "$UPLOAD"
   sudo install -o ubuntu -g ubuntu -m 0644 "$SOURCE_WORK/deploy/domestic-promote.py" "$UPLOAD"
   EXPECTED=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$DOMESTIC_MAIN_SHA:deploy/domestic-promote.py" | sha256sum | awk '{print $1}')
   test "$(sudo sha256sum "$UPLOAD" | awk '{print $1}')" = "$EXPECTED"
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'set -e; test ! -e /home/ubuntu/domestic-promote.py.new; test ! -L /home/ubuntu/domestic-promote.py.new; sudo test -d /usr/local/libexec; sudo test ! -L /usr/local/libexec; test "$(sudo stat -c "%F %U:%G %a" /usr/local/libexec)" = "directory root:root 755"; if sudo test -L /usr/local/libexec/aicrm; then exit 1; elif sudo test -e /usr/local/libexec/aicrm; then test "$(sudo stat -c "%F %U:%G %a" /usr/local/libexec/aicrm)" = "directory root:root 755"; else sudo install -d -o root -g root -m 0755 /usr/local/libexec/aicrm; fi; sudo test ! -e /usr/local/libexec/aicrm/domestic-promote.py.new; sudo test ! -L /usr/local/libexec/aicrm/domestic-promote.py.new'
   scp -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     "$UPLOAD" ubuntu@10.0.4.13:/home/ubuntu/domestic-promote.py.new
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'set -e; test -f /home/ubuntu/domestic-promote.py.new; test ! -L /home/ubuntu/domestic-promote.py.new; sudo test ! -e /usr/local/libexec/aicrm/domestic-promote.py.new; sudo test ! -L /usr/local/libexec/aicrm/domestic-promote.py.new; sudo install -o root -g root -m 0755 /home/ubuntu/domestic-promote.py.new /usr/local/libexec/aicrm/domestic-promote.py.new; sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py.new'
   REMOTE_SHA=$(ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py.new' | awk '{print $1}')
   test "$REMOTE_SHA" = "$EXPECTED"
   ```

   只有 production `.new` 摘要与 stage verified seed 中准确 commit 的 `deploy/domestic-promote.py` blob SHA-256 完全相同，才复制现有 helper 到 root-only 回滚文件并切换；存在旧回滚文件时停止，不能覆盖：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   EXPECTED=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$DOMESTIC_MAIN_SHA:deploy/domestic-promote.py" | sha256sum | awk '{print $1}')
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   REMOTE_NEW_SHA=$(ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py.new' | awk '{print $1}')
   test "$REMOTE_NEW_SHA" = "$EXPECTED"
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'set -e; sudo test ! -L /usr/local/libexec/aicrm/domestic-promote.py; sudo test -f /usr/local/libexec/aicrm/domestic-promote.py; test "$(sudo stat -c "%u:%g" /usr/local/libexec/aicrm/domestic-promote.py)" = "0:0"; sudo test ! -e /usr/local/libexec/aicrm/domestic-promote.py.pre-domestic-main; sudo test ! -L /usr/local/libexec/aicrm/domestic-promote.py.pre-domestic-main; sudo test -e /usr/local/libexec/aicrm/domestic-promote.py.new; sudo test ! -L /usr/local/libexec/aicrm/domestic-promote.py.new; sudo install -o root -g root -m 0600 /usr/local/libexec/aicrm/domestic-promote.py /usr/local/libexec/aicrm/domestic-promote.py.pre-domestic-main; sudo mv /usr/local/libexec/aicrm/domestic-promote.py.new /usr/local/libexec/aicrm/domestic-promote.py; sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py'
   REMOTE_SHA=$(ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py' | awk '{print $1}')
   test "$REMOTE_SHA" = "$EXPECTED"
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'test -f /home/ubuntu/domestic-promote.py.new; test ! -L /home/ubuntu/domestic-promote.py.new; rm -- /home/ubuntu/domestic-promote.py.new'
   ```

   摘要不符时停止并从回滚文件恢复；任何 host write 都须等到旧队列已按序处理完已知运行时变化、stage 旧 timer/service 已停止、production 旧/新 unit 不存在、准确 PR #46 head 的 app→head 分类为 `runtime_changed=false`、两机 app identity 相等、源码 first-parent 关系验证通过且无不明发布结果。

   安装后在 stage 对 `/usr/local/libexec/aicrm/domestic_main_release.py`、`domestic_release.py`、`domestic_release_build.py`、`domestic-promote.py` 和两个 `/etc/systemd/system/aicrm-domestic-main-release.*` 文件运行 `sha256sum`；在 production 对 `/usr/local/libexec/aicrm/domestic-promote.py` 运行 `sha256sum`。逐一与上面的同一 `DOMESTIC_MAIN_SHA` 源摘要比较；只有全等才继续。用 `systemd-analyze verify` 核对落盘 unit，并确认新 timer 仍 disabled/inactive。任何摘要不符都停下，使用回滚文件恢复旧字节并复核。

4. **准备受保护配置与合成数据库。** 将脱敏样例复制到固定位置，再由 root 按真实环境填写 production SSH key 路径、持久 Host Key pin、合成数据库连接及固定工具 PATH；配置文件必须 `root:root 0600`。不得将密码、私钥或生产数据库连接放进样例、命令历史或报告。state 路径必须精确为 `/opt/aicrm/domestic/control/state.json`，控制器会拒绝其他值。核验 PostgreSQL 为本机 16，检查库为 `aicrm_ci` 或 `aicrm_test_*`，并且只含合成数据。

   ```bash
   SOURCE_WORK="/var/tmp/domestic-main-source-<EXACT_PR46_HEAD_SHA>"
   sudo test ! -e /etc/aicrm/domestic-main-release.json
   sudo test ! -L /etc/aicrm/domestic-main-release.json
   sudo install -o root -g root -m 0600 "$SOURCE_WORK/deploy/domestic-main-release-example.json" /etc/aicrm/domestic-main-release.json
   sudo stat -c '%U:%G %a %n' /etc/aicrm/domestic-main-release.json
   ```

   首次安装后用只校验配置的命令确认字段和固定路径有效；输出不得打印配置内容。样例的 `production_enabled` 先保持 `false`：

   ```bash
   sudo /usr/bin/python3 -c 'import sys; sys.path.insert(0, "/usr/local/libexec/aicrm"); import domestic_main_release as m; c=m.load_config(m.Path(m.DEFAULT_CONFIG)); print("config_valid", c["production_enabled"])'
   ```

   固定工具和配置安装完成后，以准确审核过的 PR #46 head 建立国内裸仓。GitHub `main` 仍为人工归档线，当前停在 `6d3ee9c`；不等待 PR #46 合并。`DOMESTIC_MAIN_SHA` 必须等于最终通过准确 head CI 的 PR #46 head，且以生产已安装 `APP_SHA` 为 first-parent 祖先、`classify --base "$APP_SHA" --target "$DOMESTIC_MAIN_SHA"` 返回 `runtime_changed=false`。从 Mac 上含该准确对象的 V4 仓库生成自包含 bundle；不要使用 stage 旧 `/opt/aicrm/source` GitHub clone（它可能缺该对象且带旧凭据配置）。前面的 verified seed bare repo 既用于导出和安装固定工具，也作为 `--seed-repo`；不要重新从旧 clone seed。bundle 不包含 GitHub 凭据，stage 不配置 GitHub remote 或凭据。初始化前核对 bundle 的 SHA-256、Git ref、main/tree、app first-parent 祖先和完整对象库；任一不符就停止。仓库路径必须尚不存在。若路径已存在，停止并盘点，禁止覆盖：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   DOMESTIC_MAIN_TREE=<EXACT_PR46_HEAD_TREE>
   APP_SHA=<EXACT_INSTALLED_APP_SHA>
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   BUNDLE="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.bundle"
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   sudo test ! -e /opt/aicrm/domestic/source.git
   sudo test ! -L /opt/aicrm/domestic/source.git
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse 'refs/heads/main^{tree}')" = "$DOMESTIC_MAIN_TREE"
   sudo /usr/bin/git --git-dir="$SEED_REPO" merge-base --is-ancestor "$APP_SHA" "$DOMESTIC_MAIN_SHA"
   sudo /usr/bin/git --git-dir="$SEED_REPO" rev-list --first-parent "$DOMESTIC_MAIN_SHA" | grep -Fx "$APP_SHA"
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py \
     --config /etc/aicrm/domestic-main-release.json bootstrap \
     --seed-repo "$SEED_REPO" --baseline-sha "$DOMESTIC_MAIN_SHA"
   ```

   bootstrap 成功并读回 `/opt/aicrm/domestic/source.git` 的 main SHA/tree 后，才删除本次准确 SHA 命名的 stage 临时 bundle、seed bare repo、导出目录和 helper 上传副本；若 bootstrap 结果不明，保留证据并只读对账，不重跑。删除前确认每个路径均不为 symlink，且名称中的 SHA 等于已核验的 `DOMESTIC_MAIN_SHA`。

   ```bash
   set -euo pipefail
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   for path in "$BUNDLE" "$SEED_REPO" "$SOURCE_WORK" /home/ubuntu/domestic-promote.py.upload; do
     test ! -L "$path"
   done
   sudo rm -f -- "$BUNDLE" /home/ubuntu/domestic-promote.py.upload
   sudo rm -rf -- "$SEED_REPO" "$SOURCE_WORK"
   ```

   裸仓由 root 创建，推送组只写 Git objects 和 `refs/heads/codex/*`；`main`、candidate pins、钩子、配置均不可由推送账号改写。运行 `verify_bare_repository` 前核对 `aicrm-build` 能读仓库且不属于推送组。

   5. **在 2 核、2GB 预备机运行 build-only 容量演练。** 仅在旧发布队列已按序处理完已知运行时变化、准确 PR #46 head 与两机已安装 app 的身份关系读回并验证完成、stage 旧 timer/service 已停止且 production units 为 `not-found`、所有结果不明项已对账后进行。此前记录的完整构建用时不包含峰值内存，不能据此认定 2GB 容量稳定。该演练会使用现有 build-worker 的 Go/npm 共享缓存，因此旧队列活动期间严禁运行；不得清空或重置共享缓存。先确认 stage 当前 `/readyz` 健康、磁盘有足够空间、无运行发布任务。演练只运行 `domestic_release_build.py build`；**不要以 `poll` 作为演练命令**，因为开启生产配置后它会真实晋级生产。以下用隔离的本地 clone，`--base-release none` 明确强制完整构建，不安装、不迁移、不连接生产。为输出填写生产已安装 SHA 与当前准确 PR #46 head。冷缓存与热缓存各用不同 `RUN_KIND` 和新的 UTC `RUN_ID`，每次得到全新 workspace；目录已存在或为 symlink 时停止，不覆盖旧结果：

   ```bash
   REPO=/opt/aicrm/domestic/source.git
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   APP_SHA=<EXACT_INSTALLED_APP_SHA>
   RUN_KIND=cold # warm pass: set to warm and rerun the complete block
   case "$RUN_KIND" in cold|warm) ;; *) echo 'RUN_KIND must be cold or warm' >&2; exit 1 ;; esac
   RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
   WORK=/opt/aicrm/domestic/build-worker/benchmark-$DOMESTIC_MAIN_SHA-$RUN_KIND-$RUN_ID
   test -x /usr/bin/time && test -x /usr/bin/vmstat && test -x /usr/bin/free
   sudo -u aicrm-build -H test ! -e "$WORK"
   sudo -u aicrm-build -H test ! -L "$WORK"
   sudo -u aicrm-build -H mkdir -m 0750 "$WORK"
   sudo -u aicrm-build -H git clone -q --no-checkout --local --no-hardlinks "$REPO" "$WORK/source"
   sudo -u aicrm-build -H git -C "$WORK/source" fetch -q --no-tags "$REPO" "$DOMESTIC_MAIN_SHA"
   sudo -u aicrm-build -H git -C "$WORK/source" checkout --detach "$DOMESTIC_MAIN_SHA"
   sudo -u aicrm-build -H env -i \
     PATH=/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/npm/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin \
     HOME=/opt/aicrm/domestic/build-worker \
     TMPDIR=/opt/aicrm/domestic/build-worker/tmp \
     GOCACHE=/opt/aicrm/domestic/build-worker/cache/go-build \
     GOMODCACHE=/opt/aicrm/domestic/build-worker/cache/go-mod \
     npm_config_cache=/opt/aicrm/domestic/build-worker/cache/npm \
     GOOS=linux GOARCH=amd64 GITHUB_SHA="$DOMESTIC_MAIN_SHA" \
     /usr/bin/time -v -o "$WORK/time.txt" /usr/bin/python3 "$WORK/source/scripts/domestic_release_build.py" build \
       --repo "$WORK/source" --base "$APP_SHA" --target "$DOMESTIC_MAIN_SHA" \
       --validation-scope-base "$APP_SHA" --base-release none --out "$WORK/out"
   sudo -u aicrm-build -H /usr/bin/bash -c 'cd "$1" && sha256sum -c release-files.sha256' _ "$WORK/out/release"
   sudo -u aicrm-build -H /usr/bin/python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); assert m["source_sha"]==sys.argv[2] and m["full_build"] is True and m["build_mode"]=="full"; print(json.dumps({"source_sha":m["source_sha"],"tree":m["source_tree"],"manifest":m["release_files_sha256"],"full_build":m["full_build"],"seconds":m["phase_timings_seconds"]},sort_keys=True))' "$WORK/out/domestic-release.json" "$DOMESTIC_MAIN_SHA"
   ```

   `--base-release none` 在 builder 中强制 `full_build=true` 并运行完整 `release-fast` 构建，不依赖路径分类；`--validation-scope-base "$APP_SHA"` 只记录 app→main 的变更范围，不能缩小该完整构建。此演练只测构建/清单，不代替独立的测试合同。

   演练前后记录 UTC 起止时间、`free -h`、`vmstat 1`、`df -h /opt /var`、`/usr/bin/time -v` 最大 RSS、内核 OOM 记录和 stage 健康；`vmstat 1` 可在另一终端观察并以 Ctrl-C 结束：

   ```bash
   date -u --iso-8601=seconds
   free -h
   df -h /opt /var
   vmstat 1
   sudo journalctl -k --since '<BENCHMARK_START_UTC>' --no-pager
   ```

   不要清空共享 Go/npm cache 来制造冷启动。记录 cache 起始状态；冷构建成功后以 `RUN_KIND=warm` 和新的 `RUN_ID` 完整重跑，保留两组输出与计时。若发生 OOM、持续 swap in/out、stage 健康下降、构建/摘要不完整、磁盘空间不足或资源表现不稳定，停止切换并扩容或保留旧流程。构建输出只作证据，不能替代完整受影响检查和已安装预发合同。

   6. **只在全部门禁过后写一次 baseline 并激活。** 先确认旧发布队列已按序安装并读回所有运行时变更；stage 旧 timer/service 已停用，production 的旧/新 units 均为 `not-found` 且无发布 job/process。以准确 PR #46 head 核对两机已安装 app SHA/tree/manifest 与健康状态相同，并再次确认 app→head 分类为 `runtime_changed=false`、app SHA 位于该 head 的 first-parent 链。国内 baseline 绑定 PR #46 head 的准确 SHA/tree，并独立记录 app identity。`prepare-baseline`、`activate`、`verify` 以及新 timer 都只在 stage 执行；production 不安装或启动这些 units。复制配置时确认 stage 旧/新 timer disabled/inactive、production units 仍为 `not-found`，ledger 尚不存在。再由 root 显式编辑 stage 配置，将 `production_enabled` 从 `false` 改为 `true`，保持文件 `root:root 0600`，并重新运行只输出 `config_valid`/布尔值的配置校验命令：

   ```bash
   sudoedit /etc/aicrm/domestic-main-release.json
   sudo chown root:root /etc/aicrm/domestic-main-release.json
   sudo chmod 0600 /etc/aicrm/domestic-main-release.json
   sudo /usr/bin/python3 -c 'import sys; sys.path.insert(0, "/usr/local/libexec/aicrm"); import domestic_main_release as m; c=m.load_config(m.Path(m.DEFAULT_CONFIG)); print("config_valid", c["production_enabled"])'
   ```

   只有配置校验输出 `config_valid True`、stage 旧/新 timer 仍 disabled、旧/新 service inactive，production 旧/新 units 仍 `not-found` 且 ledger 不存在时，再在 stage 执行以下动作：

   ```bash
   set -euo pipefail
   test "$(sudo systemctl is-enabled aicrm-domestic-release.timer 2>/dev/null || true)" = disabled
   test "$(sudo systemctl is-active aicrm-domestic-release.service 2>/dev/null || true)" = inactive
   test "$(sudo systemctl is-enabled aicrm-domestic-main-release.timer 2>/dev/null || true)" = disabled
   test "$(sudo systemctl is-active aicrm-domestic-main-release.service 2>/dev/null || true)" = inactive
   for unit in aicrm-domestic-release.timer aicrm-domestic-release.service \
     aicrm-domestic-main-release.timer aicrm-domestic-main-release.service; do
     state=$(ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
       -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
       ubuntu@10.0.4.13 "sudo systemctl show --property=LoadState --value $unit")
     test "$state" = not-found
   done
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json prepare-baseline
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json activate
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json verify
   ```

   `prepare-baseline`、`activate`、`verify` 读回的 source main SHA/tree 与生产 cursor 必须完全一致；stage 与 production app SHA/tree/manifest 必须彼此一致，并与独立的 installed-app 字段一致。若 app 之后只有工具/文档提交，`main_sha` 与 `installed_app_sha` 预期不同。初始队列须为空。`prepare-baseline` 会在生产保存 Git bundle 并初始化源码游标，是一次性状态变更；若响应/读回不明，不重跑，转只读对账。读回完成后最后才启用新 timer：

   ```bash
   # Run only on stage; production has no domestic-main-release unit.
   sudo systemctl enable --now aicrm-domestic-main-release.timer
   sudo systemctl is-enabled aicrm-domestic-main-release.timer
   sudo systemctl list-timers --all aicrm-domestic-main-release.timer
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json verify
   ```

   验证旧 timer/service 始终停用，新 timer 工作正常且无双重写入口后，再按归档流程撤销旧 GitHub deploy key；此后人工同步仍只在开发者电脑显式执行。任何阶段 identity、摘要、服务或健康不匹配，保持新 timer disabled，回滚固定工具文件并继续只读核对。
