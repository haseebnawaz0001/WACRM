<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toast } from 'vue-sonner'
import { MessageSquare, Loader2 } from 'lucide-vue-next'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

const fullName = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const isLoading = ref(false)

const organizationId = computed(() => (route.query.org as string) || '')

const handleRegister = async () => {
  if (!organizationId.value) {
    toast.error(t('auth.invitationRequired'))
    return
  }

  if (!fullName.value || !email.value || !password.value) {
    toast.error(t('auth.fillAllFields'))
    return
  }

  if (password.value !== confirmPassword.value) {
    toast.error(t('auth.passwordsMismatch'))
    return
  }

  if (password.value.length < 8) {
    toast.error(t('auth.passwordTooShort'))
    return
  }

  isLoading.value = true

  try {
    await authStore.register({
      full_name: fullName.value,
      email: email.value,
      password: password.value,
      organization_id: organizationId.value
    })
    toast.success(t('auth.registrationSuccess'))
    router.push('/')
  } catch (error: any) {
    const message = error.response?.data?.message || t('auth.registrationFailed')
    toast.error(message)
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <!-- Same shell as the sign-in page, down to the padding and the type scale.
       These are two halves of one front door, and they used to disagree about
       which panel, which heading size and which muted grey the product uses. -->
  <div class="min-h-screen flex items-center justify-center bg-background p-4">
    <div class="w-full max-w-md rounded-lg border border-white/[0.08] bg-white/[0.02] backdrop-blur light:bg-white light:border-gray-200 light:shadow-xl">
      <div class="p-8 space-y-1 text-center">
        <div class="flex justify-center mb-4">
          <div class="h-12 w-12 rounded-lg bg-primary flex items-center justify-center">
            <MessageSquare class="h-7 w-7 text-white" />
          </div>
        </div>
        <h2 class="text-2xl font-bold text-white light:text-gray-900">{{ $t('auth.createAccount') }}</h2>
        <p class="text-white/50 light:text-gray-500">
          {{ $t('auth.createAccountDesc') }}
        </p>
      </div>

      <!-- Arrived without an invitation: there is nothing to fill in. -->
      <template v-if="!organizationId">
        <div class="px-8 pb-4">
          <p class="text-sm text-center text-white/50 light:text-gray-500">
            {{ $t('auth.invitationRequired') }}
          </p>
        </div>
        <div class="px-8 pb-8">
          <RouterLink to="/login" class="block">
            <Button variant="outline" class="w-full">
              {{ $t('auth.signIn') }}
            </Button>
          </RouterLink>
        </div>
      </template>

      <!-- Came in on an invitation link. -->
      <form v-else @submit.prevent="handleRegister">
        <div class="px-8 pb-4 space-y-4">
          <div class="space-y-2">
            <Label for="fullName" class="text-white/70 light:text-gray-700">{{ $t('auth.fullName') }}</Label>
            <Input
              id="fullName"
              v-model="fullName"
              type="text"
              :placeholder="$t('auth.fullNamePlaceholder')"
              :disabled="isLoading"
              autocomplete="name"
            />
          </div>
          <div class="space-y-2">
            <Label for="email" class="text-white/70 light:text-gray-700">{{ $t('common.email') }}</Label>
            <Input
              id="email"
              v-model="email"
              type="email"
              :placeholder="$t('auth.emailPlaceholder')"
              :disabled="isLoading"
              autocomplete="email"
            />
          </div>
          <div class="space-y-2">
            <Label for="password" class="text-white/70 light:text-gray-700">{{ $t('auth.password') }}</Label>
            <Input
              id="password"
              v-model="password"
              type="password"
              :placeholder="$t('auth.passwordMinLength')"
              :disabled="isLoading"
              autocomplete="new-password"
            />
          </div>
          <div class="space-y-2">
            <Label for="confirmPassword" class="text-white/70 light:text-gray-700">{{ $t('auth.confirmPassword') }}</Label>
            <Input
              id="confirmPassword"
              v-model="confirmPassword"
              type="password"
              :placeholder="$t('auth.confirmPasswordPlaceholder')"
              :disabled="isLoading"
              autocomplete="new-password"
            />
          </div>
          <Button type="submit" class="w-full" :disabled="isLoading">
            <Loader2 v-if="isLoading" class="mr-2 h-4 w-4 animate-spin" />
            {{ $t('auth.createAccountBtn') }}
          </Button>
        </div>
      </form>

      <div v-if="organizationId" class="px-8 pb-8">
        <p class="text-sm text-center text-white/50 light:text-gray-500">
          {{ $t('auth.alreadyHaveAccount') }}
          <RouterLink to="/login" class="text-emerald-400 light:text-emerald-600 hover:underline">
            {{ $t('auth.signIn') }}
          </RouterLink>
        </p>
      </div>
    </div>
  </div>
</template>
