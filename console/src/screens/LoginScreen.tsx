import { Button } from '@/components/ui'

export function LoginScreen({ onLogin, error }: { onLogin: () => void; error?: string }) {
  return (
    <div className="flex h-screen w-full items-center justify-center bg-bg">
      <div className="flex w-[360px] max-w-[calc(100vw-2rem)] flex-col items-center gap-[18px] rounded-modal border border-border bg-surface p-8 shadow-card animate-fadeUp">
        <img src="/console/logo.svg" alt="hugr" className="h-[52px] w-[52px]" />
        <div className="flex flex-col gap-1 text-center">
          <div className="text-lg font-bold tracking-[-0.01em]">Hugr Hub Console</div>
          <div className="text-sm text-text2">
            Sign in with your organization identity to manage the data mesh and agent fleet.
          </div>
        </div>
        {error && (
          <div className="w-full rounded-btn border border-red/40 bg-red-soft px-3 py-2 text-xs text-text">
            {error}
          </div>
        )}
        <Button variant="primary" size="lg" className="w-full" onClick={onLogin}>
          Sign in
        </Button>
        <div className="text-center text-xs text-text3">
          OIDC Authorization Code + PKCE · issuer from /console/config.json
        </div>
      </div>
    </div>
  )
}
