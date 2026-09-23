#!/usr/bin/env bash
set -euo pipefail
release_dir="${1:?release directory required}"
[[ "$release_dir" =~ ^/opt/aicrm/releases/[0-9a-f]{40}$ ]] || exit 2
# Initial activation requires explicitly provisioned configuration. Future
# releases preserve that activation and never infer a data source or sender.
[[ -f /etc/aicrm-excel/config.json && -f /etc/aicrm-excel/service.env ]] || exit 0
test -f "$release_dir/components/excel-batches/batches.py"
test -f "$release_dir/components/excel-batches/requirements.txt"
if ! id aicrm-excel >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/aicrm-excel --shell /usr/sbin/nologin aicrm-excel
fi
install -d -m 0750 -o root -g aicrm-excel /etc/aicrm-excel
install -d -m 0755 /opt/aicrm-excel
install -d -m 0700 -o aicrm-excel -g aicrm-excel /var/lib/aicrm-excel
if [[ ! -x /opt/aicrm-excel/venv/bin/python ]]; then
  python3 -m venv /opt/aicrm-excel/venv
fi
/opt/aicrm-excel/venv/bin/pip install --disable-pip-version-check -r "$release_dir/components/excel-batches/requirements.txt"
chown root:aicrm-excel /etc/aicrm-excel/config.json /etc/aicrm-excel/service.env
chmod 0640 /etc/aicrm-excel/config.json /etc/aicrm-excel/service.env
install -m 0644 "$release_dir/components/excel-batches/aicrm-excel-batches.service" /etc/systemd/system/aicrm-excel-batches.service
