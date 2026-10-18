package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrReportConflict = errors.New("报表已被修改，或创建编号已用于其他内容")

type Report struct {
	ID                   int64           `json:"id"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	ConnectionID         int64           `json:"connection_id"`
	DefinitionRevisionID int64           `json:"definition_revision_id"`
	SourceName           string          `json:"source_name"`
	SourceFamilyID       string          `json:"source_family_id"`
	SourceRevision       int             `json:"source_revision"`
	DatabaseName         string          `json:"database_name"`
	SQLText              string          `json:"sql_text"`
	ChartType            string          `json:"chart_type"`
	ChartConfig          json.RawMessage `json:"chart_config"`
	Visibility           string          `json:"visibility"`
	OwnerUserID          int64           `json:"owner_user_id"`
	StarredBy            []int64         `json:"starred_by"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	OwnerName            string          `json:"owner_name,omitempty"`
	Starred              bool            `json:"starred"`
	Version              int64           `json:"version"`
	CreationKey          string          `json:"-"`
}

type ReportFilter struct {
	Keyword      string
	OwnerID      int64
	StarredBy    int64
	Visibility   string
	ConnectionID int64
	Offset       int
	Limit        int
}

type ReportRepository struct{ pool *pgxpool.Pool }

func NewReportRepository(pool *pgxpool.Pool) *ReportRepository { return &ReportRepository{pool: pool} }

func (r *ReportRepository) Create(ctx context.Context, rep *Report) error {
	// Retrying an uncertain create uses the same key AND original payload. Never
	// turn a retry into an update of an existing report.
	payload, err := json.Marshal([]any{rep.Name, rep.Description, rep.ConnectionID, rep.DefinitionRevisionID, rep.DatabaseName, rep.SQLText, rep.ChartType, rep.ChartConfig, rep.Visibility})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	err = r.pool.QueryRow(ctx, `INSERT INTO sys_reports
 (name,description,connection_id,definition_revision_id,database_name,sql_text,chart_type,chart_config,visibility,owner_user_id,creation_key,creation_hash)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')::uuid,$12)
 ON CONFLICT(owner_user_id,creation_key) WHERE creation_key IS NOT NULL DO NOTHING
 RETURNING id,created_at,updated_at,version`, rep.Name, rep.Description, rep.ConnectionID, rep.DefinitionRevisionID, rep.DatabaseName, rep.SQLText, rep.ChartType, rep.ChartConfig, rep.Visibility, ActorID(ctx), rep.CreationKey, hash).Scan(&rep.ID, &rep.CreatedAt, &rep.UpdatedAt, &rep.Version)
	if errors.Is(err, pgx.ErrNoRows) && rep.CreationKey != "" {
		var original string
		err = r.pool.QueryRow(ctx, `SELECT id,created_at,updated_at,version,creation_hash FROM sys_reports WHERE owner_user_id=$1 AND creation_key=$2::uuid`, ActorID(ctx), rep.CreationKey).Scan(&rep.ID, &rep.CreatedAt, &rep.UpdatedAt, &rep.Version, &original)
		if err == nil && original != hash {
			return ErrReportConflict
		}
	}
	return err
}

func reportConnection(ctx context.Context) string {
	return "COALESCE((SELECT mine.id FROM sys_connections mine WHERE mine.user_id=" + fmt.Sprint(ActorID(ctx)) + " AND mine.definition_revision_id=r.definition_revision_id),0)"
}
func reportVisible(ctx context.Context) string {
	actor := fmt.Sprint(ActorID(ctx))
	return "((r.owner_user_id=" + actor + " OR r.visibility='shared') AND dbhub_source_visible(r.definition_revision_id," + actor + "))"
}
func reportColumns(ctx context.Context) string {
	return `r.id,r.name,r.description,` + reportConnection(ctx) + `,r.definition_revision_id,d.name,d.family_id::text,d.revision,r.database_name,r.sql_text,r.chart_type,r.chart_config,r.visibility,r.owner_user_id,COALESCE(r.starred_by,'{}'),r.created_at,r.updated_at,COALESCE(u.username,''),COALESCE(` + fmt.Sprint(ActorID(ctx)) + `=ANY(r.starred_by),FALSE),r.version`
}

const reportJoins = ` FROM sys_reports r JOIN sys_source_definitions d ON d.id=r.definition_revision_id LEFT JOIN sys_users u ON u.id=r.owner_user_id `

func scanReport(row pgx.Row) (*Report, error) {
	rep := &Report{}
	err := row.Scan(&rep.ID, &rep.Name, &rep.Description, &rep.ConnectionID, &rep.DefinitionRevisionID, &rep.SourceName, &rep.SourceFamilyID, &rep.SourceRevision, &rep.DatabaseName, &rep.SQLText, &rep.ChartType, &rep.ChartConfig, &rep.Visibility, &rep.OwnerUserID, &rep.StarredBy, &rep.CreatedAt, &rep.UpdatedAt, &rep.OwnerName, &rep.Starred, &rep.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return rep, err
}
func (r *ReportRepository) Get(ctx context.Context, id int64) (*Report, error) {
	return scanReport(r.pool.QueryRow(ctx, `SELECT `+reportColumns(ctx)+reportJoins+` WHERE r.id=$1 AND `+reportVisible(ctx), id))
}
func (r *ReportRepository) List(ctx context.Context, f ReportFilter) ([]Report, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	conds := []string{reportVisible(ctx)}
	args := []any{}
	add := func(tmpl string, values ...any) {
		for _, v := range values {
			args = append(args, v)
			tmpl = strings.Replace(tmpl, "?", fmt.Sprintf("$%d", len(args)), 1)
		}
		conds = append(conds, tmpl)
	}
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		add("(r.name ILIKE ? OR r.description ILIKE ? OR r.sql_text ILIKE ?)", like, like, like)
	}
	if f.OwnerID > 0 {
		add("r.owner_user_id=?", f.OwnerID)
	}
	if f.StarredBy > 0 {
		add("?=ANY(r.starred_by)", f.StarredBy)
	}
	if f.Visibility != "" {
		add("r.visibility=?", f.Visibility)
	}
	if f.ConnectionID > 0 {
		add(reportConnection(ctx)+"=?", f.ConnectionID)
	}
	where := strings.Join(conds, " AND ")
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sys_reports r WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + reportColumns(ctx) + reportJoins + ` WHERE ` + where + fmt.Sprintf(` ORDER BY r.updated_at DESC,r.id DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, f.Limit, f.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Report{}
	for rows.Next() {
		rep, err := scanReport(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *rep)
	}
	return out, total, rows.Err()
}
func (r *ReportRepository) Update(ctx context.Context, rep *Report) error {
	err := r.pool.QueryRow(ctx, `UPDATE sys_reports SET name=$2,description=$3,connection_id=NULLIF($4,0),definition_revision_id=$5,database_name=$6,sql_text=$7,chart_type=$8,chart_config=$9,visibility=$10,version=version+1
 WHERE id=$1 AND owner_user_id=$11 AND version=$12 RETURNING version,updated_at`, rep.ID, rep.Name, rep.Description, rep.ConnectionID, rep.DefinitionRevisionID, rep.DatabaseName, rep.SQLText, rep.ChartType, rep.ChartConfig, rep.Visibility, ActorID(ctx), rep.Version).Scan(&rep.Version, &rep.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReportConflict
	}
	return err
}
func (r *ReportRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_reports WHERE id=$1 AND owner_user_id=$2`, id, ActorID(ctx))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (r *ReportRepository) SetStar(ctx context.Context, id, userID int64, star bool) error {
	if userID != ActorID(ctx) || userID < 1 {
		return ErrNotFound
	}
	expression := `array_remove(COALESCE(r.starred_by,'{}'),$2)`
	if star {
		expression = `ARRAY(SELECT DISTINCT x FROM unnest(array_append(COALESCE(r.starred_by,'{}'),$2)) x)`
	}
	tag, err := r.pool.Exec(ctx, `UPDATE sys_reports r SET starred_by=`+expression+` WHERE id=$1 AND `+reportVisible(ctx), id, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
