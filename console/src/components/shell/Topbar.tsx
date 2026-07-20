import { useLocation, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { Bell } from 'lucide-react'
import { useTheme } from '@/lib/theme'
import { useSession } from '@/lib/session'
import { titleForPath } from '@/app/nav'
import { useChatActivity } from '@/api/useChatActivity'
import { markChatRead, eventKindLabel } from '@/api/notifications'
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

// relative "2m" / "1h" / "3d" for a bell timestamp.
function ago(iso?: string): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86400)}d`
}

export function Topbar() {
  const { theme, toggle } = useTheme()
  const { identity, initials, signOut } = useSession()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { data: activity = [] } = useChatActivity()
  const unreadChats = activity.filter((a) => a.unread > 0)
  const badge = unreadChats.reduce((n, a) => n + a.unread, 0)
  const title = titleForPath(pathname)

  const openChat = (id: string) => navigate(`/chat/${id}`)
  const markAllRead = async () => {
    await Promise.all(unreadChats.map((a) => markChatRead(a.chat_id, a.last_seq).catch(() => {})))
    qc.invalidateQueries({ queryKey: ['chat-activity'] })
  }

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
            {badge > 0 && (
              <span className="absolute -right-1 -top-1 flex h-[15px] min-w-[15px] items-center justify-center rounded-full bg-red px-[3px] text-[9.5px] font-bold text-white">
                {badge > 99 ? '99+' : badge}
              </span>
            )}
          </button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-[330px] p-1.5">
          <div className="flex items-center border-b border-border px-2.5 pb-2 pt-1.5">
            <span className="flex-1 text-sm font-semibold">Notifications</span>
            {unreadChats.length > 0 && (
              <button onClick={markAllRead} className="text-xs font-semibold text-accent">
                Mark all read
              </button>
            )}
          </div>
          {unreadChats.length === 0 ? (
            <div className="px-2.5 py-6 text-center text-xs text-text3">You're all caught up.</div>
          ) : (
            unreadChats.map((a) => {
              const ev = eventKindLabel(a.last_event?.kind)
              return (
                <button
                  key={a.chat_id}
                  onClick={() => openChat(a.chat_id)}
                  className="flex w-full items-start gap-2.5 border-b border-border bg-surface2 px-2.5 py-2 text-left last:border-b-0 hover:bg-surface3"
                >
                  <span className="mt-[5px] h-[7px] w-[7px] flex-none rounded-full" style={{ background: ev.dot }} />
                  <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="truncate text-xs leading-snug text-text">
                      <span className="font-semibold">{a.title || 'Chat'}</span> {ev.text}
                    </span>
                    <span className="text-2xs text-text3">
                      {a.unread} new{a.last_event?.at ? ` · ${ago(a.last_event.at)}` : ''}
                    </span>
                  </span>
                </button>
              )
            })
          )}
          <div className="px-2.5 pb-1 pt-2 text-2xs text-text3">Agent session events across your chats</div>
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
