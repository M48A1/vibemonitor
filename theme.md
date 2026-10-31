### 默认主题与独立安装包

Design 是唯一内置默认主题。Hex（原默认）、Rakugaki、Win2000、Design 和 ServerStatus 的独立 ZIP 安装包位于 [themes](themes/README.md)。其他主题仅在上传后出现在主题管理中；旧内置主题选择在升级后回到 Design，已上传的自定义主题保留。

### 自定义主题包

点击网站名称 → **主题管理**，管理员登录后可上传 ZIP、选择主题并点击“启用所选主题”。主题对所有访客生效，存储在 SQLite 中，重启后保留。删除正在使用的自定义主题会恢复默认主题；默认主题不可删除。

ZIP 包最大 2MB，根目录包含 `theme.json` 和其中指定的一个 CSS 文件（最大 1MB，UTF-8）。窗口提供可直接上传的示例包。`theme.json` 示例：

```json
{"name":"海蓝主题","version":"1.0.0","description":"基于默认主题修改主色","css":"theme.css"}
```

`theme.css` 会替换 Design 样式层，在共享基础样式之后加载。表格主题可用 `:root { --vibemonitor-layout: serverstatus; }` 启用表格布局；其他主题使用卡片布局。可覆盖 CSS 变量和已有样式：

```css
:root {
  --primary: #0284c7;
  --border-focus: #0284c7;
  --background: #dceef5;
}
```

可点击查看履历的资源名称使用 `.metric-button .metric-name`，名称右侧的 `.metric-history-icon` 是折线图标提示；主题可覆盖这两个类来强化或弱化该提示。表格布局的 ServerStatus 使用独立的“CPU 历史”等按钮，不受这两条规则影响。

每个主题包仅包含说明和 CSS，不支持 JavaScript 或额外文件；图片可使用 CSS data URL。最多保存 32 个自定义主题，相同 CSS 再次上传会更新主题名称和说明。若主题影响管理入口，在页面网址加上 `?theme=default`（已有参数时使用 `&theme=default`）即可临时以默认外观打开，然后在主题管理中启用默认主题。

### 实时数据与性能

首页使用 `/api/nodes?view=dashboard` 和 `/api/clients?view=dashboard` 获取精简节点数据，省去未使用的短期 `history` 数组。CPU、内存、网速及延迟曲线仍由原来的历史接口提供。不带 `view=dashboard` 的 HTTP / WebSocket 接口继续返回完整字段，所有主题共用同一组接口。

节点响应按 UUID 固定排序，浏览器按原来的节点序号和名称展示。未变化的卡片复用已有 DOM；页面进入后台时停止卡片渲染及兜底轮询，返回前台后刷新最新状态。

周期保存先复制状态，再在状态锁外提交 SQLite；历史样本批量事务写入，失败保留重试，并在删除节点或修改测试目标时过滤失效样本。历史查询使用最多两个只读 WAL 连接，写入保持单连接。磁盘持续不可用时重试缓冲最多 65,536 条样本，达到上限后暂缓新历史采样，恢复写入后继续。
