package db

import "testing"

func TestExportPagingDetectsOverflow(t *testing.T) {
	maximum := float64(100)
	policy := &ReportExportPolicy{Enabled: true, MaxRows: 100, PageSizeParameter: "size"}
	defs := []ReportParameter{{Name: "size", Label: "行数", Type: "integer", Max: &maximum}}
	if ValidateReportExportParameters(policy, defs) == nil {
		t.Fatal("paging hides an overflow row")
	}
	maximum = 101
	if err := ValidateReportExportParameters(policy, defs); err != nil {
		t.Fatal(err)
	}
}
