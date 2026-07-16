import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Bell } from 'lucide-react'
import { useTheme } from '@/lib/theme'
import { useSession } from '@/lib/session'
import { titleForPath } from '@/app/nav'
import {
  Avatar,
  Button,
  Menu,
  MenuContent,
  MenuItem,
  MenuSeparator,
  MenuTrigger,
  PathIcon,
  Popover,
  PopoverContent,
  PopoverTrigger,
  themeIconPath,
} from '@/components/ui'
import { cn } from '@/lib/cn'

interface Notif {
  id: string
  text: string
  agent: string
  time: string
  unread: boolean
  dot: string
}

const MOCK_NOTIFS: Notif[] = [
  { id: 'n1', text: 'analytics-copilot is waiting for your approval on http_post', agent: 'analytics-copilot', time: '2m', unread: true, dot: 'var(--amber)' },
  { id: 'n2', text: 'Scheduled task “daily-revenue-report” completed', agent: 'finance-qa', time: '18m', unread: true, dot: 'var(--green)' },
  { id: 'n3', text: 'gateway_offline_24h.csv artifact produced', agent: 'analytics-copilot', time: '1h', unread: false, dot: 'var(--surface3)' },
]

export function Topbar() {
  const { theme, toggle } = useTheme()
  const { identity, initials, signOut } = useSession()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const [notifs, setNotifs] = useState(MOCK_NOTIFS)
  const unread = notifs.filter((n) => n.unread).length
  const title = titleForPath(pathname)

  return (
    <header className="flex h-[50px] flex-none items-center gap-3 border-b border-border bg-surface px-[18px]">
      <div className="text-sm font-semibold tracking-[-0.01em]">{title}</div>
      <div className="flex-1" />

      {/* connection pill */}
      <div className="flex items-center gap-2 rounded-full bg-surface2 px-2.5 py-[3px] text-xs text-text2">
        <span className="h-[7px] w-[7px] rounded-full bg-green" />
        <span>hub · connected</span>
      </div>

      {/* notifications */}
      <Popover>
        <PopoverTrigger asChild>
          <button
            title="Notifications"
            className="relative flex h-[30px] w-[30px] items-center justify-center rounded-[7px] border border-border bg-surface text-text2 hover:bg-surface2"
          >
            <Bell className="h-[15px] w-[15px]" />
            {unread > 0 && (
              <span className="absolute -right-1 -top-1 flex h-[15px] min-w-[15px] items-center justify-center rounded-full bg-red px-[3px] text-[9.5px] font-bold text-white">
                {unread}
              </span>
            )}
          </button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-[330px] p-1.5">
          <div className="flex items-center border-b border-border px-2.5 pb-2 pt-1.5">
            <span className="flex-1 text-sm font-semibold">Notifications</span>
            <button
              onClick={() => setNotifs((ns) => ns.map((n) => ({ ...n, unread: false })))}
              className="text-xs font-semibold text-accent"
            >
              Mark all read
            </button>
          </div>
          {notifs.map((n) => (
            <button
              key={n.id}
              onClick={() => setNotifs((ns) => ns.map((x) => (x.id === n.id ? { ...x, unread: false } : x)))}
              className={cn(
                'flex w-full items-start gap-2.5 border-b border-border px-2.5 py-2 text-left last:border-b-0 hover:bg-surface2',
                n.unread && 'bg-surface2',
              )}
            >
              <span className="mt-[5px] h-[7px] w-[7px] flex-none rounded-full" style={{ background: n.dot }} />
              <span className="flex flex-col gap-0.5">
                <span className="text-xs leading-snug text-text">{n.text}</span>
                <span className="text-2xs text-text3">
                  {n.agent} · {n.time}
                </span>
              </span>
            </button>
          ))}
          <div className="px-2.5 pb-1 pt-2 text-2xs text-text3">Scheduled tasks &amp; agent session events</div>
        </PopoverContent>
      </Popover>

      {/* theme toggle */}
      <Button variant="secondary" size="icon" onClick={toggle} title="Toggle theme">
        <PathIcon d={themeIconPath[theme]} size={15} />
      </Button>

      {/* user menu */}
      <Menu>
        <MenuTrigger asChild>
          <button className="flex items-center gap-2 rounded-full border border-border bg-surface py-[3px] pl-1 pr-3 text-text hover:bg-surface2">
            <Avatar initials={initials} />
            <span className="text-sm font-medium">{identity.name}</span>
            <span className="text-xs text-text3">· {identity.role}</span>
          </button>
        </MenuTrigger>
        <MenuContent align="end" className="w-[230px]">
          <div className="border-b border-border px-2.5 pb-2 pt-1.5">
            <div className="text-sm font-semibold">{identity.name}</div>
            <div className="text-xs text-text3">{identity.email}</div>
            <div className="mt-1 inline-block rounded-chip bg-accent-soft px-1.5 py-0.5 text-xs font-semibold text-accent">
              {identity.role}
            </div>
          </div>
          <MenuItem onSelect={() => navigate('/me')}>My access &amp; identity</MenuItem>
          <MenuSeparator />
          <MenuItem danger onSelect={signOut}>
            Sign out
          </MenuItem>
        </MenuContent>
      </Menu>
    </header>
  )
}
