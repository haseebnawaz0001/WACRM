<script setup lang="ts">
/**
 * Choosing who a campaign goes to (plan 05).
 *
 * A campaign used to be aimed at whatever list somebody pasted in, and the list
 * was gone the moment it was sent. Pointing it at a segment makes the audience
 * reproducible — and the preview is the point: "12,400 contacts" and "12,400
 * minus 3,000 who opted out" are different decisions, and a rendered sample is
 * the only way to see what the customer will actually read.
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  segmentsService, campaignAudienceService,
  type Segment, type AudiencePreview
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { Users, RefreshCw } from 'lucide-vue-next'

const props = defineProps<{
  campaignId: string
  segmentId?: string | null
  /** A campaign that has started can no longer change who it is aimed at. */
  editable?: boolean
}>()

const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const authStore = useAuthStore()

const segments = ref<Segment[]>([])
const selected = ref<string>(props.segmentId || '')
const preview = ref<AudiencePreview | null>(null)
const isPreviewing = ref(false)

const canUseSegments = computed(() => authStore.hasPermission('segments', 'read'))
const editable = computed(() => props.editable !== false)

async function loadSegments() {
  if (!canUseSegments.value) return
  try {
    const { data } = await segmentsService.list()
    segments.value = data.segments || []
  } catch {
    segments.value = []
  }
}

async function choose(segmentId: string) {
  selected.value = segmentId
  try {
    await campaignAudienceService.set(props.campaignId, segmentId || null)
    emit('changed')
    await refreshPreview()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function refreshPreview() {
  if (!selected.value) {
    preview.value = null
    return
  }
  isPreviewing.value = true
  try {
    const { data } = await campaignAudienceService.preview(props.campaignId)
    preview.value = data
  } catch {
    preview.value = null
  } finally {
    isPreviewing.value = false
  }
}

const excludedRows = computed(() =>
  Object.entries(preview.value?.excluded || {}).map(([reason, count]) => ({ reason, count }))
)

watch(() => props.segmentId, value => {
  selected.value = value || ''
})

onMounted(async () => {
  await loadSegments()
  if (selected.value) await refreshPreview()
})
</script>

<template>
  <Card v-if="canUseSegments">
    <CardHeader class="flex-row items-center justify-between space-y-0">
      <CardTitle class="flex items-center gap-2 text-base">
        <Users class="h-4 w-4 text-muted-foreground" />
        {{ t('campaignAudience.title') }}
      </CardTitle>
      <Button
        v-if="selected"
        variant="ghost" size="sm"
        :disabled="isPreviewing"
        @click="refreshPreview"
      >
        <RefreshCw class="mr-1.5 h-4 w-4" :class="isPreviewing ? 'animate-spin' : ''" />
        {{ t('campaignAudience.refresh') }}
      </Button>
    </CardHeader>

    <CardContent class="space-y-3">
      <div class="space-y-1.5">
        <Label>{{ t('campaignAudience.segment') }}</Label>
        <Select
          :model-value="selected"
          :disabled="!editable"
          @update:model-value="v => choose(String(v ?? ''))"
        >
          <SelectTrigger>
            <SelectValue :placeholder="t('campaignAudience.uploadedList')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="">{{ t('campaignAudience.uploadedList') }}</SelectItem>
            <SelectItem v-for="segment in segments" :key="segment.id" :value="segment.id">
              {{ segment.name }}
            </SelectItem>
          </SelectContent>
        </Select>
        <p class="text-xs text-muted-foreground">{{ t('campaignAudience.hint') }}</p>
      </div>

      <template v-if="preview">
        <p class="text-sm">
          <strong class="tabular-nums">{{ preview.count }}</strong>
          {{ t('campaignAudience.willReceive') }}
        </p>

        <!-- Why people are being left out, not just how many are left. -->
        <ul v-if="excludedRows.length" class="space-y-0.5">
          <li v-for="row in excludedRows" :key="row.reason" class="text-xs text-muted-foreground">
            {{ t(`campaignAudience.excluded.${row.reason}`, row.reason) }}: {{ row.count }}
          </li>
        </ul>

        <div v-if="preview.sample.length" class="space-y-1.5">
          <Label class="text-xs">{{ t('campaignAudience.sample') }}</Label>
          <ul class="space-y-1.5">
            <li
              v-for="row in preview.sample"
              :key="row.contact_id"
              class="rounded-md border p-2 text-sm"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="truncate font-medium">{{ row.name || row.phone_number }}</span>
                <Badge variant="outline" class="shrink-0 px-1.5 py-0 text-[11px]">
                  {{ row.phone_number }}
                </Badge>
              </div>
              <p v-if="row.preview" class="mt-0.5 text-xs text-muted-foreground">{{ row.preview }}</p>
            </li>
          </ul>
        </div>
      </template>
    </CardContent>
  </Card>
</template>
