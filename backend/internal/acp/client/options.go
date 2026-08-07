package client

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/helloxz/zacp/internal/model"
)

// ToConfigOptionDTOs 将 SDK 的 SessionConfigOption 转为对外 DTO（select/boolean 变体展开）。
// 供 service（创建会话落库）与 ws（update 通知落库）复用。
func ToConfigOptionDTOs(opts []acp.SessionConfigOption) []model.ConfigOptionDTO {
	out := make([]model.ConfigOptionDTO, 0, len(opts))
	for _, opt := range opts {
		if sel := opt.Select; sel != nil {
			dto := model.ConfigOptionDTO{
				ID:           string(sel.Id),
				Name:         sel.Name,
				Type:         "select",
				CurrentValue: string(sel.CurrentValue),
			}
			if sel.Description != nil {
				dto.Description = *sel.Description
			}
			if sel.Category != nil {
				dto.Category = string(*sel.Category)
			}
			dto.Options = flattenSelectOptions(sel.Options)
			out = append(out, dto)
		} else if b := opt.Boolean; b != nil {
			dto := model.ConfigOptionDTO{
				ID:           string(b.Id),
				Name:         b.Name,
				Type:         "boolean",
				CurrentValue: b.CurrentValue,
			}
			if b.Description != nil {
				dto.Description = *b.Description
			}
			if b.Category != nil {
				dto.Category = string(*b.Category)
			}
			out = append(out, dto)
		}
	}
	return out
}

// ToAvailableCommandDTOs 将 SDK 的 AvailableCommand 转为对外 DTO。
// 供 bridge 处理 available_commands_update 通知时落库 + 广播复用。
func ToAvailableCommandDTOs(cmds []acp.AvailableCommand) []model.AvailableCommandDTO {
	out := make([]model.AvailableCommandDTO, 0, len(cmds))
	for _, c := range cmds {
		dto := model.AvailableCommandDTO{Name: c.Name, Description: c.Description}
		// input.hint 在 unstructured 变体中携带，用于前端展示参数占位（如 "<task>"）。
		if c.Input != nil && c.Input.Unstructured != nil {
			dto.InputHint = c.Input.Unstructured.Hint
		}
		out = append(out, dto)
	}
	return out
}

// flattenSelectOptions 展开 select 选项（分组结构拍平成平铺列表，供前端下拉）。
func flattenSelectOptions(opts acp.SessionConfigSelectOptions) []model.ConfigOptionValueDTO {
	var values []model.ConfigOptionValueDTO
	if opts.Ungrouped != nil {
		for _, v := range *opts.Ungrouped {
			values = append(values, configOptionValue(v))
		}
	}
	if opts.Grouped != nil {
		for _, g := range *opts.Grouped {
			for _, v := range g.Options {
				values = append(values, configOptionValue(v))
			}
		}
	}
	return values
}

func configOptionValue(v acp.SessionConfigSelectOption) model.ConfigOptionValueDTO {
	dto := model.ConfigOptionValueDTO{Value: string(v.Value), Name: v.Name}
	if v.Description != nil {
		dto.Description = *v.Description
	}
	return dto
}
