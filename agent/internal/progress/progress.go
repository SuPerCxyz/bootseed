// Package progress 跟踪部署任务的进度并对外提供瞬时 / 平均速度。
package progress

import (
	"sync"
	"time"
)

// Snapshot 是部署任务进度的快照，可被 JSON 序列化给前端。
type Snapshot struct {
	Stage           string  `json:"stage"`
	Message         string  `json:"message"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	WrittenBytes    int64   `json:"written_bytes"`
	TotalBytes      int64   `json:"total_bytes"`
	Percent         float64 `json:"percent"`
	SpeedBps        float64 `json:"speed_bps"`
	AverageBps      float64 `json:"average_bps"`
	ElapsedSeconds  float64 `json:"elapsed_seconds"`
	ETASeconds      float64 `json:"eta_seconds"`
	Error           string  `json:"error,omitempty"`
}

// Tracker 维护一个部署任务的实时进度。
type Tracker struct {
	mu         sync.Mutex
	stage      string
	message    string
	downloaded int64
	written    int64
	total      int64
	startedAt  time.Time
	lastSample time.Time
	lastBytes  int64
	speed      float64
	errMessage string
	listeners  []chan Snapshot
}

// NewTracker 构造空 Tracker。
func NewTracker() *Tracker { return &Tracker{} }

// Start 重置 Tracker 状态。
func (t *Tracker) Start(total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stage = "preparing"
	t.message = ""
	t.downloaded = 0
	t.written = 0
	t.total = total
	t.startedAt = time.Now()
	t.lastSample = t.startedAt
	t.lastBytes = 0
	t.speed = 0
	t.errMessage = ""
}

// SetStage 修改阶段名。
func (t *Tracker) SetStage(stage, message string) {
	t.mu.Lock()
	t.stage = stage
	t.message = message
	t.mu.Unlock()
	t.broadcastLocked()
}

// SetTotal 在已知镜像 raw_size 后更新总字节数。
func (t *Tracker) SetTotal(total int64) {
	t.mu.Lock()
	t.total = total
	t.mu.Unlock()
}

// AddDownloaded 累加压缩流读取字节。
func (t *Tracker) AddDownloaded(n int64) {
	t.mu.Lock()
	t.downloaded += n
	t.mu.Unlock()
}

// AddWritten 累加已写盘字节（用于计算进度）。
func (t *Tracker) AddWritten(n int64) {
	t.mu.Lock()
	t.written += n
	now := time.Now()
	if !t.lastSample.IsZero() {
		dt := now.Sub(t.lastSample).Seconds()
		if dt >= 0.5 {
			t.speed = float64(t.written-t.lastBytes) / dt
			t.lastBytes = t.written
			t.lastSample = now
		}
	} else {
		t.lastSample = now
		t.lastBytes = t.written
	}
	t.mu.Unlock()
}

// Fail 记录错误。
func (t *Tracker) Fail(err error) {
	t.mu.Lock()
	t.errMessage = err.Error()
	t.stage = "failed"
	t.mu.Unlock()
	t.broadcastLocked()
}

// Snapshot 返回当前状态。
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

func (t *Tracker) snapshotLocked() Snapshot {
	elapsed := 0.0
	if !t.startedAt.IsZero() {
		elapsed = time.Since(t.startedAt).Seconds()
	}
	var pct, eta, avg float64
	if t.total > 0 {
		pct = float64(t.written) / float64(t.total) * 100
		if pct > 100 {
			pct = 100
		}
	}
	if elapsed > 0 {
		avg = float64(t.written) / elapsed
	}
	if t.speed > 0 && t.total > t.written {
		eta = float64(t.total-t.written) / t.speed
	}
	return Snapshot{
		Stage:           t.stage,
		Message:         t.message,
		DownloadedBytes: t.downloaded,
		WrittenBytes:    t.written,
		TotalBytes:      t.total,
		Percent:         pct,
		SpeedBps:        t.speed,
		AverageBps:      avg,
		ElapsedSeconds:  elapsed,
		ETASeconds:      eta,
		Error:           t.errMessage,
	}
}

// Subscribe 创建 SSE 用监听 channel。
func (t *Tracker) Subscribe() <-chan Snapshot {
	ch := make(chan Snapshot, 16)
	t.mu.Lock()
	t.listeners = append(t.listeners, ch)
	t.mu.Unlock()
	return ch
}

// Unsubscribe 关闭并移除监听。
func (t *Tracker) Unsubscribe(ch <-chan Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, c := range t.listeners {
		if c == ch {
			close(c)
			t.listeners = append(t.listeners[:i], t.listeners[i+1:]...)
			return
		}
	}
}

func (t *Tracker) broadcastLocked() {
	t.mu.Lock()
	snap := t.snapshotLocked()
	listeners := append([]chan Snapshot(nil), t.listeners...)
	t.mu.Unlock()
	for _, c := range listeners {
		select {
		case c <- snap:
		default:
		}
	}
}

// Broadcast 让外部周期性触发广播。
func (t *Tracker) Broadcast() { t.broadcastLocked() }
