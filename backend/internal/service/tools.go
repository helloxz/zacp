package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/helloxz/zacp/internal/model"
	"github.com/helloxz/zacp/internal/store"
)

const (
	toolFileManager = "file-manager"
	toolZed         = "zed"
	toolSublime     = "sublime"
	toolTerminal    = "terminal"
	toolGhostty     = "ghostty"
)

var (
	// ErrInvalidTool 表示请求的工具不在当前平台白名单中。
	ErrInvalidTool = errors.New("invalid external tool")
	// ErrToolUnavailable 表示工具属于当前平台白名单，但本机未检测到。
	ErrToolUnavailable = errors.New("external tool unavailable")
	// ErrToolWorkspace 表示会话绑定的工作区不存在或不是目录。
	ErrToolWorkspace = errors.New("session workspace unavailable")
	// ErrToolLaunch 表示系统拒绝启动外部工具。
	ErrToolLaunch = errors.New("failed to launch external tool")
)

type toolCommand struct {
	program string
	args    []string
}

type toolRuntime struct {
	platform     string
	lookup       func(string) (string, error)
	appAvailable func(string) bool
}

type toolDefinition struct {
	id      string
	label   string
	resolve func(toolRuntime, string) (toolCommand, bool)
}

// ToolService 负责按当前平台枚举并启动白名单中的本地工具。
//
// 命令和工作目录均由服务端固定解析：前端只能提交工具 ID，不能提交命令或路径，
// 避免把「打开项目」接口变成任意命令执行入口。GUI 工具启动后由独立进程负责，
// HTTP 请求不等待工具退出。
type ToolService struct {
	sessionRepo *store.SessionRepository
	runtime     toolRuntime
	start       func(*exec.Cmd) error
}

// NewToolService 创建本地工具服务。
func NewToolService(sessionRepo *store.SessionRepository) *ToolService {
	return &ToolService{
		sessionRepo: sessionRepo,
		runtime:     defaultToolRuntime(),
		start: func(cmd *exec.Cmd) error {
			return cmd.Start()
		},
	}
}

// ListAvailable 返回当前平台且已安装的工具。
func (s *ToolService) ListAvailable() []model.ExternalToolDTO {
	if s == nil {
		return []model.ExternalToolDTO{}
	}

	defs := toolDefinitions(s.runtime)
	tools := make([]model.ExternalToolDTO, 0, len(defs))
	for _, def := range defs {
		if _, ok := def.resolve(s.runtime, ""); !ok {
			continue
		}
		tools = append(tools, model.ExternalToolDTO{ID: def.id, Label: def.label})
	}
	return tools
}

// OpenSessionTool 在当前 Session 的工作区启动一个白名单工具。
func (s *ToolService) OpenSessionTool(sessionID uint, toolID string) error {
	if s == nil || s.sessionRepo == nil {
		return fmt.Errorf("%w: session service is not configured", ErrToolLaunch)
	}

	toolID = strings.TrimSpace(toolID)
	def, ok := findToolDefinition(toolDefinitions(s.runtime), toolID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrInvalidTool, toolID)
	}

	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}

	workspacePath := session.Workspace.Path
	if workspacePath == "" {
		return fmt.Errorf("%w: path is empty", ErrToolWorkspace)
	}
	workspacePath, err = filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("%w: resolve path: %v", ErrToolWorkspace, err)
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrToolWorkspace, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: path is not a directory", ErrToolWorkspace)
	}

	command, ok := def.resolve(s.runtime, workspacePath)
	if !ok {
		return fmt.Errorf("%w: %s", ErrToolUnavailable, toolID)
	}

	cmd := exec.Command(command.program, command.args...)
	// 统一设置 cwd：终端类程序通常直接继承 cwd，编辑器类程序同时收到显式路径参数。
	cmd.Dir = workspacePath
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := s.start(cmd); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrToolLaunch, toolID, err)
	}

	// Start 成功后必须 Wait，避免短命 GUI 启动器在 Unix 上留下 zombie 进程。
	// 测试替身可能不会设置 Process，因此仅真实进程启动后等待。
	if cmd.Process != nil {
		go func() { _ = cmd.Wait() }()
	}
	return nil
}

func defaultToolRuntime() toolRuntime {
	return toolRuntime{
		platform:     runtime.GOOS,
		lookup:       exec.LookPath,
		appAvailable: macApplicationAvailable,
	}
}

func macApplicationAvailable(name string) bool {
	return exec.Command("open", "-Ra", name).Run() == nil
}

func (r toolRuntime) hasCommand(name string) bool {
	if r.lookup == nil || name == "" {
		return false
	}
	_, err := r.lookup(name)
	return err == nil
}

func (r toolRuntime) hasApp(name string) bool {
	return r.appAvailable != nil && r.appAvailable(name)
}

func toolDefinitions(r toolRuntime) []toolDefinition {
	switch r.platform {
	case "darwin":
		return darwinToolDefinitions()
	case "linux":
		return linuxToolDefinitions()
	case "windows":
		return windowsToolDefinitions()
	default:
		return nil
	}
}

func darwinToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			id:    toolFileManager,
			label: "Finder",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				return commandIfAvailable(r, "open", cwd), r.hasCommand("open")
			},
		},
		{
			id:    toolZed,
			label: "Zed",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				if r.hasCommand("zed") {
					return toolCommand{program: "zed", args: []string{cwd}}, true
				}
				if r.hasCommand("open") && r.hasApp("Zed") {
					return toolCommand{program: "open", args: []string{"-a", "Zed", cwd}}, true
				}
				return toolCommand{}, false
			},
		},
		{
			id:    toolSublime,
			label: "Sublime Text",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				if r.hasCommand("subl") {
					return toolCommand{program: "subl", args: []string{cwd}}, true
				}
				if r.hasCommand("open") && r.hasApp("Sublime Text") {
					return toolCommand{program: "open", args: []string{"-a", "Sublime Text", cwd}}, true
				}
				return toolCommand{}, false
			},
		},
		{
			id:    toolTerminal,
			label: "Terminal",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				if !r.hasCommand("open") {
					return toolCommand{}, false
				}
				return toolCommand{program: "open", args: []string{"-a", "Terminal", cwd}}, true
			},
		},
		{
			id:    toolGhostty,
			label: "Ghostty",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				if r.hasCommand("ghostty") {
					return toolCommand{program: "ghostty", args: []string{"--working-directory=" + cwd}}, true
				}
				if r.hasCommand("open") && r.hasApp("Ghostty") {
					return toolCommand{
						program: "open",
						args:    []string{"-a", "Ghostty", "--args", "--working-directory=" + cwd},
					}, true
				}
				return toolCommand{}, false
			},
		},
	}
}

func linuxToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			id:    toolFileManager,
			label: "File Manager",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				return commandIfAvailable(r, "xdg-open", cwd), r.hasCommand("xdg-open")
			},
		},
		{
			id:    toolZed,
			label: "Zed",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				return commandIfAvailable(r, "zed", cwd), r.hasCommand("zed")
			},
		},
		{
			id:    toolSublime,
			label: "Sublime Text",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				return commandIfAvailable(r, "subl", cwd), r.hasCommand("subl")
			},
		},
		{
			id:    toolTerminal,
			label: "Terminal",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				for _, candidate := range []string{
					"x-terminal-emulator",
					"gnome-terminal",
					"konsole",
					"xfce4-terminal",
					"kitty",
				} {
					if r.hasCommand(candidate) {
						return toolCommand{program: candidate}, true
					}
				}
				return toolCommand{}, false
			},
		},
		{
			id:    toolGhostty,
			label: "Ghostty",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				if !r.hasCommand("ghostty") {
					return toolCommand{}, false
				}
				return toolCommand{program: "ghostty", args: []string{"--working-directory=" + cwd}}, true
			},
		},
	}
}

func windowsToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			id:    toolFileManager,
			label: "Explorer",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				return commandIfAvailable(r, "explorer.exe", cwd), r.hasCommand("explorer.exe")
			},
		},
		{
			id:    toolZed,
			label: "Zed",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				for _, candidate := range []string{"zed", "zed.exe"} {
					if r.hasCommand(candidate) {
						return commandIfAvailable(r, candidate, cwd), true
					}
				}
				return toolCommand{}, false
			},
		},
		{
			id:    toolSublime,
			label: "Sublime Text",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				for _, candidate := range []string{"subl", "subl.exe"} {
					if r.hasCommand(candidate) {
						return commandIfAvailable(r, candidate, cwd), true
					}
				}
				return toolCommand{}, false
			},
		},
		{
			id:    toolTerminal,
			label: "Windows Terminal",
			resolve: func(r toolRuntime, cwd string) (toolCommand, bool) {
				if !r.hasCommand("wt.exe") {
					return toolCommand{}, false
				}
				return toolCommand{program: "wt.exe", args: []string{"-d", cwd}}, true
			},
		},
	}
}

func commandIfAvailable(r toolRuntime, program, cwd string) toolCommand {
	return toolCommand{program: program, args: []string{cwd}}
}

func findToolDefinition(defs []toolDefinition, id string) (toolDefinition, bool) {
	for _, def := range defs {
		if def.id == id {
			return def, true
		}
	}
	return toolDefinition{}, false
}
