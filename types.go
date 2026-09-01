package mosaic

import (
	"context"
	"io"
	"net/http"
)

type Option func(*options)

type Client struct {
	transport *transport
}

type options struct {
	endpoint    string
	token       string
	tokenSource string
	httpClient  *http.Client
	retries     *int
}

type Command struct {
	Cmd  string   `json:"cmd,omitempty"`
	Argv []string `json:"argv,omitempty"`
}

func Shell(cmd string) Command    { return Command{Cmd: cmd} }
func Argv(argv ...string) Command { return Command{Argv: argv} }

type CreateOptions struct {
	Template       string
	SnapshotID     string
	Name           string
	Region         string
	MemoryMB       int
	VCPU           int
	EnableSSH      bool
	SSHPublicKey   string
	TTLSeconds     *int
	Persist        *bool
	Metadata       map[string]string
	Volumes        []VolumeMount
	Secrets        []string
	NetworkAllow   []string
	NetworkDeny    []string
	EgressPreset   string
	PresetOptions  []string
	VerifySNI      bool
	IdempotencyKey string
}

type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path,omitempty"`
	ReadOnly  bool   `json:"read_only"`
}

type ListOptions struct {
	Metadata map[string]string
	State    string
	Limit    int
	Cursor   string
}

type ExecOptions struct {
	Cwd       string
	Env       map[string]string
	Stdin     string
	TimeoutMs int
	User      string
}

type RunOnceOptions struct {
	CreateOptions
	ExecOptions
	KeepSandbox bool
}

type SandboxSummary struct {
	ID          string            `json:"id"`
	Name        *string           `json:"name"`
	Template    string            `json:"template"`
	State       string            `json:"state"`
	MemoryMB    int               `json:"memory_mb"`
	VCPU        int               `json:"vcpu"`
	SSHHost     *string           `json:"ssh_host,omitempty"`
	SSHPort     *int              `json:"ssh_port,omitempty"`
	TTLSeconds  *int              `json:"ttl_seconds,omitempty"`
	ExpiresAtNS *int64            `json:"expires_at_ns,omitempty"`
	Persist     *bool             `json:"persist,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Volume      string            `json:"volume,omitempty"`
	Volumes     []VolumeMount     `json:"volumes,omitempty"`
	Region      *string           `json:"region,omitempty"`
	Metro       *string           `json:"metro,omitempty"`
}

type SandboxPage struct {
	Sandboxes  []SandboxSummary `json:"sandboxes"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type SSHEndpoint struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	ConnectionString string `json:"connection_string"`
	PrivateKey       string `json:"private_key,omitempty"`
}

type Termination struct {
	Signal     int    `json:"signal"`
	SignalName string `json:"signal_name"`
	OOMKilled  bool   `json:"oom_killed"`
	OOMVictim  string `json:"oom_victim,omitempty"`
}

type ExecutionResult struct {
	SandboxID       string       `json:"sandbox_id"`
	ExecutionID     string       `json:"execution_id"`
	Stdout          string       `json:"stdout"`
	Stderr          string       `json:"stderr"`
	ExitCode        int          `json:"exit_code"`
	TTIMs           int          `json:"tti_ms"`
	DurationMS      float64      `json:"duration_ms"`
	Success         bool         `json:"success"`
	Reason          string       `json:"reason"`
	StdoutTruncated bool         `json:"stdout_truncated"`
	StderrTruncated bool         `json:"stderr_truncated"`
	Termination     *Termination `json:"termination,omitempty"`
}

type OneShotResult struct {
	ExecutionResult
	SandboxDestroyed bool     `json:"sandbox_destroyed"`
	TimingsMS        *Timings `json:"timings_ms,omitempty"`
}

type Timings struct {
	Create int `json:"create"`
	Total  int `json:"total"`
}

type ExecEvent struct {
	Type      string `json:"type"`
	Data      string `json:"data,omitempty"`
	ProcessID string `json:"process_id,omitempty"`
	SandboxID string `json:"sandbox_id,omitempty"`
	Message   string `json:"message,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
}

type FileInfo struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Size         int64  `json:"size"`
	ModifiedAtNS int64  `json:"modified_at_ns"`
}

type ProcessInfo struct {
	ID          string       `json:"id"`
	SandboxID   string       `json:"sandbox_id"`
	State       string       `json:"state"`
	PID         *int         `json:"pid"`
	ExitCode    *int         `json:"exit_code"`
	StartedAtNS int64        `json:"started_at_ns"`
	EndedAtNS   *int64       `json:"ended_at_ns"`
	PTY         bool         `json:"pty"`
	Termination *Termination `json:"termination,omitempty"`
}

type ProcessLogs struct {
	ProcessID        string `json:"process_id"`
	Stdout           string `json:"stdout"`
	Stderr           string `json:"stderr"`
	NextStdoutOffset int    `json:"next_stdout_offset"`
	NextStderrOffset int    `json:"next_stderr_offset"`
	State            string `json:"state"`
}

type SandboxEvent struct {
	AtMS   int64  `json:"at_ms"`
	Event  string `json:"event"`
	Detail string `json:"detail,omitempty"`
}

type SandboxTimeline struct {
	SandboxID       string         `json:"sandbox_id"`
	Running         bool           `json:"running"`
	DestroyedAt     *string        `json:"destroyed_at"`
	Events          []SandboxEvent `json:"events"`
	HostStartedAtMS *int64         `json:"host_started_at_ms"`
}

type TrajectoryProcess struct {
	ProcessInfo
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	OutputTruncated bool   `json:"output_truncated"`
}

type FilesystemChange struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	SizeBytes    int64  `json:"size_bytes"`
	ModifiedAtMS int64  `json:"modified_at_ms"`
}

type FilesystemDiff struct {
	Root      string             `json:"root"`
	SinceMS   int64              `json:"since_ms"`
	Truncated bool               `json:"truncated"`
	Changes   []FilesystemChange `json:"changes"`
}

type Trajectory struct {
	SandboxTimeline
	Template   *string             `json:"template"`
	MemoryMB   *int                `json:"memory_mb"`
	VCPU       *int                `json:"vcpu"`
	Processes  []TrajectoryProcess `json:"processes"`
	Filesystem *FilesystemDiff     `json:"filesystem"`
	Egress     *EgressPolicy       `json:"egress"`
}

type EgressPolicy struct {
	Mode         string                `json:"mode"`
	Enforced     bool                  `json:"enforced"`
	Allow        []string              `json:"allow"`
	Deny         []string              `json:"deny,omitempty"`
	Denied       []string              `json:"denied"`
	DeniedNames  []string              `json:"denied_names"`
	EgressPreset *ExpandedEgressPreset `json:"egress_preset,omitempty"`
}

type ExpandedEgressPreset struct {
	Name    string   `json:"name"`
	Options []string `json:"options"`
	Allow   []string `json:"allow"`
	Deny    []string `json:"deny"`
}

type EgressPresetCatalogue struct {
	Presets []EgressPresetCatalogueEntry `json:"presets"`
}

type EgressPresetCatalogueEntry struct {
	Name           string              `json:"name"`
	Summary        string              `json:"summary"`
	Allow          []string            `json:"allow"`
	Deny           []string            `json:"deny"`
	Options        map[string][]string `json:"options"`
	DefaultOptions []string            `json:"default_options"`
}

type NetworkPolicy struct {
	Mode          string
	Allow         []string
	Deny          []string
	EgressPreset  string
	PresetOptions []string
}

type SnapshotCoverage struct {
	Hosts       []string `json:"hosts"`
	FleetHosts  int      `json:"fleet_hosts"`
	Complete    bool     `json:"complete"`
	Remediation string   `json:"remediation,omitempty"`
}

type SnapshotInfo struct {
	ID                string            `json:"id"`
	Name              *string           `json:"name"`
	SourceSandboxID   string            `json:"source_sandbox_id"`
	Template          string            `json:"template"`
	MemoryMB          int               `json:"memory_mb"`
	VCPU              int               `json:"vcpu"`
	CreatedAtNS       int64             `json:"created_at_ns"`
	ExpiresAtNS       *int64            `json:"expires_at_ns"`
	State             string            `json:"state"`
	Replicas          *int              `json:"replicas,omitempty"`
	Coverage          *SnapshotCoverage `json:"coverage,omitempty"`
	LastUsedAt        *string           `json:"last_used_at,omitempty"`
	RetentionSeconds  *int              `json:"retention_seconds,omitempty"`
	ReclaimAfter      *string           `json:"reclaim_after,omitempty"`
	Region            *string           `json:"region,omitempty"`
	Metro             *string           `json:"metro,omitempty"`
	SourceImage       string            `json:"source_image,omitempty"`
	SourceImageDigest string            `json:"source_image_digest,omitempty"`
	ImageEntrypoint   []string          `json:"image_entrypoint,omitempty"`
	ImageCmd          []string          `json:"image_cmd,omitempty"`
	ImageWorkingDir   string            `json:"image_working_dir,omitempty"`
	ImageUser         string            `json:"image_user,omitempty"`
}

type VolumeInfo struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	CreatedAt string `json:"created_at,omitempty"`
	Objects   *int   `json:"objects,omitempty"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

type SecretInfo struct {
	Name       string `json:"name"`
	ValueBytes *int   `json:"value_bytes,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Rotated    bool   `json:"rotated,omitempty"`
	Readable   *bool  `json:"readable,omitempty"`
}

type SandboxMetrics struct {
	SandboxID        string   `json:"sandbox_id"`
	State            string   `json:"state"`
	VCPU             int      `json:"vcpu"`
	MemoryMB         int      `json:"memory_mb"`
	CPUSecondsTotal  float64  `json:"cpu_seconds_total"`
	CPUUsage         *float64 `json:"cpu_usage,omitempty"`
	CPUWindowSeconds *float64 `json:"cpu_window_seconds,omitempty"`
	MemoryUsedMB     int      `json:"memory_used_mb"`
	UptimeSeconds    float64  `json:"uptime_seconds"`
	AwakeSeconds     float64  `json:"awake_seconds"`
	IdleSeconds      float64  `json:"idle_seconds"`
}

type TemplateCatalogue struct {
	DefaultTemplate string                      `json:"default_template"`
	Templates       []TemplateCatalogueTemplate `json:"templates"`
}

type TemplateCatalogueTemplate struct {
	ID               string              `json:"id"`
	Status           string              `json:"status"`
	Creatable        bool                `json:"creatable"`
	DefaultMemoryMB  int                 `json:"default_memory_mb"`
	DefaultVCPU      int                 `json:"default_vcpu"`
	SupportedShapes  []SupportedShape    `json:"supported_shapes"`
	AvailableTools   []string            `json:"available_tools"`
	UnavailableTools []string            `json:"unavailable_tools"`
	Image            ImageCatalogueEntry `json:"image"`
}

type SupportedShape struct {
	MemoryMB     int     `json:"memory_mb"`
	VCPU         int     `json:"vcpu"`
	BillingUnits float64 `json:"billing_units"`
}

type ImageCatalogueEntry struct {
	Template string            `json:"template"`
	BuildID  string            `json:"build_id"`
	Debian   string            `json:"debian"`
	Versions map[string]string `json:"versions"`
	Tools    []string          `json:"tools"`
}

type ForkOptions struct {
	TTLSeconds     *int
	Metadata       map[string]string
	MinCount       *int
	IdempotencyKey string
}

type ForkResult struct {
	Count     int            `json:"count"`
	Requested int            `json:"requested"`
	Shortfall *ForkShortfall `json:"shortfall,omitempty"`
	Children  []*Sandbox
}

type ForkShortfall struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type Limits struct {
	Region          string           `json:"region"`
	Regions         []string         `json:"regions"`
	Placement       string           `json:"placement"`
	DefaultTemplate string           `json:"default_template"`
	Environments    map[string]any   `json:"environments"`
	Templates       []map[string]any `json:"templates"`
	Execution       map[string]any   `json:"execution"`
	Hibernation     map[string]any   `json:"hibernation"`
	Fork            map[string]any   `json:"fork"`
	Volume          map[string]any   `json:"volume"`
	Previews        map[string]any   `json:"previews"`
	Processes       map[string]any   `json:"processes"`
	Files           map[string]any   `json:"files"`
	Guest           map[string]any   `json:"guest"`
	Values          map[string]any   `json:"-"`
}

type ExecStream struct {
	ctx    context.Context
	body   io.ReadCloser
	buffer string
	event  ExecEvent
	err    error
	done   bool
}

type Files struct {
	sandbox *Sandbox
	user    string
}

type Processes struct {
	sandbox *Sandbox
}

type Process struct {
	ID           string
	Initial      ProcessInfo
	sandbox      *Sandbox
	stdoutOffset int
	stderrOffset int
}
