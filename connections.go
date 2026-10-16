package mosaic

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type ConnectionOperation struct {
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	AllowQuery bool              `json:"allow_query,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}
type ConnectionInjection struct {
	Mode        string `json:"mode"`
	Header      string `json:"header,omitempty"`
	UsernameKey string `json:"username_key,omitempty"`
}
type ConnectionAuthentication struct {
	Type           string              `json:"type"`
	Namespace      string              `json:"namespace"`
	Name           string              `json:"name"`
	Key            string              `json:"key"`
	Inject         ConnectionInjection `json:"inject"`
	RoleARN        string              `json:"role_arn,omitempty"`
	Region         string              `json:"region,omitempty"`
	Service        string              `json:"service,omitempty"`
	SessionSeconds int                 `json:"session_seconds,omitempty"`
	SessionPolicy  map[string]any      `json:"session_policy,omitempty"`
}

func (a ConnectionAuthentication) MarshalJSON() ([]byte, error) {
	if a.Type == "aws_oidc" {
		return json.Marshal(map[string]any{"type": a.Type, "role_arn": a.RoleARN, "region": a.Region, "service": a.Service, "session_seconds": a.SessionSeconds, "session_policy": a.SessionPolicy})
	}
	type wire ConnectionAuthentication
	return json.Marshal(wire(a))
}

type ConnectionFederationInfo struct {
	Audience                          string `json:"audience"`
	BootstrapGuestReadable            bool   `json:"bootstrap_guest_readable"`
	TemporaryCredentialsGuestReadable bool   `json:"temporary_credentials_guest_readable"`
}
type ConnectionService struct {
	Host       string                `json:"host"`
	Operations []ConnectionOperation `json:"operations"`
}
type ConnectionWorkloads struct {
	Mode       string   `json:"mode"`
	SandboxIDs []string `json:"sandbox_ids"`
}
type ConnectionNetwork struct {
	Kind      string                   `json:"kind"`
	Profile   *NetworkProfileReference `json:"profile,omitempty"`
	Service   string                   `json:"service,omitempty"`
	Readiness string                   `json:"readiness,omitempty"`
	Usable    *bool                    `json:"usable,omitempty"`
}
type ConnectionPolicy struct {
	Service            ConnectionService        `json:"service"`
	Authentication     ConnectionAuthentication `json:"authentication"`
	Consumers          []string                 `json:"consumers"`
	Workloads          ConnectionWorkloads      `json:"workloads"`
	Network            ConnectionNetwork        `json:"network"`
	MaxGrantTTLSeconds int                      `json:"max_grant_ttl_seconds"`
	ExpiresAt          *string                  `json:"expires_at,omitempty"`
}
type ConnectionInfo struct {
	Federation *ConnectionFederationInfo `json:"federation,omitempty"`
	ConnectionPolicy
	ID                             string  `json:"id"`
	Namespace                      string  `json:"namespace"`
	Name                           string  `json:"name"`
	Revision                       int     `json:"revision"`
	RevokedAt                      *string `json:"revoked_at"`
	CreatedAt                      string  `json:"created_at"`
	UpdatedAt                      string  `json:"updated_at"`
	ServiceCredentialGuestReadable bool    `json:"service_credential_guest_readable"`
}
type ConnectionGrantInfo struct {
	ID                string   `json:"id"`
	ConnectionID      string   `json:"connection_id"`
	Revision          int      `json:"revision"`
	SandboxID         string   `json:"sandbox_id"`
	Operations        []string `json:"operations"`
	ExpiresAt         string   `json:"expires_at"`
	ProxyURL          string   `json:"proxy_url,omitempty"`
	RevokedAt         *string  `json:"revoked_at,omitempty"`
	NetworkProvenance bool     `json:"network_provenance"`
}
type ConnectionResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}

func (r ConnectionResponse) Format(state fmt.State, verb rune) {
	fmt.Fprintf(state, "ConnectionResponse{Status:%d, Body:[redacted]}", r.Status)
}

type ConnectionGrant struct {
	info       ConnectionGrantInfo
	credential string
	client     *http.Client
}

func (g ConnectionGrant) Metadata() ConnectionGrantInfo {
	info := g.info
	info.Operations = append([]string(nil), info.Operations...)
	return info
}
func (g ConnectionGrant) BearerToken() string { return g.credential }
func (g ConnectionGrant) Format(state fmt.State, verb rune) {
	fmt.Fprintf(state, "ConnectionGrant{ID:%s, Credential:[redacted]}", g.info.ID)
}
func (g ConnectionGrant) MarshalJSON() ([]byte, error) { return json.Marshal(g.Metadata()) }

var connectionCapability = regexp.MustCompile("^mcg_live_[0-9a-f]{64}$")

func privateHTTPClient(client *http.Client) (*http.Client, error) {
	cloned := *client
	// A dedicated transport with no reusable connections avoids net/http's
	// implicit retry of requests on a stale pooled connection.
	transport := cloned.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if base, ok := transport.(*http.Transport); ok {
		private := base.Clone()
		private.DisableKeepAlives = true
		private.ForceAttemptHTTP2 = false
		private.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		if private.TLSClientConfig == nil {
			private.TLSClientConfig = &tls.Config{}
		} else {
			private.TLSClientConfig = private.TLSClientConfig.Clone()
		}
		if private.TLSClientConfig.InsecureSkipVerify {
			return nil, errors.New("Private requests require TLS certificate verification")
		}
		private.TLSClientConfig.NextProtos = []string{"http/1.1"}
		if private.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			private.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
		private.Proxy = nil
		cloned.Transport = private
	} else {
		return nil, errors.New("Private requests require a standard HTTP transport without custom retry middleware")
	}
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if cloned.Timeout == 0 || cloned.Timeout > 25*time.Second {
		cloned.Timeout = 25 * time.Second
	}
	return &cloned, nil
}

func newConnectionGrant(info ConnectionGrantInfo, credential, endpoint string, client *http.Client) (*ConnectionGrant, error) {
	target, err := url.Parse(info.ProxyURL)
	if err != nil {
		return nil, errors.New("invalid Connection proxy")
	}
	expected, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("invalid Connection endpoint")
	}
	if target.Scheme != expected.Scheme || target.Host != expected.Host || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Path != "/connect/"+info.ConnectionID {
		return nil, errors.New("Connection proxy must match configured endpoint origin and resource")
	}
	if target.Scheme != "https" {
		ip := net.ParseIP(target.Hostname())
		if target.Scheme != "http" || (target.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("Connection capabilities require HTTPS outside loopback fixtures")
		}
	}
	if !connectionCapability.MatchString(credential) {
		return nil, errors.New("invalid Connection capability")
	}
	cloned, err := privateHTTPClient(client)
	if err != nil {
		return nil, err
	}
	info.NetworkProvenance = false
	info.Operations = append([]string(nil), info.Operations...)
	return &ConnectionGrant{info: info, credential: credential, client: cloned}, nil
}

// Request never retries or follows redirects. The capability is not evidence
// of network provenance; service credentials remain at the broker.
func (g *ConnectionGrant) Request(ctx context.Context, method, path string, body []byte, headers http.Header) (*ConnectionResponse, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\#") {
		return nil, errors.New("expected relative service path")
	}
	if len(body) > 1024*1024 {
		return nil, errors.New("Connection body exceeds 1 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, method, g.info.ProxyURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid Connection request")
	}
	for name, values := range headers {
		lower := strings.ToLower(name)
		if lower != "accept" && lower != "content-type" && lower != "user-agent" {
			return nil, errors.New("unapproved Connection header")
		}
		req.Header[name] = append([]string(nil), values...)
	}
	req.Header.Set("Authorization", "Bearer "+g.credential)
	response, err := g.client.Do(req)
	if err != nil {
		return nil, &Error{Code: "connection_transport_failed", Message: "Connection transport failed"}
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, &Error{Status: response.StatusCode, Code: "connection_redirect_blocked", Message: "Connection redirect blocked"}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return nil, &Error{Code: "connection_transport_failed", Message: "Connection transport failed"}
	}
	if len(data) > 1024*1024 {
		return nil, &Error{Code: "connection_response_limit", Message: "Connection response exceeds limit"}
	}
	return &ConnectionResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: data}, nil
}
func connectionPath(namespace, name string) string {
	path := "/v1/connections/" + url.PathEscape(namespace)
	if name != "" {
		path += "/" + url.PathEscape(name)
	}
	return path
}
func (c *Client) CreateConnection(ctx context.Context, namespace, name string, policy ConnectionPolicy) (*ConnectionInfo, error) {
	var result ConnectionInfo
	err := c.transport.requestInto(ctx, http.MethodPost, connectionPath(namespace, name), policy, "", &result)
	return &result, err
}
func (c *Client) ReplaceConnection(ctx context.Context, namespace, name string, policy ConnectionPolicy, revision int) (*ConnectionInfo, error) {
	if revision < 1 {
		return nil, errors.New("positive expected revision required")
	}
	var result ConnectionInfo
	err := c.transport.requestInto(ctx, http.MethodPut, connectionPath(namespace, name)+fmt.Sprintf("?revision=%d", revision), policy, "", &result)
	return &result, err
}
func (c *Client) GetConnection(ctx context.Context, namespace, name string) (*ConnectionInfo, error) {
	var result ConnectionInfo
	err := c.transport.requestInto(ctx, http.MethodGet, connectionPath(namespace, name), nil, "", &result)
	return &result, err
}
func (c *Client) ListConnections(ctx context.Context, namespace string) ([]ConnectionInfo, error) {
	rows := []ConnectionInfo{}
	cursor := ""
	for {
		var page struct {
			Connections []ConnectionInfo `json:"connections"`
			NextCursor  string           `json:"next_cursor"`
		}
		path := connectionPath(namespace, "") + "?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		if err := c.transport.requestInto(ctx, http.MethodGet, path, nil, "", &page); err != nil {
			return nil, err
		}
		rows = append(rows, page.Connections...)
		cursor = page.NextCursor
		if cursor == "" {
			return rows, nil
		}
	}
}
func (c *Client) RevokeConnection(ctx context.Context, namespace, name string, revision int) error {
	if revision < 1 {
		return errors.New("positive expected revision required")
	}
	return c.transport.request(ctx, http.MethodDelete, connectionPath(namespace, name)+fmt.Sprintf("?revision=%d", revision), nil, "")
}

// IssueConnectionGrant uses the policy default TTL when expiresIn is zero.
// A nonzero TTL must be positive; issuance is not replayed on ambiguous failure.
func (c *Client) IssueConnectionGrant(ctx context.Context, namespace, name, sandboxID string, operations []string, expiresIn int) (*ConnectionGrant, error) {
	if expiresIn < 0 {
		return nil, errors.New("Connection grant lifetime cannot be negative")
	}
	var payload struct {
		ConnectionGrantInfo
		Credential string `json:"credential"`
	}
	body := map[string]any{"sandbox_id": sandboxID, "operations": operations}
	if expiresIn > 0 {
		body["expires_in"] = expiresIn
	}
	if err := c.transport.requestInto(ctx, http.MethodPost, connectionPath(namespace, name)+"/grants", body, "", &payload); err != nil {
		return nil, err
	}
	return newConnectionGrant(payload.ConnectionGrantInfo, payload.Credential, c.transport.endpoint, c.transport.client)
}
func (s *Sandbox) CreateConnectionGrant(ctx context.Context, namespace, name string, operations []string, expiresIn int) (*ConnectionGrant, error) {
	return s.client.IssueConnectionGrant(ctx, namespace, name, s.ID, operations, expiresIn)
}
func (c *Client) ListConnectionGrants(ctx context.Context, namespace, name string) ([]ConnectionGrantInfo, error) {
	var result struct {
		Grants []ConnectionGrantInfo `json:"grants"`
	}
	err := c.transport.requestInto(ctx, http.MethodGet, connectionPath(namespace, name)+"/grants", nil, "", &result)
	return result.Grants, err
}
func (c *Client) RevokeConnectionGrant(ctx context.Context, namespace, name, id string) error {
	return c.transport.request(ctx, http.MethodDelete, connectionPath(namespace, name)+"/grants", map[string]string{"id": id}, "")
}
func (c *Client) ConnectionEvents(ctx context.Context, namespace, name string) ([]map[string]any, error) {
	rows := []map[string]any{}
	cursor := ""
	for {
		var page struct {
			Events     []map[string]any `json:"events"`
			NextCursor string           `json:"next_cursor"`
		}
		path := connectionPath(namespace, name) + "/events?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		if err := c.transport.requestInto(ctx, http.MethodGet, path, nil, "", &page); err != nil {
			return nil, err
		}
		rows = append(rows, page.Events...)
		cursor = page.NextCursor
		if cursor == "" {
			return rows, nil
		}
	}
}
