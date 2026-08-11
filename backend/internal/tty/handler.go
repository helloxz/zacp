package tty

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/helloxz/zacp/internal/auth"
	"github.com/helloxz/zacp/internal/service"
	wsbridge "github.com/helloxz/zacp/internal/ws"
)

const (
	writerTimeout = 10 * time.Second
	pingInterval  = 30 * time.Second
)

var errTTYClientClosed = errors.New("tty client closed connection")

// Handler 将一个浏览器 WebSocket 连接绑定到一个临时 PTY Session。
// TTY 使用独立 Handler，不进入 ACP Hub，避免聊天消息队列丢弃终端输出。
type Handler struct {
	manager *Manager
	service *service.TTYService
	log     *slog.Logger
	authSvc *auth.Service
}

// NewHandler 创建 TTY WebSocket Handler。
func NewHandler(manager *Manager, ttyService *service.TTYService, log *slog.Logger, authSvc *auth.Service) *Handler {
	return &Handler{
		manager: manager,
		service: ttyService,
		log:     log,
		authSvc: authSvc,
	}
}

// ServeHTTP 处理 GET /api/v1/tty/ws?workspaceId={id}。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := parseWorkspaceID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	proto, ok := wsbridge.AuthSubprotocol(r, h.authSvc)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	workspace, err := h.service.ResolveWorkspace(workspaceID)
	if err != nil {
		status := http.StatusInternalServerError
		message := "workspace unavailable"
		switch {
		case errors.Is(err, service.ErrTTYWorkspaceNotFound):
			status = http.StatusNotFound
			message = "workspace not found"
		case errors.Is(err, service.ErrTTYWorkspacePathInvalid):
			status = http.StatusConflict
			message = "workspace path is unavailable"
		}
		if h.log != nil {
			h.log.Warn("tty workspace unavailable", "workspaceID", workspaceID, "err", err)
		}
		http.Error(w, message, status)
		return
	}

	shellPath, shellArgs, err := h.service.Shell()
	if err != nil {
		http.Error(w, "terminal shell unavailable", http.StatusInternalServerError)
		return
	}

	session, err := h.manager.Create(context.Background(), workspace, shellPath, shellArgs)
	if err != nil {
		if errors.Is(err, ErrSessionLimit) {
			http.Error(w, "too many active terminals", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "failed to create terminal", http.StatusInternalServerError)
		return
	}

	options := &websocket.AcceptOptions{InsecureSkipVerify: true}
	if proto != "" {
		options.Subprotocols = []string{proto}
	}
	conn, err := websocket.Accept(w, r, options)
	if err != nil {
		_ = session.Close("websocket accept failed")
		return
	}

	if err := session.start(); err != nil {
		_ = writeControl(conn, ServerControlMessage{
			Type:    "error",
			Code:    "pty_start_failed",
			Message: "failed to start terminal",
		})
		_ = conn.Close(websocket.StatusInternalError, "pty start failed")
		return
	}

	go h.run(conn, session)
}

func parseWorkspaceID(r *http.Request) (uint, error) {
	raw := r.URL.Query().Get("workspaceId")
	if raw == "" {
		return 0, errors.New("workspaceId is required")
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return 0, errors.New("workspaceId must be a positive integer")
	}
	return uint(id), nil
}

func (h *Handler) run(conn *websocket.Conn, session *Session) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := newOutboundQueue()
	// ready 先入队，再启动 reader，保证 shell 首帧不会排在 ready 前面。
	ready, err := marshalServerMessage(ServerControlMessage{
		Type:       "ready",
		TerminalID: session.ID(),
	})
	if err != nil {
		_ = session.Close("marshal ready failed")
		_ = conn.Close(websocket.StatusInternalError, "marshal ready failed")
		return
	}
	if err := queue.push(ctx, websocket.MessageText, ready); err != nil {
		_ = session.Close("queue ready failed")
		_ = conn.Close(websocket.StatusInternalError, "queue ready failed")
		return
	}

	loopErrors := make(chan error, 4)
	writerDone := make(chan struct{})
	go h.writeLoop(ctx, conn, queue, loopErrors, writerDone)
	go h.outputLoop(ctx, session, queue, loopErrors)
	go h.inputLoop(ctx, conn, session, loopErrors)
	go h.pingLoop(ctx, conn, loopErrors)

	select {
	case <-session.Done():
		if session.State() == SessionExited {
			code := session.ExitCode()
			exit, marshalErr := marshalServerMessage(ServerControlMessage{Type: "exit", Code: code})
			if marshalErr == nil {
				writeCtx, writeCancel := context.WithTimeout(context.Background(), writerTimeout)
				_ = queue.push(writeCtx, websocket.MessageText, exit)
				writeCancel()
			}
		}
	case err := <-loopErrors:
		if err != nil && h.log != nil {
			h.log.Debug("tty loop stopped", "sessionID", session.ID(), "err", err)
		}
		if err != nil && !errors.Is(err, errTTYClientClosed) {
			if data, marshalErr := marshalServerMessage(ServerControlMessage{
				Type:    "error",
				Code:    ttyErrorCode(err),
				Message: ttyErrorMessage(err),
			}); marshalErr == nil {
				writeCtx, writeCancel := context.WithTimeout(context.Background(), writerTimeout)
				_ = queue.push(writeCtx, websocket.MessageText, data)
				writeCancel()
			}
		}
		_ = session.Close("websocket loop stopped")
	}

	queue.close()
	select {
	case <-writerDone:
	case <-time.After(writerTimeout):
	}
	_ = conn.Close(websocket.StatusNormalClosure, "terminal closed")
}

func (h *Handler) writeLoop(ctx context.Context, conn *websocket.Conn, queue *outboundQueue, loopErrors chan<- error, done chan<- struct{}) {
	defer close(done)

	for {
		message, ok, err := queue.pop(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				sendLoopError(loopErrors, err)
			}
			return
		}
		if !ok {
			return
		}
		writeCtx, cancel := context.WithTimeout(ctx, writerTimeout)
		err = conn.Write(writeCtx, message.messageType, message.data)
		cancel()
		if err != nil {
			sendLoopError(loopErrors, err)
			return
		}
	}
}

func (h *Handler) pingLoop(ctx context.Context, conn *websocket.Conn, loopErrors chan<- error) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeCtx, cancel := context.WithTimeout(ctx, writerTimeout)
			err := conn.Ping(writeCtx)
			cancel()
			if err != nil {
				sendLoopError(loopErrors, err)
				return
			}
		}
	}
}

func (h *Handler) outputLoop(ctx context.Context, session *Session, queue *outboundQueue, loopErrors chan<- error) {
	buf := make([]byte, 32*1024)
	for {
		n, err := session.Read(buf)
		if n > 0 {
			if pushErr := queue.push(ctx, websocket.MessageBinary, buf[:n]); pushErr != nil {
				if !errors.Is(pushErr, context.Canceled) && !errors.Is(pushErr, errOutputQueueClosed) {
					sendLoopError(loopErrors, pushErr)
				}
				return
			}
		}
		if err == nil {
			continue
		}

		state := session.State()
		if state == SessionExited {
			session.finalizeExit()
			return
		}
		if state == SessionClosing || state == SessionClosed || state == SessionError {
			return
		}
		sendLoopError(loopErrors, err)
		return
	}
}

func (h *Handler) inputLoop(ctx context.Context, conn *websocket.Conn, session *Session, loopErrors chan<- error) {
	conn.SetReadLimit(maxInputFrameBytes)
	for {
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
				sendLoopError(loopErrors, errTTYClientClosed)
			} else {
				sendLoopError(loopErrors, err)
			}
			return
		}
		switch messageType {
		case websocket.MessageBinary:
			if len(data) > maxInputFrameBytes {
				sendLoopError(loopErrors, ErrInputTooLarge)
				return
			}
			if _, err := session.WriteInput(data); err != nil {
				sendLoopError(loopErrors, err)
				return
			}
		case websocket.MessageText:
			control, err := decodeClientControl(data)
			if err != nil {
				sendLoopError(loopErrors, err)
				return
			}
			if err := session.Resize(control.Cols, control.Rows); err != nil {
				sendLoopError(loopErrors, err)
				return
			}
		default:
			sendLoopError(loopErrors, ErrUnknownMessage)
			return
		}
	}
}

func sendLoopError(ch chan<- error, err error) {
	select {
	case ch <- err:
	default:
	}
}

func writeControl(conn *websocket.Conn, message ServerControlMessage) error {
	data, err := marshalServerMessage(message)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), writerTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, data)
}

func ttyErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrUnknownMessage):
		return "unknown_message"
	case errors.Is(err, ErrInvalidResize):
		return "invalid_resize"
	case errors.Is(err, ErrInputTooLarge):
		return "input_too_large"
	default:
		return "tty_error"
	}
}

func ttyErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrUnknownMessage):
		return "unsupported terminal message"
	case errors.Is(err, ErrInvalidResize):
		return "invalid terminal size"
	case errors.Is(err, ErrInputTooLarge):
		return "terminal input is too large"
	default:
		return "terminal connection failed"
	}
}
