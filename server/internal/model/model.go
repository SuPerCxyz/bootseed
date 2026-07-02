// Package model 定义服务端门户与节点登记共享的数据结构.
package model

import "time"

// Image 与 agent 端镜像清单字段保持一致(index.json 的条目).
type Image struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	OS               string   `json:"os"`
	Version          string   `json:"version"`
	Architecture     string   `json:"architecture"`
	Firmware         []string `json:"firmware"`
	Path             string   `json:"path"`
	Format           string   `json:"format"`
	CompressedSize   int64    `json:"compressed_size"`
	RawSize          int64    `json:"raw_size"`
	SHA256Compressed string   `json:"sha256_compressed"`
	SHA256Raw        string   `json:"sha256_raw,omitempty"`
	Description      string   `json:"description"`
}

// FileStatus 描述某个关键文件是否存在.
type FileStatus struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// Index 是 images/index.json 的顶层结构.
type Index struct {
	SchemaVersion int     `json:"schema_version"`
	Images        []Image `json:"images"`
}

// Deploy 是一次部署的历史记录.
type Deploy struct {
	ImageID      string    `json:"image_id"`
	TargetDisk   string    `json:"target_disk"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at,omitempty"`
	Result       string    `json:"result"` // running/completed/failed/cancelled
	BytesWritten int64     `json:"bytes_written"`
	Error        string    `json:"error,omitempty"`
}

// Node 是一台连接过平台的节点(主键 UUID).
type Node struct {
	UUID          string    `json:"uuid"`
	Hostname      string    `json:"hostname,omitempty"`
	MAC           string    `json:"mac"`
	IP            string    `json:"ip"`
	Architecture  string    `json:"arch"`
	BootMode      string    `json:"boot_mode"`
	KernelVersion string    `json:"kernel_version"`
	AlpineVersion string    `json:"alpine_version"`
	AgentVersion  string    `json:"agent_version"`
	AgentPort     int       `json:"agent_port,omitempty"`
	AgentURL      string    `json:"agent_url,omitempty"`
	Origin        string    `json:"origin,omitempty"`
	NetworkMode   string    `json:"network_mode,omitempty"`
	NetworkStatus string    `json:"network_status,omitempty"`
	ManagementIF  string    `json:"management_iface,omitempty"`
	Netmask       string    `json:"netmask,omitempty"`
	Gateway       string    `json:"gateway,omitempty"`
	DNS           []string  `json:"dns,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	Deploys       []Deploy  `json:"deploys"`
}

// Online 报告节点是否在线(last_seen 在超时阈值内).
func (n *Node) Online(now time.Time, timeout time.Duration) bool {
	return now.Sub(n.LastSeen) < timeout
}

// LastResult 取最后一次部署结果.
func (n *Node) LastResult() string {
	if len(n.Deploys) == 0 {
		return ""
	}
	return n.Deploys[len(n.Deploys)-1].Result
}

// DeployedEver 报告该节点是否部署过.
func (n *Node) DeployedEver() bool { return len(n.Deploys) > 0 }

// NodeView 是返回给前端的节点视图(附派生字段).
type NodeView struct {
	Node
	Status       string `json:"status"`        // online/offline
	LastResultV  string `json:"last_result"`   // 派生
	DeployedEver bool   `json:"deployed_ever"` // 派生
	Lifecycle    string `json:"lifecycle"`     // bootseed_online/deploying/completed/failed/offline
}

// AlpineBuild 是某架构内存系统构建的结构化信息.
type AlpineBuild struct {
	Ready            bool         `json:"ready"`
	KernelVersion    string       `json:"kernel_version"`
	AlpineVersion    string       `json:"alpine_version"`
	Modules          int          `json:"modules"`
	Firmware         int          `json:"firmware"`
	BuildTime        string       `json:"build_time"`
	Note             string       `json:"note,omitempty"` // 未构建/异常时的说明
	RequiredFiles    []FileStatus `json:"required_files,omitempty"`
	ExistingFiles    []string     `json:"existing_files,omitempty"`
	MissingFiles     []string     `json:"missing_files,omitempty"`
	IncludedModules  []string     `json:"included_modules,omitempty"`
	IncludedFirmware []string     `json:"included_firmware,omitempty"`
	IncludedTools    []string     `json:"included_tools,omitempty"`
}

// ServerInfo 是服务端配置/状态总览.
type ServerInfo struct {
	PXEServerIP   string                 `json:"pxe_server_ip"`
	HTTPPort      string                 `json:"http_port"`
	PXEInterface  string                 `json:"pxe_interface"`
	PXESubnet     string                 `json:"pxe_subnet"`
	EnterSecret   string                 `json:"enter_secret,omitempty"`
	Architectures []string               `json:"architectures"`
	AlpineVersion string                 `json:"alpine_version"`
	AgentVersion  string                 `json:"agent_version"`
	IPXERef       string                 `json:"ipxe_ref"`
	IPXEFiles     []FileStatus           `json:"ipxe_files"`    // 关键文件存在性
	AlpineBuilds  map[string]AlpineBuild `json:"alpine_builds"` // arch -> 构建信息
	Healthy       bool                   `json:"healthy"`
	Time          string                 `json:"time"`
}
