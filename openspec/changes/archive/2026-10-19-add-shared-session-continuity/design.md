# Design

## Context

动机见 [proposal.md](proposal.md)。当前 `Session` 已保存目标、总结、反思和结束状态；prewarm 已有完整条目预算与安全扫描。新增能力应补齐进行中任务的明确进度，而不是再次保存这些已有数据。

代码核验基线为 `main@37ca6539`：

- `cmd/mnemos/hook.go:projectFromHook` 与部分启动入口使用目录末级名；同名目录不能据此判定为同一个工作区。
- `internal/session/service.go:Resolve` 和旧启动路径具有最近会话回退、时间窗口复用语义；新连续性写入不能继承这些猜测。
- `internal/prewarm/prewarm.go` 已实现全文/简短/最小表示、预算、安全警告和准确的 observation/skill 引用。
- `runHookPostTool`、`runHookSessionEnd` 的空结果不是事务提交证明；`pi/extensions/hook-transport.ts` 的旧解析器也不是持久化回执协议。
- 现有 `memory-reading` 规定明确 agent/project scope，旧单条 Get/replay 刻意保留兼容行为；不能使用后者拼装跨 agent 的共享正文。

参考 ai-memory 的 [架构](https://github.com/akitaonrails/ai-memory/blob/b18f22a7c2f3068575c70d37f13d167fed3476bb/docs/ARCHITECTURE.md)、[交接数据模型](https://github.com/akitaonrails/ai-memory/blob/b18f22a7c2f3068575c70d37f13d167fed3476bb/crates/ai-memory-core/src/handoff.rs) 和 [数据处理边界](https://github.com/akitaonrails/ai-memory/blob/b18f22a7c2f3068575c70d37f13d167fed3476bb/DATA_HANDLING.md)。吸收明确来源、受限捕获与可靠提交的思想；本设计按用户选择提供共享读取，不采用其领取状态或完整 spool 体系。

## Goals / Non-Goals

**Goals:**

- 新会话可明确回答“从哪个会话、哪个版本、何时记录的进度继续”，并在执行前核验当前代码。
- 共享的是 agent 主动发布的工作状态；工作区身份、写入身份与可见范围由服务统一验证。
- 任何错误、超时、重复写入或清除都不能把旧进度误写入另一个会话。

**Non-Goals:**

- 不把工作检查点当作经过验证的长期知识，也不使用它训练、晋升技能或改变记忆评分。
- 不对既有 project/agent 数据模型做全库迁移；本次隔离承诺针对新增连续性路径。
- 不承诺回放完整对话、恢复模型内部状态、恢复终端进程或即时获知其他 agent 尚未保存的操作。

## Decisions

### D1. 复用 SQLite，新增窄的连续性服务

```text
MCP / HTTP / CLI / Host adapters
               |
      Continuity service
        /      |       \
 Workspace   Session   Safety
 resolver    binding   scanner
               |
     SQLite: sessions + bindings + checkpoints
               |
      Scoped candidate / source read
               |
      Budgeted context composer
               |
        New independent session
```

在 `internal/continuity` 集中实现选择、作用域、检查点校验和写入规则；存储接口在该消费者包声明。会话服务继续管理现有 Session，存储层负责原子写入；协议层只转换形状。所有连续性操作只需本地 SQLite、时钟和工作区信息，不初始化 embedding provider 或调用 LLM。

替代方案：把检查点保存为 observation 会使临时状态参与检索、衰减及晋升；把完整正文加入所有 Session 列表会隐式扩大数据输出。选择独立窄表避免这两种副作用。

### D2. 可靠工作区身份只服务新增连续性路径

保留原 `project` 字符串作为旧协议的标签与作用域，不重写历史值。新增 `workspace_key`：以版本前缀和本机规范化工作树根路径计算 SHA-256。Git 仓库使用当前 worktree 的顶层目录；非 Git 项目需要调用方明确提供根目录。根目录必须存在并成功规范化；失败时停用自动恢复，不回退到 basename。

规范化遵循实际文件系统的路径等价规则并解析合法链接，不能对所有平台路径盲目 lowercase。使用参数数组调用只读 Git 查询，不解释 shell 字符串，不读取 remote 凭据；排除影响仓库选择的环境覆盖，并验证返回的根确实包含调用目录，无法可靠判断时不建立 binding。相同工作树的子目录应解析到同一 key；同名 clone 和不同 worktree 默认不同。分支名不是身份，分支和 HEAD 是写入时采样的现场元数据。

本次不加入跨 worktree 映射、仓库移动后的自动归并或旧 session 批量绑定。旧记录缺失 binding 时保留旧功能，但不进入新恢复候选。原有普通 memory 查询继续遵循原契约；新增工作区 key 不表示已消除所有旧 project 名碰撞。

### D3. 新会话和启动重试必须可区分

新增一对一 `session_continuity_bindings`，外键指向 Session，记录 `workspace_key`、规范化根、`harness_kind`、可选宿主会话 ID 和 `start_key`。owner 继续取 Session 的 `AgentID`；缺省 agent 明确解析为 `default`，空值不表示所有 agent。宿主种类与 AgentID 分开。

连续性启动携带调用方生成的非空 `start_key`（最多 128 UTF-8 字节），每个真正的新启动、reload、new、resume、fork 产生新 key，同一次调用的重试保持原 key。`(owner, workspace_key, harness_kind, start_key)` 唯一；创建 Session 和 binding 在同一事务完成。同 key、同规范化请求只复用仍打开的对应 Session；冲突输入或已关闭目标返回冲突，不另开会话。binding 保存最小启动请求 hash，供相同请求判定。session/owner/host ID 最多 256 UTF-8 字节，harness 最多 64 字节，根目录和 legacy project 最多 4096 字节；拒绝非法 UTF-8 或越界输入。

旧原生 host ID 只作为来源元数据，不以裸 host ID 或 90 秒时间窗口选择新路径的会话。checkpoint 写入必须携带精确 Mnemos session ID 和匹配的 owner/workspace；查无、未绑定或范围不符返回统一 unavailable，不回退到最新会话。接手来源 ID 永远不能代替新会话 ID。

隔离也覆盖反向路径：新版二进制中的旧式 adopt、Resolve（含裸 host ID 匹配）、无精确目标的 close/twins 与 stale sweep 必须排除 continuity-bound Session。既有只含 legacy Session 的行为和输出保持兼容，已有精确 Mnemos ID 的合法操作仍可定位其目标。普通列表和管理员导出不因该过滤丢失数据；过滤只用于推测会话归属或隐式关闭。未升级的旧二进制无法遵守新增约束，因此启用前必须重连同一数据库的宿主进程，不能把新旧二进制同时运行列为受支持场景。

### D4. 每个会话只有一份最新检查点

`session_checkpoints` 以 `session_id` 为主键并级联关联 Session。字段为 `revision`、可空的结构化 `payload`、`visibility`、`updated_at`、当前 `last_request_id` 和 `last_request_hash`。不保存版本历史或整个请求。正文为空的行是清除后的版本标记。

payload 为完整替换，初始契约如下；限制是本设计选定值，不是现有功能：

| 内容 | 限制与含义 |
| --- | --- |
| `task` | 必填，1–256 UTF-8 字节的 agent 编写任务标签；精确匹配，不做语义聚类 |
| `state` | `active`、`blocked`、`done`；调用方报告的进度，不是领取或调度状态 |
| `summary` | 必填，最多 4096 UTF-8 字节，说明已完成与已验证事实 |
| `next_steps`、`blockers` | 各最多 8 项，每项最多 512 UTF-8 字节；缺失表示未记录 |
| `references` | 最多 16 项，每项 `kind=observation|file` 与最多 256 字节的标识或项目相对路径 |
| 工作现场 | 服务端采样的分支或 detached 标记、HEAD；不可用时显式 unknown，不能当作匹配 |
| 完整 payload | 规范 JSON 编码连同现场元数据最多 16 KiB；超限拒绝，不截断后保存 |

请求结构未知字段、非法 UTF-8、越界数组和超长值失败。入口读取原始请求至多 32 KiB；超限在解析前拒绝。先进行显式字段投影和既有安全扫描，再验证规范化大小；高风险写入拒绝，不写原始 payload 日志。只允许 agent 主动编写摘要，hooks 不把 prompt、工具输入/输出或 transcript 自动转换为检查点。安全扫描不是完整的秘密识别器，发布者必须选择可共享内容。

工作检查点不隐式修改 Session.Goal、Summary、Reflection、结束状态、observation 或访问/成功计数。最终总结仍通过现有 session_end 完成；中途保存与结束总结可以各自失败，任何一方都不得虚报另一方成功。

### D5. 共享单位是显式发布的检查点

`visibility` 默认 `owner`；写入时明确选择 `workspace` 才能被同一 workspace 的其他 agent 读取。共享只开放该检查点正文及必要来源元数据，不开放来源会话的 Goal、Summary、Reflection、其他检查点历史或 observation 正文。调用方选择 workspace 发布即表示该次正文的共享范围，不建立用户、团队或 ACL 系统。

引用只作为有界标识返回，不自动读取文件、URL、legacy Get 或 replay。接收者后续读取 observation 必须使用自己的既有 scoped-read 规则；不能借用来源 agent 的 scope。未绑定或旧会话正文不自动转为共享检查点。

同 owner、已绑定且在当前 workspace 的结束会话可作为摘要候选，直接有界投影已有 Summary，标记 `source_kind=session_summary`，不复制成 checkpoint；Reflection 不进入恢复块。有可见 checkpoint 的同一会话只返回 checkpoint。跨 owner 只返回显式共享的 checkpoint。清除 checkpoint 后该会话不再回退到摘要候选，避免清除后旧正文重新浮现。

不同 agent 采用同一个 `default` owner 时属于同一个 owner 命名空间；宿主名字不是权限边界。接口遵循调用方提供并验证的本地 scope，不声称抵御可任意访问数据库的本地进程。

### D6. 版本校验、清除和回执只覆盖当前版本

写入/清除要求 `session_id`、workspace、owner、`expected_revision`、非空且最多 128 UTF-8 字节的 `request_id`。从未写入的 checkpoint 视为 revision 0；revision 为非负 64 位整数，溢出拒绝。单个事务内先验证身份，再判定当前精确重试，最后验证该操作允许的会话状态和 revision；新正文要求会话打开，clear 允许已关闭会话。新 mutation 的 revision 加一，服务时钟记录 updated_at。

请求 hash 使用固定版本的规范化结构，绑定 session ID、操作、owner/workspace、原 expected_revision、显式正文和 visibility，不直接 hash 原始 JSON 或无序 map，不包含重试时重新采样的分支或时间。clear 不接受正文。初版规范化规则升级前需保留旧请求的重试兼容。

- 同一当前 request ID 与规范化请求 hash 完全一致：返回 `replayed` 和原 revision/时间，不重新采样现场、不改写正文。即便会话随后关闭，精确重试仍可确认原提交。
- 同一当前 request ID 对应不同内容，或 expected_revision 过期：返回 conflict。允许 owner 得到当前 revision，正文需另行读取。
- 已出现更新版本后，旧请求不能覆盖新版本；本设计没有历史 receipt，因此只保证当前版本的精确重试确认。
- 清除采用相同 CAS，正文置空、visibility 重置 owner、revision 加一；保留最小版本和当前请求身份，防止 ABA。只要会话尚存在就不重置 revision。已关闭会话允许 owner 清除，拒绝新正文写入。
- 提交成功后才返回 `{protocol_version:1, status:applied|replayed, session_id, revision, updated_at}`。超时或进程中断代表结果未知；调用方以同一请求精确重试或重新读取，不能生成新请求强行覆盖。

关闭和 checkpoint 更新使用同一数据库事务边界串行判定：更新先提交则正文可见，关闭先提交则拒绝新正文；都不能重开 Session。无需 spool、单独 ACK 表、后台 drain 或网络投递队列。

### D7. 候选选择是有界、确定的读取

恢复接口不创建或关闭 Session、不领取进度、不自动执行 next_steps。其输入为当前新会话身份、可选精确 `source_session_id`、可选精确 task 标签，以及 `resume_intent`。

所有候选数量、可见性、revision 和正文从同一 SQLite 读取快照获得；本次读取开始后发生的 clear/撤回不拼接到旧版本正文，后续读取重新验证权限。服务在每次读取时从当前会话绑定根采样 branch/HEAD，不使用启动时缓存；仓库采样与数据库不是跨资源原子快照，因此它只提供比较线索，执行前仍须核验。

1. 显式来源必须先通过 workspace 和可见性验证；不存在或不可见统一 unavailable，并且不替换为另一个候选。显式可读来源可以过旧、已 done 或分支不同，但必须返回对应标记。
2. 无显式来源时，排除当前会话，按 scope/visibility 筛选，再按可选 task 精确过滤。绝不按自然语言推断不同标签是同一任务。
3. 自动选择要求 `resume_intent=true`，scope/visibility/task 过滤后的全部可见候选恰好一个，再检查该候选是 24 小时内、state 为 active/blocked、分支与 HEAD 已知且与本次读取现场相同的 checkpoint。另一个 stale、done、异分支或摘要候选也阻止自动选择；可通过精确 task 或显式来源缩小集合。无 Git 元数据或现场查询失败时不能自动选择。摘要候选因缺乏可靠进度/分支信息仅供明确选择。
4. 否则返回 `none` 或 `candidates`；多个符合条件的候选标记 ambiguous。排序使用 updated_at（摘要使用 EndedAt）降序再按 session ID；“最新”不能解除歧义。

候选默认 5 项、最大 20 项；在完整可见结果集上确定唯一性和 `has_more`，不能在 limit 或响应预算截断后误判唯一。非 owner 的候选不泄露私有标题、数量或正文。已超过 24 小时称为 `stale`，表示需要人工核验，不表示数据已过期删除。

显式选择只确定来源，不能承诺源代码仍然有效。分支/HEAD 变化标记 `workspace_changed`；dirty 工作区和未提交改动无法由 HEAD 证明，恢复块始终要求核验当前文件、差异与相关测试。来源关闭状态与 checkpoint state 分别展示，结束会话不等于任务完成。

### D8. 恢复正文临时组装，受同一个预算限制

恢复服务返回 source kind、来源 session/revision、owner/harness、记录时间、状态、分支/HEAD 差异标记及有界正文。完整 JSON 响应默认 32 KiB、最大 64 KiB，包含候选、引用、警告和信封；放不下的完整条目省略并报告 `omitted_for_size`/`has_more`，预算省略不能被标为成功交付。

session-start 的 opt-in 连续性模式始终传入 resolved owner，在现有 pinned 核心之后、既有其他 section 之前加入恢复 section，并省略旧 recent-session section；会话摘要只经 D5 的受限投影进入恢复候选。其余 section 相对顺序不变。普通观察的原 scope、排序与可见性不扩大；该变化不表示旧 project collision 被全局修复。默认启动没有明确恢复意图时只提供有界候选提示；选定来源需要明确恢复意图或显式来源。当前会话 compaction recovery 保留原语义，不把旧来源当作当前 Session。

完整条目或完整紧凑表示必须带来源、时间、必要的 stale/workspace_changed 警告与核验提示；不够预算则省略。上下文最终输出沿用 `ceil(UTF-8 bytes / 4)` 估计，标题、分隔符、安全包裹和候选提示计入同一预算。选择与渲染扫描未可信正文，不能提升为系统规则。

返回的 `continuity_refs` 仅描述实际交付的 session/checkpoint 来源，不冒充 observation/skill 引用，也不写入现有 injection 表。重复读取不保存候选、渲染块、消费确认或成功事件。无需持久化“由谁接手”；多个会话可同时使用相同来源。

### D9. 协议显式选择，适配器不改变决策

计划提供统一服务语义的接口：

| 操作 | MCP | CLI | HTTP / Go client |
| --- | --- | --- | --- |
| 打开连续性会话 | `mnemos_session_start` 的可选 continuity v1 输入 | `mnemos continuity open` | 新增连续性 session-open 路由/方法 |
| 保存或清除检查点 | `mnemos_checkpoint`，明确 action | `mnemos continuity checkpoint` | scoped checkpoint 写入/清除方法 |
| 列候选或读来源 | `mnemos_resume_context` | `mnemos continuity context` | scoped resume-context 读取方法 |

新增 CLI 使用 `--payload` 和 JSON，显式 `protocol_version:1`；新错误具有固定 code，CLI 非零退出、MCP 工具错误与 HTTP 对应错误一致。公开响应区分 unavailable、conflict、validation、storage_failure 和结果未知的传输失败；未提交时不产生成功回执。

旧 hook 的空 JSON、成功退出和静默降级继续原样；新调用方不得据此认定保存。新增无数据库写入、无配置创建、无 Session 副作用的 `mnemos continuity capabilities` 查询。仅能力查询明确不支持，或确认 open 未执行的 unknown-command，才允许旧式启动降级。open 发出后的超时、旧/畸形 envelope、不支持的回执版本或缺字段都属于 outcome_unknown，不证明未提交；只能以原 start_key 核验，不另开 fallback 会话。新 checkpoint 写工具也纳入各宿主的写边界检查和 pi 后缀匹配。

仅安装 MCP 的宿主通过显式会话启动/检查点/恢复工具接入，不声称存在自动启动事件。skill 指引在用户要求切换、形成阶段性结果、遇到阻塞或上下文压缩前主动记录；写入内容由 agent 编写，核心不总结原始消息。

### D10. pi 维护当前会话代次，不保留跨会话队列

pi 只有在 `MNEMOS_CONTINUITY=1` 且 disable 开关未设置时尝试连续性能力查询与 open；未启用时继续旧启动路径。仅 D9 定义的无副作用 unsupported 才能创建旧式降级会话，open 提交结果未知时不能另开 fallback 会话。每个 session_start 生成新 start_key/内存代次；同一次调用重试复用 key。延迟返回的旧结果不得替换新 session ID、候选或预热块。

open 成功后由同次服务调用复用 composer 为返回的精确 session ID 生成 prewarm，不再调用会创建/adopt 会话的旧 CLI 入口。预热失败返回已提交 ID 与 context-unavailable 状态，不撤销会话，也不触发再次启动；相同 key 重试仍绑定该 Session。

连续性启动未确认时维护明确的 unbound 状态，不把空字符串当作可回退的 session ID。该代次的 user-prompt、post-tool touch、compaction 与 shutdown 等依赖会话归属的自动调用暂不发送；只有原 start_key 确认得到精确 ID 后才启用。pre-tool 的安全扫描继续通过不查询/不变更会话的 guardrail-only 路径执行，不能因缺少身份而跳过防护，也不能顺带执行旧记忆注入或 Session 回退。显式 MCP 调用继续返回各自真实结果。未确认期间的事件不进入持久队列，不在确认后补放；因此降级可能遗漏一次触碰，但不会误关闭并行会话。

恢复内容在 `before_agent_start` 的非累积通道提供，或作为明确 MCP 工具结果返回。`tool_call` 只执行现有 guardrail，不被当作上下文通道。checkpoint 的 agent 写入通过 MCP 显式调用，扩展不从 tool_result 或 user prompt 拼正文。更新工具识别和 skill 说明，但不在本规划阶段修改已安装包。

宿主 shutdown 继续精确关闭当前 session；hook 不承诺生成最后检查点。`MNEMOS_DISABLED` 阻止新增自动启动、恢复提示和副作用，显式 MCP 工具仍沿用原可用策略。自动路径失败保持静默且不阻塞工作；显式保存失败对调用 agent 返回真实错误。

### D11. 生命周期与备份边界保持诚实

首版没有定时 TTL 或检查点历史：每 Session 至多一份 16 KiB 正文，owner 可随时清除，真正删除 Session 时级联删除 binding/checkpoint。24 小时 stale 只控制自动选择。会话数量仍可增长，所以“单份有界”不代表全库总量固定。清除不承诺抹除 WAL 或已有备份中的字节。

新增逻辑 dump 不悄悄声称保存连续性数据：本次普通 JSON export/import 继续原格式，导出检测到连续性数据时明确提示未包含；提示写 stderr，不能污染 JSON。检测失败时不得声称已确认无连续性数据，需报告范围未知。导入旧 dump 不创建或覆盖 bindings/checkpoints。工作区身份是本机绑定，跨机器携带还需要独立设计。完整同机恢复使用 SQLite 一致性备份，保留版本和请求身份；测试用 SQLite backup，不能复制正在写入的裸 db 文件。

这避免顺带扩展当前只读取有限 sessions 的逻辑导出范围。后续若要求逻辑备份完整保真，应单独解决完整分页、错误传播、导入映射与防止旧版本覆盖问题。

### D12. 用具体场景验证收益

确定性测试验证正确性：同名目录隔离、不同 worktree、双 agent 可见性、候选歧义、CAS 并发、ACK 丢失、旧请求延迟、clear ABA、关闭竞争、中文边界、总响应预算、恶意内容和 disabled/unsupported。特别覆盖 open 已提交但 ACK 缺失/畸形、open 成功但 prewarm 失败、关闭后精确重试和 clear、重试不重采样现场，以及 legacy recent-session 旁路隔离。混用验证使用同一新版二进制：unknown-start 后 prompt/touch/shutdown 不触发空 ID 回退且 guardrail 仍生效；legacy adopt/resolve/twin-close/sweep 不能选中 bound Session。

另准备固定小项目与固定进度，比较“现有结束摘要”与“检查点恢复”两种流程到首次有效行动所需的工具读取次数、重复探索次数及遗漏阻塞数。普通测试不调用 LLM；真实 agent 的接续体验作为明确标注的手工观察，记录场景、客户端版本和样本量。无对照数据时不宣称百分比收益。

## Risks / Trade-offs

- 检查点依赖 agent 主动保存：通过明确工具说明和阶段节点指引改善；异常退出只能恢复到最后成功提交。
- latest-only 会丢失旧进度版本：保留既有最终总结与正式记忆，历史审计不在本变更中。
- 多个 agent 同时执行相同下一步：共享只读不解决任务分配或文件冲突，执行前核验现场。
- 旧记忆的 project 名碰撞仍存在：新连续性 key 单独隔离，不把本变更表述为全局身份迁移。
- 显式共享的正文可能过度包含敏感内容：默认 owner、严格字段边界和扫描；不自动复制私有记录，不承诺万能脱敏。
- 暂无逻辑 dump 保真：导出明确告知边界，同机完整备份使用 SQLite 一致性备份。
- 新旧适配器混用：显式协议版本、unsupported 降级和旧输出差分测试；超时不得误判旧版本。
- 新旧二进制共用数据库：旧二进制无法排除新绑定会话，启用前停止旧进程并重连；只保证新版二进制上新旧协议适配器的混用。

## Migration Plan

1. 在实施时选取下一个空闲迁移编号，增加两张窄表、必要唯一约束和外键，不改现有迁移或重写历史 project。
2. 新服务/API 先显式选择使用；缺少 binding 的旧 session 保持旧行为并被新自动恢复排除。
3. 接入连续性启动和预算恢复，随后升级 pi 源包、工具说明及协议文档。用真实二进制和隔离数据库验证跨宿主闭环；启用前确认所有连接该数据库的宿主已重启到支持绑定隔离的新二进制。
4. 对已有 SQLite 做一致性备份后再进行部署验证；本次文档批准不执行部署、版本变更或本机文件替换。
5. 首选回退为关闭新路径、在新版二进制上使用旧适配器，保留新增表，不执行破坏性逆迁移。若必须降级二进制，先停止所有同库进程并保存一致性备份，在独立恢复副本评估旧行为；不承诺旧二进制对 bound Session 的隔离。降级后的逻辑 export 不可用作连续性保真备份。
