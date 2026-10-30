// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"

	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/hostlist"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Filters returns the configured filters.
func (s *Service) Filters() []filter.Filter { return s.opts.Filters }

// Rows builds the host list rows for the current snapshot. Visibility and
// badges come from the service options.
func (s *Service) Rows(o ListOptions) []Row {
	snap := s.Snapshot()
	o.Visibility = s.opts.Visibility
	o.Badges = s.opts.Filters
	return hostlist.Build(&snap, o)
}

// CountMatching returns how many visible hosts match rule (for filter tabs).
func (s *Service) CountMatching(rule Rule, o ListOptions) int {
	snap := s.Snapshot()
	o.Visibility = s.opts.Visibility
	o.Filter = rule
	return len(hostlist.Hosts(&snap, o))
}

// ParseRule parses a filter/search expression.
func ParseRule(src string) (Rule, error) { return filter.Parse(src) }

// ErrNoLauncher is returned for non-SSH hosts when no launcher is configured.
var ErrNoLauncher = errors.New("no external program configured")

// LaunchExternal opens a VNC/SMB/FTP/SFTP/RDP host in an external program.
func (s *Service) LaunchExternal(ctx context.Context, h model.Host) error {
	if s.opts.Launch == nil {
		return ErrNoLauncher
	}
	return s.opts.Launch(ctx, h, func(ctx context.Context) (secret.Secret, error) { return s.Secret(ctx, h.ID) })
}
