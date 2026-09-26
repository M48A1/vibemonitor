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
