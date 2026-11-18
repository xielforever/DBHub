package auth

import (
	"context"
	"time"
)

type ExternalIdentity struct {
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	Version   int64     `json:"version"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}
type OIDCStore interface {
	FindExternalIdentity(context.Context, string, string) (*User, error)
	ExternalIdentities(context.Context, string) ([]ExternalIdentity, error)
	BindExternalIdentity(context.Context, string, string, string, int64, bool) error
}
