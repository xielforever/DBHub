package db

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

var ErrSourceInput = errors.New("参数无效")
var ErrSourceAccessDenied = errors.New("此数据源的发布授权已撤销，请联系管理员")
var ErrSourceVersionConflict = errors.New("配置已被其他人修改，请刷新后重试")

type SourceAccess struct {
	Mode     string  `json:"mode"`
	UserIDs  []int64 `json:"user_ids"`
	GroupIDs []int64 `json:"group_ids"`
	Version  int64   `json:"version"`
}
type SourceProfile struct {
	Category   string   `json:"category"`
	Tags       []string `json:"tags"`
	Maintainer string   `json:"maintainer"`
	Contact    string   `json:"contact"`
	Version    int64    `json:"version"`
}
type DirectoryFilter struct {
	Keyword, Category, View string
	Limit, Offset           int
}

const familyJSON = `jsonb_build_object('access_mode',f.access_mode,'access_version',f.access_version,'profile_version',f.profile_version,'category',f.category,'tags',f.tags,'maintainer',f.maintainer,'contact',f.contact)`

func (r *ConnectionRepository) SearchDefinitions(ctx context.Context, filter DirectoryFilter) ([]SourceDefinition, int64, error) {
	if ActorID(ctx) == 0 {
		return nil, 0, ErrNotFound
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	base := ` FROM (SELECT DISTINCT ON(family_id) * FROM sys_source_definitions d WHERE dbhub_source_visible(d.id,$1) ORDER BY family_id,revision DESC) d
 JOIN sys_source_families f USING(family_id)
 LEFT JOIN sys_source_preferences p ON p.family_id=d.family_id AND p.user_id=$1
 LEFT JOIN sys_connections c ON c.definition_family_id=d.family_id AND c.user_id=$1
 WHERE ($2='' OR d.name ILIKE '%'||$2||'%' OR d.description ILIKE '%'||$2||'%' OR d.config::text ILIKE '%'||$2||'%' OR array_to_string(f.tags,' ') ILIKE '%'||$2||'%')
 AND ($3='' OR f.category=$3) AND ($4<>'favorites' OR p.favorite) AND ($4<>'recent' OR p.last_used_at IS NOT NULL)`
	args := []any{ActorID(ctx), filter.Keyword, filter.Category, filter.View}
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*)`+base, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	args = append(args, filter.Limit, filter.Offset)
	rows, e := r.pool.Query(ctx, `SELECT to_jsonb(d)||`+familyJSON+`||jsonb_build_object('favorite',COALESCE(p.favorite,false),'last_used_at',p.last_used_at,'my_connection_id',c.id,'my_revision_id',c.definition_revision_id)`+base+` ORDER BY CASE WHEN $4='recent' THEN p.last_used_at END DESC NULLS LAST,d.created_at DESC,d.id DESC LIMIT $5 OFFSET $6`, args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []SourceDefinition{}
	for rows.Next() {
		d, e := scanDefinition(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *d)
	}
	return out, total, rows.Err()
}
func (r *ConnectionRepository) DefinitionCategories(ctx context.Context) ([]string, error) {
	rows, e := r.pool.Query(ctx, `SELECT DISTINCT f.category FROM sys_source_families f JOIN sys_source_definitions d USING(family_id) WHERE f.category<>'' AND dbhub_source_visible(d.id,$1) ORDER BY f.category`, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if e = rows.Scan(&s); e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Published families require a current administrator, including when addressed
// through an older private predecessor owned by a now-demoted publisher.
func lockManagedFamily(ctx context.Context, tx pgx.Tx, id int64) (*SourceDefinition, error) {
	d, e := scanDefinition(tx.QueryRow(ctx, `SELECT to_jsonb(d) FROM sys_source_definitions d WHERE id=$1`, id))
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('source-definition:'||$1,0))`, d.FamilyID); e != nil {
		return nil, e
	}
	var published, admin, active bool
	if e = tx.QueryRow(ctx, `SELECT u.is_active,r.code='admin',EXISTS(SELECT 1 FROM sys_source_definitions WHERE family_id=$2::uuid AND visibility='global') FROM sys_users u JOIN sys_roles r ON r.id=u.role_id WHERE u.id=$1`, ActorID(ctx), d.FamilyID).Scan(&active, &admin, &published); e != nil {
		return nil, ErrNotFound
	}
	if !active || (published && !admin) || (!published && d.OwnerID != ActorID(ctx)) {
		return nil, ErrNotFound
	}
	if _, e = tx.Exec(ctx, `SELECT 1 FROM sys_source_families WHERE family_id=$1::uuid FOR UPDATE`, d.FamilyID); e != nil {
		return nil, e
	}
	return d, nil
}
func (r *ConnectionRepository) DefinitionAccess(ctx context.Context, id int64) (*SourceAccess, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	d, e := lockManagedFamily(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	a := &SourceAccess{UserIDs: []int64{}, GroupIDs: []int64{}}
	e = tx.QueryRow(ctx, `SELECT access_mode,access_version,COALESCE((SELECT array_agg(user_id ORDER BY user_id) FROM sys_source_grants WHERE family_id=$1::uuid AND user_id IS NOT NULL),'{}'::bigint[]),COALESCE((SELECT array_agg(group_id ORDER BY group_id) FROM sys_source_grants WHERE family_id=$1::uuid AND group_id IS NOT NULL),'{}'::bigint[]) FROM sys_source_families WHERE family_id=$1::uuid`, d.FamilyID).Scan(&a.Mode, &a.Version, &a.UserIDs, &a.GroupIDs)
	return a, e
}
func validateSubjects(ids []int64) bool {
	if len(ids) > 500 {
		return false
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (r *ConnectionRepository) SetDefinitionAccess(ctx context.Context, id int64, a SourceAccess, expected int64) error {
	if a.Mode != "global" && a.Mode != "restricted" || !validateSubjects(a.UserIDs) || !validateSubjects(a.GroupIDs) || a.Mode == "global" && (len(a.UserIDs)+len(a.GroupIDs) > 0) {
		return fmt.Errorf("%w: 授权范围或成员参数无效", ErrSourceInput)
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	d, e := lockManagedFamily(ctx, tx, id)
	if e != nil {
		return e
	}
	if d.Visibility != "global" {
		return fmt.Errorf("%w: 请先发布数据源，再管理发布范围", ErrSourceInput)
	}
	result, e := tx.Exec(ctx, `UPDATE sys_source_families SET access_mode=$2,access_version=access_version+1 WHERE family_id=$1::uuid AND access_version=$3`, d.FamilyID, a.Mode, expected)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return ErrSourceVersionConflict
	}
	if e = lockAudienceGroups(ctx, tx, a.GroupIDs); e != nil {
		return e
	}
	var valid bool
	if e = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM sys_users WHERE id=ANY($1::bigint[]) AND is_active AND dbhub_member_role(id) IS NOT NULL)=cardinality($1::bigint[]) AND (SELECT count(*) FROM sys_user_groups WHERE id=ANY($2::bigint[]))=cardinality($2::bigint[])`, nonNilIDs(a.UserIDs), nonNilIDs(a.GroupIDs)).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return fmt.Errorf("%w: 用户已停用、不存在或用户组不存在", ErrSourceInput)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM sys_source_grants WHERE family_id=$1::uuid`, d.FamilyID); e != nil {
		return e
	}
	for _, uid := range a.UserIDs {
		if _, e = tx.Exec(ctx, `INSERT INTO sys_source_grants(family_id,user_id)VALUES($1::uuid,$2)`, d.FamilyID, uid); e != nil {
			return e
		}
	}
	for _, gid := range a.GroupIDs {
		if _, e = tx.Exec(ctx, `INSERT INTO sys_source_grants(family_id,group_id)VALUES($1::uuid,$2)`, d.FamilyID, gid); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
func (r *ConnectionRepository) SetDefinitionProfile(ctx context.Context, id int64, p SourceProfile, expected int64) error {
	p.Category = strings.TrimSpace(p.Category)
	p.Maintainer = strings.TrimSpace(p.Maintainer)
	p.Contact = strings.TrimSpace(p.Contact)
	if len([]rune(p.Category)) > 64 || len([]rune(p.Maintainer)) > 100 || len([]rune(p.Contact)) > 200 || len(p.Tags) > 10 {
		return fmt.Errorf("%w: 目录资料超出长度上限", ErrSourceInput)
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, t := range p.Tags {
		t = strings.TrimSpace(t)
		if len([]rune(t)) > 32 {
			return fmt.Errorf("%w: 标签最多32字符", ErrSourceInput)
		}
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	d, e := lockManagedFamily(ctx, tx, id)
	if e != nil {
		return e
	}
	result, e := tx.Exec(ctx, `UPDATE sys_source_families SET category=$2,tags=$3,maintainer=$4,contact=$5,profile_version=profile_version+1 WHERE family_id=$1::uuid AND profile_version=$6`, d.FamilyID, p.Category, tags, p.Maintainer, p.Contact, expected)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return ErrSourceVersionConflict
	}
	return tx.Commit(ctx)
}
func (r *ConnectionRepository) SetDefinitionFavorite(ctx context.Context, id int64, favorite bool) error {
	result, e := r.pool.Exec(ctx, `INSERT INTO sys_source_preferences(user_id,family_id,favorite) SELECT $2,d.family_id,$3 FROM sys_source_definitions d WHERE d.id=$1 AND dbhub_source_visible(d.id,$2) ON CONFLICT(user_id,family_id) DO UPDATE SET favorite=EXCLUDED.favorite`, id, ActorID(ctx), favorite)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// Record usage only after a successful physical open, at most once per 30 s.
func (r *ConnectionRepository) MarkUsed(ctx context.Context, c *Connection) {
	if c.ID <= 0 || c.UserID != ActorID(ctx) || c.DefinitionFamilyID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _ = r.pool.Exec(ctx, `INSERT INTO sys_source_preferences(user_id,family_id,last_used_at) SELECT $1,$2::uuid,now() WHERE EXISTS(SELECT 1 FROM sys_connections c WHERE c.id=$3 AND c.user_id=$1 AND dbhub_source_visible(c.definition_revision_id,$1)) ON CONFLICT(user_id,family_id) DO UPDATE SET last_used_at=now() WHERE sys_source_preferences.last_used_at IS NULL OR sys_source_preferences.last_used_at<now()-interval '30 seconds'`, ActorID(ctx), c.DefinitionFamilyID, c.ID)
}
