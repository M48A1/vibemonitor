# VibeMonitor v1.0.41 — SQLite-only

本版本适用于已完成 SQLite 迁移的部署。现有数据库格式保持兼容。

- 删除旧 JSON 文件导入、摘要校验、迁移后删除旧文件及备份目录的代码。
- 数据与备份统一使用 SQLite。备份生成包含 WAL 已提交内容的完整数据库快照，恢复直接在 SQLite 事务中复制数据。
- 删除 JSON 路径映射及旧明文密码兼容分支，只接受数据库路径和 bcrypt 密码哈希。
- 将依赖旧 JSON 临时文件的故障模拟改为真实 SQLite 写入失败测试。
- 标准安装的旧启动参数由更新脚本改为现有 .db 路径，失败时恢复原 unit 和程序；自定义 unit/drop-in 需先手动修改。
- 未迁移的部署须先使用 v1.0.40。旧 JSON 备份不再接受，升级后请重新生成 .db 备份。

网页、探针通信，以及 SQLite 内的结构化字段仍使用 JSON 序列化；程序不再读写独立 JSON 数据文件。

## 更新方式

先下载本次 Release 的新版 `install.sh`，再执行 `sudo bash install.sh update -p 1314`（端口按实际部署修改），让更新脚本同步修正标准服务的旧数据路径。不要仅替换二进制后继续使用指向 `.json` 的旧启动参数。

更新后执行 `sudo bash install.sh backup` 生成新的 SQLite 备份。`server` 命令为清空重装，请勿用于保留数据更新。
