#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run this uninstaller as root." >&2
  exit 1
fi

systemctl disable --now m365plus 2>/dev/null || true
rm -f /etc/systemd/system/m365plus.service
rm -rf /opt/m365plus
systemctl daemon-reload
echo "M365Plus was removed. Configuration and account data remain in /etc/m365plus and /var/lib/m365plus."
