# Profile、Pi SDK 与配置验证

此记录覆盖 0.17.0-dev 候选构建的配置/profile/维护 CLI 定向验证，以及 Pi SDK/adapter 的客户端工具面。候选构建仍是工作树迭代版，不代表正式发布或本机安装。

## 配置、profile 与 CLI

- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/config ./internal/mcp -run 'Test(Maintenance|CoreProfile|EmptyProfile|UnknownProfile)' -count=1` — PASS。覆盖 TOML budget 默认与验证、core/maintenance 启动工具表、空 profile 默认 core、未知 profile fail-closed。
- `go test ./cmd/mnemos -run 'Test(DecodeMaintenancePayload|MaintenanceCLI|MaintenanceJSONErrors|MaintenanceOversized)' -count=1` — PASS。覆盖有界严格 JSON、CLI list/evidence/resolve/dismiss 成功与拒绝、决策持久化、参数错误在打开存储前返回，以及稳定 JSON 错误 envelope。
- `go test ./internal/rumination -run 'Test(ResolveDecision|DismissDecision)' -count=1` — PASS。MCP/CLI 共享的结构校验与原服务决策入口。
- 工作树针对当前候选的配置、MCP 与 CLI 检查已按 Windows Go cache 权限要求在提权 PowerShell 下运行；全量 Go suite 与 `go vet` 由 core lane 单独验证，见同 change 的 core verification。

## 实际 Pi SDK/adapter 工具面

候选二进制为 `bin/mnemos-0.17-flow-candidate.exe`，版本 `0.17.0-dev`，SHA-256 `2a1e4d43983d69e78265ab36a830b642863144af32daa30f65929bda78812809`。Pi SDK 1.3.0 与 pi-mcp-adapter 5.3.2 均来自本机既有安装；测试用临时 package 副本、独立 agent 目录、临时 TOML 与 SQLite，不调用模型，也不使用认证。

- `node --check verify/pi/checkpoint-schema.mjs` — PASS。
- `node verify/pi/checkpoint-schema.mjs --profile core --sdk <existing-pi-sdk> --package pi --adapter <existing-pi-mcp-adapter> --binary bin/mnemos-0.17-flow-candidate.exe` — PASS，Mnemos server 工具精确 9 个，12 次真实 adapter direct executions。
- 同命令将 `--profile core` 改为 `--profile maintenance` — PASS，精确 27 个工具，13 次 direct executions，额外执行已有 `mnemos_stats`。
- 两轮都验证 checkpoint input schema 不暴露 `workspace`、伪造 workspace metadata 不改变已保存状态、save/replay/clear 成功；实际 provider/model 请求为 0，auth 使用为 false，五运行时 TypeScript 文件的副本 SHA-256 与 source 一致。
- 同一个隔离 Pi agent directory 的 profile refresh PASS：先让 maintenance 填入 27 个 `mcp-cache.json` 工具，再将临时 TOML 改成 core，并通过 adapter `mcp({ connect: serverName })` 重连。metadata cache 变为精确 9 项，Pi session active Mnemos MCP 工具面也精确为 9 项；旧高级工具不再 active。安装包和用户级 Pi cache 未触碰。

pi-mcp-adapter 文档及 5.3.2 源码支持 `/mcp-adapter reconnect <server>` 与 `mcp({ connect: "<server>" })` 重新发现并同步 direct-tool metadata。profile 切换后按 `/mcp-adapter` 显示的服务名重连；本机当前 package 的服务名为 `kls-mnemos-pi__mnemos`。变更 package 或 adapter 配置文件后使用 Pi `/reload`。此验证覆盖 SDK 及 adapter active tool surface，不声称完成真实 TUI 用户操作或模型工具选择验证。
