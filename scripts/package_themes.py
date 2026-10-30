#!/usr/bin/env python3
"""Build reproducible ZIPs accepted by VibeMonitor's CSS-only theme importer."""
import json
from pathlib import Path
from zipfile import ZipFile, ZipInfo, ZIP_DEFLATED

root = Path(__file__).resolve().parents[1]
web = root / "internal/web/dist"
notices = (web / "third-party-notices.txt").read_text()
packages = [
    ("hex", "Hex · 原默认主题", "1.0.0", "玻璃卡片与地球概览", web / "themes.css"),
    ("rakugaki", "Rakugaki · 手绘纸感", "1.0.0", "米白纸面、墨线边框、手绘圆角与陶土橙", root / "themes/src/rakugaki.css"),
    ("win2000", "Win2000 · 经典桌面", "1.0.0", "蓝色桌面、灰色立体窗口与渐变标题栏", root / "themes/src/win2000.css"),
    ("design", "Design · 简约磨砂", "1.0.0", "暖白窗口、细线列表与柔和圆角", web / "design.css"),
    ("serverstatus", "ServerStatus · 紧凑表格", "1.3.0", "紧凑表格、斑马行与可展开详情", root / "themes/src/serverstatus.css"),
]
for slug, name, version, description, source in packages:
    manifest = dict(name=name, version=version, description=description, css="theme.css")
    css = "/*\n" + notices.replace("*/", "* /") + "\n*/\n" + source.read_text()
    with ZipFile(root / "themes" / f"{slug}.zip", "w") as archive:
        for filename, content in [("theme.json", json.dumps(manifest, ensure_ascii=False)), ("theme.css", css)]:
            entry = ZipInfo(filename, (2026, 1, 1, 0, 0, 0))
            entry.compress_type = ZIP_DEFLATED
            entry.external_attr = 0o100644 << 16
            archive.writestr(entry, content.encode())
    print(f"themes/{slug}.zip")
