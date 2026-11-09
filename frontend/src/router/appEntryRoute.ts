import type { RouteRecordRaw } from 'vue-router'
import { isPlatformSsoOnlyLogin } from '../utils/platformSso'

// beforeEnter runs after the global guard has loaded the login policy.
// Always start a fresh handshake at the iframe root, including when a previous
// account's token is still in storage.
export const appEntryRoute: RouteRecordRaw = {
  path: '/',
  name: 'appEntry',
  component: { render: () => null },
  meta: { requiresAuth: false, requiresInit: false },
  beforeEnter: () => isPlatformSsoOnlyLogin() ? '/sso/platform' : '/platform/knowledge-bases',
}
