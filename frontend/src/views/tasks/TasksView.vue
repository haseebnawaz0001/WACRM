<script setup lang="ts">
/**
 * Follow-ups (plan 04).
 *
 * A shared inbox can only answer "what has arrived?". Everything an agent
 * commits to outside that — call back tomorrow, send the quote on Friday —
 * lived in their head or a private note, so it was invisible to everyone else
 * and lost when they were away.
 *
 * The list opens on the viewer's own work, because a task list that opens on
 * everyone's is a list nobody reads.
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Checkbox } from '@/components/ui/checkbox'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import { PageHeader, ErrorState } from '@/components/shared'
import {
  tasksService, contactsService,
  type Task, type TaskType
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { useDebounceFn } from '@vueuse/core'
import { ListChecks, Plus, Check, X } from 'lucide-vue-next'

const { t, locale } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

type TaskViewKey = 'mine' | 'overdue' | 'all'

const tasks = ref<Task[]>([])
const types = ref<TaskType[]>([])
const view = ref<TaskViewKey>('mine')
const status = ref('open')

const isLoading = ref(true)
const fetchError = ref(false)
const showCreate = ref(false)

const canWrite = computed(() => authStore.hasPermission('tasks', 'write'))

async function fetchTasks() {
  try {
    const { data: envelope } = await tasksService.list({ view: view.value, status: status.value, limit: 100 })
    const data = (envelope as any)?.data ?? envelope
    tasks.value = data.tasks || []
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

watch([view, status], fetchTasks)

async function fetchTypes() {
  try {
    const { data: envelope } = await tasksService.types()
    const data = (envelope as any)?.data ?? envelope
    types.value = data.task_types || []
  } catch {
    types.value = []
  }
}

async function complete(task: Task) {
  // Optimistic: the tick is the whole interaction, and waiting for a round
  // trip before it moves makes the list feel broken.
  const previous = task.status
  task.status = 'completed'
  try {
    await tasksService.complete(task.id)
    if (status.value === 'open') tasks.value = tasks.value.filter(row => row.id !== task.id)
  } catch (error: any) {
    task.status = previous
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function cancel(task: Task) {
  try {
    await tasksService.cancel(task.id)
    tasks.value = tasks.value.filter(row => row.id !== task.id)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

// --- Creating ---

const draft = ref({ title: '', type_key: '', contact_id: '', due_at: '', priority: 'normal' })
const contactQuery = ref('')
const contactResults = ref<Array<{ id: string; profile_name: string; phone_number: string }>>([])

const searchContacts = useDebounceFn(async () => {
  if (contactQuery.value.length < 2) {
    contactResults.value = []
    return
  }
  try {
    const { data: envelope } = await contactsService.list({ search: contactQuery.value, limit: 10 })
    const data = (envelope as any)?.data ?? envelope
    contactResults.value = data.contacts || data || []
  } catch {
    contactResults.value = []
  }
}, 300)
watch(contactQuery, searchContacts)

async function create() {
  if (!draft.value.title || !draft.value.contact_id) return
  try {
    await tasksService.create({
      ...draft.value,
      due_at: draft.value.due_at ? new Date(draft.value.due_at).toISOString() : undefined
    })
    showCreate.value = false
    draft.value = { title: '', type_key: '', contact_id: '', due_at: '', priority: 'normal' }
    contactQuery.value = ''
    fetchTasks()
    toast.success(t('tasks.created'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

// --- Display ---

function due(task: Task): string {
  const date = new Date(task.due_at)
  const today = new Date()
  const sameDay = date.toDateString() === today.toDateString()
  if (sameDay) {
    return t('tasks.todayAt', {
      time: new Intl.DateTimeFormat(locale.value, { hour: '2-digit', minute: '2-digit' }).format(date)
    })
  }
  return new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric' }).format(date)
}

/**
 * The list in sections, overdue first.
 *
 * Both sections carry a heading. Only the overdue one used to, so the page read
 * as "Overdue (2)" followed by four rows, and there was nothing to say where
 * the overdue ones stopped — the only clue was that two of the date badges were
 * red and two were not.
 */
const sections = computed(() => {
  const overdue = tasks.value.filter(task => task.overdue)
  const rest = tasks.value.filter(task => !task.overdue)
  return [
    { key: 'overdue', tasks: overdue, heading: t('tasks.overdueHeading', { count: overdue.length }), urgent: true },
    { key: 'rest', tasks: rest, heading: t('tasks.restHeading', { count: rest.length }), urgent: false }
  ].filter(section => section.tasks.length > 0)
})

onMounted(async () => {
  await Promise.all([fetchTasks(), fetchTypes()])
})
</script>

<template>
  <div class="flex h-full flex-col">
    <PageHeader :title="t('tasks.title')" :description="t('tasks.description')" :icon="ListChecks">
      <template #actions>
        <Button v-if="canWrite" size="sm" @click="showCreate = true">
          <Plus class="mr-1.5 h-4 w-4" />
          {{ t('tasks.new') }}
        </Button>
      </template>
    </PageHeader>
    <div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 md:p-6">
      <div class="flex flex-wrap items-center gap-3">
        <Tabs :model-value="view" @update:model-value="v => view = v as TaskViewKey">
          <TabsList>
            <TabsTrigger value="mine">{{ t('tasks.viewMine') }}</TabsTrigger>
            <TabsTrigger value="overdue">{{ t('tasks.viewOverdue') }}</TabsTrigger>
            <TabsTrigger value="all">{{ t('tasks.viewAll') }}</TabsTrigger>
          </TabsList>
        </Tabs>

        <Select v-model="status">
          <SelectTrigger class="h-8 w-36" :aria-label="t('common.status')"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="open">{{ t('tasks.statusOpen') }}</SelectItem>
            <SelectItem value="completed">{{ t('tasks.statusCompleted') }}</SelectItem>
            <SelectItem value="all">{{ t('tasks.statusAll') }}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <ErrorState v-if="fetchError" :message="t('tasks.loadFailed')" @retry="fetchTasks" />
      <p v-else-if="isLoading" class="text-muted-foreground">{{ t('common.loading') }}</p>

      <Card v-else-if="!tasks.length">
        <CardContent class="flex flex-col items-center gap-3 py-10 text-center">
          <ListChecks class="h-8 w-8 text-muted-foreground" />
          <p class="text-sm text-muted-foreground">{{ t('tasks.empty') }}</p>
        </CardContent>
      </Card>

      <template v-else>
        <!--
          One markup for every row.

          Overdue and not-overdue were two copies of the same twenty lines, and
          they had already drifted: the overdue checkbox had lost its
          `model-value`, so it never reflected a completed task. What differs
          between the two is the heading and whether the date reads as a warning.
        -->
        <section v-for="section in sections" :key="section.key" class="space-y-2">
          <h2 :class="['text-sm font-medium', section.urgent ? 'text-destructive' : 'text-muted-foreground']">
            {{ section.heading }}
          </h2>
          <ul class="space-y-2">
            <li v-for="task in section.tasks" :key="task.id">
              <Card>
                <CardContent class="flex items-center gap-3 p-3">
                  <Checkbox
                    :model-value="task.status === 'completed'"
                    :disabled="!canWrite || task.status !== 'open'"
                    :aria-label="t('tasks.completeLabel', { title: task.title })"
                    @update:model-value="() => complete(task)"
                  />
                  <button class="min-w-0 flex-1 text-left" @click="router.push(`/contacts/${task.contact_id}`)">
                    <p class="truncate text-sm font-medium" :class="task.status === 'completed' ? 'line-through opacity-60' : ''">
                      {{ task.title }}
                    </p>
                    <p class="truncate text-xs text-muted-foreground">
                      {{ task.contact_name || task.contact_id }}
                      <span v-if="task.type_label"> · {{ task.type_label }}</span>
                    </p>
                  </button>
                  <Badge
                    :variant="section.urgent ? 'destructive' : 'outline'"
                    class="shrink-0 px-1.5 py-0 text-[11px]"
                  >
                    {{ due(task) }}
                  </Badge>
                  <Button
                    v-if="canWrite && task.status === 'open'"
                    variant="ghost" size="icon"
                    :aria-label="t('tasks.cancelLabel', { title: task.title })"
                    @click="cancel(task)"
                  >
                    <X class="h-4 w-4" />
                  </Button>
                  <Check v-else-if="task.status === 'completed'" class="h-4 w-4 shrink-0 text-muted-foreground" />
                </CardContent>
              </Card>
            </li>
          </ul>
        </section>
      </template>

      <!-- New task -->
      <Dialog v-model:open="showCreate">
        <DialogContent>
          <DialogHeader><DialogTitle>{{ t('tasks.new') }}</DialogTitle></DialogHeader>

          <div class="space-y-3">
            <div class="space-y-1.5">
              <Label>{{ t('tasks.titleLabel') }}</Label>
              <Input v-model="draft.title" :placeholder="t('tasks.titlePlaceholder')" />
            </div>

            <div class="space-y-1.5">
              <Label>{{ t('tasks.contact') }}</Label>
              <Input v-model="contactQuery" :placeholder="t('tasks.contactPlaceholder')" />
              <ul v-if="contactResults.length" class="max-h-40 overflow-y-auto rounded-md border">
                <li
                  v-for="contact in contactResults"
                  :key="contact.id"
                  class="cursor-pointer px-3 py-1.5 text-sm hover:bg-accent"
                  :class="draft.contact_id === contact.id ? 'bg-accent' : ''"
                  @click="draft.contact_id = contact.id; contactQuery = contact.profile_name || contact.phone_number"
                >
                  {{ contact.profile_name || contact.phone_number }}
                </li>
              </ul>
            </div>

            <div class="space-y-1.5">
              <Label>{{ t('tasks.type') }}</Label>
              <Select v-model="draft.type_key">
                <SelectTrigger :aria-label="$t('tasks.typePlaceholder')"><SelectValue :placeholder="t('tasks.typePlaceholder')" /></SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="type in types" :key="type.id" :value="type.key">
                    {{ type.label }}
                  </SelectItem>
                </SelectContent>
              </Select>
              <p class="text-xs text-muted-foreground">{{ t('tasks.typeHint') }}</p>
            </div>

            <div class="space-y-1.5">
              <Label>{{ t('tasks.dueAt') }}</Label>
              <Input v-model="draft.due_at" type="datetime-local" />
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" @click="showCreate = false">{{ t('common.cancel') }}</Button>
            <Button :disabled="!draft.title || !draft.contact_id" @click="create">
              {{ t('common.create') }}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  </div>
</template>
