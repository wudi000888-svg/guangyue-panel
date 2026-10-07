# 广月面板 · 月庭 UI UX · 0.33.0

本目录包含在本机 Penpot 2.17.2 中实际创建、保存并从界面导出的原生文件：[打开/下载可编辑设计](guangyue-mooncourt-v0.33.0.penpot)。将文件导入 Penpot 即可继续编辑；文件已包含图层、文字、SVG、组件、颜色 Token 和原型流程。

## 文件内容

- 基础体系：1 画板、26 颜色 Token、主次按钮组件。
- 桌面端：14 核心流程与 17 资源/管理页面。
- 手机端：对应 31 页面，390px 设计宽度。
- 原型：201 个同页面有效跳转、5 个流程入口。跨页面的管理模块通过 Penpot 页面列表切换；真实产品路由保留全部功能。

结构审计结果见 [audit.json](audit.json)，文件标识和 SHA256 见 [manifest.json](manifest.json)。审计检查文件完整性、画板数量与重叠、字体、导航目标、父子按钮一致性和流程起点；不替代产品交互测试。

## 实现与再生成

[设计规范与施工方案](../penpot-courtyard-system.md) 描述实现结构与技术债；[验收记录](../courtyard-ui-qa.md) 区分真实界面、隔离数据和 Linux CI 验证。

`generate-assets.py` 以标准 Python 生成原创庭院与品牌 SVG。`build-penpot.js` 定义基础体系及绘图函数，`screens-penpot.js` 创建核心流程，`workspaces-penpot.js` 创建管理模块，`finish-penpot.js` 加载逐页收尾函数。它们为本地 Penpot 官方 MCP 的 execute_code 提供源码；需要先将本目录 Lucide SVG 和 frontend/public/design 内插画加载到 storage.assets，然后按页面依次执行。切换页面与批量修改必须分开调用，避免修改未激活页面。现成 `.penpot` 是完整交付文件，无需重新运行生成脚本。

## 素材与许可

庭院、广州塔、月亮、月门、水庭、竹叶与品牌印章为本项目原创 SVG，可在 Penpot 中编辑。设计用 Lucide 图标来自官方 lucide-static 包，许可文件见 [ISC license](assets/lucide/LICENSE.txt)。产品图标使用同一 Lucide 系列。

品牌字体来自 Google Fonts 的 Noto Serif SC，仅本地加载所需品牌文字子集；来源与 SIL OFL 许可在 frontend/public/design 的 font-source.txt 和 NotoSerifSC-OFL.txt 中。页面正文使用系统字体栈。运行时不依赖第三方图片或远程字体。样例二维码指向 example.invalid，不包含真实订阅令牌；产品二维码由真实订阅数据生成。
