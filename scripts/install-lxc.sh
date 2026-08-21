#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run this installer as root." >&2
  exit 1
fi

ARCH=$(uname -m)
case "${ARCH}" in
  x86_64|amd64) PACKAGE_ARCH=amd64 ;;
  aarch64|arm64) PACKAGE_ARCH=arm64 ;;
  *) echo "Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac

VERSION=${M365PLUS_VERSION:-latest}
REPOSITORY=${M365PLUS_REPOSITORY:-ryc2077/m365plus}
if [ "${VERSION}" = latest ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPOSITORY}/releases/latest" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
fi
if [ -z "${VERSION}" ]; then
  echo "Unable to determine the latest release version." >&2
  exit 1
fi

PACKAGE="m365plus-lxc-linux-${PACKAGE_ARCH}.tar.gz"
URL="https://github.com/${REPOSITORY}/releases/download/${VERSION}/${PACKAGE}"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

command -v curl >/dev/null 2>&1 || { apt-get update; apt-get install -y curl ca-certificates; }
curl -fL "${URL}" -o "${TMP_DIR}/${PACKAGE}"
tar -xzf "${TMP_DIR}/${PACKAGE}" -C "${TMP_DIR}"

if ! id m365plus >/dev/null 2>&1; then
  useradd --system --home /var/lib/m365plus --shell /usr/sbin/nologin m365plus
fi
install -d -m 0755 /opt/m365plus/bin /opt/m365plus/web /etc/m365plus
install -d -o m365plus -g m365plus -m 0750 /var/lib/m365plus /var/lib/m365plus/tokens /var/lib/m365plus/sing-box
install -m 0755 "${TMP_DIR}/m365plus/bin/m365-bridge" /opt/m365plus/bin/m365-bridge
install -m 0755 "${TMP_DIR}/m365plus/bin/sing-box" /opt/m365plus/bin/sing-box
install -m 0755 "${TMP_DIR}/m365plus/bin/m365plus-entrypoint" /opt/m365plus/bin/m365plus-entrypoint
cp -R "${TMP_DIR}/m365plus/web/." /opt/m365plus/web/
install -m 0644 "${TMP_DIR}/m365plus/m365plus.service" /etc/systemd/system/m365plus.service
if [ ! -f /etc/m365plus/m365plus.env ]; then
  install -m 0640 "${TMP_DIR}/m365plus/m365plus.env" /etc/m365plus/m365plus.env
fi
chown -R root:root /opt/m365plus
systemctl daemon-reload
systemctl enable --now m365plus
echo "M365Plus ${VERSION} is running on port 8234."
echo "Status: systemctl status m365plus"
echo "Logs: journalctl -u m365plus -f"
