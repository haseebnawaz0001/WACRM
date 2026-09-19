<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { chatbotService } from '@/services/api'
import { toast } from 'vue-sonner'
import { PageHeader, ConfirmDialog, ErrorState } from '@/components/shared'
import { getErrorMessage } from '@/lib/api-utils'
import {
  Bot,
  Key,
  Workflow,
  Sparkles,
  Power,
  Settings,
  Clock,
  ChevronRight
} from 'lucide-vue-next'

const { t } = useI18n()

interface ChatbotSettings {
  enabled: boolean
  greeting_message: string
  fallback_message: string
  session_timeout_minutes: number
  ai_enabled: boolean
  ai_provider: string
}

interface Stats {
  total_sessions: number
  active_sessions: number
  messages_handled: number
  ai_responses: number
  agent_transfers: number
  keywords_count: number
  flows_count: number
  ai_contexts_count: number
}

const settings = ref<ChatbotSettings>({
  enabled: false,
  greeting_message: '',
  fallback_message: '',
  session_timeout_minutes: 30,
  ai_enabled: false,
  ai_provider: ''
})

const stats = ref<Stats>({
  total_sessions: 0,
  active_sessions: 0,
  messages_handled: 0,
  ai_responses: 0,
  agent_transfers: 0,
  keywords_count: 0,
  flows_count: 0,
  ai_contexts_count: 0
})

const isLoading = ref(true)
const isToggling = ref(false)
const error = ref(false)
const showToggleConfirm = ref(false)

onMounted(async () => {
  try {
    const response = await chatbotService.getSettings()
    // API response is wrapped in { status: "success", data: { settings: {...}, stats: {...} } }
    const data = response.data.data || response.data
    settings.value = data.settings || settings.value
    stats.value = data.stats || stats.value
  } catch (err) {
    console.error('Failed to load chatbot settings:', err)
    error.value = true
  } finally {
    isLoading.value = false
  }
})

async function toggleChatbot() {
  isToggling.value = true
  try {
    const newState = !settings.value.enabled
    await chatbotService.updateSettings({ enabled: newState })
    settings.value.enabled = newState
    toast.success(newState ? t('common.enabledSuccess', { resource: t('resources.Chatbot') }) : t('common.disabledSuccess', { resource: t('resources.Chatbot') }))
  } catch (err: any) {
    toast.error(getErrorMessage(err, t('common.failedToggle', { resource: t('resources.chatbot') })))
  } finally {
    isToggling.value = false
    showToggleConfirm.value = false
  }
}

async function retryFetch() {
  isLoading.value = true
  error.value = false
  try {
    const response = await chatbotService.getSettings()
    const data = response.data.data || response.data
    settings.value = data.settings || settings.value
    stats.value = data.stats || stats.value
  } catch (err) {
    console.error('Failed to load chatbot settings:', err)
    error.value = true
  } finally {
    isLoading.value = false
  }
}

const statCards = computed(() => [
  { title: t('chatbot.totalSessions'), key: 'total_sessions' },
  { title: t('chatbot.activeSessions'), key: 'active_sessions' },
  { title: t('chatbot.messagesHandled'), key: 'messages_handled' },
  { title: t('chatbot.aiResponses'), key: 'ai_responses' }
])

/**
 * The three places this page sends you, as a list.
 *
 * They were three same-size cards of icon, heading and prose — the shape a
 * page reaches for when it has nothing to structure. They are destinations
 * with a count each, and a list says that in a third of the height.
 */
const destinations = computed(() => [
  {
    to: '/chatbot/keywords', icon: Key,
    title: t('chatbot.keywordRules'), desc: t('chatbot.keywordRulesDesc'),
    count: t('chatbot.rulesConfigured', { count: stats.value.keywords_count }),
  },
  {
    to: '/chatbot/flows', icon: Workflow,
    title: t('chatbot.conversationFlows'), desc: t('chatbot.flowsDesc'),
    count: t('chatbot.flowsCreated', { count: stats.value.flows_count }),
  },
  {
    to: '/chatbot/ai', icon: Sparkles,
    title: t('chatbot.aiContexts'), desc: t('chatbot.aiContextsDesc'),
    count: t('chatbot.contextsActive', { count: stats.value.ai_contexts_count }),
  },
])
</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <PageHeader
      :title="$t('chatbot.title')"
      :description="$t('chatbot.subtitle')"
      :icon="Bot"
    >
      <template #status>
        <Badge
          :class="settings.enabled ? 'bg-emerald-500/20 text-emerald-400 light:bg-emerald-100 light:text-emerald-700' : 'bg-white/[0.08] text-white/50 light:bg-gray-100 light:text-gray-500'"
        >
          {{ settings.enabled ? $t('chatbot.active') : $t('chatbot.inactive') }}
        </Badge>
      </template>
      <template #actions>
          <Button
            variant="outline"
            size="sm"
            @click="showToggleConfirm = true"
            :disabled="isToggling"
            :class="settings.enabled ? 'border-red-500/50 text-red-400 hover:bg-red-500/10' : 'border-emerald-500/50 text-emerald-400 hover:bg-emerald-500/10'"
          >
            <Power class="h-4 w-4 mr-2" />
            {{ settings.enabled ? $t('chatbot.disable') : $t('chatbot.enable') }}
          </Button>
      </template>
    </PageHeader>

    <!-- Confirm Toggle Dialog -->
    <ConfirmDialog
      v-model:open="showToggleConfirm"
      :title="settings.enabled ? $t('chatbot.confirmDisableTitle') : $t('chatbot.confirmEnableTitle')"
      :description="settings.enabled ? $t('chatbot.confirmDisableDescription') : $t('chatbot.confirmEnableDescription')"
      :confirm-label="settings.enabled ? $t('chatbot.disable') : $t('chatbot.enable')"
      :variant="settings.enabled ? 'destructive' : 'default'"
      :is-submitting="isToggling"
      @confirm="toggleChatbot"
    />

    <!-- Error State -->
    <ErrorState
      v-if="error && !isLoading"
      :title="$t('chatbot.fetchErrorTitle')"
      :description="$t('chatbot.fetchErrorDescription')"
      :retry-label="$t('common.retry')"
      class="flex-1"
      @retry="retryFetch"
    />

    <!-- Content -->
    <ScrollArea v-else class="flex-1">
      <div class="p-6 space-y-6">
        <!--
          Four counts, stated once.

          These were four cards, each with a 40px tile in a hue of its own —
          blue, green, purple, orange, chosen per metric and meaning nothing —
          above a 3xl number, two of which are usually zero. One strip of
          hairline-separated figures says the same thing in a quarter of the
          room and does not imply that sessions are blue.
        -->
        <div class="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-white/[0.08] bg-white/[0.08] sm:grid-cols-4 light:border-gray-200 light:bg-gray-200">
          <div
            v-for="card in statCards"
            :key="card.key"
            class="bg-[#0a0a0b] px-4 py-3 light:bg-white"
          >
            <div class="text-xs text-muted-foreground">{{ card.title }}</div>
            <Skeleton v-if="isLoading" class="mt-1.5 h-7 w-14" />
            <div v-else class="mt-0.5 text-2xl font-semibold tabular-nums">
              {{ stats[card.key as keyof Stats].toLocaleString() }}
            </div>
          </div>
        </div>

        <!-- Where to go next. -->
        <div class="overflow-hidden rounded-lg border border-white/[0.08] light:border-gray-200">
          <RouterLink
            v-for="d in destinations"
            :key="d.to"
            :to="d.to"
            class="flex items-center gap-3 border-b border-white/[0.06] px-4 py-3 transition-colors last:border-b-0 hover:bg-white/[0.03] light:border-gray-200 light:hover:bg-gray-50"
          >
            <component :is="d.icon" class="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <div class="min-w-0 flex-1">
              <h3 class="font-medium">{{ d.title }}</h3>
              <p class="truncate text-sm text-muted-foreground">{{ d.desc }}</p>
            </div>
            <span class="shrink-0 text-sm tabular-nums text-muted-foreground">{{ d.count }}</span>
            <ChevronRight class="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          </RouterLink>
        </div>

        <!-- Current Settings -->
        <div class="rounded-lg border border-white/[0.08] bg-white/[0.02] light:bg-white light:border-gray-200">
          <div class="p-6">
            <div class="flex items-center justify-between">
              <div>
                <h3 class="text-lg font-semibold text-white light:text-gray-900">{{ $t('chatbot.currentConfiguration') }}</h3>
                <p class="text-sm text-white/50 light:text-gray-500">{{ $t('chatbot.configOverview') }}</p>
              </div>
              <RouterLink to="/settings/chatbot">
                <Button variant="outline" size="sm">
                  <Settings class="h-4 w-4 mr-2" />
                  {{ $t('chatbot.editSettings') }}
                </Button>
              </RouterLink>
            </div>
          </div>
          <div class="px-6 pb-6">
            <div class="grid gap-4 md:grid-cols-2">
              <div class="space-y-2">
                <h4 class="font-medium text-sm text-white/70 light:text-gray-700">{{ $t('chatbot.greetingMessage') }}</h4>
                <p class="text-sm text-white/50 light:text-gray-600 bg-white/[0.04] light:bg-gray-100 p-3 rounded-lg">
                  {{ settings.greeting_message || $t('chatbot.notConfigured') }}
                </p>
              </div>
              <div class="space-y-2">
                <h4 class="font-medium text-sm text-white/70 light:text-gray-700">{{ $t('chatbot.fallbackMessage') }}</h4>
                <p class="text-sm text-white/50 light:text-gray-600 bg-white/[0.04] light:bg-gray-100 p-3 rounded-lg">
                  {{ settings.fallback_message || $t('chatbot.notConfigured') }}
                </p>
              </div>
              <div class="space-y-2">
                <h4 class="font-medium text-sm text-white/70 light:text-gray-700">{{ $t('chatbot.sessionTimeout') }}</h4>
                <div class="flex items-center gap-2 text-sm text-white/50 light:text-gray-600">
                  <Clock class="h-4 w-4" />
                  {{ $t('chatbot.minutes', { count: settings.session_timeout_minutes }) }}
                </div>
              </div>
              <div class="space-y-2">
                <h4 class="font-medium text-sm text-white/70 light:text-gray-700">{{ $t('chatbot.aiProvider') }}</h4>
                <div class="flex items-center gap-2">
                  <Badge v-if="settings.ai_enabled" class="bg-emerald-500/20 text-emerald-400 light:bg-emerald-100 light:text-emerald-700">
                    {{ settings.ai_provider || $t('chatbot.notConfigured') }}
                  </Badge>
                  <Badge v-else class="bg-white/[0.08] text-white/50 light:bg-gray-100 light:text-gray-500">{{ $t('chatbot.disabled') }}</Badge>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </ScrollArea>
  </div>
</template>
