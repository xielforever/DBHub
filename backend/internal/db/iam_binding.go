package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

// Explicit normal-space binding is one current statement. A missing result
// falls through to the existing recovery/ambiguous-space path, never an old
// permission cache. SQL errors propagate instead of falling back.
func (r *UserRepository) bindWorkspaceCurrent(ctx context.Context, id int64) (*Workspace, error) {
	w := &Workspace{}
	s := &iam.Snapshot{Subject: ActorID(ctx), WorkspaceID: id}
	var expiry *time.Time
	var raw []byte
	e := r.pool.maintenance.QueryRow(ctx, `SELECT * FROM dbhub_iam_bind_workspace($1,$2)`, ActorID(ctx), id).Scan(
		&w.ID, &w.Name, &w.Role, &w.AllowUnknown, &w.Version, &w.Capabilities, &w.ExpiresAt,
		&w.AuthorizationEpoch, &w.ConfigVersion, &w.PermissionMode, &s.RevocationSequence, &s.DeniedActions,
		&s.Target, &expiry, &s.RollbackGuard, &raw, &s.Actions)
	if e == pgx.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	s.MembershipVersion = w.Version
	s.ConfigVersion = w.ConfigVersion
	s.AuthorizationEpoch = w.AuthorizationEpoch
	s.Mode = w.PermissionMode
	s.LegacyDataRole = w.Role
	s.LegacyCapabilities = append([]string{}, w.Capabilities...)
	s.ExpiresAt = w.ExpiresAt
	if (s.Target || s.RollbackGuard) && expiry != nil && (s.ExpiresAt == nil || expiry.Before(*s.ExpiresAt)) {
		s.ExpiresAt = expiry
	}
	if !s.Target {
		s.Mode = iam.LegacyGuarded
		if w.PermissionMode == iam.Shadow {
			s.Mode = iam.Shadow
		}
	} else {
		if e = json.Unmarshal(raw, &s.Sources); e != nil {
			return nil, e
		}
		allowed := []string{}
		for _, action := range s.Actions {
			if iam.Allows(s, action, time.Now()) {
				allowed = append(allowed, action)
			}
		}
		s.Actions = allowed
	}
	w.IAM = s
	return w, nil
}
func boundWorkspaceContext(ctx context.Context, w *Workspace) (context.Context, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c == nil {
		return ctx, ErrWorkspaceRequired
	}
	copy := *c
	copy.Role = w.Role
	copy.Capabilities = append([]string{}, w.Capabilities...)
	copy.IAM = w.IAM
	return context.WithValue(WithWorkspace(auth.WithClaims(ctx, &copy), w.ID), memberEpochKey{}, w.Version), nil
}
