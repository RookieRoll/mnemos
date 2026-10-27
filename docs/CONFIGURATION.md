# 配置参考

Mnemos 的运行配置由 TOML 文件决定。默认文件为 `~/.mnemos/config.toml`；普通首次运行会创建它。可在命令前显式选择另一个文件：

```text
mnemos --config "D:/work/app config.toml" stats
```

同一条命令不能选择两个不同配置。相对的 `storage.path`、`vault.path` 和 `[clients]` 路径都相对于所选 TOML 文件所在目录解析；`~` 表示当前操作系统用户的主目录。旧产品环境变量不再覆盖设置，迁移表见下文。

完整配置示例：

```toml
[storage]
path = "~/.mnemos/mnemos.db"

[search]
decay_rate = 0.05
default_limit = 20
max_context_tokens = 2000
prewarm_max_tokens = 500
hybrid_alpha = 0.5
near_dup_window = "24h"

[server]
transport = "stdio"
http_addr = ":8080"
api_key = ""

[mcp]
profile = "core"

[maintenance]
enabled = true
interval = "24h"
max_pass_duration = "5s"
lease_duration = "15s"
renew_interval = "2s"

[embedding]
provider = "auto"
model = "nomic-embed-text"
dimension = 768
base_url = ""
api_key = ""

[vault]
enabled = false
path = "~/.mnemos/vault"
watch_interval = "5m"

[dream]
interval = ""
stale_days = 30
decay_amount = 1

[rumination]
enabled = true
skill_effectiveness_floor = 0.3
skill_min_uses = 10
stale_skill_days = 90
stale_skill_floor = 0.5
correction_repeat_n = 3
contradiction_threshold = 1
stale_raw_days = 14
raw_promote = "auto"

[integration]
disabled = false

[continuity]
enabled = false

[clients]
claude_config_dir = ""
pi_agent_dir = ""
claude_desktop_config_path = ""

[diagnostics.workspaces]
max_roots = 1000
max_candidates = 200
timeout = "10s"
```

Omitting a field uses the default shown above. Boolean `false`, empty strings and numeric zero have field-specific meaning; `search.hybrid_alpha = 0` is valid and requests pure semantic weighting, while zero-valued limits receive their default and cannot be used to disable a budget.

## 字段、默认值与作用

| TOML 字段 | 默认值 | 作用与限制 |
| --- | --- | --- |
| `storage.path` | `~/.mnemos/mnemos.db` | SQLite 数据库文件。相对路径按配置文件目录解析。 |
| `search.decay_rate` | `0.05` | 记忆新近度衰减率。`0.05` 保留按记忆类型校准的衰减；设置其他值时对所有类型使用该统一值。当前加载器不校验其取值范围。 |
| `search.default_limit` | `20` | 搜索没有显式 limit 时的结果上限；必须至少为 1。 |
| `search.max_context_tokens` | `2000` | 上下文组合的默认 token 预算；至少 100。 |
| `search.prewarm_max_tokens` | `500` | session-start prewarm block 的 token 预算；至少 100。 |
| `search.hybrid_alpha` | `0.5` | 关键词/向量混合权重，范围 `[0,1]`；0 表示纯语义，1 表示纯关键词。 |
| `server.transport` | `stdio` | 服务传输方式：`stdio` 或 `http`。 |
| `search.near_dup_window` | `24h` | 近重复合并窗口，非负 Go duration；`0s` 关闭近重复合并，精确内容去重保留。所有 CLI、MCP 和 HTTP 保存路径使用同一配置。 |
| `server.http_addr` | `:8080` | HTTP 服务监听地址；仅 HTTP 模式使用。 |
| `server.api_key` | 空 | HTTP API bearer key；为空时不启用 key 校验。按密钥管理，不要提交到仓库。 |
| `mcp.profile` | `core` | 启动时固定工具面：`core` 只注册九个常用任务工具；`maintenance` 恢复完整既有 MCP 工具集合。未知值在启动前拒绝。默认值改变是 0.17.0 的 MCP 工具面破坏性变更；需要高级工具的宿主须显式设为 `maintenance` 并重启。 |
| `maintenance.enabled` | `true` | 是否在有效任务会话开始时尝试到期维护；也可独立关闭自动维护。显式 `mnemos dream` / `mnemos maintenance` 命令仍可执行。 |
| `maintenance.interval` | `24h` | 自动维护的到期周期，正 Go duration。未完成且租约过期的周期优先恢复，即使上次成功时间较近。 |
| `maintenance.max_pass_duration` | `5s` | 每轮维护最长执行时间；正数，严格短于租约，最大 `5m`。 |
| `maintenance.lease_duration` | `15s` | dream、watch 与自动机会共享的数据库租约时长；正数，最大 `10m`。 |
| `maintenance.renew_interval` | `2s` | 租约续期间隔；正数且短于租约。 |
| `embedding.provider` | `auto` | Provider：`auto`、`ollama`、`openai` 或 `none`。 |
| `embedding.model` | `nomic-embed-text` | Provider 使用的模型名。 |
| `embedding.dimension` | `768` | 预期向量维度；必须与模型输出一致。 |
| `embedding.base_url` | 空 | 可选 provider endpoint。 |
| `embedding.api_key` | 空 | 可选 embedding provider 密钥。按密钥管理，不要提交到仓库。 |
| `vault.enabled` | `false` | 是否允许 vault export/watch；关闭时 export/watch 会在产生文件副作用前报错，status 仍可读。 |
| `vault.path` | `~/.mnemos/vault` | Obsidian vault 目录。 |
| `vault.watch_interval` | `5m` | watcher 轮询间隔，启动 watcher 时按 Go duration 格式解析。 |
| `dream.interval` | 空 | 仅 `mnemos dream --watch` 读取此周期；留空时该命令使用 6 小时默认周期。未加 `--watch` 时 `mnemos dream` 只执行一轮。非空值在启动 watcher 时按 Go duration 格式解析。`mnemos serve` 不会自动启动 dream daemon。 |
| `dream.stale_days` | `30` | dream 清理/衰减使用的闲置天数阈值。 |
| `dream.decay_amount` | `1` | 每次 dream pass 的重要性衰减量。 |
| `rumination.enabled` | `true` | 是否在 dream pass 中运行阈值候选检测及暴露可选工具。 |
| `rumination.skill_effectiveness_floor` | `0.3` | 低于此 effectiveness 可触发候选；加载时验证范围 `[0,1]`。 |
| `rumination.skill_min_uses` | `10` | 技能进入 effectiveness 检查前的最少使用次数；至少 1。 |
| `rumination.stale_skill_days` | `90` | 技能闲置判断天数；当前加载器不单独验证此字段。 |
| `rumination.stale_skill_floor` | `0.5` | 闲置技能候选的 effectiveness 上限；当前加载器不单独验证此字段的范围。 |
| `rumination.correction_repeat_n` | `3` | 同主题纠错重复触发阈值；当前加载器不单独验证此字段。 |
| `rumination.contradiction_threshold` | `1` | 指向目标的 contradicts 关系最小数量；当前加载器不单独验证此字段。 |
| `rumination.stale_raw_days` | `14` | 未晋升 raw observation 的闲置天数阈值；当前加载器不单独验证此字段。 |
| `rumination.raw_promote` | `auto` | raw→promote 开关，只接受 `auto` 或 `never`。`auto` 保持现状：闲置超过 `stale_raw_days` 的 raw observation 会生成 stale-raw 候选并进入待 review 队列，可用 `mnemos_promote` 或 dismiss 处理。`never` 关闭该通路：raw 观测不再生成 stale-raw 候选，因此永远不会入队，但仍可在其产生处观测；其他 rumination monitor（effectiveness 下限、闲置技能、纠错重复、contradicts）不受影响。加载配置时其他取值直接报错并列出这两个值，不会静默回退默认值。 |
| `integration.disabled` | `false` | 为 true 时关闭自动 hooks/prewarm 集成；显式调用的 MCP 工具仍可用。 |
| `continuity.enabled` | `false` | 为 true 时启用支持该能力的自动 continuity 启动。Workspace root 仍需按现有 API/宿主规则显式提供，不是全局配置字段。 |
| `clients.claude_config_dir` | home 下的 `.claude` | 覆盖 Claude Code 配置目录；留空使用 `~/.claude`。 |
| `clients.pi_agent_dir` | home 下的 `.pi/agent` | 覆盖 pi agent 配置目录；留空使用 `~/.pi/agent`。 |
| `clients.claude_desktop_config_path` | home 下的 OS 常规路径 | 覆盖 Claude Desktop 配置文件路径；留空使用 `~/AppData/Roaming/Claude/claude_desktop_config.json`（Windows）或 `~/Library/Application Support/Claude/claude_desktop_config.json`（macOS）。如 AppData/known folder 重定向到其他位置，请显式填写完整路径；Linux 不自动探测 Claude Desktop。 |
| `diagnostics.workspaces.max_roots` | `1000` | `doctor --workspaces` 最多读取/探测的已有根数量；至少 1。 |
| `diagnostics.workspaces.max_candidates` | `200` | 最多输出的候选数量；只有找到额外候选或其他预算耗尽时报告 partial。 |
| `diagnostics.workspaces.timeout` | `10s` | 工作区诊断总时间预算，Go duration 格式且必须为正数。 |

配置投影 `mnemos config --client pi` 只输出非密钥字段，不会打开数据库或初始化 embedding provider。正常 `mnemos config` 会显示解析后的完整配置，因此输出可能包含服务或 provider 密钥；分享日志前先检查并遮蔽密钥。

`core`/`maintenance` 是启动时固定的 MCP 发现面，不是账号 ACL。`core` 注册 `session_start`、`session_end`、`search`、`get_many`、`save`、`correct`、`convention`、`checkpoint` 和 `resume_context`；profile 不会在进程运行中变化。需要高级 MCP 操作时设 `[mcp].profile = "maintenance"` 后重启。Pi adapter 会缓存 direct-tool 元数据；只切换 TOML profile 后，可在同一 Pi 会话运行 `mcp({ connect: "<Mnemos server name>" })` 或 `/mcp-adapter reconnect <server name>`，按 `/mcp-adapter` 显示的名称刷新列表和缓存。改了 Pi package 或 adapter 配置文件后再运行 `/reload`。使用 `core` 的活动宿主仍可通过 JSON CLI 桥接执行显式维护决策：

```text
mnemos maintenance list --limit 20
mnemos maintenance evidence --id CANDIDATE_ID
mnemos maintenance resolve --payload '{"candidate_id":"CANDIDATE_ID","resolved_by":"REVISION_ID","why_better":"The revised rule predicts this concrete case will now succeed."}'
mnemos maintenance dismiss --payload '{"candidate_id":"CANDIDATE_ID","reason":"The counterexample was a one-off and the rule still holds."}'
```

CLI 只验证必填字段与长度，并复用 MCP 同一决策服务；是否有足够证据以及是否应该修改知识由活动宿主 agent 判断。没有会话时不会唤醒 agent，也不会自动作语义 resolve/dismiss。安装、改配置、备份和迁移必须由明确任务或已配置策略授权；会话启动的机会维护不会自行做这些管理操作。

需要升级数据库 schema 时，由获明确授权的 Maintenance Agent 先调用现有一致性备份能力，再执行迁移。普通启动与每次会话开始不创建额外数据库快照；离线交付包不携带用户设置、凭据或 provider 密钥。

`examples/hooks/` 中的 HTTP hook 使用 Python 3.11+ 标准库 `tomllib` 和 `urllib`，从同一个 `config.toml` 读取 `[server].http_addr` 与 `[server].api_key`。可通过 `--config <绝对路径>` 选择文件，并通过 `--project <名称>` 显式给出 legacy project label；没有该参数时从 Git root 或当前目录派生名称。JSON 用标准库编码，包含引号或换行的文件名/提交正文不会拼接进 JSON 字符串。hook 示例不读取产品环境变量，也不增加第三方依赖。

## 迁移旧环境设置

旧版文档曾建议用环境变量选择二进制、关闭自动 hooks、传递 API key 或定位客户端目录。新版忽略这些产品设置环境变量。把配置迁入 TOML，再重启对应宿主；操作系统自身的 `HOME`/`PATH` 仍负责常规程序发现，Mnemos 不把它们当作产品开关。

| 旧环境变量 | 新位置 |
| --- | --- |
| `MNEMOS_CONFIG` | 改用命令前缀 `mnemos --config <绝对路径> <命令>`。 |
| `MNEMOS_DISABLED` | `[integration] disabled = true`。 |
| `MNEMOS_CONTINUITY` | `[continuity] enabled = true`。 |
| `MNEMOS_API_KEY` | `[server] api_key = "…"`；HTTP 服务配置使用同一文件。 |
| `MNEMOS_BIN` | 在安装器或 pi 的 `mcp.json` 路由中配置可执行文件；不要通过环境变量给 hooks 指定另一份二进制。 |
| `CLAUDE_CONFIG_DIR` | `[clients] claude_config_dir = "…"`。 |
| `PI_CODING_AGENT_DIR` | `[clients] pi_agent_dir = "…"`。 |
| `APPDATA` 重定向带来的旧路径依赖 | `[clients] claude_desktop_config_path = "…"`；Mnemos 不读该变量来重定向路径。默认使用 home 下的常规 Windows `AppData/Roaming` 路径；若用户文件实际位于重定向目录，应在 TOML 中写完整绝对路径。 |

示例：

```toml
[integration]
disabled = true

[continuity]
enabled = true

[clients]
claude_config_dir = "D:/Users/alex/.claude"
pi_agent_dir = "D:/Users/alex/.pi/agent"
```

绝对配置路径也应写入安装后的 MCP/hook 命令，避免不同工作目录加载不同数据库。显式 `--config` 指向不存在文件会失败；只读投影和 `doctor --workspaces` 在隐式默认配置不存在时使用内置默认值且不创建文件。
