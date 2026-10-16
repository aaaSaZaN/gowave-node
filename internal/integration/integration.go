// Package integration hosts optional node integrations. The v3.4.2 reference
// contains no concrete providers; an empty registry deliberately does no work.
package integration

import (
	"context"
	"errors"
	"fmt"
)

type Provider interface {
	Name() string
	Sync(context.Context, map[string]interface{}, map[string]interface{}) error
	Stop(context.Context) error
}
type Manager struct{ providers []Provider }

func New(providers ...Provider) *Manager {
	return &Manager{providers: append([]Provider(nil), providers...)}
}
func (m *Manager) Sync(ctx context.Context, settings, metadata map[string]interface{}) error {
	if len(m.providers) > 0 && metadata == nil {
		return errors.New("Node metadata is missing.")
	}
	if settings == nil {
		settings = map[string]interface{}{}
	}
	var result error
	for _, p := range m.providers {
		if err := p.Sync(ctx, settings, metadata); err != nil {
			result = errors.Join(result, fmt.Errorf("[%s] %w", p.Name(), err))
		}
	}
	return result
}
func (m *Manager) Stop(ctx context.Context) error {
	var result error
	for _, p := range m.providers {
		result = errors.Join(result, p.Stop(ctx))
	}
	return result
}
