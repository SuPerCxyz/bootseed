// Package store 用嵌入式库 bbolt 持久化节点与部署历史(纯 Go,单文件,无 CGO).
package store

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/anomalyco/bootseed/server/internal/model"
)

var bucketNodes = []byte("nodes")

// Store 封装 bbolt 数据库.
type Store struct {
	db *bolt.DB
}

// Open 打开/创建数据库文件并初始化 bucket.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(bucketNodes)
		return e
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close 关闭数据库.
func (s *Store) Close() error { return s.db.Close() }

// get 读取节点(不存在返回 nil).
func (s *Store) get(tx *bolt.Tx, uuid string) (*model.Node, error) {
	b := tx.Bucket(bucketNodes).Get([]byte(uuid))
	if b == nil {
		return nil, nil
	}
	var n model.Node
	if err := json.Unmarshal(b, &n); err != nil {
		return nil, err
	}
	return &n, nil
}

func put(tx *bolt.Tx, n *model.Node) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return tx.Bucket(bucketNodes).Put([]byte(n.UUID), data)
}

func normalizeNode(in *model.Node) {
	in.Origin = strings.TrimSpace(in.Origin)
	if in.Origin != "bootseed-enter" {
		in.Hostname = ""
	}
}

// Register 注册/更新节点基本信息(开机上报).已存在则保留首次时间与部署历史.
// 节点重新注册意味着重启后再次进入 BootSeed 内存系统,上一次仍标为 "running"
// 的部署记录必然是硬中断/未上报,自动收敛为 "cancelled" 以让门户显示准确。
func (s *Store) Register(in model.Node, now time.Time) error {
	normalizeNode(&in)
	return s.db.Update(func(tx *bolt.Tx) error {
		cur, err := s.get(tx, in.UUID)
		if err != nil {
			return err
		}
		if cur == nil {
			in.FirstSeen = now
			in.LastSeen = now
			if in.Deploys == nil {
				in.Deploys = []model.Deploy{}
			}
			return put(tx, &in)
		}
		// 更新可变字段,保留历史
		cur.Hostname = in.Hostname
		cur.MAC, cur.IP = in.MAC, in.IP
		cur.Architecture, cur.BootMode = in.Architecture, in.BootMode
		cur.KernelVersion, cur.AlpineVersion = in.KernelVersion, in.AlpineVersion
		cur.AgentVersion = in.AgentVersion
		cur.AgentPort = in.AgentPort
		cur.AgentURL = in.AgentURL
		cur.Origin = in.Origin
		cur.NetworkMode = in.NetworkMode
		cur.NetworkStatus = in.NetworkStatus
		cur.ManagementIF = in.ManagementIF
		cur.Netmask = in.Netmask
		cur.Gateway = in.Gateway
		cur.DNS = in.DNS
		cur.LastError = in.LastError
		cur.LastSeen = now
		// 收敛卡在 running 的旧记录(硬中断导致 agent 没能上报 end)
		if len(cur.Deploys) > 0 {
			d := &cur.Deploys[len(cur.Deploys)-1]
			if d.Result == "running" {
				d.EndedAt = now
				d.Result = "cancelled"
				if d.Error == "" {
					d.Error = "节点重启/硬中断,状态自动收敛"
				}
			}
		}
		return put(tx, cur)
	})
}

// Delete 删除单个节点及其部署历史.
func (s *Store) Delete(uuid string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketNodes).Delete([]byte(uuid))
	})
}

// Heartbeat 刷新 last_seen(不存在则忽略).
func (s *Store) Heartbeat(uuid string, now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		n, err := s.get(tx, uuid)
		if err != nil || n == nil {
			return err
		}
		n.LastSeen = now
		return put(tx, n)
	})
}

// DeployStart 追加一条部署记录(result=running).
func (s *Store) DeployStart(uuid, imageID, target string, now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		n, err := s.get(tx, uuid)
		if err != nil || n == nil {
			return err
		}
		n.LastSeen = now
		n.Deploys = append(n.Deploys, model.Deploy{
			ImageID: imageID, TargetDisk: target, StartedAt: now, Result: "running",
		})
		return put(tx, n)
	})
}

// DeployEnd 更新最后一条部署记录的结果.
func (s *Store) DeployEnd(uuid, result string, bytes int64, errMsg string, now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		n, err := s.get(tx, uuid)
		if err != nil || n == nil {
			return err
		}
		n.LastSeen = now
		if len(n.Deploys) > 0 {
			d := &n.Deploys[len(n.Deploys)-1]
			d.EndedAt = now
			d.Result = result
			d.BytesWritten = bytes
			d.Error = errMsg
		}
		n.LastError = errMsg
		return put(tx, n)
	})
}

// Get 返回单个节点视图.
func (s *Store) Get(uuid string, now time.Time, onlineTimeout time.Duration) (*model.NodeView, error) {
	var out *model.NodeView
	err := s.db.View(func(tx *bolt.Tx) error {
		n, err := s.get(tx, uuid)
		if err != nil || n == nil {
			return err
		}
		view := makeNodeView(*n, now, onlineTimeout)
		out = &view
		return nil
	})
	return out, err
}

// List 返回全部节点视图,按 last_seen 倒序.
func (s *Store) List(now time.Time, onlineTimeout time.Duration) ([]model.NodeView, error) {
	var out []model.NodeView
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketNodes).ForEach(func(_, v []byte) error {
			var n model.Node
			if err := json.Unmarshal(v, &n); err != nil {
				return nil // 跳过损坏条目
			}
			out = append(out, makeNodeView(n, now, onlineTimeout))
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, err
}

func makeNodeView(n model.Node, now time.Time, onlineTimeout time.Duration) model.NodeView {
	status := "offline"
	if n.Online(now, onlineTimeout) {
		status = "online"
	}
	lifecycle := "offline"
	switch last := n.LastResult(); {
	case status == "online" && last == "running":
		lifecycle = "deploying"
	case status == "online":
		lifecycle = "bootseed_online"
	case last == "completed":
		lifecycle = "completed"
	case last == "failed":
		lifecycle = "failed"
	case last == "cancelled":
		lifecycle = "failed"
	}
	return model.NodeView{
		Node:         n,
		Status:       status,
		LastResultV:  n.LastResult(),
		DeployedEver: n.DeployedEver(),
		Lifecycle:    lifecycle,
	}
}
