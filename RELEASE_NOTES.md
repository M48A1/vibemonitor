# VibeMonitor v1.0.46 — 主题管理健壮性修复

本版本针对主题管理系统进行了多项防御性修复，无功能变更，向下完全兼容。

- **修复 HTML 主题注入静默失败**：`embed.go` 现在在服务启动时通过 `init()` 校验 `index.html` 占位符是否存在，模板结构若发生变化会立即 panic 而非无声地返回错误主题；提取 `htmlPlaceholder` 常量，运行时替换引用同一常量，彻底消除字符串拷贝不一致的风险。
- **前端主题白名单统一**：在 `embed.go` 中引入 `validThemes` map 和 `normaliseAppearance()` 函数作为包内唯一白名单出口，消除原先 `if theme != "hex"` 散落的重复判断，并在代码注释中标注需与后端 `store/settings.go` 同步。
- **修复设置表单 radio null 未防护**：管理员设置页提交时若 radio 未选中会抛 `TypeError`，现改用可选链 `?.value ?? 'default'` 安全回退。
- **WebSocket 广播 API 语义澄清**：将含义混乱的 `broadcastNodes(force ...bool)` 拆分为两个明确函数：`broadcastNodes()`（带去重，用于定时推送）和 `forceBroadcastNodes()`（跳过去重，用于设置变更后强制同步），彻底消除"无参数 = 强制"的反直觉行为。

## 更新方式

先下载本次 Release 的新版 `install.sh`，再执行：
```bash
sudo bash install.sh update -p 1314
```
（端口请按实际部署修改）。更新前建议先执行 `sudo bash install.sh backup` 备份数据库。
