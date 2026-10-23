# suga 品牌资源

- 符号：一条不断折行的线，折两次成 S —— 会话压缩、交接之后仍然继续，而不是断掉。
- 字标：Noto Sans SC（SIL OFL）Latin，wght 600，字距 −12/1000，已转曲（文件里只有路径，不依赖字体）。
- 配色：主色 `#12B5A5`（青绿），墨色 `#0E1A1D`；默认透明背景。
- 图标：实底圆角方 + 挖空的 S，专供 16–48px，缩放后仍能读出 S。

文件：

| 文件 | 用途 |
|---|---|
| `suga-mark.svg` / `suga-mark-teal.svg` | 符号，`currentColor` / 锁定主色 |
| `suga-wordmark.svg` | 字标（紧贴墨迹的 viewBox） |
| `suga-logo-horizontal.svg` / `-color.svg` | 横版组合，`currentColor` / 青绿符号 + 墨色字标 |
| `suga-icon.svg` / `-teal.svg` | 应用图标与 favicon 源 |
| `png/` | 符号 PNG 1024–16、图标 PNG 512–16、favicon.ico、apple-touch-icon、android-chrome、site.webmanifest |

最小尺寸：图标 16px，单独符号 ≥24px，横版组合 ≥160px 宽。
SVG 由脚本生成（`tmp/suga-logo/gen_brand.py`，未入库），改参数后重跑再重新导出。
未做商标检索；商用注册前建议单独做一次近似检索。
