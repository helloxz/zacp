package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/helloxz/zacp/internal/model"
	"github.com/helloxz/zacp/internal/store"
	"gorm.io/gorm"
)

var (
	// ErrTTYWorkspaceNotFound 表示 TTY 请求的工作区不存在或已被删除。
	ErrTTYWorkspaceNotFound = errors.New("tty workspace not found")
	// ErrTTYWorkspacePathInvalid 表示工作区记录存在，但路径已失效或不再是目录。
	ErrTTYWorkspacePathInvalid = errors.New("tty workspace path invalid")
	// ErrTTYShellUnavailable 表示当前平台没有可启动的默认 shell。
	ErrTTYShellUnavailable = errors.New("tty shell unavailable")
)

// TTYService 为临时终端解析工作区和默认 shell。
// 终端只能使用已登记 Workspace.Path 作为 cwd，不能接受前端传入任意路径或命令。
type TTYService struct {
	workspaceRepo *store.WorkspaceRepository
}

// NewTTYService 创建 TTY 服务。
func NewTTYService(workspaceRepo *store.WorkspaceRepository) *TTYService {
	return &TTYService{workspaceRepo: workspaceRepo}
}

// ResolveWorkspace 查询并重新校验 TTY 使用的工作区路径。
// 工作区可能在登记后被删除或变成文件，因此不能只依赖数据库记录创建时的校验。
func (s *TTYService) ResolveWorkspace(workspaceID uint) (*model.Workspace, error) {
	if workspaceID == 0 {
		return nil, fmt.Errorf("%w: id must be positive", ErrTTYWorkspaceNotFound)
	}

	workspace, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: id=%d", ErrTTYWorkspaceNotFound, workspaceID)
		}
		return nil, fmt.Errorf("get tty workspace id=%d: %w", workspaceID, err)
	}

	info, err := os.Stat(workspace.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: stat workspace directory: %v", ErrTTYWorkspacePathInvalid, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: workspace path is not a directory", ErrTTYWorkspacePathInvalid)
	}
	return workspace, nil
}

// Shell 返回当前平台的默认 shell 路径和参数。
// 启动失败后的固定 fallback 由 TTY Session 负责，避免这里持有进程生命周期。
func (s *TTYService) Shell() (path string, args []string, err error) {
	var candidate string
	fallback := "/bin/sh"
	if runtime.GOOS == "windows" {
		candidate = strings.TrimSpace(os.Getenv("COMSPEC"))
		fallback = "cmd.exe"
	} else {
		candidate = strings.TrimSpace(os.Getenv("SHELL"))
	}

	if resolved, ok := lookExecutable(candidate); ok {
		return resolved, nil, nil
	}
	if resolved, ok := lookExecutable(fallback); ok {
		return resolved, nil, nil
	}
	return "", nil, fmt.Errorf("%w: default=%q fallback=%q", ErrTTYShellUnavailable, candidate, fallback)
}

func lookExecutable(candidate string) (string, bool) {
	if candidate == "" {
		return "", false
	}
	resolved, err := exec.LookPath(candidate)
	if err != nil {
		return "", false
	}
	return resolved, true
}
