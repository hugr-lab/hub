import { useEffect } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider } from './lib/auth'
import { useSession } from './lib/session'
import { baseForMode, useAppMode } from './lib/appMode'
import { AuthGate } from './components/shell/AuthGate'
import { AppShell } from './components/shell/AppShell'
import { RequireAdmin } from './components/shell/RequireAdmin'
import { defaultRoute } from './app/nav'
import { ChatScreen } from './screens/ChatScreen'
import { MonitoringScreen } from './screens/MonitoringScreen'
import { AgentsScreen } from './screens/AgentsScreen'
import { SkillsScreen } from './screens/SkillsScreen'
import { MeScreen } from './screens/MeScreen'
import { DataSourcesScreen } from './screens/platform/DataSourcesScreen'
import { DuckLakeScreen } from './screens/platform/DuckLakeScreen'
import { CatalogsScreen } from './screens/platform/CatalogsScreen'
import { RolesScreen } from './screens/platform/RolesScreen'
import { ApiKeysScreen } from './screens/platform/ApiKeysScreen'
import { SchemaExplorerScreen } from './screens/platform/SchemaExplorerScreen'
import { AgentTypesScreen } from './screens/platform/AgentTypesScreen'

function RootRedirect() {
  const { persona } = useSession()
  return <Navigate to={defaultRoute(persona)} replace />
}

/**
 * The /console management surface is admin-only (gated on the `hub:management.admin`
 * capability, surfaced as `session.isAdmin`). A non-admin who lands here — a
 * direct link or a post-login return — is bounced to their /app workspace. The
 * server already enforces admin on every management data endpoint; this is the
 * UX gate so non-admins never see the console shell. No-op in /app mode.
 */
function ConsoleGuard({ children }: { children: React.ReactNode }) {
  const mode = useAppMode()
  const { isAdmin } = useSession()
  const bounce = mode === 'console' && !isAdmin
  useEffect(() => {
    if (bounce) window.location.replace(baseForMode('app') + '/')
  }, [bounce])
  if (bounce) return null
  return <>{children}</>
}

function AppRoutes() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<RootRedirect />} />
        <Route
          path="/monitoring"
          element={
            <RequireAdmin>
              <MonitoringScreen />
            </RequireAdmin>
          }
        />
        <Route path="/chat" element={<ChatScreen />} />
        <Route path="/chat/:chatId" element={<ChatScreen />} />
        <Route path="/agents" element={<AgentsScreen />} />
        <Route path="/skills" element={<SkillsScreen />} />
        <Route
          path="/platform/data-sources"
          element={
            <RequireAdmin>
              <DataSourcesScreen />
            </RequireAdmin>
          }
        />
        <Route
          path="/platform/ducklake"
          element={
            <RequireAdmin>
              <DuckLakeScreen />
            </RequireAdmin>
          }
        />
        <Route
          path="/platform/catalogs"
          element={
            <RequireAdmin>
              <CatalogsScreen />
            </RequireAdmin>
          }
        />
        <Route
          path="/platform/schema"
          element={
            <RequireAdmin>
              <SchemaExplorerScreen />
            </RequireAdmin>
          }
        />
        <Route
          path="/platform/roles"
          element={
            <RequireAdmin>
              <RolesScreen />
            </RequireAdmin>
          }
        />
        <Route
          path="/platform/keys"
          element={
            <RequireAdmin>
              <ApiKeysScreen />
            </RequireAdmin>
          }
        />
        <Route
          path="/platform/agent-types"
          element={
            <RequireAdmin>
              <AgentTypesScreen />
            </RequireAdmin>
          }
        />
        <Route path="/me" element={<MeScreen />} />
        <Route path="*" element={<RootRedirect />} />
      </Route>
    </Routes>
  )
}

export function App() {
  return (
    <AuthProvider>
      <AuthGate>
        <ConsoleGuard>
          <AppRoutes />
        </ConsoleGuard>
      </AuthGate>
    </AuthProvider>
  )
}
