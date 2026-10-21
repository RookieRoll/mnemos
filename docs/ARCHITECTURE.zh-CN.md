# Mnemos Memory 架构说明

本文记录 0.16.0 历史基线及 0.17.0 实现与本机交付状态，整理日期为 2026-10-21。0.16.0 基线来源为 `main@79e68e8`（完整提交 `79e68e8308c5a1887c04092292d8b62e7507c408`）。八个发布分发文件已随 0.17.0 源提交更新；release tag 与 GitHub 发布尚未执行。最终 0.17.0 本机交付构建、安装与验收记录见[交付验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-delivery.md)；此前 0.17.0-dev 候选验证作为开发阶段证据保留在归档 change 的 verification 记录中。
英文主文档见 [ARCHITECTURE.md](ARCHITECTURE.md)，接口细节见 [MCP_TOOLS.md](MCP_TOOLS.md)。

Mnemos 是运行在本机的 agent 持久记忆层：把值得长期保留的知识和明确发布的任务进度保存到 SQLite，在新会话开始或显式查询时提供受预算限制的上下文。模型负责理解、总结和判断；Mnemos 负责存储、检索、作用域校验和可靠提交。

## 0.17.0 已实现架构与本机交付状态

> 本节描述 0.17.0 的实现和本机安装状态。本机安装不代表已发布 GitHub release；在线发布仍走正式 release 流程。候选构建与最终交付验收范围分别见[开发阶段验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-flow.md)和[最终交付验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-delivery.md)。

实现与本机交付状态：对应 change 的 19 项实现任务已完成并归档。最终二进制由源提交 `3651ba2b0cd56462f8e4d8a4a991ac6811032050` 构建，版本为 `0.17.0+3651ba2b`，SHA-256 为 `DFBC17047EFB8C4A057ADF126A41DFD52B29A62945C1E0DC9180375E5B2AA3F7`，安装于 `D:/software/mnemos/mnemos.exe`。一致性更新前备份 integrity 为 `ok`、schema 为 10；显式 maintenance CLI 打开数据库后迁移至 schema 11，integrity 仍为 `ok`。二进制安装时 TOML 哈希为 `7DD145F84AB6DA731AA835B422826D492C8EC98D14C9B831FCB57AC4954E1C43`；用户授权后显式写入 core profile、maintenance 默认预算和 integration enabled 策略，最终 TOML SHA-256 为 `7A644E58B85F2FBC52D059CC15B9B4BA1DA0424EAA7E057F348E97C43730CAB9`。配置加载验证确认 effective values 为 core、maintenance enabled、24h/5s/15s/2s、integration disabled=false、continuity enabled=true。配置前态备份、Pi 本地 package 副本和四项最终二进制 legacy 比较详见[最终交付验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-delivery.md)。该本机交付不代表 release tag 或 GitHub 发布已执行。

全量 `CGO_ENABLED=0 go test -p 1 ./... -count=1`、`go vet -p 1 ./...` 和 migration/config 检查通过。最终二进制的四项真实 legacy 比较均 PASS 且无 skip；Codex app-server core/maintenance gate 均通过。Pi 扩展测试 62/62、无 skip；最终二进制 core/maintenance 工具面精确为 9/27，profile refresh 后 active surface 从 27 更新到 9。Pi 用户级 package-only 副本更新和离线 ZIP 的 CRC、逐文件 SHA、解压二进制版本及 Pi gate 均通过。所有客户端 gate 使用隔离数据库且不发模型请求；它们不代表模型采纳率或 TUI 行为效果。最终 ZIP 哈希及验证细节见[最终交付验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-delivery.md)；0.17.0-dev 候选的历史验证结果仍见[共享流程验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-flow.md)、[profile 验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-profiles.md)与[维护验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-maintenance.md)。

### 两种职责与系统层次

产品只有两种逻辑职责：**任务 Agent** 在用户分配的任务中使用知识与进度能力；**维护 Agent** 复核知识质量，并仅在明确任务或配置策略授权时执行安装、配置、备份或迁移。同一宿主 Agent 可以切换职责或委派给宿主提供的另一个 Agent。Mnemos 不内置或调用 LLM，也不依赖 Luna、OpenAI 或特定模型；需要语义判断时，由当前活跃宿主 Agent 执行。到期检查本身不授权安装、配置、备份或迁移。

```mermaid
flowchart TB
    subgraph Roles["宿主提供的两种逻辑职责"]
        TaskAgent["任务 Agent：知识与工作进度"]
        MaintAgent["维护 Agent：证据复核与获准的维护操作"]
        TaskAgent <-->|"宿主切换或委派"| MaintAgent
    end

    subgraph Adapters["协议适配层：只转换输入与输出"]
        MCP["MCP：固定工具 profile"]
        HTTP["HTTP API"]
        CLI["CLI：显式操作与维护 JSON 桥接"]
        Hooks["宿主 hook / prewarm"]
    end

    subgraph App["共享应用行为"]
        Flow["continuityflow：task-session / receipt / context 组装"]
        Due["机会式维护 due coordinator"]
    end

    subgraph Services["领域与存储服务"]
        Continuity["continuity：owner / workspace / session"]
        Context["prewarm、memory、skills：知识上下文"]
        Dream["dream / rumination：确定性维护与候选"]
        Lease["共享 owner-token lease / cycle fence"]
        SQLite[("SQLite：进度、知识及最小协调状态")]
    end

    TaskAgent --> MCP
    TaskAgent --> HTTP
    TaskAgent --> CLI
    TaskAgent --> Hooks
    MaintAgent --> MCP
    MaintAgent --> CLI
    MCP --> Flow
    HTTP --> Flow
    CLI --> Flow
    Hooks --> Flow
    Flow --> Continuity
    Flow --> Context
    Flow --> Due
    Due --> Lease
    Lease --> Dream
    Continuity --> SQLite
    Context --> SQLite
    Dream --> SQLite
```

协议层只解码和编码各自的 envelope；root 解析、receipt、context 选择和维护策略由共享应用层执行。MCP profile 在进程启动前由 TOML 决定并在整个进程生命周期固定，不通过运行中的 `tools/list` 动态切换。profile 只选择入口和职责工具面，不增加账号或 ACL。维护 lease 只协调内部确定性维护 pass，不代表任务领取或会话领取；progress 仍可按既有规则由多个会话共享，不引入单一接手人或任务锁。

### 固定工具面与职责入口

0.17.0 默认使用 `core` profile，精确暴露九个任务工具：`session_start`、`session_end`、`search`、`get_many`、`save`、`correct`、`convention`、`checkpoint`、`resume_context`。这是默认 MCP 工具面的 breaking change；依赖高级 MCP 工具的部署须在 TOML 中选 `maintenance` 并重启，该 profile 保留 23 个既有工具，`[rumination].enabled=true` 时再增加四个 rumination 工具，合计 27 个；资源另有四项只读能力。

当 MCP 固定为 `core` 时，维护 Agent 通过 `mnemos maintenance` JSON CLI 桥接读取候选证据并提交现有规则校验的 resolve/dismiss 操作。桥接不改变在线 MCP 工具列表，也不启动 Agent。候选没有活跃的宿主维护 Agent 可复核时保持 pending；Mnemos 不作语义推断或自行生成决策理由。

### task-session 生命周期与统一 receipt

```mermaid
sequenceDiagram
    participant Host as 宿主 Agent
    participant Adapter as MCP / HTTP / CLI / hook 适配器
    participant Flow as 共享 continuityflow
    participant Continuity as session / continuity 会话领域
    participant Context as Context composer
    participant Due as due coordinator
    participant DB as SQLite
    Host->>Adapter: task-session start + 显式知识标签/进度范围
    Adapter->>Flow: 规范化输入
    alt v1 verified continuity receipt
        Flow->>Continuity: 按既有验证规则打开或精确重试
        Continuity->>DB: 提交或重放 owner/workspace/session receipt
        DB-->>Continuity: 已提交的 v1 receipt
        Continuity-->>Flow: 同一 v1 receipt
    else legacy session receipt
        Flow->>Continuity: 保持 legacy session lifecycle
        Continuity->>DB: 创建或沿用 legacy session
        DB-->>Continuity: legacy session receipt（不保证绑定 owner/workspace）
        Continuity-->>Flow: 同一 legacy receipt
    end
    Flow->>Context: 按该入口适用的 receipt 组装上下文
    Context-->>Flow: context 或可恢复错误
    Flow->>Due: best-effort MaybeRun（receipt 已提交）
    Due-->>Flow: 完成、未到期、繁忙或超时
    Flow-->>Adapter: receipt + context 结果（维护不隐藏 receipt）
```

统一入口沿用调用方显式提供的知识标签 `agent_id/project` 及现有默认规则，并独立携带适用的 continuity receipt；只有 v1 verified continuity receipt 才保证 owner/workspace/session 绑定。MCP `session_start`、宿主 hook `prewarm session_start`、CLI continuity open 和明确的 HTTP session-start 都是有效 task-session 开始；普通 doctor、export、search、context、recovery、checkpoint 不是。若 context 组装或维护超时，已提交的 receipt 仍返回且不被隐藏。入口不根据当前目录、basename、历史或共享 ancestor 猜测 root；未验证的 root 继续按现有 fail-closed/unbound 规则处理。

### 机会式维护、租约与 cycle 恢复

每个有效 task-session 开始只做一次短暂 due 检查。due 判定先检查是否存在 lease 已过期的未完成 cycle；它必须优先恢复，即使 `last_success` 很近（例如较近成功后提前执行的手动 pass 失败）。只有不存在未完成 cycle 时，才按 `last_success + interval` 判定是否开启新 cycle；从未成功过时允许首次运行。仅当 `[maintenance].enabled = true`、`[integration].disabled = false` 且满足上述恢复或新周期条件时，才尝试运行确定性 dream/rumination pass。TOML 默认值为 `interval = '24h'`、`max_pass_duration = '5s'`、`lease_duration = '15s'`、`renew_interval = '2s'`；四个 duration 都可配置，必须为正数，max pass 小于 lease、renew 小于 lease，max pass 不超过 `5m`、lease 不超过 `10m`。维护预算由配置解析并作为已校验的 `time.Duration` 注入 coordinator，不复制到各 transport 或 Pi 非敏感配置投影。

```mermaid
sequenceDiagram
    participant Start as task-session start
    participant Coord as 共享 dream coordinator
    participant DB as SQLite lease/cycle 状态
    participant Dream as prune → decay → promote → rumination → journal（仅显式 pass）
    Note over Coord,Dream: 阶段计算在事务外完成；事务只包住围栏检查与该阶段数据库写入
    Start->>Coord: MaybeRun（在 receipt 提交后）
    Coord->>DB: 检查 due、claim owner-token 与 active cycle
    alt 未到期或 lease 忙
        DB-->>Coord: NotDue / Busy
        Coord-->>Start: 立即返回，不影响会话
    else 已取得 lease
        DB-->>Coord: owner + expiry + cycle_id
        loop 每个有副作用阶段
            Coord->>Dream: 在事务外计算本阶段写入计划
            rect rgb(230, 245, 255)
                Note over Coord,DB: 同一个短 SQLite 写事务仅包含围栏校验、阶段写入、复检和提交
                Coord->>DB: BEGIN IMMEDIATE；校验 owner-token、cycle ID、lease expiry
                Dream->>DB: 在该事务内执行本阶段 SQL
                Coord->>DB: 在提交前再次校验 owner-token、cycle ID、lease expiry
                Coord->>DB: COMMIT 本阶段写入
            end
        end
        Dream-->>Coord: 成功或聚合错误
        Coord->>DB: owner CAS 完成；仅全成功时更新 last_success
        Coord-->>Start: best-effort 结果，receipt 已可见
    end
```

机会式 coordinator 调用 `MaybeRun`，由 runner 执行 `Run(ctx, fence, false)`；自动维护无论是否有候选或实际变化，都不写普通 dream observation。显式 dream 保留既有 journal 行为。one-shot dream、`dream --watch` 与机会式 pass 共用同一个数据库租约。手动 pass 可以跳过 due 检查，但不能绕开互斥。session-start 的 lease claim 非阻塞；pass 运行有最大时长，续租失败或失去 owner 后取消，并停止后续阶段。所有 mutating stage 在提交事务中校验 owner-token、cycle ID 和未过期 lease；失去租约的旧 worker 不能推进后续写入或 `last_success`。到期只允许崩溃后的 at-least-once 接管，不宣称任意暂停下无重叠或普遍 exactly-once。

decay 是非幂等阶段：`cycle_id` 与 `decay_applied` marker 和 importance 更新在同一个已围栏写事务中提交。接管未完成 cycle 时沿用原 cycle；若 decay 已提交但后续崩溃，重试跳过该 cycle 的第二次 decay；完整成功后下一次 pass 才获得新 cycle。其他阶段按既有稳定身份安全重放：prune 谓词、`promoted-origin` key、rumination candidate upsert/标签关闭，以及稳定 cycle ID 的 journal。阶段写事务分别校验租约；部分阶段成功后发生失败，保留已提交效果供同 cycle 恢复，聚合错误不推进 `last_success`。如果某项副作用无法证明可安全重放，就不能纳入自动重试 pass。

### 最小持久化边界与 stdio 限制

协调表只保存必要的 `last_success`、lease owner/expiry、未完成 `cycle_id` 和 `decay_applied`；候选继续使用已有 rumination 状态。每次运行历史、诊断全文、临时上下文、prompt、空跑 observation 都不落库。只有用户任务或配置策略明确授权安装、配置或迁移时，维护 Agent 才在迁移操作前使用现有 SQLite 一致性备份能力备份；`storage.Open` 不会因每次启动自动先备份，0.17.0 也不新增每次会话快照。普通会话入口不做隐式知识迁移。

stdio MCP 只能在已经启动且收到请求时工作，不能按时唤醒宿主模型。机会定义为有效 task-session start，所以 Pi 的 CLI continuity open、host hook prewarm 等启动路径可触发；只依赖 MCP `session_start` 的部署不能代表所有宿主启动机制。没有 eligible session 时不承诺到期即运行，也不承诺跨宿主 subagent 可用。严格 wall-clock 定时只交给用户明确配置的外部 scheduler；0.17.0 不实现常驻 daemon，也不自动部署 OS task。

### 知识标签与进度身份的兼容边界

知识仍按现有 `agent_id/project` label 可见；progress 继续按已验证的 owner/workspace/session 身份隔离。两个不同 workspace 使用相同 project label 时，历史知识仍可能共享；需要新知识隔离的调用方须显式提供不同 label。此 change 不增加 project registry，不从目录名生成 hash，不自动 alias、重写或迁移旧知识，也不宣称解决 basename project 碰撞。旧知识和 v1 progress receipt 的 owner/root/CAS/retry/clear 规则分别保持兼容；新统一入口不改变 checkpoint 可见性。

## 1. 整体分层

系统以一个无 CGO 的 Go 可执行文件交付。不同宿主可以分别启动 MCP 进程，CLI 也可以独立运行；使用同一数据库路径的进程通过 SQLite 协作。HTTP 是另一种入口。

```mermaid
flowchart TB
    subgraph Hosts["宿主与调用方"]
        Codex["Codex"]
        Pi["pi 与本地扩展"]
        Other["其他 MCP 客户端"]
        HTTPClient["HTTP 客户端"]
        Operator["CLI 操作"]
    end

    subgraph Entry["入口与适配层"]
        MCP["internal/mcp：MCP 协议转换"]
        API["internal/api：HTTP 协议转换"]
        CLI["cmd/mnemos：命令与依赖组装"]
    end

    subgraph Services["服务层"]
        Memory["memory：知识、检索与版本演进"]
        Session["session：目标、总结与会话生命周期"]
        Continuity["continuity：工作区绑定与进度检查点"]
        Flow["continuityflow / prewarm：启动与上下文组装"]
        Skills["skills / dream / rumination：经验维护"]
        Safety["safety：输入与上下文安全扫描"]
    end

    Store["internal/storage：事务、查询与迁移"]
    DB[("本机 SQLite：mnemos.db + FTS5")]
    Embed["可选 Embedder 接口"]
    Provider["外部本机或远程 embedding 服务"]

    Codex --> MCP
    Pi --> MCP
    Pi --> CLI
    Other --> MCP
    HTTPClient --> API
    Operator --> CLI
    MCP --> Memory
    MCP --> Flow
    API --> Memory
    API --> Continuity
    CLI --> Memory
    CLI --> Flow
    CLI --> Skills
    Flow --> Continuity
    Flow --> Session
    Flow --> Memory
    Flow --> Skills
    Memory --> Safety
    Continuity --> Safety
    Memory --> Store
    Session --> Store
    Continuity --> Store
    Skills --> Store
    Store --> DB
    Memory -. "可选向量能力" .-> Embed
    Embed -.-> Provider
```

图中合并了部分调用关系，用于解释分层，不是逐函数调用图。MCP 和 HTTP 入口只转换协议形状；业务规则由服务实现。存储接口定义在使用它的服务包，SQLite 实现通过构造函数注入。

核心约束：保持纯 Go、无 CGO；不在记忆层内调用生成式 LLM；不引入全局状态或反射装配；不把业务决策放进协议层。可选 embedding 服务会接收提交给它的记忆文本和查询文本。

## 2. 两条记忆通道

长期知识与工作进度分别建模，避免把“正在修改某文件”当成长期事实参与检索、衰减或技能晋升。

| 通道 | 典型内容 | 保存位置 | 主要读取方式 |
| --- | --- | --- | --- |
| 长期知识 | 项目约定、架构决策、纠错、经验、偏好 | `observations`、`skills` 及关联表 | 搜索、按 ID 读取、prewarm |
| 会话记录 | 本次目标、结束总结、反思、结束状态 | `sessions` | 会话查询、受限摘要恢复 |
| 工作进度 | 当前任务、已完成事项、阻塞、下一步 | `session_checkpoints`，通过 binding 绑定工作区 | 显式恢复、启动候选提示 |

检查点由 agent 主动编写，每个会话只保留最新一份正文。它不会自动生成 observation、初始化 embedding、晋升技能，或成为“任务已验证成功”的证明。

## 3. 长期记忆如何写入

```mermaid
flowchart TD
    A["Agent 编写值得保留的知识"] --> B["MCP / HTTP / CLI 接收输入"]
    B --> C["服务校验字段、作用域与来源"]
    C --> D["安全扫描与规范化"]
    D --> E{"可选 embedding 是否可用"}
    E -->|"可用"| F["事务外请求向量"]
    E -->|"未启用或不可用"| G["继续保存文本"]
    F --> G
    G --> H["SQLite 写事务：去重或显式版本替换"]
    H --> I["保存 observation、证据与必要关系"]
    I --> J["文本变化时同步 FTS5"]
    J --> K["提交后返回结果"]
```

来源与信任等级会限制数据可见性；原始材料 `raw` 默认隔离，不能靠重要性或 pin 绕过信任过滤。向量生成在写事务之外同步进行，provider 失败不阻止保存文本，后续可显式补齐向量。

去重使用内容身份及所属作用域。带 `topic_key`、证据引用的写入也考虑这些元数据，避免把“正文相同、主题或证据不同”的记录错误合并。

知识修订通过明确的 `replaces_id` 或 `supersedes` 关系完成：同一事务插入新版本、保存证据与替代关系、使旧版本失效。旧版本保留历史身份；同主题不表示互相替代，因此规则与例外可以同时存在。

证据只保存 locator、revision、selector、可选指纹和最新检查状态。系统不自动抓取引用正文，也不把调用方报告的 `matched` 当成独立认证。

## 4. 如何检索并排序

关键词与语义检索各自产生候选，再统一排序。向量检索不依赖先命中关键词，因此没有共同关键词的记录也可能被语义召回。

```mermaid
flowchart TD
    Q["查询文本与 agent / project 等过滤条件"] --> K["FTS5 / BM25 关键词召回"]
    Q --> V{"配置允许语义检索且查询向量可用"}
    V -->|"是"| VC["筛选模型标识、维度与有限非零信号兼容的向量"]
    V -->|"否"| FB["使用关键词权重"]
    VC --> S["对兼容向量计算余弦相似度"]
    K --> U["候选合并与可用信号加权"]
    S --> U
    FB --> U
    U --> P["重要性、衰减、访问与项目因子"]
    P --> G["受同样过滤约束的图关系与矛盾因子"]
    G --> R["统一分数排序，返回 limit 条及解释字段"]
```

排序契约为：

```text
Score = clamp(relevance × policy × graph_factor × contradiction_factor, (0, 1.2])

policy = importance_weight × recency_factor × access_factor × cross_project_penalty
```

`relevance` 由归一化关键词与向量信号按可用权重合成；知识类型决定时间衰减速度，pin 固定时间因子但保留信任过滤。图扩展不会绕过 project、agent、类型、信任和有效时间等约束。

`hybrid_alpha` 默认 `0.5`。显式 `1` 使用关键词权重并跳过查询向量和语义召回；显式 `0` 在查询向量可用时使用纯语义权重，没有兼容向量的候选被排除。查询向量获取失败或信号无效时，即使配置为 `0` 也回退关键词。单条存量向量不兼容时不参与比较。

向量以 BLOB 保存在原 SQLite 文件中，余弦计算使用纯 Go。当前没有 ANN 索引，扫描成本随范围内向量数量和维度增加。模型通过可选接口外置提供，**当前不内置模型权重或推理运行时**。

读取可以分阶段进行：先搜索，再用 `mnemos_get_many` 获取选中的完整记录，或用 `mnemos_neighborhood` 浏览同会话的相邻摘要。批量详情默认预算 32 KiB、上限 256 KiB，完整条目放不下时返回 `omitted_for_size`，不截断事实正文。

## 5. 上下文如何交付

```mermaid
flowchart TD
    Start["会话启动或显式上下文请求"] --> Pinned["优先保留 pinned 核心记忆"]
    Pinned --> Mode{"是否启用连续性启动"}
    Mode -->|"是"| Progress["恢复 section：已选来源或候选提示"]
    Mode -->|"否"| Recent["普通分区：约定、最近会话、技能、纠错与热点"]
    Progress --> Sections["约定、相关技能、纠错与热点文件等 section"]
    Sections --> Scan["安全扫描与作用域过滤"]
    Recent --> Scan
    Scan --> Budget["按预算选择完整或完整紧凑条目"]
    Budget --> Context["临时组装上下文正文与实际引用"]
    Context --> Host["返回给宿主或调用 agent"]
    Context -. "普通记忆引用的最小投递记录" .-> Log[("injections")]
```

本图突出的是选取阶段，普通 section 的具体顺序由 composer 保持。连续性启动把恢复 section 放在 pinned 核心之后，并省略旧的最近会话 section；其余既有 section 的相对顺序保持。

预算估计统一使用 `ceil(UTF-8 字节数 / 4)`，包括标题、分隔符和警告；默认 prewarm 目标约 500 个估计 token。它不是某个模型 tokenizer 的精确计数。条目按完整、完整紧凑、最小引用等表示尝试，必要的安全和矛盾警告必须一起保留；放不下就省略。

候选集合、去重集合、紧凑片段、最终渲染正文和 `continuity_refs` 都是请求内数据，不另建表。普通 observation/skill 的最小投递记录只说明服务输出过引用，不证明宿主显示、模型采用或任务成功；continuity 来源不会冒充 observation/skill 写入该日志。

系统不在后台持续把其他会话的进度推入当前上下文。需要更新时由 agent 显式查询；宿主支持的 hook 入口及其注入通道另受宿主能力约束。

## 6. 切换 agent 后如何恢复会话

恢复共享进度时，接手 agent 打开自己的新会话，再读取来源检查点。来源会话 ID 用来定位进度，不能代替接手方的当前会话 ID。

```mermaid
sequenceDiagram
    participant A as 原 Agent
    participant M as Mnemos 服务
    participant D as SQLite
    participant B as 接手 Agent

    A->>M: session_start（连续性 v1，新 start_key）
    M->>D: 原子创建会话 A 与工作区 binding
    D-->>M: 提交成功
    M-->>A: 当前 session_id=A
    A->>M: checkpoint save（A，revision=0，visibility=workspace）
    M->>D: 校验 owner、工作区与版本，保存完整检查点
    D-->>M: 提交 revision=1
    M-->>A: applied 回执

    B->>M: session_start（连续性 v1，另一个 start_key）
    M->>D: 原子创建独立会话 B 与 binding
    M-->>B: 当前 session_id=B 与受限候选提示
    B->>M: resume_context（B，显式来源 A）
    M->>D: 同一读取快照内校验可见性并读取来源
    D-->>M: 检查点正文、revision 与记录时间
    M-->>B: 返回进度、来源、现场差异与核验提示
    Note over B: 核验当前文件、diff 和相关测试
    B->>M: 后续检查点保存到会话 B
```

### 6.1 同一个项目如何判定

连续性使用 `workspace_key`，由规范化的本机工作树根路径计算带版本前缀的哈希。Git 仓库根与其子目录共享身份；同名 clone、不同 worktree 默认不同。分支与 HEAD 是现场元数据，不是工作区身份。

非 Git 项目必须显式传入存在的 `workspace_root`，且根必须包含调用目录。不能用末级目录名猜测。旧的 observation `project` 标签仍按原契约工作，新增工作区身份不会重写旧记录或消除所有旧标签碰撞。

### 6.2 什么时候允许自动选来源

只有明确 `resume_intent=true`，且完整可见、经 task 精确过滤的候选集合恰好一项时，才继续检查自动恢复条件：它必须是 24 小时内的 `active` 或 `blocked` 检查点，分支和 HEAD 已知且与当前现场一致。

第二条 stale、done、异分支或摘要候选也会阻止自动选择。候选显示默认 5 项、最多 20 项，但唯一性判断不能先按显示条数截断。“最新一条”不具有自动接手权。显式选择旧来源可以返回对应警告，仍需检查当前代码；相同 HEAD 也不能证明未提交改动相同。

### 6.3 共享与可靠提交边界

owner 取 `agent_id`，默认 `default`，与 Codex/pi 等宿主名字分开。不同宿主都使用 `default` 时属于同一 owner 命名空间；这不是抵御能直接访问本机数据库进程的安全隔离。

检查点默认 `visibility=owner`，显式 `workspace` 才向同工作区其他 owner 发布该份正文。共享不会开放来源会话的私有目标、反思、其他观察或引用文件，也不会转移所有权。恢复不领取、不消费来源，多个会话可读取同一份进度。

更新使用 `expected_revision` 和稳定 `request_id`。当前请求精确重试返回原 `replayed` 回执；过期请求不能覆盖新版本。清除保留无正文的版本标记，阻止旧请求复活正文或回退到结束摘要。超时表示结果未知，应按原请求核验。检查点 `state=done` 与 session_end 是两件事。

恢复的是 agent 编写的工作状态与定位线索，不恢复完整对话、模型内部状态、终端进程或尚未保存的操作。每个会话只保留最新检查点，没有检查点历史或领取状态。

## 7. 哪些数据需要落库

持久化的判断标准是：它是否构成跨会话可复用事实、明确进度，或保证重试、版本和来源正确性所必需的状态。可重算的请求中间结果保留在内存中。

| 数据 | 是否落库 | 设计理由 |
| --- | --- | --- |
| 知识正文、类型、重要性、信任与生命周期 | 是 | 长期记忆的主体 |
| 主题、版本替代与关系边 | 是 | 保留规则/例外和显式修订链 |
| 证据定位、可选指纹、最新检查状态 | 是 | 说明来源及最近一次调用方核验 |
| 可选 embedding 向量与模型标识 | 是 | 避免每次重算，校验检索兼容性 |
| 会话目标、总结、反思、结束状态 | 是 | 保留工作记录，不在共享恢复时全部开放 |
| 工作区 binding 与启动重试身份 | 是 | 确认当前会话，避免超时后串会话 |
| 最新检查点、revision、当前请求标识/哈希 | 是 | 恢复进度与防止重复、过期写入 |
| 清除后的无正文版本标记 | 是 | 阻止旧请求与旧摘要复活 |
| 技能、使用计数、效果与纠错维护状态 | 是 | 支持经验维护与确定性晋升 |
| 维护协调状态 | 是 | 只保存 `last_success`、owner-token/lease expiry、unfinished `cycle_id` 与 decay marker；只用于维护 pass 的互斥和恢复 |
| 热点文件触碰、最小投递引用 | 是 | 支持热点与输出来源统计，不保存文件正文 |
| FTS 索引 | 是，派生结构 | 加速关键词检索，由事务触发器维护 |
| 检索候选、排序分数、恢复候选列表 | 否 | 随查询、过滤和现场变化，可重新计算 |
| prompt 的有限目标片段 | 条件性落库 | 既有 prompt hook 可把首个非 synthetic 用户 prompt 折叠空白并有限截短后补入空 `sessions.goal`；pi 也可经过此路径 |
| 完整 transcript、prompt 和工具输出原文的持续镜像 | 否 | 上述有限目标采集不等于保存完整对话；知识和检查点仍由 agent 明确编写和选择 |
| 上下文渲染块、紧凑片段、预算与去重集合 | 否 | 只服务当前请求，不重复保存知识 |
| 源 URL 或文件正文的自动镜像 | 否 | 引用只保存定位信息，不自动抓取 |
| embedding 回填游标与失败摘要 | 否 | 单次维护过程状态，下次可重新扫描 |
| 任务接手人、会话领取锁、进度消费确认 | 否 | 多会话继续共享读取进度；maintenance owner-token lease 仅协调内部确定性 pass，不是任务锁 |

### 7.1 主要实体关系

```mermaid
erDiagram
    sessions o|--o{ observations : recorded_in
    sessions ||--o| session_continuity_bindings : bound_by
    sessions ||--o| session_checkpoints : latest_progress
    observations ||--o{ observation_evidence : owns
    observations ||--o{ observation_links : source
    observations ||--o{ observation_links : target

    sessions {
        string id PK
        string agent_id
        string project
        string goal
        string summary
        string reflection
        datetime ended_at
    }
    session_continuity_bindings {
        string session_id PK,FK
        string agent_id
        string workspace_key
        string workspace_root
        string harness_kind
        string start_key
        string start_request_hash
    }
    session_checkpoints {
        string session_id PK,FK
        int revision
        string payload
        string visibility
        string last_request_id
        string last_request_hash
    }
    observations {
        string id PK
        string session_id FK
        string agent_id
        string project
        string content
        string topic_key
        datetime valid_from
        datetime valid_until
    }
    observation_evidence {
        string observation_id PK,FK
        string evidence_key PK
        string locator
        string fingerprint
        string status
    }
    observation_links {
        string source_id PK,FK
        string target_id PK,FK
        string link_type PK
        string replacement_hash
    }
```

此图省略技能、统计、索引及部分字段。每个 session 可以没有 binding 或 checkpoint；每个 observation 也可以不属于某个 session。关系、证据与检查点保存在原 SQLite 文件中，不引入额外数据库。

## 8. SQLite 并发、维护与备份

数据库使用 WAL、外键和 busy timeout，迁移通过内嵌编号 SQL 与 `schema_migrations` 管理。0.16.0 基线 schema 为 **10**；本机 0.17.0 安装已应用维护协调状态迁移 **0011**，当前 schema 为 **11**。跨进程升级时，取得立即写事务后重新检查版本，再执行 DDL 和写入迁移标记，防止多个进程重复升级。

首次 WAL 连接初始化的 Ping 阶段只对类型明确的 SQLite BUSY 错误进行有限重试；业务写入与迁移 SQL 不因此得到通用自动重试。驱动连接本身还受 busy timeout 影响，不能把应用层重试窗口当成绝对墙钟超时承诺。

迁移 0010 使 FTS 更新只在索引文本字段实际变化时执行。访问计数、信任、生命周期或效果统计变化不重建文本索引。

恢复候选在同一读取快照中扫描全部可见元数据以统计歧义，只保留 top-K 窗口，再读取这 K 条完整正文；元数据扫描成本仍随历史增长。该优化不会删除历史，也不改变唯一性判断。

embedding 回填使用有限 ID 高水位和每页 100 条的 keyset 游标，失败也推进游标。默认补缺，`--rebuild-incompatible` 显式重建不兼容模型/维度；部分失败返回非零状态，已成功更新保留，不另建持久作业表。

普通知识采用事实时间与系统时间字段，默认查询当前有效内容，可显式查询历史。`AsOf` 不是所有可变元数据的完整历史快照。pin 保留时间权重并避免 TTL 清理，但不代表绝对不可删除。

知识导出支持主题、证据与修订关系；continuity 状态不在既有逻辑导出的完整范围内。同机完整连续性备份应使用一致 SQLite 备份。清除检查点是逻辑清除，不保证删除 WAL 或已有备份中的旧正文。详见 [BACKUP.md](BACKUP.md)。

## 9. 技能、巩固与反思

技能可由 agent 主动编写，也可由 dream 进行确定性纠错聚类晋升。相同 `(agent_id, project, label)` 的 3 条及以上 live correction 可以生成模板技能，使用 `promoted-origin:<12位哈希>` 标记保持幂等。

维护 pass 按清理过期记忆 → 衰减闲置重要性 → 晋升技能 → rumination 检测及基于既有标签的确定性关闭顺序执行。只有显式 dream pass 写入 journal；自动机会维护无论是否发生变化都不写 dream observation 或 journal。rumination 保存被质疑的候选及处理理由，修订理由需要给出新方案能作出的新预测。这些流程不在记忆层调用生成式模型。

## 10. Codex 与 pi 接入

| 项目 | Codex | pi |
| --- | --- | --- |
| 工具入口 | MCP server 配置启动 `mnemos serve` | 本地 package 经 MCP adapter 暴露工具 |
| 生命周期 | 仅配置 MCP 不代表有自动启动 hook；agent 按 skill 显式调用 | 扩展处理启动、reload、new、resume、fork 等事件 |
| 连续性开启 | agent 按 skill 显式调用；MCP 注册本身不隐含自动启动 | `[continuity].enabled = true` 后扩展按 package `mcp.json` 路由自动启动 |
| 自动集成关闭 | `[integration].disabled = true` 关闭 Mnemos hooks/prewarm，显式 MCP 工具仍可调用 | 同一文件开关控制扩展自动行为 |
| 非 Git 根目录 | 每次 continuity 调用显式传 `workspace_root` | Task Agent 可显式打开 continuity session；pi hooks 不会自动接管该 session |
| 上下文交付 | 工具返回由 agent 使用，取决于宿主接入 | 启动块通过 `before_agent_start` 的 `systemPrompt` 交付 |
| 扩展更新 | 新 MCP 进程加载新二进制 | 复制 package 到 pi 用户目录后 reload；本地目录包不由 `pi update` 下载更新 |

pi 使用已安装 package 中的 `mcp.json` 作为 MCP 与 hook 的共同可执行文件/配置路由。离线本地副本位于 `~/.pi/agent/local-packages/mnemos` 时，更新需重新复制 package 的运行文件并 reload；`pi update` 不会下载或覆盖本地目录副本。

pi 的 `tool_call` 是阻断/放行通道，不支持通过返回 system prompt 注入记忆。自动连续性启动结果未知时，扩展保持未绑定状态，不猜测其他 session；安全 guardrail 仍执行，依赖 session 的自动事件暂不发送，不排队补放。

0.17.0 的默认 `core` profile 提供精确 9 个工具；显式 `maintenance` profile 保留 23 个既有工具，并在开启 rumination 时增加 4 个工具（共 27 个）。两种 profile 都提供 4 个只读资源。磁盘二进制替换后，已有 MCP 进程仍可能运行旧程序；启用连续性前，应重启同一数据库的所有旧宿主进程。

### 10.1 配置选择与工作区诊断

CLI 使用标准 `~/.mnemos/config.toml`，也可在命令前通过 `mnemos --config <绝对路径> <命令>` 选择配置。相对 storage、vault 和 client 路径以该 TOML 所在目录为基准；Mnemos 产品开关不从旧 `MNEMOS_*`、`CLAUDE_CONFIG_DIR`、`PI_CODING_AGENT_DIR` 或 `APPDATA` 环境变量读取。字段、默认值和限制见[配置参考](CONFIGURATION.md)。

非 Git 项目由 Task Agent 根据用户明确的项目上下文、目录结构和相关构建文件判断边界，并在当前 invocation 显式传 `workspace_root`。单个 manifest 不是项目边界的证明；若多个模块或边界仍不明确，agent 应列出候选并暂不绑定。Mnemos 不创建 workspace registry、不添加必需 marker，也不把上次会话的历史根路径当作新会话的默认值。Maintenance Agent 可以按用户任务显式声明被维护项目的根；同样不维护全局 workspace 清单。

```mermaid
flowchart TD
    Invocation["Task Agent / Maintenance Agent 当前任务"] --> Boundary["依据用户任务与宿主上下文判断项目边界"]
    Boundary -->|"Git worktree"| GitRoot["使用已验证 worktree 根"]
    Boundary -->|"non-Git 且清楚"| Explicit["本次调用显式 workspace_root"]
    Boundary -->|"仍有歧义"| Ask["展示候选，暂不绑定"]
    GitRoot --> Start["连续性 session_start"]
    Explicit --> Start
    Start --> Receipt["保留回执中的当前 session/root"]
    Receipt --> Next["同一会话后续显式携带"]
    Next --> NewSession["下一会话重新声明根目录"]
```

`mnemos doctor --workspaces` 是单独的只读报告路径：在进入普通 doctor 的配置创建、可写数据库初始化和客户端检查前分流；只读取已登记 binding 与聚合计数，只 probe 记录的根。WAL 模式要求已经存在 `-wal` 与 `-shm`；诊断可能更新已有 SHM reader mark，但不会创建/删除 sidecar 或修改主 DB/WAL 字节。物理路径相等只说明当前文件系统等价，不证明历史绑定可安全合并。详见[工作区诊断](WORKSPACE_DIAGNOSTICS.md)。

0.16.0 安装构建的历史基线为 `0.16.0+79e68e8`，源提交 `79e68e8308c5a1887c04092292d8b62e7507c408`，安装二进制 SHA-256 为 `c43ca3982d8e3e96e67145a4e36c702adc2be9f6488e1b6eab01df83498ebf89`。其 Pi 扩展测试 62/62 通过；对应真实 Pi 与 Codex 门禁和 package-only 更新记录见[0.16.0 部署与 Pi 验收](../openspec/changes/archive/2026-10-21-file-based-runtime-configuration/verification-delivery.md)。当前 0.17.0 本机安装及最终二进制验证见本节开头和[交付验证](../openspec/changes/archive/2026-10-21-simplify-agent-memory-workflow/verification-delivery.md)；Pi package-only 更新和离线 ZIP 均已验收。

## 11. 阅读入口

| 需要了解的内容 | 文档 |
| --- | --- |
| 英文架构与完整评分公式 | [ARCHITECTURE.md](ARCHITECTURE.md) |
| MCP 输入、回执、限制与错误 | [MCP_TOOLS.md](MCP_TOOLS.md) |
| 上下文预算与投递来源 | [CONTEXT_PROTOCOL.md](CONTEXT_PROTOCOL.md) |
| 向量 provider、兼容性与维护 | [EMBEDDINGS.md](EMBEDDINGS.md) |
| TOML 字段、默认值与迁移 | [CONFIGURATION.md](CONFIGURATION.md) |
| 工作区诊断分类与安全边界 | [WORKSPACE_DIAGNOSTICS.md](WORKSPACE_DIAGNOSTICS.md) |
| 共享进度的设计取舍 | [共享会话连续性设计](../openspec/changes/archive/2026-10-19-add-shared-session-continuity/design.md) |
| 并发升级、检索及读取优化 | [运行时加固设计](../openspec/changes/archive/2026-10-20-harden-local-memory-runtime/design.md) |

内置向量模型仍属于后续规划，不在当前已实现架构内。跨机器同步、完整对话回放、单一接手人领取锁、后台持续注入和多租户 ACL 也不属于本文所述能力。
