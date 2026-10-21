# 维护租约与 fencing 验证记录

本记录对应 SQLite maintenance coordinator、dream stages 与共享 runtime builder，列出本 lane 定向测试与 candidate gate 证据。仓库全量 Go 测试及 `go vet ./...` 已记录在 [verification-flow.md](verification-flow.md)；本机 Windows 不支持 CI 使用的 `-race` 检查。

## 已运行检查

以下检查均使用 CGO_ENABLED=0、GOMAXPROCS=2 和 go test -p 1：

| 命令 | 结果 | 覆盖内容 |
| --- | --- | --- |
| `go test -p 1 ./internal/storage -count=1` | PASS | 全 storage 包；包括迁移 0011 后的并发 Open 与历史迁移用例 |
| `go test -p 1 ./internal/dream ./internal/rumination -count=1` | PASS | 全 dream/rumination 包与显式调用点 |
| `go test -p 1 ./internal/dream -run 'TestCoordinator|TestMaybeRun' -count=1` | PASS | stage error、取消、失租、续租失败、预算超时及 automatic gate |
| `go test -p 1 ./internal/storage -run 'TestMaintenance|TestFencedDreamWrites|TestFencedPruneRollsBack|TestDreamJournal' -count=1` | PASS | 争锁、memory fallback、cycle takeover、decay marker、失效 fence 和 journal 幂等 |
| `go test -p 1 ./internal/storage -run 'TestFencedDreamWritesRejectInvalidOwnerAndExpiredLease' -count=1` | PASS | 五类写阶段在 DB 重启并由新 owner 接管后拒绝旧 fence |
| `go test -p 1 ./internal/rumination -run 'TestRumination_EndToEnd' -count=1` | PASS | 自动 MaybeRun 产出待审候选，不写 dream observation、不调用计数 Embedder |
| `go test -p 1 ./cmd/mnemos -count=1` | PASS | CLI builder、session-start maintenance wiring 与命令包回归 |

Go 的暖构建缓存曾在受限沙箱下返回 Access is denied；按工作区授权重试后上述检查通过。Windows 本地 CGO 关闭，因此没有运行 CI 使用的 -race 检查。

新增 fencing 用例以 invalid owner 和 expired lease 两种状态分别检查 prune、skill promotion、rumination candidate upsert、provenance auto-resolve 与 journal 写入。每项均要求 ErrFenceLost，并核对对应业务行未新增、未删除或未改变。另一个 prune 用例在同一事务内由触发器令租约失效；写后 fence 检查返回 ErrFenceLost，随后核实删除和触发器写入一并回滚。

新增 restart 矩阵先取得有效 fence、令 lease 过期、关闭并重开同一个文件数据库，再由新 owner 接管原 cycle；旧 fence 对五类写阶段均返回 ErrFenceLost，且业务行保持不变。

协调器用例验证普通 stage error 会返回 partial journal 和聚合 error，但不调用 Complete；失租或取消会停止后续 stage、不会写 journal，也不会 Complete。续租失败会取消阻塞的 stage，最大 pass budget 超时也不会推进 last_success。storage 用例另以两个 SQLite 连接同时 claim，要求恰有一个 acquired 和一个 busy；文件 DB 持有的短等待 pool 会很快跳过真实 writer lock，:memory: fallback 则复用主连接池并尊重调用 context。

## Crash replay 边界

本设计提供可恢复且有界的重放语义，不承诺整轮 exactly-once。decay 与 decay_applied 在同一 fenced SQLite 写事务提交；接管未完成 cycle 后看到 marker 会跳过第二次 decay，成功 Complete 后新 pass 才创建新 cycle。prune 可按过期谓词重跑；promotion 继续使用既有 promoted-origin:<hash> 稳定键；rumination 通过 (monitor, target kind, target ID) 的 candidate upsert 和既有 resolve 状态转换重放；journal 使用 cycle ID 作为稳定 observation ID，重试返回原内容且不增加 access count。

每个阶段各自提交，因而崩溃可能留下前序阶段的已提交效果，后续接管会继续同一 cycle。fence 检查阻止过期或被替代的 owner 提交之后的阶段；它不能撤销此前已经提交的独立事务。busy/锁竞争会跳过 opportunity claim；pass 预算到期或续租失败会取消 context，并保留未完成 cycle 供接管恢复。

maintenance_coordinator 只存单例 last-success、owner、lease expiry、cycle ID 与 decay marker，不存运行历史、诊断日志或模型生成内容。自动 pass 不写 dream journal、不调用 provider；显式 dream pass 的 cycle journal 是现有的用户可检索 observation。

## Codex MCP candidate gate

使用 ignored `bin/verify-codex-workflow-candidate.py` 对 `bin/mnemos-0.17-flow-candidate.exe` 运行真实 Codex app-server MCP 协议，参数显式指定 binary、profile 和预期版本。两个实例分别使用临时 `CODEX_HOME`、TOML 与 SQLite 文件；未读取用户配置或 auth 文件，不发送 turn/model 请求。core profile 精确发现 9 个规定工具、4 个 resources，并完成 16 次直接工具调用；maintenance profile 精确发现 27 个既有工具、4 个 resources，并完成 19 次直接工具调用，覆盖旧高级调用路径。两轮均 PASS，临时 transcript、inventory、result 和 isolated fixtures 保存在 `bin/codex-workflow-candidate-*`。该 gate 针对 0.17-dev 候选二进制；最终源提交构建尚未验证。
