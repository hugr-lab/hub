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
