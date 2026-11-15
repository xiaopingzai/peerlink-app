1.0.2 → 1.0.2-2 更新内容（1.0.2-1 未单独发布，本版为累计版本）：

## 新增

- 应用内更新检查新增官网清单源：优先读取官网 `update.txt` 更新清单，GitHub Release 作为回退源；清单按平台给出镜像直链，国内网络下不再只依赖 GitHub API。
- 品牌正式更名为 PeerLink：产品名、CLI/UI 产物命名统一为 `PeerLink-UI-*` / `PeerLink-CLI-*`，保留上游 VoxLink by AUGUHDAR 的 LGPL-3.0 归属。

## 修复

- 修正打包内置链接页的 Modrinth slug 与官网落地页指向。
- 兼容垫片：补齐 Go 1.20 可编译的 min/max/clear 实现，供 Win7 legacy CLI 旧栈使用。

## 说明

- 更新器同时识别新旧两种产物前缀（`PeerLink-UI-*` 优先，`VoxLink-UI-*` 回退），旧版本 Release 仍可作为回退源选包。
- 本项目派生自 VoxLink by AUGUHDAR，LGPL-3.0，归属见仓库 NOTICE.md。
