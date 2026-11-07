#!/usr/bin/env bash
set -Eeuo pipefail

readonly INSTALL_DIR="/opt/gwd"
readonly BIN_PATH="${INSTALL_DIR}/gwd"
readonly CTL_PATH="${INSTALL_DIR}/gwdctl.py"
readonly SERVICE_FILE="/etc/systemd/system/gwd.service"
readonly REPO="QiuXiaoye1112/gwdctl"

info() { printf '\033[1;34m[INFO]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[OK]\033[0m   %s\n' "$*"; }
die()  { printf '\033[1;31m[ERR]\033[0m  %s\n' "$*" >&2; exit 1; }

[[ $(uname -s) == Linux ]] || die "仅支持 Linux 系统。"
[[ $(id -u) -eq 0 ]] || die "请使用 root 权限运行。"

for cmd in curl gzip python3 systemctl; do
  command -v "$cmd" >/dev/null 2>&1 || die "缺少依赖命令: $cmd"
done

mkdir -p "${INSTALL_DIR}/data"
tmp_dir=$(mktemp -d /var/tmp/gwd-install.XXXXXX)
trap 'rm -rf "$tmp_dir"' EXIT

info "正在下载 Google Drive 服务端程序..."
BIN_URL="https://github.com/${REPO}/releases/download/v1.0.0/gwd-linux-amd64.gz"
RAW_BIN_URL="https://raw.githubusercontent.com/${REPO}/main/gwd-linux-amd64.gz"

if ! curl -fL --retry 3 --connect-timeout 15 "$BIN_URL" -o "${tmp_dir}/gwd.gz" 2>/dev/null; then
  curl -fL --retry 3 --connect-timeout 15 "$RAW_BIN_URL" -o "${tmp_dir}/gwd.gz" || die "下载服务端程序失败。"
fi

systemctl stop gwd 2>/dev/null || true
gzip -dc "${tmp_dir}/gwd.gz" > "${BIN_PATH}"
chmod 755 "${BIN_PATH}"

info "正在下载 gwdctl 终端管理工具..."
CTL_URL="https://raw.githubusercontent.com/${REPO}/main/gwdctl.py?t=$(date +%s)"
curl -fsSL --retry 3 --connect-timeout 15 "$CTL_URL" -o "${CTL_PATH}" || die "下载 gwdctl 失败。"
chmod 755 "${CTL_PATH}"
ln -sf "${CTL_PATH}" /usr/local/bin/gwdctl
ln -sf "${CTL_PATH}" /usr/local/sbin/gwdctl

info "正在配置 systemd 服务 (端口: 5244)..."
cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=Google Drive Web Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BIN_PATH} server
Restart=on-failure
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now gwd

for _ in {1..20}; do
  [[ -f "${INSTALL_DIR}/data/data.db" ]] && break
  sleep 0.5
done

ok "安装完成！默认监听端口: 5244"
ok "请运行 gwdctl 打开终端管理菜单。"
