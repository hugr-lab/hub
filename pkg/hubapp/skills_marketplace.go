package hubapp

// SK1 — the skills marketplace HTTP surface (spec-skills-distribution §6):
//
//	GET /skills/catalog          → the §4-filtered catalog (shared skills)
//	GET /skills/{name}/bundle    → the bundle tar.gz (§4-gated)
//
// Both are exempt from the OIDC middleware and verify the caller in-handler via
// hugr auth.me (skills_auth.go), so they accept agent tokens as well as user
// tokens. The catalog is a privileged read of the Agent DB `skills` table
// (secret-key principal, sidesteps per-agent RLS); the §4 gate is the caller's
// own capability grants, evaluated by hugr check_access under the caller's role.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/query-engine/types"
)

// catalogEntry is one marketplace listing. bundle_path / owner / agent_id are
// deliberately NOT exposed — the caller downloads by name, the hub resolves
// the bytes.
type catalogEntry struct {
	Name                 string   `json:"name"`
	Version              string   `json:"version"`
	Description          string   `json:"description,omitempty"`
	ContentHash          string   `json:"content_hash"`
	Source               string   `json:"source,omitempty"`
	TaskEligible         bool     `json:"task_eligible"`
	Keywords             []string `json:"keywords,omitempty"`
	TierCompat           []string `json:"tier_compat,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}

// sharedSkillRow is the privileged read shape of a shared `skills` row.
// Metadata scans directly into map[string]any: the `metadata` JSON scalar
// arrives over IPC as utf8 JSON, and the query-engine scanner decodes a
// utf8-JSON column into a map/any destination (jsonStringConvertFunc). A
// []byte-shaped json.RawMessage fails (it routes to the Arrow-list converter),
// and a NAMED struct fails too (structConvertFunc wants an Arrow Struct
// column) — map[string]any is the struct-shaped target this column supports.
type sharedSkillRow struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Description  string         `json:"description"`
	ContentHash  string         `json:"content_hash"`
	Source       string         `json:"source"`
	TaskEligible bool           `json:"task_eligible"`
	Keywords     []string       `json:"keywords"`
	TierCompat   []string       `json:"tier_compat"`
	Metadata     map[string]any `json:"metadata"`
}

const capabilitySkillNamespace = "hugen:skill:capability"

// handleSkillsCatalog serves GET /skills/catalog.
func (a *HubApp) handleSkillsCatalog(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.skillsAuthOrFail(w, r)
	if !ok {
		return
	}
	rows, err := a.querySharedSkills(r.Context())
	if err != nil {
		a.logger.Warn("skills catalog read", "error", err)
		skillsError(w, http.StatusBadGateway, "catalog_unavailable", "could not read the catalog")
		return
	}
	out := make([]catalogEntry, 0, len(rows))
	for _, s := range rows {
		req := requiredCapsFromMetadata(s.Metadata)
		if len(req) > 0 {
			granted, err := a.callerHasCaps(r.Context(), caller, req)
			if err != nil {
				a.logger.Warn("skills catalog cap check", "skill", s.Name, "error", err)
				continue // fail closed for this entry
			}
			if !granted {
				continue
			}
		}
		out = append(out, catalogEntry{
			Name:                 s.Name,
			Version:              s.Version,
			Description:          s.Description,
			ContentHash:          s.ContentHash,
			Source:               s.Source,
			TaskEligible:         s.TaskEligible,
			Keywords:             s.Keywords,
			TierCompat:           s.TierCompat,
			RequiredCapabilities: req,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"skills": out})
}

// handleSkillsBundle serves GET /skills/{name}/bundle.
func (a *HubApp) handleSkillsBundle(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.skillsAuthOrFail(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := validBundleName(name); err != nil {
		skillsError(w, http.StatusBadRequest, "invalid_name", "invalid skill name")
		return
	}
	s, err := a.sharedSkillByName(r.Context(), name)
	if errors.Is(err, errSkillNotFound) {
		skillsError(w, http.StatusNotFound, "not_found", "no such skill in the catalog")
		return
	}
	if err != nil {
		a.logger.Warn("skills bundle lookup", "skill", name, "error", err)
		skillsError(w, http.StatusBadGateway, "catalog_unavailable", "could not read the catalog")
		return
	}
	if req := requiredCapsFromMetadata(s.Metadata); len(req) > 0 {
		granted, err := a.callerHasCaps(r.Context(), caller, req)
		if err != nil {
			a.logger.Warn("skills bundle cap check", "skill", name, "error", err)
			skillsError(w, http.StatusForbidden, "forbidden", "capability check failed")
			return
		}
		if !granted {
			skillsError(w, http.StatusForbidden, "forbidden", "missing required capability")
			return
		}
	}
	if a.bundleStore == nil {
		skillsError(w, http.StatusServiceUnavailable, "no_store", "byte-store unavailable")
		return
	}
	rc, err := a.bundleStore.Get(r.Context(), name, s.Version)
	if errors.Is(err, ErrBundleNotFound) {
		skillsError(w, http.StatusNotFound, "not_found", "bundle bytes missing")
		return
	}
	if err != nil {
		a.logger.Warn("skills bundle read", "skill", name, "error", err)
		skillsError(w, http.StatusBadGateway, "bundle_unavailable", "could not read the bundle")
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+".tar.gz\"")
	w.Header().Set("X-Skill-Content-Hash", s.ContentHash)
	w.Header().Set("X-Skill-Version", s.Version)
	_, _ = io.Copy(w, rc)
}

// skillsAuthOrFail verifies the caller and writes a 401 on failure.
func (a *HubApp) skillsAuthOrFail(w http.ResponseWriter, r *http.Request) (skillsCaller, bool) {
	caller, err := a.verifySkillsCaller(r.Context(), extractBearerHeader(r))
	if err != nil {
		a.logger.Info("skills auth failed", "path", r.URL.Path, "error", err)
		skillsError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing token")
		return skillsCaller{}, false
	}
	return caller, true
}

// querySharedSkills privileged-reads all shared catalog rows.
func (a *HubApp) querySharedSkills(ctx context.Context) ([]sharedSkillRow, error) {
	res, err := a.client.Query(ctx,
		`query { hub { agent { db { skills(
			filter: { shared: { eq: true } }
		) { name version description content_hash source task_eligible keywords tier_compat metadata } } } } }`,
		nil,
	)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var rows []sharedSkillRow
	if err := res.ScanData("hub.agent.db.skills", &rows); err != nil {
		// An empty catalog is not an error: the query-engine client returns
		// ErrNoData / ErrWrongDataPath when the filtered set is empty (e.g. a
		// fresh deployment before the seed runs). Treat it as zero skills —
		// otherwise the seeder's readiness probe (this call) never passes and
		// the catalog never fills (deadlock).
		if errors.Is(err, types.ErrNoData) || errors.Is(err, types.ErrWrongDataPath) {
			return nil, nil
		}
		return nil, err
	}
	return rows, nil
}

var errSkillNotFound = errors.New("skill not found in catalog")

// sharedSkillByName privileged-reads one shared row by name.
func (a *HubApp) sharedSkillByName(ctx context.Context, name string) (sharedSkillRow, error) {
	res, err := a.client.Query(ctx,
		`query($n: String!) { hub { agent { db { skills(
			filter: { shared: { eq: true }, name: { eq: $n } } limit: 1
		) { name version description content_hash source task_eligible keywords tier_compat metadata } } } } }`,
		map[string]any{"n": name},
	)
	if err != nil {
		return sharedSkillRow{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return sharedSkillRow{}, res.Err()
	}
	var rows []sharedSkillRow
	if err := res.ScanData("hub.agent.db.skills", &rows); err != nil {
		if errors.Is(err, types.ErrNoData) || errors.Is(err, types.ErrWrongDataPath) {
			return sharedSkillRow{}, errSkillNotFound
		}
		return sharedSkillRow{}, err
	}
	if len(rows) == 0 {
		return sharedSkillRow{}, errSkillNotFound
	}
	return rows[0], nil
}

// callerUserInfo projects a verified marketplace caller into the auth.UserInfo
// withIdentity threads onto the impersonated hugr query, so check_access
// evaluates against the caller's real role — not hub-service's principal.
func callerUserInfo(caller skillsCaller) auth.UserInfo {
	return auth.UserInfo{ID: caller.ID, Name: caller.Name, Role: caller.Role}
}

// callerHasCaps reports whether the caller's role holds a POSITIVE
// (non-disabled) `hugen:skill:capability` grant for EVERY required capability —
// deny-by-default (SK5).
//
// It reads the caller-role's `core.role_permissions` rows PRIVILEGED (service
// principal, unimpersonated ctx — an agent role is floor-denied from reading
// core) and requires an explicit grant per cap. It deliberately does NOT use
// hugr `check_access` the way the publish gate does: check_access is
// allow-by-default (query-engine perm.checkObjectField — "without a matching
// row access is allowed"), which the publish gate corrects with a single floor
// deny on the bounded `(hugen:skill, publish)` key. Capability names are
// UNBOUNDED, so they cannot be floor-denied — an allow-by-default read would
// grant every cap absent an explicit deny, defeating the gate. Hence the
// positive-row read.
//
// No admin bypass: a `hasCapability(hub:management.admin)` bypass would itself
// be allow-by-default (an agent role carries no hub:management deny), so every
// agent would slip through. An admin that needs a capped skill is granted the
// cap row like anyone else (grant_skill_capability). caller.Role is
// hub-minted from the admin-assigned identity, not agent-spoofable (§4).
func (a *HubApp) callerHasCaps(ctx context.Context, caller skillsCaller, required []string) (bool, error) {
	if len(required) == 0 {
		return true, nil
	}
	granted, err := a.roleSkillCapabilities(ctx, caller.Role)
	if err != nil {
		return false, err
	}
	return hasAllCaps(granted, required), nil
}

// hasAllCaps reports whether every required capability is present in granted.
func hasAllCaps(granted map[string]bool, required []string) bool {
	for _, c := range required {
		if !granted[c] {
			return false
		}
	}
	return true
}

// rolePermRow is the read shape of one core.role_permissions row.
type rolePermRow struct {
	TypeName  string `json:"type_name"`
	FieldName string `json:"field_name"`
	Disabled  bool   `json:"disabled"`
}

// roleSkillCapabilities privileged-reads the set of ENABLED
// `hugen:skill:capability` grants on `role`. A missing role, or a role with no
// such grants, yields an empty set (deny-by-default). The read runs as the
// service principal (ctx unimpersonated): the caller's own agent role cannot
// read core.role_permissions (floor-denied), so this must not carry the
// caller's identity.
func (a *HubApp) roleSkillCapabilities(ctx context.Context, role string) (map[string]bool, error) {
	if strings.TrimSpace(role) == "" {
		return nil, nil
	}
	res, err := a.client.Query(ctx,
		`query($role: String!) { core { roles_by_pk(name: $role) { permissions { type_name field_name disabled } } } }`,
		map[string]any{"role": role},
	)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var role_ struct {
		Permissions []rolePermRow `json:"permissions"`
	}
	if err := res.ScanData("core.roles_by_pk", &role_); err != nil && !isNoData(err) {
		return nil, err
	}
	return enabledCapsFromRows(role_.Permissions), nil
}

// enabledCapsFromRows projects the enabled hugen:skill:capability field names
// out of a role's permission rows.
func enabledCapsFromRows(rows []rolePermRow) map[string]bool {
	out := make(map[string]bool)
	for _, p := range rows {
		if p.TypeName == capabilitySkillNamespace && !p.Disabled {
			out[p.FieldName] = true
		}
	}
	return out
}

// requiredCapsFromMetadata pulls metadata.hugen.required_capabilities out of a
// decoded skills.metadata map (scanned straight from the JSON column). Lenient:
// any shape mismatch yields no caps (a public skill), never an error — a
// malformed blob must not brick the catalog. Shared by the catalog read path
// and the publish gate (the parsed manifest's Metadata has the same shape).
func requiredCapsFromMetadata(md map[string]any) []string {
	hugen, ok := md["hugen"].(map[string]any)
	if !ok {
		return nil
	}
	list, ok := hugen["required_capabilities"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractBearerHeader returns the raw bearer token from the Authorization
// header, or "".
func extractBearerHeader(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// skillsError writes the marketplace error envelope.
func skillsError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}
