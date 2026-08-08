package store

import (
	"encoding/json"
	"fmt"
	"log"

	"gorm.io/gorm"

	"github.com/helloxz/zacp/internal/acp/client"
	"github.com/helloxz/zacp/internal/model"
	"github.com/helloxz/zacp/pkg/eventstore"
)

// migrateV6 messages 表新增 tool_details 列（工具调用详情），并把历史 events 中的
// 工具 input/output 一次性回填拆分：
//   - events 改写为瘦身版（tool_call 系列事件去掉 input/output，保留 type/toolId/
//     title/status，前端仍能重建工具卡时间线）；
//   - tool_details 保存每工具最终一份详情（toolId → {input, output}），
//     历史消息列表体积可瘦身约 90%（此前每次 tool_call_update 都落全量快照）。
//
// 容错与失败语义：
//   - 单行 events JSON 损坏（理论不发生）→ 跳过该行并告警，不阻塞启动，
//     前端对无详情卡片已有「纯标题行」兜底；
//   - 结构类/回填失败 → 返回错误，runMigrations 外层事务整体回滚并拒绝启动，
//     下次启动自动重试，不会出现半新半旧 schema。
func migrateV6(db *gorm.DB) error {
	// 1) 加列：SQLite ALTER TABLE ADD COLUMN 为纯元数据操作，秒级、不重写表
	if err := db.AutoMigrate(&model.Message{}); err != nil {
		return fmt.Errorf("add tool_details column: %w", err)
	}

	// 2) 回填：逐行解析 events → 拆分 → 双列改写（迁移事务由 runMigrations 外层统一管理）
	type messageRow struct {
		ID     uint
		Events string
	}
	var rows []messageRow
	if err := db.Model(&model.Message{}).
		Where("events != ''").
		Order("id").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("load messages for backfill: %w", err)
	}

	backfilled, skipped := 0, 0
	for _, row := range rows {
		var events []client.Event
		if err := json.Unmarshal([]byte(row.Events), &events); err != nil {
			skipped++
			log.Printf("[migrate v6] message %d events 解析失败，跳过回填: %v", row.ID, err)
			continue
		}
		slim, details := eventstore.SplitToolDetails(events)
		if len(details) == 0 {
			continue // 无工具事件，无需改写
		}
		if err := db.Model(&model.Message{}).
			Where("id = ?", row.ID).
			Updates(map[string]any{
				"events":       eventstore.Marshal(slim),
				"tool_details": eventstore.MarshalDetails(details),
			}).Error; err != nil {
			return fmt.Errorf("backfill tool_details for message %d: %w", row.ID, err)
		}
		backfilled++
	}

	// 3) 校验：回填结果抽查，失败则整体回滚（见文件头注释）
	if err := verifyToolDetailsBackfill(db, backfilled); err != nil {
		return err
	}

	log.Printf("[migrate v6] backfill done: rows=%d backfilled=%d skipped=%d", len(rows), backfilled, skipped)
	return nil
}

// verifyToolDetailsBackfill 迁移后校验（轻量抽查，不逐行全量比对）：
//  1. tool_details 非空行数 = 回填成功行数（损坏跳过行无详情，两边口径一致）；
//  2. 抽查前 3 行：tool_details JSON 可解析，且 events 中 tool_call 系列事件
//     已无 input/output 字段（空 toolId 的旧事件例外——SplitToolDetails 不处理
//     空 toolId，前端同样跳过，不参与校验避免误报）。
func verifyToolDetailsBackfill(db *gorm.DB, backfilled int) error {
	var count int64
	if err := db.Model(&model.Message{}).
		Where("tool_details != ''").
		Count(&count).Error; err != nil {
		return fmt.Errorf("verify tool_details count: %w", err)
	}
	if count != int64(backfilled) {
		return fmt.Errorf("verify tool_details count mismatch: db=%d backfilled=%d", count, backfilled)
	}

	type sampleRow struct {
		ID          uint
		Events      string
		ToolDetails string
	}
	var samples []sampleRow
	if err := db.Model(&model.Message{}).
		Where("tool_details != ''").
		Order("id").
		Limit(3).
		Find(&samples).Error; err != nil {
		return fmt.Errorf("verify tool_details samples: %w", err)
	}
	for _, s := range samples {
		var details map[string]eventstore.ToolDetail
		if err := json.Unmarshal([]byte(s.ToolDetails), &details); err != nil {
			return fmt.Errorf("verify tool_details JSON for message %d: %w", s.ID, err)
		}
		var events []map[string]any
		if err := json.Unmarshal([]byte(s.Events), &events); err != nil {
			return fmt.Errorf("verify events JSON for message %d: %w", s.ID, err)
		}
		for _, ev := range events {
			if ev["type"] == "tool_call" || ev["type"] == "tool_call_update" {
				// 空 toolId 的旧事件未被瘦身（SplitToolDetails 以 toolId 为键），跳过不误报
				if toolID, _ := ev["toolId"].(string); toolID == "" {
					continue
				}
				if _, has := ev["input"]; has {
					return fmt.Errorf("verify message %d: events still contains input", s.ID)
				}
				if _, has := ev["output"]; has {
					return fmt.Errorf("verify message %d: events still contains output", s.ID)
				}
			}
		}
	}
	return nil
}
