package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrDashboardRecipientReview = fmt.Errorf("%w: 收件数据范围扩大，需重新送审", ErrManagedConflict)

func (r *ManagedReportRepository) decodeDashboardDelivery(sealed string, out any) error {
	plain, err := r.cipher.Decrypt(sealed)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewBufferString(plain))
	d.UseNumber()
	return d.Decode(out)
}

type DashboardDeliverySchedule struct {
	PlanID, OwnerID, WorkspaceID, MemberVersion int64
	Slot                                        time.Time
}

func (r *ManagedDashboardRepository) DueDashboardDeliveries(ctx context.Context) ([]DashboardDeliverySchedule, error) {
	if r.reports.phase2ReportIsolation == "" {
		return nil, nil
	}
	rows, err := r.pool.maintenance.Query(ctx, `WITH due AS(
		SELECT p.id,p.owner_id,p.workspace_id,p.owner_member_version,p.checked_slot,
		date_bin(make_interval(mins=>p.interval_minutes),statement_timestamp(),p.decided_at) AS slot
		FROM sys_dashboard_delivery_plans p JOIN sys_iam_isolated_dashboard_reviews d USING(workspace_id,dashboard_id)
		JOIN sys_iam_isolation_guard i ON i.isolation_id=d.isolation_id AND i.database_name=current_database()
		WHERE d.isolation_id=$1 AND p.status='approved' AND p.kind<>'export' AND p.review_until>statement_timestamp()
		AND p.decided_at+make_interval(mins=>p.interval_minutes)<=statement_timestamp()
		AND NOT EXISTS(SELECT 1 FROM sys_dashboard_delivery_plans n WHERE n.workspace_id=p.workspace_id AND n.previous_id=p.id))
		SELECT id,owner_id,workspace_id,owner_member_version,slot FROM due
		WHERE checked_slot IS NULL OR checked_slot<slot ORDER BY slot,id LIMIT 20`, r.reports.phase2ReportIsolation)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[DashboardDeliverySchedule])
}

func (r *ManagedDashboardRepository) OpenDashboardDeliverySlot(ctx context.Context, item DashboardDeliverySchedule) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = r.dashboardDeliveryContext(ctx, tx, 0); err != nil {
		return err
	}
	if err = lockDashboardDelivery(ctx, tx, item.PlanID); err != nil {
		return err
	}
	p, err := r.readDashboardDeliveryPlan(ctx, tx, item.PlanID)
	if err != nil {
		return err
	}
	if p.Owner != ActorID(ctx) {
		return ErrManagedDenied
	}
	if p.Kind == "team" && p.Status == "approved" {
		var basis dashboardDeliveryBasis
		if err = r.reports.decodeDashboardDelivery(p.Approval, &basis); err != nil {
			return err
		}
		if err = checkDashboardDeliveryOwner(ctx, tx, p, basis); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `SELECT dbhub_dashboard_delivery_enqueue($1,$2)`, item.PlanID, item.Slot)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ManagedDashboardRepository) MarkDashboardDeliverySlot(ctx context.Context, item DashboardDeliverySchedule) error {
	_, err := r.pool.maintenance.Exec(ctx, `UPDATE sys_dashboard_delivery_plans SET checked_slot=$3
		WHERE workspace_id=$1 AND id=$2 AND (checked_slot IS NULL OR checked_slot<$3)`, item.WorkspaceID, item.PlanID, item.Slot)
	return err
}

type DashboardDeliveryJob struct {
	ID, WorkspaceID, PlanID, UserID, MemberVersion int64
	token                                          string
}

func (r *ManagedDashboardRepository) ClaimDashboardDelivery(ctx context.Context) (*DashboardDeliveryJob, error) {
	if r.reports.phase2ReportIsolation == "" {
		return nil, ErrNotFound
	}
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, subscriptionQueueLock); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_dashboard_deliveries SET status='interrupted',worker_token=NULL,lease_until=NULL
		WHERE status='running' AND lease_until<=statement_timestamp()`); err != nil {
		return nil, err
	}
	j := &DashboardDeliveryJob{token: dashboardUUID()}
	err = tx.QueryRow(ctx, `UPDATE sys_dashboard_deliveries d SET status='running',worker_token=$1::uuid,lease_until=statement_timestamp()+interval '6 minutes'
		WHERE d.id=(SELECT q.id FROM sys_dashboard_deliveries q JOIN sys_dashboard_delivery_plans p ON p.id=q.plan_id
		JOIN sys_iam_isolated_dashboard_reviews e ON e.workspace_id=p.workspace_id AND e.dashboard_id=p.dashboard_id
		JOIN sys_iam_isolation_guard i ON i.isolation_id=e.isolation_id AND i.database_name=current_database()
		WHERE q.status='queued' AND e.isolation_id=$2 AND NOT EXISTS(SELECT 1 FROM sys_dashboard_deliveries busy
			WHERE busy.plan_id=q.plan_id AND busy.user_id=q.user_id AND busy.status='running')
		ORDER BY q.slot,q.id FOR UPDATE OF q SKIP LOCKED LIMIT 1)
		AND (SELECT count(*) FROM sys_dashboard_deliveries WHERE status='running')<2
		RETURNING d.id,d.workspace_id,d.plan_id,d.user_id,d.member_version`, j.token, r.reports.phase2ReportIsolation).
		Scan(&j.ID, &j.WorkspaceID, &j.PlanID, &j.UserID, &j.MemberVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if e := tx.Commit(ctx); e != nil {
		return nil, e
	}
	if err != nil {
		return nil, ErrNotFound
	}
	return j, nil
}

func (r *ManagedDashboardRepository) FailDashboardDelivery(ctx context.Context, j *DashboardDeliveryJob, skip, review bool) error {
	status := "failed"
	if skip {
		status = "skipped"
	}
	_, err := r.pool.maintenance.Exec(ctx, `WITH stopped AS(UPDATE sys_dashboard_deliveries SET status=$3,worker_token=NULL,lease_until=NULL
		WHERE id=$1 AND status='running' AND worker_token=$2::uuid RETURNING workspace_id,plan_id,user_id,grant_version)
		UPDATE sys_dashboard_recipient_access a SET enabled=false,needs_review=true,version=a.version+1,
			reason='当前数据范围扩大，需重新送审' FROM stopped s WHERE $4 AND a.workspace_id=s.workspace_id
		AND a.plan_id=s.plan_id AND a.user_id=s.user_id AND a.version=s.grant_version`, j.ID, j.token, status, review)
	return err
}

type dashboardCardProof struct {
	Key, Policy, Manifest          string
	Publication, AccountGeneration int64
}
type dashboardDeliveryProof struct {
	Dashboard, Publication int64
	Groups                 string
	Cards                  []dashboardCardProof
}

type DashboardDeliveryWork struct {
	plan    dashboardDeliveryPlan
	basis   dashboardDeliveryBasis
	proof   dashboardDeliveryProof
	Refresh *DashboardRefresh
}

func currentDashboardDeliveryOwnerScope(ctx context.Context, tx pgx.Tx, plan int64, card dashboardDeliveryCard) (bool, error) {
	var allowed bool
	if card.Owner == nil {
		return false, nil
	}
	err := tx.QueryRow(ctx, `SELECT dbhub_dashboard_delivery_owner_scope($1,$2,$3,$4,$5,$6)`,
		plan, card.Approval.ReportID, card.Approval.Scope.Dataset, card.Owner.Version, card.Owner.IdentityVersion,
		card.Approval.Scope.DatasetVersion).Scan(&allowed)
	return allowed, err
}

func checkDashboardDeliveryOwner(ctx context.Context, tx pgx.Tx, p dashboardDeliveryPlan, basis dashboardDeliveryBasis) error {
	if p.Kind != "team" {
		return nil
	}
	for _, card := range basis.Cards {
		if !card.Required {
			continue
		}
		allowed, err := currentDashboardDeliveryOwnerScope(ctx, tx, p.ID, card)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrManagedDenied
		}
	}
	return nil
}

// dashboardDeliveryAccess rechecks the recipient grant generation, current roster
// and owner qualification, independent of the payload's retention clock.
func (r *ManagedDashboardRepository) dashboardDeliveryAccess(ctx context.Context, tx pgx.Tx, id int64, token string) (DashboardDeliveryWork, error) {
	var out DashboardDeliveryWork
	if err := r.dashboardDeliveryContext(ctx, tx, 0); err != nil {
		return out, err
	}
	var plan int64
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT d.plan_id,dbhub_dashboard_delivery_live(d.plan_id,$2<>'')
		AND dbhub_dashboard_delivery_open(d.plan_id,dbhub_actor(),'recipient') AND d.member_version=dbhub_member_epoch()
		AND (p.kind<>'team' OR EXISTS(SELECT 1 FROM sys_dashboard_recipient_access a WHERE a.workspace_id=d.workspace_id
		AND a.plan_id=d.plan_id AND a.user_id=d.user_id AND a.enabled AND a.version=d.grant_version AND a.member_version=d.member_version))
		FROM sys_dashboard_deliveries d JOIN sys_dashboard_delivery_plans p ON p.workspace_id=d.workspace_id AND p.id=d.plan_id
		WHERE d.workspace_id=dbhub_workspace() AND d.id=$1 AND d.user_id=dbhub_actor()
		AND (($2='' AND d.status='succeeded') OR ($2<>'' AND d.status='running' AND d.worker_token=NULLIF($2,'')::uuid AND d.lease_until>statement_timestamp()))`, id, token).
		Scan(&plan, &allowed)
	if err != nil {
		return out, managedError(err)
	}
	if !allowed {
		return out, ErrManagedDenied
	}
	out.plan, err = r.readDashboardDeliveryPlan(ctx, tx, plan)
	if err != nil {
		return out, err
	}
	err = r.reports.decodeDashboardDelivery(out.plan.Approval, &out.basis)
	if err != nil {
		return out, err
	}
	return out, checkDashboardDeliveryOwner(ctx, tx, out.plan, out.basis)
}

func (r *ManagedDashboardRepository) dashboardDeliveryProof(ctx context.Context, tx pgx.Tx, w *DashboardDeliveryWork, durable bool) error {
	p, basis := w.plan, w.basis
	view, definition, err := r.publishedDefinition(ctx, tx, p.Dashboard)
	if err != nil {
		return err
	}
	if view.PublicationNo != p.Publication || reportDigest(definition) != basis.Definition {
		return ErrManagedConflict
	}
	proof := dashboardDeliveryProof{Dashboard: p.Dashboard, Publication: p.Publication}
	groups := append(append([]int64{}, p.Groups...), definition.GroupIDs...)
	for _, card := range basis.Cards {
		groups = append(groups, card.Approval.Scope.Groups...)
	}
	var groupVersions []string
	if durable {
		if err = lockAudienceGroups(ctx, tx, groups); err != nil {
			return err
		}
		for _, key := range p.Datasets {
			if err = lockBusinessReportPolicy(ctx, tx, key); err != nil {
				return err
			}
		}
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(array_agg(id::text||':'||version::text ORDER BY id),'{}'::text[]) FROM sys_user_groups
		WHERE id=ANY($1::bigint[])`, groups).Scan(&groupVersions); err != nil {
		return err
	}
	proof.Groups = reportDigest(groupVersions)
	for _, card := range basis.Cards {
		c := card.Approval
		ix := slices.IndexFunc(card.Members, func(m reportReviewAudience) bool { return m.UserID == ActorID(ctx) })
		if ix < 0 {
			if card.Required {
				return ErrManagedDenied
			}
			proof.Cards = append(proof.Cards, dashboardCardProof{Key: c.Key})
			continue
		}
		x, e := r.reports.ResolvePublished(ctx, c.ReportID, c.Publication)
		if errors.Is(e, ErrNotFound) || errors.Is(e, ErrManagedDenied) {
			if card.Required {
				return ErrManagedDenied
			}
			proof.Cards = append(proof.Cards, dashboardCardProof{Key: c.Key})
			continue
		}
		if e != nil {
			return e
		}
		if x.Manifest.Contract == nil || x.Manifest.Contract.Hash != c.Contract {
			return ErrManagedConflict
		}
		if e = r.reports.useBusinessReportBinding(ctx, tx, x); e != nil {
			return e
		}
		row, output, _, e := r.reports.businessReportPolicy(ctx, tx, x)
		if e != nil {
			return e
		}
		member, e := readOrganizationScope(ctx, tx, ActorID(ctx), row.DatasetKey)
		if errors.Is(e, ErrNotFound) && !card.Required {
			proof.Cards = append(proof.Cards, dashboardCardProof{Key: c.Key})
			continue
		}
		if e != nil {
			return e
		}
		scopes := effectiveBusinessScopes(row.Definition, member)
		if len(scopes) == 0 {
			if card.Required {
				return ErrManagedDenied
			}
			proof.Cards = append(proof.Cards, dashboardCardProof{Key: c.Key})
			continue
		}
		if !subset(scopes, card.Members[ix].Scopes) {
			return ErrDashboardRecipientReview
		}
		frozen := c.Scope
		frozen.Audience = card.Members
		if !custodyReadCovered(frozen, x, row, output, member) {
			return ErrManagedConflict
		}
		if p.Kind == "export" && (x.Definition.ExportPolicy == nil || ValidateReportExportPolicy(x.Definition.ExportPolicy) != nil) {
			return ErrManagedDenied
		}
		if e = r.reports.prepareBusinessReport(ctx, tx, x); e != nil {
			return e
		}
		proof.Cards = append(proof.Cards, dashboardCardProof{Key: c.Key, Policy: x.BusinessPolicyStamp(), Manifest: x.Hash, Publication: x.PublicationNo, AccountGeneration: x.Generation})
	}
	w.proof = proof
	return nil
}

func (r *ManagedDashboardRepository) checkDashboardWork(ctx context.Context, j *DashboardDeliveryJob) (DashboardDeliveryWork, error) {
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return DashboardDeliveryWork{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out, err := r.dashboardDeliveryAccess(ctx, tx, j.ID, j.token)
	if err != nil {
		return out, err
	}
	err = r.dashboardDeliveryProof(ctx, tx, &out, false)
	return out, err
}

func (r *ManagedDashboardRepository) LoadDashboardWork(ctx context.Context, j *DashboardDeliveryJob) (DashboardDeliveryWork, error) {
	out, err := r.checkDashboardWork(ctx, j)
	if err != nil {
		return out, err
	}
	out.Refresh, err = r.Refresh(ctx, out.plan.Dashboard, "published", 0, out.basis.Input.Params)
	if err != nil {
		return out, err
	}
	if out.Refresh.PublicationNo != out.plan.Publication {
		return out, ErrManagedConflict
	}
	return out, nil
}

// DashboardWorkTile returns only cards proven to belong to the authorized
// selection, and repeats the whole recipient fence inside the query handler.
func (r *ManagedDashboardRepository) DashboardWorkTile(ctx context.Context, j *DashboardDeliveryJob, w DashboardDeliveryWork, key string) (*DashboardExecution, error) {
	i := slices.IndexFunc(w.proof.Cards, func(c dashboardCardProof) bool { return c.Key == key })
	if i < 0 || w.proof.Cards[i].Policy == "" {
		return nil, ErrNotFound
	}
	x, err := r.ResolveTile(ctx, w.Refresh.ID, key)
	if err != nil {
		return nil, err
	}
	expected := w.proof.Cards[i]
	if expected.Manifest != x.Report.Hash || expected.Publication != x.Report.PublicationNo || expected.AccountGeneration != x.Report.Generation {
		return nil, ErrManagedConflict
	}
	guard := x.Report.AdditionalCheck
	x.Report.AdditionalCheck = func(run context.Context) error {
		if err := guard(run); err != nil {
			return err
		}
		cur, err := r.checkDashboardWork(run, j)
		if err != nil {
			return err
		}
		if reportDigest(cur.proof) != reportDigest(w.proof) {
			return ErrManagedConflict
		}
		return nil
	}
	return x, nil
}
