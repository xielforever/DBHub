package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type DashboardDeliveryInput struct {
	Kind        string           `json:"kind"`
	Publication int64            `json:"publication_no"`
	TileKey     string           `json:"tile_key,omitempty"`
	Params      map[string]any   `json:"params"`
	Format      string           `json:"format,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	Confirmed   bool             `json:"confirmed,omitempty"`
	Interval    int              `json:"interval_minutes,omitempty"`
	Previous    int64            `json:"previous_id,omitempty"`
	UserIDs     []int64          `json:"user_ids,omitempty"`
	GroupIDs    []int64          `json:"group_ids,omitempty"`
	AlertTile   string           `json:"alert_tile,omitempty"`
	Alert       *MetricAlertRule `json:"alert_rule,omitempty"`
}

type dashboardDeliveryPlan struct {
	ID, Dashboard, Publication, Owner, Version int64
	Kind, Status                               string
	Datasets                                   []string
	Recipients, Direct, Groups                 []int64
	Request, Approval                          string
}

type dashboardDeliveryCard struct {
	Approval dashboardReviewCard
	Required bool
	Members  []reportReviewAudience
	Owner    *reportReviewAudience
}

type dashboardDeliveryBasis struct {
	Input      DashboardDeliveryInput
	Definition string
	Cards      []dashboardDeliveryCard
}

func (r *ManagedDashboardRepository) dashboardDeliveryContext(ctx context.Context, tx pgx.Tx, review int64) error {
	if r.reports.phase2ReportIsolation == "" {
		return ErrManagedDenied
	}
	_, err := tx.Exec(ctx, `SELECT set_config('dbhub.dashboard_delivery_isolation',$1,true),
		set_config('dbhub.dashboard_delivery_review',$2,true)`, r.reports.phase2ReportIsolation, strconv.FormatInt(review, 10))
	return err
}

func lockDashboardDelivery(ctx context.Context, tx pgx.Tx, plan int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		fmt.Sprintf("dashboard-delivery:%d:%d", WorkspaceID(ctx), plan))
	return err
}

func (r *ManagedDashboardRepository) readDashboardDeliveryPlan(ctx context.Context, tx pgx.Tx, id int64) (dashboardDeliveryPlan, error) {
	var p dashboardDeliveryPlan
	err := tx.QueryRow(ctx, `SELECT id,dashboard_id,publication_no,owner_id,version,kind,status,dataset_keys,
		recipient_ids,direct_ids,group_ids,request_payload,approval_payload FROM sys_dashboard_delivery_plans
		WHERE workspace_id=dbhub_workspace() AND id=$1`, id).Scan(&p.ID, &p.Dashboard, &p.Publication, &p.Owner, &p.Version,
		&p.Kind, &p.Status, &p.Datasets, &p.Recipients, &p.Direct, &p.Groups, &p.Request, &p.Approval)
	return p, managedError(err)
}

func (r *ManagedDashboardRepository) dashboardDeliveryBase(ctx context.Context, tx pgx.Tx, id, publication int64, tile string) (DashboardDefinition, dashboardDeliveryBasis, error) {
	var definition DashboardDefinition
	var out dashboardDeliveryBasis
	enrolled, err := r.governedDashboard(ctx, tx, id)
	if err != nil {
		return definition, out, err
	}
	if !enrolled {
		return definition, out, ErrManagedDenied
	}
	var review, assignment int64
	err = tx.QueryRow(ctx, `SELECT v.definition,rv.id,rv.assignment_version FROM sys_managed_dashboards d
		JOIN sys_iam_isolated_dashboard_delivery e ON e.workspace_id=d.workspace_id AND e.dashboard_id=d.id
		JOIN sys_dashboard_releases re ON re.id=d.active_release_id
		JOIN sys_dashboard_reviews rv ON rv.id=re.review_id AND rv.status='approved'
		JOIN sys_dashboard_versions v ON v.id=rv.version_id
		WHERE d.workspace_id=dbhub_workspace() AND d.id=$1 AND d.publication_no=$2 AND d.lifecycle='active'`, id, publication).
		Scan(&definition, &review, &assignment)
	if err != nil {
		return definition, out, managedError(err)
	}
	check, err := readDashboardReviewPolicy(ctx, tx, review, assignment)
	if err != nil {
		return definition, out, err
	}
	out.Definition = reportDigest(definition)
	if check.boundary.Definition != out.Definition || check.requiresData && check.dataReviewer == nil {
		return definition, out, ErrManagedConflict
	}
	for _, item := range definition.Tiles {
		if tile != "" && item.Key != tile {
			continue
		}
		i := slices.IndexFunc(check.boundary.Cards, func(card dashboardReviewCard) bool { return card.Key == item.Key })
		if i < 0 || check.boundary.Cards[i].ReportID != item.ReportID || check.boundary.Cards[i].Contract != item.ContractHash {
			return definition, out, ErrManagedConflict
		}
		out.Cards = append(out.Cards, dashboardDeliveryCard{Approval: check.boundary.Cards[i], Required: item.Required || tile != ""})
	}
	if len(out.Cards) == 0 {
		return definition, out, ErrManagedInput
	}
	return definition, out, nil
}

// dashboardDeliveryMembers narrows the publication approval to the explicitly
// selected roster and today's effective ranges. Optional cards omit ineligible members.
func (r *ManagedDashboardRepository) dashboardDeliveryMembers(ctx context.Context, tx pgx.Tx, basis *dashboardDeliveryBasis, roster []int64, reviewer int64) error {
	for i := range basis.Cards {
		card := &basis.Cards[i]
		approved := card.Approval.Scope
		if reviewer == 0 {
			if !dashboardGrant(ctx, tx, approved.Users, approved.Groups, ActorID(ctx)) && !card.Required {
				continue
			}
			x := &ManagedExecution{ReportID: card.Approval.ReportID}
			if err := r.reports.useBusinessReportBinding(ctx, tx, x); err != nil {
				return err
			}
			if x.businessBinding != approved.Dataset {
				return ErrManagedConflict
			}
		}
		row, err := readBusinessRowColumnPolicy(ctx, tx, approved.Dataset)
		if err != nil && !card.Required && reviewer == 0 && errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if row.DatasetVersion != approved.DatasetVersion || row.Version != approved.RowPolicy {
			return ErrManagedConflict
		}
		for _, uid := range roster {
			ix := slices.IndexFunc(approved.Audience, func(m reportReviewAudience) bool { return m.UserID == uid })
			if ix < 0 || !dashboardGrant(ctx, tx, approved.Users, approved.Groups, uid) {
				if card.Required {
					return ErrManagedDenied
				}
				continue
			}
			member, err := reportReviewMember(ctx, tx, row, uid)
			if errors.Is(err, ErrManagedDenied) && !card.Required {
				continue
			}
			if err != nil {
				return err
			}
			prior := approved.Audience[ix]
			if member.IdentityVersion != prior.IdentityVersion || !subset(member.Scopes, prior.Scopes) {
				return ErrManagedConflict
			}
			card.Members = append(card.Members, member)
		}
		if reviewer != 0 {
			coverage, err := reportReviewMember(ctx, tx, row, reviewer)
			if err != nil {
				return err
			}
			for _, member := range card.Members {
				if !subset(member.Scopes, coverage.Scopes) {
					return ErrManagedDenied
				}
			}
		}
	}
	return nil
}

func dashboardDeliveryPermission(kind string) string {
	switch kind {
	case "export":
		return "dashboard.export"
	case "team":
		return "dashboard.subscribeTeam"
	default:
		return "dashboard.subscribe"
	}
}

func (r *ManagedDashboardRepository) SubmitDashboardDelivery(ctx context.Context, key string, id int64, in DashboardDeliveryInput) (ReportCommandResult, error) {
	if in.Publication < 1 || len(in.TileKey) > 64 || in.Previous < 0 ||
		(in.Kind != "export" && in.Kind != "personal" && in.Kind != "team") ||
		(in.Kind == "export" && (in.Format != "csv" && in.Format != "json" && in.Format != "xlsx" || strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 500 || in.Interval != 0 || in.Previous != 0)) ||
		(in.Kind != "export" && (in.Interval < 5 || in.Interval > 10080 || in.Format != "")) ||
		(in.Kind == "personal" && !in.Confirmed) || (in.Kind != "personal" && in.Alert != nil) ||
		(in.Alert == nil) != (in.AlertTile == "") || (in.Kind != "team" && len(in.UserIDs)+len(in.GroupIDs) > 0) {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.reports.command(ctx, key, "dashboard-delivery-submit", []any{id, in}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		var out ReportCommandResult
		if err := r.dashboardDeliveryContext(ctx, tx, 0); err != nil {
			return out, err
		}
		definition, basis, err := r.dashboardDeliveryBase(ctx, tx, id, in.Publication, in.TileKey)
		if err != nil {
			return out, err
		}
		if !dashboardGrant(ctx, tx, definition.UserIDs, definition.GroupIDs, ActorID(ctx)) {
			return out, ErrManagedDenied
		}
		var denied bool
		if err = tx.QueryRow(ctx, `SELECT dbhub_iam_action_denied(dbhub_actor(),$1)`, dashboardDeliveryPermission(in.Kind)).Scan(&denied); err != nil {
			return out, err
		}
		if denied {
			return out, ErrManagedDenied
		}
		in.Params, err = ResolveReportParameters(definition.Parameters, in.Params, time.Now())
		if err != nil {
			return out, ErrManagedInput
		}
		basis.Input = in
		datasets := []string{}
		for _, card := range basis.Cards {
			datasets = append(datasets, card.Approval.Scope.Dataset)
		}
		slices.Sort(datasets)
		datasets = slices.Compact(datasets)
		direct, roster := []int64{ActorID(ctx)}, []int64{ActorID(ctx)}
		groups := []int64{}
		if in.Kind == "team" {
			var allowed bool
			if err = tx.QueryRow(ctx, `SELECT dbhub_dashboard_delivery_granted($1,dbhub_actor(),'dashboard.subscribeTeam')`, datasets).Scan(&allowed); err != nil {
				return out, err
			}
			if !allowed {
				return out, ErrManagedDenied
			}
			if err = r.dashboardDeliveryMembers(ctx, tx, &basis, []int64{ActorID(ctx)}, 0); err != nil {
				return out, err
			}
			for i := range basis.Cards {
				if basis.Cards[i].Required {
					basis.Cards[i].Owner = &basis.Cards[i].Members[0]
				}
				basis.Cards[i].Members = nil
			}
			if err = audience(ctx, tx, in.UserIDs, in.GroupIDs); err != nil {
				return out, err
			}
			direct, groups = normalizedIDs(in.UserIDs), normalizedIDs(in.GroupIDs)
			roster, err = reportAudienceMembers(ctx, tx, direct, groups)
			if err != nil {
				return out, err
			}
			if len(roster) == 0 || len(roster) > 100 {
				return out, ErrManagedInput
			}
			for _, uid := range roster {
				if !dashboardGrant(ctx, tx, definition.UserIDs, definition.GroupIDs, uid) {
					return out, ErrManagedDenied
				}
			}
		} else if err = r.dashboardDeliveryMembers(ctx, tx, &basis, roster, 0); err != nil {
			return out, err
		}
		if in.Alert != nil {
			ix := slices.IndexFunc(basis.Cards, func(c dashboardDeliveryCard) bool { return c.Approval.Key == in.AlertTile && len(c.Members) > 0 })
			if ix < 0 {
				return out, ErrManagedInput
			}
			p, err := loadDashboardReport(ctx, tx, basis.Cards[ix].Approval.ReportID, ActorID(ctx))
			if err != nil {
				return out, err
			}
			if p.Contract.Declaration.ChartType != "metric" || !slices.Contains(p.Contract.Declaration.Metrics, in.Alert.Column) {
				return out, ErrManagedInput
			}
			if err = ValidateMetricAlertRule(in.Alert, &p.Contract.Declaration); err != nil {
				return out, ErrManagedInput
			}
		}
		var previous *int64
		if in.Previous > 0 {
			if err = lockDashboardDelivery(ctx, tx, in.Previous); err != nil {
				return out, err
			}
			old, err := r.readDashboardDeliveryPlan(ctx, tx, in.Previous)
			if err != nil {
				return out, err
			}
			if old.Owner != ActorID(ctx) || old.Dashboard != id || old.Kind != in.Kind || old.Status != "approved" {
				return out, ErrManagedConflict
			}
			var replaced bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_dashboard_delivery_plans WHERE workspace_id=dbhub_workspace() AND previous_id=$1)`, old.ID).Scan(&replaced); err != nil {
				return out, err
			}
			if replaced {
				return out, ErrManagedConflict
			}
			previous = &in.Previous
		}
		sealed, err := r.reports.cipher.Encrypt(string(canonicalReportJSON(basis)))
		if err != nil {
			return out, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO sys_dashboard_delivery_plans(workspace_id,dashboard_id,publication_no,owner_id,owner_member_version,
			kind,status,dataset_keys,recipient_ids,direct_ids,group_ids,request_payload,approval_payload,decided_at,review_until,previous_id,interval_minutes)
			VALUES(dbhub_workspace(),$1,$2,dbhub_actor(),dbhub_member_epoch(),$3,CASE WHEN $3='personal' THEN 'approved' ELSE 'pending' END,
			$4,$5,$6,$7,$8,CASE WHEN $3='personal' THEN $8 ELSE '' END,CASE WHEN $3='personal' THEN statement_timestamp() END,
			CASE WHEN $3='personal' THEN statement_timestamp()+interval '90 days' END,$9,NULLIF($10,0)) RETURNING id,version`,
			id, in.Publication, in.Kind, datasets, roster, direct, groups, sealed, previous, in.Interval).Scan(&out.ID, &out.Version)
		return out, err
	})
}

type DashboardDeliveryReview struct {
	Version     int64          `json:"version"`
	Basis       string         `json:"basis_sha256"`
	Kind        string         `json:"kind"`
	Publication int64          `json:"publication_no"`
	Datasets    []string       `json:"datasets"`
	TileKeys    []string       `json:"tile_keys"`
	Recipients  []int64        `json:"recipient_ids"`
	Params      map[string]any `json:"params"`
	Reason      string         `json:"reason,omitempty"`
}

func (r *ManagedDashboardRepository) dashboardDeliveryReviewBasis(ctx context.Context, tx pgx.Tx, id int64) (DashboardDeliveryReview, dashboardDeliveryPlan, dashboardDeliveryBasis, error) {
	var out DashboardDeliveryReview
	var requested dashboardDeliveryBasis
	if err := r.dashboardDeliveryContext(ctx, tx, id); err != nil {
		return out, dashboardDeliveryPlan{}, requested, err
	}
	p, err := r.readDashboardDeliveryPlan(ctx, tx, id)
	if err != nil {
		return out, p, requested, err
	}
	var canReview bool
	if err = tx.QueryRow(ctx, `SELECT dbhub_dashboard_delivery_open($1,dbhub_actor(),'review')`, id).Scan(&canReview); err != nil {
		return out, p, requested, err
	}
	if !canReview {
		return out, p, requested, ErrManagedDenied
	}
	if p.Status != "pending" {
		return out, p, requested, ErrManagedConflict
	}
	if err = r.reports.decodeDashboardDelivery(p.Request, &requested); err != nil {
		return out, p, requested, err
	}
	definition, current, err := r.dashboardDeliveryBase(ctx, tx, p.Dashboard, p.Publication, requested.Input.TileKey)
	if err != nil {
		return out, p, current, err
	}
	if current.Definition != requested.Definition || !dashboardGrant(ctx, tx, definition.UserIDs, definition.GroupIDs, p.Owner) {
		return out, p, current, ErrManagedConflict
	}
	roster, err := reportAudienceMembers(ctx, tx, p.Direct, p.Groups)
	if err != nil {
		return out, p, current, err
	}
	if !slices.Equal(roster, p.Recipients) {
		return out, p, current, ErrManagedConflict
	}
	for _, uid := range roster {
		if !dashboardGrant(ctx, tx, definition.UserIDs, definition.GroupIDs, uid) {
			return out, p, current, ErrManagedConflict
		}
	}
	if err = r.dashboardDeliveryMembers(ctx, tx, &current, roster, ActorID(ctx)); err != nil {
		return out, p, current, err
	}
	if p.Kind == "team" {
		for i, card := range requested.Cards {
			if card.Owner == nil {
				continue
			}
			ok, err := currentDashboardDeliveryOwnerScope(ctx, tx, p.ID, card)
			if err != nil {
				return out, p, current, err
			}
			if !ok {
				return out, p, current, ErrManagedConflict
			}
			current.Cards[i].Owner = card.Owner
		}
	}
	current.Input = requested.Input
	if p.Kind == "export" && reportDigest(requested) != reportDigest(current) {
		return out, p, current, ErrManagedConflict
	}
	out = DashboardDeliveryReview{Version: p.Version, Basis: reportDigest([]any{p.ID, p.Version, current, ActorID(ctx)}), Kind: p.Kind,
		Publication: p.Publication, Datasets: p.Datasets, Recipients: roster, Params: current.Input.Params, Reason: current.Input.Reason}
	for _, c := range current.Cards {
		out.TileKeys = append(out.TileKeys, c.Approval.Key)
	}
	return out, p, current, nil
}

func (r *ManagedDashboardRepository) ReviewDashboardDelivery(ctx context.Context, id int64) (DashboardDeliveryReview, error) {
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return DashboardDeliveryReview{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out, _, _, err := r.dashboardDeliveryReviewBasis(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (r *ManagedDashboardRepository) DecideDashboardDelivery(ctx context.Context, key string, id, version int64, basis, decision string) (ReportCommandResult, error) {
	if version < 1 || len(basis) != 64 || decision != "approved" && decision != "rejected" {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.reports.command(ctx, key, "dashboard-delivery-review", []any{id, version, basis, decision}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		if err := lockDashboardDelivery(ctx, tx, id); err != nil {
			return out, err
		}
		review, p, approved, err := r.dashboardDeliveryReviewBasis(ctx, tx, id)
		if err != nil {
			return out, err
		}
		if p.Version != version || review.Basis != basis {
			return out, ErrManagedConflict
		}
		sealed := ""
		if decision == "approved" {
			sealed, err = r.reports.cipher.Encrypt(string(canonicalReportJSON(approved)))
			if err != nil {
				return out, err
			}
		}
		var at *time.Time
		err = tx.QueryRow(ctx, `UPDATE sys_dashboard_delivery_plans SET status=$2,version=version+1,approval_payload=$3,
			reviewer_id=dbhub_actor(),basis_sha256=$4,decided_at=CASE WHEN $2='approved' THEN statement_timestamp() END,
			review_until=CASE WHEN $2='approved' THEN statement_timestamp()+CASE WHEN kind='team' THEN interval '30 days' ELSE interval '1 day' END END
			WHERE workspace_id=dbhub_workspace() AND id=$1 AND version=$5 AND status='pending' RETURNING version,decided_at`, id, decision, sealed, basis, version).
			Scan(&out.Version, &at)
		if err != nil {
			return out, err
		}
		if p.Kind == "export" && at != nil {
			var live bool
			if err = tx.QueryRow(ctx, `SELECT dbhub_dashboard_delivery_live($1,true)`, id).Scan(&live); err != nil {
				return out, err
			}
			if !live {
				return out, ErrManagedConflict
			}
			_, err = tx.Exec(ctx, `SELECT dbhub_dashboard_delivery_enqueue($1,$2)`, id, *at)
		}
		return out, err
	})
}

func (r *ManagedDashboardRepository) ChangeDashboardRecipient(ctx context.Context, key string, id int64, in TeamRecipientCommand) (ReportCommandResult, error) {
	grant := in.Action == "grant"
	if in.RecipientID < 1 || in.Version < 0 || !grant && in.Action != "revoke" || strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 500 {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.reports.command(ctx, key, "dashboard-recipient-access", []any{id, in}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: in.RecipientID}
		if err := r.dashboardDeliveryContext(ctx, tx, 0); err != nil {
			return out, err
		}
		if err := lockDashboardDelivery(ctx, tx, id); err != nil {
			return out, err
		}
		p, err := r.readDashboardDeliveryPlan(ctx, tx, id)
		if err != nil {
			return out, err
		}
		var allowed, live, selected bool
		if err = tx.QueryRow(ctx, `SELECT dbhub_dashboard_delivery_open($1,dbhub_actor(),'authorize'),
			dbhub_dashboard_delivery_live($1,true),dbhub_dashboard_delivery_open($1,$2,'recipient')`, id, in.RecipientID).Scan(&allowed, &live, &selected); err != nil {
			return out, err
		}
		if !allowed || !containsID(p.Recipients, in.RecipientID) {
			return out, ErrManagedDenied
		}
		if grant && (!live || !selected) {
			return out, ErrManagedConflict
		}
		var version, member int64
		var review bool
		err = tx.QueryRow(ctx, `SELECT version,member_version,needs_review FROM sys_dashboard_recipient_access
			WHERE workspace_id=dbhub_workspace() AND plan_id=$1 AND user_id=$2`, id, in.RecipientID).Scan(&version, &member, &review)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if version != in.Version || !grant && version == 0 || grant && review {
			return out, ErrManagedConflict
		}
		if grant {
			err = tx.QueryRow(ctx, `SELECT version FROM sys_workspace_members WHERE workspace_id=dbhub_workspace() AND user_id=$1
				AND active AND dbhub_iam_task_allowed(dbhub_workspace(),$1,'dashboard.view')`, in.RecipientID).Scan(&member)
			if err != nil {
				return out, managedError(err)
			}
		}
		err = tx.QueryRow(ctx, `INSERT INTO sys_dashboard_recipient_access(workspace_id,plan_id,user_id,version,member_version,enabled,changed_by,reason)
			VALUES(dbhub_workspace(),$1,$2,1,$3,$4,dbhub_actor(),$5)
			ON CONFLICT(workspace_id,plan_id,user_id) DO UPDATE SET version=sys_dashboard_recipient_access.version+1,
			member_version=$3,enabled=$4,changed_by=dbhub_actor(),reason=$5 RETURNING version`, id, in.RecipientID, member, grant, in.Reason).Scan(&out.Version)
		return out, err
	})
}
