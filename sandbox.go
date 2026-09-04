package mosaic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	MaxUploadBytes     = 8 * 1024 * 1024
	MaxUploadFileBytes = 256 * 1024 * 1024
	UploadChunkBytes   = 6 * 1024 * 1024
)

func createBody(options CreateOptions) map[string]any {
	body := make(map[string]any)
	if options.SnapshotID != "" {
		body["snapshot_id"] = options.SnapshotID
		if options.Template != "" {
			body["template"] = options.Template
		}
		if options.MemoryMB != 0 {
			body["memory_mb"] = options.MemoryMB
		}
		if options.VCPU != 0 {
			body["vcpu"] = options.VCPU
		}
	} else {
		template := options.Template
		if template == "" {
			template = "base"
		}
		body["template"], body["memory_mb"], body["vcpu"] = template, valueOr(options.MemoryMB, 4096), valueOr(options.VCPU, 2)
	}
	body["enable_ssh"] = options.EnableSSH
	if options.Name != "" {
		body["name"] = options.Name
	}
	if options.SSHPublicKey != "" {
		body["ssh_public_key"] = options.SSHPublicKey
	}
	if options.TTLSeconds != nil {
		body["ttl_seconds"] = *options.TTLSeconds
	}
	if options.Persist != nil {
		body["persist"] = *options.Persist
	}
	if options.Metadata != nil {
		body["metadata"] = options.Metadata
	}
	if options.Volumes != nil {
		body["volumes"] = options.Volumes
	}
	if options.Secrets != nil {
		body["secrets"] = options.Secrets
	}
	if options.NetworkAllow != nil || options.NetworkDeny != nil || options.EgressPreset != "" || options.PresetOptions != nil {
		network := make(map[string]any)
		if options.EgressPreset != "" {
			network["preset"] = options.EgressPreset
		}
		if options.PresetOptions != nil {
			network["preset_options"] = options.PresetOptions
		}
		if options.NetworkAllow != nil {
			network["allow"] = options.NetworkAllow
		}
		if options.NetworkDeny != nil {
			network["deny"] = options.NetworkDeny
		}
		if options.VerifySNI {
			network["verify_sni"] = true
		}
		body["network"] = network
	}
	if options.Region != "" {
		body["region"] = options.Region
	}
	return body
}

func valueOr(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

type sandboxWire struct {
	ID                  string  `json:"id"`
	Name                *string `json:"name"`
	Region              *string `json:"region"`
	Metro               *string `json:"metro"`
	TTIMS               float64 `json:"tti_ms"`
	Reused              bool    `json:"reused"`
	Resumed             bool    `json:"resumed"`
	SSHHost             string  `json:"ssh_host"`
	SSHPort             int     `json:"ssh_port"`
	SSHConnectionString string  `json:"ssh_connection_string"`
	SSHPrivateKey       string  `json:"ssh_private_key"`
}

func (c *Client) newSandbox(value sandboxWire, options CreateOptions) *Sandbox {
	sandbox := &Sandbox{
		client: c, ID: value.ID, Name: stringValue(value.Name), Region: stringValue(value.Region), Metro: stringValue(value.Metro),
		TTIMs: value.TTIMS, Reused: value.Reused, Resumed: value.Resumed,
	}
	if value.SSHHost != "" && value.SSHPort != 0 {
		sandbox.SSH = &SSHEndpoint{Host: value.SSHHost, Port: value.SSHPort, ConnectionString: value.SSHConnectionString, PrivateKey: value.SSHPrivateKey}
		if sandbox.SSH.ConnectionString == "" {
			sandbox.SSH.ConnectionString = fmt.Sprintf("ssh -p %d root@%s", value.SSHPort, value.SSHHost)
		}
	}
	return sandbox
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (c *Client) CreateSandbox(ctx context.Context, options CreateOptions) (*Sandbox, error) {
	var value sandboxWire
	if err := c.transport.requestInto(ctx, http.MethodPost, "/v1/sandboxes", createBody(options), options.IdempotencyKey, &value); err != nil {
		return nil, err
	}
	return c.newSandbox(value, options), nil
}

func (c *Client) GetOrCreateSandbox(ctx context.Context, metadata map[string]string, options CreateOptions) (*Sandbox, error) {
	bodyOptions := options
	if metadata != nil {
		bodyOptions.Metadata = metadata
	}
	var value sandboxWire
	if err := c.transport.requestInto(ctx, http.MethodPut, "/v1/sandboxes", createBody(bodyOptions), options.IdempotencyKey, &value); err != nil {
		return nil, err
	}
	return c.newSandbox(value, options), nil
}

func (c *Client) SandboxFromSnapshot(ctx context.Context, snapshot string, options CreateOptions) (*Sandbox, error) {
	options.SnapshotID = snapshot
	return c.CreateSandbox(ctx, options)
}

func (c *Client) ConnectSandbox(ctx context.Context, id string) (*Sandbox, error) {
	var value sandboxWire
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/sandboxes/"+url.PathEscape(id), nil, "", &value); err != nil {
		return nil, err
	}
	return c.newSandbox(value, CreateOptions{}), nil
}

func (c *Client) ListSandboxes(ctx context.Context, options ListOptions) ([]SandboxSummary, error) {
	page, err := c.ListSandboxesPage(ctx, options)
	if err != nil {
		return nil, err
	}
	return page.Sandboxes, nil
}

func (c *Client) ListSandboxesPage(ctx context.Context, options ListOptions) (*SandboxPage, error) {
	query := url.Values{}
	for key, value := range options.Metadata {
		query.Set("metadata."+key, value)
	}
	if options.State != "" {
		query.Set("state", options.State)
	}
	if options.Limit != 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	path := "/v1/sandboxes"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var wire struct {
		Sandboxes  []SandboxSummary `json:"sandboxes"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := c.transport.requestInto(ctx, http.MethodGet, path, nil, "", &wire); err != nil {
		return nil, err
	}
	return &SandboxPage{Sandboxes: wire.Sandboxes, NextCursor: wire.NextCursor}, nil
}

func (c *Client) SandboxTimeline(ctx context.Context, id string) (*SandboxTimeline, error) {
	var timeline SandboxTimeline
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/sandboxes/"+url.PathEscape(id)+"/events", nil, "", &timeline); err != nil {
		return nil, err
	}
	return &timeline, nil
}

func (c *Client) RunOnce(ctx context.Context, command Command, options RunOnceOptions) (*OneShotResult, error) {
	body := createBody(options.CreateOptions)
	for key, value := range commandMap(command) {
		body[key] = value
	}
	if options.Cwd != "" {
		body["cwd"] = options.Cwd
	}
	if options.Env != nil {
		body["env"] = options.Env
	}
	if options.Stdin != "" {
		body["stdin"] = options.Stdin
	}
	if options.TimeoutMs != 0 {
		body["timeout_ms"] = options.TimeoutMs
	}
	if options.KeepSandbox {
		body["keep_sandbox"] = true
	}
	var result OneShotResult
	if err := c.transport.requestInto(ctx, http.MethodPost, "/v1/run", body, options.IdempotencyKey, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Function is a reusable client-side function specification. Invoke uses the
// existing one-shot create, exec, and destroy lifecycle and creates no
// persistent control-plane resource, so it inherits that route's cleanup,
// timeout, idempotency, secret and network-policy behaviour.
type Function struct {
	client  *Client
	command Command
	options RunOnceOptions
}

// InvocationOverrides are the per-call inputs one invocation may replace. What
// the sandbox is - template, secrets, network policy, resources - belongs to
// the specification and is fixed for every invocation. The fields are pointers
// so that an invocation can also clear a specification's default: a supplied
// empty Cwd or Stdin is applied, an omitted one is not.
type InvocationOverrides struct {
	Cwd            *string
	Env            map[string]string
	Stdin          *string
	TimeoutMs      *int
	IdempotencyKey string
}

// FunctionOptions is what a function specification is made of: the sandbox
// every invocation gets and the exec defaults every invocation runs with. It
// spells those fields out rather than embedding RunOnceOptions so that the two
// per-call options cannot be written here at all: KeepSandbox, because kept
// once is a sandbox left behind on every invocation and a function's cleanup is
// the service's, and IdempotencyKey, because a key identifies one invocation
// and reusing it would replay the first result or fail as a reused key. Use
// RunOnce for a single run that keeps its sandbox, and InvokeWith for a key.
type FunctionOptions struct {
	Template      string
	SnapshotID    string
	Name          string
	Region        string
	MemoryMB      int
	VCPU          int
	EnableSSH     bool
	SSHPublicKey  string
	TTLSeconds    *int
	Persist       *bool
	Metadata      map[string]string
	Volumes       []VolumeMount
	Secrets       []string
	NetworkAllow  []string
	NetworkDeny   []string
	EgressPreset  string
	PresetOptions []string
	VerifySNI     bool

	Cwd       string
	Env       map[string]string
	Stdin     string
	TimeoutMs int
	User      string
}

func (o FunctionOptions) runOnceOptions() RunOnceOptions {
	return RunOnceOptions{
		CreateOptions: CreateOptions{
			Template:      o.Template,
			SnapshotID:    o.SnapshotID,
			Name:          o.Name,
			Region:        o.Region,
			MemoryMB:      o.MemoryMB,
			VCPU:          o.VCPU,
			EnableSSH:     o.EnableSSH,
			SSHPublicKey:  o.SSHPublicKey,
			TTLSeconds:    o.TTLSeconds,
			Persist:       o.Persist,
			Metadata:      o.Metadata,
			Volumes:       o.Volumes,
			Secrets:       o.Secrets,
			NetworkAllow:  o.NetworkAllow,
			NetworkDeny:   o.NetworkDeny,
			EgressPreset:  o.EgressPreset,
			PresetOptions: o.PresetOptions,
			VerifySNI:     o.VerifySNI,
		},
		ExecOptions: ExecOptions{
			Cwd:       o.Cwd,
			Env:       o.Env,
			Stdin:     o.Stdin,
			TimeoutMs: o.TimeoutMs,
			User:      o.User,
		},
	}
}

// Function takes a copy of the command and options, so that later changes to
// the caller's slices and maps cannot rewrite what an invocation runs.
func (c *Client) Function(command Command, options FunctionOptions) *Function {
	return &Function{
		client:  c,
		command: cloneCommand(command),
		options: cloneRunOnceOptions(options.runOnceOptions()),
	}
}

func (f *Function) Invoke(ctx context.Context) (*OneShotResult, error) {
	return f.client.RunOnce(ctx, cloneCommand(f.command), cloneRunOnceOptions(f.options))
}

// InvokeWith invokes the function with per-call inputs applied on top of the
// specification, which is left unchanged.
func (f *Function) InvokeWith(ctx context.Context, overrides InvocationOverrides) (*OneShotResult, error) {
	options := cloneRunOnceOptions(f.options)
	if overrides.Cwd != nil {
		options.Cwd = *overrides.Cwd
	}
	if overrides.Env != nil {
		options.Env = cloneStringMap(overrides.Env)
	}
	if overrides.Stdin != nil {
		options.Stdin = *overrides.Stdin
	}
	if overrides.TimeoutMs != nil {
		options.TimeoutMs = *overrides.TimeoutMs
	}
	options.IdempotencyKey = overrides.IdempotencyKey
	return f.client.RunOnce(ctx, cloneCommand(f.command), options)
}

func cloneCommand(command Command) Command {
	command.Argv = cloneStrings(command.Argv)
	return command
}

func cloneRunOnceOptions(options RunOnceOptions) RunOnceOptions {
	options.Metadata = cloneStringMap(options.Metadata)
	options.Env = cloneStringMap(options.Env)
	options.Secrets = cloneStrings(options.Secrets)
	options.NetworkAllow = cloneStrings(options.NetworkAllow)
	options.NetworkDeny = cloneStrings(options.NetworkDeny)
	options.PresetOptions = cloneStrings(options.PresetOptions)
	if options.Volumes != nil {
		options.Volumes = append([]VolumeMount(nil), options.Volumes...)
	}
	if options.TTLSeconds != nil {
		ttl := *options.TTLSeconds
		options.TTLSeconds = &ttl
	}
	if options.Persist != nil {
		persist := *options.Persist
		options.Persist = &persist
	}
	return options
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func commandMap(command Command) map[string]any {
	if command.Cmd != "" {
		return map[string]any{"cmd": command.Cmd}
	}
	return map[string]any{"argv": command.Argv}
}

// WithSandbox creates a sandbox, invokes fn, and releases the sandbox using a
// detached context with a 30-second cleanup deadline.
func (c *Client) WithSandbox(ctx context.Context, options CreateOptions, fn func(context.Context, *Sandbox) error) (err error) {
	sandbox, err := c.CreateSandbox(ctx, options)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		destroyErr := sandbox.Destroy(cleanupCtx)
		if destroyErr != nil {
			if err != nil {
				err = errors.Join(err, destroyErr)
			} else {
				err = destroyErr
			}
		}
	}()
	return fn(ctx, sandbox)
}

type Sandbox struct {
	ID      string
	Name    string
	Region  string
	Metro   string
	TTIMs   float64
	SSH     *SSHEndpoint
	Reused  bool
	Resumed bool
	client  *Client
}

func (s *Sandbox) call(ctx context.Context, method, suffix string, body any, key string, output any) error {
	return s.client.transport.requestInto(ctx, method, "/v1/sandboxes/"+url.PathEscape(s.ID)+suffix, body, key, output)
}

func (s *Sandbox) Exec(ctx context.Context, command Command, options ExecOptions) (*ExecutionResult, error) {
	body := commandMap(command)
	addExecOptions(body, options)
	var result ExecutionResult
	if err := s.call(ctx, http.MethodPost, "/exec", body, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func addExecOptions(body map[string]any, options ExecOptions) {
	if options.Cwd != "" {
		body["cwd"] = options.Cwd
	}
	if options.Env != nil {
		body["env"] = options.Env
	}
	if options.Stdin != "" {
		body["stdin"] = options.Stdin
	}
	if options.TimeoutMs != 0 {
		body["timeout_ms"] = options.TimeoutMs
	}
	if options.User != "" {
		body["user"] = options.User
	}
}

func (s *Sandbox) ExecStream(ctx context.Context, command Command, options ExecOptions) (*ExecStream, error) {
	body := commandMap(command)
	addExecOptions(body, options)
	body["stream"] = true
	response, err := s.client.transport.open(ctx, http.MethodPost, "/v1/sandboxes/"+url.PathEscape(s.ID)+"/exec", body)
	if err != nil {
		return nil, err
	}
	return &ExecStream{ctx: ctx, body: response.Body}, nil
}

func (s *Sandbox) Destroy(ctx context.Context) error {
	return s.client.transport.destroy(ctx, "/v1/sandboxes/"+url.PathEscape(s.ID))
}

func (s *Sandbox) Pause(ctx context.Context) error {
	return s.call(ctx, http.MethodPost, "/pause", map[string]any{}, "", nil)
}

func (s *Sandbox) Resume(ctx context.Context) error {
	return s.call(ctx, http.MethodPost, "/resume", map[string]any{}, "", nil)
}

func (s *Sandbox) SetTimeout(ctx context.Context, ttlSeconds int) (*SandboxSummary, error) {
	var result SandboxSummary
	if err := s.call(ctx, http.MethodPost, "/timeout", map[string]any{"ttl_seconds": ttlSeconds}, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Sandbox) Metrics(ctx context.Context) (*SandboxMetrics, error) {
	var result SandboxMetrics
	if err := s.call(ctx, http.MethodGet, "/metrics", nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Sandbox) Timeline(ctx context.Context) (*SandboxTimeline, error) {
	var result SandboxTimeline
	if err := s.call(ctx, http.MethodGet, "/events", nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Sandbox) Trajectory(ctx context.Context) (*Trajectory, error) {
	var result Trajectory
	if err := s.call(ctx, http.MethodGet, "/trajectory", nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Sandbox) EgressPolicy(ctx context.Context) (*EgressPolicy, error) {
	var result EgressPolicy
	if err := s.call(ctx, http.MethodGet, "/egress", nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Sandbox) SetNetwork(ctx context.Context, policy NetworkPolicy) (*EgressPolicy, error) {
	if (policy.Mode == "allow_all" || policy.Mode == "deny_all") && (len(policy.Allow) > 0 || len(policy.Deny) > 0 || policy.EgressPreset != "" || len(policy.PresetOptions) > 0) {
		return nil, errors.New("allow_all and deny_all network modes cannot be combined with network lists or presets")
	}
	body := map[string]any{}
	if policy.Mode != "" {
		body["mode"] = policy.Mode
	}
	if policy.Allow != nil {
		body["allow"] = policy.Allow
	}
	if policy.Deny != nil {
		body["deny"] = policy.Deny
	}
	if policy.EgressPreset != "" {
		body["preset"] = policy.EgressPreset
	}
	if policy.PresetOptions != nil {
		body["preset_options"] = policy.PresetOptions
	}
	var result EgressPolicy
	if err := s.call(ctx, http.MethodPost, "/network", body, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type SnapshotOptions struct {
	Name             string
	RetentionSeconds *int
	IdempotencyKey   string
}

func (s *Sandbox) Snapshot(ctx context.Context, options SnapshotOptions) (*SnapshotInfo, error) {
	body := map[string]any{}
	if options.Name != "" {
		body["name"] = options.Name
	}
	if options.RetentionSeconds != nil {
		body["retention_seconds"] = *options.RetentionSeconds
	}
	var result SnapshotInfo
	if err := s.call(ctx, http.MethodPost, "/snapshots", body, options.IdempotencyKey, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Sandbox) Fork(ctx context.Context, options ForkOptions) (*Sandbox, error) {
	result, err := s.ForkMany(ctx, 1, options)
	if err != nil {
		return nil, err
	}
	if len(result.Children) == 0 {
		return nil, errors.New("fork returned no children")
	}
	return result.Children[0], nil
}

func (s *Sandbox) ForkMany(ctx context.Context, count int, options ForkOptions) (*ForkResult, error) {
	body := map[string]any{"count": count}
	if options.TTLSeconds != nil {
		body["ttl_seconds"] = *options.TTLSeconds
	}
	if options.Metadata != nil {
		body["metadata"] = options.Metadata
	}
	if options.MinCount != nil {
		body["min_count"] = *options.MinCount
	}
	var wire struct {
		Count     int            `json:"count"`
		Requested int            `json:"requested"`
		Shortfall *ForkShortfall `json:"shortfall"`
		Children  []sandboxWire  `json:"children"`
	}
	if err := s.call(ctx, http.MethodPost, "/fork", body, options.IdempotencyKey, &wire); err != nil {
		return nil, err
	}
	children := make([]*Sandbox, 0, len(wire.Children))
	for _, child := range wire.Children {
		children = append(children, s.client.newSandbox(child, CreateOptions{}))
	}
	result := &ForkResult{Count: wire.Count, Requested: wire.Requested, Shortfall: wire.Shortfall, Children: children}
	if result.Count == 0 {
		result.Count = len(children)
	}
	if result.Requested == 0 {
		result.Requested = count
	}
	return result, nil
}

func (s *Sandbox) Files() *Files         { return &Files{sandbox: s} }
func (s *Sandbox) Processes() *Processes { return &Processes{sandbox: s} }

func (s *Sandbox) baseURL() string {
	return s.client.Endpoint() + "/v1/sandboxes/" + url.PathEscape(s.ID)
}

func (f *Files) As(user string) *Files {
	copy := *f
	copy.user = user
	return &copy
}

func (f *Files) Read(ctx context.Context, path string) ([]byte, error) {
	suffix := "?path=" + url.QueryEscape(path)
	if f.user != "" {
		suffix += "&user=" + url.QueryEscape(f.user)
	}
	var value struct {
		ContentBase64 string `json:"content_base64"`
	}
	if err := f.sandbox.call(ctx, http.MethodGet, "/files/content"+suffix, nil, "", &value); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(value.ContentBase64)
}

func (f *Files) ReadString(ctx context.Context, path string) (string, error) {
	content, err := f.Read(ctx, path)
	return string(content), err
}

func (f *Files) Write(ctx context.Context, path string, content []byte) (int64, error) {
	if len(content) > MaxUploadFileBytes {
		return 0, fmt.Errorf("content for %s exceeds the %d byte maximum file upload limit", path, MaxUploadFileBytes)
	}
	if len(content) <= MaxUploadBytes {
		var value struct {
			Size int64 `json:"size"`
		}
		err := f.sandbox.call(ctx, http.MethodPut, "/files/content", f.writePayload(path, content, false), "", &value)
		return value.Size, err
	}
	landed := 0
	for offset := 0; offset < len(content); offset += UploadChunkBytes {
		end := offset + UploadChunkBytes
		if end > len(content) {
			end = len(content)
		}
		if err := f.sandbox.call(ctx, http.MethodPut, "/files/content", f.writePayload(path, content[offset:end], offset > 0), "", nil); err != nil {
			return int64(landed), &Error{Code: "file_upload_partial", Message: fmt.Sprintf("upload of %s failed after %d bytes landed", path, landed), cause: err}
		}
		landed += end - offset
	}
	return int64(len(content)), nil
}

func (f *Files) writePayload(path string, content []byte, appendChunk bool) map[string]any {
	payload := map[string]any{"path": path, "content_base64": base64.StdEncoding.EncodeToString(content), "create_parents": true}
	if f.user != "" {
		payload["user"] = f.user
	}
	if appendChunk {
		payload["append"] = true
	}
	return payload
}

func (f *Files) WriteString(ctx context.Context, path, content string) (int64, error) {
	return f.Write(ctx, path, []byte(content))
}

func (f *Files) List(ctx context.Context, path string) ([]FileInfo, error) {
	if path == "" {
		path = "/workspace"
	}
	suffix := "?path=" + url.QueryEscape(path)
	if f.user != "" {
		suffix += "&user=" + url.QueryEscape(f.user)
	}
	var value struct {
		Entries []FileInfo `json:"entries"`
	}
	if err := f.sandbox.call(ctx, http.MethodGet, "/files"+suffix, nil, "", &value); err != nil {
		return nil, err
	}
	return value.Entries, nil
}

func (f *Files) Mkdir(ctx context.Context, path string, recursive bool) error {
	body := map[string]any{"path": path, "recursive": recursive}
	if f.user != "" {
		body["user"] = f.user
	}
	return f.sandbox.call(ctx, http.MethodPost, "/files/mkdir", body, "", nil)
}

func (f *Files) Remove(ctx context.Context, path string, recursive bool) error {
	suffix := "?path=" + url.QueryEscape(path) + "&recursive=" + strconv.FormatBool(recursive)
	if f.user != "" {
		suffix += "&user=" + url.QueryEscape(f.user)
	}
	return f.sandbox.call(ctx, http.MethodDelete, "/files/content"+suffix, nil, "", nil)
}

func (f *Files) Move(ctx context.Context, source, destination string, overwrite bool) error {
	body := map[string]any{"source": source, "destination": destination, "overwrite": overwrite}
	if f.user != "" {
		body["user"] = f.user
	}
	return f.sandbox.call(ctx, http.MethodPost, "/files/move", body, "", nil)
}

func (p *Processes) Start(ctx context.Context, command Command, options StartOptions) (*Process, error) {
	body := commandMap(command)
	addExecOptions(body, options.ExecOptions)
	body["pty"] = options.PTY
	var info ProcessInfo
	if err := p.sandbox.call(ctx, http.MethodPost, "/processes", body, "", &info); err != nil {
		return nil, err
	}
	return &Process{ID: info.ID, Initial: info, sandbox: p.sandbox}, nil
}

func (p *Processes) List(ctx context.Context) ([]*Process, error) {
	var value struct {
		Processes []ProcessInfo `json:"processes"`
	}
	if err := p.sandbox.call(ctx, http.MethodGet, "/processes", nil, "", &value); err != nil {
		return nil, err
	}
	result := make([]*Process, 0, len(value.Processes))
	for _, info := range value.Processes {
		result = append(result, &Process{ID: info.ID, Initial: info, sandbox: p.sandbox})
	}
	return result, nil
}

func (p *Processes) Run(ctx context.Context, command Command, options ExecOptions) (*ExecutionResult, error) {
	process, err := p.Start(ctx, command, StartOptions{ExecOptions: options})
	if err != nil {
		return nil, err
	}
	return process.Wait(ctx)
}

type StartOptions struct {
	ExecOptions
	PTY bool
}

func (p *Process) Info(ctx context.Context) (*ProcessInfo, error) {
	var info ProcessInfo
	if err := p.sandbox.call(ctx, http.MethodGet, "/processes/"+url.PathEscape(p.ID), nil, "", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (p *Process) Logs(ctx context.Context) (*ProcessLogs, error) {
	suffix := "/processes/" + url.PathEscape(p.ID) + "/logs?stdout_offset=" + strconv.Itoa(p.stdoutOffset) + "&stderr_offset=" + strconv.Itoa(p.stderrOffset)
	var logs ProcessLogs
	if err := p.sandbox.call(ctx, http.MethodGet, suffix, nil, "", &logs); err != nil {
		return nil, err
	}
	p.stdoutOffset, p.stderrOffset = logs.NextStdoutOffset, logs.NextStderrOffset
	return &logs, nil
}

func (p *Process) SendInput(ctx context.Context, data string, closeStdin bool) error {
	return p.sandbox.call(ctx, http.MethodPost, "/processes/"+url.PathEscape(p.ID)+"/input", map[string]any{"data": data, "close": closeStdin}, "", nil)
}

func (p *Process) CloseStdin(ctx context.Context) error { return p.SendInput(ctx, "", true) }

func (p *Process) Kill(ctx context.Context) error {
	return p.sandbox.call(ctx, http.MethodDelete, "/processes/"+url.PathEscape(p.ID), nil, "", nil)
}

func (p *Process) Wait(ctx context.Context) (*ExecutionResult, error) {
	stdout, stderr := "", ""
	for {
		logs, err := p.Logs(ctx)
		if err != nil {
			return nil, fmt.Errorf("process %s: %w", p.ID, err)
		}
		stdout += logs.Stdout
		stderr += logs.Stderr
		if logs.State == "finished" {
			break
		}
		if err := p.sandbox.client.transport.sleep(ctx, 100*time.Millisecond); err != nil {
			return nil, fmt.Errorf("process %s: %w", p.ID, err)
		}
	}
	info, err := p.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("process %s: %w", p.ID, err)
	}
	exitCode := 0
	if info.ExitCode != nil {
		exitCode = *info.ExitCode
	}
	reason := "completed"
	if info.Termination != nil {
		if info.Termination.OOMKilled {
			reason = "oom_killed"
		} else {
			reason = "signal"
		}
	} else if exitCode != 0 {
		reason = "exit_nonzero"
	}
	duration := float64(0)
	if info.EndedAtNS != nil {
		duration = float64(*info.EndedAtNS-info.StartedAtNS) / 1e6
	} else {
		duration = float64(time.Now().UnixNano()-info.StartedAtNS) / 1e6
	}
	return &ExecutionResult{
		SandboxID: p.sandbox.ID, ExecutionID: p.ID, Stdout: stdout, Stderr: stderr,
		ExitCode: exitCode, DurationMS: duration, Success: exitCode == 0, Reason: reason,
		Termination: info.Termination,
	}, nil
}

func (p *Process) StreamURL() string {
	return p.sandbox.baseURL() + "/processes/" + url.PathEscape(p.ID) + "/stream?stdout_offset=" + strconv.Itoa(p.stdoutOffset) + "&stderr_offset=" + strconv.Itoa(p.stderrOffset)
}

func (s *ExecStream) Next() bool {
	if s.err != nil || (s.done && s.buffer == "") {
		return false
	}
	for {
		if index := strings.Index(s.buffer, "\n\n"); index >= 0 {
			frame := s.buffer[:index]
			s.buffer = s.buffer[index+2:]
			if event, ok := parseSSE(frame); ok {
				s.event = event
				return true
			}
			continue
		}
		select {
		case <-s.ctx.Done():
			s.err = s.ctx.Err()
			_ = s.body.Close()
			return false
		default:
		}
		chunk := make([]byte, 4096)
		n, err := s.body.Read(chunk)
		if n > 0 {
			s.buffer += string(chunk[:n])
		}
		if err != nil {
			if err == io.EOF {
				s.done = true
				if index := strings.Index(s.buffer, "\n\n"); index >= 0 {
					frame := s.buffer[:index]
					s.buffer = s.buffer[index+2:]
					if event, ok := parseSSE(frame); ok {
						s.event = event
						return true
					}
				}
				if event, ok := parseSSE(s.buffer); ok {
					s.buffer = ""
					s.event = event
					return true
				}
				_ = s.body.Close()
				return false
			}
			s.err = err
			_ = s.body.Close()
			return false
		}
	}
}

func (s *ExecStream) Event() ExecEvent { return s.event }
func (s *ExecStream) Err() error       { return s.err }
func (s *ExecStream) Close() error {
	if s.body == nil {
		return nil
	}
	_, _ = io.Copy(io.Discard, s.body)
	err := s.body.Close()
	s.done = true
	return err
}

func parseSSE(frame string) (ExecEvent, bool) {
	var name string
	var data []string
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(line, "event:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if name == "" {
		return ExecEvent{}, false
	}
	var payload struct {
		Text      string `json:"text"`
		ProcessID string `json:"process_id"`
		SandboxID string `json:"sandbox_id"`
		Message   string `json:"message"`
		ExitCode  *int   `json:"exit_code"`
	}
	_ = json.Unmarshal([]byte(strings.Join(data, "\n")), &payload)
	event := ExecEvent{Type: name}
	event.Data, event.ProcessID, event.SandboxID, event.Message, event.ExitCode = payload.Text, payload.ProcessID, payload.SandboxID, payload.Message, payload.ExitCode
	return event, true
}
