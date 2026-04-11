package hubapp

// Airport-go mutating functions for the conversation write path.
//
// CRUD + action coverage:
//   create_conversation   — new chat row, auto-generates id, seeds user FK
//   rename_conversation   — owner-only title edit
//   delete_conversation   — owner-only soft delete
//   move_conversation     — owner-only folder change (nullable)
//   branch_conversation   — owner-only child conversation with depth<=3
//   summarize_conversation — LLM-backed message summarization + junction table
//
// All functions inject the caller identity via ArgFromContext. Optional args
// use v0.3.24 InputStruct() / FieldNullable() — no empty-string sentinels.
// Returns are v0.3.24 Struct() where it saves the frontend a round-trip.

import (
	"fmt"
	"time"

	"github.com/hugr-lab/hub/pkg/llmrouter"
	"github.com/hugr-lab/query-engine/client/app"
)

// ───── SDL type builders ─────

// conversationHandleType is the return type for create/branch mutations —
// enough for the frontend to insert the new row into the sidebar without a
// follow-up fetch.
func conversationHandleType() app.Type {
	return app.Struct("conversation_handle").
		Desc("Reference to a conversation returned by create/branch mutations.").
		Field("id", app.String).
		Field("title", app.String).
		Field("mode", app.String).
		FieldNullable("parent_id", app.String).
		FieldNullable("branch_point_message_id", app.String).
		AsType()
}

// summarizeResultType is the return type for summarize_conversation — the
// summary message id plus metadata so the frontend can patch the existing
// message list without re-querying.
func summarizeResultType() app.Type {
	return app.Struct("summarize_result").
		Desc("Result of summarize_conversation — summary message id, text, and count of summarized originals.").
		Field("id", app.String).
		Field("summary_text", app.String).
		Field("message_count", app.Int64).
		AsType()
}

// createConversationInputType is the InputStruct for create_conversation —
// lets the frontend omit optional fields cleanly instead of passing empty strings.
func createConversationInputType() app.Type {
	return app.InputStruct("create_conversation_input").
		Field("mode", app.String).
		FieldNullable("title", app.String).
		FieldNullable("folder", app.String).
		FieldNullable("model", app.String).
		FieldNullable("agent_id", app.String).
		FieldNullable("agent_type_id", app.String).
		AsType()
}

// branchConversationInputType is the InputStruct for branch_conversation.
// Nullable fields drop the empty-string sentinel workaround.
func branchConversationInputType() app.Type {
	return app.InputStruct("branch_conversation_input").
		Field("parent_id", app.String).
		Field("branch_point_message_id", app.String).
		FieldNullable("branch_label", app.String).
		FieldNullable("title", app.String).
		AsType()
}

// registerConversationMutations registers every write-path conversation function.
func (a *HubApp) registerConversationMutations() error {
	// create_conversation
	if err := a.mux.HandleFunc("default", "create_conversation", a.handleCreateConversation,
		app.Arg("input", createConversationInputType()),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(conversationHandleType()),
		app.Mutation(),
		app.Desc("Create a new conversation owned by the caller. Auto-generates id, seeds user row if missing, optionally auto-resolves agent_type_id from agent_id."),
	); err != nil {
		return err
	}

	// rename_conversation
	if err := a.mux.HandleFunc("default", "rename_conversation", a.handleRenameConversation,
		app.Arg("id", app.String),
		app.Arg("title", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(app.String),
		app.Mutation(),
		app.Desc("Rename a conversation. Returns the conversation id. Owner-only."),
	); err != nil {
		return err
	}

	// delete_conversation
	if err := a.mux.HandleFunc("default", "delete_conversation", a.handleDeleteConversation,
		app.Arg("id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(app.String),
		app.Mutation(),
		app.Desc("Soft-delete a conversation (sets deleted_at = NOW()). Returns the id. Owner-only."),
	); err != nil {
		return err
	}

	// move_conversation
	if err := a.mux.HandleFunc("default", "move_conversation", a.handleMoveConversation,
		app.Arg("id", app.String),
		app.Arg("folder", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(app.String),
		app.Mutation(),
		app.Desc("Move a conversation to a folder. Pass folder='' to clear. Returns the id. Owner-only."),
	); err != nil {
		return err
	}

	// branch_conversation
	if err := a.mux.HandleFunc("default", "branch_conversation", a.handleBranchConversation,
		app.Arg("input", branchConversationInputType()),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(conversationHandleType()),
		app.Mutation(),
		app.Desc("Create a branch (child conversation) from a parent message. Validates depth <= 3 and ownership of parent. Returns the new conversation handle."),
	); err != nil {
		return err
	}

	// summarize_conversation
	if err := a.mux.HandleFunc("default", "summarize_conversation", a.handleSummarizeConversation,
		app.Arg("conversation_id", app.String),
		app.Arg("up_to_message_id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(summarizeResultType()),
		app.Mutation(),
		app.Desc("Summarize messages in a conversation up to a target message. Calls LLM, persists summary with nested summary_items junction rows, marks originals as summarized. Returns the summary message id, text, and count."),
	); err != nil {
		return err
	}

	return nil
}

// ───── handlers ─────

// handleCreateConversation creates a new conversation row owned by the caller.
func (a *HubApp) handleCreateConversation(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}

	var input struct {
		Mode        string  `json:"mode"`
		Title       *string `json:"title"`
		Folder      *string `json:"folder"`
		Model       *string `json:"model"`
		AgentID     *string `json:"agent_id"`
		AgentTypeID *string `json:"agent_type_id"`
	}
	if err := r.JSON("input", &input); err != nil {
		return fmt.Errorf("parse input: %w", err)
	}
	if input.Mode == "" {
		input.Mode = "tools"
	}
	title := "New Chat"
	if input.Title != nil && *input.Title != "" {
		title = *input.Title
	}

	ctx := withIdentity(r.Context(), u)

	// Seed user row (FK for conversations.user_id).
	a.ensureUser(ctx, u.ID, u.Role)

	// Auto-resolve agent_type_id from agent_id if caller omitted it.
	if input.AgentID != nil && *input.AgentID != "" && (input.AgentTypeID == nil || *input.AgentTypeID == "") {
		agentRes, err := a.client.Query(ctx,
			`query($id: String!) { hub { db { agents(filter: { id: { eq: $id } } limit: 1) { agent_type_id } } } }`,
			map[string]any{"id": *input.AgentID},
		)
		if err == nil {
			if agentRes.Err() == nil {
				var agents []struct {
					AgentTypeID string `json:"agent_type_id"`
				}
				if agentRes.ScanData("hub.db.agents", &agents) == nil && len(agents) > 0 && agents[0].AgentTypeID != "" {
					v := agents[0].AgentTypeID
					input.AgentTypeID = &v
				}
			}
			agentRes.Close()
		}
	}

	convID := fmt.Sprintf("conv-%d", time.Now().UnixNano())

	varDefs := `$id: String!, $uid: String!, $title: String!, $mode: String!`
	dataFields := `id: $id, user_id: $uid, title: $title, mode: $mode`
	vars := map[string]any{"id": convID, "uid": u.ID, "title": title, "mode": input.Mode}

	if input.Folder != nil && *input.Folder != "" {
		varDefs += `, $folder: String`
		dataFields += `, folder: $folder`
		vars["folder"] = *input.Folder
	}
	if input.Model != nil && *input.Model != "" {
		varDefs += `, $model: String`
		dataFields += `, model: $model`
		vars["model"] = *input.Model
	}
	if input.AgentID != nil && *input.AgentID != "" {
		varDefs += `, $aid: String`
		dataFields += `, agent_id: $aid`
		vars["aid"] = *input.AgentID
	}
	if input.AgentTypeID != nil && *input.AgentTypeID != "" {
		varDefs += `, $atid: String`
		dataFields += `, agent_type_id: $atid`
		vars["atid"] = *input.AgentTypeID
	}

	gql := fmt.Sprintf(`mutation(%s) {
		hub { db { insert_conversations(data: { %s }) { id } } }
	}`, varDefs, dataFields)

	res, err := a.client.Query(ctx, gql, vars)
	if err != nil {
		return fmt.Errorf("create conversation: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("create conversation: %w", res.Err())
	}

	a.logger.Info("conversation created via mutation", "id", convID, "by", u.ID, "mode", input.Mode)
	return w.SetJSON(map[string]any{
		"id":                      convID,
		"title":                   title,
		"mode":                    input.Mode,
		"parent_id":               nil,
		"branch_point_message_id": nil,
	})
}

// handleRenameConversation updates the title — owner required.
func (a *HubApp) handleRenameConversation(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	id := r.String("id")
	title := r.String("title")
	if id == "" || title == "" {
		return fmt.Errorf("id and title are required")
	}

	ctx := withIdentity(r.Context(), u)
	if err := a.verifyConversationOwner(ctx, id, u.ID); err != nil {
		return fmt.Errorf("conversation not accessible: %w", err)
	}

	res, err := a.client.Query(ctx,
		`mutation($id: String!, $title: String!) {
			hub { db { update_conversations(
				filter: { id: { eq: $id } }
				data: { title: $title }
			) { affected_rows } } }
		}`,
		map[string]any{"id": id, "title": title},
	)
	if err != nil {
		return fmt.Errorf("rename conversation: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("rename conversation: %w", res.Err())
	}
	return w.Set(id)
}

// handleDeleteConversation soft-deletes the conversation — owner required.
func (a *HubApp) handleDeleteConversation(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	id := r.String("id")
	if id == "" {
		return fmt.Errorf("id is required")
	}

	ctx := withIdentity(r.Context(), u)
	if err := a.verifyConversationOwner(ctx, id, u.ID); err != nil {
		return fmt.Errorf("conversation not accessible: %w", err)
	}

	res, err := a.client.Query(ctx,
		`mutation($id: String!) {
			hub { db { delete_conversations(
				filter: { id: { eq: $id } }
			) { affected_rows } } }
		}`,
		map[string]any{"id": id},
	)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("delete conversation: %w", res.Err())
	}
	a.logger.Info("conversation deleted via mutation", "id", id, "by", u.ID)
	return w.Set(id)
}

// handleMoveConversation updates the folder — owner required. folder="" clears it.
func (a *HubApp) handleMoveConversation(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	id := r.String("id")
	if id == "" {
		return fmt.Errorf("id is required")
	}
	folder := r.String("folder")

	ctx := withIdentity(r.Context(), u)
	if err := a.verifyConversationOwner(ctx, id, u.ID); err != nil {
		return fmt.Errorf("conversation not accessible: %w", err)
	}

	var gql string
	vars := map[string]any{"id": id}
	if folder == "" {
		gql = `mutation($id: String!) {
			hub { db { update_conversations(
				filter: { id: { eq: $id } }
				data: { folder: null }
			) { affected_rows } } }
		}`
	} else {
		gql = `mutation($id: String!, $folder: String!) {
			hub { db { update_conversations(
				filter: { id: { eq: $id } }
				data: { folder: $folder }
			) { affected_rows } } }
		}`
		vars["folder"] = folder
	}

	res, err := a.client.Query(ctx, gql, vars)
	if err != nil {
		return fmt.Errorf("move conversation: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("move conversation: %w", res.Err())
	}
	return w.Set(id)
}

// handleBranchConversation creates a child conversation from a parent message.
func (a *HubApp) handleBranchConversation(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}

	var input struct {
		ParentID             string  `json:"parent_id"`
		BranchPointMessageID string  `json:"branch_point_message_id"`
		BranchLabel          *string `json:"branch_label"`
		Title                *string `json:"title"`
	}
	if err := r.JSON("input", &input); err != nil {
		return fmt.Errorf("parse input: %w", err)
	}
	if input.ParentID == "" || input.BranchPointMessageID == "" {
		return fmt.Errorf("parent_id and branch_point_message_id are required")
	}

	title := "New Thread"
	if input.Title != nil && *input.Title != "" {
		title = *input.Title
	}

	ctx := withIdentity(r.Context(), u)

	if err := a.verifyConversationOwner(ctx, input.ParentID, u.ID); err != nil {
		return fmt.Errorf("parent conversation not accessible: %w", err)
	}

	depth, err := a.getConversationDepth(ctx, input.ParentID)
	if err != nil {
		return fmt.Errorf("check depth: %w", err)
	}
	if depth >= 3 {
		return fmt.Errorf("maximum branch depth (3) reached")
	}

	// Fetch parent fields to copy.
	parentRes, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { conversations(
			filter: { id: { eq: $id } } limit: 1
		) { mode agent_type_id agent_id model } } } }`,
		map[string]any{"id": input.ParentID},
	)
	if err != nil {
		return fmt.Errorf("lookup parent: %w", err)
	}
	defer parentRes.Close()
	if parentRes.Err() != nil {
		return fmt.Errorf("lookup parent: %w", parentRes.Err())
	}
	var parents []struct {
		Mode        string `json:"mode"`
		AgentTypeID string `json:"agent_type_id"`
		AgentID     string `json:"agent_id"`
		Model       string `json:"model"`
	}
	if err := parentRes.ScanData("hub.db.conversations", &parents); err != nil || len(parents) == 0 {
		return fmt.Errorf("parent conversation not found")
	}
	parent := parents[0]

	convID := fmt.Sprintf("conv-%d", time.Now().UnixNano())
	vars := map[string]any{
		"id": convID, "uid": u.ID, "title": title,
		"mode": parent.Mode, "parent_id": input.ParentID, "branch_msg": input.BranchPointMessageID,
	}
	varDefs := `$id: String!, $uid: String!, $title: String!, $mode: String!, $parent_id: String!, $branch_msg: String!`
	dataFields := `id: $id, user_id: $uid, title: $title, mode: $mode, parent_id: $parent_id, branch_point_message_id: $branch_msg`
	if input.BranchLabel != nil && *input.BranchLabel != "" {
		varDefs += `, $label: String`
		dataFields += `, branch_label: $label`
		vars["label"] = *input.BranchLabel
	}
	if parent.AgentTypeID != "" {
		varDefs += `, $atid: String`
		dataFields += `, agent_type_id: $atid`
		vars["atid"] = parent.AgentTypeID
	}
	if parent.AgentID != "" {
		varDefs += `, $aid: String`
		dataFields += `, agent_id: $aid`
		vars["aid"] = parent.AgentID
	}
	if parent.Model != "" {
		varDefs += `, $model: String`
		dataFields += `, model: $model`
		vars["model"] = parent.Model
	}

	gql := fmt.Sprintf(`mutation(%s) {
		hub { db { insert_conversations(data: { %s }) { id } } }
	}`, varDefs, dataFields)

	res, err := a.client.Query(ctx, gql, vars)
	if err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("create branch: %w", res.Err())
	}

	a.logger.Info("conversation branched via mutation", "parent", input.ParentID, "branch", convID, "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":                      convID,
		"title":                   title,
		"mode":                    parent.Mode,
		"parent_id":               input.ParentID,
		"branch_point_message_id": input.BranchPointMessageID,
	})
}

// handleSummarizeConversation generates an LLM summary of messages up to a target
// and persists it atomically with the junction rows.
func (a *HubApp) handleSummarizeConversation(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	convID := r.String("conversation_id")
	upToMsgID := r.String("up_to_message_id")
	if convID == "" || upToMsgID == "" {
		return fmt.Errorf("conversation_id and up_to_message_id are required")
	}
	if a.llmRouter == nil {
		return fmt.Errorf("LLM not configured — summarization unavailable")
	}

	ctx := withIdentity(r.Context(), u)

	if err := a.verifyConversationOwner(ctx, convID, u.ID); err != nil {
		return fmt.Errorf("conversation not accessible: %w", err)
	}

	// Fetch all messages in the conversation (ordered by created_at ASC).
	res, err := a.client.Query(ctx,
		`query($cid: String!) { hub { db { agent_messages(
			filter: { conversation_id: { eq: $cid } }
			order_by: [{field: "created_at", direction: ASC}]
		) { id role content created_at } } } }`,
		map[string]any{"cid": convID},
	)
	if err != nil {
		return fmt.Errorf("fetch messages: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("fetch messages: %w", res.Err())
	}

	var allMsgs []struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := res.ScanData("hub.db.agent_messages", &allMsgs); err != nil || len(allMsgs) == 0 {
		return fmt.Errorf("no messages to summarize")
	}

	// Collect messages up to (but not including) the target.
	var history string
	var msgIDs []string
	for _, m := range allMsgs {
		if m.ID == upToMsgID {
			break
		}
		history += fmt.Sprintf("[%s]: %s\n", m.Role, m.Content)
		msgIDs = append(msgIDs, m.ID)
	}
	if len(msgIDs) == 0 {
		return fmt.Errorf("no messages before target to summarize")
	}

	// Call LLM.
	resp, err := a.llmRouter.Complete(ctx, llmrouter.CompletionRequest{
		Messages: []llmrouter.Message{
			{Role: "system", Content: "Summarize the following conversation concisely, preserving key facts, decisions, and context. Reply with ONLY the summary, nothing else."},
			{Role: "user", Content: history},
		},
	})
	if err != nil {
		return fmt.Errorf("generate summary: %w", err)
	}

	// One atomic Hugr mutation: nested insert into message_summary_items via
	// @field_references reverse relation, plus pointer update on originals.
	summaryID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	items := make([]map[string]any, len(msgIDs))
	for i, mid := range msgIDs {
		items[i] = map[string]any{
			"original_message_id": mid,
			"position":            i,
		}
	}

	mRes, err := a.client.Query(ctx,
		`mutation($id: String!, $cid: String!, $content: String!, $items: [hub_db_message_summary_items_mut_input_data!]!, $ids: [String!]!) {
			hub { db {
				insert_agent_messages(data: {
					id: $id, conversation_id: $cid, role: "system", content: $content,
					is_summary: true, summary_items: $items
				}) { id }
				update_agent_messages(
					filter: { id: { in: $ids } }
					data: { summarized_by: $id }
				) { affected_rows }
			} }
		}`,
		map[string]any{
			"id": summaryID, "cid": convID, "content": resp.Content,
			"items": items, "ids": msgIDs,
		},
	)
	if err != nil {
		return fmt.Errorf("persist summary: %w", err)
	}
	defer mRes.Close()
	if mRes.Err() != nil {
		return fmt.Errorf("persist summary: %w", mRes.Err())
	}

	a.logger.Info("conversation summarized via mutation", "conversation", convID, "summary", summaryID, "messages", len(msgIDs), "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":            summaryID,
		"summary_text":  resp.Content,
		"message_count": int64(len(msgIDs)),
	})
}
