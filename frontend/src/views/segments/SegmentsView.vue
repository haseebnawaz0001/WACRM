<script setup lang="ts">
/**
 * Segments (plan 05).
 *
 * A campaign used to be aimed at whatever list somebody assembled by hand, and
 * the list was gone the moment it was sent. A segment is a saved question —
 * "everyone tagged VIP with an open deal" — so the audience is reproducible and
 * stays right as contacts change.
 *
 * The live count is the point of the builder: nobody should have to save a
 * segment to find out it matches four people.
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import { PageHeader, SearchInput, FilterBuilder, ErrorState } from '@/components/shared'
import {
  segmentsService, contactsService,
  type Segment, type FilterNode, type FilterFieldInfo, type ContactSearchRow
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { useDebounceFn } from '@vueuse/core'
import { formatDate } from '@/lib/utils'
import { Users, Plus, Trash2, RefreshCw } from 'lucide-vue-next'

const { t } = useI18n()
const authStore = useAuthStore()

const segments = ref<Segment[]>([])
const filterFields = ref<FilterFieldInfo[]>([])
const search = ref('')

const isLoading = ref(true)
const fetchError = ref(false)

const canWrite = computed(() => authStore.hasPermission('segments', 'write'))
const canDelete = computed(() => authStore.hasPermission('segments', 'delete'))

// --- Editing ---

const editing = ref<Segment | null>(null)
const draftFilter = ref<FilterNode>({ op: 'and', rules: [] })
const previewCount = ref<number | null>(null)
const isCounting = ref(false)

const previewMatches = useDebounceFn(async () => {
  if (!draftFilter.value.rules?.length) {
    previewCount.value = null
    return
  }
  isCounting.value = true
  try {
    const { data: envelope } = await segmentsService.previewCount(draftFilter.value)
    const data = (envelope as any)?.data ?? envelope
    previewCount.value = data.count
  } catch {
    previewCount.value = null
  } finally {
    isCounting.value = false
  }
}, 500)

watch(draftFilter, previewMatches, { deep: true })

function startNew() {
  editing.value = {
    id: '', name: '', description: '', visibility: 'shared',
    filter: { op: 'and', rules: [] }, created_at: ''
  } as Segment
  draftFilter.value = { op: 'and', rules: [] }
  previewCount.value = null
}

function startEdit(segment: Segment) {
  editing.value = { ...segment }
  draftFilter.value = segment.filter || { op: 'and', rules: [] }
  previewMatches()
}

async function save() {
  if (!editing.value?.name) return
  const body = {
    name: editing.value.name,
    description: editing.value.description,
    visibility: editing.value.visibility,
    filter: draftFilter.value
  }
  try {
    if (editing.value.id) {
      await segmentsService.update(editing.value.id, body as any)
    } else {
      await segmentsService.create(body as any)
    }
    editing.value = null
    await fetchSegments()
    toast.success(t('common.savedSuccess'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function remove(segment: Segment) {
  try {
    await segmentsService.delete(segment.id)
    segments.value = segments.value.filter(row => row.id !== segment.id)
    toast.success(t('common.deletedSuccess'))
  } catch (error: any) {
    // A segment another segment or campaign points at cannot simply go; the
    // message names the dependents.
    toast.error(error?.response?.data?.message || t('segments.inUse'))
  }
}

async function recount(segment: Segment) {
  try {
    const { data: envelope } = await segmentsService.count(segment.id)
    const data = (envelope as any)?.data ?? envelope
    segment.contact_count = data.count
    segment.counted_at = new Date().toISOString()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

// --- Members ---

const viewing = ref<Segment | null>(null)
const members = ref<ContactSearchRow[]>([])
const memberTotal = ref(0)

async function openMembers(segment: Segment) {
  viewing.value = segment
  try {
    const { data: envelope } = await segmentsService.contacts(segment.id, { limit: 50 })
    const data = (envelope as any)?.data ?? envelope
    members.value = data.contacts || []
    memberTotal.value = data.total
  } catch {
    members.value = []
    memberTotal.value = 0
  }
}

// --- Loading ---

async function fetchSegments() {
  try {
    const { data: envelope } = await segmentsService.list({ search: search.value })
    const data = (envelope as any)?.data ?? envelope
    segments.value = data.segments || []
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

const debouncedFetch = useDebounceFn(fetchSegments, 300)
watch(search, debouncedFetch)

onMounted(async () => {
  await fetchSegments()
  try {
    const { data: envelope } = await contactsService.filterFields()
    const data = (envelope as any)?.data ?? envelope
    filterFields.value = data.fields || []
  } catch {
    filterFields.value = []
  }
})
</script>

<template>
  <div class="space-y-4 p-4">
    <PageHeader :title="t('segments.title')" :description="t('segments.description')" :icon="Users">
      <template #actions>
        <Button v-if="canWrite" size="sm" @click="startNew">
          <Plus class="mr-1.5 h-4 w-4" />
          {{ t('segments.new') }}
        </Button>
      </template>
    </PageHeader>

    <SearchInput v-model="search" :placeholder="t('segments.searchPlaceholder')" class="w-64" />

    <ErrorState v-if="fetchError" :message="t('segments.loadFailed')" @retry="fetchSegments" />
    <!-- The shape of the answer, not the word "Loading". A line of text tells
         you nothing about what is coming; these rows do. -->
    <ul v-else-if="isLoading" class="space-y-2" aria-hidden="true">
      <li v-for="i in 5" :key="i">
        <Card>
          <CardContent class="flex items-center gap-3 p-4">
            <div class="min-w-0 flex-1 space-y-2">
              <Skeleton class="h-4 w-48" />
              <Skeleton class="h-3 w-72" />
            </div>
            <Skeleton class="h-8 w-16 shrink-0" />
          </CardContent>
        </Card>
      </li>
    </ul>

    <Card v-else-if="!segments.length">
      <CardContent class="flex flex-col items-center gap-3 py-10 text-center">
        <Users class="h-8 w-8 text-muted-foreground" />
        <p class="text-sm text-muted-foreground">{{ t('segments.empty') }}</p>
      </CardContent>
    </Card>

    <ul v-else class="space-y-2">
      <li v-for="segment in segments" :key="segment.id">
        <Card>
          <CardContent class="flex flex-wrap items-center gap-3 p-4">
            <!-- Opening the members is the thing people come here to do, and
                 nothing said the name did it: a bare button inside a card looks
                 exactly like a heading. -->
            <button class="group min-w-0 flex-1 text-left" @click="openMembers(segment)">
              <div class="flex items-center gap-2">
                <span class="truncate font-medium underline-offset-4 group-hover:underline">{{ segment.name }}</span>
                <Badge v-if="segment.visibility === 'private'" variant="secondary" class="px-1.5 py-0 text-[11px]">
                  {{ t('segments.private') }}
                </Badge>
              </div>
              <p v-if="segment.description" class="truncate text-sm text-muted-foreground">
                {{ segment.description }}
              </p>
            </button>

            <!-- The count carries its age: a number with no age is a number
                 people quote long after it was true. -->
            <div class="text-right text-xs text-muted-foreground">
              <p class="tabular-nums">
                {{ segment.contact_count ?? '—' }} {{ t('segments.contacts') }}
              </p>
              <p v-if="segment.counted_at">{{ t('segments.countedAt', { when: formatDate(segment.counted_at) }) }}</p>
            </div>

            <Button variant="ghost" size="icon" :aria-label="t('segments.recount')" @click="recount(segment)">
              <RefreshCw class="h-4 w-4" />
            </Button>
            <Button v-if="canWrite" variant="outline" size="sm" @click="startEdit(segment)">
              {{ t('common.edit') }}
            </Button>
            <!-- Destructive on hover, not at rest. Seven segments meant seven
                 saturated red bins down the right edge, which made the one
                 action nobody wants the most colourful thing on the page. -->
            <Button
              v-if="canDelete"
              variant="ghost"
              size="icon"
              class="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
              :aria-label="t('common.delete')"
              @click="remove(segment)"
            >
              <Trash2 class="h-4 w-4" />
            </Button>
          </CardContent>
        </Card>
      </li>
    </ul>

    <!-- Builder -->
    <Dialog :open="!!editing" @update:open="open => !open && (editing = null)">
      <DialogContent class="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{{ editing?.id ? t('segments.edit') : t('segments.new') }}</DialogTitle>
          <DialogDescription>{{ t('segments.builderHint') }}</DialogDescription>
        </DialogHeader>

        <div v-if="editing" class="space-y-3">
          <div class="space-y-1.5">
            <Label>{{ t('segments.name') }}</Label>
            <Input v-model="editing.name" :placeholder="t('segments.namePlaceholder')" />
          </div>
          <div class="space-y-1.5">
            <Label>{{ t('segments.descriptionLabel') }}</Label>
            <Input v-model="editing.description" />
          </div>

          <label class="flex items-center gap-2 text-sm">
            <Switch
              :model-value="editing.visibility === 'private'"
              @update:model-value="(v: boolean) => editing!.visibility = v ? 'private' : 'shared'"
            />
            {{ t('segments.privateLabel') }}
          </label>

          <div class="space-y-1.5">
            <Label>{{ t('segments.conditions') }}</Label>
            <FilterBuilder v-model="draftFilter" :fields="filterFields" />
          </div>

          <p class="text-sm">
            <span v-if="isCounting" class="text-muted-foreground">{{ t('segments.counting') }}</span>
            <span v-else-if="previewCount !== null">
              {{ t('segments.matches', { count: previewCount }) }}
            </span>
            <span v-else class="text-muted-foreground">{{ t('segments.addACondition') }}</span>
          </p>
        </div>

        <DialogFooter>
          <Button variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
          <Button :disabled="!editing?.name" @click="save">{{ t('common.save') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Members -->
    <Dialog :open="!!viewing" @update:open="open => !open && (viewing = null)">
      <DialogContent class="max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{{ viewing?.name }}</DialogTitle>
          <DialogDescription>{{ t('segments.memberCount', { count: memberTotal }) }}</DialogDescription>
        </DialogHeader>
        <ul class="divide-y">
          <li v-for="contact in members" :key="contact.id" class="py-1.5 text-sm">
            {{ contact.profile_name || contact.phone_number }}
            <span class="ml-2 text-xs text-muted-foreground">{{ contact.phone_number }}</span>
          </li>
        </ul>
        <p v-if="!members.length" class="text-sm text-muted-foreground">{{ t('segments.noMembers') }}</p>
        <DialogFooter>
          <Button variant="outline" @click="viewing = null">{{ t('common.close') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
