# 运维与开发指南

## 安装位置与服务

通过 `install.sh` 安装时使用以下路径；手动启动可用 `--data` 指定其他数据库位置。

| 内容 | 默认位置或服务名 |
| --- | --- |
| 可执行文件 | `/usr/local/bin/vibemonitor` |
| 主控数据库 | `/etc/vibemonitor/vibemonitor-data.db` |
| 主控服务 | `vibemonitor-server.service` |
| 探针服务 | `vibemonitor-agent.service` |
| Nginx 配置 | `/etc/nginx/conf.d/vibemonitor.conf` |

数据库保存账号密码哈希、节点 Token、Telegram 配置、主题、图标和历史数据。应限制数据库文件的访问权限。SQLite 工作时可能生成同名的 `-wal`、`-shm` 文件。

```bash
sudo systemctl status vibemonitor-server
sudo journalctl -u vibemonitor-server -n 100 --no-pager
sudo journalctl -u vibemonitor-agent -n 100 --no-pager
```

手动启动主控时，未指定 `--data` 的默认路径是当前工作目录下的 `vibemonitor-data.db`。数据文件必须以 `.db`、`.sqlite` 或 `.sqlite3` 结尾。首次启动设置的管理员账号密码才会生效，已有数据库不会因重新传入启动参数而重置密码。

## 数据库结构与版本

数据库结构版本记录在 SQLite 的 `PRAGMA user_version` 中，定义见 `internal/store/migrations.go`。启动时按顺序执行版本号更高的迁移，每步成功后立即写回版本号；迁移只运行一次，之后启动不再重复。

迁移是尽力而为：只读磁盘或空间不足导致失败时只记录日志，不阻止服务启动，也不写入版本号，下次启动自动重试。启动日志出现 `[Store] Schema upgraded to vN` 表示本次执行了迁移。

v1 迁移删除 `ping_history` 上早期版本重复创建的 `idx_ping_lookup` 与 `idx_ping_series`，并在空闲页超过 8MB 时执行一次 `VACUUM` 回收文件空间。删除索引后所有查询仍按 `idx_ping_unique` 有序返回，数据行不变。迁移是一次性的启动开销，体积较大的库会看到磁盘占用明显下降（实测 50 万行样本自 72MB 降至 38MB）。只有确实删除了索引的库才会执行 `VACUUM`，未曾创建过这两个索引的库升级时不会重写文件。

回退到旧版本时，旧代码会重新创建这两个索引，不影响使用；只是再次升级时 v1 已记录为完成，不会重复迁移。如确需再次清理，可手动执行 `DROP INDEX idx_ping_lookup; DROP INDEX idx_ping_series; VACUUM;`。

## 数据写入

探针上报先更新内存状态并将历史样本加入有界队列，不等待历史数据写盘。后台通常每秒批量提交历史样本，配置及节点快照仍每 15 秒保存；数据库繁忙或执行历史维护时可能延后。历史查询以已提交数据为准，实时状态仍随上报更新。

写入失败保留队列重试，最多积压 65,536 条历史样本，满额时暂缓新历史采样。正常退出会保存待写数据；断电或强制结束进程可能丢失尚未写入的样本和状态。

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

完整运行环境为 Linux amd64/arm64，Go 版本以 `go.mod` 为准。GitHub Actions 检查 Go、前端 JavaScript 和安装脚本，并构建两个 Linux 架构的发布产物；JavaScript 语法检查使用 Node 22。

```bash
go vet ./...
for file in internal/web/dist/*.js; do node --check "$file"; done
bash -n install.sh
make release-all
```

测试源码、测试脚本、测试专用依赖和测试工作流仅在本地保留，由 `.gitignore` 排除，不再随仓库或发布源码分发。已有本地测试文件时仍可运行 `go test -race ./...`；全新的检出不包含这些测试。

发布时更新 `RELEASE_VERSION` 和 `RELEASE_NOTES.md`，推送对应的 `vX.Y.Z` tag。发布工作流由 tag 触发，生成 GitHub Release 及附件。

后端 `internal/server/app.go` 负责服务生命周期和路由组装，认证、节点、历史、设置与 Telegram 路由分别位于 `*_routes.go`，压缩中间件在 `compression.go`。

数据库访问约定：凡是由 HTTP 请求、探针上报或后台循环触发的读写，都接收调用方的 `context.Context`（`store` 包中的 `*Context` 方法），客户端断开或服务关闭会中止对应查询。只有启动阶段的工作（建库、迁移、加载快照）不带 ctx。新增数据库调用时请沿用该约定，不要退回 `context.Background()` 包装。

前端直接发布 `internal/web/dist` 中的文件，无额外打包步骤。`index.html` 按顺序加载共享状态与渲染（`app.js`）、认证、设置、节点管理、Ping 历史和资源历史，最后由 `bootstrap.js` 启动请求与定时器。这些经典脚本共享页面作用域，新增跨模块调用时需保持加载顺序。本地保留的 DOM 回归测试从 HTML 中的 `data-dashboard-module` 脚本列表加载相同模块，并覆盖五种内置主题。

### Ping 探测时间与升级兼容

从 v1.0.96 起，每个 Ping 结果携带 `measured_at`（探测完成时的 Unix 秒），与探针上报时间分开。探针重复上报缓存结果时保留原探测时间；历史数据沿用 60 秒采样间隔，但以探测时间采样，不再把缓存结果写成新的历史点。

全部测试目标超时的 Telegram 告警要求每个目标都有有效、未过期的探测结果。连续失败的计时由新的探测时间推进，单次失败被反复上报不会满足告警延迟。超过 120 秒、缺失时间或超过主控时间 5 秒的结果视为未知，不触发 Ping 离线或恢复通知；未知期间重置未完成的超时计时。通知展示探测时间（多目标取最早值）。请保持主控与探针系统时间同步。

本次需要同时升级主控与探针。旧探针仍可上报资源数据，Ping 历史暂按接收时间兼容记录，但不参与 Ping 离线/恢复告警；主控按心跳判断的节点离线/上线通知保持正常。

## ASN 与地区信息刷新

探针启动（包括安装、更新后由 systemd 重启）时立即查询 ASN 与 YouTube Premium 地区；页面保留 Google 图标，分别展示 IPv4 和 IPv6 的英文地区。正常情况下每 7 天刷新。主控每次启动生成新的刷新标识，通过探针正常上报的响应通知各节点重新查询，同一标识只触发一次。查询在独立后台任务中进行，不阻塞指标采样；临时失败会在 1 分钟和 4 分钟后重试，之后恢复每周检测。仅 IPv6 不可用不会触发重试。

首次启用该机制需同时升级主控和探针；旧探针不会识别主控的刷新通知。主控升级无法替代远端探针程序升级。无有效地区结果时显示 `Unknown`。

地区显示按地址族区分：Agent 通过本地路由的源地址选择判断 IPv4/IPv6
是否可用（不发送探测数据包）。确认没有对应出口地址时隐藏该地区项；
有出口地址但服务地区查询失败时显示 `Unknown`。旧 Agent 未上报可用性时
保留 `Unknown`，升级 Agent 后生效。

ASN 查询参考 [xykt/IPQuality](https://github.com/xykt/IPQuality/blob/main/ip.sh)：先获取节点直连 IPv4，再请求 `ipinfo.check.place/{IP}?lang=en` 的 MaxMind ASN 编号及机构名；失败时使用 `ipinfo.io/widget/demo/{IP}` 的 IPinfo 数据。两个源都失败时显示 `Unknown`，保持每周及启动/更新后的刷新机制。
