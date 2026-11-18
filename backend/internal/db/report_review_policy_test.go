package db

import "testing"

func TestReportReviewBoundaryExpansion(t *testing.T) {
	deptA := OrganizationScopeEntry{DepartmentKey: "a"}
	deptB := OrganizationScopeEntry{DepartmentKey: "b"}
	before := reportReviewBoundary{Dataset: "orders", Source: 1, Account: 2, Schema: "public", View: "approved",
		Columns: []string{"total"}, Audience: []reportReviewAudience{{UserID: 3, Scopes: []OrganizationScopeEntry{deptA}}}}
	for _, tc := range []struct {
		name  string
		setup func(*reportReviewBoundary)
	}{
		{"source revision", func(x *reportReviewBoundary) { x.Source++ }},
		{"execution account", func(x *reportReviewBoundary) { x.Account++ }},
		{"organization scope", func(x *reportReviewBoundary) {
			x.Audience = []reportReviewAudience{{UserID: 3, Scopes: []OrganizationScopeEntry{deptA, deptB}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := before
			tc.setup(&after)
			if !reportReviewExpanded(before, after) {
				t.Fatal("changed boundary did not require dual review")
			}
		})
	}
	after := before
	after.Definition = "revision-without-a-boundary-change"
	if reportReviewExpanded(before, after) {
		t.Fatal("definition-only change unexpectedly expanded the reviewed boundary")
	}
}

func TestBusinessReportContractMaintainsMaskedProjection(t *testing.T) {
	columns := []string{"note", "watermark"}
	fields := map[string]businessFieldProtection{
		"note":      {class: businessClassSensitive},
		"watermark": {class: businessClassInternal},
	}
	contract := &ApprovedContract{Declaration: DataContract{
		Schema:    []ContractColumn{{Name: "note", Type: "string", Nullable: true}, {Name: "watermark", Type: "timestamp_with_timezone"}},
		Freshness: ContractFreshness{Mode: "watermark_column", Column: "watermark"},
	}}
	if !validBusinessReportContract(contract, columns, fields) {
		t.Fatal("compatible masked projection was rejected")
	}
	if validBusinessReportContract(contract, []string{"watermark", "note"}, fields) {
		t.Fatal("reordered projection was accepted")
	}
	contract.Declaration.Schema[0].Nullable = false
	if validBusinessReportContract(contract, columns, fields) {
		t.Fatal("non-null contract accepted a masked column")
	}
	contract.Declaration.Schema[0].Nullable = true
	contract.Declaration.Schema[1].Nullable = true
	fields["watermark"] = businessFieldProtection{class: businessClassSensitive}
	if validBusinessReportContract(contract, columns, fields) {
		t.Fatal("masked freshness watermark was accepted")
	}
}
