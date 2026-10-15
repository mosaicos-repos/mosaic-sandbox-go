package mosaic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

type NetworkProfileReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Revision  int    `json:"revision"`
}
type PrivateServiceDefinition struct {
	Name             string   `json:"name"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	DestinationCIDRs []string `json:"destination_cidrs"`
}
type NetworkProfileDefinition struct {
	Kind       string                     `json:"kind"`
	Region     string                     `json:"region"`
	Allocation string                     `json:"allocation,omitempty"`
	Services   []PrivateServiceDefinition `json:"services,omitempty"`
	Consumers  []string                   `json:"consumers"`
	ExpiresAt  *string                    `json:"expires_at,omitempty"`
}
type NetworkProfileInfo struct {
	NetworkProfileDefinition
	ID                       string   `json:"id"`
	Namespace                string   `json:"namespace"`
	Name                     string   `json:"name"`
	Revision                 int      `json:"revision"`
	State                    string   `json:"state"`
	Readiness                string   `json:"readiness"`
	Usable                   bool     `json:"usable"`
	EgressIPs                []string `json:"egress_ips"`
	CredentialMaterialStored bool     `json:"credential_material_stored"`
	RevokedAt                *string  `json:"revoked_at"`
	CreatedAt                string   `json:"created_at"`
	UpdatedAt                string   `json:"updated_at"`
}

// NetworkProfileResolution is a fresh metadata snapshot, not a reusable grant.
type NetworkProfileResolution struct {
	NetworkProfileReference
	Kind                     string   `json:"kind"`
	Region                   string   `json:"region"`
	Allocation               string   `json:"allocation"`
	Readiness                string   `json:"readiness"`
	Usable                   bool     `json:"usable"`
	EgressIPs                []string `json:"egress_ips"`
	CredentialMaterialStored bool     `json:"credential_material_stored"`
	QualificationExpiresAt   string   `json:"qualification_expires_at"`
}

func (c *Client) ResolveNetworkProfile(ctx context.Context, space, name string, revision int) (*NetworkProfileResolution, error) {
	if revision < 1 {
		return nil, errors.New("positive expected revision required")
	}
	var out NetworkProfileResolution
	err := c.transport.requestInto(ctx, http.MethodPost, networkProfilePath(space, name)+"/resolve",
		map[string]int{"revision": revision}, "", &out)
	return &out, err
}

func networkProfilePath(space, name string) string {
	p := "/v1/network-profiles/" + url.PathEscape(space)
	if name != "" {
		p += "/" + url.PathEscape(name)
	}
	return p
}
func (c *Client) CreateNetworkProfile(ctx context.Context, space, name string, definition NetworkProfileDefinition) (*NetworkProfileInfo, error) {
	var out NetworkProfileInfo
	e := c.transport.requestInto(ctx, http.MethodPost, networkProfilePath(space, name), definition, "", &out)
	return &out, e
}
func (c *Client) ReplaceNetworkProfile(ctx context.Context, space, name string, definition NetworkProfileDefinition, revision int) (*NetworkProfileInfo, error) {
	if revision < 1 {
		return nil, errors.New("positive expected revision required")
	}
	var out NetworkProfileInfo
	e := c.transport.requestInto(ctx, http.MethodPut, networkProfilePath(space, name)+fmt.Sprintf("?revision=%d", revision), definition, "", &out)
	return &out, e
}
func (c *Client) GetNetworkProfile(ctx context.Context, space, name string) (*NetworkProfileInfo, error) {
	var out NetworkProfileInfo
	e := c.transport.requestInto(ctx, http.MethodGet, networkProfilePath(space, name), nil, "", &out)
	return &out, e
}
func (c *Client) ListNetworkProfiles(ctx context.Context, space string) ([]NetworkProfileInfo, error) {
	out := []NetworkProfileInfo{}
	cursor := ""
	for {
		var page struct {
			Profiles []NetworkProfileInfo `json:"network_profiles"`
			Next     string               `json:"next_cursor"`
		}
		path := networkProfilePath(space, "") + "?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		if e := c.transport.requestInto(ctx, http.MethodGet, path, nil, "", &page); e != nil {
			return nil, e
		}
		out = append(out, page.Profiles...)
		if page.Next == "" {
			return out, nil
		}
		cursor = page.Next
	}
}
func (c *Client) RevokeNetworkProfile(ctx context.Context, space, name string, revision int) error {
	if revision < 1 {
		return errors.New("positive expected revision required")
	}
	return c.transport.requestInto(ctx, http.MethodDelete, networkProfilePath(space, name)+fmt.Sprintf("?revision=%d", revision), nil, "", nil)
}
func (c *Client) NetworkProfileEvents(ctx context.Context, space, name string) ([]map[string]any, error) {
	var out struct {
		Events []map[string]any `json:"events"`
	}
	e := c.transport.requestInto(ctx, http.MethodGet, networkProfilePath(space, name)+"/events", nil, "", &out)
	return out.Events, e
}
