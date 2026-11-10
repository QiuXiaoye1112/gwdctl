# gwdctl

> **系统要求**：Linux x86_64 (amd64) 架构。

## 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/QiuXiaoye1112/gwdctl/main/install.sh | sudo bash
```

## 终端管理

```bash
gwdctl
```

## Nginx 反向代理

服务默认只监听 `127.0.0.1:5244`，并仅信任本机代理提供的客户端 IP。Nginx 的代理位置必须覆盖客户端传入的转发头，不要直接追加客户端提供的 `X-Forwarded-For`：

```nginx
location / {
    proxy_pass http://127.0.0.1:5244;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

如果 Nginx 前还有 CDN 或其他代理，先按其官方说明仅信任指定的代理地址，并正确配置 Nginx real IP 模块；否则 `$remote_addr` 可能是上游代理地址，多个用户仍可能共用登录限制。

## 卸载

```bash
gwdctl uninstall
```
