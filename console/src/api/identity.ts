import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

export interface Me {
  user_id: string
  name: string
  role: string
  email?: string
}

export interface PermissionRow {
  role: string
  type_name: string
  field_name: string
  hidden: boolean
  disabled: boolean
}

export interface MyPermissions {
  role: string
  permissions: PermissionRow[]
}

export interface IdentityProbe {
  me: Me
  myPermissions: MyPermissions
  isAdmin: boolean
}

const ADMIN_TYPE = 'hub:management'
const ADMIN_FIELD = 'admin'

const PROBE_QUERY = `
query IdentityProbe {
  function {
    core {
      auth {
        me { user_id name role }
        my_permissions {
          role
          permissions { type_name field_name disabled hidden }
        }
      }
    }
  }
}`

interface ProbeResp {
  function: {
    core: {
      auth: {
        me: Me
        my_permissions: MyPermissions
      }
    }
  }
}

/**
 * Determine admin from `my_permissions`: ALLOW-by-default means admin holds the
 * `hub:management.admin` capability *unless* a row hides/disables it.
 */
function deriveAdmin(perms: MyPermissions): boolean {
  const denies = perms.permissions.some(
    (p) =>
      (p.type_name === ADMIN_TYPE || p.type_name === '*') &&
      (p.field_name === ADMIN_FIELD || p.field_name === '*') &&
      (p.hidden || p.disabled),
  )
  // In a floored deployment the admin role is named; treat a non-denied
  // hub:management surface + an "admin"-ish role as admin.
  if (denies) return false
  return /admin/i.test(perms.role)
}

const MOCK_PROBE: IdentityProbe = {
  me: { user_id: 'u_mkeller', name: 'Maren Keller', role: 'admin', email: 'm.keller@acme.io' },
  myPermissions: { role: 'admin', permissions: [] },
  isAdmin: true,
}

export async function probeIdentity(): Promise<IdentityProbe> {
  return withDemo(MOCK_PROBE, async () => {
    const data = await postGraphQL<ProbeResp>(PROBE_QUERY)
    const auth = data.function.core.auth
    return {
      me: auth.me,
      myPermissions: auth.my_permissions,
      isAdmin: deriveAdmin(auth.my_permissions),
    }
  })
}
