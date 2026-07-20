import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

export interface Me {
  user_id: string
  /** display name — hugr returns `user_name` */
  name: string
  role: string
  email?: string
}

export interface PermissionRow {
  /** hugr type/object name (e.g. "hub:management", "*") */
  object: string
  /** field name (e.g. "admin", "*") */
  field: string
  hidden: boolean
  disabled: boolean
}

export interface MyPermissions {
  role: string
  disabled: boolean
  permissions: PermissionRow[]
}

export interface IdentityProbe {
  me: Me
  myPermissions: MyPermissions
  isAdmin: boolean
}

const ADMIN_OBJECT = 'hub:management'
const ADMIN_FIELD = 'admin'

// Field names verified against live hugr (core_auth_auth_me /
// _my_permissions / _my_permission_entry introspection): me → user_id/
// user_name/role; my_permissions → role_name/disabled/permissions[];
// entry → object/field/hidden/disabled/filter/data.
const PROBE_QUERY = `
query IdentityProbe {
  function {
    core {
      auth {
        me { user_id user_name role }
        my_permissions {
          role_name
          disabled
          permissions { object field hidden disabled }
        }
      }
    }
  }
}`

interface ProbeResp {
  function: {
    core: {
      auth: {
        me: { user_id: string; user_name: string; role: string }
        my_permissions: {
          role_name: string
          disabled: boolean
          permissions: PermissionRow[]
        }
      }
    }
  }
}

/**
 * Admin = the caller's role carries a non-denied `hub:management.admin`
 * capability entry. `hub:management.admin` is grant-gated (not allow-by-default
 * like data access), so its presence — not hidden, not disabled — is the
 * definitive signal. `*` wildcards match.
 */
function deriveAdmin(perms: MyPermissions): boolean {
  return perms.permissions.some(
    (p) =>
      (p.object === ADMIN_OBJECT || p.object === '*') &&
      (p.field === ADMIN_FIELD || p.field === '*') &&
      !p.hidden &&
      !p.disabled,
  )
}

const MOCK_PROBE: IdentityProbe = {
  me: { user_id: 'u_mkeller', name: 'Maren Keller', role: 'admin', email: 'm.keller@acme.io' },
  myPermissions: {
    role: 'admin',
    disabled: false,
    permissions: [{ object: 'hub:management', field: 'admin', hidden: false, disabled: false }],
  },
  isAdmin: true,
}

export async function probeIdentity(): Promise<IdentityProbe> {
  return withDemo(MOCK_PROBE, async () => {
    const data = await postGraphQL<ProbeResp>(PROBE_QUERY)
    const auth = data.function.core.auth
    const myPermissions: MyPermissions = {
      role: auth.my_permissions.role_name,
      disabled: auth.my_permissions.disabled,
      permissions: auth.my_permissions.permissions ?? [],
    }
    return {
      me: { user_id: auth.me.user_id, name: auth.me.user_name, role: auth.me.role },
      myPermissions,
      isAdmin: deriveAdmin(myPermissions),
    }
  })
}
