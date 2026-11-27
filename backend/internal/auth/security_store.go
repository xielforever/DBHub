package auth

import (
	"context"
	"errors"
	"time"
)

var ErrMFAInvalid = errors.New("验证信息无效、已使用或已过期")

type MFAState struct {
	Enabled          bool
	EncryptedSecret  string
	LastCounter      int64
	RecoveryCount    int
	PendingID        string
	EncryptedPending string
	PendingExpires   *time.Time
	Version          int64
}
type MFAChallenge struct {
	Username        string
	UserID          int64
	PasswordVersion string
}

// Identities and password snapshots come only from authenticated users or an
// opaque, password-verified challenge, never from a browser-supplied user ID.
type SecurityStore interface {
	MFAState(context.Context, int64) (*MFAState, error)
	StartMFASetup(context.Context, *User, string, string, time.Time) error
	EnableMFA(context.Context, *User, string, int64, int64, []string) error
	CreateMFAChallenge(context.Context, *User, string, time.Time) error
	AttemptMFAChallenge(context.Context, string) (*MFAChallenge, error)
	ConsumeMFAChallenge(context.Context, *User, string, int64, int64, string) error
	DisableMFA(context.Context, *User, int64, int64, string) error
}
