package hubapp

// Management plane of the HB5 gateway (spec-hub-gateway §2): chat/project
// thread organization + admin access management, registered as airport-go
// functions. Called through /hugr with the caller's bearer; identity arrives
// as hidden ArgFromContext args (the my_agent_instances pattern) and every
// handler enforces ownership server-side with the SERVICE principal — no
// dependency on hub.db RLS (user-tier RLS is the HB5.1 follow-up).
//
// Chats reference agent sessions LAZILY: create_chat inserts the thread row
// with root_session_id NULL; the first transport-plane message binds it
// (spec-hub-gateway §3) — the function plane has no user bearer to open a
// session with, and the agent may be down.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/query-engine/client/app"
)

const chatTitleDefault = "New Chat"

// keysetMargin bounds the extra rows my_chats over-fetches to skip the
// boundary group of a keyset page (equal last_active_at timestamps). More
// than keysetMargin chats bumped in the same microsecond is not a real
// workload; a page may come back short in that pathology, never duplicated.
const keysetMargin = 100

// chatProjection is the single column list every chat read uses.
const chatProjection = `id project_id user_id agent_id title root_session_id created_at updated_at last_active_at archived`

// chatRow mirrors hub.db.chats. Timestamps stay RFC3339 strings end-to-end —
// my_chats hands last_active_at/id back to the client as the opaque-enough
// keyset page key.
type chatRow struct {
	ID            string     `json:"id"`
	ProjectID     *string    `json:"project_id"`
	UserID        string     `json:"user_id"`
	AgentID       string     `json:"agent_id"`
	Title         string     `json:"title"`
	RootSessionID *string    `json:"root_session_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastActiveAt  time.Time `json:"last_active_at"`
	// Archived is a plain boolean, NOT an archived_at timestamp: hugr coerces a
	// null Timestamp mutation value to the zero time instead of SQL NULL
	// (query-engine ask #9), so "unarchive" could never be expressed.
	Archived bool `json:"archived"`
}

// fmtTime renders row timestamps for the string-typed function columns (the
// Flight scan yields time.Time; strings would fail the scan).
func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// likeEscape neutralizes LIKE metacharacters in user input so q is a literal
// substring match ("50%" must not match everything).
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func newChatID() string    { return "ch-" + randHex(9) }
func newProjectID() string { return "prj-" + randHex(9) }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err)) // never happens on a sane OS
	}
	return hex.EncodeToString(b)
}

// identityArgs is the standard hidden-arg quartet every user-facing function
// declares (see userFromArgs).
func identityArgs() []app.Option {
	return []app.Option{
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
	}
}

// registerChatFunctions wires the chat/project/access management functions.
// Called from registerCatalog().
func (a *HubApp) registerChatFunctions() error {
	chatCols := []app.Option{
		app.ColPK("id", app.String),
		app.Col("project_id", app.String),
		app.Col("user_id", app.String),
		app.Col("agent_id", app.String),
		app.Col("title", app.String),
		app.Col("root_session_id", app.String),
		app.Col("created_at", app.String),
		app.Col("updated_at", app.String),
		app.Col("last_active_at", app.String),
		app.Col("archived", app.Boolean),
	}

	// ── my_chats — keyset-paginated thread list ──
	opts := append([]app.Option{
		app.Arg("limit", app.Int32),
		app.Arg("before_active_at", app.String),
		app.Arg("before_id", app.String),
		app.Arg("project_id", app.String),
		app.Arg("agent_id", app.String),
		app.Arg("q", app.String),
		app.Arg("archived", app.Boolean),
	}, identityArgs()...)
	opts = append(opts, chatCols...)
	opts = append(opts, app.Desc("The caller's chat threads, ordered last_active_at DESC, id DESC (keyset pagination: pass the last row's last_active_at/id back as before_active_at/before_id for the next page). All args are required by the framework: limit<=0 means the default 30 (max 100); empty strings mean no filter; q matches the title (substring); archived=false lists live chats, true the archived ones."))
	if err := a.mux.HandleTableFunc("default", "my_chats", a.handleMyChats, opts...); err != nil {
		return err
	}

	// ── create_chat ──
	if err := a.mux.HandleFunc("default", "create_chat", a.handleCreateChat,
		append(identityArgs(),
			app.Arg("agent_id", app.String),
			app.Arg("project_id", app.String),
			app.Arg("title", app.String),
			app.Return(chatStructType()),
			app.Mutation(),
			app.Desc("Create a chat thread with an agent the caller has access to. project_id='' for no project, title='' for the default. The root session binds lazily on the first transport-plane message — creation succeeds even when the agent is down."),
		)...); err != nil {
		return err
	}

	// ── update_chat ──
	if err := a.mux.HandleFunc("default", "update_chat", a.handleUpdateChat,
		append(identityArgs(),
			app.Arg("id", app.String),
			app.Arg("title", app.String),
			app.Arg("project_id", app.String),
			app.Arg("archived", app.String),
			app.Return(chatStructType()),
			app.Mutation(),
			app.Desc("Rename / move / archive the caller's chat. Empty string args are left unchanged; project_id='none' clears the project; archived accepts 'true'/'false' ('' = unchanged)."),
		)...); err != nil {
		return err
	}

	// ── delete_chat ──
	if err := a.mux.HandleFunc("default", "delete_chat", a.handleDeleteChat,
		append(identityArgs(),
			app.Arg("id", app.String),
			app.Return(deletedType()),
			app.Mutation(),
			app.Desc("Delete the caller's chat thread. The agent-side root session is left to hugen's idle lifecycle; the transcript in the Agent DB is untouched."),
		)...); err != nil {
		return err
	}

	// ── my_projects ──
	projOpts := append(identityArgs(),
		app.ColPK("id", app.String),
		app.Col("owner_user_id", app.String),
		app.Col("name", app.String),
		app.Col("created_at", app.String),
		app.Col("updated_at", app.String),
		app.Desc("The caller's projects (chat groupings), newest first."),
	)
	if err := a.mux.HandleTableFunc("default", "my_projects", a.handleMyProjects, projOpts...); err != nil {
		return err
	}

	// ── project CRUD ──
	if err := a.mux.HandleFunc("default", "create_project", a.handleCreateProject,
		append(identityArgs(),
			app.Arg("name", app.String),
			app.Return(projectStructType()),
			app.Mutation(),
			app.Desc("Create a project owned by the caller."),
		)...); err != nil {
		return err
	}
	if err := a.mux.HandleFunc("default", "update_project", a.handleUpdateProject,
		append(identityArgs(),
			app.Arg("id", app.String),
			app.Arg("name", app.String),
			app.Return(projectStructType()),
			app.Mutation(),
			app.Desc("Rename the caller's project."),
		)...); err != nil {
		return err
	}
	if err := a.mux.HandleFunc("default", "delete_project", a.handleDeleteProject,
		append(identityArgs(),
			app.Arg("id", app.String),
			app.Return(deletedType()),
			app.Mutation(),
			app.Desc("Delete the caller's project; its chats drop to no-project (FK ON DELETE SET NULL)."),
		)...); err != nil {
		return err
	}

	// ── access management (admin) ──
	// NOTE: the grant's access role arg is `access_role` — `role` is the hidden
	// caller-identity context arg (same collision create_agent documents).
	if err := a.mux.HandleFunc("default", "grant_agent_access", a.handleGrantAgentAccess,
		append(identityArgs(),
			app.Arg("user_id_grant", app.String),
			app.Arg("agent_id", app.String),
			app.Arg("access_role", app.String),
			app.Return(accessStructType()),
			app.Mutation(),
			app.Desc("Grant a user access to an agent (upsert into user_agents). access_role: 'member' (default when '') or 'owner'. Admin only."),
		)...); err != nil {
		return err
	}
	if err := a.mux.HandleFunc("default", "revoke_agent_access", a.handleRevokeAgentAccess,
		append(identityArgs(),
			app.Arg("user_id_grant", app.String),
			app.Arg("agent_id", app.String),
			app.Return(deletedType()),
			app.Mutation(),
			app.Desc("Revoke a user's access to an agent. Transport-plane calls re-check the grant, so revocation bites immediately. Admin only."),
		)...); err != nil {
		return err
	}
	accessOpts := append(identityArgs(),
		app.Arg("agent_id", app.String),
		app.ColPK("user_id_grant", app.String),
		app.Col("user_name", app.String),
		app.Col("access_role", app.String),
		app.Col("created_at", app.String),
		app.Desc("List the users granted access to an agent (user_name from the users registry; a stub grant shows the id until first login). Admin only."),
	)
	return a.mux.HandleTableFunc("default", "agent_access", a.handleAgentAccess, accessOpts...)
}

func chatStructType() app.Type {
	return app.Struct("chat").
		Desc("A chat thread (platform row; the transcript lives in the Agent DB).").
		Field("id", app.String).
		Field("project_id", app.String).
		Field("user_id", app.String).
		Field("agent_id", app.String).
		Field("title", app.String).
		Field("root_session_id", app.String).
		Field("created_at", app.String).
		Field("updated_at", app.String).
		Field("last_active_at", app.String).
		Field("archived", app.Boolean).
		AsType()
}

func projectStructType() app.Type {
	return app.Struct("project").
		Desc("A project — the caller's grouping of chats.").
		Field("id", app.String).
		Field("owner_user_id", app.String).
		Field("name", app.String).
		AsType()
}

func accessStructType() app.Type {
	return app.Struct("agent_access_grant").
		Desc("A user↔agent access grant (user_agents row).").
		Field("user_id", app.String).
		Field("agent_id", app.String).
		Field("access_role", app.String).
		AsType()
}

func deletedType() app.Type {
	return app.Struct("deleted_row").
		Desc("Deletion acknowledgement.").
		Field("id", app.String).
		Field("deleted", app.Boolean).
		AsType()
}

// ───────────────────────── chats ─────────────────────────

func (a *HubApp) handleMyChats(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := r.Context()

	limit := int(r.Int32("limit"))
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}

	filter := map[string]any{
		"user_id":  map[string]any{"eq": u.ID},
		"archived": map[string]any{"eq": r.Bool("archived")},
	}
	if v := strings.TrimSpace(r.String("project_id")); v != "" {
		filter["project_id"] = map[string]any{"eq": v}
	}
	if v := strings.TrimSpace(r.String("agent_id")); v != "" {
		filter["agent_id"] = map[string]any{"eq": v}
	}
	if v := strings.TrimSpace(r.String("q")); v != "" {
		filter["title"] = map[string]any{"ilike": "%" + likeEscape(v) + "%"}
	}
	// Keyset page: everything strictly after (before_active_at, before_id) in
	// (last_active_at DESC, id DESC) order. hugr's StringFilter has no ordering
	// ops, so the id tie-break cannot live in the SQL filter: fetch `lte` the
	// boundary time with a margin and skip the at-or-before-boundary rows here.
	bt := strings.TrimSpace(r.String("before_active_at"))
	bi := strings.TrimSpace(r.String("before_id"))
	var btT time.Time
	fetchLimit := limit
	if bi != "" && bt == "" {
		return errors.New("before_id requires before_active_at (pass BOTH fields of the last row)")
	}
	if bt != "" {
		var err error
		btT, err = time.Parse(time.RFC3339Nano, bt)
		if err != nil {
			return fmt.Errorf("before_active_at %q is not RFC3339: %w", bt, err)
		}
		if bi != "" {
			filter["last_active_at"] = map[string]any{"lte": bt}
			fetchLimit = limit + keysetMargin
		} else {
			filter["last_active_at"] = map[string]any{"lt": bt}
		}
	}

	res, err := a.client.Query(ctx,
		`query($filter: hub_db_chats_filter, $limit: Int!) {
			hub { db { chats(
				filter: $filter,
				order_by: [{field: "last_active_at", direction: DESC}, {field: "id", direction: DESC}],
				limit: $limit
			) { `+chatProjection+` } } } }`,
		map[string]any{"filter": filter, "limit": fetchLimit},
	)
	if err != nil {
		return fmt.Errorf("list chats: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("list chats: %w", res.Err())
	}
	var rows []chatRow
	if err := res.ScanData("hub.db.chats", &rows); err != nil && !isNoData(err) {
		return fmt.Errorf("scan chats: %w", err)
	}
	emitted := 0
	for _, c := range rows {
		// Boundary skip: rows sharing the page-key timestamp whose id sorts
		// at-or-before before_id (DESC) belong to the previous page.
		if bi != "" && c.LastActiveAt.Equal(btT) && c.ID >= bi {
			continue
		}
		if err := w.Append(
			c.ID, strPtrOrNil(c.ProjectID), c.UserID, c.AgentID, c.Title,
			strPtrOrNil(c.RootSessionID), fmtTime(c.CreatedAt), fmtTime(c.UpdatedAt),
			fmtTime(c.LastActiveAt), c.Archived,
		); err != nil {
			return err
		}
		emitted++
		if emitted == limit {
			break
		}
	}
	// Pathology guard: if the whole over-fetched window was boundary-skipped,
	// an empty page would read as end-of-list and strand every older row.
	if emitted == 0 && bi != "" && len(rows) == fetchLimit {
		return fmt.Errorf("keyset boundary group exceeds %d rows at %s — narrow the page or contact the operator", fetchLimit, bt)
	}
	return nil
}

func (a *HubApp) handleCreateChat(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := r.Context()

	agentID := strings.TrimSpace(r.String("agent_id"))
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	// The caller must be allowed to talk to this agent — the same platform
	// gate the transport plane enforces.
	if err := a.checkAgentAccess(ctx, u, agentID, ""); err != nil {
		return err
	}
	// And the agent identity must exist (a grant may outlive a deleted agent).
	if _, err := a.agentForToken(ctx, agentID); err != nil {
		return fmt.Errorf("agent %q: %w", agentID, err)
	}

	// First touch may be the user's first entry — provision the users row from
	// the token (chats.user_id FK target).
	if err := a.ensureUser(ctx, u); err != nil {
		return err
	}

	projectID := strings.TrimSpace(r.String("project_id"))
	if projectID != "" {
		if _, err := a.projectOwned(ctx, u, projectID); err != nil {
			return err
		}
	}
	title := strings.TrimSpace(r.String("title"))
	if title == "" {
		title = chatTitleDefault
	}

	// Timestamps ride the SDL insert_exp defaults (NOW()).
	row := map[string]any{
		"id":       newChatID(),
		"user_id":  u.ID,
		"agent_id": agentID,
		"title":    title,
	}
	if projectID != "" {
		row["project_id"] = projectID
	}
	res, err := a.client.Query(ctx,
		`mutation($data: hub_db_chats_mut_input_data!) {
			hub { db { insert_chats(data: $data) { id } } } }`,
		map[string]any{"data": row},
	)
	if err != nil {
		return fmt.Errorf("create chat: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("create chat: %w", res.Err())
	}

	chat, err := a.fetchChat(ctx, row["id"].(string))
	if err != nil {
		return fmt.Errorf("chat created but re-read failed: %w", err)
	}
	a.logger.Info("chat created", "chat", chat.ID, "agent", agentID, "user", u.ID)
	return w.SetJSON(chatJSON(chat))
}

func (a *HubApp) handleUpdateChat(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := r.Context()

	id := strings.TrimSpace(r.String("id"))
	if id == "" {
		return errors.New("id is required")
	}
	if _, err := a.chatOwned(ctx, u, id); err != nil {
		return err
	}

	data := map[string]any{}
	if v := strings.TrimSpace(r.String("title")); v != "" {
		data["title"] = v
	}
	switch v := strings.TrimSpace(r.String("project_id")); {
	case v == "none":
		data["project_id"] = nil
	case v != "":
		if _, err := a.projectOwned(ctx, u, v); err != nil {
			return err
		}
		data["project_id"] = v
	}
	switch strings.TrimSpace(strings.ToLower(r.String("archived"))) {
	case "true":
		data["archived"] = true
	case "false":
		data["archived"] = false
	case "":
		// unchanged
	default:
		return errors.New(`archived must be "true", "false" or ""`)
	}
	if len(data) == 0 {
		return errors.New("nothing to update: pass title, project_id or archived")
	}
	// updated_at rides the SDL update_exp (NOW()); last_active_at deliberately
	// does NOT move here — organizing a thread is not activity.

	if err := a.updateChatRow(ctx, id, data); err != nil {
		return err
	}
	after, err := a.fetchChat(ctx, id)
	if err != nil {
		return err
	}
	a.logger.Info("chat updated", "chat", id, "user", u.ID)
	return w.SetJSON(chatJSON(after))
}

func (a *HubApp) handleDeleteChat(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := r.Context()

	id := strings.TrimSpace(r.String("id"))
	if id == "" {
		return errors.New("id is required")
	}
	if _, err := a.chatOwned(ctx, u, id); err != nil {
		return err
	}
	res, err := a.client.Query(ctx,
		`mutation($id: String!) {
			hub { db { delete_chats(filter: { id: { eq: $id } }) { affected_rows } } } }`,
		map[string]any{"id": id},
	)
	if err != nil {
		return fmt.Errorf("delete chat: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("delete chat: %w", res.Err())
	}
	a.logger.Info("chat deleted", "chat", id, "user", u.ID)
	return w.SetJSON(map[string]any{"id": id, "deleted": true})
}

// ───────────────────────── projects ─────────────────────────

type projectRow struct {
	ID          string `json:"id"`
	OwnerUserID string `json:"owner_user_id"`
	Name        string `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (a *HubApp) handleMyProjects(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	res, err := a.client.Query(r.Context(),
		`query($uid: String!) {
			hub { db { projects(
				filter: { owner_user_id: { eq: $uid } },
				order_by: [{field: "created_at", direction: DESC}]
			) { id owner_user_id name created_at updated_at } } } }`,
		map[string]any{"uid": u.ID},
	)
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("list projects: %w", res.Err())
	}
	var rows []projectRow
	if err := res.ScanData("hub.db.projects", &rows); err != nil && !isNoData(err) {
		return fmt.Errorf("scan projects: %w", err)
	}
	for _, p := range rows {
		if err := w.Append(p.ID, p.OwnerUserID, p.Name, fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (a *HubApp) handleCreateProject(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	name := strings.TrimSpace(r.String("name"))
	if name == "" {
		return errors.New("name is required")
	}
	if err := a.ensureUser(r.Context(), u); err != nil {
		return err
	}
	id := newProjectID()
	res, err := a.client.Query(r.Context(),
		`mutation($data: hub_db_projects_mut_input_data!) {
			hub { db { insert_projects(data: $data) { id } } } }`,
		map[string]any{"data": map[string]any{
			"id": id, "owner_user_id": u.ID, "name": name,
		}},
	)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("create project: %w", res.Err())
	}
	a.logger.Info("project created", "project", id, "user", u.ID)
	return w.SetJSON(map[string]any{"id": id, "owner_user_id": u.ID, "name": name})
}

func (a *HubApp) handleUpdateProject(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := r.Context()
	id := strings.TrimSpace(r.String("id"))
	if id == "" {
		return errors.New("id is required")
	}
	name := strings.TrimSpace(r.String("name"))
	if name == "" {
		return errors.New("name is required")
	}
	proj, err := a.projectOwned(ctx, u, id)
	if err != nil {
		return err
	}
	res, err := a.client.Query(ctx,
		`mutation($id: String!, $data: hub_db_projects_mut_data!) {
			hub { db { update_projects(filter: { id: { eq: $id } }, data: $data) { affected_rows } } } }`,
		map[string]any{"id": id, "data": map[string]any{"name": name}},
	)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("update project: %w", res.Err())
	}
	// proj.OwnerUserID, not u.ID — an admin renaming a foreign project must
	// not be reported as its owner.
	return w.SetJSON(map[string]any{"id": id, "owner_user_id": proj.OwnerUserID, "name": name})
}

func (a *HubApp) handleDeleteProject(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := r.Context()
	id := strings.TrimSpace(r.String("id"))
	if id == "" {
		return errors.New("id is required")
	}
	if _, err := a.projectOwned(ctx, u, id); err != nil {
		return err
	}
	res, err := a.client.Query(ctx,
		`mutation($id: String!) {
			hub { db { delete_projects(filter: { id: { eq: $id } }) { affected_rows } } } }`,
		map[string]any{"id": id},
	)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("delete project: %w", res.Err())
	}
	a.logger.Info("project deleted", "project", id, "user", u.ID)
	return w.SetJSON(map[string]any{"id": id, "deleted": true})
}

// ───────────────────────── access management ─────────────────────────

func (a *HubApp) handleGrantAgentAccess(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}

	userID := strings.TrimSpace(r.String("user_id_grant"))
	agentID := strings.TrimSpace(r.String("agent_id"))
	role := strings.TrimSpace(r.String("access_role"))
	if userID == "" || agentID == "" {
		return errors.New("user_id_grant and agent_id are required")
	}
	if role == "" {
		role = "member"
	}
	if role != "member" && role != "owner" {
		return fmt.Errorf("access_role %q is invalid: member | owner", role)
	}
	// The grant may precede the user's first login — lazily provision a stub
	// users row (FK target); their first authenticated call upgrades the name.
	if err := a.ensureUser(ctx, auth.UserInfo{ID: userID}); err != nil {
		return err
	}
	if _, err := a.agentForToken(ctx, agentID); err != nil {
		return fmt.Errorf("agent %q: %w", agentID, err)
	}

	// Upsert = delete + insert (PK user_id+agent_id; postgres source reports
	// affected_rows 0 on success, so no conditional logic on the delete).
	if err := a.deleteUserAgent(ctx, userID, agentID); err != nil {
		return err
	}
	res, err := a.client.Query(ctx,
		`mutation($data: hub_db_user_agents_mut_input_data!) {
			hub { db { insert_user_agents(data: $data) { user_id } } } }`,
		map[string]any{"data": map[string]any{
			"user_id": userID, "agent_id": agentID, "role": role,
		}},
	)
	if err == nil {
		defer res.Close()
		err = res.Err()
	}
	if err != nil {
		// Concurrent grants for the same (user, agent) race the delete+insert
		// upsert — a grant row existing afterwards is success.
		if !a.grantRowExists(ctx, userID, agentID) {
			return fmt.Errorf("grant access: %w", err)
		}
	}
	a.logger.Info("agent access granted", "agent", agentID, "grantee", userID, "role", role, "by", u.ID)
	return w.SetJSON(map[string]any{"user_id": userID, "agent_id": agentID, "access_role": role})
}

func (a *HubApp) handleRevokeAgentAccess(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}
	userID := strings.TrimSpace(r.String("user_id_grant"))
	agentID := strings.TrimSpace(r.String("agent_id"))
	if userID == "" || agentID == "" {
		return errors.New("user_id_grant and agent_id are required")
	}
	if err := a.deleteUserAgent(ctx, userID, agentID); err != nil {
		return err
	}
	a.logger.Info("agent access revoked", "agent", agentID, "grantee", userID, "by", u.ID)
	return w.SetJSON(map[string]any{"id": userID + "/" + agentID, "deleted": true})
}

func (a *HubApp) handleAgentAccess(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}
	agentID := strings.TrimSpace(r.String("agent_id"))
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	res, err := a.client.Query(ctx,
		`query($aid: String!) {
			hub { db { user_agents(filter: { agent_id: { eq: $aid } }) {
				user_id role created_at user { display_name }
			} } } }`,
		map[string]any{"aid": agentID},
	)
	if err != nil {
		return fmt.Errorf("list access: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("list access: %w", res.Err())
	}
	var rows []struct {
		UserID    string    `json:"user_id"`
		Role      string    `json:"role"`
		CreatedAt time.Time `json:"created_at"`
		// `user` is a to-ONE relation (user_agents.user_id → users), so hugr
		// returns a single Arrow struct — scan it into a struct, NOT a slice
		// (a []struct fails: "cannot scan Arrow struct … into slice").
		User struct {
			DisplayName string `json:"display_name"`
		} `json:"user"`
	}
	if err := res.ScanData("hub.db.user_agents", &rows); err != nil && !isNoData(err) {
		return fmt.Errorf("scan access: %w", err)
	}
	for _, g := range rows {
		// Stub grants (user never logged in) show the id until ensureUser
		// upgrades the name on first entry. Append arity MUST match the four
		// declared columns — the adapter nil-pads short rows silently.
		name := g.UserID
		if g.User.DisplayName != "" {
			name = g.User.DisplayName
		}
		if err := w.Append(g.UserID, name, g.Role, fmtTime(g.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}

// ───────────────────────── shared helpers ─────────────────────────

var errChatNotFound = errors.New("chat not found")

// fetchChat reads one chat row with the service principal.
func (a *HubApp) fetchChat(ctx context.Context, id string) (chatRow, error) {
	res, err := a.client.Query(ctx,
		`query($id: String!) {
			hub { db { chats(filter: { id: { eq: $id } }, limit: 1) { `+chatProjection+` } } } }`,
		map[string]any{"id": id},
	)
	if err != nil {
		return chatRow{}, fmt.Errorf("chat lookup: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return chatRow{}, fmt.Errorf("chat lookup: %w", res.Err())
	}
	var rows []chatRow
	if err := res.ScanData("hub.db.chats", &rows); err != nil && !isNoData(err) {
		return chatRow{}, fmt.Errorf("chat lookup: %w", err)
	}
	if len(rows) == 0 {
		return chatRow{}, errChatNotFound
	}
	return rows[0], nil
}

// chatOwned fetches the chat and enforces ownership: the caller must own the
// thread, or pass the admin gate. A foreign chat reads as NOT FOUND — never
// leak existence.
func (a *HubApp) chatOwned(ctx context.Context, u auth.UserInfo, id string) (chatRow, error) {
	chat, err := a.fetchChat(ctx, id)
	if err != nil {
		return chatRow{}, err
	}
	if chat.UserID == u.ID {
		return chat, nil
	}
	if err := a.requireAdmin(withIdentity(ctx, u), u); err == nil {
		return chat, nil
	}
	return chatRow{}, errChatNotFound
}

// projectOwned verifies the project exists and belongs to the caller (admin
// passes). Foreign project → not found.
func (a *HubApp) projectOwned(ctx context.Context, u auth.UserInfo, id string) (projectRow, error) {
	res, err := a.client.Query(ctx,
		`query($id: String!) {
			hub { db { projects(filter: { id: { eq: $id } }, limit: 1) { id owner_user_id name created_at updated_at } } } }`,
		map[string]any{"id": id},
	)
	if err != nil {
		return projectRow{}, fmt.Errorf("project lookup: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return projectRow{}, fmt.Errorf("project lookup: %w", res.Err())
	}
	var rows []projectRow
	if err := res.ScanData("hub.db.projects", &rows); err != nil && !isNoData(err) {
		return projectRow{}, fmt.Errorf("project lookup: %w", err)
	}
	if len(rows) == 0 {
		return projectRow{}, errors.New("project not found")
	}
	p := rows[0]
	if p.OwnerUserID == u.ID {
		return p, nil
	}
	if err := a.requireAdmin(withIdentity(ctx, u), u); err == nil {
		return p, nil
	}
	return projectRow{}, errors.New("project not found")
}

// updateChatRow applies a partial update to a chat row (service principal).
func (a *HubApp) updateChatRow(ctx context.Context, id string, data map[string]any) error {
	res, err := a.client.Query(ctx,
		`mutation($id: String!, $data: hub_db_chats_mut_data!) {
			hub { db { update_chats(filter: { id: { eq: $id } }, data: $data) { affected_rows } } } }`,
		map[string]any{"id": id, "data": data},
	)
	if err != nil {
		return fmt.Errorf("update chat: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("update chat: %w", res.Err())
	}
	return nil
}

// grantRowExists is the race-loser check for grant_agent_access's upsert.
func (a *HubApp) grantRowExists(ctx context.Context, userID, agentID string) bool {
	res, err := a.client.Query(ctx,
		`query($uid: String!, $aid: String!) { hub { db { user_agents(
			filter: { user_id: { eq: $uid }, agent_id: { eq: $aid } } limit: 1
		) { user_id } } } }`,
		map[string]any{"uid": userID, "aid": agentID},
	)
	if err != nil {
		return false
	}
	defer res.Close()
	if res.Err() != nil {
		return false
	}
	var rows []struct {
		UserID string `json:"user_id"`
	}
	if err := res.ScanData("hub.db.user_agents", &rows); err != nil {
		return false
	}
	return len(rows) > 0
}

func (a *HubApp) deleteUserAgent(ctx context.Context, userID, agentID string) error {
	res, err := a.client.Query(ctx,
		`mutation($uid: String!, $aid: String!) {
			hub { db { delete_user_agents(filter: { user_id: { eq: $uid }, agent_id: { eq: $aid } }) { affected_rows } } } }`,
		map[string]any{"uid": userID, "aid": agentID},
	)
	if err != nil {
		return fmt.Errorf("revoke access: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("revoke access: %w", res.Err())
	}
	return nil
}

// chatJSON shapes a chatRow for the struct-typed mutation returns (nil
// pointers become empty strings — the framework has no null struct fields).
func chatJSON(c chatRow) map[string]any {
	return map[string]any{
		"id": c.ID, "project_id": deref(c.ProjectID), "user_id": c.UserID,
		"agent_id": c.AgentID, "title": c.Title,
		"root_session_id": deref(c.RootSessionID),
		"created_at":      fmtTime(c.CreatedAt), "updated_at": fmtTime(c.UpdatedAt),
		"last_active_at": fmtTime(c.LastActiveAt), "archived": c.Archived,
	}
}
