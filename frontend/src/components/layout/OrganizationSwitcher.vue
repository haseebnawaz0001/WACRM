<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useOrganizationsStore } from '@/stores/organizations'
import { useAuthStore } from '@/stores/auth'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { organizationsService } from '@/services/api'
import { toast } from 'vue-sonner'
import { Building2, Plus, Loader2 } from 'lucide-vue-next'

defineProps<{
  collapsed?: boolean
}>()

const emit = defineEmits<{
  expand: []
}>()

const { t } = useI18n()
const organizationsStore = useOrganizationsStore()
const authStore = useAuthStore()

const isSuperAdmin = computed(() => authStore.user?.is_super_admin || false)
const canCreateOrg = computed(() => authStore.hasPermission('organizations', 'write'))

const shouldShowSwitcher = computed(() =>
  isSuperAdmin.value || organizationsStore.isMultiOrg
)

// Build the org list depending on user type
const orgList = computed(() => {
  if (isSuperAdmin.value) {
    return organizationsStore.organizations.map(org => ({ id: org.id, name: org.name }))
  }
  return organizationsStore.myOrganizations.map(org => ({ id: org.organization_id, name: org.name }))
})

const currentOrgId = computed(() => {
  if (isSuperAdmin.value) {
    return organizationsStore.selectedOrgId || ''
  }
  return authStore.user?.organization_id || ''
})

const currentOrgName = computed(() =>
  orgList.value.find(org => org.id === currentOrgId.value)?.name || t('nav.organization')
)

onMounted(async () => {
  // Fetch user's org memberships for all authenticated users
  await organizationsStore.fetchMyOrganizations()

  if (isSuperAdmin.value) {
    organizationsStore.init()
    await organizationsStore.fetchOrganizations()

    // If no org selected, default to user's own org
    if (!organizationsStore.selectedOrgId && authStore.user?.organization_id) {
      organizationsStore.selectOrganization(authStore.user.organization_id)
    }
  }
})

// Watch for auth changes
watch(() => authStore.user?.is_super_admin, async (superAdmin) => {
  if (superAdmin) {
    organizationsStore.init()
    await organizationsStore.fetchOrganizations()
  }
})

const handleOrgChange = async (value: string | number | bigint | Record<string, any> | null) => {
  if (!value || typeof value !== 'string') return

  // Everyone goes through switch-org, super admins included: the X-Organization-ID
  // header alone only scopes axios calls. The WebSocket token, <img src> media and
  // window.open previews carry no headers, so the JWT itself has to name the target
  // org or those keep talking to the previous one.
  try {
    await authStore.switchOrg(value)
    organizationsStore.selectOrganization(value)
    window.location.reload()
  } catch {
    // If switch fails, don't reload
  }
}

// Create org dialog
const isCreateDialogOpen = ref(false)
const newOrgName = ref('')
const isCreating = ref(false)

async function submitCreateOrg() {
  if (!newOrgName.value.trim()) return
  isCreating.value = true
  try {
    await organizationsService.create({ name: newOrgName.value.trim() })
    toast.success(t('organizations.created'))
    isCreateDialogOpen.value = false
    newOrgName.value = ''
    await refreshOrgs()
  } catch {
    toast.error(t('organizations.createFailed'))
  } finally {
    isCreating.value = false
  }
}

const refreshOrgs = async () => {
  if (isSuperAdmin.value) {
    await organizationsStore.fetchOrganizations()
  } else {
    await organizationsStore.fetchMyOrganizations()
  }
}
</script>

<template>
  <div v-if="shouldShowSwitcher" class="px-2 py-2 border-b border-white/[0.08] light:border-gray-200">
    <div v-if="!collapsed" class="space-y-1">
      <div class="flex h-6 items-center justify-between">
        <span class="px-2.5 text-[10px] font-semibold uppercase tracking-wider text-white/45 light:text-gray-500">
          {{ t('nav.organization') }}
        </span>
        <Tooltip v-if="canCreateOrg">
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="icon"
              class="h-6 w-6 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100"
              :aria-label="t('organizations.createNew')"
              @click="isCreateDialogOpen = true"
            >
              <Plus class="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="right">{{ t('organizations.createNew') }}</TooltipContent>
        </Tooltip>
      </div>
      <Select
        v-if="orgList.length > 0"
        :model-value="currentOrgId"
        @update:model-value="handleOrgChange"
      >
        <SelectTrigger class="h-8 text-[13px]" :aria-label="t('nav.organization')">
          <SelectValue :placeholder="t('nav.selectOrganization')" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem
            v-for="org in orgList"
            :key="org.id"
            :value="org.id"
          >
            <div class="flex items-center gap-2">
              <Building2 class="h-3.5 w-3.5 text-muted-foreground" />
              <span>{{ org.name }}</span>
            </div>
          </SelectItem>
        </SelectContent>
      </Select>
      <div v-else-if="organizationsStore.loading" class="flex h-8 items-center gap-2 px-2.5 text-[12px] text-muted-foreground">
        <Loader2 class="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
        {{ t('common.loading') }}
      </div>
      <div v-else-if="organizationsStore.error" class="px-2.5 text-[12px] text-destructive">
        {{ organizationsStore.error }}
      </div>
      <div v-else class="px-2.5 text-[12px] text-muted-foreground">
        {{ t('nav.noOrganizations') }}
      </div>
    </div>

    <!-- Collapsed: the switcher needs room, so the button opens the sidebar -->
    <div v-else class="flex justify-center">
      <Tooltip :delay-duration="0">
        <TooltipTrigger as-child>
          <Button
            variant="ghost"
            size="icon"
            class="h-8 w-8 text-white/55 hover:text-white hover:bg-white/[0.04] light:text-gray-600 light:hover:text-gray-900 light:hover:bg-gray-100/70"
            :aria-label="`${t('nav.organization')}: ${currentOrgName}`"
            @click="emit('expand')"
          >
            <Building2 class="h-4 w-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent side="right" :side-offset="10">{{ currentOrgName }}</TooltipContent>
      </Tooltip>
    </div>
  </div>

  <!-- Create Org Dialog -->
  <Dialog v-model:open="isCreateDialogOpen">
    <DialogContent class="max-w-sm">
      <DialogHeader>
        <DialogTitle>{{ t('organizations.createTitle') }}</DialogTitle>
        <DialogDescription>{{ t('organizations.createDesc') }}</DialogDescription>
      </DialogHeader>
      <div class="py-4">
        <Input
          v-model="newOrgName"
          :placeholder="t('organizations.namePlaceholder')"
          @keydown.enter="submitCreateOrg"
        />
      </div>
      <DialogFooter>
        <Button variant="outline" @click="isCreateDialogOpen = false">{{ t('common.cancel') }}</Button>
        <Button @click="submitCreateOrg" :disabled="isCreating || !newOrgName.trim()">
          <Loader2 v-if="isCreating" class="h-4 w-4 mr-2 animate-spin" />
          {{ t('common.create') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
