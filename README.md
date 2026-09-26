# VibeMonitor

面向 Linux x86-64 / ARM64（aarch64）的轻量服务器监控程序。

## 功能


- 无插件
- 无外部通知功能
- Hex 主题提供节点状态、周期流量、实时网速和高负载总览
- 无远程控制及文件管理
- 账单周期基础功能及流量统计
- 首个流量周期可以自定义已使用流量
- 单用户管理员登录/点击站点图标进入管理菜单
- 探针仅回传/安装命令在管理>编辑现有节点>显示安装命令
- 添加节点/编辑节点/删除节点
- 测速节点目标手动维护在节点信息内


  
## 安装
- 粘贴复制选择菜单内容，更新主控菜单里选择10
- 初次安装域名反代下面也有说明
```bash
curl -4 -fsSL -o install.sh https://raw.githubusercontent.com/M48A1/vibemonitor/main/install.sh && bash install.sh
```

## 域名反代（Nginx）

先将域名解析到主控服务器，并准备好 HTTPS 证书。以下示例假设 Nginx 与主控运行在同一台机器，主控端口为 `1314`。

安装主控后，执行 `sudo systemctl edit vibemonitor-server`，将主控改为只监听本机：

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/vibemonitor server --listen 127.0.0.1:1314 --data /etc/vibemonitor/vibemonitor-data.db
```

保存后运行 `sudo systemctl daemon-reload && sudo systemctl restart vibemonitor-server`。如果你修改过主控的程序路径、数据路径或启动参数，请按现有服务配置调整上面的 `ExecStart`。

将以下示例保存为 Nginx 站点配置（例如 `/etc/nginx/conf.d/vibemonitor.conf`），并替换域名和证书路径：

```nginx
server {
    listen 80;
    server_name monitor.example.com;
    return 301 https://monitor.example.com$request_uri;
}

server {
    listen 443 ssl;
    server_name monitor.example.com;
    ssl_certificate /etc/nginx/certs/monitor.example.com.fullchain.pem;
    ssl_certificate_key /etc/nginx/certs/monitor.example.com.key;
    client_max_body_size 3m;

    location / {
        proxy_pass http://127.0.0.1:1314;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 90s;
    }
}
```

保存 Nginx 配置后运行 `sudo nginx -t && sudo systemctl reload nginx`，再通过 `https://monitor.example.com` 访问主控，探针也填写这个 HTTPS 地址。反代应覆盖客户端传来的 `X-Forwarded-For` 和 `X-Real-IP`；动态探针安装链接含节点 Token，不要公开分享。
