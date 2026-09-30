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

