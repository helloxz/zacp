package tty

import (
	"context"
	"errors"
	"sync"

	"github.com/coder/websocket"
)

const maxOutputQueueBytes = 4 << 20

var errOutputQueueClosed = errors.New("tty output queue closed")

// outboundMessage 是一个有序的 WebSocket 出站帧。
type outboundMessage struct {
	messageType websocket.MessageType
	data        []byte
}

// outboundQueue 按字节数而不是 item 数量限制缓冲，避免高速 PTY 输出无界增长。
type outboundQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []outboundMessage
	bytes  int
	closed bool
}

func newOutboundQueue() *outboundQueue {
	queue := &outboundQueue{}
	queue.cond = sync.NewCond(&queue.mu)
	return queue
}

func (q *outboundQueue) push(ctx context.Context, messageType websocket.MessageType, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(data) > maxOutputQueueBytes {
		return errors.New("tty output message exceeds queue capacity")
	}

	wake := context.AfterFunc(ctx, func() {
		q.mu.Lock()
		q.cond.Broadcast()
		q.mu.Unlock()
	})
	defer wake()

	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.closed && q.bytes+len(data) > maxOutputQueueBytes {
		if err := ctx.Err(); err != nil {
			return err
		}
		q.cond.Wait()
	}
	if q.closed {
		return errOutputQueueClosed
	}

	copyData := append([]byte(nil), data...)
	q.items = append(q.items, outboundMessage{messageType: messageType, data: copyData})
	q.bytes += len(copyData)
	q.cond.Broadcast()
	return nil
}

func (q *outboundQueue) pop(ctx context.Context) (outboundMessage, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	wake := context.AfterFunc(ctx, func() {
		q.mu.Lock()
		q.cond.Broadcast()
		q.mu.Unlock()
	})
	defer wake()

	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		if err := ctx.Err(); err != nil {
			return outboundMessage{}, false, err
		}
		q.cond.Wait()
	}
	if len(q.items) == 0 && q.closed {
		return outboundMessage{}, false, nil
	}

	message := q.items[0]
	q.items[0] = outboundMessage{}
	q.items = q.items[1:]
	q.bytes -= len(message.data)
	q.cond.Broadcast()
	return message, true, nil
}

func (q *outboundQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}
