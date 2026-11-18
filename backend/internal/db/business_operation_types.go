package db

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	crypto "github.com/user/dbhub/pkg/crypto"
)

type BusinessOperationRepository struct {
	pool     *ScopedPool
	cipher   *crypto.Cipher
	commands *ManagedReportRepository
}

func (r *ConnectionRepository) BusinessOperations() *BusinessOperationRepository {
	return &BusinessOperationRepository{r.pool, r.cipher, r.ManagedReports()}
}

type OperationSpec struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	TargetLabel string `json:"target_label"`
	AmountLabel string `json:"amount_label"`
	Min         int    `json:"min"`
	Max         int    `json:"max"`
	Description string `json:"description"`
}

var operationSpecs = []OperationSpec{
	{"member_points_adjust", "会员积分调整", "会员编号", "积分增减", -500, 500, "每次增减不超过500，调整后积分须在0至100000；仅可操作分配给本人的会员。"},
	{"member_coupon_issue", "会员发券", "会员编号", "发券张数", 1, 10, "一次发放1至10张演示券，固定有效期30天；审批冻结有效期及数量。"},
	{"warehouse_stock_adjust", "仓库库存调整", "SKU编号", "库存增减", -500, 500, "调整后库存不得低于已预留数量，且不超过100000。"},
	{"transport_status_advance", "运输状态推进", "运单编号", "推进步数", 1, 1, "按待揽收、已揽收、运输中、已签收顺序推进一步，不得跳级或回退。"},
}

func OperationSpecification(kind string) (OperationSpec, error) {
	for _, s := range operationSpecs {
		if s.Kind == kind {
			return s, nil
		}
	}
	return OperationSpec{}, fmt.Errorf("%w: 未提供此操作模板", ErrManagedInput)
}

var operationTargetPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func ValidateOperationParameters(kind, key string, amount int) error {
	s, e := OperationSpecification(kind)
	if e != nil {
		return e
	}
	if !operationTargetPattern.MatchString(key) || amount < s.Min || amount > s.Max || amount == 0 {
		return fmt.Errorf("%w: 对象编号或操作数量超出业务范围", ErrManagedInput)
	}
	return nil
}

type OperationAccount struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	DefinitionID   int64  `json:"definition_revision_id"`
	Username       string `json:"username"`
	InstallationID string `json:"installation_id"`
	Enabled        bool   `json:"enabled"`
	Version        int64  `json:"version"`
}
type OperationTemplate struct {
	ID        int64         `json:"id"`
	AccountID int64         `json:"account_id,omitempty"`
	Kind      string        `json:"kind"`
	UserIDs   []int64       `json:"user_ids,omitempty"`
	GroupIDs  []int64       `json:"group_ids,omitempty"`
	Enabled   bool          `json:"enabled"`
	Version   int64         `json:"version"`
	Spec      OperationSpec `json:"spec"`
}
type OperationPreview struct {
	ID             string         `json:"id,omitempty"`
	InstallationID string         `json:"installation_id"`
	WorkspaceID    int64          `json:"workspace_id"`
	ActorID        int64          `json:"actor_id"`
	Kind           string         `json:"kind"`
	TargetKey      string         `json:"target_key"`
	Amount         int            `json:"amount"`
	PreviewAt      int64          `json:"preview_at"`
	TargetVersion  string         `json:"target_version"`
	BeforeHash     string         `json:"before_sha256"`
	AfterHash      string         `json:"after_sha256"`
	Before         map[string]any `json:"before"`
	After          map[string]any `json:"after"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
}
type OperationReceipt struct {
	OperationPreview
	OperationID  string    `json:"operation_id"`
	ReviewerID   int64     `json:"reviewer_id"`
	ExecutedAt   time.Time `json:"executed_at"`
	AffectedRows int       `json:"affected_rows"`
}
type OperationRequest struct {
	ID                    int64             `json:"id"`
	OperationID           string            `json:"operation_id"`
	UserID                int64             `json:"user_id"`
	UserName              string            `json:"user_name"`
	TemplateID            int64             `json:"template_id"`
	Kind                  string            `json:"kind"`
	MemberVersion         int64             `json:"-"`
	TemplateVersion       int64             `json:"-"`
	AccountVersion        int64             `json:"-"`
	Preview               OperationPreview  `json:"preview"`
	Reason                string            `json:"reason"`
	Status                string            `json:"status"`
	Version               int64             `json:"version"`
	ReviewerID            *int64            `json:"reviewer_id,omitempty"`
	ReviewerMemberVersion *int64            `json:"-"`
	ReviewComment         string            `json:"review_comment"`
	Receipt               *OperationReceipt `json:"receipt,omitempty"`
	ReceiptHash           string            `json:"receipt_sha256"`
	CreatedAt             time.Time         `json:"created_at"`
	ExpiresAt             time.Time         `json:"expires_at"`
	StartedAt             *time.Time        `json:"execution_started_at,omitempty"`
}
type OperationTarget struct {
	Connection *Connection
	Password   string
	Account    OperationAccount
	Template   OperationTemplate
}

func (r *BusinessOperationRepository) decode(raw string, out any) error {
	return (&ReportSubscriptionRepository{cipher: r.cipher}).decode(raw, out)
}
func (r *BusinessOperationRepository) encode(value any) (string, error) {
	return r.cipher.Encrypt(string(canonicalReportJSON(value)))
}
func sameOperationPreview(a, b OperationPreview) bool {
	return a.WorkspaceID == b.WorkspaceID && a.ActorID == b.ActorID && a.Kind == b.Kind && a.TargetKey == b.TargetKey && a.Amount == b.Amount && a.InstallationID == b.InstallationID && a.TargetVersion == b.TargetVersion && a.BeforeHash == b.BeforeHash && a.AfterHash == b.AfterHash && a.PreviewAt == b.PreviewAt
}

// Keep the versioned wire format independent of browser number coercion.
func operationJSON(value any) json.RawMessage { return canonicalReportJSON(value) }
