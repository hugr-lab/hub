import { NavLink } from 'react-router-dom'
import { cn } from '@/lib/cn'
import { useSession } from '@/lib/session'
import { navGroupsFor } from '@/app/nav'
import { PathIcon, Pill, Segmented } from '@/components/ui'
import { useNavCounts } from './useNavCounts'

export function Sidebar() {
  const { persona, setPersona, isAdmin } = useSession()
  const groups = navGroupsFor(persona)
  const counts = useNavCounts()

  return (
    <nav className="flex w-[216px] flex-none flex-col border-r border-border bg-surface">
      {/* brand */}
      <div className="flex items-center gap-2.5 px-4 pb-3 pt-3.5">
        <img src="/console/logo.svg" alt="hugr" className="h-7 w-7" />
        <div className="flex flex-col leading-none">
          <span className="text-sm font-bold tracking-[-0.01em]">hugr hub</span>
          <span className="eyebrow mt-0.5">console</span>
        </div>
      </div>

      {/* nav groups */}
      <div className="flex flex-1 flex-col gap-3.5 overflow-y-auto px-2 pb-3 pt-1">
        {groups.map((grp, gi) => (
          <div key={grp.label ?? gi} className="flex flex-col gap-px">
            {grp.label && (
              <div className="eyebrow px-2.5 pb-1 pt-0.5 tracking-[0.08em]">{grp.label}</div>
            )}
            {grp.items.map((it) => {
              const badge = it.badgeKey ? counts[it.badgeKey] : undefined
              return (
                <NavLink
                  key={it.id}
                  to={it.to}
                  className={({ isActive }) =>
                    cn(
                      'flex items-center gap-2.5 rounded-btn px-2.5 py-[6.5px] text-[13px] transition-colors',
                      isActive
                        ? 'bg-accent-soft font-semibold text-accent'
                        : 'font-medium text-text2 hover:bg-surface2',
                    )
                  }
                >
                  <PathIcon d={it.icon} size={15} className="flex-none opacity-85" />
                  <span className="flex-1">{it.label}</span>
                  {badge != null && <Pill>{badge}</Pill>}
                </NavLink>
              )
            })}
          </div>
        ))}
      </div>

      {/* persona switcher */}
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
    </nav>
  )
}
