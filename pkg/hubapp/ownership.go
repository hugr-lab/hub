package hubapp

// Shared helpers used by the airport-go mutating / reading functions:
//   - ensureUser: lazily provision a hub.db.users row on first touch (FK target)

import (
	"context"
	"fmt"
	"strings"

	"github.com/hugr-lab/hub/pkg/auth"
)

// ensureUser lazily provisions a users row on first touch — there is no OIDC
// sync anymore (HB6 prune): the verified token IS the registry (owner
// 2026-07-10). Insert-if-missing with the display name + role from the token;
// a differing non-empty token name refreshes display_name (IdP renames
// follow), except an id-as-name placeholder. Grant targets that have never
// logged in get a stub row (display_name = id) their first authenticated
// call upgrades.
func (a *HubApp) ensureUser(ctx context.Context, u auth.UserInfo) error {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { users(filter: { id: { eq: $id } }, limit: 1) { id display_name } } } }`,
		map[string]any{"id": u.ID},
	)
	if err != nil {
		return fmt.Errorf("user lookup: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("user lookup: %w", res.Err())
	}
	var rows []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	if err := res.ScanData("hub.db.users", &rows); err != nil && !isNoData(err) {
		return fmt.Errorf("user lookup: %w", err)
	}

	name := strings.TrimSpace(u.Name)
	if len(rows) == 0 {
		if name == "" {
			name = u.ID
		}
		role := u.Role
		if role == "" {
			role = "user"
		}
		ins, err := a.client.Query(ctx,
			`mutation($data: hub_db_users_mut_input_data!) {
				hub { db { insert_users(data: $data) { id } } } }`,
			map[string]any{"data": map[string]any{
				"id": u.ID, "display_name": name, "hugr_role": role,
			}},
		)
		if err == nil {
			defer ins.Close()
			err = ins.Err()
		}
		if err != nil {
			// Two first-touch calls can race the insert (PK violation for the
			// loser) — the row existing is SUCCESS, whoever wrote it.
			if a.userRowExists(ctx, u.ID) {
				return nil
			}
			return fmt.Errorf("provision user: %w", err)
		}
		a.logger.Info("user lazily provisioned", "user", u.ID, "name", name, "role", role)
		return nil
	}
	// An id-as-name is a placeholder, not an IdP rename — it must never clobber a
	// real display name. A name equal to the id is exactly what a nameless
	// identity carries: the management middleware stamps Name = user id for
	// secret-key callers (middleware.go), and it is also the stub written below
	// on first touch. Only a genuine, distinct token name refreshes display_name.
	if name != "" && name != u.ID && name != rows[0].DisplayName {
		upd, err := a.client.Query(ctx,
			`mutation($id: String!, $name: String!) {
				hub { db { update_users(filter: { id: { eq: $id } }, data: { display_name: $name }) { affected_rows } } } }`,
			map[string]any{"id": u.ID, "name": name},
		)
		if err != nil {
			a.logger.Warn("user name refresh failed", "user", u.ID, "error", err)
			return nil // best-effort: the row exists, the FK is satisfied
		}
		upd.Close()
	}
	return nil
}

// userRowExists is the race-loser check for ensureUser's first-touch insert.
func (a *HubApp) userRowExists(ctx context.Context, id string) bool {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { users(filter: { id: { eq: $id } }, limit: 1) { id } } } }`,
		map[string]any{"id": id},
	)
	if err != nil {
		return false
	}
	defer res.Close()
	if res.Err() != nil {
		return false
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := res.ScanData("hub.db.users", &rows); err != nil {
		return false
	}
	return len(rows) > 0
}
