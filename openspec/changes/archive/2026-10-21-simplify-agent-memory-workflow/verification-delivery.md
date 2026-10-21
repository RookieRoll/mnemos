# 0.17.0 本地交付验证

此记录描述本机 0.17.0 安装交付，不代表已发布 GitHub release。八个发布分发文件已随源提交更新到 0.17.0；release tag 与 GitHub 发布尚未执行。

## 最终源构建与兼容门禁

- 源提交：`3651ba2b0cd56462f8e4d8a4a991ac6811032050`。
- 最终二进制：`bin/mnemos-0.17.0-3651ba2b.exe`，版本 `0.17.0+3651ba2b`，SHA-256 `DFBC17047EFB8C4A057ADF126A41DFD52B29A62945C1E0DC9180375E5B2AA3F7`。
- `cmd/mnemos` 四项真实二进制 legacy 输出比较全部 PASS、无 skip：`TestPreToolRenderingMatchesPreChangeBinary`、`TestPreChangeBinaryMatchesSeededPreToolRendering`、`TestPreChangeBinaryMatchesSeededPromptRendering` 和 `TestContinuityLegacyBinaryStreamsMatch`。旧基线为 `bin/mnemos-0.16.0-79e68e8.exe`；实际比较 stdout、stderr 与退出码的适用场景。
- 执行命令：`CGO_ENABLED=0 GOMAXPROCS=2 MNEMOS_PRE_CHANGE_BIN=D:\Study\mnemos\bin\mnemos-0.16.0-79e68e8.exe MNEMOS_TEST_BIN=D:\Study\mnemos\bin\mnemos-0.17.0-3651ba2b.exe go test -p 1 -v ./cmd/mnemos -run '^(TestPreToolRenderingMatchesPreChangeBinary|TestPreChangeBinaryMatchesSeededPreToolRendering|TestPreChangeBinaryMatchesSeededPromptRendering|TestContinuityLegacyBinaryStreamsMatch)$' -count=1`。
- 最终二进制 Codex app-server gate：core 精确发现 9 tools、4 resources 并完成 16 次直接调用；maintenance 精确发现 27 tools、4 resources 并完成 19 次直接调用。使用隔离配置和数据库，auth 使用与模型请求均为 0。
- Pi SDK/adapter gate 的扩展测试为 62/62、无 skip；最终二进制 core/maintenance 分别发现精确 9/27 个工具，profile refresh 将 active surface 从 27 更新到 9。隔离验证没有使用 auth 或发出模型请求。
- Pi 本地 package-only 更新已完成：`C:/Users/chinasoft.h.qin/.pi/agent/local-packages/mnemos` 的包备份保存在 `bin/local-pi-package-backup-0170-3651ba2b/mnemos`（10 files）。白名单 runtime、manifest、README、skill 文件与仓库一致（包括五个 TypeScript runtime 文件）；`mcp.json` 的原 route、`directTools` 和 `toolPrefix` 保留，用户 settings 未改。隔离目录执行 `pi update --extensions --no-approve` 返回 PASS；因目标是本地目录包，Pi updater 不会更新该目录。该检查环境未设置 `PI_OFFLINE`。

## 本机数据库与安装

- 更新前使用 Python SQLite backup API 建立一致性备份：`bin/local-0.17-backup-20261021-103200`。备份 integrity 检查为 `ok`，schema 为 10；备份 SHA-256 `87651C18F5E1DB6F6A765FDB985C0F08F0CCED82AAAADFD83B6DCBDDCFBA2571`。
- 最终二进制已安装到 `D:/software/mnemos/mnemos.exe`。首次显式维护 CLI 操作打开数据库后，迁移 schema 为 11，SQLite integrity 为 `ok`。
- 二进制安装时 TOML 配置 SHA-256 为 `7DD145F84AB6DA731AA835B422826D492C8EC98D14C9B831FCB57AC4954E1C43`。用户授权将 `[mcp] profile = "core"`、`[maintenance] enabled = true, interval = "24h", max_pass_duration = "5s", lease_duration = "15s", renew_interval = "2s"` 和 `[integration] disabled = false` 显式写入 TOML，连续性保持 enabled。显式配置完成后的 SHA-256 为 `7A644E58B85F2FBC52D059CC15B9B4BA1DA0424EAA7E057F348E97C43730CAB9`；加载解析验证通过，确认有效值为 core、maintenance enabled、24h/5s/15s/2s、integration disabled=false、continuity enabled=true。修改前配置副本保存在 `bin/local-0.17-backup-20261021-103200/config.toml.pre-explicit-runtime-20261021-103809.bak`，其 SHA-256 与修改前值相同。更新过程中没有终止宿主进程。

## 离线交付验收

### 离线 Windows ZIP

- ZIP：`C:/Users/chinasoft.h.qin/Downloads/mnemos-pi-0.17.0-windows-amd64-offline-20261021.zip`，大小 `6,798,422` bytes，共 34 个文件；不含安装脚本。
- ZIP SHA-256 与 sidecar 一致：`BD82803AB4991AC9DC5268D73C360E28D72CBF2318FEC853B671DD384C8041AF`。
- 完整 CRC、逐文件 SHA-256、解压二进制版本及 Pi 工具面验证均 PASS。解压二进制版本为 `0.17.0+3651ba2b`；Pi 精确发现 core/maintenance 9/27 个工具并完成 12/13 次直接调用，模型请求与 auth 使用均为 0。
- 构建与解压验收报告：`bin/offline-0170-3651ba2b-aaaf69d31c/build-result.json`。
- ZIP 内文档是构建时的交付记录快照，因此不含 ZIP 自身的事后 SHA-256。归档中的该验证记录补记外部 ZIP/sidecar 哈希，不循环重建 ZIP。

最终二进制、Pi 本地 package-only 更新及离线 ZIP 均已验收。五份 delta 已同步到主 spec 并核对一致；本 change 已归档至 `openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/`。Release tag 与 GitHub 发布尚未执行。
