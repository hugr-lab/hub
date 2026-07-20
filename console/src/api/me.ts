import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

/**
 * "Me / Access" data layer. Capabilities are derived from the identity probe in
 * the screen; agent grants are fetched here. Backing store for grants is
 * `hub.db.user_agents` (owner ⊃ member), surfaced through the caller-scoped
 * `hub.my_agent_instances` view.
 */

export type GrantRole = 'owner' | 'member'

export interface AgentGrant {
  agentId: string
  name: string
  grant: GrantRole
  /** Runtime state used for the status dot, e.g. `running` / `starting`. */
  runtime: string
}

// Admin demo seed (mirrors the prototype `grants` for the admin persona, which
// is what the demo identity probe resolves to).
const MOCK_GRANTS: AgentGrant[] = [
  { agentId: 'agt_7f3a', name: 'analytics-copilot', grant: 'owner', runtime: 'running' },
  { agentId: 'agt_9d10', name: 'etl-warden', grant: 'owner', runtime: 'stopped' },
  { agentId: 'agt_4b77', name: 'finance-qa', grant: 'member', runtime: 'starting' },
]

interface RawAgentGrant {
  id: string
  display_name: string
  status: string
  /** The caller's access role from `hub.db.user_agents` (owner ⊃ member). */
  access_role: string
}

export async function listMyAgentGrants(): Promise<AgentGrant[]> {
  return withDemo(MOCK_GRANTS, async () => {
    const d = await postGraphQL<{ hub: { my_agent_instances: RawAgentGrant[] } }>(
      `query { hub { my_agent_instances { id display_name status access_role } } }`,
    )
    return d.hub.my_agent_instances.map((a) => ({
      agentId: a.id,
      name: a.display_name,
      grant: a.access_role === 'owner' ? 'owner' : 'member',
      runtime: a.status,
    }))
  })
}
