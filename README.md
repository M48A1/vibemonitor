# VibeMonitor

轻量的服务器监控程序，支持 Linux x86-64 / ARM64 和 systemd。主控展示节点状态、实时网速、周期流量、TCP 延迟与账单信息；探针只向主控上报数据。

## 安装

在需要安装的机器上下载最新版安装器：

```bash
curl -4 -fL --progress-bar -o install.sh https://github.com/M48A1/vibemonitor/releases/latest/download/install.sh
sudo bash install.sh
```

菜单中选择 **1 安装主控**、**2 安装探针**。安装主控需要设置管理员账号和密码；安装探针前，先在主控的节点管理中创建节点并取得 Token。安装器会校验从 GitHub Release 下载的程序和 SHA-256 清单。公网访问建议使用 HTTPS。

也可直接运行命令：

```bash
sudo bash install.sh server -p 1314 -u admin -w '请替换为强密码'
sudo bash install.sh agent -s https://monitor.example.com -t YOUR_NODE_TOKEN -i 3s
```

主控和探针分别在对应机器上执行。命令行中的密码和 Token 可能留在 shell 历史中；在交互终端中可使用安装菜单输入。`server` 会清空重装主控，请勿用它升级已有服务。

## 更新

仅从 GitHub 拉取源码或配置不会更新正在运行的服务。先在主控机器下载最新版安装器、备份并更新主控：

```bash
curl -4 -fL --progress-bar -o install.sh https://github.com/M48A1/vibemonitor/releases/latest/download/install.sh
sudo bash install.sh backup
sudo bash install.sh update -p 1314
```

将 `1314` 改为主控实际监听端口。`update` 保留账号、节点、配置和监控数据；也可在菜单选择 **10 更新主控**。然后在各探针机器下载新版安装器，使用原来的主控地址和节点 Token 再次执行 `agent` 安装命令。同机探针共用主控程序文件，更新主控后重启 `vibemonitor-agent` 服务即可。

新版探针使用请求头传递 Token；仍通过 RPC URL 的 `?token=` 传递 Token 的旧探针无法上报。旧版探针仍可向新版主控上报，但无法提供新版流量计数器和重启标识。

## HTTPS 反向代理

以同机 Nginx 为例，先用 `systemctl edit vibemonitor-server` 将主控监听地址设为 `127.0.0.1:1314`：

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/vibemonitor server --listen 127.0.0.1:1314 --data /etc/vibemonitor/vibemonitor-data.db
```

执行 `sudo systemctl daemon-reload && sudo systemctl restart vibemonitor-server` 使其生效。配置域名和证书，并将请求转发到主控：

```nginx
server {
    listen 443 ssl;
    server_name monitor.example.com;
    ssl_certificate /etc/nginx/certs/monitor.example.com.fullchain.pem;
    ssl_certificate_key /etc/nginx/certs/monitor.example.com.key;

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

保存 Nginx 配置后，执行 `sudo nginx -t && sudo systemctl reload nginx`。

反向代理应覆盖客户端传来的 `X-Forwarded-For` 和 `X-Real-IP`。探针 RPC 只接受 `Authorization: Bearer` 或 `X-Token` 请求头中的 Token；动态安装链接仍包含 Token，请勿公开分享。

## 流量统计

- 默认按月统计上传与下载之和，未设置重置日时使用每月 1 日。节点管理中可手动设置当前周期已用流量；留空保持原值，填写 `0` 清零，之后继续累加新流量。
- 新版探针上报各网卡累计计数器、采样时间和系统重启标识。主控处理单张网卡计数器重置及接口变化；跨账期的上报间隔按时间比例估算流量，无法还原每一刻的精确用量。
- 默认排除回环和常见虚拟或重复计数网卡。复杂网络可在探针启动参数中使用 `--interfaces eth0`，或设置 `VIBEMONITOR_INTERFACES=eth0`；多个网卡用逗号分隔，避免同时统计同一流量经过的物理接口和隧道接口。

## 数据与备份

主控默认将数据保存在 `/etc/vibemonitor/vibemonitor-data.db`，其中包含配置、流量和监控历史。备份与恢复使用：

```bash
sudo bash install.sh backup
sudo bash install.sh restore /etc/vibemonitor/backups/data-YYYYMMDD-HHMMSS.XXXXXX.db
```

恢复会覆盖当前数据和密码，请妥善保管备份文件。不要在服务运行时只复制 SQLite 的 `.db` 文件而忽略 WAL。`server` 重装和 `uninstall` 会删除主控数据；升级请使用 `update`。从 v1.0.40 以前版本升级时，请先完成 SQLite 迁移。

## 常用命令

```bash
sudo bash install.sh status
sudo bash install.sh restart
sudo bash install.sh agent-uninstall  # 仅卸载探针
sudo bash install.sh uninstall        # 卸载主控和探针，并删除数据
```

节点管理可为每个节点设置 TCP 测试目标（例如 `电信,example.com:443`）和账单信息。TCP 测试只测连接延迟，不测下载速度或 HTTP 响应内容。
