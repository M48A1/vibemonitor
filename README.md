# VibeMonitor

面向 Linux x86-64（Intel/AMD 64 位）的轻量服务器监控程序。主控服务、探针和内嵌网页共用一个 Go 二进制；不提供远程终端、文件管理、多用户或外部通知推送。

## 功能

- 采集 CPU、内存、Swap、磁盘、负载、网络速度、累计流量、连接数、进程数及运行时间。
- 通过 WebSocket 更新节点卡片，支持分组、搜索、地区、站点标题、公告和深浅色主题。
- 按上传＋下载累计月流量，支持配额、账单重置日、初始已用量和页面变色提醒。该统计是探针观测值，不等同于云厂商账单。
- 自定义 IPv4 或域名延迟目标；最多 64 个目标。`域名:端口` 使用 TCP 连接测时，无端口目标优先调用系统 `ping -4`，失败后尝试 TCP 80/443。页面提示实际测量方式；TCP 连接耗时与 ICMP 往返延迟不可直接混作同一指标。
- 目标名称必须唯一；探针只可上报已配置且地址匹配的目标，每次最多 64 条结果。删除目标、改名或更换地址时清理对应旧历史。
- 延迟历史每个目标约 60 秒取一个样本，保留最多 1440 点，提供 1 小时和 24 小时视图。页面丢失率按留存采样计算，并非连续抓包统计。曲线和统计仅使用最近一次采样对应的检测方式，TCP、ICMP 和旧探针未标明方式的样本不混算。首次升级时会清理缺少地址/方式记录、无法可靠归属的旧延迟历史，节点和流量数据保留。
- 默认每 3 秒上报。自带探针上报间隔支持大于零、最多 1 小时；离线阈值取 10 秒和上报间隔三倍中的较大值。未携带间隔字段的第三方探针仍使用 10 秒阈值。
- 单管理员登录；退出撤销当前会话，改密撤销所有会话。公开节点和 WebSocket 不包含节点密钥。

程序体积、内存占用与节点数量、目标数量、访问量、编译版本有关，项目不承诺固定资源占用。实际部署前请按自身规模测试。

## 安装要求

仅支持 **Linux x86-64 + systemd**，不支持 ARM、32 位 x86 或其他操作系统。安装器需要 root，以及 `curl`、`sha256sum`、`systemctl`、`mktemp`、`od`、`awk` 等常用工具。ICMP 测量需要系统提供 `ping`。

安装器从 GitHub Releases 下载固定版本的二进制和 SHA-256 清单，校验通过后原子替换。启动或主控健康检查失败会恢复旧二进制和旧服务配置。如果回退步骤也失败，会保留恢复目录中的旧程序和服务配置，并在错误输出中显示路径，需检查后手动恢复；不会自动删除恢复材料。探针的启动检查仅确认进程存活，是否成功连接主控请查看面板或日志。

**需要先发布包含 `vibemonitor-linux-amd64`、`install.sh`、`sha256sums.txt` 的新版 Release。** 仅推送源码不会更新服务器，也不会创建新版 Release。SHA-256 用于完整性校验，信任来源仍是该 GitHub 仓库及其发布权限。

下载并查看安装脚本后执行：

```bash
curl -4 -fsSL -o install.sh https://raw.githubusercontent.com/M48A1/vibemonitor/main/install.sh
bash install.sh
```

菜单从终端读取输入；无交互终端时必须使用以下子命令。

### 主控

```bash
bash install.sh server -p 1314
```

首次创建数据文件时自动生成密码，可用以下命令查看初始化日志：

```bash
journalctl -u vibemonitor-server -n 30
```

也可用 `-w '初始密码'` 指定首次密码。已有数据文件时保留已保存的密码；网页改密不会在重启后恢复旧密码。

### 探针

在管理页面添加节点并取得 Token，然后运行：

```bash
bash install.sh agent -s https://monitor.example.com -t YOUR_NODE_TOKEN -i 3s
```

管理员页面的动态接入命令会下载并校验 Release 中的同一个安装器，不再使用主控网站页面作为二进制备用下载。

旧动态安装器曾使用 `/opt/vibemonitor/vibemonitor` 和 `vibemonitor.service`；如从该方式迁移，确认新探针在线后停用旧探针服务，避免重复上报。不要停用同名的旧主控服务。

## HTTPS 部署

公网管理和探针连接应使用 HTTPS。直接访问 `http://IP:1314` 不提供传输加密。

以 Nginx 在同一台机器代理为例，先将主控监听地址改为 `127.0.0.1:1314`。使用 `systemctl edit vibemonitor-server` 设置：

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/vibemonitor server --listen 127.0.0.1:1314 --data /etc/vibemonitor/vibemonitor-data.json
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

默认主文件为 `/etc/vibemonitor/vibemonitor-data.json`，保存配置、节点资料及流量等状态；ping 历史与最近测试结果单独保存在同目录的 `vibemonitor-data.json.ping.json`。自定义 `--data` 时，ping 文件名为该路径追加 `.ping.json`。旧版内嵌的有效 ping 数据会自动迁移。两个文件通过摘要匹配；不匹配的 ping 文件不会载入，避免恢复配置后混入其他历史。配置和节点管理修改保存成功后才返回成功；失败会保留原配置。指标在内存接收后定期保存（约 15 秒），新延迟采样还会触发保存；后台写入失败会记日志并重试。进程崩溃可能丢失尚未落盘的近期指标。

```bash
bash install.sh backup
bash install.sh restore /etc/vibemonitor/backups/data-YYYYMMDD-HHMMSS.XXXXXX
```

备份会短暂停止正在运行的标准主控服务，以完成退出保存，然后复制主文件及其 `.ping.json` 配套文件并重新启动。转移备份时请保留两个文件的名称和相邻位置；只有主文件的旧备份仍可恢复，延迟数据会重新采集。恢复前验证文件结构、备份当前数据，恢复后若服务启动失败则回退到恢复前数据。标准服务名为 `vibemonitor-server`；自定义服务名或数据路径的手动部署需要自行停服并备份实际文件。

备份和数据文件包含管理员密码及节点密钥，应仅供管理员读取。恢复也会恢复备份时的密码和节点密钥。安装/更新和卸载前需输入 `yes` 二次确认，并删除专用 `backups` 目录中的所有旧备份（包括 ping 配套文件），不会自动建立新备份；服务端安装/重装和卸载会删除整个配置目录中的配置、账号、节点及监控数据；探针安装仍仅清理备份。需要留存的备份请提前复制到其他位置。备份保存在本机，应另行复制到其他机器，并自行制定保留期限。

```bash
bash install.sh status
bash install.sh restart
bash install.sh uninstall
```

卸载删除全部主控配置、账号、节点、监控数据和备份。更新安装器会重写标准 unit；服务端清空重装会删除旧服务的 drop-in 配置。若 drop-in 改了监听端口，请为更新命令传入对应的 `-p` 端口，以便健康检查。

## 编译与测试

```bash
make build
make release-all
```

均只编译 Linux amd64。可在 Mac 上交叉编译，但不能在 Mac 上运行产物。版本号来自 Git 标签或提交；Release 使用标签和提交哈希，可通过 `vibemonitor version` 或 `/api/version` 查询。

在 Linux x86-64 上运行完整测试：

```bash
go test -race ./...
node --test internal/web/app_test.cjs
python3 -m unittest discover -s tests -v
```

安装器测试使用临时目录和模拟网络、systemd，不会修改真实服务。GitHub Actions 在 main 推送和 PR 时执行这些检查；推送 `v*` 标签会测试、编译并创建 Release。

## 参数

| 参数 | 环境变量 | 默认值 |
| --- | --- | --- |
| `--listen`, `-l` | `VIBEMONITOR_LISTEN` | `0.0.0.0:1314` |
| `--data`, `-d` | `VIBEMONITOR_DATA` | `vibemonitor-data.json` |
| `--admin-password`, `-p` | `VIBEMONITOR_ADMIN_PASSWORD` | 首次自动生成 |
| `--server`, `-s` | `VIBEMONITOR_SERVER` | 探针必填 |
| `--token`, `-t` | `VIBEMONITOR_TOKEN` | 探针必填 |
| `--interval`, `-i` | `VIBEMONITOR_INTERVAL` | `3s` |

`vibemonitor validate-data FILE` 只检查备份格式，不启动服务或修改文件。

### 管理员账号

系统仅有一个管理员账号。安装时必须填写账号和密码，空值或纯空格会提示重新输入，密码输入不回显。命令行安装必须提供 `-u 用户名 -w 密码`，缺少任一项会在清理数据前退出。

账号密码保存在 `/etc/vibemonitor/vibemonitor-data.json` 的 `config.admin_username` 和 `config.admin_password` 字段中，当前为明文保存，文件权限为 `0600`。手动指定 `--data` 时，以该路径为准。安装器还会在 `/etc/systemd/system/vibemonitor-server.service` 中保存初始化账号密码（权限 `0600`）；网页改密后，当前有效密码以 JSON 配置为准。备份主文件也包含账号密码，请勿公开分享。
