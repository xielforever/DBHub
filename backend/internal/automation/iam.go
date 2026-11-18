package automation

import (
	"context"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
)

func (s *Service) iamTaskLease(ctx context.Context, actions ...string) (context.Context, func(), error) {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil {
		return ctx, nil, db.ErrAutomationDenied
	}
	if c.IAM != nil {
		selected := ""
		for _, action := range actions {
			if auth.HasAction(c, action) {
				selected = action
				break
			}
		}
		if selected == "" {
			return ctx, nil, db.ErrAutomationDenied
		}
		copy := *c
		copy.CurrentAction = selected
		ctx = auth.WithClaims(ctx, &copy)
	}
	release, e := s.Users.IAMLease(ctx)
	return ctx, release, e
}
