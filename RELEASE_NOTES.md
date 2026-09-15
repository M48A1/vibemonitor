# VibeMonitor v1.0.45 — 新增 Linux ARM64 支持与测试目录隔离

本版本正式引入对 Linux ARM 架构的原生支持，并优化了仓库发布结构：

- **新增 Linux ARM64 (aarch64) 原生支持**：全面支持在 AWS Graviton、甲骨文 ARM 实例、各云厂商 ARM 服务器及树莓派 64 位系统上运行服务端与探针。
- **安装与发布脚本适配**：`install.sh` 自动识别 `x86_64` (AMD64) 与 `aarch64` (ARM64)，自动拉取对应架构的 64 位静态 ELF 二进制并进行严格的机器码完整性校验。
- **CI/CD 自动化构建**：GitHub Actions 自动构建发布 `vibemonitor-linux-amd64` 与 `vibemonitor-linux-arm64` 预编译二进制及其校验清单。
- **仓库测试文件隔离**：将本地测试套件目录加入忽略规则，避免非必要的测试文件污染版本库。

## 更新方式

先下载本次 Release 的新版 `install.sh`，再执行：
```bash
sudo bash install.sh update -p 1314
```
（端口请按实际部署修改）。更新前建议先执行 `sudo bash install.sh backup` 备份数据库。

