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
[[ $(uname -m) == x86_64 ]] || die "仅支持 x86_64 (amd64) 架构。"
[[ $(id -u) -eq 0 ]] || die "请使用 root 权限运行。"

for cmd in curl gzip python3 systemctl; do
  command -v "$cmd" >/dev/null 2>&1 || die "缺少依赖命令: $cmd"
done

mkdir -p "${INSTALL_DIR}/data"
tmp_dir=$(mktemp -d "${INSTALL_DIR}/.tmp-install.XXXXXX")

rollback() {
  local exit_code=$?
  if [[ $exit_code -ne 0 ]]; then
    if [[ -f "${tmp_dir}/gwd.bak" ]]; then
      info "安装或验证失败，正在停止服务并回滚旧版本..."
      systemctl stop gwd 2>/dev/null || true
      cp -fp "${tmp_dir}/gwd.bak" "${BIN_PATH}"
      chmod 755 "${BIN_PATH}"
      if [[ -f "${tmp_dir}/gwdctl.bak" ]]; then
        cp -fp "${tmp_dir}/gwdctl.bak" "${CTL_PATH}"
        chmod 755 "${CTL_PATH}"
      fi
      info "正在重启旧版本服务..."
      systemctl daemon-reload 2>/dev/null || true
      systemctl start gwd 2>/dev/null || true
      if systemctl is-active --quiet gwd; then
        ok "旧版本服务已成功恢复运行。"
      else
        info "警告: 旧版本服务恢复后未能正常运行，请检查: journalctl -u gwd -n 30"
      fi
    fi
  fi
  rm -rf "$tmp_dir"
}
trap rollback EXIT

info "正在下载 Google Drive 服务端程序..."
BIN_URL="https://github.com/${REPO}/releases/download/v1.0.4/gwd-linux-amd64.gz"

curl -fL --retry 3 --connect-timeout 15 "$BIN_URL" -o "${tmp_dir}/gwd.gz" || die "下载服务端程序失败。"

gzip -t "${tmp_dir}/gwd.gz" || die "下载的服务端压缩包损坏。"
gzip -dc "${tmp_dir}/gwd.gz" > "${tmp_dir}/gwd"
chmod 755 "${tmp_dir}/gwd"

info "正在下载 gwdctl 终端管理工具..."
CTL_URL="https://raw.githubusercontent.com/${REPO}/main/gwdctl.py?t=$(date +%s)"
curl -fsSL --retry 3 --connect-timeout 15 "$CTL_URL" -o "${tmp_dir}/gwdctl.py" || die "下载 gwdctl 失败。"
python3 -m py_compile "${tmp_dir}/gwdctl.py" >/dev/null 2>&1 || die "下载的 gwdctl.py 脚本语法错误。"
chmod 755 "${tmp_dir}/gwdctl.py"

# Pre-create or update config.json with 127.0.0.1 before starting service
if [[ ! -f "${INSTALL_DIR}/data/config.json" ]]; then
  cat > "${INSTALL_DIR}/data/config.json" <<'EOF'
{
  "scheme": {
    "address": "127.0.0.1",
    "http_port": 5244
  }
}
EOF
  chmod 600 "${INSTALL_DIR}/data/config.json"
else
  sed -i 's/"address": "[^"]*"/"address": "127.0.0.1"/' "${INSTALL_DIR}/data/config.json"
fi

# Backup existing binaries if any
if [[ -f "${BIN_PATH}" ]]; then
  cp -p "${BIN_PATH}" "${tmp_dir}/gwd.bak"
fi
if [[ -f "${CTL_PATH}" ]]; then
  cp -p "${CTL_PATH}" "${tmp_dir}/gwdctl.bak"
fi

systemctl stop gwd 2>/dev/null || true

# Atomic replace on the same filesystem
mv -f "${tmp_dir}/gwd" "${BIN_PATH}"
mv -f "${tmp_dir}/gwdctl.py" "${CTL_PATH}"
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

check_health() {
  python3 -c "
import urllib.request, json, sys
try:
    req = urllib.request.Request('http://127.0.0.1:5244/api/public/init_status', headers={'User-Agent': 'gwd-installer'})
    with urllib.request.urlopen(req, timeout=3) as resp:
        if resp.status != 200:
            sys.exit(1)
        data = json.loads(resp.read().decode('utf-8'))
        if data.get('code') == 200 and isinstance(data.get('data'), dict) and isinstance(data['data'].get('initialized'), bool):
            sys.exit(0)
        sys.exit(1)
except Exception:
    sys.exit(1)
" >/dev/null 2>&1
}

info "正在等待服务启动及健康检查..."
ready=0
for _ in {1..30}; do
  if systemctl is-active --quiet gwd && [[ -f "${INSTALL_DIR}/data/data.db" ]] && check_health; then
    ready=1
    break
  fi
  sleep 1
done

if [[ $ready -ne 1 ]]; then
  die "服务未能成功启动、数据库未就绪或健康检查超时，请执行 journalctl -u gwd -n 30 查看日志。"
fi

ok "安装完成！默认监听: 127.0.0.1:5244"
ok "请运行 gwdctl 打开终端管理菜单。"
