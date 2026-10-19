# Tasks

本清单用于后续实施；规划文档完成不代表以下任务已执行。需求依据为本目录四份 delta specs，设计决策见 [design.md](design.md)。各组包含本组需要的测试和文档，最后一组仅验证跨模块闭环。

## 1. 工作区与精确会话身份

- [x] 1.1 实现只读工作区解析器及明确的输入边界；表驱动测试验证同名目录隔离、子目录归一、不同 worktree、合法链接/大小写等价、非 Git 显式根及失效路径，并验证 Git 环境覆盖不能错误绑定其他根。
- [x] 1.2 添加下一个可用的增量迁移，建立 session binding 和 latest checkpoint 窄表、唯一约束及级联关系；迁移测试覆盖空库、当前库重复启动和旧 session 未绑定，确认历史 project 和 observation 不变。
- [x] 1.3 实现 continuity open 的事务和 start-key 重试身份；使用两个独立数据库连接测试重复启动只有一个 Session、新 key 创建独立 Session、跨 harness 不混用以及已关闭目标/冲突输入不重开。
- [x] 1.4 为新版二进制的 legacy adopt/Resolve/裸 host ID 匹配、newest/twin close 和 stale sweep 排除 continuity-bound Session；混用测试证明旧客户端不会反向采用或关闭 bound Session，同时 legacy-only 行为及正常列表/备份不变。
- [x] 1.5 在 `docs/ARCHITECTURE.md` 说明新增工作区身份仅约束连续性路径及旧数据兼容边界；逐条对照 identity/start 规格和测试结果确认无全局项目迁移承诺。

## 2. 检查点写入、清除与提交回执

- [x] 2.1 实现 continuity payload 类型、字段白名单、原始/规范对象大小限制与原文和净化后安全检查；测试中文、未知字段、嵌套超限、恶意文本及敏感原始字段不被自动采集，失败时原正文和日志不泄漏新增输入。
- [x] 2.2 实现精确 owner/workspace/session 校验和最新版本完整替换；测试共享可读但不可由其他 owner 改写、无效 ID 不回退、同 workspace 并行 Session 不串写，且不修改最终 summary/reflection。
- [x] 2.3 实现固定版本的请求规范化、CAS 和提交后 applied/replayed 回执；独立连接竞争、提交前取消、提交后丢 ACK、相同 request ID 不同正文/visibility、旧请求晚到等测试证明不覆盖新版本。
- [x] 2.4 实现带 CAS 的 clear 和保留版本标记；测试关闭后 clear、旧请求不能复活正文、revision 不重置、重复 clear 精确回执、owner 读取 cleared 状态和删除 Session 的级联清理。
- [x] 2.5 测试 checkpoint 与 Close 的事务竞争、关闭后拒绝新正文但允许当前精确重试；通过依赖 spy 确认这些路径不初始化 embedding、不新建 memory/注入/成功事件、不执行引用或 next_steps。
- [x] 2.6 文档化 16 KiB 正文、每会话最新一份、无 TTL/历史以及清除不代表物理安全擦除的边界；检查规范、公开错误码和测试使用同一套限制。

## 3. 共享候选与显式恢复读取

- [x] 3.1 实现 owner 默认与显式 workspace 发布、读取快照及引用只返回标识；测试跨 owner 私有正文/数量不可见、撤回共享、读取与 clear 竞争、私有 observation 引用不能被自动解引用。
- [x] 3.2 实现绑定 workspace 下的候选聚合与同 owner 结束摘要投影；测试旧未绑定记录排除、checkpoint 优先、clear 不回退摘要、当前 Session 排除、摘要不推断待办或读取 Reflection。
- [x] 3.3 实现显式来源和精确 task 过滤、稳定排序及全量可见候选的唯一性判断；测试无效显式来源不回退、limit/字节省略不消除歧义、fresh 与 stale/done/summary 混合时不隐式选择。
- [x] 3.4 实现本次调用现场采样、24 小时 stale 标记与 resume-intent 选择规则；固定时钟和临时 Git 仓库测试 branch/HEAD 变化、unknown、非 Git、done、无恢复意图及显式选择旧来源的可见警告。
- [x] 3.5 实现完整 JSON 响应预算和上限；测试候选/引用/警告/信封都计费、完整对象省略有明确 metadata、多个接收会话重复读取不创建领取或消费状态。
- [x] 3.6 在 `docs/ARCHITECTURE.md` 补充选择算法和保守歧义规则，给出两个并行任务及一次明确切换的例子；检查示例输入输出能由本组测试重现。

## 4. 预算上下文与来源标识

- [x] 4.1 接入 opt-in continuity prewarm，显式传 owner，省略旧 recent-session section，保持 pinned 优先及其他 sections 相对顺序；测试旧未绑定/同名其他工作区摘要不能旁路进入恢复块。
- [x] 4.2 实现完整/紧凑恢复表示和非累积候选提示；固定预算测试中文、来源 ID/时间/revision、安全警告、stale/workspace-changed 提示及核验指令全部计费，不够时整项省略。
- [x] 4.3 返回仅实际交付的 continuity_refs，并保留普通 observation/skill 的既有日志语义；测试候选或 size-omitted 正文不被记成交付，不写 checkpoint injection/消费记录，也不持久化渲染块。
- [x] 4.4 在上下文协议文档说明恢复上下文与当前会话 compaction recovery 的区别；验证文档示例、最终 refs 与实际渲染条目一致，默认未启用模式保持原格式。

## 5. 协议、工具说明与兼容边界

- [x] 5.1 实现无副作用的 continuity capabilities 和新 CLI open/checkpoint/context 接口；测试查询不创建配置或 Session、命令的参数/版本/错误码、明确 unsupported 与 open 后 outcome_unknown 的区别，并更新 CLI 帮助与协议示例。
- [x] 5.2 为 MCP 增加 continuity v1 的 session-start 输入及 checkpoint/resume-context 工具；验证生成 schema、actual tool naming、错误回执和不支持版本，并更新 `docs/MCP_TOOLS.md`。
- [x] 5.3 增加 HTTP 与 `pkg/client` 的薄适配；同一组 fixture 对比 CLI/MCP/HTTP/Go client 的选中来源、scope、revision、预算和错误语义，并更新对应 API 文档。
- [x] 5.4 使 open 在提交后以精确 Session 调用不创建/adopt 的 composer；注入 context 失败和畸形/丢失 ACK，验证提交身份仍可通过原 start_key 确认且不生成第二个 Session。
- [x] 5.5 在既有各宿主写边界识别中加入 checkpoint 工具，更新 skill 的阶段保存、显式发布与执行前核验指引；测试直连/包前缀命名，同时以真实 pre-change binary 验证未启用连续性的 Claude stdout/stderr/exit 兼容。
- [x] 5.6 为逻辑 export 添加不污染 JSON 的 continuity omission 提示及检测失败说明，保留旧 import 语义；测试未导出状态被明确说明、旧 dump 不覆盖本地版本，同机 SQLite 一致性备份恢复后精确重试身份仍有效，并更新 `docs/BACKUP.md`。

## 6. pi 连续性接入

- [x] 6.1 实现 `MNEMOS_CONTINUITY=1` opt-in、能力检查和每次 session_start 的 start_key/内存代次；扩展测试覆盖 startup/reload/new/resume/fork、未启用旧路径、disabled 和经确认不支持时的降级。
- [x] 6.2 实现迟到响应隔离与结果未知处理；用 fake pi.exec 测试旧结果不污染新 Session、open 已提交但回执畸形不 fallback、open 成功而 prewarm 失败仍保留当前 ID。
- [x] 6.3 实现 unknown-start 的明确 unbound 状态及 guardrail-only 路径；并行会话测试覆盖后续 prompt/touch/compaction/shutdown 不发送空 ID、不误改或关闭其他 Session，安全扫描仍阻止高风险输入，确认身份后不补放旧事件。
- [x] 6.4 通过 before_agent_start 投递有界恢复材料，并支持显式工具继续读取；测试非累积、候选不自动变成选中任务、tool_call 不改 system prompt、其他会话新进度不后台推送。
- [x] 6.5 更新 pi skill/README 的发布 checkpoint、任务选择、手动核验和旧二进制降级说明；验证实际工具前缀可调用、显式写入错误对 agent 可见，shutdown 不声称生成检查点，已安装本地复制包的更新流程不依赖仓库直连；明确启用前同库旧进程需重启，新旧二进制共库不在支持范围。

## 7. 跨模块验收与收益核验

- [x] 7.1 在隔离 SQLite 和临时工作区运行真实二进制场景：A 发布中途 checkpoint，B/C 各自新会话共享读取；验证来源不被领取、未提交更新在异常退出后不可恢复、两个同名项目和两个 worktree 不串数据。
- [x] 7.2 对已有结束摘要与新 checkpoint 恢复准备相同任务基线，记录到首次有效行动的读取次数、重复探索量和遗漏阻塞；自动测试使用确定性 fixture，真实宿主观察记录版本/样本/局限，不用访问计数推导效率百分比。
- [x] 7.3 执行相关 Go/Node 测试、全量 Go 测试及 vet、格式检查和 manifests 一致性；Windows 无法执行的 race/lint 明确交给可用 CI，不以跳过测试作为通过。
- [x] 7.4 独立审查实现与全部 delta scenarios，修复问题后运行 OpenSpec 严格校验；确认文档和实测结果一致，不把规划完成写成已部署或已发布。

## Workflow follow-up

- 所有实施任务完成并通过审查后，再同步主规格和归档本变更；规划阶段保持 active。
- 归档、版本发布、本机二进制/插件替换分别按届时确认的操作范围执行；本清单不把这些操作视为已完成。
