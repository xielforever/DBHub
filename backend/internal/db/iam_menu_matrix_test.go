package db

import (
	"slices"
	"testing"
	"time"

	"github.com/user/dbhub/internal/iam"
)

func TestIAMRoleMenuMatrixUsesQualifiedRoleUnion(t *testing.T) {
	now := time.Now()
	expired := now.Add(-time.Second)
	choices := IAMRoleMenuVisibility{"hidden": {"M04": "hide"}, "visible": {"M04": "show"}}
	items := IAMMenus{Items: []IAMMenuItem{{ID: "M04", Visible: true}}, RoleVisibility: &choices}
	allowsPage := []string{"query.page"}
	hidden := iam.Source{RoleID: "hidden", Actions: allowsPage}
	visible := iam.Source{RoleID: "visible", Actions: allowsPage}
	for _, tc := range []struct {
		name    string
		target  bool
		global  bool
		actions []string
		sources []iam.Source
		want    bool
	}{
		{"hide in the only source", true, true, allowsPage, []iam.Source{hidden}, false},
		{"show when another valid source allows", true, true, allowsPage, []iam.Source{hidden, visible}, true},
		{"page permission must be in the contributing role", true, true, allowsPage, []iam.Source{hidden, {RoleID: "visible", Actions: []string{"audit.page"}}}, false},
		{"expired source does not contribute", true, true, allowsPage, []iam.Source{hidden, {RoleID: "visible", Actions: allowsPage, ExpiresAt: &expired}}, false},
		{"workspace hide is an upper bound", true, false, allowsPage, []iam.Source{visible}, false},
		{"final page denial is an upper bound", true, true, nil, []iam.Source{visible}, false},
		{"missing choice inherits", true, true, allowsPage, []iam.Source{{RoleID: "default", Actions: allowsPage}}, true},
		{"compatibility navigation is preserved", false, true, allowsPage, []iam.Source{hidden}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items.Items[0].Visible = tc.global
			snapshot := &iam.Snapshot{Target: tc.target, Sources: tc.sources}
			if got := slices.Contains(iamVisibleMenuIDs(snapshot, items, tc.actions, now), "M04"); got != tc.want {
				t.Fatalf("visible = %v, want %v", got, tc.want)
			}
		})
	}
}
