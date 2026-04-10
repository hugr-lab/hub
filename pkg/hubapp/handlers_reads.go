package hubapp

// Read-side airport-go table functions.
//
// These expose user-scoped lists that previously lived behind REST endpoints
// (conversation list, conversation messages, agent instances). They all inject
// the caller's identity via ArgFromContext and enforce ownership server-side,
// so the browser never needs to know its own user_id.
//
// They are registered as TABLE functions — the caller does:
//
//	query {
//	  hub {
//	    my_conversations(folder: "work", limit: 50) { id title mode updated_at }
//	  }
//	}

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hugr-lab/query-engine/client/app"
	"github.com/hugr-lab/query-engine/types"
)

// isNoData reports whether err is a benign "zero-rows" signal from the query
// engine's ScanData. Listing queries use this to treat empty results as an
// empty slice rather than an error.
func isNoData(err error) bool {
	return errors.Is(err, types.ErrNoData)
}

// registerReadFunctions registers the read-side table functions.
// Call from registerCatalog().
func (a *HubApp) registerReadFunctions() error {
	if err := a.registerMyConversations(); err != nil {
		return err
	}
	if err := a.registerMyConversationMessages(); err != nil {
		return err
	}
	if err := a.registerMyAgentInstances(); err != nil {
		return err
	}
	return nil
}

// ───── my_conversations ─────

func (a *HubApp) registerMyConversations() error {
	return a.mux.HandleTableFunc("default", "my_conversations", a.handleMyConversations,
		app.Arg("folder", app.String),
		app.Arg("limit", app.Int64),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.ColPK("id", app.String),
		app.Col("user_id", app.String),
		app.Col("title", app.String),
		app.ColNullable("folder", app.String),
		app.Col("mode", app.String),
		app.ColNullable("agent_type_id", app.String),
		app.ColNullable("agent_id", app.String),
		app.ColNullable("model", app.String),
		app.ColNullable("parent_id", app.String),
		app.ColNullable("branch_point_message_id", app.String),
		app.ColNullable("branch_label", app.String),
		app.Col("created_at", app.String),
		app.Col("updated_at", app.String),
		app.Desc("Conversations owned by the current user. Pass folder='' to list all, or a folder name to filter. limit=0 means no limit."),
	)
}

func (a *HubApp) handleMyConversations(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	folder := r.String("folder")
	limit := r.Int64("limit")
	if limit <= 0 {
		limit = 100
	}

	ctx := withIdentity(r.Context(), u)

	// Build filter — user_id always bound; folder optional.
	varDefs := `$uid: String!, $limit: Int!`
	filter := `user_id: { eq: $uid }`
	vars := map[string]any{"uid": u.ID, "limit": limit}
	if folder != "" {
		varDefs += `, $folder: String!`
		filter += `, folder: { eq: $folder }`
		vars["folder"] = folder
	}

	gql := fmt.Sprintf(`query(%s) { hub { db { conversations(
		filter: { %s }
		order_by: [{ field: "updated_at", direction: DESC }]
		limit: $limit
	) {
		id user_id title folder mode agent_type_id agent_id model
		parent_id branch_point_message_id branch_label
		created_at updated_at
	} } } }`, varDefs, filter)

	res, err := a.client.Query(ctx, gql, vars)
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("list conversations: %w", res.Err())
	}

	var convs []struct {
		ID                   string  `json:"id"`
		UserID               string  `json:"user_id"`
		Title                string  `json:"title"`
		Folder               *string `json:"folder"`
		Mode                 string  `json:"mode"`
		AgentTypeID          *string `json:"agent_type_id"`
		AgentID              *string `json:"agent_id"`
		Model                *string `json:"model"`
		ParentID             *string `json:"parent_id"`
		BranchPointMessageID *string `json:"branch_point_message_id"`
		BranchLabel          *string `json:"branch_label"`
		CreatedAt            string  `json:"created_at"`
		UpdatedAt            string  `json:"updated_at"`
	}
	if err := res.ScanData("hub.db.conversations", &convs); err != nil && !isNoData(err) {
		return fmt.Errorf("scan conversations: %w", err)
	}

	for _, c := range convs {
		if err := w.Append(
			c.ID, c.UserID, c.Title,
			strPtrOrNil(c.Folder),
			c.Mode,
			strPtrOrNil(c.AgentTypeID),
			strPtrOrNil(c.AgentID),
			strPtrOrNil(c.Model),
			strPtrOrNil(c.ParentID),
			strPtrOrNil(c.BranchPointMessageID),
			strPtrOrNil(c.BranchLabel),
			c.CreatedAt, c.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

// ───── my_conversation_messages ─────

func (a *HubApp) registerMyConversationMessages() error {
	return a.mux.HandleTableFunc("default", "my_conversation_messages", a.handleMyConversationMessages,
		app.Arg("conversation_id", app.String),
		app.Arg("limit", app.Int64),
		app.Arg("before", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.ColPK("id", app.String),
		app.Col("conversation_id", app.String),
		app.Col("role", app.String),
		app.Col("content", app.String),
		app.ColNullable("tool_calls", app.JSON),
		app.ColNullable("tool_call_id", app.String),
		app.ColNullable("tokens_used", app.Int64),
		app.ColNullable("model", app.String),
		app.Col("is_summary", app.Boolean),
		app.ColNullable("summarized_by", app.String),
		app.Col("created_at", app.String),
		app.Desc("Messages for a conversation owned by the current user. Ordered by created_at ASC. Pass before='<msg_id>' for keyset pagination, limit=0 for default of 200."),
	)
}

func (a *HubApp) handleMyConversationMessages(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	convID := r.String("conversation_id")
	if convID == "" {
		return fmt.Errorf("conversation_id is required")
	}
	limit := r.Int64("limit")
	if limit <= 0 {
		limit = 200
	}
	before := r.String("before")

	ctx := withIdentity(r.Context(), u)

	// Ownership check — strictly user-scoped. Only engine-internal management
	// auth bypasses (no user identity at all); OIDC roles are not recognized
	// as privileged here because role names are deployment-specific.
	if u.AuthType != "management" {
		if err := a.verifyConversationOwner(ctx, convID, u.ID); err != nil {
			return fmt.Errorf("conversation not accessible: %w", err)
		}
	}

	varDefs := `$cid: String!, $limit: Int!`
	filter := `conversation_id: { eq: $cid }`
	vars := map[string]any{"cid": convID, "limit": limit}
	if before != "" {
		varDefs += `, $before: String!`
		filter += `, id: { lt: $before }`
		vars["before"] = before
	}

	gql := fmt.Sprintf(`query(%s) { hub { db { agent_messages(
		filter: { %s }
		order_by: [{ field: "created_at", direction: ASC }]
		limit: $limit
	) {
		id conversation_id role content tool_calls tool_call_id tokens_used model
		is_summary summarized_by created_at
	} } } }`, varDefs, filter)

	res, err := a.client.Query(ctx, gql, vars)
	if err != nil {
		return fmt.Errorf("load messages: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("load messages: %w", res.Err())
	}

	var msgs []struct {
		ID             string  `json:"id"`
		ConversationID string  `json:"conversation_id"`
		Role           string  `json:"role"`
		Content        string  `json:"content"`
		ToolCalls      any     `json:"tool_calls"`
		ToolCallID     *string `json:"tool_call_id"`
		TokensUsed     *int64  `json:"tokens_used"`
		Model          *string `json:"model"`
		IsSummary      bool    `json:"is_summary"`
		SummarizedBy   *string `json:"summarized_by"`
		CreatedAt      string  `json:"created_at"`
	}
	if err := res.ScanData("hub.db.agent_messages", &msgs); err != nil && !isNoData(err) {
		return fmt.Errorf("scan messages: %w", err)
	}

	for _, m := range msgs {
		var toolCallsJSON any
		if m.ToolCalls != nil {
			// The field comes back already decoded from the JSON column — re-marshal
			// so the JSON-typed column receives a string payload.
			toolCallsJSON = jsonMarshal(m.ToolCalls)
		}
		if err := w.Append(
			m.ID, m.ConversationID, m.Role, m.Content,
			toolCallsJSON,
			strPtrOrNil(m.ToolCallID),
			int64PtrOrNil(m.TokensUsed),
			strPtrOrNil(m.Model),
			m.IsSummary,
			strPtrOrNil(m.SummarizedBy),
			m.CreatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

// ───── my_agent_instances ─────

func (a *HubApp) registerMyAgentInstances() error {
	return a.mux.HandleTableFunc("default", "my_agent_instances", a.handleMyAgentInstances,
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.ColPK("id", app.String),
		app.Col("agent_type_id", app.String),
		app.Col("display_name", app.String),
		app.Col("hugr_role", app.String),
		app.Col("status", app.String),
		app.Col("connected", app.Boolean),
		app.Col("access_role", app.String),
		app.Desc("Agent instances the current user has access to, enriched with runtime status and WebSocket connection state. Admins see all agents."),
	)
}

func (a *HubApp) handleMyAgentInstances(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)

	type row struct {
		id, agentTypeID, displayName, hugrRole, accessRole string
	}
	var rows []row

	// Only engine-internal management auth lists everything. Everyone else —
	// including OIDC users whose deployment calls them "admin" — goes through
	// hub.db.user_agents grants. Cross-user listing for the admin panel is
	// done via direct `hub.db.agents` queries that Hugr RBAC gates.
	if u.AuthType == "management" {
		res, err := a.client.Query(ctx,
			`{ hub { db { agents {
				id agent_type_id display_name hugr_role
			} } } }`, nil,
		)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		defer res.Close()
		if res.Err() != nil {
			return fmt.Errorf("list agents: %w", res.Err())
		}
		var agents []struct {
			ID          string `json:"id"`
			AgentTypeID string `json:"agent_type_id"`
			DisplayName string `json:"display_name"`
			HugrRole    string `json:"hugr_role"`
		}
		if err := res.ScanData("hub.db.agents", &agents); err != nil && !isNoData(err) {
			return fmt.Errorf("scan agents: %w", err)
		}
		for _, ag := range agents {
			rows = append(rows, row{ag.ID, ag.AgentTypeID, ag.DisplayName, ag.HugrRole, "admin"})
		}
	} else {
		res, err := a.client.Query(ctx,
			`query($uid: String!) { hub { db { user_agents(
				filter: { user_id: { eq: $uid } }
			) { role agent { id agent_type_id display_name hugr_role } } } } }`,
			map[string]any{"uid": u.ID},
		)
		if err != nil {
			return fmt.Errorf("list user_agents: %w", err)
		}
		defer res.Close()
		if res.Err() != nil {
			return fmt.Errorf("list user_agents: %w", res.Err())
		}
		var access []struct {
			Role  string `json:"role"`
			Agent struct {
				ID          string `json:"id"`
				AgentTypeID string `json:"agent_type_id"`
				DisplayName string `json:"display_name"`
				HugrRole    string `json:"hugr_role"`
			} `json:"agent"`
		}
		if err := res.ScanData("hub.db.user_agents", &access); err != nil && !isNoData(err) {
			return fmt.Errorf("scan user_agents: %w", err)
		}
		for _, ua := range access {
			rows = append(rows, row{ua.Agent.ID, ua.Agent.AgentTypeID, ua.Agent.DisplayName, ua.Agent.HugrRole, ua.Role})
		}
	}

	for _, rr := range rows {
		status := "stopped"
		if a.agentMgr != nil {
			st := a.agentMgr.AgentStatus(rr.id)
			if st.Status != "" {
				status = st.Status
			}
		}
		connected := false
		if a.agentConnMgr != nil {
			connected = a.agentConnMgr.IsConnected(rr.id)
		}
		if err := w.Append(
			rr.id, rr.agentTypeID, rr.displayName, rr.hugrRole,
			status, connected, rr.accessRole,
		); err != nil {
			return err
		}
	}
	return nil
}

// ───── small helpers ─────

func strPtrOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func int64PtrOrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// jsonMarshal is a silent JSON encoder — returns "null" on error so table-function
// rows never fail on marshaling a foreign JSON payload.
func jsonMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
