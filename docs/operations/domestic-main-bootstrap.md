# CRM v4 国内主仓：一次性主机准备与切换

> **切换操作当前禁用。** 本次只允许恢复已核实的 partial bare repo：main=`291baa2d13864c3a60f3ed93e08382c3e598db33`、tree=`3c17b8a86e2e69ed4f6942304300c609300fb077`，installed app=`960b30e9406fae2045aeb7ef5dce863976407727`。若现场 SHA/tree 不符就停下，不 bootstrap、不重建、不删除仓库或 ledger。恢复只补 hook 和仓库安全权限，保持 main=291；initial baseline 也是 291。准确 PR #46 head 的本地 bundle 只作为经 hash 验证的工具/候选来源，baseline 后作为普通 controller-only 首个候选。`prepare-baseline`/`activate` 必须由该 bundle 导出的精确 PR46 head 临时脚本执行，配置中的 `controller_path` 仍固定指向已安装 291 controller。旧 291 controller 先对队首候选完整运行 `maintenance-check`，再由精确候选脚本重复检查并写 durable marker；逐项比对 SHA/tree/base、controller 文件、toolchain、lane 与全通过状态（receipt raw SHA 可能因时间、用时和日志路径不同）。stage 与 production app identity/健康、旧队列结果和 timer 门禁仍须全部满足。GitHub `main` 保持 `6d3ee9c`，不需要先合 PR #46。未满足这些条件时不得执行 host write、构建演练或 timer 激活。新流程日常入口见[国内主仓发布](domestic-main-release.md)。

## 一次性主机准备与切换

以下步骤仅供一次性切换，且必须按此顺序：旧发布器处理完已知运行时变化并逐项读回安装/健康证据；stage 旧 timer/service 停用并确认 inactive，production 的旧/新 units 读回 `not-found` 且无相关 job/process；只读复核已有 source.git=291/tree `3c17b8a86e2e69ed4f6942304300c609300fb077` 和 app SHA `960b30e9406fae2045aeb7ef5dce863976407727`。修复后 `prepare-baseline` 仍以 main=291；PR #46 精确 head 后由正常 candidate 队列处理，不能把它直接当 initial main。未满足条件就保持新 timer disabled，不安装新 timer 到 production。尖括号占位符均须换成核实值。GitHub 凭据只留在开发机；本次 `main=6d3ee9c` 仅供人工归档。完整顺序见本文件后续步骤；配置样例保持 `production_enabled=false` 直到独立门禁通过。

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

**先从 Mac 准备并验证准确 PR #46 head 的 seed bundle。** 旧 stage `/opt/aicrm/source` 不含 PR #46 head 且含旧 GitHub remote 配置，不能用作工具或候选来源。本次国内 `source.git` 已存在并保留 main=291；PR #46 bundle 只用于核验候选历史、导出精确工具字节和运行候选 controller，不用于重建或覆盖该仓库。GitHub `main` 保持在 `6d3ee9c`，仅作人工归档；本次切换不查询也不依赖 GitHub Actions。GitHub `pull_request` 检查使用 GitHub 合成 merge tree，不能证明候选相对国内 291 的检查结果。后文 `resume-baseline` 会在 verified seed 中按 291→candidate 运行 trusted tool lanes、国内 release-controller 全套单测及预备机 helper host contract；这些是本次国内基线的实际检查。bundle 只经现有运维 SSH（`/Users/qianlan/Downloads/zhengshi.pem`，通过 production `124.220.53.183` 跳板到 stage `10.0.4.6`）传到 `/var/tmp`；先核对 HostKey pin 与该 key `0600`。此身份只用于受控文件传输，不提供 GitHub 凭据，也不使用受限 archive-ack key。

   在 Mac 开发机的 clean 工作树上执行，`DOMESTIC_MAIN_SHA` 是 PR #46 最终准确 head，`APP_SHA` 是已安装应用 SHA，`DOMESTIC_MAIN_TREE` 是已审查的 head tree。临时 ref 不覆盖现有 branch，bundle 只包含该 commit 的完整可达历史：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   DOMESTIC_MAIN_TREE=<EXACT_PR46_HEAD_TREE>
   APP_SHA=<EXACT_INSTALLED_APP_SHA>
   SEED_REPO="${TMPDIR:-/tmp}/domestic-main-seed-source-$DOMESTIC_MAIN_SHA.git"
   SEED_REF="refs/heads/main"
   BUNDLE="${TMPDIR:-/tmp}/domestic-main-seed-$DOMESTIC_MAIN_SHA.bundle"
   test ! -e "$SEED_REPO"
   test ! -L "$SEED_REPO"
   test ! -e "$BUNDLE"
   test ! -L "$BUNDLE"
   test "$(git rev-parse HEAD)" = "$DOMESTIC_MAIN_SHA"
   test -z "$(git status --porcelain)"
   test "$(git rev-parse "$DOMESTIC_MAIN_SHA^{tree}")" = "$DOMESTIC_MAIN_TREE"
   git cat-file -e "$DOMESTIC_MAIN_SHA^{commit}"
   git merge-base --is-ancestor "$APP_SHA" "$DOMESTIC_MAIN_SHA"
   git rev-list --first-parent "$DOMESTIC_MAIN_SHA" | grep -Fx "$APP_SHA"
   git init --bare --quiet "$SEED_REPO"
   git -C "$(git rev-parse --show-toplevel)" push "$SEED_REPO" "$DOMESTIC_MAIN_SHA:$SEED_REF"
   git --git-dir="$SEED_REPO" bundle create "$BUNDLE" "$SEED_REF"
   VERIFY=$(git bundle verify "$BUNDLE")
   printf '%s\n' "$VERIFY"
   grep -F 'The bundle records a complete history.' <<<"$VERIFY"
   git bundle list-heads "$BUNDLE" | grep -Fx "$DOMESTIC_MAIN_SHA $SEED_REF"
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
   SEED_REF="refs/heads/main"
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
   BASELINE_SHA=291baa2d13864c3a60f3ed93e08382c3e598db33
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

3. **核验并安装固定工具字节。** 本次基线 SHA 为 `291baa2d13864c3a60f3ed93e08382c3e598db33`。在准确 PR #46 candidate `<EXACT_PR46_HEAD_SHA>` 的 clean V4 工作树核对 fixed files；只有 `deploy/domestic-promote.py` 与 `scripts/domestic_main_release.py` 可与 291 不同，其他固定工具、builder、unit 必须与 291 blob 相同。`domestic-promote.py` 是恢复 cursor 所需的唯一 overlay：先在 stage 与 production 手工备份并原子安装候选 helper，逐台核对候选 SHA；不安装应用，也不改 production controller。PR46 candidate controller 从 seed 导出后仅作临时命令脚本，baseline 前不替换 stage controller。新 controller 在 baseline 后通过候选自身的 `maintenance-check` 写 marker，再手工安装。

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   BASELINE_SHA=291baa2d13864c3a60f3ed93e08382c3e598db33
   test "$(git rev-parse HEAD)" = "$DOMESTIC_MAIN_SHA"
   test -z "$(git status --porcelain)"
   for path in scripts/domestic_release.py \
     scripts/domestic_release_build.py \
     deploy/aicrm-domestic-main-release.service deploy/aicrm-domestic-main-release.timer; do
     baseline=$(git show "$BASELINE_SHA:$path" | sha256sum | cut -d ' ' -f 1)
     candidate=$(git show "$DOMESTIC_MAIN_SHA:$path" | sha256sum | cut -d ' ' -f 1)
     test "$candidate" = "$baseline" || { printf 'fixed tool differs from baseline: %s\n' "$path" >&2; exit 1; }
     printf '%s  %s\n' "$baseline" "$path"
   done
   BASELINE_HELPER=$(git show "$BASELINE_SHA:deploy/domestic-promote.py" | sha256sum | cut -d ' ' -f 1)
   CANDIDATE_HELPER=$(git show "$DOMESTIC_MAIN_SHA:deploy/domestic-promote.py" | sha256sum | cut -d ' ' -f 1)
   test "$CANDIDATE_HELPER" != "$BASELINE_HELPER" || {
     echo 'PR #46 candidate helper unexpectedly equals baseline 291' >&2
     exit 1
   }
   printf '%s  candidate deploy/domestic-promote.py\n' "$CANDIDATE_HELPER"
   ```

   stage 工具先备份再安装为同目录 `.new`，比较文件摘要完全相同后才 `mv` 到正式路径：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   BASELINE_SHA=291baa2d13864c3a60f3ed93e08382c3e598db33
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
   EXPECTED_BASELINE_HELPER=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$BASELINE_SHA:deploy/domestic-promote.py" | sha256sum | awk '{print $1}')
   for path in /usr/local/libexec/aicrm/domestic_release.py \
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
       if [[ "$path" == /usr/local/libexec/aicrm/domestic-promote.py ]]; then
         test "$(sudo sha256sum "$path" | awk '{print $1}')" = "$EXPECTED_BASELINE_HELPER"
         backup="$path.pre-baseline-overlay-291"
         if sudo test -L "$backup"; then
           echo "refusing linked baseline helper backup: $backup" >&2; exit 1
         elif sudo test -e "$backup"; then
           sudo test -f "$backup"
           test "$(sudo stat -c '%F %U:%G %a' -- "$backup")" = 'regular file root:root 600'
           test "$(sudo sha256sum "$backup" | awk '{print $1}')" = "$EXPECTED_BASELINE_HELPER"
         else
           sudo test ! -e "$backup" && sudo test ! -L "$backup"
           sudo install -o root -g root -m 0600 "$path" "$backup"
         fi
         test "$(sudo stat -c '%F %U:%G %a' -- "$backup")" = 'regular file root:root 600'
         test "$(sudo sha256sum "$backup" | awk '{print $1}')" = "$EXPECTED_BASELINE_HELPER"
       else
         sudo test ! -e "$path.pre-domestic-main"
         sudo test ! -L "$path.pre-domestic-main"
         sudo install -o root -g root -m 0600 "$path" "$path.pre-domestic-main"
       fi
     fi
   done
   for path in /usr/local/libexec/aicrm/domestic_release.py.new \
     /usr/local/libexec/aicrm/domestic_release_build.py.new \
     /usr/local/libexec/aicrm/domestic-promote.py.new \
     /etc/systemd/system/aicrm-domestic-main-release.service.new \
     /etc/systemd/system/aicrm-domestic-main-release.timer.new; do
     sudo test ! -e "$path"
     sudo test ! -L "$path"
   done
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/scripts/domestic_release.py" /usr/local/libexec/aicrm/domestic_release.py.new
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/scripts/domestic_release_build.py" /usr/local/libexec/aicrm/domestic_release_build.py.new
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/deploy/domestic-promote.py" /usr/local/libexec/aicrm/domestic-promote.py.new
   sudo install -o root -g root -m 0644 "$SOURCE_WORK/deploy/aicrm-domestic-main-release.service" /etc/systemd/system/aicrm-domestic-main-release.service.new
   sudo install -o root -g root -m 0644 "$SOURCE_WORK/deploy/aicrm-domestic-main-release.timer" /etc/systemd/system/aicrm-domestic-main-release.timer.new
   ```

   在替换正式文件前，固定工具与 unit 的 `.new` 文件逐个比较 baseline 291 blob；唯一例外 `domestic-promote.py.new` 必须匹配准确 PR #46 candidate blob。任一不符即停止，不能执行后续 `mv`。另只读确认已安装 controller 仍匹配 baseline 291 blob，不能安装 candidate controller：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   BASELINE_SHA=291baa2d13864c3a60f3ed93e08382c3e598db33
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   EXPECTED_CONTROLLER=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$BASELINE_SHA:scripts/domestic_main_release.py" | sha256sum | awk '{print $1}')
   test "$(sudo sha256sum /usr/local/libexec/aicrm/domestic_main_release.py | awk '{print $1}')" = "$EXPECTED_CONTROLLER"
   while IFS=' ' read -r source target; do
     expected=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$BASELINE_SHA:$source" | sha256sum | awk '{print $1}')
     actual=$(sudo sha256sum "$target" | awk '{print $1}')
     test "$actual" = "$expected" || { printf 'hash mismatch: %s\n' "$target" >&2; exit 1; }
   done <<'EOF'
   scripts/domestic_release.py /usr/local/libexec/aicrm/domestic_release.py.new
   scripts/domestic_release_build.py /usr/local/libexec/aicrm/domestic_release_build.py.new
   deploy/aicrm-domestic-main-release.service /etc/systemd/system/aicrm-domestic-main-release.service.new
   deploy/aicrm-domestic-main-release.timer /etc/systemd/system/aicrm-domestic-main-release.timer.new
   EOF
   EXPECTED_HELPER=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$DOMESTIC_MAIN_SHA:deploy/domestic-promote.py" | sha256sum | awk '{print $1}')
   test "$(sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py.new | awk '{print $1}')" = "$EXPECTED_HELPER"
   ```

   比对 `.new` 文件与准确 main 源摘要后，才原子替换并读回：

   ```bash
   set -euo pipefail
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

   现有 `.pre-domestic-main` 是更早 helper 的历史备份，保留不动，不作为回滚到 291 的副本。只有 production 当前 helper 与 291 blob、`.new` 与准确 candidate blob 均匹配时，才创建或验证专用 `.pre-baseline-overlay-291`。该专用回滚文件必须为 root:root 0600 regular file 且摘要等于 291；已存在且摘要相同则复用，不覆盖；不存在才从当前 291 helper create-only 保存。随后原子切换 candidate：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   BASELINE_SHA=291baa2d13864c3a60f3ed93e08382c3e598db33
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   EXPECTED=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$DOMESTIC_MAIN_SHA:deploy/domestic-promote.py" | sha256sum | awk '{print $1}')
   EXPECTED_BASELINE=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$BASELINE_SHA:deploy/domestic-promote.py" | sha256sum | awk '{print $1}')
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   REMOTE_NEW_SHA=$(ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py.new' | awk '{print $1}')
   test "$REMOTE_NEW_SHA" = "$EXPECTED"
   REMOTE_CURRENT_SHA=$(ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py' | awk '{print $1}')
   test "$REMOTE_CURRENT_SHA" = "$EXPECTED_BASELINE"
   BACKUP=/usr/local/libexec/aicrm/domestic-promote.py.pre-baseline-overlay-291
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 "sudo bash -s -- '$EXPECTED_BASELINE'" <<'REMOTE_BACKUP_SCRIPT'
   set -euo pipefail
   expected="$1"
   current=/usr/local/libexec/aicrm/domestic-promote.py
   backup=/usr/local/libexec/aicrm/domestic-promote.py.pre-baseline-overlay-291
   temporary="$backup.new"
   test "$(sha256sum "$current" | cut -d ' ' -f1)" = "$expected"
   if test -L "$backup"; then
     echo "refusing linked baseline helper backup: $backup" >&2
     exit 1
   elif test -e "$backup"; then
     test -f "$backup"
     test "$(stat -c '%F %U:%G %a' "$backup")" = 'regular file root:root 600'
     test "$(sha256sum "$backup" | cut -d ' ' -f1)" = "$expected"
   else
     if test -L "$temporary"; then
       echo "refusing linked temporary helper backup: $temporary" >&2
       exit 1
     elif test -e "$temporary"; then
       test -f "$temporary"
       test "$(stat -c '%F %U:%G %a' "$temporary")" = 'regular file root:root 600'
       test "$(sha256sum "$temporary" | cut -d ' ' -f1)" = "$expected"
     else
       install -o root -g root -m 0600 "$current" "$temporary"
     fi
     test "$(stat -c '%F %U:%G %a' "$temporary")" = 'regular file root:root 600'
     test "$(sha256sum "$temporary" | cut -d ' ' -f1)" = "$expected"
     ln -- "$temporary" "$backup"
     rm -- "$temporary"
   fi
   test "$(stat -c '%F %U:%G %a' "$backup")" = 'regular file root:root 600'
   test "$(sha256sum "$backup" | cut -d ' ' -f1)" = "$expected"
REMOTE_BACKUP_SCRIPT
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'set -e; sudo test ! -L /usr/local/libexec/aicrm/domestic-promote.py; sudo test -f /usr/local/libexec/aicrm/domestic-promote.py; test "$(sudo stat -c "%u:%g" /usr/local/libexec/aicrm/domestic-promote.py)" = "0:0"; sudo test -e /usr/local/libexec/aicrm/domestic-promote.py.new; sudo test ! -L /usr/local/libexec/aicrm/domestic-promote.py.new; sudo mv /usr/local/libexec/aicrm/domestic-promote.py.new /usr/local/libexec/aicrm/domestic-promote.py; sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py'
   REMOTE_SHA=$(ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'sudo sha256sum /usr/local/libexec/aicrm/domestic-promote.py' | awk '{print $1}')
   test "$REMOTE_SHA" = "$EXPECTED"
   ssh -i /home/ubuntu/.ssh/ai-crm-v4-prod-deploy \
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/ubuntu/.ssh/known_hosts_aicrm_prod \
     ubuntu@10.0.4.13 'test -f /home/ubuntu/domestic-promote.py.new; test ! -L /home/ubuntu/domestic-promote.py.new; rm -- /home/ubuntu/domestic-promote.py.new'
   ```

   摘要不符时停止并从回滚文件恢复；任何 host write 都须等到旧队列已按序处理完已知运行时变化、stage 旧 timer/service 已停止、production 旧/新 unit 不存在、准确 PR #46 head 的 app→head 分类为 `runtime_changed=false`、两机 app identity 相等、源码 first-parent 关系验证通过且无不明发布结果。

   安装后在 stage 对固定工具与两个 `/etc/systemd/system/aicrm-domestic-main-release.*` 文件运行 `sha256sum`；已安装 controller 必须仍匹配 291 blob，`domestic-promote.py` 必须匹配 PR #46 candidate blob，其他 fixed files 与 291 源 blob 相等。在 production 对 `/usr/local/libexec/aicrm/domestic-promote.py` 运行 `sha256sum`，与 candidate SHA 的源 blob 比较；只有全等才继续。用 `systemd-analyze verify` 核对落盘 unit，并确认新 timer 仍 disabled/inactive。任何摘要不符都停下，使用回滚文件恢复旧字节并复核。

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

   **本次禁止运行旧 `bootstrap` 命令。** `/opt/aicrm/domestic/source.git` 是已核实的 partial bare repo，必须保留。先在 stage 只读确认它仍与 baseline 完全一致，再由经过同一 verified seed 导出且 blob 摘要已核对的 PR #46 临时 controller 执行一次性 `recover-partial-bootstrap`。该恢复只添加 receive hook 并收紧新建 hooks 目录权限，不移动 main、不建 ledger：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   BASELINE_SHA=291baa2d13864c3a60f3ed93e08382c3e598db33
   BASELINE_TREE=3c17b8a86e2e69ed4f6942304300c609300fb077
   APP_SHA=960b30e9406fae2045aeb7ef5dce863976407727
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   SOURCE_REPO=/opt/aicrm/domestic/source.git
   test "$(sudo /usr/bin/git --git-dir="$SOURCE_REPO" rev-parse refs/heads/main)" = "$BASELINE_SHA"
   test "$(sudo /usr/bin/git --git-dir="$SOURCE_REPO" rev-parse 'refs/heads/main^{tree}')" = "$BASELINE_TREE"
   test "$(sudo /usr/bin/git --git-dir="$SOURCE_REPO" config --get core.sharedRepository)" = 1
   test "$(sudo /usr/bin/git --git-dir="$SOURCE_REPO" for-each-ref --format='%(refname)')" = refs/heads/main
   sudo /usr/bin/git --git-dir="$SOURCE_REPO" fsck --full --strict --no-reflogs
   sudo test ! -e /opt/aicrm/domestic/control/state.json
   sudo test ! -L /opt/aicrm/domestic/control/state.json
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse refs/heads/main)" = "$DOMESTIC_MAIN_SHA"
   test "$(sudo /usr/bin/git --git-dir="$SEED_REPO" rev-parse "$BASELINE_SHA^{tree}")" = "$BASELINE_TREE"
   sudo /usr/bin/python3 "$SOURCE_WORK/scripts/domestic_main_release.py" \
     --config /etc/aicrm/domestic-main-release.json recover-partial-bootstrap \
     --sha "$BASELINE_SHA" --tree "$BASELINE_TREE" --installed-app-sha "$APP_SHA"
   test "$(sudo /usr/bin/git --git-dir="$SOURCE_REPO" rev-parse refs/heads/main)" = "$BASELINE_SHA"
   test "$(sudo /usr/bin/git --git-dir="$SOURCE_REPO" rev-parse 'refs/heads/main^{tree}')" = "$BASELINE_TREE"
   sudo /usr/bin/git --git-dir="$SOURCE_REPO" fsck --full --strict --no-reflogs
   ```

   若恢复在 hook 创建后失败或中断，禁止重跑恢复命令、删除 hook 或删除/重建仓库；停下，只读核对现场并制定单独修复方案。hook 已存在时恢复器会 fail-closed。保留 bundle、seed repo 与 `SOURCE_WORK`，后续 baseline 和首次候选检查仍需这些已验证对象。

   裸仓由 root 创建，推送组只写 Git objects 和 `refs/heads/codex/*`；`main`、candidate pins、钩子、配置均不可由推送账号改写。运行 `verify_bare_repository` 前核对 `aicrm-build` 能读仓库且不属于推送组。

   5. **在 2 核、2GB 预备机运行 build-only 容量演练。** 仅在旧发布队列已按序处理完已知运行时变化、准确 PR #46 head 与两机已安装 app 的身份关系读回并验证完成、stage 旧 timer/service 已停止且 production units 为 `not-found`、所有结果不明项已对账后进行。恢复 hook 后，先用现有受限 SSH 流程将准确 candidate SHA 推到新的 `refs/heads/codex/domestic-main-cutover` 并读回；此时不要 submit 到队列，国内 main 仍须为 291。该 candidate ref 使下面从 `/opt/aicrm/domestic/source.git` 的 clone 能取得待测对象。此前记录的完整构建用时不包含峰值内存，不能据此认定 2GB 容量稳定。该演练会使用现有 build-worker 的 Go/npm 共享缓存，因此旧队列活动期间严禁运行；不得清空或重置共享缓存。先确认 stage 当前 `/readyz` 健康、磁盘有足够空间、无运行发布任务。演练只运行 `domestic_release_build.py build`；**不要以 `poll` 作为演练命令**，因为开启生产配置后它会真实晋级生产。以下用隔离的本地 clone，`--base-release none` 明确强制完整构建，不安装、不迁移、不连接生产。为输出填写生产已安装 SHA 与当前准确 PR #46 head。冷缓存与热缓存各用不同 `RUN_KIND` 和新的 UTC `RUN_ID`，每次得到全新 workspace；目录已存在或为 symlink 时停止，不覆盖旧结果：

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
   CANDIDATE_REF=refs/heads/codex/domestic-main-cutover
   sudo -u aicrm-build -H git -C "$WORK/source" fetch -q --no-tags "$REPO" "$CANDIDATE_REF"
   test "$(sudo -u aicrm-build -H git -C "$WORK/source" rev-parse FETCH_HEAD)" = "$DOMESTIC_MAIN_SHA"
   sudo -u aicrm-build -H git -C "$WORK/source" checkout --detach FETCH_HEAD
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

   6. **以 291 写 baseline，再将 PR46 head 作为首个候选串行升级。** 先确认两机已安装 app SHA/tree/manifest 与健康状态相同，stage 旧 timer/service 已停用，production 旧/新 units 为 `not-found`，且 source.git main/tree 仍为 291。baseline 必须绑定 `291baa2d...`/`3c17b8a...`，并独立记录 app=960。ledger 尚不存在时，由 root 显式编辑 stage 配置，将 `production_enabled` 从 `false` 改为 `true`，保持 `root:root 0600`。`prepare-baseline`、`activate`、`verify` 均使用从 verified PR46 seed 导出且 blob SHA 精确匹配的临时 NEW_HEAD 脚本；配置 `controller_path` 始终指向当前已安装的 291 controller。production 不安装或启动 stage units：

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
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   BASELINE_SHA=291baa2d13864c3a60f3ed93e08382c3e598db33
   BASELINE_TREE=3c17b8a86e2e69ed4f6942304300c609300fb077
   APP_SHA=960b30e9406fae2045aeb7ef5dce863976407727
   test "$(sudo /usr/bin/git --git-dir=/opt/aicrm/domestic/source.git rev-parse refs/heads/main)" = "$BASELINE_SHA"
   test "$(sudo /usr/bin/git --git-dir=/opt/aicrm/domestic/source.git rev-parse 'refs/heads/main^{tree}')" = "$BASELINE_TREE"
   SEED_BUNDLE="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.bundle"
   test -f "$SEED_BUNDLE" && test ! -L "$SEED_BUNDLE"
   # This verifies the exact 291-to-candidate seed and local gates; GitHub CI is not queried.
   # It writes an immutable, root-only proof at work/baseline-helper-overlay-$DOMESTIC_MAIN_SHA.json.
   sudo /usr/bin/python3 "$SOURCE_WORK/scripts/domestic_main_release.py" \
     --config /etc/aicrm/domestic-main-release.json resume-baseline \
     --candidate-sha "$DOMESTIC_MAIN_SHA" --seed-bundle "$SEED_BUNDLE"
   sudo /usr/bin/python3 "$SOURCE_WORK/scripts/domestic_main_release.py" \
     --config /etc/aicrm/domestic-main-release.json activate --candidate-sha "$DOMESTIC_MAIN_SHA"
   sudo /usr/bin/python3 "$SOURCE_WORK/scripts/domestic_main_release.py" \
     --config /etc/aicrm/domestic-main-release.json verify --candidate-sha "$DOMESTIC_MAIN_SHA"
   ```

   本次中断前生产 source bundle 与 receipt 已持久化，所以必须先 `resume-baseline`；全新初始化且确认 production 尚无对应 backup 时才执行一次 `prepare-baseline`。两种路径返回的 source main SHA/tree 与生产 cursor 必须完全一致，初始队列为空。overlay 会把候选 SHA、候选 helper/controller blob、seed 摘要及 production source receipt 原始摘要写入每 SHA 独立的 root-only 文件，并在 `work_root/baseline-helper-overlay-active.json` 记录当前唯一候选。GitHub CI 不参与此门禁；硬门禁是在 verified seed 上相对 291 执行的 trusted tool lanes、controller 全套单测及 stage helper host contract。resume 只有在准确 seed/helper 已核对后才只读查询 production cursor 是否存在；首次候选先建立 active pointer，再写 marker、再幂等初始化 cursor。若 cursor 与 ledger 都尚不存在，新 head 可以原子 supersede active SHA，旧 SHA marker 保留且 pointer 记录历史；任一已存在时 active SHA 不可更换。`activate`/`verify` 在调用任一 production helper 前要求 marker 与 active pointer SHA 一致，并先核对当前 helper/controller 摘要。随后将同一 SHA 通过现有受限入口登记为 `base=291` 的队首候选；不得先 poll。运行 verified seed 导出的 exact candidate script 执行 `maintenance-check`，确认 marker 后再安装 candidate controller：

   如果后续 `prepare-baseline` 在生产 source bundle 已保存后中断，**不要再次运行 `prepare-baseline`**。先只读核对 stage 本地 `.bundle` 与 `.json` 的 SHA、树、旧 app SHA 和 bundle 摘要，再用相同 PR46 head 和 seed bundle 执行 `resume-baseline`：

   ```bash
   sudo /usr/bin/python3 "$SOURCE_WORK/scripts/domestic_main_release.py" \
     --config /etc/aicrm/domestic-main-release.json resume-baseline \
     --candidate-sha "$DOMESTIC_MAIN_SHA" --seed-bundle "/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.bundle"
   ```

   该恢复入口只验证已有本地 bundle/meta，并让已摘要核对的 candidate production helper 只读核验其 root-owned bundle 与 receipt，随后幂等初始化/读回 production cursor。它不重新传包、不调用保存命令、不构建或安装应用。摘要、身份、receipt、现存 cursor 任一不符就停止；不得删除、覆盖、重建或重传。同 SHA 重入重新检查 seed、helper、base=291 tool lanes、controller 单测及 staging host contract；create-only marker 保留首次 check receipt 摘要，后续运行目录/耗时导致的 receipt hash 差异不改变候选身份，也不会覆盖 marker。若 PR head 变更且 cursor 与 ledger 都尚不存在，可用新 head 更新 active pointer、保留旧 SHA marker 供审计；若 cursor 或 ledger 任一已创建，则 active pointer 固定，只能继续原 SHA 并对账，禁止换候选。

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   CONFIG=/etc/aicrm/domestic-main-release.json
   sudo /usr/bin/python3 "$SOURCE_WORK/scripts/domestic_main_release.py" \
     --config "$CONFIG" maintenance-check --sha "$DOMESTIC_MAIN_SHA"
   ```

   结果必须显示 candidate SHA/tree/base、controller 文件清单、toolchain 与 lane 集合，且每条 lane 均通过。此处 `maintenance-check` 是在正式激活前的独立复核；`resume-baseline` 还会先对 verified seed 的准确 291→candidate 树运行 trusted tool lanes 与 `scripts.test_domestic_release_controller` 全套单测，并现场执行 staging helper host contract。只有当前准确候选脚本会写入 marker。确认 marker 绑定 active pointer 唯一 SHA、队首候选的 SHA/base/tree 和 fixed-file hashes 后，才以不可覆盖的回滚副本和原子替换方式安装候选 controller：

   ```bash
   set -euo pipefail
   DOMESTIC_MAIN_SHA=<EXACT_PR46_HEAD_SHA>
   SOURCE_WORK="/var/tmp/domestic-main-source-$DOMESTIC_MAIN_SHA"
   SEED_REPO="/var/tmp/domestic-main-seed-$DOMESTIC_MAIN_SHA.git"
   CONTROLLER=/usr/local/libexec/aicrm/domestic_main_release.py
   BACKUP="$CONTROLLER.pre-maintenance-$DOMESTIC_MAIN_SHA"
   EXPECTED=$(sudo /usr/bin/git --git-dir="$SEED_REPO" show "$DOMESTIC_MAIN_SHA:scripts/domestic_main_release.py" | sha256sum | awk '{print $1}')
   sudo test -f "$CONTROLLER"
   sudo test ! -L "$CONTROLLER"
   sudo test ! -e "$BACKUP"
   sudo test ! -L "$BACKUP"
   sudo test ! -e "$CONTROLLER.new"
   sudo test ! -L "$CONTROLLER.new"
   sudo install -o root -g root -m 0600 "$CONTROLLER" "$BACKUP"
   sudo install -o root -g root -m 0755 "$SOURCE_WORK/scripts/domestic_main_release.py" "$CONTROLLER.new"
   test "$(sudo sha256sum "$CONTROLLER.new" | awk '{print $1}')" = "$EXPECTED"
   sudo mv "$CONTROLLER.new" "$CONTROLLER"
   test "$(sudo sha256sum "$CONTROLLER" | awk '{print $1}')" = "$EXPECTED"
   ```

   若候选已被旧 controller 记为 failed，只能通过原受限入口对同一 SHA/ref/base 显式重提，不能改候选。未有 marker 时 poll 保持原 fixed-tool 摘要门禁；marker 建立后 poll 仅为当前唯一队首候选临时接受该工具集合。该 source-only candidate 通过正常锁、测试与 CAS 晋级后，main 到达新 SHA，固定工具摘要校验自动恢复常规严格模式。若 prepare/activate 响应或读回不明，不重跑，转只读对账：

   固定 controller 精确替换后，先手动显式处理唯一队首候选，再独立读回 ledger、国内 main、生产 cursor、安装 app identity 与健康；不得让启用 timer 触发首个候选：

   ```bash
   set -euo pipefail
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py \
     --config /etc/aicrm/domestic-main-release.json poll
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py \
     --config /etc/aicrm/domestic-main-release.json verify
   ```

   只有 `poll` 精确完成 candidate、`verify` 读回 domestic main 与生产 cursor 一致、app SHA/tree/manifest 仍与 960 安装收据一致且 stage/production 健康时，才最后启用新 timer：

   ```bash
   # Run only on stage; production has no domestic-main-release unit.
   sudo systemctl enable --now aicrm-domestic-main-release.timer
   sudo systemctl is-enabled aicrm-domestic-main-release.timer
   sudo systemctl list-timers --all aicrm-domestic-main-release.timer
   sudo /usr/bin/python3 /usr/local/libexec/aicrm/domestic_main_release.py --config /etc/aicrm/domestic-main-release.json verify
   ```

   若首个 poll 未完成、verify/readback 不一致或任一结果不明，保持新 timer disabled，只读对账，不盲目重试。验证旧 timer/service 始终停用、新 timer 工作正常且无双重写入口后，再按归档流程撤销旧 GitHub deploy key；此后人工同步仍只在开发者电脑显式执行。任何阶段 identity、摘要、服务或健康不匹配，保持新 timer disabled，回滚固定工具文件并继续只读核对。
