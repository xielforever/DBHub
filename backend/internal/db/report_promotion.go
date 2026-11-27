package db

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

func (r *ManagedReportRepository) ApprovedVersion(ctx context.Context, id, reviewID int64) (*ReportVersion, error) {
	if _, err := r.Get(ctx, id); err != nil {
		return nil, err
	}
	v := &ReportVersion{ReportID: id, ReviewID: reviewID}
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT v.id,v.number,v.definition,v.content_hash FROM sys_report_versions v JOIN sys_report_reviews rv ON rv.version_id=v.id WHERE v.report_id=$1 AND rv.id=$2 AND rv.status='approved'`, id, reviewID).Scan(&v.ID, &v.Number, &raw, &v.ContentHash)
	if err != nil {
		return nil, managedError(err)
	}
	err = json.Unmarshal(raw, &v.Definition)
	return v, err
}

func checkPromotion(ctx context.Context, tx pgx.Tx, p *ReportPromotion) error {
	if p == nil {
		return nil
	}
	var hash string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT v.content_hash,v.definition FROM sys_managed_reports m JOIN sys_report_versions v ON v.report_id=m.id JOIN sys_report_reviews rv ON rv.version_id=v.id WHERE m.id=$1 AND v.id=$2 AND v.number=$3 AND rv.id=$4 AND rv.status='approved' AND (m.owner_id=$5 OR $6) FOR SHARE OF m,rv`, p.ReportID, p.VersionID, p.Version, p.ReviewID, ActorID(ctx), ActorReportReviewer(ctx)).Scan(&hash, &raw)
	if err != nil {
		return managedError(err)
	}
	// Compare the immutable version's source, not the mutable current draft.
	var original ReportDefinition
	if err = json.Unmarshal(raw, &original); err != nil {
		return err
	}
	if hash != p.ContentHash || original.DefinitionID != p.DefinitionID {
		return ErrManagedConflict
	}
	return nil
}
