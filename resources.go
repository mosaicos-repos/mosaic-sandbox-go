package mosaic

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

type runtimeTemplate struct {
	ID              string `json:"id"`
	Creatable       bool   `json:"creatable"`
	DefaultMemoryMB int    `json:"default_memory_mb"`
	DefaultVCPU     int    `json:"default_vcpu"`
}

type RuntimeLimits struct {
	Region          string            `json:"region"`
	Regions         []string          `json:"regions"`
	Placement       string            `json:"placement"`
	DefaultTemplate string            `json:"default_template"`
	Templates       []runtimeTemplate `json:"templates"`
	Environments    map[string]any    `json:"environments"`
	Execution       map[string]any    `json:"execution"`
	Hibernation     map[string]any    `json:"hibernation"`
	Fork            map[string]any    `json:"fork"`
	Volume          map[string]any    `json:"volume"`
	Previews        map[string]any    `json:"previews"`
	Processes       map[string]any    `json:"processes"`
	Files           map[string]any    `json:"files"`
	Guest           map[string]any    `json:"guest"`
}

func (c *Client) SetSecret(ctx context.Context, name, value string) (*SecretInfo, error) {
	var result SecretInfo
	if err := c.transport.requestInto(ctx, http.MethodPut, "/v1/secrets/"+url.PathEscape(name), map[string]any{"value": value}, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListSecrets(ctx context.Context) ([]SecretInfo, error) {
	var result struct {
		Secrets []SecretInfo `json:"secrets"`
	}
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/secrets", nil, "", &result); err != nil {
		return nil, err
	}
	return result.Secrets, nil
}

func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.transport.request(ctx, http.MethodDelete, "/v1/secrets/"+url.PathEscape(name), nil, "")
}

func (c *Client) CreateVolume(ctx context.Context, name, idempotencyKey string) (*VolumeInfo, error) {
	var result VolumeInfo
	if err := c.transport.requestInto(ctx, http.MethodPost, "/v1/volumes", map[string]any{"name": name}, idempotencyKey, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	var result struct {
		Volumes []VolumeInfo `json:"volumes"`
	}
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/volumes", nil, "", &result); err != nil {
		return nil, err
	}
	return result.Volumes, nil
}

func (c *Client) GetVolume(ctx context.Context, name string) (*VolumeInfo, error) {
	var result VolumeInfo
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/volumes/"+url.PathEscape(name), nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteVolume(ctx context.Context, name string) (int, error) {
	var result struct {
		DeletedObjects int `json:"deleted_objects"`
	}
	if err := c.transport.requestInto(ctx, http.MethodDelete, "/v1/volumes/"+url.PathEscape(name), nil, "", &result); err != nil {
		return 0, err
	}
	return result.DeletedObjects, nil
}

func (c *Client) ListSnapshots(ctx context.Context) ([]SnapshotInfo, error) {
	var result struct {
		Snapshots []SnapshotInfo `json:"snapshots"`
	}
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/snapshots", nil, "", &result); err != nil {
		return nil, err
	}
	return result.Snapshots, nil
}

func (c *Client) GetSnapshot(ctx context.Context, reference string) (*SnapshotInfo, error) {
	var result SnapshotInfo
	err := c.transport.requestInto(ctx, http.MethodGet, "/v1/snapshots/"+url.PathEscape(reference), nil, "", &result)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteSnapshot(ctx context.Context, reference string) error {
	return c.transport.request(ctx, http.MethodDelete, "/v1/snapshots/"+url.PathEscape(reference), nil, "")
}

func (c *Client) Templates(ctx context.Context) (*TemplateCatalogue, error) {
	var result TemplateCatalogue
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/templates", nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) EgressPresets(ctx context.Context) (*EgressPresetCatalogue, error) {
	var result EgressPresetCatalogue
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/egress-presets", nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Limits(ctx context.Context) (*Limits, error) {
	var result Limits
	if err := c.transport.requestInto(ctx, http.MethodGet, "/v1/limits", nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}
