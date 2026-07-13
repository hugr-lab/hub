package hubapp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"
)

// Integration tests require live Hugr server.
// Run with: go test ./pkg/hubapp/ -run TestIntegration -v
// Env: HUGR_URL (default localhost:15004/ipc), HUGR_SECRET_KEY (default secret-key)
//
// The ADK-era conversation/message integration tests were removed with the HB6
// store-prune (conversations/agent_messages dropped; the user-facing thread is
// now `chats`, whose transcript lives in the Agent DB). Chat/project coverage
// rides the HB-EXT cross-source gate.

func skipIfNoHugr(t *testing.T) {
	t.Helper()
	hugrURL := os.Getenv("HUGR_URL")
	if hugrURL == "" {
		hugrURL = "http://localhost:15004/ipc"
	}
	secretKey := os.Getenv("HUGR_SECRET_KEY")
	if secretKey == "" {
		secretKey = "secret-key"
	}

	c := NewHugrClient(hugrURL, secretKey, 10*time.Second, 0)
	ctx := auth.ContextWithUser(context.Background(), auth.UserInfo{ID: "test", Role: "admin"})
	res, err := c.Query(ctx, `{ hub { db { users(limit: 1) { id } } } }`, nil)
	if err != nil {
		t.Skipf("Hugr not available: %v", err)
	}
	defer res.Close()
	if res.Err() != nil {
		t.Skipf("Hugr query error: %v", res.Err())
	}
}

func testClient(t *testing.T, userID ...string) (*HubApp, context.Context) {
	t.Helper()
	hugrURL := os.Getenv("HUGR_URL")
	if hugrURL == "" {
		hugrURL = "http://localhost:15004/ipc"
	}
	secretKey := os.Getenv("HUGR_SECRET_KEY")
	if secretKey == "" {
		secretKey = "secret-key"
	}

	c := NewHugrClient(hugrURL, secretKey, 10*time.Second, 0)
	app := &HubApp{
		client: c,
		logger: slog.Default(),
	}

	uid := "admin"
	if len(userID) > 0 {
		uid = userID[0]
	}
	ctx := auth.ContextWithUser(context.Background(), auth.UserInfo{
		ID: uid, Name: uid, Role: "admin",
	})
	return app, ctx
}

// TestIntegration_EnsureAgentRoleFloor_ManagedSubset pins the core invariant of
// the per-agent role model (design/008 spec-hub-gateway §12): re-seeding the
// isolation floor on a role must replace ONLY hub's floor rows and leave an
// admin-added capability grant on the same role untouched — that coexistence is
// what lets an admin grant data-source / function access with standard
// core.insert_role_permissions mutations after create_agent.
func TestIntegration_EnsureAgentRoleFloor_ManagedSubset(t *testing.T) {
	skipIfNoHugr(t)
	app, _ := testClient(t)
	ctx := context.Background() // service principal (secret-key, full access) — as boot seeds
	role := fmt.Sprintf("agent:test-%d", time.Now().UnixNano())
	defer app.deleteRoleAndPerms(ctx, role)

	// A distinctive admin grant row (a data-object:query on a SOURCE table, not a
	// floor key) — this is what a real deployment adds post-creation.
	const grantType, grantField = "data-object:query", "some_source_table_zzz"
	insertGrant := func() {
		t.Helper()
		res, err := app.client.Query(ctx,
			`mutation($d: core_role_permissions_mut_input_data!) {
				core { insert_role_permissions(data: $d) { role } } }`,
			map[string]any{"d": map[string]any{
				"role": role, "type_name": grantType, "field_name": grantField,
				"disabled": false, "hidden": false,
			}},
		)
		if err != nil {
			t.Fatalf("insert grant: %v", err)
		}
		res.Close()
		if res.Err() != nil {
			t.Fatalf("insert grant: %v", res.Err())
		}
	}
	permKeys := func() map[permKey]bool {
		t.Helper()
		res, err := app.client.Query(ctx,
			`query($r: String!) { core { roles_by_pk(name: $r) { permissions { type_name field_name } } } }`,
			map[string]any{"r": role},
		)
		if err != nil {
			t.Fatalf("read perms: %v", err)
		}
		defer res.Close()
		if res.Err() != nil {
			t.Fatalf("read perms: %v", res.Err())
		}
		var role_ struct {
			Permissions []struct {
				TypeName  string `json:"type_name"`
				FieldName string `json:"field_name"`
			} `json:"permissions"`
		}
		if err := res.ScanData("core.roles_by_pk", &role_); err != nil && !isNoData(err) {
			t.Fatalf("scan perms: %v", err)
		}
		out := map[permKey]bool{}
		for _, p := range role_.Permissions {
			out[permKey{p.TypeName, p.FieldName}] = true
		}
		return out
	}

	// 1. First seed — the floor lands (createAgentRoleWithFloor verifies internally).
	if err := app.createAgentRoleWithFloor(ctx, role, "test per-agent role"); err != nil {
		t.Fatalf("first floor seed: %v", err)
	}
	// 2. Admin layers a capability grant.
	insertGrant()
	// 3. Re-seed the floor (a floor-schema change / boot reconcile).
	if err := app.createAgentRoleWithFloor(ctx, role, "test per-agent role"); err != nil {
		t.Fatalf("re-seed floor: %v", err)
	}
	// 4. The grant SURVIVED, and the floor is present.
	keys := permKeys()
	if !keys[permKey{grantType, grantField}] {
		t.Fatal("admin grant row was wiped by the floor re-seed — managed-subset broken")
	}
	sample := permKey{"data-object:query", "hub_agent_db_sessions"}
	if !keys[sample] {
		t.Fatalf("floor row %v missing after re-seed", sample)
	}
	// 5. Cleanup drops the whole role (floor + grant).
	if err := app.deleteRoleAndPerms(ctx, role); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	if inUse, _ := app.agentRoleInUse(ctx, role); inUse {
		t.Error("role still referenced after delete (should be unused)")
	}
	if left := permKeys(); len(left) != 0 {
		t.Errorf("role permissions remain after delete: %d", len(left))
	}

	// A protected platform role is refused outright.
	if err := app.createAgentRoleWithFloor(ctx, "admin", "nope"); err == nil {
		t.Error("createAgentRoleWithFloor must refuse the protected role 'admin'")
	}
}

func TestIntegration_EnsureUser(t *testing.T) {
	skipIfNoHugr(t)
	userID := fmt.Sprintf("test-user-%d", time.Now().UnixNano())
	app, ctx := testClient(t, userID)
	if err := app.ensureUser(ctx, auth.UserInfo{ID: userID, Role: "user"}); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	// Verify user exists
	res, err := app.client.Query(ctx,
		`query($id: String!) { hub { db { users(filter: { id: { eq: $id } }) { id display_name hugr_role } } } }`,
		map[string]any{"id": userID},
	)
	if err != nil {
		t.Fatalf("query user: %v", err)
	}
	defer res.Close()
	if res.Err() != nil {
		t.Fatalf("query error: %v", res.Err())
	}

	var users []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		HugrRole    string `json:"hugr_role"`
	}
	if err := res.ScanData("hub.db.users", &users); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(users) == 0 {
		t.Fatal("user not found after ensureUser")
	}
	if users[0].ID != userID {
		t.Fatalf("unexpected user id: %s", users[0].ID)
	}

	// Cleanup
	app.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_users(filter: { id: { eq: $id } }) { affected_rows } } } }`,
		map[string]any{"id": userID},
	)
}

// TestIntegration_EnsureUser_PlaceholderNameKeepsDisplayName reproduces the
// live-observed clobber (query-engine ask #7 companion): impersonation without
// a -User-Name header defaults user_name to the user id, and that id-as-name
// stub reached ensureUser's refresh and renamed a real user. The guard must
// (a) keep an established display_name when the token name equals the id, and
// (b) still honour a genuine IdP rename.
func TestIntegration_EnsureUser_PlaceholderNameKeepsDisplayName(t *testing.T) {
	skipIfNoHugr(t)
	userID := fmt.Sprintf("test-user-%d", time.Now().UnixNano())
	app, ctx := testClient(t, userID)
	defer app.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_users(filter: { id: { eq: $id } }) { affected_rows } } } }`,
		map[string]any{"id": userID},
	)

	displayNameOf := func() string {
		t.Helper()
		res, err := app.client.Query(ctx,
			`query($id: String!) { hub { db { users(filter: { id: { eq: $id } }) { display_name } } } }`,
			map[string]any{"id": userID},
		)
		if err != nil {
			t.Fatalf("query user: %v", err)
		}
		defer res.Close()
		if res.Err() != nil {
			t.Fatalf("query error: %v", res.Err())
		}
		var users []struct {
			DisplayName string `json:"display_name"`
		}
		if err := res.ScanData("hub.db.users", &users); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(users) == 0 {
			t.Fatal("user not found")
		}
		return users[0].DisplayName
	}

	// Provision with a real display name (an IdP-supplied name).
	if err := app.ensureUser(ctx, auth.UserInfo{ID: userID, Name: "Гейтвей Тестов", Role: "user"}); err != nil {
		t.Fatalf("ensureUser (provision): %v", err)
	}
	if got := displayNameOf(); got != "Гейтвей Тестов" {
		t.Fatalf("display_name = %q, want provisioned real name", got)
	}

	// Second touch with the id-as-name placeholder must NOT clobber it.
	if err := app.ensureUser(ctx, auth.UserInfo{ID: userID, Name: userID, Role: "user"}); err != nil {
		t.Fatalf("ensureUser (placeholder): %v", err)
	}
	if got := displayNameOf(); got != "Гейтвей Тестов" {
		t.Fatalf("display_name = %q — an id-as-name placeholder must not overwrite a real name", got)
	}

	// A genuine IdP rename must still be honoured.
	if err := app.ensureUser(ctx, auth.UserInfo{ID: userID, Name: "Gateway Renamed", Role: "user"}); err != nil {
		t.Fatalf("ensureUser (rename): %v", err)
	}
	if got := displayNameOf(); got != "Gateway Renamed" {
		t.Fatalf("display_name = %q — a real rename must still apply", got)
	}
}
