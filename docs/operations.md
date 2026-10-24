# 运维与开发指南

## 安装位置与服务

通过 `install.sh` 安装时使用以下路径；手动启动可用 `--data` 指定其他数据库位置。

| 内容 | 默认位置或服务名 |
| --- | --- |
| 可执行文件 | `/usr/local/bin/vibemonitor` |
| 主控数据库 | `/etc/vibemonitor/vibemonitor-data.db` |
| 安装脚本生成的备份 | `/etc/vibemonitor/backups/` |
| 主控服务 | `vibemonitor-server.service` |
| 探针服务 | `vibemonitor-agent.service` |
| Nginx 配置 | `/etc/nginx/conf.d/vibemonitor.conf` |

数据库保存账号密码哈希、节点 Token、Telegram 配置、主题、图标和历史数据。备份也包含这些信息，应限制访问权限。SQLite 工作时可能生成同名的 `-wal`、`-shm` 文件；使用程序提供的导出功能生成完整备份。

```bash
sudo systemctl status vibemonitor-server
sudo journalctl -u vibemonitor-server -n 100 --no-pager
sudo journalctl -u vibemonitor-agent -n 100 --no-pager
```

手动启动主控时，未指定 `--data` 的默认路径是当前工作目录下的 `vibemonitor-data.db`。数据文件必须以 `.db`、`.sqlite` 或 `.sqlite3` 结尾。首次启动设置的管理员账号密码才会生效，已有数据库不会因重新传入启动参数而重置密码。

## 备份与恢复

在保存了安装脚本的目录执行：

```bash
sudo bash install.sh backup
```

脚本会暂停正在运行的主控、导出带时间标识的备份，并恢复原来的运行状态。成功后会打印备份路径。将备份复制到其他机器前，可以单独检查：

```bash
sudo /usr/local/bin/vibemonitor validate-data /path/to/backup.db
```

恢复命令：

```bash
sudo bash install.sh restore /path/to/backup.db
```

恢复会替换当前账号、Token 和监控数据。脚本先验证备份，再停服并备份当前数据库；若恢复后原本运行的服务无法启动，会尝试恢复之前的数据库。请保留输出中的恢复前备份路径，并检查服务日志和页面是否正常。

手动部署也可直接使用 CLI：

```text
vibemonitor export-data DATA_PATH OUTPUT
vibemonitor restore-data BACKUP DATA_PATH
```

这两个操作前都应停止主控。导出目标必须不存在或为空文件，且父目录已存在。恢复前先导出当前数据库；确认命令成功后再启动服务，失败时检查日志并保留备份。

建议在升级、恢复和批量修改节点前做备份，并定期在独立目录验证恢复流程。数据库快照通常每 15 秒保存，正常退出会保存剩余状态；异常终止可能丢失尚未保存的状态。节点和历史恢复后，检查探针是否重新上线。

## 探针参数

探针安装命令可从“节点管理 → 编辑现有节点 → 显示安装命令”获取。手动运行支持以下参数：

| 参数 | 环境变量 | 含义 |
| --- | --- | --- |
| `--server` / `-s` | `VIBEMONITOR_SERVER` | 主控最终访问 URL，例如 `https://monitor.example.com` |
| `--token` / `-t` | `VIBEMONITOR_TOKEN` | 该节点的通信 Token |
| `--interval` / `-i` | `VIBEMONITOR_INTERVAL` | 上报间隔，默认 `1s`，必须大于 0 且不超过 `1h` |
| `--interfaces` | `VIBEMONITOR_INTERFACES` | 逗号分隔的网卡名；留空自动选择 |
| 无对应命令行参数 | `VIBEMONITOR_DISK_PATH` | 采集该路径所在文件系统的容量，默认 `/` |

命令行参数优先于同名环境变量。显式选择的网卡不存在时，采集会报错；多网卡、桥接或隧道环境可指定实际承载流量的网卡，并对照系统计数检查，避免重复统计。硬盘指标对应一个文件系统，并非所有磁盘容量之和。

systemd 服务可通过 `sudo systemctl edit vibemonitor-agent` 添加环境配置，例如：

```ini
[Service]
Environment="VIBEMONITOR_INTERFACES=eth0"
Environment="VIBEMONITOR_DISK_PATH=/data"
```

保存后执行 `sudo systemctl daemon-reload` 和 `sudo systemctl restart vibemonitor-agent`。如果要改变上报间隔，先用 `systemctl cat vibemonitor-agent` 检查 `ExecStart`：安装器会显式传入间隔，此时只改环境变量不会覆盖它。

## 流量账期规则

- 界面中的流量配额按 `1 GiB = 1024³ bytes` 换算；配额为 0 表示不限制。
- 账期与每日流量统一使用 UTC+8；每月重置日可选 1–31，空白或 API 的 0 表示 1 日。短月份将重置日钳制到月底。
- 累计流量采用上传与下载之和；跨日采样按时间分摊。未采集的数据无法恢复，历史图中按 0 计入。
- 日流量只记录采集值，不包含手动补录和校正。
- 修改重置日后，如果当前账期起点改变，会按新账期已有日流量立即重算累计值，并清除旧账期手动补录。缺少历史采样的部分按 0 计算，可同时填写“当前周期已用流量”进行校正。
- 如果账期起点不变（例如 2 月底将 30 日改为 31 日），保留当前用量和校正值。
- “当前周期已用流量”留空表示不校正，填写 0 表示清零。只改节点名称等信息不会重写未修改的配额。

## 排行榜统计口径

网站标题菜单中的「排行榜」对访客开放。日榜、周榜、月榜分别统计北京时间今天、本周（周一开始）、本月至今，每项显示前三个节点。离线节点仍可凭时段内已有记录参与排名；缺少对应记录的节点不参与，数值为零的有效记录正常计入。

- 连接数量：TCP 与 UDP 在用数量之和的采样平均值。
- 系统负载：系统 1 分钟平均负载（Load 1）的采样平均值，不是 CPU 使用率。
- 内存占用：内存使用率的采样平均值。
- 建连速度：TCP 建连尝试数/秒的采样平均值，与概览中的连接速率一致。
- 流量：所选自然日、周、月内上传与下载的累计采集量，不包含手动补录或校正，与概览按各节点账期汇总的「周期流量」口径不同。

资源样本按现有一分钟间隔保存，平均值按有效采样数量计算；小时汇总按原始样本数加权。连接数量、系统负载与建连速度的历史从升级后开始记录，旧数据库的缺失历史不会补成零；已有内存和日流量记录可直接参与排名。此次变更只需更新主控，已有支持这些字段的探针无需更新。

## 反向代理与排查

填写域名安装时，脚本配置 HTTPS，并让主控监听本机回环地址；未填写域名时直接监听配置端口。探针地址应使用实际可访问的主控 URL。

非本机反向代理可通过主控 `--trusted-proxy` / `VIBEMONITOR_TRUSTED_PROXY` 指定可信的代理 IP 或 CIDR，逗号分隔。仅填写实际代理地址；该设置决定是否信任客户端 IP 和 HTTPS 转发头。本机回环代理默认受信任。

| 现象 | 检查内容 |
| --- | --- |
| 探针离线 | 探针服务日志、主控 URL、节点 Token、指定网卡是否存在 |
| 页面数据过期 | 浏览器网络请求、`/api/clients` WebSocket、反向代理配置和主控日志 |
| 历史缺失或保存失败 | 主控日志中的 `[Store]` 错误、数据库目录权限、磁盘剩余空间 |
| Telegram 告警失败 | 管理界面测试功能、Bot Token、Chat ID 和主控到 Telegram 的网络 |
| 自定义主题异常 | 用 `?theme=default` 打开首页，再在主题管理中切回默认主题 |

## 开发与验证

完整运行环境为 Linux amd64/arm64，Go 版本以 `go.mod` 为准。前端测试依赖以 `scripts/package-lock.json` 为准，CI 使用 Node 22。

```bash
go vet ./...
go test -race ./...
npm ci --prefix scripts --ignore-scripts --no-audit --no-fund
npm test --prefix scripts
bash -n install.sh
bash scripts/check_installer_health.sh
bash scripts/check_installer_rollback.sh
make release-all
```

macOS 上可以运行独立的存储、服务端和纯逻辑测试，但不会执行带 Linux build tag 的采集和探针代码。Linux CI 同时覆盖 amd64 和 arm64。测试源码应随项目提交，`.gitignore` 只排除测试生成物，不排除测试文件。

后端 `internal/server/app.go` 负责服务生命周期和路由组装，认证、节点、历史、设置与 Telegram 路由分别位于 `*_routes.go`，压缩中间件在 `compression.go`。

前端直接发布 `internal/web/dist` 中的文件，无额外打包步骤。`index.html` 按顺序加载共享状态与渲染（`app.js`）、认证、设置、节点管理、Ping 历史和资源历史，最后由 `bootstrap.js` 启动请求与定时器。这些经典脚本共享页面作用域，新增跨模块调用时需保持加载顺序。DOM 回归测试从 HTML 中的 `data-dashboard-module` 脚本列表加载相同模块，并覆盖五种内置主题。
