package tty

import (
	"context"
	"errors"
	"testing"

	"github.com/helloxz/zacp/internal/model"
)

func TestManagerEnforcesSessionLimit(t *testing.T) {
	manager := NewManager(nil, 1)
	workspace := &model.Workspace{ID: 1, Path: t.TempDir()}

	first, err := manager.Create(context.Background(), workspace, "shell", nil)
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	defer first.Close("test cleanup")

	if _, err := manager.Create(context.Background(), workspace, "shell", nil); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("second create error = %v, want ErrSessionLimit", err)
	}
	if got := manager.Count(); got != 1 {
		t.Fatalf("manager count = %d, want 1", got)
	}
}

func TestManagerCloseAllRemovesSessions(t *testing.T) {
	manager := NewManager(nil, 2)
	workspace := &model.Workspace{ID: 1, Path: t.TempDir()}
	first, err := manager.Create(context.Background(), workspace, "shell", nil)
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	second, err := manager.Create(context.Background(), workspace, "shell", nil)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	manager.CloseAll()
	<-first.Done()
	<-second.Done()
	if got := manager.Count(); got != 0 {
		t.Fatalf("manager count after CloseAll = %d, want 0", got)
	}
	if first.State() != SessionClosed || second.State() != SessionClosed {
		t.Fatalf("states after CloseAll = %s, %s; want closed, closed", first.State(), second.State())
	}
}
