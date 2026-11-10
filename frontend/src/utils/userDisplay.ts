import { KNOWLEDGE_WORKSPACE } from '../config/knowledgeWorkspace'

type AccountLabel = { username?: string; email?: string }

/** Prefer the platform-provided email without changing the account's internal username. */
export function userDisplayName(user: AccountLabel | null | undefined, preferEmail = KNOWLEDGE_WORKSPACE): string {
  const email = user?.email?.trim() || ''
  const usableEmail = email.includes('@') && !email.toLowerCase().endsWith('@platform-sso.invalid')
  if (preferEmail && usableEmail) return email
  return user?.username?.trim() || (usableEmail ? email : '')
}

export function userSecondaryLabel(user: AccountLabel): string {
  const email = user.email?.trim() || ''
  return email && email !== userDisplayName(user) && !email.toLowerCase().endsWith('@platform-sso.invalid') ? email : ''
}
