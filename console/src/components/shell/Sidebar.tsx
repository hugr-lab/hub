import { NavLink } from 'react-router-dom'
import { cn } from '@/lib/cn'
import { useSession } from '@/lib/session'
import { navGroupsFor } from '@/app/nav'
import { PathIcon, Pill, Segmented } from '@/components/ui'
import { useAppMode } from '@/lib/appMode'
import { useNavCounts } from './useNavCounts'
import { useSidebarCollapsed } from './useSidebarCollapsed'

// Chevron toggle (viewBox 0 0 16 16) — points the way the click will move the rail.
const CHEVRON_LEFT = 'M9.5 4L6 8l3.5 4'
const CHEVRON_RIGHT = 'M6.5 4L10 8l-3.5 4'

export function Sidebar() {
  const { persona, setPersona, isAdmin } = useSession()
  const appMode = useAppMode() === 'app'
  const groups = navGroupsFor(persona)
  const counts = useNavCounts()
  const [collapsed, toggleCollapsed] = useSidebarCollapsed()

  return (
    <nav
      className={cn(
        'flex flex-none flex-col border-r border-border bg-surface transition-[width] duration-200',
        collapsed ? 'w-[60px]' : 'w-[216px]',
      )}
    >
      {/* brand + collapse toggle */}
      {collapsed ? (
        <div className="flex flex-col items-center gap-2 px-2 pb-3 pt-3.5">
          <img src="/console/logo.svg" alt="hugr" className="h-7 w-7" />
          <CollapseToggle collapsed onClick={toggleCollapsed} />
        </div>
      ) : (
        <div className="flex items-center gap-2.5 px-4 pb-3 pt-3.5">
          <img src="/console/logo.svg" alt="hugr" className="h-7 w-7" />
          <div className="flex flex-1 flex-col leading-none">
            <span className="text-sm font-bold tracking-[-0.01em]">hugr hub</span>
            <span className="eyebrow mt-0.5">{appMode ? 'workspace' : 'console'}</span>
          </div>
          <CollapseToggle collapsed={false} onClick={toggleCollapsed} />
        </div>
      )}

      {/* nav groups */}
      <div className="flex flex-1 flex-col gap-3.5 overflow-y-auto px-2 pb-3 pt-1">
        {groups.map((grp, gi) => (
          <div key={grp.label ?? gi} className="flex flex-col gap-px">
            {grp.label &&
              (collapsed ? (
                gi > 0 && <div className="mx-auto mb-1 mt-0.5 h-px w-5 bg-border" />
              ) : (
                <div className="eyebrow px-2.5 pb-1 pt-0.5 tracking-[0.08em]">{grp.label}</div>
              ))}
            {grp.items.map((it) => {
              const badge = it.badgeKey ? counts[it.badgeKey] : undefined
              return (
                <NavLink
                  key={it.id}
                  to={it.to}
                  title={collapsed ? it.label : undefined}
                  className={({ isActive }) =>
                    cn(
                      'relative flex items-center rounded-btn text-[13px] transition-colors',
                      collapsed ? 'justify-center py-2' : 'gap-2.5 px-2.5 py-[6.5px]',
                      isActive
                        ? 'bg-accent-soft font-semibold text-accent'
                        : 'font-medium text-text2 hover:bg-surface2',
                    )
                  }
                >
                  <PathIcon d={it.icon} size={collapsed ? 17 : 15} className="flex-none opacity-85" />
                  {!collapsed && <span className="flex-1">{it.label}</span>}
                  {!collapsed && badge != null && <Pill>{badge}</Pill>}
                  {collapsed && badge != null && badge > 0 && (
                    <span className="absolute right-[7px] top-[7px] h-[7px] w-[7px] rounded-full bg-red" />
                  )}
                </NavLink>
              )
            })}
          </div>
        ))}
      </div>

      {/* persona switcher — hidden in the /app workspace (persona pinned owner) */}
      {appMode ? null : collapsed
        ? isAdmin && (
            <div className="flex flex-col items-center border-t border-border px-2 py-2.5">
              <button
                title={`View as ${persona === 'admin' ? 'Admin' : 'Personal'} — click to switch`}
                onClick={() => setPersona(persona === 'admin' ? 'owner' : 'admin')}
                className="flex h-8 w-8 items-center justify-center rounded-btn border border-border text-2xs font-bold text-text2 hover:bg-surface2"
              >
                {persona === 'admin' ? 'A' : 'P'}
              </button>
            </div>
          )
        : (
            <div className="flex flex-col gap-2 border-t border-border px-3 py-2.5">
              <Segmented
                size="sm"
                className="w-full [&>button]:flex-1"
                options={[
                  { value: 'admin', label: 'Admin' },
                  { value: 'owner', label: 'Personal' },
                ]}
                value={persona}
                onChange={(p) => isAdmin && setPersona(p)}
              />
              <div className="text-center text-2xs text-text3">view as · role-gated nav</div>
            </div>
          )}
    </nav>
  )
}

function CollapseToggle({ collapsed, onClick }: { collapsed: boolean; onClick: () => void }) {
  return (
    <button
      title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
      onClick={onClick}
      className="flex h-6 w-6 flex-none items-center justify-center rounded text-text3 hover:bg-surface2 hover:text-text2"
    >
      <PathIcon d={collapsed ? CHEVRON_RIGHT : CHEVRON_LEFT} size={15} />
    </button>
  )
}
