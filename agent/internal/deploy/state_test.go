package deploy

import (
	"context"
	"testing"
)

func TestManager_AcquireBusy(t *testing.T) {
	m := NewManager()
	ctx, err := m.Acquire(context.Background(), &Task{ID: "t1", ImageID: "img1", Target: "/dev/sda"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire(context.Background(), &Task{ID: "t2"}); err != ErrBusy {
		t.Fatalf("第二次 Acquire 应该返回 ErrBusy: %v", err)
	}
	_ = ctx
	m.Finish(StateCompleted, "")
	if _, err := m.Acquire(context.Background(), &Task{ID: "t3"}); err != nil {
		t.Fatalf("完成后应可以 Acquire: %v", err)
	}
}

func TestManager_Cancel(t *testing.T) {
	m := NewManager()
	ctx, _ := m.Acquire(context.Background(), &Task{ID: "t1"})
	if !m.Cancel() {
		t.Fatal("Cancel 应该成功")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("ctx 应该被取消")
	}
	snap, _ := m.Snapshot()
	if snap.State != StateCancelled {
		t.Errorf("state = %s, want cancelled", snap.State)
	}
}

func TestState_Helpers(t *testing.T) {
	if !StateCompleted.IsTerminal() || !StateFailed.IsTerminal() || !StateCancelled.IsTerminal() {
		t.Error("terminal states 判断失败")
	}
	if StateDownloading.IsTerminal() {
		t.Error("downloading 不应为 terminal")
	}
	if !StateDownloading.IsRunning() || !StateWriting.IsRunning() {
		t.Error("running 判断失败")
	}
	if StateIdle.IsRunning() {
		t.Error("idle 不应 running")
	}
}

func TestManager_IsRunning(t *testing.T) {
	m := NewManager()
	if m.IsRunning() {
		t.Fatal("空 Manager 不应 running")
	}
	_, _ = m.Acquire(context.Background(), &Task{ID: "x"})
	m.SetState(StateWriting, "")
	if !m.IsRunning() {
		t.Fatal("Writing 应 running")
	}
	m.Finish(StateCompleted, "")
	if m.IsRunning() {
		t.Fatal("完成后不应 running")
	}
}
