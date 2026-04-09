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

	c := NewHugrClient(hugrURL, secretKey, 10*time.Second)
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

	c := NewHugrClient(hugrURL, secretKey, 10*time.Second)
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
	app.ensureUser(ctx, userID, "user")

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

func TestIntegration_ConversationCRUD(t *testing.T) {
	skipIfNoHugr(t)
	userID := fmt.Sprintf("conv-test-%d", time.Now().UnixNano())
	app, ctx := testClient(t, userID)
	app.ensureUser(ctx, userID, "user")

	// Create conversation
	convID := fmt.Sprintf("conv-%d", time.Now().UnixNano())
	res, err := app.client.Query(ctx,
		`mutation($id: String!, $uid: String!, $title: String!, $mode: String!) {
			hub { db { insert_conversations(data: {
				id: $id, user_id: $uid, title: $title, mode: $mode
			}) { id title mode } } }
		}`,
		map[string]any{"id": convID, "uid": userID, "title": "Test Conv", "mode": "tools"},
	)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	res.Close()
	if res.Err() != nil {
		t.Fatalf("create conversation error: %v", res.Err())
	}

	// List conversations
	res, err = app.client.Query(ctx,
		`query($uid: String!) { hub { db { conversations(
			filter: { user_id: { eq: $uid } }
		) { id title mode } } } }`,
		map[string]any{"uid": userID},
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer res.Close()
	if res.Err() != nil {
		t.Fatalf("list error: %v", res.Err())
	}

	var convs []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Mode  string `json:"mode"`
	}
	if err := res.ScanData("hub.db.conversations", &convs); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(convs) == 0 {
		t.Fatal("no conversations found after create")
	}

	found := false
	for _, c := range convs {
		if c.ID == convID && c.Title == "Test Conv" && c.Mode == "tools" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created conversation not found in list, got: %+v", convs)
	}

	// Rename
	res2, err := app.client.Query(ctx,
		`mutation($id: String!, $uid: String!, $title: String!) { hub { db { update_conversations(
			filter: { id: { eq: $id }, user_id: { eq: $uid } }
			data: { title: $title }
		) { affected_rows } } } }`,
		map[string]any{"id": convID, "uid": userID, "title": "Renamed Conv"},
	)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	res2.Close()

	// Soft delete
	res3, err := app.client.Query(ctx,
		`mutation($id: String!, $uid: String!) { hub { db { delete_conversations(
			filter: { id: { eq: $id }, user_id: { eq: $uid } }
		) { affected_rows } } } }`,
		map[string]any{"id": convID, "uid": userID},
	)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	res3.Close()

	// Verify soft deleted — should not appear in normal list
	res4, err := app.client.Query(ctx,
		`query($uid: String!) { hub { db { conversations(
			filter: { user_id: { eq: $uid } }
		) { id } } } }`,
		map[string]any{"uid": userID},
	)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	defer res4.Close()

	var remaining []struct{ ID string `json:"id"` }
	res4.ScanData("hub.db.conversations", &remaining)
	for _, c := range remaining {
		if c.ID == convID {
			t.Fatal("deleted conversation still appears in list")
		}
	}

	// Cleanup user
	app.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_users(filter: { id: { eq: $id } }) { affected_rows } } } }`,
		map[string]any{"id": userID},
	)
}

func TestIntegration_MessagePersistence(t *testing.T) {
	skipIfNoHugr(t)
	userID := fmt.Sprintf("msg-test-%d", time.Now().UnixNano())
	app, ctx := testClient(t, userID)
	app.ensureUser(ctx, userID, "user")

	// Create conversation
	convID := fmt.Sprintf("conv-%d", time.Now().UnixNano())
	res, err := app.client.Query(ctx,
		`mutation($id: String!, $uid: String!, $title: String!, $mode: String!) {
			hub { db { insert_conversations(data: {
				id: $id, user_id: $uid, title: $title, mode: $mode
			}) { id } } }
		}`,
		map[string]any{"id": convID, "uid": userID, "title": "Msg Test", "mode": "tools"},
	)
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}
	res.Close()

	// Persist messages
	app.persistMessage(ctx, convID, "user", "hello world")
	app.persistMessage(ctx, convID, "assistant", "hello back")

	// Allow time for persist
	time.Sleep(100 * time.Millisecond)

	// Load messages
	res2, err := app.client.Query(ctx,
		`query($cid: String!) { hub { db { agent_messages(
			filter: { conversation_id: { eq: $cid } }
		) { role content } } } }`,
		map[string]any{"cid": convID},
	)
	if err != nil {
		t.Fatalf("load messages: %v", err)
	}
	defer res2.Close()
	if res2.Err() != nil {
		t.Fatalf("messages error: %v", res2.Err())
	}

	var msgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := res2.ScanData("hub.db.agent_messages", &msgs); err != nil {
		t.Fatalf("scan messages: %v", err)
	}

	if len(msgs) < 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	foundUser := false
	foundAssistant := false
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "hello world" {
			foundUser = true
		}
		if m.Role == "assistant" && m.Content == "hello back" {
			foundAssistant = true
		}
	}
	if !foundUser || !foundAssistant {
		t.Fatalf("messages not found: user=%v assistant=%v, got %+v", foundUser, foundAssistant, msgs)
	}

	// Cleanup
	app.client.Query(ctx,
		`mutation($cid: String!) { hub { db { delete_agent_messages(
			filter: { conversation_id: { eq: $cid } }
		) { affected_rows } } } }`,
		map[string]any{"cid": convID},
	)
	app.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_conversations(
			filter: { id: { eq: $id } }
		) { affected_rows } } } }`,
		map[string]any{"id": convID},
	)
	app.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_users(
			filter: { id: { eq: $id } }
		) { affected_rows } } } }`,
		map[string]any{"id": userID},
	)
}

func TestIntegration_LookupConversation(t *testing.T) {
	skipIfNoHugr(t)
	userID := fmt.Sprintf("lookup-test-%d", time.Now().UnixNano())
	app, ctx := testClient(t, userID)
	app.ensureUser(ctx, userID, "user")

	convID := fmt.Sprintf("conv-%d", time.Now().UnixNano())
	res, err := app.client.Query(ctx,
		`mutation($id: String!, $uid: String!) {
			hub { db { insert_conversations(data: {
				id: $id, user_id: $uid, title: "Lookup Test", mode: "llm", model: "test-model"
			}) { id } } }
		}`,
		map[string]any{"id": convID, "uid": userID},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res.Close()

	conv, err := app.lookupConversation(ctx, convID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if conv.ID != convID || conv.UserID != userID || conv.Mode != "llm" || conv.Model != "test-model" {
		t.Fatalf("unexpected conv: %+v", conv)
	}

	// Cleanup
	app.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_conversations(filter: { id: { eq: $id } }) { affected_rows } } } }`,
		map[string]any{"id": convID},
	)
	app.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_users(filter: { id: { eq: $id } }) { affected_rows } } } }`,
		map[string]any{"id": userID},
	)
}
