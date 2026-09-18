<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { toast } from 'vue-sonner'
import { User, Eye, EyeOff, Loader2, Bell } from 'lucide-vue-next'
import { usersService } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { PageHeader } from '@/components/shared'
import { getErrorMessage } from '@/lib/api-utils'

const { t } = useI18n()
const authStore = useAuthStore()
const isChangingPassword = ref(false)
const showCurrentPassword = ref(false)
const showNewPassword = ref(false)
const showConfirmPassword = ref(false)

const passwordForm = ref({
  current_password: '',
  new_password: '',
  confirm_password: ''
})

/**
 * Per-type notification preferences (plan 00, F5).
 *
 * notify.Send has read these since the bell shipped, and nothing could write
 * them: every user sat on the default for every type, so an agent getting a
 * sound for each of forty campaign updates could only mute the browser tab.
 *
 * Both default to on. A type somebody has never expressed an opinion about
 * should reach them — the alternative is silently withholding something they
 * are waiting for.
 */
const notificationTypes = [
  'task_due',
  'task_overdue',
  'task_assigned',
  'conversation_assigned',
  'conversation_snooze_ended',
  'sla_escalation',
  'automation',
  'merge_suggestions',
  'deal_rotting',
  'campaign_paused'
] as const

type NotificationPrefs = Record<string, { in_app: boolean; sound: boolean }>

const prefs = ref<NotificationPrefs>({})
const isSavingPrefs = ref(false)

function loadPrefs() {
  const stored = (authStore.user?.settings?.notifications ?? {}) as NotificationPrefs
  const next: NotificationPrefs = {}
  for (const type of notificationTypes) {
    next[type] = {
      in_app: stored[type]?.in_app ?? true,
      sound: stored[type]?.sound ?? true
    }
  }
  prefs.value = next
}

async function savePrefs() {
  isSavingPrefs.value = true
  try {
    const settings = authStore.user?.settings ?? {}
    // The other flags are sent unchanged: this endpoint replaces them, so
    // omitting one would quietly turn it off.
    await usersService.updateSettings({
      email_notifications: settings.email_notifications ?? true,
      new_message_alerts: settings.new_message_alerts ?? true,
      campaign_updates: settings.campaign_updates ?? true,
      timezone: settings.timezone,
      notifications: prefs.value
    })
    if (authStore.user) {
      authStore.user.settings = { ...settings, notifications: prefs.value }
    }
    toast.success(t('profile.notificationsSaved'))
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('profile.notificationsSaveFailed')))
  } finally {
    isSavingPrefs.value = false
  }
}

onMounted(loadPrefs)

async function changePassword() {
  // Validate passwords match
  if (passwordForm.value.new_password !== passwordForm.value.confirm_password) {
    toast.error(t('profile.passwordMismatch'))
    return
  }

  // Validate password length
  if (passwordForm.value.new_password.length < 6) {
    toast.error(t('profile.passwordTooShort'))
    return
  }

  isChangingPassword.value = true
  try {
    await usersService.changePassword({
      current_password: passwordForm.value.current_password,
      new_password: passwordForm.value.new_password
    })
    toast.success(t('profile.passwordChanged'))
    // Clear the form
    passwordForm.value = {
      current_password: '',
      new_password: '',
      confirm_password: ''
    }
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('profile.passwordChangeFailed')))
  } finally {
    isChangingPassword.value = false
  }
}
</script>

<template>
  <div class="flex flex-col h-full">
    <PageHeader
      :title="$t('profile.title')"
      :description="$t('profile.description')"
      :icon="User"
    />

    <!-- Content -->
    <ScrollArea class="flex-1">
      <div class="p-6 space-y-6 max-w-2xl mx-auto">
        <!-- User Info -->
        <Card>
          <CardHeader>
            <CardTitle>{{ $t('profile.accountInfo') }}</CardTitle>
            <CardDescription>{{ $t('profile.accountInfoDesc') }}</CardDescription>
          </CardHeader>
          <CardContent class="space-y-4">
            <div class="grid grid-cols-2 gap-4">
              <div>
                <Label class="text-muted-foreground">{{ $t('common.name') }}</Label>
                <p class="font-medium">{{ authStore.user?.full_name }}</p>
              </div>
              <div>
                <Label class="text-muted-foreground">{{ $t('common.email') }}</Label>
                <p class="font-medium">{{ authStore.user?.email }}</p>
              </div>
              <div>
                <Label class="text-muted-foreground">{{ $t('users.role') }}</Label>
                <p class="font-medium capitalize">{{ authStore.user?.role?.name }}</p>
              </div>
            </div>
          </CardContent>
        </Card>

        <!-- Notifications (plan 00, F5) -->
        <Card>
          <CardHeader>
            <CardTitle class="flex items-center gap-2">
              <Bell class="h-4 w-4" />{{ $t('profile.notifications') }}
            </CardTitle>
            <CardDescription>{{ $t('profile.notificationsDesc') }}</CardDescription>
          </CardHeader>
          <CardContent class="space-y-1">
            <div class="flex items-center justify-between pb-2 text-xs text-muted-foreground">
              <span>{{ $t('profile.notificationType') }}</span>
              <span class="flex items-center gap-6">
                <span class="w-16 text-center">{{ $t('profile.inApp') }}</span>
                <span class="w-16 text-center">{{ $t('profile.sound') }}</span>
              </span>
            </div>
            <Separator />
            <div
              v-for="type in notificationTypes"
              :key="type"
              class="flex items-center justify-between py-2"
            >
              <Label :for="`notif-${type}`" class="font-normal">
                {{ $t(`profile.notificationTypes.${type}`) }}
              </Label>
              <span class="flex items-center gap-6">
                <span class="w-16 flex justify-center">
                  <Switch
                    :id="`notif-${type}`"
                    v-model:checked="prefs[type].in_app"
                    @update:checked="savePrefs"
                  />
                </span>
                <span class="w-16 flex justify-center">
                  <!-- A sound with no notification would be a noise with
                       nothing behind it. -->
                  <Switch
                    v-model:checked="prefs[type].sound"
                    :disabled="!prefs[type].in_app"
                    @update:checked="savePrefs"
                  />
                </span>
              </span>
            </div>
            <p v-if="isSavingPrefs" class="pt-2 text-xs text-muted-foreground">
              {{ $t('common.saving') }}
            </p>
          </CardContent>
        </Card>

        <!-- Change Password -->
        <Card>
          <CardHeader>
            <CardTitle>{{ $t('profile.changePassword') }}</CardTitle>
            <CardDescription>{{ $t('profile.changePasswordDesc') }}</CardDescription>
          </CardHeader>
          <CardContent class="space-y-4">
            <div class="space-y-2">
              <Label for="current_password">{{ $t('profile.currentPassword') }}</Label>
              <div class="relative">
                <Input
                  id="current_password"
                  v-model="passwordForm.current_password"
                  :type="showCurrentPassword ? 'text' : 'password'"
                  :placeholder="$t('profile.currentPasswordPlaceholder')"
                />
                <button
                  type="button"
                  class="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  @click="showCurrentPassword = !showCurrentPassword"
                >
                  <Eye v-if="!showCurrentPassword" class="h-4 w-4" />
                  <EyeOff v-else class="h-4 w-4" />
                </button>
              </div>
            </div>
            <div class="space-y-2">
              <Label for="new_password">{{ $t('profile.newPassword') }}</Label>
              <div class="relative">
                <Input
                  id="new_password"
                  v-model="passwordForm.new_password"
                  :type="showNewPassword ? 'text' : 'password'"
                  :placeholder="$t('profile.newPasswordPlaceholder')"
                />
                <button
                  type="button"
                  class="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  @click="showNewPassword = !showNewPassword"
                >
                  <Eye v-if="!showNewPassword" class="h-4 w-4" />
                  <EyeOff v-else class="h-4 w-4" />
                </button>
              </div>
              <p class="text-xs text-muted-foreground">{{ $t('profile.passwordMinLength') }}</p>
            </div>
            <div class="space-y-2">
              <Label for="confirm_password">{{ $t('profile.confirmNewPassword') }}</Label>
              <div class="relative">
                <Input
                  id="confirm_password"
                  v-model="passwordForm.confirm_password"
                  :type="showConfirmPassword ? 'text' : 'password'"
                  :placeholder="$t('profile.confirmNewPasswordPlaceholder')"
                />
                <button
                  type="button"
                  class="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  @click="showConfirmPassword = !showConfirmPassword"
                >
                  <Eye v-if="!showConfirmPassword" class="h-4 w-4" />
                  <EyeOff v-else class="h-4 w-4" />
                </button>
              </div>
            </div>
            <div class="flex justify-end">
              <Button variant="outline" size="sm" @click="changePassword" :disabled="isChangingPassword">
                <Loader2 v-if="isChangingPassword" class="mr-2 h-4 w-4 animate-spin" />
                {{ $t('profile.changePassword') }}
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    </ScrollArea>
  </div>
</template>
