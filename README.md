# VibeMonitor

面向 Linux x86-64 / ARM64（aarch64）的轻量服务器监控程序。

## 功能

- 节点状态、实时网速、高负载节点和内存紧张节点总览；内存使用率达到 85% 的在线节点计入内存紧张统计
- 每个节点可单独设置 CPU 负载阈值；未设置阈值的节点不计入高负载统计
- 账单周期与流量统计，首个周期可自定义已使用流量
- 单用户管理员登录，点击网站标题进入管理菜单；可添加、编辑或删除节点，并维护测速目标
- 探针安装命令在「节点管理 → 编辑现有节点 → 显示安装命令」中获取
- 无插件，不提供远程控制或文件管理；可选用 Telegram Bot 接收告警
- 安装脚本自动反代域名

## Telegram 告警

在 Telegram 中通过 [@BotFather](https://t.me/BotFather) 创建 Bot，并向 Bot 发送一条消息，或将 Bot 加入要接收告警的群组/频道。私聊发送 `/start` 后，可调用 Telegram Bot API 的 `getUpdates`，读取 `result[].message.chat.id` 获取 Chat ID。在面板的「外部通信设置 → Telegram 告警」填写 Bot Token 和 Chat ID，勾选启用，点击「保存 Telegram 配置」，再点击「发送测试消息」。Chat ID 支持数字 ID 或公开频道的 `@用户名`；Bot 必须有向目标发送消息的权限。

主控会在节点已离线且距最后上报达到可配置的秒数后发出告警；已发离线告警的节点恢复在线时发送恢复消息。节点 CPU 使用率达到该节点设置的 CPU 阈值、内存使用率达到 85%、本周期流量达到节点额度时，各发送一次告警；指标回落后再次越过阈值才会重发。未设置 CPU 阈值或流量额度的节点不会触发对应告警。可在节点编辑页单独关闭该节点的全部 Telegram 告警。

填写节点到期日后，还可设置提前 1–7 天、指定时区与小时的到期提醒；提醒窗口内每天最多发送一次，设为 0 天可关闭。告警去重状态保存在 SQLite 中，主控重启和备份恢复后仍会保留。Telegram 不可用时会在后续检查中重试尚未成功发送的告警。

Bot Token 存储在主控数据库及其备份中；管理接口仅返回是否已配置，不返回 Token。请保护数据库、备份和 Token。主控需要能访问 `api.telegram.org:443`。

## 界面预览

![VibeMonitor 监控面板预览](docs/dashboard-preview.png)

## 安装与更新

支持 Linux x86-64 / ARM64，使用 systemd。运行安装脚本后，选择 **1. 安装主控**；以后更新主控选择 **2. 更新主控**，会保留账号、节点和历史数据。

```bash
curl -4 -fsSL -o install.sh https://raw.githubusercontent.com/M48A1/vibemonitor/main/install.sh && bash install.sh
```

安装主控时填写域名，脚本会配置 Nginx HTTP 反向代理、申请 HTTPS 证书并启用自动续期。留空域名时使用原有的直接端口访问方式。探针填写最终的主控访问地址。

## 域名与 HTTPS

配置域名前，请将域名的 **A 记录**解析到主控服务器，暂不设置 AAAA 记录，并确保公网 **80 和 443 端口**可访问。安装时可选填证书通知邮箱。配置完成后通过 `https://monitor.example.com` 访问；启用域名的主控仅监听本机地址。

已有主控选择菜单 **3. 设置 / 更换访问域名与 HTTPS**。菜单会显示当前域名并读取主控监听端口；更换域名会保留账号、节点和历史数据。切换成功后，请将各探针的主控地址改为新域名。

也可以使用命令行：

```bash
# 给已有主控设置或更换域名；脚本通常会自动识别监听端口
bash install.sh domain -d monitor.example.com
# 需要指定端口或证书通知邮箱时，可加上 -p 1314 或 -e admin@example.com

# 安装新主控时指定域名（还需填写管理员账号和密码）
bash install.sh server -u admin -w 'your-password' -d monitor.example.com
```

脚本通过 `apt-get`、`dnf` 或 `yum` 安装 Nginx 和 Certbot。申请证书时，域名的 HTTP 验证路径必须能从公网访问。若接管旧版手工反代配置，脚本会将原文件保存为 `.before-vibemonitor.bak`。

如果脚本提示「HTTP 验证路径不可用」，请检查 `nginx -T` 是否加载了 `/etc/nginx/conf.d/vibemonitor.conf`、是否有同域名的旧站点，以及 `ss -lntp '( sport = :80 )'` 显示的 80 端口监听服务。域名的公网 80 端口也必须转发到这台服务器的 Nginx。脚本会在失败时恢复原有站点配置。

更换域名后，旧证书仍保留在 Certbot 中。确认旧证书不再被使用后，选择菜单 **12. 删除旧域名证书**，再选择证书并输入名称确认。该列表可能包含同一服务器上其他站点的证书；当前域名证书不会列出，脚本还会检查 Nginx 和常见服务配置中的引用。删除前仍需自行确认其他程序没有使用该证书。

动态探针安装链接包含节点 Token，请勿公开分享。
