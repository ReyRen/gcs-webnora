import { getApiBaseUrl } from './api-base'

const marker = 'weknora_platform_sso'
export type PlatformSsoConfig = { enabled: boolean; sso_only?: boolean; client_id?: string; parent_origins?: string[] }
let currentConfig: PlatformSsoConfig | undefined

export function isPlatformSsoOnlyLogin(): boolean {
  return currentConfig?.enabled === true && currentConfig.sso_only === true
}

export function getLoginRoute(loggedOut = false): string {
  if (!isPlatformSsoOnlyLogin() && !isPlatformSsoFrame()) return '/login'
  return loggedOut ? '/sso/platform?logged_out=1' : '/sso/platform'
}

export function platformSsoEntryRedirect(path: string, pendingOIDCCallback = false): string | undefined {
  const normalized = path.replace(/\/+$/, '').toLowerCase()
  if (isPlatformSsoOnlyLogin() && normalized !== '/404'
    && (normalized === '/login' || normalized === '/register' || window.parent === window || pendingOIDCCallback)) {
    return '/404'
  }
}

export function isPlatformSsoFrame(): boolean {
  try { return window.parent !== window && sessionStorage.getItem(marker) === '1' } catch { return false }
}

export function markPlatformSsoFrame() { sessionStorage.setItem(marker, '1') }

export function platformSsoMessageTargets(origins: string[]): string[] {
  const parentOrigin = window.location?.ancestorOrigins?.[0]
  // Chromium/Electron can expose the direct parent's origin without cross-origin
  // access to parent.location. A referrer policy may redact it as "null".
  if (parentOrigin && parentOrigin !== 'null') {
    return origins.includes(parentOrigin) ? [parentOrigin] : []
  }
  // Older browsers or a redacted ancestor list: only try configured origins.
  return [...new Set(origins)]
}

export function postPlatformSsoMessage(origins: string[], type: string, nonce: string, extra: Record<string, unknown> = {}) {
  // Temporary handshake diagnostics: ready carries no credentials. Token
  // reception and authenticated/error messages still use the exact allowlist.
  const targets = type === 'weknora:sso-ready' ? ['*'] : platformSsoMessageTargets(origins)
  for (const origin of targets) {
    window.parent.postMessage({ type, nonce, ...extra }, origin)
    if (type === 'weknora:sso-ready') {
      // This records a postMessage call, not proof of delivery to the parent.
      console.info('[SSO] ready-posted', { type, targetOrigin: origin, framed: window.parent !== window })
    }
  }
  return targets.length
}

export function trustedPlatformMessage(event: MessageEvent, origins: string[], nonce?: string): boolean {
  return window.parent !== window && event.source === window.parent && origins.includes(event.origin)
    && !!event.data && typeof event.data === 'object' && (!nonce || event.data.nonce === nonce)
}

export async function getPlatformSsoConfig(): Promise<PlatformSsoConfig> {
  const response = await fetch(`${getApiBaseUrl()}/api/v1/auth/platform-sso/config`, { cache: 'no-store' })
  if (!response.ok) throw new Error('无法读取平台登录配置')
  currentConfig = await response.json()
  return currentConfig!
}

/** Kept at App level so logout messages continue working after leaving the entry route. */
export function installPlatformSsoLogout(onLogout: () => Promise<void>): () => void {
  let stopped = false
  let origins: string[] = []
  void getPlatformSsoConfig().then(config => { if (!stopped && config.enabled) origins = config.parent_origins || [] }).catch(() => {})
  const receive = (event: MessageEvent) => {
    if (isPlatformSsoFrame() && trustedPlatformMessage(event, origins) && event.data.type === 'weknora:sso-logout') {
      window.dispatchEvent(new Event('weknora:platform-logout'))
      void onLogout()
    }
  }
  window.addEventListener('message', receive)
  return () => { stopped = true; window.removeEventListener('message', receive) }
}
