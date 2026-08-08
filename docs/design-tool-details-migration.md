# 设计：消息工具详情瘦身（tool_details 拆分）与自动迁移

> 状态：已批准设计，待实现
> 关联问题：历史消息列表 `GET /api/v1/sessions/:id/messages` 的 `events` 字段携带全量工具
> input/output（含多次 `tool_call_update` 的全量重复快照），列表 payload 臃肿（实测单库
> 534 行消息、约 90% 体积来自工具详情），且后端每次读取都要整串透传。
> 方案：**b1 —— 走一次轻量迁移，把工具详情拆到独立列，events 落库时瘦身**。

---

## 1. 结论先行：升级后自动迁移

**能自动完成，无需用户任何手工操作。**

本项目已有版本化迁移框架（`backend/internal/store` 的 `runMigrations` +
`schema_migrations` 表，现有 v1–v5）。`store.Open` 在进程启动时按版本顺序执行未应用
的迁移，失败则拒绝启动。因此：

- 用户升级新版本二进制 → 首次启动 → 自动执行 `migrateV6`（加列 + 回填 + 校验）→ 正常提供服务。
- 迁移事务化：中途失败整体回滚，下次启动重试；成功后在 `schema_migrations` 记 v6，之后不再重复执行。
- 结构类失败（加列失败）拒绝启动并打印明确错误，不会出现半新半旧 schema。
- 回填类容错见 §6：单行坏数据跳过、不阻塞启动。

这与此前 v2–v5 加列迁移的体验完全一致，用户侧零感知（启动耗时增加毫秒～秒级）。

---

## 2. 目标与非目标

**目标**

1. 历史消息列表接口 payload 瘦身约 90%（去掉工具 input/output 的重复快照）。
2. 后端列表读取零额外解析（不再需要为剥离详情而解析 events）。
3. 实时流式路径（WebSocket）**完全不变**：实时卡仍展示 input/output，不受影响。
4. 升级自动、幂等、可回滚。

**非目标**

- 不新增「工具详情懒加载端点」：拆分后 `tool_details` 已是每工具一份最终
  input/output 的最小完整数据（前端已有 50000 字符截断 + `max-h` 滚动防撑爆），
  随列表直接返回即可，省去二次请求与缓存复杂度。
- 不改 WebSocket 实时协议（`WsEvent` 的 `input/output` 字段保留，仅落库时拆分）。

---

## 3. 数据模型变更

### 3.1 新列：`messages.tool_details`

`model.Message` 增加字段：

```go
// ToolDetails 工具调用详情（toolId → {input, output}），与 events 同步写入。
// events 落库时已剥离 input/output；本字段保存每工具最终一份详情，供前端展开工具卡。
ToolDetails string `gorm:"type:text" json:"toolDetails"`
```

JSON 结构：

```json
{
  "tool_123abc": { "input": { "...": "..." }, "output": "..." },
  "tool_456def": { "input": "ls -la", "output": "total 8..." }
}
```

- key：`toolId`（与 `events` 中 `tool_call` 事件的 `toolId` 对应）。
- `input`/`output`：与 ACP 事件原始值一致（对象或字符串原样保存）。
- 合并语义：同一 toolId 多次 `tool_call_update` 只保留**最终**一份（与前端工具卡最终展示一致，
  这正是 90% 瘦身的主要来源——现在 events 里每次 update 都带全量重复快照）。

### 3.2 events 落库瘦身规则

`tool_call` / `tool_call_update` 事件落库时**删除 `input`、`output` 字段**，
其余字段（`type`、`toolId`、`title`、`status`）全部保留——前端
`deriveBlocks` 需要这些字段重建工具卡的时间线位置与状态。

> 注意：`agent_thought`、`plan`、`agent_message` 等其他事件不受影响；
> `user_message` 事件同样原样保留。

---

## 4. 后端改动

### 4.1 新增序列化/拆分工具（单一来源）

新建 `backend/pkg/eventstore/eventstore.go`（轻量复用包，AGENTS.md 目录树约定），集中封装：

```go
// SplitToolDetails 拆分事件列表：抽取每工具最终 input/output 到 details，
// 返回瘦身版事件列表（tool_call 系列事件去掉 input/output）。
func SplitToolDetails(events []client.Event) (slim []client.Event, details map[string]ToolDetail)

// Marshal 序列化 events 为落库 JSON（空列表返回 ""），供两处落库复用。
func Marshal(events []client.Event) string
```

理由：WS 落库（`bridge.go`）与 REST 落库（`service.go`）是两条独立路径，
目前各写一份 `marshalEvents`/`json.Marshal`；拆分逻辑必须两处共用，否则极易漂移。
放 `internal/pkg` 符合项目「可被多处复用的轻量工具库」定位。

### 4.2 写入路径

| 位置 | 现状 | 改动 |
|------|------|------|
| `internal/ws/bridge.go`（WS 路径，约 L502） | `Events: marshalEvents(result.Events)` | `slim, details := eventstore.SplitToolDetails(result.Events)`；`Events: eventstore.Marshal(slim)`，`ToolDetails: eventstore.MarshalDetails(details)`（空时存 `""`） |
| `internal/service/service.go`（REST 路径，约 L407） | `Events: eventsJSON` | 同上，共用 `eventstore` 包 |

`MarshalDetails` 对空 map 返回 `""`，与现有 `Events` 空串约定一致。

### 4.3 读取路径

`GET /api/v1/sessions/:id/messages`（`service.GetMessages` → `Message` model）**零改动**：
新增 `toolDetails` 字段随 `json:"toolDetails"` 自动出现在响应中，前端直接消费。
payload 瘦身效果 = events 去掉 input/output 后的大小（见 §7 估算）。

---

## 5. 前端改动

### 5.1 类型

`frontend/src/types/models.ts` 的 `ChatMessage` 增加：

```ts
/** 工具调用详情（toolId → {input, output}），历史消息展开工具卡用；旧数据/迁移跳过行可能缺失 */
toolDetails?: Record<string, { input?: unknown; output?: unknown }>
```

### 5.2 历史渲染：`deriveBlocks` 合并详情

`frontend/src/composables/useMessageBlocks.ts` 的 `deriveBlocks(events)` 增加可选参数
`toolDetails`：

```ts
export function deriveBlocks(events: WsEvent[], toolDetails?: ChatMessage['toolDetails']): MessageBlock[]
```

构建 tool block 时：

- `toolDetails?.[toolId]` 存在 → `card.input/output` 取详情值；
- 缺失 → **回退**到事件内嵌的 `input/output`（兼容未迁移旧库 / 迁移跳过的坏行 / 旧后端 + 新前端场景）。

流式路径（`streamBlocks` / `upsertToolCard`）不改：实时事件仍带 `input/output`。

### 5.3 调用方

`MessageItem.vue`（或 store 中调用 `deriveBlocks` 处）传入 `message.toolDetails`。
`ToolCallCard.vue` 零改动：`hasDetail`/惰性 `details` computed 已兼容「无详情 → 纯标题行」。

### 5.4 兼容矩阵

| 前端 | 后端 | 行为 |
|------|------|------|
| 新 | 新（已迁移） | 正常：工具卡展开显示详情 |
| 新 | 旧（未迁移） | 正常：`toolDetails` 缺失，回退 events 内嵌 input/output |
| 旧 | 新（已迁移） | 工具卡仅标题行（无详情可展开，按钮禁用）——旧前端本就有该分支，不报错；生产端前后端同版本发布无此窗口 |

---

## 6. 迁移实现细节（migrateV6）

位置：`backend/internal/store`，沿用现有 `runMigrations` 框架，追加：

```go
func migrateV6(db *gorm.DB, log *slog.Logger) error
```

### 6.1 加列（结构变更）

- `model.Message` 已加 `ToolDetails` 字段 → `db.AutoMigrate(&model.Message{})`。
- SQLite `ALTER TABLE ADD COLUMN` 为纯元数据操作，不重写表、不锁读，秒级完成。
- 失败 → 返回错误 → 拒绝启动（与 v2–v5 相同模式）。

### 6.2 回填（事务内）

```
事务 begin
  SELECT id, events FROM messages WHERE events != ''
  逐行：
    json.Unmarshal(events) → []client.Event
    解析成功：
      复用 eventstore.SplitToolDetails 抽取详情 + 瘦身
      UPDATE messages SET tool_details = ?, events = ? WHERE id = ?
    解析失败（数据损坏，理论不发生）：
      log.Warn 记录 id，跳过该行（tool_details 保持 NULL，events 不动）
      不阻塞迁移 —— 前端对无详情卡片已有兜底（标题行），
      且事件流本身不依赖详情即可重建时间线
  COMMIT
```

事务保证：中途 DB 错误整体回滚 → 启动失败 → 下次启动重试，不会出现半新半旧。

### 6.3 校验

回填完成后执行（仍在迁移内）：

1. 计算 `tool_details` 非空行数，与含 `tool_call` 事件的 events 行数对比，差异 > 0 时 log.Warn（仅告警，不阻塞——差异行即解析跳过行）。
2. 抽查 3 行：`tool_details` JSON 可解析、key 与 events 中 toolId 集合一致、events 中已无 `input`/`output` 字段。

### 6.4 幂等与安全

- `schema_migrations` 记录 v6 后不重复执行。
- 迁移前**必须**先备份（运维动作，见 §8），理论上即使需要回退也可整体恢复。

---

## 7. 效果估算

实测库 534 条消息。events 中每个工具调用的体积构成：`tool_call` 一次全量 input/output
+ 多次 `tool_call_update` 全量 output 重复快照。拆分后：

- 列表接口 payload：`tool_details`（每工具最终一份）≈ 原 events 体积的 **10%** 量级；
- 后端零额外解析（原来也不需要解析，但不再透传无用字节）；
- 前端展开工具卡的数据来源从「解析整串 events」变为「按 toolId 取 map 键」，且只发生在展开时（惰性，现状已是）。

---

## 8. 备份与回滚

WAL 模式下**禁止直接 `cp zacp.db`**（WAL 中可能有未合并数据），正确备份：

```bash
sqlite3 ~/.zacp/data/zacp.db ".backup ~/.zacp/data/zacp.db.bak"
```

（或先 `PRAGMA wal_checkpoint(TRUNCATE)` 再 cp。）

回退：停服 → 用 `.bak` 覆盖 `zacp.db`（及删除 `-wal`/`-shm` 附属文件）→ 启动。
表结构与数据整体回到迁移前，无半新半旧。

---

## 9. 测试与验收

### 单元测试

1. `SplitToolDetails`：含 `tool_call` + 多次 `tool_call_update` 的事件列表 →
   断言 details 为最终值、slim 事件无 input/output、非工具事件原样保留。
2. 迁移回填：构造含工具事件的旧数据行 → 跑回填函数 → 断言拆分正确、空 events 行跳过、
   非法 JSON 行跳过不报错。
3. 幂等：`schema_migrations` 已含 v6 时 `runMigrations` 不再执行 v6。

### 手工验收（模拟升级）

1. 用旧版本启动并产生若干含工具调用的会话。
2. 备份 DB（`.backup`）。
3. 换新版本启动 → 日志出现 `migrate v6` 记录 → 启动成功。
4. 打开旧会话：工具卡标题/状态正确、展开显示 input/output；消息时间线顺序不变。
5. 对比迁移前后 `GET /sessions/:id/messages` 的响应字节数（预期大幅下降）。
6. 新会话实时流式工具卡展示正常（WS 协议未动）。

---

## 10. 实施清单

| # | 改动 | 位置 |
|---|------|------|
| 1 | `Message` 加 `ToolDetails` 字段 | `backend/internal/model/models.go` |
| 2 | 新增 `eventstore` 包（SplitToolDetails / Marshal / MarshalDetails） | `backend/pkg/eventstore/` |
| 3 | WS 落库改走 eventstore | `backend/internal/ws/bridge.go` |
| 4 | REST 落库改走 eventstore | `backend/internal/service/service.go` |
| 5 | `migrateV6`（加列 + 回填 + 校验） | `backend/internal/store/` |
| 6 | `ChatMessage` 加 `toolDetails` | `frontend/src/types/models.ts` |
| 7 | `deriveBlocks` 合并详情 + 回退 | `frontend/src/composables/useMessageBlocks.ts` |
| 8 | 调用处传 `message.toolDetails` | `frontend/src/components/chat/MessageItem.vue` |
| 9 | 单测（拆分、迁移、幂等） | `backend/pkg/eventstore/`、`backend/internal/store/` |
| 10 | API 文档补充 `toolDetails` 字段说明 | `docs/API.md` |

实施顺序：1 → 2 → 3/4 → 5 → 9（后端闭环）→ 6 → 7/8（前端）→ 10。
后端先行且可独立验证：迁移 + 落库拆分 + 单测全绿后，前端再消费新字段。
