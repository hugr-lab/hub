import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import './index.css'
import { App } from './App'
import { ThemeProvider } from './lib/theme'
import { ToastProvider } from './components/ui'
import { queryClient } from './lib/query'
import { AppModeProvider, baseForMode, detectAppMode } from './lib/appMode'

// One build serves two surfaces: /console (management) and /app (personal
// workspace). The router basename tracks the mount; assets stay under /console/.
const mode = detectAppMode()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <ToastProvider>
          <BrowserRouter basename={baseForMode(mode)}>
            <AppModeProvider mode={mode}>
              <App />
            </AppModeProvider>
          </BrowserRouter>
        </ToastProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
)
