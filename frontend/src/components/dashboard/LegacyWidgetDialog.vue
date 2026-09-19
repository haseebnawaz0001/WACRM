<script setup lang="ts">
/**
 * Editing a widget made with the original builder.
 *
 * New widgets are built from measures (WidgetBuilder). Widgets made before
 * that are stored as a raw source, metric and filters, which the measure
 * builder cannot read back, so they keep this form for editing — with the
 * field names put into words, since they are still what it offers.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Plus, X } from 'lucide-vue-next'
import { widgetsService, type DashboardWidget } from '@/services/api'
import { useAppToast } from '@/composables/useAppToast'

const props = defineProps<{ open: boolean; widget: DashboardWidget | null }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [] }>()

const { t } = useI18n()
const { success, error: showError } = useAppToast()

const dataSources = ref<Array<{ name: string; label: string; fields: string[] }>>([])
const operators = ref<Array<{ value: string; label: string }>>([])
const saving = ref(false)

const form = ref({
  name: '',
  data_source: '',
  metric: 'count',
  filters: [] as Array<{ field: string; operator: string; value: string }>,
  display_type: 'number',
  chart_type: '',
  group_by_field: '',
  show_change: true,
  color: 'blue',
  is_shared: false
})

watch(() => props.open, async open => {
  if (!open || !props.widget) return
  const w = props.widget
  form.value = {
    name: w.name,
    data_source: w.data_source,
    metric: w.metric,
    filters: (w.filters || []).map(f => ({ ...f })),
    display_type: w.display_type,
    chart_type: w.chart_type,
    group_by_field: w.group_by_field || '',
    show_change: w.show_change,
    color: w.color || 'blue',
    is_shared: w.is_shared
  }
  if (!dataSources.value.length) {
    try {
      const res = await widgetsService.getDataSources()
      const data = (res.data as any).data || res.data
      dataSources.value = data.data_sources || []
      operators.value = data.operators || []
    } catch {
      // The form still saves what it has.
    }
  }
})

const fields = computed(() => dataSources.value.find(s => s.name === form.value.data_source)?.fields || [])
const fieldLabel = (f: string) => f.replace(/_/g, ' ').replace(/^./, c => c.toUpperCase())

watch(() => form.value.display_type, value => {
  if (value === 'chart' && !form.value.chart_type) form.value.chart_type = 'line'
  if (value !== 'chart') form.value.chart_type = ''
  if (value !== 'chart' && value !== 'table') form.value.group_by_field = ''
})

async function save() {
  if (!props.widget || !form.value.name.trim()) return
  saving.value = true
  try {
    await widgetsService.update(props.widget.id, {
      ...form.value,
      filters: form.value.filters.filter(f => f.field && f.operator && f.value)
    })
    success(t('common.updatedSuccess', { resource: t('resources.Widget') }))
    emit('saved')
    emit('update:open', false)
  } catch (err: any) {
    showError(t('common.error'), err?.response?.data?.message || t('common.failedSave', { resource: t('resources.widget') }))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="v => emit('update:open', v)">
    <DialogContent class="max-h-[90vh] overflow-y-auto sm:max-w-[520px]">
      <DialogHeader>
        <DialogTitle>{{ t('dashboard.editWidget') }}</DialogTitle>
        <DialogDescription>{{ t('dashboard.legacy.description') }}</DialogDescription>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div class="space-y-1.5">
          <Label>{{ t('dashboard.widgetName') }}</Label>
          <Input v-model="form.name" />
        </div>

        <div class="grid grid-cols-2 gap-3">
          <div class="space-y-1.5">
            <Label>{{ t('dashboard.dataSource') }}</Label>
            <Select v-model="form.data_source">
              <SelectTrigger :aria-label="t('dashboard.dataSource')"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="s in dataSources" :key="s.name" :value="s.name">{{ s.label }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-if="form.display_type !== 'table'" class="space-y-1.5">
            <Label>{{ t('dashboard.metric') }}</Label>
            <Select v-model="form.metric">
              <SelectTrigger :aria-label="t('dashboard.metric')"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="count">{{ t('dashboard.metricCount') }}</SelectItem>
                <SelectItem value="sum">{{ t('dashboard.metricSum') }}</SelectItem>
                <SelectItem value="avg">{{ t('dashboard.metricAverage') }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-3">
          <div class="space-y-1.5">
            <Label>{{ t('dashboard.displayType') }}</Label>
            <Select v-model="form.display_type">
              <SelectTrigger :aria-label="t('dashboard.displayType')"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="number">{{ t('dashboard.displayNumber') }}</SelectItem>
                <SelectItem value="chart">{{ t('dashboard.displayChart') }}</SelectItem>
                <SelectItem value="table">{{ t('dashboard.displayTable') }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-if="form.display_type === 'chart'" class="space-y-1.5">
            <Label>{{ t('dashboard.chartType') }}</Label>
            <Select v-model="form.chart_type">
              <SelectTrigger :aria-label="t('dashboard.chartType')"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="line">{{ t('dashboard.chartLine') }}</SelectItem>
                <SelectItem value="bar">{{ t('dashboard.chartBar') }}</SelectItem>
                <SelectItem value="pie">{{ t('dashboard.chartPie') }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div v-if="form.display_type === 'chart' || form.display_type === 'table'" class="space-y-1.5">
          <Label>{{ t('dashboard.groupBy') }}</Label>
          <Select :model-value="form.group_by_field || 'none'" @update:model-value="v => form.group_by_field = v === 'none' ? '' : String(v)">
            <SelectTrigger :aria-label="t('dashboard.groupBy')"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="none">{{ t('dashboard.noneTimeSeries') }}</SelectItem>
              <SelectItem v-for="f in fields" :key="f" :value="f">{{ fieldLabel(f) }}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div class="space-y-2">
          <div class="flex items-center justify-between">
            <Label>{{ t('dashboard.filters') }}</Label>
            <Button type="button" variant="outline" size="sm" @click="form.filters.push({ field: '', operator: 'equals', value: '' })">
              <Plus class="mr-1 h-4 w-4" />{{ t('dashboard.addFilter') }}
            </Button>
          </div>
          <div v-for="(f, i) in form.filters" :key="i" class="flex items-center gap-2">
            <Select v-model="f.field">
              <SelectTrigger class="flex-1" :aria-label="t('dashboard.field')"><SelectValue :placeholder="t('dashboard.field')" /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="field in fields" :key="field" :value="field">{{ fieldLabel(field) }}</SelectItem>
              </SelectContent>
            </Select>
            <Select v-model="f.operator">
              <SelectTrigger class="w-32" :aria-label="t('dashboard.operator')"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="op in operators" :key="op.value" :value="op.value">{{ op.label }}</SelectItem>
              </SelectContent>
            </Select>
            <Input v-model="f.value" class="flex-1" :placeholder="t('dashboard.value')" />
            <Button variant="ghost" size="icon" class="shrink-0" :aria-label="t('common.remove')" @click="form.filters.splice(i, 1)">
              <X class="h-4 w-4" />
            </Button>
          </div>
        </div>

        <div class="flex flex-wrap items-center justify-between gap-3">
          <label v-if="form.display_type === 'number'" class="flex items-center gap-2 text-sm">
            <Switch v-model:checked="form.show_change" />{{ t('dashboard.showPercentChange') }}
          </label>
          <label class="flex items-center gap-2 text-sm">
            <Switch v-model:checked="form.is_shared" />{{ t('dashboard.shareWithTeam') }}
          </label>
        </div>
      </div>

      <DialogFooter>
        <Button variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
        <Button :disabled="saving || !form.name.trim()" @click="save">{{ t('common.save') }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
