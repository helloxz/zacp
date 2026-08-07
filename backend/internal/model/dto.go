package model

// ConfigOptionValueDTO 会话配置选项的可选值（前端下拉项）。
type ConfigOptionValueDTO struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ConfigOptionDTO 会话配置项（model / mode / thought_level 等），与 ACP configOptions 对齐。
type ConfigOptionDTO struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	Category     string                 `json:"category,omitempty"` // model | mode | thought_level | ...
	Type         string                 `json:"type"`               // select | boolean
	CurrentValue any                    `json:"currentValue"`
	Options      []ConfigOptionValueDTO `json:"options,omitempty"`
}

// AvailableCommandDTO 会话可用 / 命令（agent 经 ACP available_commands_update 通告）。
type AvailableCommandDTO struct {
	Name        string `json:"name"`                  // 命令名（不含斜杠，如 "init"）
	Description string `json:"description,omitempty"` // 命令说明
	InputHint   string `json:"inputHint,omitempty"`   // 参数提示（如 "<task>"），选中后展示
}

// SessionModeDTO 兼容旧版 session modes。
type SessionModeDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SessionModesDTO 旧版 modes 状态。
type SessionModesDTO struct {
	CurrentModeID  string           `json:"currentModeId"`
	AvailableModes []SessionModeDTO `json:"availableModes"`
}

// CreateSessionResult 创建会话业务结果（DB 会话 + ACP 配置）。
type CreateSessionResult struct {
	Session       *Session          `json:"session"`
	ConfigOptions []ConfigOptionDTO `json:"configOptions,omitempty"`
	Modes         *SessionModesDTO  `json:"modes,omitempty"`
}

// PermissionOptionDTO 权限选项（推给前端卡片按钮）。
type PermissionOptionDTO struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
}

// FileEntryDTO 文件树条目（目录或文件），Path 为相对工作区根的路径（统一 `/` 分隔）。
type FileEntryDTO struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size,omitempty"`     // 仅文件
	MimeType string `json:"mimeType,omitempty"` // 仅文件；按扩展名推断，可能为空
}

// FileListDTO 目录列表结果。
type FileListDTO struct {
	Path    string         `json:"path"` // 当前目录的相对路径（空 = 工作区根）
	Entries []FileEntryDTO `json:"entries"`
}

// GitStatusDTO 工作区 Git 状态摘要与变更文件列表。
// GitInstalled=false 或 IsRepository=false 时，Files 保持为空切片，供前端展示对应空态。
type GitStatusDTO struct {
	GitInstalled bool           `json:"gitInstalled"`
	IsRepository bool           `json:"isRepository"`
	Summary      GitSummaryDTO  `json:"summary"`
	Files        []GitChangeDTO `json:"files"`
	Truncated    bool           `json:"truncated"`
	HiddenCount  int            `json:"hiddenCount"`
}

// GitSummaryDTO Git 状态汇总；计数包含被 UI 隐藏的路径，HiddenCount 用于解释差异。
type GitSummaryDTO struct {
	Changed    int `json:"changed"`
	Staged     int `json:"staged"`
	Unstaged   int `json:"unstaged"`
	Untracked  int `json:"untracked"`
	Conflicted int `json:"conflicted"`
}

// GitChangeDTO 单个 Git 变更条目，Path 始终相对于当前 workspace。
type GitChangeDTO struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"originalPath,omitempty"`
	Status         string `json:"status"`
	IndexStatus    string `json:"indexStatus"`
	WorktreeStatus string `json:"worktreeStatus"`
}

// DirectoryEntryDTO 目录浏览条目（新建项目弹窗用，仅文件夹）。
// Path 为子文件夹的绝对路径，前端可直接作为下一步浏览 / 创建项目路径。
type DirectoryEntryDTO struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DirectoryListDTO 目录浏览结果（GET /api/v1/fs/directories）。
type DirectoryListDTO struct {
	// Path 当前目录的绝对路径（请求 path 为空时 = session.default_cwd 解析结果）。
	Path string `json:"path"`
	// Parent 上级目录绝对路径；已在根目录时为 ""（前端据此禁用「返回上级」）。
	Parent string `json:"parent"`
	// Entries 仅子文件夹（隐藏目录与 ignoredDirNames 大目录由后端过滤）。
	Entries []DirectoryEntryDTO `json:"entries"`
}
