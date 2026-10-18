// Package auditctx carries a closed, credential-free audit snapshot per request.
package auditctx

import (
	"context"
	"sync"
)

type Data struct {
	ActorID             int64  `json:"actor_id,omitempty"`
	ActorUsername       string `json:"actor_username,omitempty"`
	ActorRole           string `json:"actor_role,omitempty"`
	ConnectionID        int64  `json:"connection_id,omitempty"`
	SourceFamilyID      string `json:"source_family_id,omitempty"`
	DefinitionID        int64  `json:"definition_revision_id,omitempty"`
	Revision            int    `json:"revision,omitempty"`
	DatabaseAccount     string `json:"database_account,omitempty"`
	Driver              string `json:"driver,omitempty"`
	TargetOpened        bool   `json:"target_opened,omitempty"`
	ExecutionRequestID  string `json:"execution_request_id,omitempty"`
	TransactionID       string `json:"transaction_id,omitempty"`
	JobID               string `json:"job_id,omitempty"`
	ExportID            int64  `json:"export_id,omitempty"`
	ReportID            int64  `json:"report_id,omitempty"`
	ReportVersion       int64  `json:"report_version,omitempty"`
	ReportMode          string `json:"report_mode,omitempty"`
	ReportReviewID      int64  `json:"report_review_id,omitempty"`
	ReportPublicationNo int64  `json:"report_publication_no,omitempty"`
	ReportAccountID     int64  `json:"report_account_id,omitempty"`
	ReportManifestHash  string `json:"report_manifest_hash,omitempty"`
	ReportDecision      string `json:"report_decision,omitempty"`
	ReportReleaseKind   string `json:"report_release_kind,omitempty"`
	ReportAuthor        string `json:"report_author,omitempty"`
	ReportApprover      string `json:"report_approver,omitempty"`
	ReportPublisher     string `json:"report_publisher,omitempty"`
	ReportOwnerID       int64  `json:"report_owner_id,omitempty"`
	HTTPStatus          int    `json:"http_status,omitempty"`
	ResponseFailed      bool   `json:"response_failed,omitempty"`
	AuthFailure         string `json:"auth_failure,omitempty"`
	Route               string `json:"route,omitempty"`
	Outcome             string `json:"outcome,omitempty"`
}
type key struct{}
type checkpointKey struct{}

func WithCheckpoint(ctx context.Context, fn func() error) context.Context {
	return context.WithValue(ctx, checkpointKey{}, fn)
}
func Checkpoint(ctx context.Context) error {
	if fn, ok := ctx.Value(checkpointKey{}).(func() error); ok {
		return fn()
	}
	return nil
}

type Snapshot struct {
	mu   sync.Mutex
	data Data
}

func Begin(ctx context.Context) (context.Context, *Snapshot) {
	s := &Snapshot{}
	return context.WithValue(ctx, key{}, s), s
}
func Update(ctx context.Context, fn func(*Data)) {
	if s, ok := ctx.Value(key{}).(*Snapshot); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		fn(&s.data)
	}
}
func Read(ctx context.Context) Data {
	if s, ok := ctx.Value(key{}).(*Snapshot); ok {
		return s.Read()
	}
	return Data{}
}
func (s *Snapshot) Read() Data { s.mu.Lock(); defer s.mu.Unlock(); return s.data }
