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
- 探针仅回传
- 添加节点 / 编辑节点 / 删除节点
- 测速节点目标手动维护在节点信息内


  
## 安装
- 粘贴复制选择菜单内容，更新主控菜单里选择10
- 初次安装域名反代下面也有说明
```bash
curl -4 -fsSL -o install.sh https://raw.githubusercontent.com/M48A1/vibemonitor/main/install.sh && bash install.sh
```


# 以下有时间可以细看
## 安装要求

仅支持 **Linux (x86-64 / ARM64) + systemd**，不支持 32 位系统或其他非 Linux 操作系统。安装器需要 root，以及 `curl`、`sha256sum`、`systemctl`、`mktemp`、`od`、`awk` 等常用工具。ICMP 测量需要系统提供 `ping`。

安装器从 GitHub Releases 下载固定版本的二进制和 SHA-256 清单，校验通过后原子替换。启动或主控健康检查失败会恢复旧二进制和旧服务配置。如果回退步骤也失败，会保留恢复目录中的旧程序和服务配置，并在错误输出中显示路径，需检查后手动恢复；不会自动删除恢复材料。探针的启动检查仅确认进程存活，是否成功连接主控请查看面板或日志。

**需要先发布包含 `vibemonitor-linux-amd64`、`vibemonitor-linux-arm64`、`install.sh`、`sha256sums.txt` 的新版 Release。** 仅推送源码不会更新服务器，也不会创建新版 Release。SHA-256 用于完整性校验，信任来源仍是该 GitHub 仓库及其发布权限。

下载并查看安装脚本后执行：

```bash
curl -4 -fsSL -o install.sh https://raw.githubusercontent.com/M48A1/vibemonitor/main/install.sh
bash install.sh
```

菜单从终端读取输入；无交互终端时必须使用以下子命令。

### 更新已安装的主控（保留数据）

下载新版安装器后使用 `update`，或在菜单选择 **10. 更新主控（保留全部数据）**：

```bash
curl -4 -fsSL -o install.sh https://github.com/M48A1/vibemonitor/releases/latest/download/install.sh
sudo bash install.sh update -p 1314
```

端口请填写现有主控实际监听端口。此命令替换程序并重启主控，保留账号、节点、配置、监控历史和备份；标准安装中旧的 `--data ...json` 参数会改为现有 `.db` 路径，其他 systemd 设置保留，无需重新填写密码和 Token。启动或健康检查失败时回退旧程序，不回退新程序启动后产生的数据变化。同机探针共用程序文件，重启探针后使用新版。

`update` 不执行下文安装/重装的清理步骤。`server` 仍是清空重装命令，请勿用它进行保留数据升级。

### 主控

```bash
bash install.sh server -p 1314 -u admin -w '请替换为强密码'
```

安装器要求显式填写账号密码。直接运行二进制、首次初始化且未指定密码时，会自动生成密码；systemd 部署可用以下命令查看初始化日志：

```bash
journalctl -u vibemonitor-server -n 30
```

直接运行二进制时，可用 `--admin-password '初始密码'` 指定首次密码。已有数据库时保留已保存的密码；网页改密不会在重启后恢复旧密码。安装器的 `server` 命令会清空重装，保留数据升级请用 `update`。

### 探针

在管理页面添加节点并取得 Token，然后运行：

```bash
bash install.sh agent -s https://monitor.example.com -t YOUR_NODE_TOKEN -i 3s
```

在目标机器上彻底卸载探针并删除本机 Token：

```bash
curl -4 -fL --progress-bar -o install.sh https://github.com/M48A1/vibemonitor/releases/latest/download/install.sh && bash install.sh agent-uninstall
```

执行后输入 `yes` 确认。该操作只清理目标机器上的探针服务、Token 和程序，不会删除服务端节点记录。

管理员页面的动态接入命令会下载并校验 Release 中的同一个安装器，不再使用主控网站页面作为二进制备用下载。

旧动态安装器曾使用 `/opt/vibemonitor/vibemonitor` 和 `vibemonitor.service`；如从该方式迁移，确认新探针在线后停用旧探针服务，避免重复上报。不要停用同名的旧主控服务。

## HTTPS 部署

公网管理和探针连接应使用 HTTPS。直接访问 `http://IP:1314` 不提供传输加密。

以 Nginx 在同一台机器代理为例，先将主控监听地址改为 `127.0.0.1:1314`。使用 `systemctl edit vibemonitor-server` 设置：

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/vibemonitor server --listen 127.0.0.1:1314 --data /etc/vibemonitor/vibemonitor-data.db
```

准备自己的域名及证书，将以下示例替换为实际域名与证书路径：

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
    client_max_body_size 1m;
    location / {
        proxy_pass http://127.0.0.1:1314;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 90s;
    }
}
```

主控只信任本机回环代理的 HTTPS 标记，用于安全 Cookie 和动态安装链接。自带探针的上报密钥仅放在 Authorization 请求头；兼容接口仍接受旧探针的查询参数密钥。动态安装链接本身仍含节点密钥，不要公开分享或保留到公开访问日志。

应用按直接连接地址限制登录尝试：每 5 分钟最多 10 次。同机反向代理下该额度由通过代理的用户共享；应用不会信任任意来源的转发 IP 来绕过限制。

## 数据、备份与恢复

程序仅使用 SQLite 文件存储，安装器默认路径为 `/etc/vibemonitor/vibemonitor-data.db`，直接运行默认路径为 `vibemonitor-data.db`。`--data` 接受 `.db`、`.sqlite` 或 `.sqlite3` 文件，不再进行 JSON 路径映射、导入、导出或迁移后清理。接口通信及数据库内结构化字段仍使用 JSON 序列化，它们不是独立 JSON 文件。

从 v1.0.40 升级前，请确认已有可用的 SQLite 数据库。标准安装的旧启动参数由升级脚本改为 `.db`；自定义 unit 或 drop-in 中的旧路径需先手动修改为实际数据库路径。尚未迁移的旧用户请先使用 v1.0.40 完成迁移，再升级到 SQLite-only 版本。

配置、节点修改与相关历史清理在同一数据库事务中提交，失败会回滚。周期保存跳过未变化的节点和样本；指标约每 15 秒保存，新延迟样本即时写入。后台写入失败会记日志并重试仍在内存中的样本（每个目标最近 24 个）；进程崩溃可能丢失尚未落盘的近期数据。

```bash
bash install.sh backup
bash install.sh restore /etc/vibemonitor/backups/data-YYYYMMDD-HHMMSS.XXXXXX.db
```

备份会短暂停止正在运行的标准主控服务，完成退出保存后，使用 SQLite `VACUUM INTO` 生成单个 `.db` 快照，再启动服务。快照包含配置、节点、流量和全部留存延迟历史，包括已经提交到 WAL 的数据。备份临时文件通过 SQLite 完整性和数据格式校验后才会替换空的输出占位文件；不覆盖已有的非空备份。恢复先校验备份、停服并保存当前快照，再通过 SQLite 事务替换数据；启动失败时恢复原快照，回退失败时保留恢复材料并报告路径。标准服务名为 `vibemonitor-server`。

本版本仅接受 SQLite 备份。v1.0.40 导出的 JSON 备份不再支持，升级后请重新生成 `.db` 备份。手动部署或自定义服务需要先停服，再执行 `vibemonitor export-data DATA.db BACKUP.db` 或 `vibemonitor restore-data BACKUP.db DATA.db`，完成后重新启动。不要在运行中的 SQLite 数据库上仅复制 `.db` 而忽略 WAL。

上传的站点图标（最大 2 MiB）也存储在 SQLite 中，随新备份一起恢复。升级后首次启动会把数据库当前引用的旧 `site-icon.*` 图片导入数据库，原图片保留但不再用于日常读取；后续上传和删除均在数据库事务内完成。旧版 SQLite 备份仍可恢复，但其中没有图标图片内容，迁移到新机器时可能需要重新上传。外部图标链接只备份链接，不下载远端图片。

备份和数据文件包含管理员密码哈希及节点密钥，应仅供管理员读取。恢复也会恢复备份时的密码和节点密钥。安装/重装和卸载前需输入 `yes` 二次确认，并删除专用 `backups` 目录中的所有旧备份，不会自动建立新备份；服务端安装/重装和卸载会删除整个配置目录中的配置、账号、节点及监控数据；探针安装仍仅清理备份。需要留存的备份请提前复制到其他位置。备份保存在本机，应另行复制到其他机器，并自行制定保留期限。

```bash
bash install.sh status
bash install.sh restart
bash install.sh uninstall
```

卸载删除全部主控配置、账号、节点、监控数据和备份。保留数据更新只在需要时修正标准 unit 的旧数据路径；服务端清空重装会删除旧服务的 drop-in 配置。若 drop-in 改了监听端口，请为更新命令传入对应的 `-p` 端口，以便健康检查。

## 编译与测试

```bash
make build
make release-all
```

均编译 Linux amd64 与 arm64。可在 Mac 上交叉编译，但不能在 Mac 上运行产物。版本号来自 Git 标签或提交；Release 使用标签和提交哈希，可通过 `vibemonitor version` 或 `/api/version` 查询。

在 Linux (x86-64 或 ARM64) 上运行完整测试：

```bash
go test -race ./...
node --test internal/web/app_test.cjs
python3 -m unittest discover -s tests -v
```

安装器测试使用临时目录和模拟网络、systemd，不会修改真实服务。GitHub Actions 在 main 推送和 PR 时执行这些检查；推送 `v*` 标签会测试、编译并创建 Release。

## 参数

| 参数 | 环境变量 | 默认值 |
| --- | --- | --- |
| `--listen`, `-l` | `VIBEMONITOR_LISTEN` | `[::]:1314` |
| `--data`, `-d` | `VIBEMONITOR_DATA` | `vibemonitor-data.db` |
| `--admin-password`, `-p` | `VIBEMONITOR_ADMIN_PASSWORD` | 首次自动生成 |
| `--server`, `-s` | `VIBEMONITOR_SERVER` | 探针必填 |
| `--token`, `-t` | `VIBEMONITOR_TOKEN` | 探针必填 |
| `--interval`, `-i` | `VIBEMONITOR_INTERVAL` | `3s` |
| `--interfaces` | `VIBEMONITOR_INTERFACES` | 自动选择统计网卡 |

流量统计默认排除回环、常见容器/隧道接口、网桥、VLAN 子接口和 bond 从接口。复杂网络建议明确指定出口网卡，例如 `vibemonitor agent --server https://monitor.example.com --token YOUR_TOKEN --interfaces eth0`；多个接口使用逗号分隔，勿同时选择同一流量经过的物理接口与隧道接口。systemd 安装可在探针服务的 drop-in 中设置 `Environment="VIBEMONITOR_INTERFACES=eth0"`，然后重新加载并重启探针。接口不存在或读取失败时不提交零流量报告；统计接口集合变化时重新建立计数基线，保留已累计用量，不回算历史误差。请先更新主控，再更新或重启探针，以支持新基线标记。

探针基础信息在启动时上报，失败后会在实时指标恢复上报时继续重试；连接失败后补报，正常连接期间每 15 分钟刷新一次。

`vibemonitor validate-data BACKUP.db` 以只读方式检查 SQLite 完整性、表结构及配置和节点，不启动服务或修改数据库内容。

### 管理员账号

系统仅有一个管理员账号。安装时必须填写账号和密码，空值或纯空格会提示重新输入，密码输入不回显。命令行安装必须提供 `-u 用户名 -w 密码`，缺少任一项会在清理数据前退出。

管理员账号与 bcrypt 密码哈希保存在 SQLite 的 `config` 表中，数据库权限为 `0600`。仅支持 bcrypt 密码哈希；已移除旧明文密码兼容逻辑，哈希本身不能作为密码登录。安装器仍会在 `/etc/systemd/system/vibemonitor-server.service` 中保存初始化账号密码（权限 `0600`）；网页改密后，以数据库中的密码为准。数据库、备份和 unit 都应仅供管理员读取。

### 节点 TCP 测试与账单

添加或编辑节点时，可独立配置 TCP 测试目标，每行 `名称,地址:端口`，例如 `电信,example.com:443`。表单也接受 `https://example.com`（转为端口 443）、`http://example.com`（端口 80）或 `tcp://example.com:443`。这里只测 TCP 建连延迟，不测下载速度或 HTTP 响应内容。每个节点最多 64 个目标；留空表示该节点不测试。旧节点启动时会将原有全局目标复制为各节点的独立配置；未指定端口的旧地址迁移为 TCP 80 端口，原 ICMP 历史不混入新目标。之后仅在节点信息中维护。
节点卡片展示最近 24 个留存样本的延迟和成功/超时色块，卡片丢包比例按最近 24 个、且未超过 24 小时的同一检测方式样本计算；详情统计按所选时间范围计算。无数据时显示待采样，点击目标可查看 1 小时、24 小时、7 天或全部曲线。改变目标地址或删除目标会清理对应旧历史。
账单可设置到期日、月付/季付/年付、每期金额和币种；卡片显示价格与剩余天数，到期日按浏览器本地日期计算。账单信息在公开节点卡片显示，不包含自动扣费、自动续期或通知功能。配置与延迟历史均保存在 SQLite；不配置 TCP 目标也会保留账单信息。
