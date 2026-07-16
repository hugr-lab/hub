import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider } from './lib/auth'
import { useSession } from './lib/session'
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
import { CatalogsScreen } from './screens/platform/CatalogsScreen'
import { RolesScreen } from './screens/platform/RolesScreen'
import { ApiKeysScreen } from './screens/platform/ApiKeysScreen'
import { SchemaExplorerScreen } from './screens/platform/SchemaExplorerScreen'

function RootRedirect() {
  const { persona } = useSession()
  return <Navigate to={defaultRoute(persona)} replace />
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
        <AppRoutes />
      </AuthGate>
    </AuthProvider>
  )
}
