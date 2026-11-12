# NOTICE — 归属与许可说明

## 来源 / Origin

本仓库是在原作者公开的 VoxLink 项目基础上派生（fork）的构建分支，源代码与资源均来自上游，未逐行重写。

- 应用端上游仓库（original app source）：<https://github.com/AUGUHDAR/VoxLink-app>
- 模组上游仓库（original open-source repo）：<https://github.com/AUGUHDAR/VoxLink>

## 版权 / Copyright

版权归原作者及 VoxLink 贡献者所有。本派生仓库保留上游版权声明、语言文件、图标与资源，未删除原始归属信息。

## 许可 / License

- 上游模组仓库 `AUGUHDAR/VoxLink` 以 **GNU LGPL-3.0** 发布；本仓库根目录的 `LICENSE` 即取自该上游仓库的 LGPL-3.0 全文。
- 上游应用端仓库 `AUGUHDAR/VoxLink-app` 未随附独立 LICENSE 文件；如上游后续补充或变更许可声明，以上游为准。
- 基于 LGPL-3.0 的再分发须保留许可声明、标注修改并提供对应源码。

## 本派生分支相对上游的改动 / Changes in this fork

- 发布与 CI 流水线新增 32 位 Windows（`GOARCH=386`）构建目标：
  - `CLI-windows-386`：纯 Go、`CGO_ENABLED=0`，在 Ubuntu runner 上交叉编译；
  - `UI-windows-386`：实验性 32 位图形版，Wails v2 对 386 的支持未经验证，该格允许失败，失败时请回退 CLI 386。
- README 增加上游归属与许可说明。

除上述流水线配置与文档改动外，其余源码、前端资源与协议实现均沿用上游版本（appVersion 1.0.2）。

## 已知遗留 / Known leftovers

- CI 单测里 `TestGuestConnectViaBridgeBuildsLocalBridge`（`conn_wave_test.go`）对本地回环建桥的时序敏感，在部分 runner 上会在桥接事件落定前读取状态而失败；该用例与引擎源码均未在本分支改动，属上游既有行为。交叉编译探针已拆到独立 `compile` job，不被单测结果拖住。
- 32 位产物已在本地用 Go 1.26.3 交叉编译核验：`CLI-windows-386` 为 PE32 Intel 80386 控制台程序，`UI-windows-386` 同为 PE32 80386；32 位实机运行未在本机验证。
- 自动更新探测（`update.go`）已指向本派生仓库 `xiaopingzai/peerlink-app`；不再保留 Gitee 双源竞速（本分支未自建 Gitee 镜像）。
- 仓库内面向玩家的链接（Release 下载、Issue、相关链接卡片）指向本派生仓库：应用端 `xiaopingzai/peerlink-app`、模组源码 `xiaopingzai/peerlink-mc`；上游 `AUGUHDAR/VoxLink-app` 与 `AUGUHDAR/VoxLink` 仅作为来源归属保留在本 NOTICE 与 README 的「上游与归属」小节。
