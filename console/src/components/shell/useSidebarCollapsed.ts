import { useEffect, useState } from 'react'

const STORAGE_KEY = 'hub-console-sidebar-collapsed'

/**
 * Persisted collapse state for the app sidebar (icon-rail vs full).
 * Mirrors the localStorage pattern used by the theme provider; local to the
 * Sidebar since nothing else reads sidebar width (the AppShell flex-row reflows
 * automatically on the width change).
 */
export function useSidebarCollapsed(): [boolean, () => void] {
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(STORAGE_KEY) === '1'
    } catch {
      return false
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0')
    } catch {
      /* private mode — ignore */
    }
  }, [collapsed])

  return [collapsed, () => setCollapsed((c) => !c)]
}
