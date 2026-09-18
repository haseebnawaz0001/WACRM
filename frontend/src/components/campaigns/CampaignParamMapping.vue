<script setup lang="ts">
/**
 * Where a segment campaign's template variables get their values (plan 05).
 *
 * A list campaign gets them from the CSV, column by column. A segment has no
 * CSV, so `{{customer_name}}` went out as the literal text — or the campaign
 * could not be sent at all.
 *
 * Each parameter names a source in the same namespace every other template
 * surface uses, plus a fallback. The fallback is not a nicety: Meta rejects a
 * send with an empty parameter, so one contact missing a company name would
 * fail instead of sending.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from '@/components/ui/select'
import { toast } from 'vue-sonner'
import { campaignAudienceService, variablesService, type TemplateVariable } from '@/services/api'

interface Mapping {
  source: string
  value: string
  fallback: string
}

const props = defineProps<{
  campaignId: string
  /** The template's parameter names, in the order it uses them. */
  paramNames: string[]
  existing?: Record<string, Mapping> | null
  editable?: boolean
}>()

const emit = defineEmits<{ saved: [] }>()

const { t } = useI18n()

const mappings = ref<Record<string, Mapping>>({})
const sources = ref<TemplateVariable[]>([])
const isSaving = ref(false)

const editable = computed(() => props.editable !== false)

/**
 * The sources a campaign can actually resolve.
 *
 * Taken from the backend catalog rather than hardcoded, so the list offers the
 * organization's own custom fields — the whole reason somebody wants a mapping
 * — and never offers a variable a campaign has no value for.
 */
async function loadSources() {
  try {
    const { data } = await variablesService.list('campaign')
    const payload = (data as any)?.data ?? data
    sources.value = (payload?.variables ?? []).filter((v: TemplateVariable) => !v.dynamic)
  } catch {
    sources.value = []
  }
}

function seed() {
  const next: Record<string, Mapping> = {}
  for (const name of props.paramNames) {
    next[name] = props.existing?.[name] ?? { source: '', value: '', fallback: '' }
  }
  mappings.value = next
}

watch(() => [props.paramNames, props.existing], seed, { deep: true })

/**
 * The placeholder as the template author wrote it.
 *
 * Built in script rather than the template: a literal pair of braces inside an
 * interpolation is read as a nested interpolation and fails the build.
 */
function tokenLabel(name: string): string {
  return `${'{'}${'{'}${name}${'}'}${'}'}`
}

const unmapped = computed(() =>
  props.paramNames.filter(name => !mappings.value[name]?.source)
)

async function save() {
  if (unmapped.value.length) {
    // Naming them, because "some variables are unmapped" makes an author
    // compare the template against this form by hand.
    toast.error(t('campaigns.paramsUnmapped', { names: unmapped.value.join(', ') }))
    return
  }

  isSaving.value = true
  try {
    await campaignAudienceService.setParamMappings(props.campaignId, mappings.value)
    toast.success(t('campaigns.paramsSaved'))
    emit('saved')
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    isSaving.value = false
  }
}

onMounted(async () => {
  seed()
  await loadSources()
})
</script>

<template>
  <Card v-if="paramNames.length">
    <CardHeader class="pb-3">
      <CardTitle class="text-base">{{ t('campaigns.paramMapping') }}</CardTitle>
      <p class="text-sm text-muted-foreground">{{ t('campaigns.paramMappingDesc') }}</p>
    </CardHeader>

    <CardContent class="space-y-4">
      <div v-for="name in paramNames" :key="name" class="grid gap-2 sm:grid-cols-[8rem_1fr_1fr]">
        <Label class="self-center font-mono text-xs">{{ tokenLabel(name) }}</Label>

        <Select
          v-model="mappings[name].source"
          :disabled="!editable"
        >
          <SelectTrigger :aria-label="$t('campaigns.paramSource')">
            <SelectValue :placeholder="t('campaigns.paramSource')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="static">{{ t('campaigns.paramStatic') }}</SelectItem>
            <SelectItem v-for="source in sources" :key="source.path" :value="source.path">
              {{ source.label }}
            </SelectItem>
          </SelectContent>
        </Select>

        <Input
          v-if="mappings[name].source === 'static'"
          v-model="mappings[name].value"
          :disabled="!editable"
          :placeholder="t('campaigns.paramValue')"
        />
        <!-- A fallback only makes sense for a source that can come back empty;
             a static value is never empty by definition. -->
        <Input
          v-else
          v-model="mappings[name].fallback"
          :disabled="!editable"
          :placeholder="t('campaigns.paramFallback')"
        />
      </div>

      <div v-if="editable" class="flex justify-end">
        <Button :disabled="isSaving" @click="save">{{ t('common.save') }}</Button>
      </div>
    </CardContent>
  </Card>
</template>
