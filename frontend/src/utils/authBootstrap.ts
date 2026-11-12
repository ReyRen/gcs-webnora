/** Restore cached authentication only outside the fresh platform-ticket entry. */
export async function restoreBootstrapSession(options: {
  pathname: string
  basePath: string
  hasToken: boolean
  restore: () => Promise<unknown>
}): Promise<void> {
  const entry = `${options.basePath.replace(/\/+$/, '')}/sso/platform`
  if (!options.hasToken || options.pathname.replace(/\/+$/, '') === entry) return
  await options.restore()
}
