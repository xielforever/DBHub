package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Group edits take an exclusive row lock. Lock grants in ID order so a group
// cannot change between an authorization decision and its committed release.
func lockAudienceGroups(ctx context.Context, q reportQueryer, groups []int64) error {
	if len(groups) == 0 {
		return nil
	}
	var locked []int64
	return q.QueryRow(ctx, `SELECT COALESCE(array_agg(id),'{}'::bigint[]) FROM (SELECT id FROM sys_user_groups WHERE id=ANY($1::bigint[]) ORDER BY id FOR SHARE) locked`, groups).Scan(&locked)
}

// Compare with the last released audience even while the report is offline.
// Ordinary draft submissions must have the same independence rule as explicit
// audience proposals. First publication retains independent submitter review.
func reportReviewerBenefit(ctx context.Context, q reportQueryer, review int64, users, groups []int64, reviewer int64) error {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT rv.manifest FROM sys_report_reviews rv
 JOIN sys_report_releases re ON re.review_id=rv.id
 JOIN sys_report_versions v ON v.id=rv.version_id
 WHERE v.report_id=(SELECT v2.report_id FROM sys_report_reviews rv2 JOIN sys_report_versions v2 ON v2.id=rv2.version_id WHERE rv2.id=$1)
 ORDER BY re.number DESC LIMIT 1`, review).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var before ReportManifest
	if err = json.Unmarshal(raw, &before); err != nil {
		return err
	}
	allGroups := append(append([]int64{}, before.GroupIDs...), groups...)
	if err = lockAudienceGroups(ctx, q, allGroups); err != nil {
		return err
	}
	var benefits bool
	err = q.QueryRow(ctx, `SELECT dbhub_member_role($1) IS NOT NULL
 AND NOT ($1=ANY($2::bigint[]) OR EXISTS(SELECT 1 FROM sys_user_group_members WHERE user_id=$1 AND group_id=ANY($3::bigint[])))
 AND ($1=ANY($4::bigint[]) OR EXISTS(SELECT 1 FROM sys_user_group_members WHERE user_id=$1 AND group_id=ANY($5::bigint[])))`, reviewer, nonNilIDs(before.UserIDs), nonNilIDs(before.GroupIDs), nonNilIDs(users), nonNilIDs(groups)).Scan(&benefits)
	if err != nil {
		return err
	}
	if benefits {
		return fmt.Errorf("%w: 新增权限的受益人不得审批，请交另一位管理员", ErrManagedInput)
	}
	return nil
}
