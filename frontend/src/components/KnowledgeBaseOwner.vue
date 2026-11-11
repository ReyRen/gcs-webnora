<template>
  <div class="kb-owner" :title="[ownerAccount, sourceName].filter(Boolean).join(' · ')">
    <span class="kb-owner-account">{{ ownerAccount }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ creatorId?: string; account?: string; sourceName?: string }>()
const auth = useAuthStore()
const { t } = useI18n()
const ownerAccount = computed(() => {
  if (props.account?.trim()) return props.account.trim()
  // A just-created card can precede list enrichment. Only fall back to the
  // signed-in account when its ID matches the actual KB creator.
  if (props.creatorId && props.creatorId === auth.user?.id) {
    const email = auth.user.email?.trim()
    if (email && !email.toLowerCase().endsWith('@platform-sso.invalid')) return email
    if (auth.user.username?.trim()) return auth.user.username.trim()
  }
  return t('knowledgeList.ownerUnknown')
})
</script>

<style scoped lang="less">
.kb-owner {
  display: flex;
  align-items: center;
  min-width: 0;
  font-size: var(--app-text-sm);
  line-height: 20px;
  color: var(--td-text-color-secondary);
}
.kb-owner-account { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
