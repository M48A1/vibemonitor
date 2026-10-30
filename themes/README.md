# VibeMonitor theme packages

Design is the only built-in default. Other appearances are available as optional ZIP packages:

| Package | Appearance |
| --- | --- |
| [hex.zip](hex.zip) | Original glass cards and globe |
| [rakugaki.zip](rakugaki.zip) | Hand-drawn paper |
| [win2000.zip](win2000.zip) | Classic Windows 2000 desktop |
| [design.zip](design.zip) | Minimal frosted Design appearance |
| [serverstatus.zip](serverstatus.zip) | Compact expandable server table |

Open a ZIP on GitHub and choose **Download raw file**, then upload it through **Theme management → Upload theme package → Enable selected theme**. Do not unzip the file before uploading.

Each package contains `theme.json` and `theme.css`; license notices are included as a CSS comment. Uploaded themes and the selected theme remain in the database across updates. Old built-in selections switch to Design after upgrading; upload their ZIP to use them again.

Rebuild the packages after changing their styles:

```sh
python3 scripts/package_themes.py
```

Custom styles replace the Design overlay and load on top of the shared base styles. A table theme can select the existing ServerStatus renderer with `:root { --vibemonitor-layout: serverstatus; }`. Omit this property for the standard cards. No JavaScript is allowed in theme packages.

Two shared behaviours matter when a theme restyles dialogs. `.modal-overlay` paints `--overlay-bg` behind the dialog, and `.modal-header` sticks to the top of a scrolling dialog while painting its own background (the base rule composites `--card-bg` over `--background`, which keeps glass themes opaque). Override `--overlay-bg` for the scrim; if you restyle `.modal-header` margins, keep its `top` offset in sync so the header still pins flush to the dialog edge.
