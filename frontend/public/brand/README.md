# 知数品牌素材

2026-10-29 · 数据成章 · 正式矢量重绘版。视觉契约见项目 `docs/界面设计/UI设计规范.md` 第1.3节。

## 选用

| 场景 | 文件 |
| --- | --- |
| PPT、文档、页眉，浅色背景 | `zhishu-horizontal-primary.svg`，或同名透明PNG |
| PPT、文档，深色背景 | `zhishu-horizontal-dark.svg`，或同名透明PNG |
| PPT封面、品牌展示（含ZHISHU） | `zhishu-signature-primary.svg` / `zhishu-signature-dark.svg` |
| 居中封面、方形版面 | `zhishu-stacked-primary.svg` / `zhishu-stacked-dark.svg` |
| 独立透明图标 | `zhishu-mark-primary.svg` / `.png` |
| 黑白印刷、反白叠图 | 各布局的 `black` / `white` 版本 |
| 朱红单色 | 各布局的 `vermilion` 版本 |
| 浏览器标签 | `favicon.svg`、`favicon-16.png`、`favicon-32.png`、`favicon.ico` |
| iPhone/iPad主屏幕 | `apple-touch-icon.png`（180×180，不透明底） |
| 应用图标 | `icon-192.png`、`icon-512.png`，由 `site.webmanifest` 声明 |

每种布局提供primary、dark、black、white、vermilion五种配色，共20个独立SVG及20个透明PNG。独立图形PNG为1024×1024，组合PNG宽2400像素。ICO内含16、32、48、256像素图像；额外提供64、128、256像素PNG。网页共享组件使用 `zhishu-symbols.svg`，此文件是图形定义，不适合直接插入PPT。

SVG全部转为路径，无外部图片、字体、脚本或网络依赖。现代PowerPoint可直接插入SVG并保持矢量缩放；旧版软件使用PNG。这里提供的是PPT用素材，不包含演示文稿模板。PNG镂空与外围为真实透明，白色版本需放在深色背景上查看。

## 使用约定

- 使用等比缩放。图形四周建议留白至少图形边长的1/4；素材画布以紧凑裁切为主，排版时额外保留留白。
- 浅底朱红 `#AA3828`、墨黑 `#232422`；深底浅朱红 `#F0AD9B`、暖白 `#F0EEE6`。
- 不添加投影、渐变、纹理、描边，不把图形镂空填成白色。
- 最小常规图形高度24px；网页使用28px；16px仅用于favicon。
- 中文轮廓源于本机已安装的宋体粗体，拼音使用几何路径。未附带或分发字体文件；本素材交付不代表商标注册或对底层字体权利作出保证。

## 维护

`scripts/brand/geometry.json` 为轮廓源，`scripts/brand/build-assets.cjs` 统一生成派生资源。执行 `node scripts/brand/build-assets.cjs <已安装sharp的模块路径>`，不安装新依赖。修改后检查所有配色与布局，并同步UI契约。`index.html` 是离线素材预览，可直接打开；所有图片引用使用相对路径。
