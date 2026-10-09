package server

import "testing"

// PLAT-767: each product has its own create permission. The role says whether an account creates at all; the
// per-product list narrows a creator and can grant an editor; a viewer never creates and an admin always may.
func TestCreatePermissionIsPerProduct(t *testing.T) {
	list := func(products ...string) *[]string { return &products }
	cases := []struct {
		name    string
		rec     UserRecord
		product string
		want    bool
	}{
		{"creator, no list: every product", UserRecord{CanCreate: true}, "work", true},
		{"creator narrowed to workflows: no Crews", UserRecord{CanCreate: true, CreateProducts: list("agentworks")}, "work", false},
		{"creator narrowed to workflows: workflows", UserRecord{CanCreate: true, CreateProducts: list("agentworks")}, "agentworks", true},
		{"creator with an empty list: nothing", UserRecord{CanCreate: true, CreateProducts: list()}, "agentworks", false},
		{"editor, no list: nothing", UserRecord{Role: UserRoleEditor}, "work", false},
		{"editor granted Crews: Crews", UserRecord{Role: UserRoleEditor, CreateProducts: list("work")}, "work", true},
		{"editor granted Crews: not workflows", UserRecord{Role: UserRoleEditor, CreateProducts: list("work")}, "agentworks", false},
		{"viewer with a list: nothing", UserRecord{Role: UserRoleViewer, CreateProducts: list("work")}, "work", false},
		{"admin with an empty list: everything", UserRecord{Admin: true, CreateProducts: list()}, "code", true},
		{"disabled creator: nothing", UserRecord{CanCreate: true, Disabled: true}, "agentworks", false},
	}
	for _, tc := range cases {
		rec := tc.rec
		if got := accessForRecord(&rec).CanCreateIn(tc.product); got != tc.want {
			t.Errorf("%s: CanCreateIn(%q) = %v, want %v", tc.name, tc.product, got, tc.want)
		}
	}
}
