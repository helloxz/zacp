package tty

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	maxInputFrameBytes = 1 << 20
	maxPTYCols         = 1000
	maxPTYRows         = 500
)

var (
	// ErrUnknownMessage 表示客户端发送了未支持的 Text 控制帧。
	ErrUnknownMessage = errors.New("unknown tty message type")
	// ErrInvalidResize 表示终端尺寸超出服务端安全范围。
	ErrInvalidResize = errors.New("invalid tty resize")
	// ErrInputTooLarge 表示单个 Binary 输入帧超过上限。
	ErrInputTooLarge = errors.New("tty input frame too large")
)

// ClientControlMessage 是客户端到服务端的 Text 控制帧。
type ClientControlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// ServerControlMessage 是服务端到客户端的 Text 控制帧。
type ServerControlMessage struct {
	Type       string `json:"type"`
	TerminalID string `json:"terminalId,omitempty"`
	Code       any    `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

func marshalServerMessage(message ServerControlMessage) ([]byte, error) {
	return json.Marshal(message)
}

func decodeClientControl(data []byte) (ClientControlMessage, error) {
	var message ClientControlMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return ClientControlMessage{}, fmt.Errorf("decode tty control message: %w", err)
	}
	if message.Type != "resize" {
		return ClientControlMessage{}, ErrUnknownMessage
	}
	if err := validateResize(message.Cols, message.Rows); err != nil {
		return ClientControlMessage{}, err
	}
	return message, nil
}

func validateResize(cols, rows int) error {
	if cols < 1 || cols > maxPTYCols || rows < 1 || rows > maxPTYRows {
		return fmt.Errorf("%w: cols=%d rows=%d", ErrInvalidResize, cols, rows)
	}
	return nil
}
