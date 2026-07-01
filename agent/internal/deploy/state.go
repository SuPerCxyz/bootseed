// Package deploy 实现 BootSeed 节点部署的状态机与全局互斥.
//
// 同一节点只允许一个部署任务在跑.任何并发请求都返回 ErrBusy.
package deploy

import (
	"context"
	"errors"
	"sync"
	"time"
)

// State 是部署状态.
type State string

const (
	StateIdle        State = "idle"
	StateValidating  State = "validating"
	StatePreparing   State = "preparing"
	StateDownloading State = "downloading"
	StateWriting     State = "writing"
	StateSyncing     State = "syncing"
	StateVerifying   State = "verifying"
	StateCompleted   State = "completed"
	StateFailed      State = "failed"
	StateCancelled   State = "cancelled"
)

// IsTerminal 报告是否是终止态.
func (s State) IsTerminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	}
	return false
}

// IsRunning 报告部署是否处于不可被打断 (reboot/poweroff) 的状态.
func (s State) IsRunning() bool {
	switch s {
	case StateValidating, StatePreparing, StateDownloading,
		StateWriting, StateSyncing, StateVerifying:
		return true
	}
	return false
}

// ErrBusy 表示已有任务运行.
var ErrBusy = errors.New("已有部署任务正在运行")

// Task 是一次部署任务的运行时元信息.
type Task struct {
	ID        string    `json:"id"`
	ImageID   string    `json:"image_id"`
	Target    string    `json:"target"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	State     State     `json:"state"`
	Error     string    `json:"error,omitempty"`
}

// Manager 管理唯一部署任务及其取消上下文.
type Manager struct {
	mu      sync.Mutex
	current *Task
	cancel  context.CancelFunc
}

// NewManager 构造空 Manager.
func NewManager() *Manager { return &Manager{} }

// Acquire 尝试占用部署资源.如果已有任务运行则返回 ErrBusy.
//
// 返回的 ctx 在调用 Cancel/Finish 时被取消,pipeline 必须监听它.
func (m *Manager) Acquire(parent context.Context, t *Task) (context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && !m.current.State.IsTerminal() {
		return nil, ErrBusy
	}
	ctx, cancel := context.WithCancel(parent)
	t.StartedAt = time.Now()
	t.UpdatedAt = t.StartedAt
	t.State = StateValidating
	m.current = t
	m.cancel = cancel
	return ctx, nil
}

// SetState 修改当前任务状态.
func (m *Manager) SetState(s State, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return
	}
	m.current.State = s
	m.current.Error = errMsg
	m.current.UpdatedAt = time.Now()
}

// Snapshot 返回当前任务副本.
func (m *Manager) Snapshot() (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return Task{State: StateIdle}, false
	}
	return *m.current, true
}

// Cancel 通知 pipeline 退出.
func (m *Manager) Cancel() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.current.State.IsTerminal() {
		return false
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.current.State = StateCancelled
	m.current.UpdatedAt = time.Now()
	return true
}

// Finish 标记完成并释放 cancel.
func (m *Manager) Finish(state State, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return
	}
	m.current.State = state
	m.current.Error = errMsg
	m.current.UpdatedAt = time.Now()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// IsRunning 报告当前是否处于不可重启的运行态.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current != nil && m.current.State.IsRunning()
}
