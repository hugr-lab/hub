# Hub capabilities

Hub-service declares a small number of **logical capability points** under the
virtual type `hub:management`. Deployments grant them to specific OIDC roles
via Hugr `core.role_permissions` — hub-service never hard-codes a role name.

Both hub-service (Go) and JupyterHub (`jupyterhub_config.py`) consult the same
table, so admin recognition is unified across the stack.

## Declared capabilities

| Name | Governs |
|---|---|
| `hub:management.admin` | Cross-user admin: `delete_agent` mutation, bypass of per-agent `hub.db.user_agents` grants in `checkAgentAccess`, sets JupyterHub `user.admin=True` (exposes `/hub/admin` UI + `HUGR_HUB_ADMIN=true` env in workspace). |

Adding a new capability: add a constant in `pkg/hubapp/capabilities.go`, use
`a.hasCapability(ctx, u, "your_name")` in the gate, document it here.

## How the check works

1. A handler calls `a.requireAdmin(ctx, u)` or `a.checkAgentAccess(ctx, u, ...)`.
2. That delegates to `a.hasCapability(ctx, u, CapManagementAdmin)`.
3. `hasCapability` calls `function.core.auth.check_access(type_name, fields)` via
   `withIdentity(ctx, u)` — hub-service propagates the caller's identity using
   the `x-hugr-impersonated-*` headers on top of the service secret key.
4. Hugr validates impersonation (requires the service's role to have
   `can_impersonate=true`), evaluates `check_access` against
   `core.role_permissions` for the impersonated role.
5. Hub-service reads the `enabled` flag from the returned row. **Explicit
   allow only** — absence of a rule means NOT granted.

JupyterHub's `_post_auth_hook` follows the exact same pattern from Python
(`_has_hub_management_admin` in `jupyterhub_config.py`) to set `user.admin`
at login time — calling `check_access` with the same impersonation headers.

## Built-in Hugr roles

A fresh Hugr install seeds three roles on first DB init:

| Role | `can_impersonate` | Notes |
|---|---|---|
| `admin` | ✅ true | Full access — this is the role that hub-service's management secret key maps to by default. Can impersonate others, which is what makes `withIdentity(ctx, u)` work. |
| `public` | false | Anonymous/unauthenticated. |
| `readonly` | false | Has a default rule that disables all mutations. |

Deployments typically add a `user` role for regular authenticated OIDC users
(see step 2 below).

## One-time deployment setup

### Default case: OIDC admin users already map to the `admin` role

If your OIDC provider's `x-hugr-role` claim already returns `"admin"` for your
admin users, the built-in `admin` role already has `can_impersonate=true`, so
you only need **one** mutation to grant the capability:

```graphql
mutation {
  core {
    insert_role_permissions(data: {
      role: "admin"
      type_name: "hub:management"
      field_name: "admin"
      disabled: false
      hidden: false
    }) { role type_name field_name }
  }
}
```

That role now:

- Can call `delete_agent` (hub-service `requireAdmin` lets it through).
- Bypasses per-agent grants in `start_agent` / `stop_agent`
  (hub-service `checkAgentAccess`).
- Logs in to JupyterHub as an admin (the `_post_auth_hook` sets
  `authentication["admin"] = True`, which JupyterHub uses for its admin UI
  and `/hub/admin` access).
- Gets `HUGR_HUB_ADMIN=true` as an env var in their spawned workspace, which
  the `hub-admin` JupyterLab extension reads to decide whether to show the
  admin panel.

### Alternative case: custom admin role name (e.g. `operator`, `platform-ops`)

If your OIDC provider returns something other than `"admin"` for your
privileged users, create the role first and give it `can_impersonate=true`
(so hub-service can propagate identity through it):

```graphql
mutation {
  core {
    insert_roles(data: {
      name: "operator"
      description: "Platform operators"
      disabled: false
      can_impersonate: true
    }) { name }
  }
}
```

Then grant the capability:

```graphql
mutation {
  core {
    insert_role_permissions(data: {
      role: "operator"
      type_name: "hub:management"
      field_name: "admin"
      disabled: false
      hidden: false
    }) { role type_name field_name }
  }
}
```

### Create a baseline `user` role for regular users

The default Hugr install does not ship a `user` role. For regular end users
you probably want one so they can read their own data (the `my_*` table
functions and direct `hub.db.*` queries):

```graphql
mutation {
  core {
    insert_roles(data: {
      name: "user"
      description: "Regular authenticated user — can CRUD own hub conversations, messages, agents"
      disabled: false
      can_impersonate: false
    }) { name }
  }
}
```

## Recovery

Lost access to the capability grant? Two options:

1. **Direct Hugr DB edit.** Connect to Hugr's metadata DB (the attached DuckDB
   or the `core` schema in your Postgres, depending on deploy mode) and
   `INSERT INTO core.role_permissions (role, type_name, field_name, disabled, hidden) VALUES ('admin', 'hub:management', 'admin', false, false);`
2. **Call the mutation with the service secret.** Anyone holding
   `HUGR_SECRET_KEY` can run the grant mutations above via `curl`. The secret
   key maps to the built-in `admin` role, which has full access by default.

## Revoking admin

Two options:

**Option A — remove the capability grant entirely:**

```graphql
mutation {
  core {
    delete_role_permissions(filter: {
      role: { eq: "admin" }
      type_name: { eq: "hub:management" }
      field_name: { eq: "admin" }
    }) { affected_rows }
  }
}
```

**Option B — keep the row but flip `disabled`:**

```graphql
mutation {
  core {
    update_role_permissions(
      filter: {
        role: { eq: "admin" }
        type_name: { eq: "hub:management" }
        field_name: { eq: "admin" }
      }
      data: { disabled: true }
    ) { affected_rows }
  }
}
```

## Cache

Role permission lookups are cached by Hugr with a short TTL. After any
`insert_role_permissions` / `update_role_permissions` / `delete_role_permissions`
mutation, changes propagate automatically when the TTL expires. To force an
immediate refresh (e.g. during tests or after a revocation) call the cache
invalidate **query** (it's a query, not a mutation):

```graphql
{
  function {
    core {
      cache {
        invalidate(tags: ["$role_permissions"]) {
          success
          message
        }
      }
    }
  }
}
```

## Identity propagation end-to-end

```
OIDC login → JupyterHub _post_auth_hook
  → _has_hub_management_admin(user_id, role)
      → Hugr my_permissions with x-hugr-impersonated-role
          → [Hugr validates service secret + can_impersonate]
          → returns target role's permissions
      → match object="hub:management", field="admin", !disabled
  → authentication["admin"] = True/False

Frontend calls hub-service mutation (e.g. delete_agent)
  → Hugr planner invokes airport-go handler with ArgFromContext user info
  → handler calls a.requireAdmin(ctx, u)
      → a.hasCapability(ctx, u, "admin")
          → withIdentity(ctx, u) wraps client with x-hugr-impersonated-*
          → a.client.Query(ctx, my_permissions query)
              → [Hugr validates service secret + can_impersonate]
              → returns target role's permissions
          → match object="hub:management", field="admin", !disabled
  → allowed / forbidden
```

Single source of truth, no role names in code.
