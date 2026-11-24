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

网盘使用单一管理员账号，不提供访客账号或访客浏览模式。公开分享链接仍可由持有链接的访客按分享规则访问。

## Nginx 反向代理

服务默认只监听 `127.0.0.1:5244`，并仅信任本机代理提供的客户端 IP。Nginx 的代理位置必须覆盖客户端传入的转发头，不要直接追加客户端提供的 `X-Forwarded-For`；同时建议关闭请求与响应缓冲并取消请求体大小限制，以确保大文件流式上传与文件夹打包下载不在代理层落盘或超时：

```nginx
client_max_body_size 0;

location / {
    proxy_pass http://127.0.0.1:5244;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
}
```

完整 HTTPS 与 HTTP 自动跳转参考配置见 [nginx/gwd.conf.example](nginx/gwd.conf.example)。

如果 Nginx 前还有 CDN 或其他代理，先按其官方说明仅信任指定的代理地址，并正确配置 Nginx real IP 模块；否则 `$remote_addr` 可能是上游代理地址，多个用户仍可能共用登录限制。

## 卸载

```bash
gwdctl uninstall
```
