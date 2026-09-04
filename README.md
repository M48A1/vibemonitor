# ⚡ VibeMonitor (精简版服务器监控)

> 极致轻量、安全纯粹、开箱即用的现代服务器探针监控系统。

---

## 🌟 为什么选择 VibeMonitor？

原版探针系统近期集成了较多重量级功能（如复杂多租户/OAuth、多数据库迁移、Goja JS 插件、Pprof、主题市场、以及用于远程控制的反弹 Shell/终端与文件管理器）。根据 2026 年安全研究报告（如 Huntress），默认的双向远程命令控制容易成为恶意滥用的高危入口（LOLRMM）。

**VibeMonitor 专注于最核心的探针监控与颜值展示**，进行了全面的重构与轻量化：

1. **纯粹安全 (Safe by Design)**：
   - 彻底剔除反弹 Shell (Terminal)、任意远程命令执行 (Exec)、远程文件上传下载管理 (Filemanager) 等危险后门功能。
   - 纯粹的单向性能指标采集与上报，安心部署，杜绝被黑客利用的隐患。
2. **彻底剔除插件系统与动态脚本引擎 (Zero Plugins / No JS Runtime)**：
   - 完全剔除重量级的 Goja JavaScript 动态脚本解释器、插件市场、分块 ZIP 插件上传与动态路由挂载。
   - 杜绝任意动态第三方脚本在服务端执行带来的安全漏洞与性能开销，保障核心代码 100% 静态编译、不可篡改。
3. **剔除主题市场，仅保留单一高颜值无模糊主题 (Single Clean Theme)**：
   - 彻底移除了原版复杂且易出现路径穿透的主题市场、外部 `.tar.zst` 解包与动态主题加载机制。
   - 仅保留并内置现代卡片主题，并按需求**完全去除了毛玻璃与模糊效果**，改为高对比度、低资源开销的纯色平整卡片（原生支持 Dark/Light 切换），作为唯一默认内嵌仪表盘。
4. **去除多用户复杂度 (Single Admin)**：
   - 无需复杂的注册、OAuth、用户管理表与权限策略。
   - 统一由单一管理员密码管理（支持环境变量预设或首次启动自动生成）。访客直达大屏，管理员一键登录。
5. **零依赖单二进制 (Single Binary, Zero CGo)**：
   - 纯 Go 编写（无 CGo、无 GCC 编译依赖、无 Node.js/npm 运行时依赖）。
   - 现代响应式 Web 仪表盘**完全内嵌**在单一二进制文件中，二进制体积仅约 **6.5MB**！
6. **周期性账单日流量统计与配额告警 (Sum 双向计费)**：
   - 支持设置月流量配额（如 1000 GB）与每月账单重置日（如 15 号）。
   - **支持初始已用偏移**：添加/编辑节点时支持填入当前周期已用流量，自动以此为基准并叠加探针增量；一旦到达下个周期重置日，自动清零偏移并纯按探针采集数据统计剩余。
   - 包含超额变色预警（60% 橙色预警，90% 红色告警）与重启容灾增量统计。
7. **极致轻量，极低资源占用**：
   - 服务端 (Master) 内存占用仅 **10~15MB**。
   - 探针客户端 (Agent) 内存占用 **< 5MB**。
8. **高度兼容 v2 协议**：
   - 完全兼容 v2 JSON-RPC 上报协议（如 `agent.report` / `agent.basicInfo`），不仅可以使用自带轻量 Agent，第三方客户端或测试脚本亦可无缝对接。
9. **自定义延迟监测模块 (Custom Ping Latency Monitor)**：
   - **无需内置硬编码测速点**：管理员可在管理面板“⚙️ 站点设置”中自由添加/修改任意测速目标（每行一个 `名称,IP或域名:端口`，例如：`上海电信,180.153.28.1:80`、`谷歌DNS,8.8.8.8`）。
   - **自动调度与安全探测**：探针端自动同步测速目标列表并在后台多协程轻量测量端到端往返 RTT（纯 Go 原生 TCP/ICMP 探测，无需 root/raw-socket 权限）。
   - **大屏实时卡片胶囊呈现**：节点卡片直观显示各测速点延迟（`<150ms` 绿胶囊、`150~250ms` 黄胶囊、`>250ms` 或超时红胶囊）。

---

## 🚀 快速上手

### 1. 启动服务端 (Master Server)

直接下载或编译二进制，一条命令即可启动：

```bash
# 启动服务端（默认监听 0.0.0.0:25774）
./vibemonitor server
```

> 首次启动时，若未指定密码，控制台会自动打印生成的专属管理员密码：
> ```text
> =====================================================
>  [INITIAL SETUP] Generated Admin Password: a1b2c3d4e5f6
>  Please save this password to login to the dashboard!
> =====================================================
> ```
> 可以在浏览器打开 `http://你的IP:25774`，点击右上角 **⚙️ 管理** 输入该密码进入管理面板。

你也可以在启动时自定义密码与监听端口：
```bash
./vibemonitor server --listen 0.0.0.0:25774 --admin-password "YourPassword123"
```

---

### 2. 接入被监控节点 (Agent)

在 Web 仪表盘点击 **⚙️ 管理** 登录后，点击 **➕ 添加节点**：
- 输入节点名称（如 `Tokyo-VPS-01`）与地区（如 `JP`）。
- 创建后系统将生成专属通信 Token，并自动展示一行复制命令。

#### 方式 A：Linux VPS 一键命令 (推荐)
在被监控的远程 Linux VPS 上，以 root 权限执行：
```bash
curl -fsSL http://<你的主控IP>:25774/install.sh?token=<节点TOKEN> | bash
```
> 该脚本会自动配置 Systemd 服务并开机自启后台常驻。

#### 方式 B：单二进制直接运行
```bash
./vibemonitor agent --server http://<你的主控IP>:25774 --token <节点TOKEN>
```

---

## 🐳 Docker / Docker Compose 部署

项目包含极简的多阶段 Dockerfile 与 Compose 配置：

```bash
# 一键启动服务端
docker compose up -d
```

`docker-compose.yml` 示例：
```yaml
version: '3.8'

services:
  vibemonitor:
    build: .
    container_name: vibemonitor
    restart: always
    ports:
      - "25774:25774"
    volumes:
      - ./data:/app/data
    environment:
      - VIBEMONITOR_LISTEN=0.0.0.0:25774
      - VIBEMONITOR_DATA=/app/data/vibemonitor-data.json
      # 可选：自定义固定管理员密码
      # - VIBEMONITOR_ADMIN_PASSWORD=your_secure_password
```

---

## ⚙️ 命令行参数与环境变量

| 参数 | 环境变量 | 默认值 | 作用说明 |
| :--- | :--- | :--- | :--- |
| `--listen`, `-l` | `VIBEMONITOR_LISTEN` | `0.0.0.0:25774` | 服务端监听地址与端口 |
| `--data`, `-d` | `VIBEMONITOR_DATA` | `vibemonitor-data.json` | 本地数据持久化文件路径 |
| `--admin-password`, `-p` | `VIBEMONITOR_ADMIN_PASSWORD` | *(自动生成)* | 管理员认证密码（单管理员） |
| `--server`, `-s` | `VIBEMONITOR_SERVER` | 无 | (Agent 模式) 主控服务端地址 |
| `--token`, `-t` | `VIBEMONITOR_TOKEN` | 无 | (Agent 模式) 节点的通信密钥 |
| `--interval`, `-i` | `VIBEMONITOR_INTERVAL` | `3s` | (Agent 模式) 监控上报频率 |

---

## 💻 本地编译与交叉编译

本项目采用纯 Go 标准库与现代 WebSocket 模块开发，支持全平台一键跨平台交叉编译：

```bash
# 编译当前平台二进制
make build

# 运行全套单元测试与集成测试
make test

# 交叉编译全平台发行包 (Linux amd64/arm64, macOS, Windows)
make release-all
```

---

## 📊 架构设计

```
                    +-----------------------------+
                    |  Web 浏览器 / 手机移动端     |
                    | (卡片式实时仪表盘, WebSocket)|
                    +--------------▲--------------+
                                   │  HTTP / WS
                    +--------------▼--------------+
                    |         VibeMonitor         |
                    |     (Server 主控端)         |
                    |   - 极速原子 JSON 持久化     |
                    |   - 内存环形历史队列 (Spark) |
                    |   - 兼容 v2 JSON-RPC        |
                    +--------------▲--------------+
                                   │  POST /v2/rpc (安全指标流)
            +-----------------------+-----------------------+
            │                                               │
+-----------▼----------+                       +-----------▼----------+
|  VibeMonitor Agent   |                       |    第三方兼容 Agent   |
| (Linux/macOS/Win探针)|                       | (完全兼容 v2 协议上报) |
+----------------------+                       +----------------------+
```

## 📄 License
MIT License.
