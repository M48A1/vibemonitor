# VibeMonitor

面向 Linux x86-64 / ARM64（aarch64）的轻量服务器监控程序。

## 功能

- 节点状态、实时网速、高负载节点和内存紧张节点总览;
- 每个节点可单独设置CPU负载阈值；未设置阈值的节点不计入高负载统计;
- 账单周期与流量统计,首个周期可自定义已使用流量;
- 单用户管理员登录,点击网站标题进入管理菜单;
- 可添加,编辑或删除节点,并维护测速目标;
- 探针安装命令在「节点管理 → 编辑现有节点 → 显示安装命令」中获取;
- 无插件,不提供远程控制或文件管理;
- 安装脚本自动反代域名;
- 外部通信支持telegram bot报警;
- 节点按自定义顺序排序;
- 无主题管理,建议fork自己加;

## 界面预览

![VibeMonitor 监控面板预览](docs/dashboard-preview.png)

## 安装与更新

支持 Linux x86-64 / ARM64，使用 systemd。运行安装脚本后，选择 **1. 安装主控**；以后更新主控选择 **2. 更新主控**，会保留账号、节点和历史数据。

```bash
curl -4 -fsSL -o install.sh https://raw.githubusercontent.com/M48A1/vibemonitor/main/install.sh && bash install.sh
```

安装主控时填写域名，脚本会配置 Nginx HTTP 反向代理、申请 HTTPS 证书并启用自动续期。留空域名时使用原有的直接端口访问方式。探针填写最终的主控访问地址。

## 安全与反向代理

管理员登录会设置最长有效期为 7 天的 session Cookie。session 仅保存在主控进程内存中，因此每次重启或更新主控后都需要重新登录；修改管理员密码也会注销全部 session。

同机 Nginx/Caddy 从 loopback 地址转发时，主控会识别其 `X-Forwarded-Proto: https`。反代位于其他机器、Docker 网络或 K8s Pod 时，使用 `--trusted-proxy` 或 `VIBEMONITOR_TRUSTED_PROXY` 指定**直接连接主控的代理** IP 或 CIDR（多个条目用逗号分隔），例如 `VIBEMONITOR_TRUSTED_PROXY=172.18.0.5/32`。主控只接受这些来源的 `X-Forwarded-Proto`，代理必须覆盖客户端传入的同名请求头，并限制客户端直接访问主控端口。配置错误会使主控启动失败。

节点安装命令包含通信 token，`/install.sh?token=...` 响应也会嵌入 token。跨机器安装探针时请使用 HTTPS 主控地址，避免 token 在 HTTP 传输中暴露。

源码构建 Linux ARM64 二进制可运行 `make build-arm64`；`make release-all` 会同时构建 AMD64 和 ARM64。
