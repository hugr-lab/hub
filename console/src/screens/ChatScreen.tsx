import { useParams, useNavigate } from 'react-router-dom'
import { ChatApp } from '@/chat/ChatApp'
import { runtimeConfig } from '@/lib/config'
import { getAccessToken } from '@/lib/auth-token'
import { isDemoMode } from '@/lib/demo'
import { useTheme } from '@/lib/theme'

function safeApiBase(): string {
  try {
    return runtimeConfig().api_base
  } catch {
    return '' // demo mode / config not loaded → same origin
  }
}

/** SPA route wrapper: binds the chat microfrontend to the URL + session token. */
export function ChatScreen() {
  const { chatId } = useParams()
  const navigate = useNavigate()
  const { theme } = useTheme()

  return (
    <ChatApp
      apiBase={safeApiBase()}
      getToken={getAccessToken}
      demo={isDemoMode()}
      theme={theme}
      chatId={chatId ?? null}
      showRail
      onChatChange={(id) => navigate(`/chat/${id}`)}
    />
  )
}
