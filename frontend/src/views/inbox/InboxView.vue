<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, nextTick, computed, defineAsyncComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useContactsStore, type Message } from '@/stores/contacts'
import { useAuthStore } from '@/stores/auth'
import { useUsersStore } from '@/stores/users'
import { useTransfersStore } from '@/stores/transfers'
import { wsService } from '@/services/websocket'
import { contactsService, chatbotService, messagesService, customActionsService, accountsService, cannedResponsesService, timelineService, tasksService, inboxService, contactFieldsService, dealsService, getRequestHeaders, type CustomAction, type ActionResult, type CannedResponse, type TimelineItem, type ContactField, type InboxRow, type InboxCounts } from '@/services/api'
import { useTagsStore } from '@/stores/tags'
import { TagBadge } from '@/components/ui/tag-badge'
import { getTagColorClass } from '@/lib/constants'
import { getErrorMessage, unwrapResponse } from '@/lib/api-utils'
import { compressImage } from '@/lib/imageCompression'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Spinner } from '@/components/ui/spinner'
import { Separator } from '@/components/ui/separator'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
// Lazy-load emoji picker to reduce initial bundle size
const EmojiPicker = defineAsyncComponent(() => {
  return import('vue3-emoji-picker').then(module => {
    // Import CSS when component loads
    import('vue3-emoji-picker/css')
    return module.default
  })
})
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { toast } from 'vue-sonner'
import {
  Search,
  Send,
  Paperclip,
  FileText,
  Smile,
  MoreVertical,
  Phone,
  PhoneCall,
  Check,
  CheckCheck,
  Clock,
  AlertCircle,
  User,
  UserPlus,
  UserMinus,
  UserX,
  Play,
  Reply,
  X,
  SmilePlus,
  MapPin,
  ExternalLink,
  Loader2,
  Zap,
  Ticket,
  BarChart,
  Link,
  Mail,
  Globe,
  Code,
  RotateCw,
  Filter,
  StickyNote,
  ArrowLeft
} from 'lucide-vue-next'
import { getInitials, getAvatarColor } from '@/lib/utils'
import { useColorMode } from '@/composables/useColorMode'
import { useInfiniteScroll } from '@/composables/useInfiniteScroll'
import CannedResponsePicker from '@/components/chat/CannedResponsePicker.vue'
import PreviewButtonGroup from '@/components/chatbot/flow-preview/PreviewButtonGroup.vue'
import TemplatePicker from '@/components/chat/TemplatePicker.vue'
import MediaViewerDialog from '@/components/chat/MediaViewerDialog.vue'
import ContactInfoPanel from '@/components/chat/ContactInfoPanel.vue'
import DuplicateBanner from '@/components/chat/DuplicateBanner.vue'
import ConversationNotes from '@/components/chat/ConversationNotes.vue'
import CallButton from '@/components/calling/CallButton.vue'
import { useNotesStore } from '@/stores/notes'
import { useHeaderMedia } from '@/composables/useHeaderMedia'
import { CreateContactDialog } from '@/components/shared'
import HeaderMediaUpload from '@/components/shared/HeaderMediaUpload.vue'
import { Info, Activity, Bot, Users } from 'lucide-vue-next'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import { Label } from '@/components/ui/label'

const { t } = useI18n()
/**
 * The queue, merged into this surface (plan 10, §8).
 *
 * The inbox and the chat were two pages over one conversations table: one
 * listed people sorted by who wrote last, the other listed conversations that
 * still needed an answer, and moving between them meant losing your place.
 * They are one page now — the queue picks what to work on, the thread is where
 * you work on it.
 *
 * `view` selects which list the left pane shows. The four queue views read
 * conversations; `contacts` reads the contact list, which is the only way to
 * reach somebody you have never exchanged a message with.
 */
type ListView = 'mine' | 'unassigned' | 'bot' | 'all' | 'contacts'

const LIST_VIEW_KEY = 'inbox-view'

function readListView(): ListView {
  try {
    const saved = localStorage.getItem(LIST_VIEW_KEY)
    return (['mine', 'unassigned', 'bot', 'all', 'contacts'] as const).includes(saved as ListView)
      ? (saved as ListView)
      : 'mine'
  } catch {
    return 'mine'
  }
}

const listView = ref<ListView>(readListView())
const queueStatus = ref('')
const queueRows = ref<InboxRow[]>([])
const queueCounts = ref<InboxCounts | null>(null)
const isQueueLoading = ref(false)

const isQueueView = computed(() => listView.value !== 'contacts')

watch(listView, v => {
  try {
    localStorage.setItem(LIST_VIEW_KEY, v)
  } catch {
    // Storage unavailable; the view just won't be remembered.
  }
})

async function fetchQueue() {
  if (!isQueueView.value) return
  isQueueLoading.value = true
  try {
    const { data: envelope } = await inboxService.list({
      view: listView.value,
      status: queueStatus.value || undefined,
      limit: 100
    })
    const data = (envelope as any)?.data ?? envelope
    queueRows.value = data.conversations || []
  } catch {
    queueRows.value = []
  } finally {
    isQueueLoading.value = false
  }
}

async function fetchQueueCounts() {
  try {
    const { data: envelope } = await inboxService.counts()
    queueCounts.value = ((envelope as any)?.data ?? envelope) as InboxCounts
  } catch {
    queueCounts.value = null
  }
}

/** Reloads whichever list is showing, after an action changes it. */
async function refreshList() {
  if (isQueueView.value) {
    await Promise.all([fetchQueue(), fetchQueueCounts()])
  } else {
    await contactsStore.fetchContacts()
  }
}

watch([listView, queueStatus], () => {
  if (isQueueView.value) void fetchQueue()
  else void contactsStore.fetchContacts()
})

/**
 * One row shape for both sources, so the list markup does not fork.
 *
 * A queue row knows things a contact row cannot — how long the customer has
 * been waiting, whether the bot still holds it, when a snooze ends — and those
 * are exactly what the merge was for. A contact row leaves them undefined and
 * the template simply renders less.
 */
interface ListRow {
  contactId: string
  name: string
  phone: string
  avatarUrl?: string
  lastMessageAt?: string | null
  preview?: string
  unread: number
  status?: string
  handling?: string
  waitingSince?: string | null
  snoozedUntil?: string | null
}

const listRows = computed<ListRow[]>(() => {
  if (isQueueView.value) {
    return queueRows.value.map(row => ({
      contactId: row.contact_id,
      name: row.contact_name || row.contact_phone || '',
      phone: row.contact_phone || '',
      lastMessageAt: row.last_message_at,
      preview: row.last_message_preview,
      unread: 0,
      status: row.status,
      handling: row.handling,
      waitingSince: row.waiting_since,
      snoozedUntil: row.snoozed_until
    }))
  }
  return contactsStore.sortedContacts.map(c => ({
    contactId: c.id,
    name: c.name || c.phone_number,
    phone: c.phone_number,
    avatarUrl: c.avatar_url,
    lastMessageAt: c.last_message_at,
    unread: c.unread_count || 0
  }))
})

/** How long the oldest unanswered customer message has been waiting. */
function waitingFor(since?: string | null) {
  if (!since) return ''
  const mins = Math.floor((Date.now() - new Date(since).getTime()) / 60000)
  if (mins < 1) return t('inbox.waitingNow')
  if (mins < 60) return t('inbox.waitingMinutes', { count: mins })
  const hours = Math.floor(mins / 60)
  if (hours < 24) return t('inbox.waitingHours', { count: hours })
  return t('inbox.waitingDays', { count: Math.floor(hours / 24) })
}

// ── Queue actions, on the row, without leaving the thread you are reading ────

async function takeConversation(contactId: string) {
  const me = authStore.user?.id
  if (!me) return
  try {
    await inboxService.assign(contactId, me)
    await refreshList()
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('common.error')))
  }
}

async function resolveConversation(contactId: string) {
  try {
    await inboxService.resolve(contactId)
    await refreshList()
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('common.error')))
  }
}

const snoozingContactId = ref<string | null>(null)
const snoozeUntilValue = ref('')

/**
 * Snooze presets, in the viewer's own clock.
 *
 * "Tomorrow morning" is a statement about their day, so it is computed locally
 * rather than as a fixed offset.
 */
const snoozePresets = [
  { key: 'threeHours', at: () => snoozeIn(3, 0) },
  { key: 'tomorrow', at: () => snoozeAtHour(1, 9) },
  { key: 'mondayMorning', at: () => snoozeNextMonday(9) },
  { key: 'nextWeek', at: () => snoozeAtHour(7, 9) }
] as const

function snoozeIn(hours: number, minutes: number) {
  const at = new Date()
  at.setHours(at.getHours() + hours, at.getMinutes() + minutes, 0, 0)
  return at
}

function snoozeAtHour(daysAhead: number, hour: number) {
  const at = new Date()
  at.setDate(at.getDate() + daysAhead)
  at.setHours(hour, 0, 0, 0)
  return at
}

function snoozeNextMonday(hour: number) {
  const at = new Date()
  // Sunday is 0, so a Sunday gets tomorrow rather than a week away.
  const days = (8 - at.getDay()) % 7 || 7
  at.setDate(at.getDate() + days)
  at.setHours(hour, 0, 0, 0)
  return at
}

function presetTime(at: Date) {
  return at.toLocaleString(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' })
}

async function applySnooze(until: Date) {
  const contactId = snoozingContactId.value
  if (!contactId) return
  snoozingContactId.value = null
  snoozeUntilValue.value = ''
  try {
    await inboxService.snooze(contactId, until.toISOString())
    await refreshList()
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('common.error')))
  }
}

/**
 * Takes the next waiting conversation and opens it.
 *
 * The point of a queue is not having to choose: an agent finishing one
 * conversation should be able to start the next without reading a list.
 */
async function pickNext() {
  const next = queueRows.value.find(r => !r.assignee_id && r.handling !== 'bot')
    ?? queueRows.value[0]
  if (!next) return
  await takeConversation(next.contact_id)
  router.push(`/inbox/${next.contact_id}`)
}

const route = useRoute()
const router = useRouter()
const contactsStore = useContactsStore()
const authStore = useAuthStore()
const usersStore = useUsersStore()
const transfersStore = useTransfersStore()
const tagsStore = useTagsStore()
const notesStore = useNotesStore()
const { isDark } = useColorMode()

const canWriteContacts = authStore.hasPermission('contacts', 'write')
// Raising a deal from a message needs the permission to have deals at all.
const canSeeDealsFromChat = authStore.hasPermission('deals', 'write')

const messageInput = ref('')
const messagesEndRef = ref<HTMLElement | null>(null)
const messageInputRef = ref<HTMLTextAreaElement | null>(null)
const isSending = ref(false)
const isAssignDialogOpen = ref(false)
const isTransferring = ref(false)
const isResuming = ref(false)
// Tracks incoming messages that arrived while the chat is open.
// Surfaced as a "N unread messages" pill at the top of the chat panel
// (WhatsApp-style). Click the pill to jump up to the first message of
// the unread batch; cleared on click or contact switch. See issue #280.
const newMessagesCount = ref(0)
const firstUnreadId = ref<string | null>(null)
const isAtBottom = ref(true)
const SCROLL_BOTTOM_THRESHOLD = 80
const isInfoPanelOpen = ref(false)
const isNotesPanelOpen = ref(false)
const contactSessionData = ref<any>(null)

// Multi-account state
const selectedAccount = ref<string | null>(null)
const contactAccounts = ref<string[]>([])
const orgAccounts = ref<any[]>([])

// File upload state
const fileInputRef = ref<HTMLInputElement | null>(null)
const selectedFile = ref<File | null>(null)
const filePreviewUrl = ref<string | null>(null)
const isMediaDialogOpen = ref(false)
const mediaCaption = ref('')
const isUploadingMedia = ref(false)

// In-app media viewer (lightbox) state — see MediaViewerDialog.vue
const mediaViewerOpen = ref(false)
const mediaViewerIndex = ref(0)

// Cache for media blob URLs (message_id -> blob URL)


// Canned responses slash command state
const cannedPickerOpen = ref(false)
const cannedSearchQuery = ref('')

// Canned response preview dialog state
const cannedDialogOpen = ref(false)
const selectedCannedResponse = ref<CannedResponse | null>(null)
const cannedParamNames = ref<string[]>([])
const cannedParamValues = ref<Record<string, string>>({})
const isSendingCanned = ref(false)
// Tokens that the chat already knows how to fill from the current contact /
// signed-in agent. Shared by canned responses (resolved client-side into the
// outgoing message) and templates (pre-filled into the param payload so the
// backend forwards the resolved value to Meta).
const AUTO_RESOLVED_CONTEXT_TOKENS = new Set(['contact_name', 'phone_number', 'user_name', 'agent_name'])

// Sticky date header state
const stickyDate = ref('')
const showStickyDate = ref(false)
let stickyDateTimeout: ReturnType<typeof setTimeout> | null = null

// Emoji picker state
const emojiPickerOpen = ref(false)

// Template picker state
const templatePickerRef = ref<HTMLElement | null>(null)
const templateDialogOpen = ref(false)
const selectedTemplate = ref<any>(null)
const templateParamNames = ref<string[]>([])
const templateParamValues = ref<Record<string, string>>({})
// Name of the TEXT-header variable (max 1 per Meta) and its value. Kept in
// its own ref so a positional {{1}} in the header doesn't collide with a
// {{1}} body parameter — both can be filled independently.
const templateHeaderParamName = ref<string | null>(null)
const templateHeaderParamValue = ref('')
const templateButtonUrlParams = ref<{ index: number; text: string; value: string; type: string }[]>([])
const isSendingTemplate = ref(false)
const templateHeaderType = computed(() => selectedTemplate.value?.header_type)
const {
  file: templateHeaderFile,
  previewUrl: templateHeaderPreview,
  needsMedia: templateNeedsHeaderMedia,
  acceptTypes: templateHeaderAccept,
  handleFileChange: handleTemplateHeaderFile,
  clear: clearTemplateHeaderMedia,
} = useHeaderMedia(templateHeaderType)

// Custom actions state
const customActions = ref<CustomAction[]>([])
const executingActionId = ref<string | null>(null)

// Tags filter state
const isTagFilterOpen = ref(false)

// Message actions (plan 10, 4.1).
//
// The thing worth acting on is usually a sentence the customer just wrote —
// an address, an order number, a promise to call back. Retyping it into a
// task, a note or a field is how it gets typed wrong, so the actions start
// from the message itself.
const messageActionFields = ref<ContactField[]>([])
const messageActionsFor = ref<string | null>(null)

async function loadMessageActionFields() {
  try {
    const payload = unwrapResponse<any>(await contactFieldsService.list())
    // Only fields free text can land in. Offering a date or a dropdown would
    // promise a copy that the server is right to reject.
    messageActionFields.value = (payload?.fields || []).filter(
      (field: ContactField) => !field.archived_at && ['text', 'email', 'phone'].includes(field.type)
    )
  } catch {
    messageActionFields.value = []
  }
}

/** The message as a line of text, trimmed to something a title can hold. */
function messageAsTitle(message: Message): string {
  const text = getMessageContent(message).trim()
  if (!text) return t('chat.messageActionUntitled')
  return text.length > 120 ? text.slice(0, 117) + '…' : text
}

async function runMessageAction(label: string, work: () => Promise<unknown>) {
  messageActionsFor.value = null
  try {
    await work()
    toast.success(label)
    void loadActivity()
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.messageActionFailed')))
  }
}

function createTaskFromMessage(message: Message) {
  const contact = contactsStore.currentContact
  if (!contact) return
  void runMessageAction(t('chat.commandTaskDone'), () =>
    tasksService.create({
      contact_id: contact.id,
      title: messageAsTitle(message),
      message_id: message.id
    })
  )
}

function noteAboutMessage(message: Message) {
  const contact = contactsStore.currentContact
  if (!contact) return
  // Quoted, so the note still makes sense once the thread has moved on.
  const body = t('chat.messageActionNoteBody', { text: getMessageContent(message).trim() })
  void runMessageAction(t('chat.commandNoteDone'), () =>
    notesStore.createNote(contact.id, body)
  )
}

function createDealFromMessage(message: Message) {
  const contact = contactsStore.currentContact
  if (!contact) return
  // No pipeline or stage: the server puts it on the default pipeline's first
  // open stage, which is what somebody raising a deal from a chat means.
  void runMessageAction(t('chat.messageActionDealDone'), () =>
    dealsService.create({ contact_id: contact.id, title: messageAsTitle(message) })
  )
}

function copyMessageToField(message: Message, field: ContactField) {
  const contact = contactsStore.currentContact
  if (!contact) return
  const text = getMessageContent(message).trim()
  if (!text) return
  void runMessageAction(t('chat.messageActionCopied', { field: field.label }), async () => {
    await contactsService.update(contact.id, { fields: { [field.key]: text } })
    await contactsStore.refreshContactRow(contact.id)
  })
}

// Composer commands (plan 10, 4.1).
//
// The actions an agent takes mid-conversation — promise a follow-up, note what
// was said, take the chat, park it, close it — all lived in different corners
// of the screen, so doing them meant leaving the sentence half-typed. A command
// is typed where the thought already is.
//
// Enter runs the command rather than sending it: a message beginning with a
// slash was never a message anybody meant to send.
interface SlashCommand {
  name: string
  hint: string
  /** The rest of the line is the command's argument. */
  takesText: boolean
  run: (rest: string) => Promise<void>
}

const slashCommands: SlashCommand[] = [
  {
    name: 'task',
    hint: 'chat.commandTask',
    takesText: true,
    run: async (rest) => {
      const contact = contactsStore.currentContact
      if (!contact) return
      if (!rest) {
        toast.error(t('chat.commandNeedsText'))
        return
      }
      await tasksService.create({ contact_id: contact.id, title: rest })
      toast.success(t('chat.commandTaskDone'))
      void loadActivity()
    }
  },
  {
    name: 'note',
    hint: 'chat.commandNote',
    takesText: true,
    run: async (rest) => {
      const contact = contactsStore.currentContact
      if (!contact) return
      if (!rest) {
        toast.error(t('chat.commandNeedsText'))
        return
      }
      await notesStore.createNote(contact.id, rest)
      toast.success(t('chat.commandNoteDone'))
    }
  },
  {
    name: 'assign',
    hint: 'chat.commandAssign',
    takesText: false,
    run: async () => {
      const contact = contactsStore.currentContact
      const me = authStore.user?.id
      if (!contact || !me) return
      await inboxService.assign(contact.id, me)
      toast.success(t('chat.commandAssignDone'))
      void loadActivity()
    }
  },
  {
    name: 'snooze',
    hint: 'chat.commandSnooze',
    takesText: false,
    run: async () => {
      const contact = contactsStore.currentContact
      if (!contact) return
      // Tomorrow morning, in the reader's own timezone. An agent parking a
      // chat at six in the evening means "not tonight", and asking them for a
      // timestamp is asking them to do arithmetic mid-conversation.
      const until = new Date()
      until.setDate(until.getDate() + 1)
      until.setHours(9, 0, 0, 0)
      await inboxService.snooze(contact.id, until.toISOString())
      toast.success(t('chat.commandSnoozeDone'))
      void loadActivity()
    }
  },
  {
    name: 'resolve',
    hint: 'chat.commandResolve',
    takesText: false,
    run: async () => {
      const contact = contactsStore.currentContact
      if (!contact) return
      await inboxService.resolve(contact.id)
      toast.success(t('chat.commandResolveDone'))
      void loadActivity()
    }
  }
]

/** The word after the slash, and whatever follows it. */
function parseCommand(input: string): { word: string; rest: string } | null {
  if (!input.startsWith('/')) return null
  const trimmed = input.slice(1)
  const space = trimmed.indexOf(' ')
  if (space === -1) return { word: trimmed.toLowerCase(), rest: '' }
  return { word: trimmed.slice(0, space).toLowerCase(), rest: trimmed.slice(space + 1).trim() }
}

/** Commands whose name the typed word is a prefix of. */
const commandMatches = computed<SlashCommand[]>(() => {
  const parsed = parseCommand(messageInput.value)
  if (!parsed) return []
  // A word with a space after it has been chosen; only an exact name counts.
  if (messageInput.value.includes(' ')) {
    const exact = slashCommands.find(c => c.name === parsed.word)
    return exact ? [exact] : []
  }
  return slashCommands.filter(c => c.name.startsWith(parsed.word))
})

const isCommanding = computed(() => commandMatches.value.length > 0)

/** Runs the command on the line, if it is one. Returns whether it handled it. */
async function runCommandLine(): Promise<boolean> {
  const parsed = parseCommand(messageInput.value)
  if (!parsed) return false

  const command = slashCommands.find(c => c.name === parsed.word)
  if (!command) return false

  const line = messageInput.value
  messageInput.value = ''
  resetTextareaHeight()
  try {
    await command.run(parsed.rest)
  } catch (error) {
    // Put the line back: an agent who typed it should not have to type it
    // again to find out what went wrong.
    messageInput.value = line
    toast.error(getErrorMessage(error, t('chat.commandFailed')))
  }
  return true
}

// Thread activity pills (plan 10, 4.1).
//
// The chat and the contact's timeline were telling different stories: an agent
// reading a thread could not see that the conversation had been reassigned
// twice, a task had been raised off it, or an automation had set a field —
// all of which the customer's next message is a reply to.
//
// The wording is the server's: the timeline API already renders each entry as
// a sentence, so the pill shows `summary` rather than a second renderer that
// would drift from the profile page.
const showActivity = ref(false)
const activityItems = ref<TimelineItem[]>([])

// Messages are the thread itself; notes have their own panel. What is left is
// what happened *around* the conversation, which is the point of the pills.
const PILL_EXCLUDED = new Set(['message_burst', 'note'])

try {
  showActivity.value = localStorage.getItem('chat-show-activity') === 'true'
} catch {
  // Private windows and blocked site data: the default is simply off.
}

async function loadActivity() {
  const contactId = contactsStore.currentContact?.id
  if (!contactId || !showActivity.value) {
    activityItems.value = []
    return
  }
  try {
    const payload = unwrapResponse<any>(
      await timelineService.forContact(contactId, { limit: 100 })
    )
    activityItems.value = (payload?.items || []).filter(
      (item: TimelineItem) => !PILL_EXCLUDED.has(item.type)
    )
  } catch {
    // A thread that loads without its pills is still a thread.
    activityItems.value = []
  }
}

function toggleActivity() {
  showActivity.value = !showActivity.value
  try {
    localStorage.setItem('chat-show-activity', String(showActivity.value))
  } catch {
    // Not remembering the choice is survivable; refusing to make it is not.
  }
  void loadActivity()
}

/**
 * Which pills belong above which message.
 *
 * Built once per change rather than filtered per row: a long thread with a
 * busy history would otherwise be O(messages x activity) on every render.
 * Anything older than the first loaded message is dropped — the thread is
 * paged, and a pile of pills at the top would claim a history the reader
 * cannot see the messages for.
 */
const activityPills = computed<Map<string, TimelineItem[]>>(() => {
  const byMessage = new Map<string, TimelineItem[]>()
  const messages = contactsStore.messages
  if (!showActivity.value || !messages.length || !activityItems.value.length) {
    return byMessage
  }

  const ordered = [...activityItems.value].sort(
    (a, b) => new Date(a.occurred_at).getTime() - new Date(b.occurred_at).getTime()
  )

  let cursor = 0
  for (const message of messages) {
    const at = new Date(message.created_at).getTime()
    const bucket: TimelineItem[] = []
    while (cursor < ordered.length && new Date(ordered[cursor].occurred_at).getTime() <= at) {
      bucket.push(ordered[cursor])
      cursor++
    }
    if (bucket.length) byMessage.set(message.id, bucket)
  }
  return byMessage
})

/** Anything that happened after the last message in the thread. */
const trailingPills = computed<TimelineItem[]>(() => {
  const messages = contactsStore.messages
  if (!showActivity.value || !activityItems.value.length) return []
  if (!messages.length) return activityItems.value
  const last = new Date(messages[messages.length - 1].created_at).getTime()
  return activityItems.value
    .filter(item => new Date(item.occurred_at).getTime() > last)
    .sort((a, b) => new Date(a.occurred_at).getTime() - new Date(b.occurred_at).getTime())
})

// Service window state (plan 10, 4.1).
//
// The server says whether the window was open when the contact was serialised.
// An agent can sit on a chat for hours, so the countdown is computed from
// last_inbound_at against a ticking clock: the banner flips while the chat is
// open rather than the next time something happens to refetch.
const SERVICE_WINDOW_MS = 24 * 60 * 60 * 1000
const WINDOW_WARNING_MS = 3 * 60 * 60 * 1000

const clockTick = ref(Date.now())
let windowTicker: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  // A minute is fine: the number shown is hours and minutes.
  windowTicker = setInterval(() => { clockTick.value = Date.now() }, 60_000)
})
onUnmounted(() => {
  queueUnsubscribers.forEach(stop => stop())
  queueUnsubscribers.length = 0
})

onUnmounted(() => { if (windowTicker) clearInterval(windowTicker) })

/** Milliseconds left in the 24-hour window, or null when there is no inbound. */
const serviceWindowRemaining = computed<number | null>(() => {
  const at = contactsStore.currentContact?.last_inbound_at
  if (!at) return null
  const closesAt = new Date(at).getTime() + SERVICE_WINDOW_MS
  return closesAt - clockTick.value
})

const isServiceWindowExpired = computed(() => {
  const contact = contactsStore.currentContact
  if (!contact) return false
  const remaining = serviceWindowRemaining.value
  if (remaining !== null) return remaining <= 0
  // No inbound message recorded: fall back to what the server decided.
  return contact.service_window_open === false
})

/** True while the window is open but close enough to matter. */
const isServiceWindowClosing = computed(() => {
  const remaining = serviceWindowRemaining.value
  return remaining !== null && remaining > 0 && remaining <= WINDOW_WARNING_MS
})

const serviceWindowCountdown = computed(() => {
  const remaining = serviceWindowRemaining.value ?? 0
  const minutes = Math.max(0, Math.floor(remaining / 60_000))
  const hours = Math.floor(minutes / 60)
  if (hours >= 1) return t('chat.windowClosesInHours', { hours, minutes: minutes % 60 })
  return t('chat.windowClosesInMinutes', { minutes })
})

function openTemplatePicker() {
  const btn = templatePickerRef.value?.querySelector('button')
  btn?.click()
}

// Add contact dialog state
const isAddContactOpen = ref(false)

function openAddContactDialog() {
  isAddContactOpen.value = true
}

async function onContactCreated(contact: any) {
  // Refresh contacts and select the new one
  await contactsStore.fetchContacts()
  if (contact?.id) {
    router.push({ name: 'chat-conversation', params: { contactId: contact.id } })
  }
}

// Infinite scroll for contacts (load more at bottom)
const contactsScroll = useInfiniteScroll({
  direction: 'bottom',
  onLoadMore: () => contactsStore.loadMoreContacts(),
  hasMore: computed(() => contactsStore.hasMoreContacts),
  isLoading: computed(() => contactsStore.isLoadingMoreContacts)
})

// Infinite scroll for messages (load older at top)
const messagesScroll = useInfiniteScroll({
  direction: 'top',
  onLoadMore: async () => {
    if (!contactsStore.currentContact) return
    await messagesScroll.preserveScrollPosition(async () => {
      await contactsStore.fetchOlderMessages(contactsStore.currentContact!.id, selectedAccount.value || undefined)
      await nextTick()
      // Load media for any new messages
      try {
      } catch (e) {
        console.error('Error loading media:', e)
      }
    })
  },
  hasMore: computed(() => contactsStore.hasMoreMessages),
  isLoading: computed(() => contactsStore.isLoadingOlderMessages),
  onScroll: (event) => {
    const el = event.target as HTMLElement
    updateStickyDate(el)
    updateAtBottom(el)
  }
})

function updateAtBottom(el: HTMLElement) {
  const distanceFromBottom = el.scrollHeight - el.clientHeight - el.scrollTop
  isAtBottom.value = distanceFromBottom < SCROLL_BOTTOM_THRESHOLD
}

const contactId = computed(() => route.params.contactId as string | undefined)

// Get active transfer for current contact from the store (reactive)
const activeTransfer = computed(() => {
  if (!contactsStore.currentContact) return null
  return transfersStore.getActiveTransferForContact(contactsStore.currentContact.id)
})

const activeTransferId = computed(() => activeTransfer.value?.id || null)

// Check if current user can assign contacts (admin or manager only)
const canAssignContacts = computed(() => {
  // Try store first, then fallback to localStorage
  let role = authStore.userRole
  if (!role || role === 'agent') {
    try {
      const storedUser = localStorage.getItem('user')
      if (storedUser) {
        const user = JSON.parse(storedUser)
        role = user.role?.name || user.role // Support both old and new format
      }
    } catch {
      // ignore
    }
  }
  return role === 'admin' || role === 'manager'
})

// Get list of users for assignment
const assignableUsers = computed(() => {
  return usersStore.users.filter(u => u.is_active)
})

// Icon mapping for custom actions
const actionIconMap: Record<string, any> = {
  'ticket': Ticket,
  'user': User,
  'bar-chart': BarChart,
  'link': Link,
  'phone': Phone,
  'mail': Mail,
  'file-text': FileText,
  'external-link': ExternalLink,
  'zap': Zap,
  'globe': Globe,
  'code': Code
}

function getActionIcon(iconName: string) {
  return actionIconMap[iconName] || Zap
}

async function fetchCustomActions() {
  try {
    const response = await customActionsService.list()
    const data = (response.data as any).data || response.data
    customActions.value = (data.custom_actions || []).filter((a: CustomAction) => a.is_active)
  } catch (error) {
    // Silently fail - custom actions are optional
    console.error('Failed to fetch custom actions:', error)
  }
}

function toggleTagFilter(tagName: string) {
  const index = contactsStore.selectedTags.indexOf(tagName)
  if (index === -1) {
    contactsStore.selectedTags.push(tagName)
  } else {
    contactsStore.selectedTags.splice(index, 1)
  }
  // Refetch contacts with new filter
  contactsStore.fetchContacts()
}

function clearTagFilter() {
  contactsStore.selectedTags = []
  contactsStore.fetchContacts()
}

async function executeCustomAction(action: CustomAction) {
  if (!contactsStore.currentContact || executingActionId.value) return

  executingActionId.value = action.id
  try {
    const response = await customActionsService.execute(action.id, contactsStore.currentContact.id)
    let result: ActionResult = (response.data as any).data || response.data

    // JavaScript actions are now executed server-side via goja.
    // The response already contains structured result fields (toast, clipboard, redirect_url, message).

    // Handle different result types
    if (result.redirect_url) {
      // Open URL action result - prepend base path for relative URLs
      let redirectUrl = result.redirect_url
      if (redirectUrl.startsWith('/api/')) {
        const basePath = ((window as any).__BASE_PATH__ ?? '').replace(/\/$/, '')
        redirectUrl = basePath + redirectUrl
      }
      try {
        const parsed = new URL(redirectUrl, window.location.origin)
        if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
          window.open(parsed.href, '_blank')
        }
      } catch {
        // Invalid URL, ignore
      }
    }

    if (result.clipboard) {
      // Copy to clipboard
      await navigator.clipboard.writeText(result.clipboard)
      toast.success(t('common.copiedToClipboard'))
    }

    if (result.toast) {
      // Show toast notification
      if (result.toast.type === 'success') {
        toast.success(result.toast.message)
      } else if (result.toast.type === 'error') {
        toast.error(result.toast.message)
      } else {
        toast.info(result.toast.message)
      }
    } else if (result.success && !result.redirect_url && !result.clipboard) {
      // Default success message
      toast.success(result.message || t('chat.actionExecuted'))
    } else if (!result.success) {
      toast.error(result.message || t('chat.actionFailed'))
    }
  } catch (error: any) {
    const message = error.response?.data?.message || 'Failed to execute action'
    toast.error(message)
  } finally {
    executingActionId.value = null
  }
}

// Search state for assignment dialog
const assignSearchQuery = ref('')

// Filtered users for assignment dialog
const filteredAssignableUsers = computed(() => {
  const query = assignSearchQuery.value.toLowerCase().trim()
  if (!query) return assignableUsers.value
  return assignableUsers.value.filter(u =>
    u.full_name.toLowerCase().includes(query) ||
    u.email.toLowerCase().includes(query)
  )
})

// Fetch contacts on mount (WebSocket is connected in AppLayout)
onMounted(() => {
  // The queue is the default landing state, so it loads with the page rather
  // than when somebody first clicks a tab.
  if (isQueueView.value) void fetchQueue()
  void fetchQueueCounts()
  for (const event of ['new_message', 'conversation_updated']) {
    queueUnsubscribers.push(wsService.subscribe(event, () => {
      if (isQueueView.value) void fetchQueue()
      void fetchQueueCounts()
    }))
  }
})

const queueUnsubscribers: Array<() => void> = []

onMounted(async () => {
  // Ensure auth session is restored
  if (!authStore.isAuthenticated) {
    authStore.restoreSession()
  }

  await contactsStore.fetchContacts()

  // Setup infinite scroll for contacts list
  await nextTick()
  contactsScroll.setup()

  // Fetch transfers to track active transfers
  transfersStore.fetchTransfers({ status: 'active' })

  // Fetch users if can assign contacts
  if (canAssignContacts.value) {
    usersStore.fetchUsers().catch(() => {
      // Silently fail if user list can't be loaded
    })
  }

  // Fetch custom actions for admins/managers
  if (canAssignContacts.value) {
    fetchCustomActions()
  }

  // Fetch org-level WhatsApp accounts for account tabs
  try {
    const res = await accountsService.list()
    orgAccounts.value = res.data.data?.accounts || []
  } catch {
    orgAccounts.value = []
  }

  // Fetch available tags for filtering (if not already loaded)
  if (tagsStore.tags.length === 0) {
    tagsStore.fetchTags().catch(() => {})
  }

  if (contactId.value) {
    await selectContact(contactId.value)
  }

  // Auto-scroll to the unread divider and mark messages read when the agent
  // returns — covers both tab-switch (visibilitychange) and OS window focus
  // (focus event), since "tab visible but window unfocused" is a real state
  // and we don't want to send blue-tick receipts when no one is looking.
  // See issue #280.
  document.addEventListener('visibilitychange', onUserActive)
  window.addEventListener('focus', onUserActive)
})

function onUserActive() {
  if (document.visibilityState !== 'visible' || !document.hasFocus()) return
  if (!firstUnreadId.value) return
  if (contactsStore.currentContact) {
    contactsService.markRead(contactsStore.currentContact.id)
      .catch(() => { /* non-critical */ })
  }
  nextTick(() => {
    const el = document.getElementById(`message-${firstUnreadId.value}`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
  })
}

/**
 * Watch the open contact's topic, replacing whatever was watched before.
 *
 * Switching chats has to unsubscribe the old one: a panel that accumulated
 * subscriptions would be refetching for every contact an agent had visited.
 */
let stopWatchingContact: (() => void) | null = null

function watchContactTopic(id: string) {
  stopWatchingContact?.()

  const stops = [wsService.subscribeTopics([`contact:${id}`])]
  const refresh = (payload: any) => {
    if (payload?.contact_id === id) void contactsStore.refreshContactRow(id)
  }
  for (const event of ['contact_updated', 'custom_fields_updated']) {
    stops.push(wsService.subscribe(event, refresh))
  }

  // A pill describes something that just happened, so the thread has to hear
  // about it. Without this the activity only appears on the next reload, by
  // which time the agent has usually stopped wondering.
  const refreshPills = (payload: any) => {
    if (payload?.contact_id === id) void loadActivity()
  }
  for (const event of ['crm_event', 'conversation_updated', 'contact_updated']) {
    stops.push(wsService.subscribe(event, refreshPills))
  }

  stopWatchingContact = () => stops.forEach(stop => stop())
}

onUnmounted(() => {
  stopWatchingContact?.()
  stopWatchingContact = null
  wsService.setCurrentContact(null)
  // Clear current contact when leaving chat view so notifications work on other pages
  contactsStore.setCurrentContact(null)
  notesStore.clearNotes()
  // Clear sticky date timeout
  if (stickyDateTimeout) clearTimeout(stickyDateTimeout)
  document.removeEventListener('visibilitychange', onUserActive)
  window.removeEventListener('focus', onUserActive)
})

function updateStickyDate(scrollContainer: HTMLElement) {
  // Find all date separator elements
  const dateSeparators = scrollContainer.querySelectorAll('[data-date-separator]')
  if (dateSeparators.length === 0) return

  const containerRect = scrollContainer.getBoundingClientRect()
  const containerTop = containerRect.top + 60 // Offset for sticky header position

  // Find the last date separator that's above the viewport top
  let currentDate = ''
  for (const separator of dateSeparators) {
    const rect = separator.getBoundingClientRect()
    if (rect.top < containerTop) {
      currentDate = separator.getAttribute('data-date-separator') || ''
    } else {
      break
    }
  }

  // Show sticky date if we have scrolled past at least one date separator
  if (currentDate && scrollContainer.scrollTop > 50) {
    stickyDate.value = currentDate
    showStickyDate.value = true

    // Hide after scrolling stops
    if (stickyDateTimeout) clearTimeout(stickyDateTimeout)
    stickyDateTimeout = setTimeout(() => {
      showStickyDate.value = false
    }, 1500)
  } else {
    showStickyDate.value = false
  }
}

// Watch for route changes
watch(contactId, async (newId) => {
  if (newId) {
    notesStore.notes = []
    notesStore.hasMore = false
    await selectContact(newId)
  } else {
    wsService.setCurrentContact(null)
    contactsStore.setCurrentContact(null)
    contactsStore.clearMessages()
    notesStore.clearNotes()
  }
})

async function selectContact(id: string) {
  // Direct deep links to /chat/:id may target a contact that isn't in the
  // currently-loaded (paginated) list — fall back to fetching it directly.
  let contact = contactsStore.contacts.find(c => c.id === id)
  if (!contact) {
    contact = await contactsStore.fetchContact(id)
  }
  if (contact) {
    // Reset unread pill — fetchMessages will mark everything read on the server
    newMessagesCount.value = 0
    firstUnreadId.value = null
    isAtBottom.value = true

    // Remove old scroll listener before switching contacts
    messagesScroll.cleanup()

    // Reset account selection when switching contacts
    selectedAccount.value = null
    contactAccounts.value = []
    contactsStore.setAccountFilter(null)

    contactsStore.setCurrentContact(contact)

    // A deep link from the contact timeline names the message it describes
    // (plan 02). Loading the page around it lands on that exchange instead of
    // on the newest messages, which on a long history is nowhere near it.
    const anchorId = typeof route.query.around === 'string' ? route.query.around : undefined
    await contactsStore.fetchMessages(id, anchorId ? { around: anchorId } : undefined)
    if (anchorId) {
      // After the list has painted, or there is nothing to scroll to yet.
      await nextTick()
      scrollToMessage(anchorId)
    }

    // Discover distinct accounts from the unfiltered message set
    const accounts = new Set<string>()
    for (const msg of contactsStore.messages) {
      if (msg.whatsapp_account) accounts.add(msg.whatsapp_account)
    }
    contactAccounts.value = Array.from(accounts).sort()

    // Auto-select account and filter client-side (avoids a second fetch)
    if (orgAccounts.value.length > 1) {
      // Find account of the most recent incoming message
      for (let i = contactsStore.messages.length - 1; i >= 0; i--) {
        const msg = contactsStore.messages[i]
        if (msg.direction === 'incoming' && msg.whatsapp_account) {
          selectedAccount.value = msg.whatsapp_account
          break
        }
      }
      // Fallback to contact's default account, then first org account
      if (!selectedAccount.value) {
        selectedAccount.value = contact.whatsapp_account || contactAccounts.value[0] || orgAccounts.value[0]?.name
      }
      if (selectedAccount.value) {
        contactsStore.setAccountFilter(selectedAccount.value)
        // Filter messages client-side instead of re-fetching
        contactsStore.messages = contactsStore.messages.filter(
          (m: any) => m.whatsapp_account === selectedAccount.value
        )
      }
    } else if (contactAccounts.value.length === 1) {
      selectedAccount.value = contactAccounts.value[0]
    } else if (contact.whatsapp_account) {
      selectedAccount.value = contact.whatsapp_account
    }

    // Tell WebSocket server which contact we're viewing
    wsService.setCurrentContact(id)
    // And subscribe to its topic (plan 10, S10), so a field edited on the
    // profile in another tab, or a merge, reaches this panel. set_contact
    // above is the older single-valued mechanism and stays until every
    // client is on topics.
    watchContactTopic(id)
    // The pills describe this contact, so they are refetched with them. A
    // no-op when the toggle is off.
    void loadActivity()
    // The fields a message can be copied into (plan 10, 4.1). Read once per
    // contact rather than per hover.
    void loadMessageActionFields()
    // Wait for DOM to render messages before scrolling
    await nextTick()
    // Load media for messages after messages are fetched
    try {
    } catch (e) {
      console.error('Error loading media:', e)
    }
    // Scroll after a brief delay to ensure content is rendered (instant on initial load)
    setTimeout(() => {
      scrollToBottom(true)
      // Setup scroll listener for infinite scroll after initial scroll
      messagesScroll.setup()
    }, 50)

    // Fetch notes and session data in parallel (independent requests)
    const [, sessionResult] = await Promise.all([
      notesStore.fetchNotes(id),
      contactsService.getSessionData(id).catch(() => null)
    ])
    if (sessionResult) {
      contactSessionData.value = sessionResult.data.data || sessionResult.data
      if (contactSessionData.value?.panel_config?.sections?.length > 0) {
        isInfoPanelOpen.value = true
      }
    } else {
      contactSessionData.value = null
    }
  }
}

// Watch for new messages. WhatsApp Web style: while the browser tab is
// focused on this chat the user is "watching", so auto-scroll if they're
// at the bottom. When they're on another tab, pile up unread and surface
// a divider above the first message that arrived while away (issue #280).
// The two branches are mutually exclusive — auto-scrolling while the tab
// is hidden races with the divider state.
watch(() => contactsStore.messages.length, (newLen, oldLen) => {
  if (newLen <= oldLen) return
  const latest = contactsStore.messages[newLen - 1]
  const isIncoming = latest?.direction === 'incoming'
  // "Not actively looking" covers both other-tab (hidden) and other-window
  // (visible but unfocused). The divider should pile in either case.
  const userAway = typeof document !== 'undefined'
    && (document.visibilityState === 'hidden' || !document.hasFocus())
  if (isIncoming && userAway) {
    if (newMessagesCount.value === 0) {
      firstUnreadId.value = latest.id
    }
    newMessagesCount.value += 1
    return
  }
  // Outgoing (the agent replied) — they've seen the unread, drop the divider.
  if (!isIncoming && newMessagesCount.value > 0) {
    newMessagesCount.value = 0
    firstUnreadId.value = null
  }
  if (isAtBottom.value || !isIncoming) {
    scrollToBottom()
  }
})

// Watch for messages changes to load media
watch(() => contactsStore.messages, () => {
  try {
  } catch (e) {
    console.error('Error loading media:', e)
  }
}, { deep: true })

async function switchAccount(accountName: string) {
  if (!contactsStore.currentContact || accountName === selectedAccount.value) return
  selectedAccount.value = accountName
  contactsStore.setAccountFilter(accountName)
  await contactsStore.fetchMessages(contactsStore.currentContact.id, { account: accountName })
  await nextTick()
  try {
  } catch (e) {
    console.error('Error loading media:', e)
  }
  scrollToBottom(true)
}

/**
 * Opens a conversation from either list.
 *
 * The inbox used to link rows to /chat?contact=<id> — a query the chat route
 * never read, because it takes a path parameter — so clicking a queue row
 * landed on an empty chat and you had to find the person again. One surface,
 * one path, and the list stays where it is while the thread changes.
 */
function openConversation(contactId: string) {
  router.push(`/inbox/${contactId}`)
}



async function sendMessage() {
  if (!messageInput.value.trim() || !contactsStore.currentContact) return

  // A line beginning with a known command is not a message anybody meant to
  // send to the customer (plan 10, 4.1).
  if (await runCommandLine()) return

  isSending.value = true
  try {
    await contactsStore.sendMessage(
      contactsStore.currentContact.id,
      'text',
      { body: messageInput.value },
      contactsStore.replyingTo?.id,
      selectedAccount.value || undefined
    )
    messageInput.value = ''
    contactsStore.clearReplyingTo()
    resetTextareaHeight()
    await nextTick()
    scrollToBottom()
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.sendMessageFailed')))
  } finally {
    isSending.value = false
  }
}

const retryingMessageId = ref<string | null>(null)

async function retryMessage(message: Message) {
  if (!contactsStore.currentContact || retryingMessageId.value) return

  retryingMessageId.value = message.id
  try {
    // Get the message content based on type
    const content = message.content || {}

    await contactsStore.sendMessage(
      contactsStore.currentContact.id,
      message.message_type,
      content,
      undefined,
      message.whatsapp_account || selectedAccount.value || undefined
    )

    // Remove the failed message from the list after successful retry
    const messages = (contactsStore.messages as any).get?.(contactsStore.currentContact.id) as Message[] | undefined
    if (messages) {
      const index = messages.findIndex((m: Message) => m.id === message.id)
      if (index !== -1) {
        messages.splice(index, 1)
      }
    }

    toast.success(t('chat.messageSent'))
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.sendMessageFailed')))
  } finally {
    retryingMessageId.value = null
  }
}

function autoResizeTextarea() {
  const textarea = messageInputRef.value
  if (!textarea) return
  textarea.style.height = 'auto'
  textarea.style.height = Math.min(textarea.scrollHeight, 120) + 'px'
}

function resetTextareaHeight() {
  const textarea = messageInputRef.value
  if (!textarea) return
  textarea.style.height = 'auto'
}

function getReplyPreviewContent(message: Message): string {
  if (!message.reply_to_message) return ''
  const reply = message.reply_to_message
  if (reply.message_type === 'text') {
    const body = reply.content?.body || ''
    return body.length > 50 ? body.substring(0, 50) + '...' : body
  }
  if (reply.message_type === 'button_reply') {
    const body = typeof reply.content === 'string' ? reply.content : (reply.content?.body || '')
    return body.length > 50 ? body.substring(0, 50) + '...' : body
  }
  if (reply.message_type === 'interactive') {
    const body = typeof reply.content === 'string' ? reply.content : ((reply as any).interactive_data?.body || reply.content?.body || '')
    return body.length > 50 ? body.substring(0, 50) + '...' : body
  }
  if (reply.message_type === 'template') {
    const body = reply.content?.body || ''
    return body.length > 50 ? body.substring(0, 50) + '...' : body
  }
  if (reply.message_type === 'image') return '[Photo]'
  if (reply.message_type === 'video') return '[Video]'
  if (reply.message_type === 'audio') return '[Audio]'
  if (reply.message_type === 'document') return '[Document]'
  if (reply.message_type === 'location') return '[Location]'
  if (reply.message_type === 'contacts') return '[Contact]'
  if (reply.message_type === 'sticker') return '[Sticker]'
  return '[Message]'
}

function scrollToMessage(messageId: string | undefined) {
  if (!messageId) return
  const messageEl = document.getElementById(`message-${messageId}`)
  if (messageEl) {
    messageEl.scrollIntoView({ behavior: 'smooth', block: 'center' })
    messageEl.classList.add('highlight-message')
    setTimeout(() => messageEl.classList.remove('highlight-message'), 2000)
  }
}

function extractCannedTokens(content: string): string[] {
  const seen = new Set<string>()
  const matches = content.matchAll(/\{\{\s*([\w.-]+)\s*\}\}/g)
  for (const m of matches) seen.add(m[1])
  return Array.from(seen)
}

// Collect tokens from the message body AND every button field, so the param
// dialog prompts for custom tokens used anywhere on the response.
function extractCannedTokensFromResponse(r: CannedResponse): string[] {
  const seen = new Set<string>(extractCannedTokens(r.content))
  for (const btn of r.buttons || []) {
    for (const t of extractCannedTokens(btn.title || '')) seen.add(t)
    for (const t of extractCannedTokens(btn.url || '')) seen.add(t)
    for (const t of extractCannedTokens(btn.phone_number || '')) seen.add(t)
  }
  return Array.from(seen)
}

// Resolve a single context token (contact_name / phone_number / user_name /
// agent_name) against the current chat. Returns null for any key that isn't
// in AUTO_RESOLVED_CONTEXT_TOKENS so callers can fall back to their own param
// dict.
function resolveContextToken(key: string): string | null {
  const contact = contactsStore.currentContact
  if (key === 'contact_name') return contact?.profile_name || contact?.name || 'there'
  if (key === 'phone_number') return contact?.phone_number || ''
  if (key === 'user_name' || key === 'agent_name') return authStore.user?.full_name || ''
  return null
}

// Shared {{...}} resolver used by the body preview and the button fields, so
// `{{phone_number}}` works inside a button URL the same way it does in content.
function resolveCannedTokens(text: string): string {
  if (!text) return text
  return text.replace(/\{\{\s*([\w.-]+)\s*\}\}/g, (_match, key: string) => {
    const ctx = resolveContextToken(key)
    if (ctx !== null) return ctx
    const value = cannedParamValues.value[key]
    return value ? value : `{{${key}}}`
  })
}

// What the server said this renders to, or null while it is being fetched or
// if the request failed.
const cannedResolved = ref<{ content: string; buttons?: Record<string, any>[] } | null>(null)

const cannedPreview = computed(() => {
  if (!selectedCannedResponse.value) return ''
  // The author's own placeholders are still filled here: only the person
  // typing knows what goes in them.
  const base = cannedResolved.value?.content ?? selectedCannedResponse.value.content
  return resolveCannedTokens(base)
})

// Resolved buttons (with {{...}} substitution applied) for the dialog preview.
// Empty array when no response is selected or it has no buttons.
const cannedPreviewButtons = computed(() => {
  const raw: any[] = (cannedResolved.value?.buttons as any[]) ?? selectedCannedResponse.value?.buttons ?? []
  return raw.map((b: any) => ({
    ...b,
    title: resolveCannedTokens(b.title),
    ...(b.url !== undefined ? { url: resolveCannedTokens(b.url) } : {}),
    ...(b.phone_number !== undefined ? { phone_number: resolveCannedTokens(b.phone_number) } : {}),
  }))
})

function handleCannedSelect(response: CannedResponse) {
  selectedCannedResponse.value = response
  const tokens = extractCannedTokensFromResponse(response).filter(
    t => !AUTO_RESOLVED_CONTEXT_TOKENS.has(t)
  )
  cannedParamNames.value = tokens
  cannedParamValues.value = Object.fromEntries(tokens.map(t => [t, '']))
  cannedResolved.value = null

  // Drop the slash command (or any stray text) so the textarea starts clean.
  messageInput.value = ''
  resetTextareaHeight()
  cannedPickerOpen.value = false
  cannedSearchQuery.value = ''
  cannedDialogOpen.value = true

  // The server's answer fills in when it arrives. The dialog opens first on
  // purpose: the agent has already chosen, and making them wait on a round
  // trip to see the response they picked is a worse trade than a preview that
  // sharpens a moment later — the client-side resolver covers the common
  // tokens in the meantime.
  void resolveCannedOnServer(response)
}

/**
 * Ask the server what this canned response says for this contact (plan 10, S6).
 *
 * The browser knows four token names; the server knows the whole namespace —
 * owners, custom fields, the team the conversation sits in. A failure leaves
 * the client-side resolution in place rather than blanking the preview.
 */
async function resolveCannedOnServer(response: CannedResponse) {
  const contactId = contactsStore.currentContact?.id
  if (!contactId) return

  try {
    const { data } = await cannedResponsesService.resolve(response.id, { contact_id: contactId })
    // Guard against a slow answer for a response the agent has since changed
    // away from, which would otherwise overwrite the newer preview.
    if (selectedCannedResponse.value?.id !== response.id) return
    cannedResolved.value = (data as any)?.data ?? data
  } catch {
    cannedResolved.value = null
  }
}

async function sendCannedResponse() {
  if (!contactsStore.currentContact || !selectedCannedResponse.value) return

  const missing = cannedParamNames.value.some(n => !cannedParamValues.value[n]?.trim())
  if (missing) {
    toast.error(t('chat.parameterRequired'))
    return
  }

  const body = cannedPreview.value
  const responseId = selectedCannedResponse.value.id
  // Substitute {{...}} tokens in every button field — same rules as the body —
  // so URLs like https://x.com/u/{{phone_number}} resolve at send time.
  const buttons = (selectedCannedResponse.value.buttons || []).map(b => ({
    ...b,
    title: resolveCannedTokens(b.title),
    ...(b.url !== undefined ? { url: resolveCannedTokens(b.url) } : {}),
    ...(b.phone_number !== undefined ? { phone_number: resolveCannedTokens(b.phone_number) } : {}),
  }))
  const replyButtons = buttons.filter(b => !b.type || b.type === 'reply')
  const urlButtons = buttons.filter(b => b.type === 'url')

  // WhatsApp Cloud API supports: 1-3 reply buttons (interactive.button),
  // 4-10 reply rows (interactive.list — backend's SendInteractiveButtons
  // auto-picks the right shape), a single cta_url, or a single voice_call
  // (Business Calling click-to-call). Phone buttons and multi-URL / mixed
  // combos aren't representable; the detail-page validator blocks save for
  // those, so the text fallback here is just a safety net.
  const voiceCallButtons = buttons.filter(b => b.type === 'voice_call')
  const flowButtons = buttons.filter(b => b.type === 'flow')
  let sendType: 'text' | 'interactive' = 'text'
  let interactive: {
    type: 'button' | 'list' | 'cta_url' | 'voice_call' | 'flow'
    body: string
    buttons?: Array<{ id: string; title: string }>
    button_text?: string
    url?: string
    display_text?: string
    ttl_minutes?: number
    flow_id?: string
    first_screen?: string
  } | undefined

  if (buttons.length === 1 && flowButtons.length === 1) {
    const f = flowButtons[0]
    sendType = 'interactive'
    interactive = {
      type: 'flow',
      body,
      // The button title is the CTA label shown to the customer.
      button_text: resolveCannedTokens(f.title),
      flow_id: f.flow_id,
      first_screen: f.screen,
    }
  } else if (buttons.length === 1 && voiceCallButtons.length === 1) {
    const vc = voiceCallButtons[0]
    sendType = 'interactive'
    interactive = {
      type: 'voice_call',
      body,
      // {{...}} tokens already resolved in the canned-preview path; the
      // button title is what becomes Meta's display_text. Backend
      // truncates to 20 chars and stamps the agent-id payload.
      display_text: resolveCannedTokens(vc.title),
      ttl_minutes: vc.ttl_minutes ?? 15,
    }
  } else if (buttons.length > 0 && replyButtons.length === buttons.length && replyButtons.length <= 10) {
    sendType = 'interactive'
    interactive = {
      type: replyButtons.length <= 3 ? 'button' : 'list',
      body,
      buttons: replyButtons.map(b => ({ id: b.id, title: b.title })),
    }
  } else if (buttons.length === 1 && urlButtons.length === 1) {
    sendType = 'interactive'
    interactive = {
      type: 'cta_url',
      body,
      button_text: urlButtons[0].title,
      url: urlButtons[0].url || '',
    }
  }

  isSendingCanned.value = true
  try {
    await contactsStore.sendMessage(
      contactsStore.currentContact.id,
      sendType,
      sendType === 'interactive' ? { body } : { body },
      contactsStore.replyingTo?.id,
      selectedAccount.value || undefined,
      interactive ? { interactive } : undefined,
    )
    cannedResponsesService.use(responseId).catch(() => {})
    contactsStore.clearReplyingTo()
    cannedDialogOpen.value = false
    selectedCannedResponse.value = null
    cannedParamNames.value = []
    cannedParamValues.value = {}
    await nextTick()
    scrollToBottom()
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.sendMessageFailed')))
  } finally {
    isSendingCanned.value = false
  }
}

function closeCannedPicker() {
  cannedPickerOpen.value = false
  cannedSearchQuery.value = ''
}

function insertEmoji(emoji: string) {
  messageInput.value += emoji
  emojiPickerOpen.value = false
}

// Template message handling
function getTemplateBodyContent(tpl: any): string {
  return tpl.body_content || ''
}

const templatePreview = computed(() => {
  if (!selectedTemplate.value) return ''
  const body = getTemplateBodyContent(selectedTemplate.value)
  return body.replace(/\{\{\s*([\w.-]+)\s*\}\}/g, (_match, key: string) => {
    const supplied = templateParamValues.value[key]
    if (supplied) return supplied
    const ctx = resolveContextToken(key)
    if (ctx !== null) return ctx
    return `{{${key}}}`
  })
})

// Show the header input only when the user has to fill it. Context-token
// names (contact_name, phone_number, …) auto-resolve and stay hidden — same
// rule body params follow via templateParamNames filtering.
const showHeaderParamInput = computed(() =>
  !!templateHeaderParamName.value &&
  !AUTO_RESOLVED_CONTEXT_TOKENS.has(templateHeaderParamName.value)
)

function extractButtonUrlParams(buttons: any[]): { index: number; text: string; value: string; type: string }[] {
  if (!buttons?.length) return []
  return buttons
    .map((btn: any, index: number) => {
      if (btn.type === 'COPY_CODE') {
        return { index, text: btn.text || 'Copy Code', value: btn.example?.[0] || '', type: 'COPY_CODE' }
      }
      if (btn.type !== 'URL' || !btn.url) return null
      const hasParams = /\{\{[^}]+\}\}/.test(btn.url)
      if (!hasParams) return null
      return { index, text: btn.text || 'URL Button', value: '', type: 'URL' }
    })
    .filter((b): b is { index: number; text: string; value: string; type: string } => b !== null)
}

function handleTemplateWithParams(template: any, paramNames: string[]) {
  selectedTemplate.value = template
  // Pre-fill body context tokens from the conversation; keep them in the
  // payload dict so the backend forwards them to Meta, but hide them from
  // the dialog so the agent doesn't have to type values we already know —
  // same pattern as canned responses (see handleCannedSelect).
  const initial: Record<string, string> = {}
  for (const name of paramNames) {
    const resolved = resolveContextToken(name)
    initial[name] = resolved ?? ''
  }
  templateParamValues.value = initial
  templateParamNames.value = paramNames.filter(n => !AUTO_RESOLVED_CONTEXT_TOKENS.has(n))

  // Identify the TEXT-header variable (max 1) and pre-fill from context.
  // Context-token names (contact_name / phone_number / agent_name / user_name)
  // resolve automatically and stay hidden from the dialog — same convention
  // as body params.
  templateHeaderParamName.value = null
  templateHeaderParamValue.value = ''
  if (template.header_type === 'TEXT' && template.header_content) {
    const m = template.header_content.match(/\{\{([^}]+)\}\}/)
    if (m) {
      const name = m[1].trim()
      templateHeaderParamName.value = name
      templateHeaderParamValue.value = resolveContextToken(name) ?? ''
    }
  }

  clearTemplateHeaderMedia()
  templateButtonUrlParams.value = extractButtonUrlParams(template.buttons)
  templateDialogOpen.value = true
}

async function sendTemplateMessage() {
  if (!contactsStore.currentContact || !selectedTemplate.value) return

  // Validate header param (separate ref so it can hold its own value even
  // when the body has a {{1}} that would otherwise collide). Auto-resolved
  // context tokens are exempt — their value comes from the conversation.
  if (showHeaderParamInput.value && !templateHeaderParamValue.value.trim()) {
    toast.error(t('chat.parameterRequired'))
    return
  }

  // Validate all body params are filled
  const missingBody = templateParamNames.value.some(n => !templateParamValues.value[n]?.trim())
  if (missingBody) {
    toast.error(t('chat.parameterRequired'))
    return
  }

  // Validate header media if required
  if (templateNeedsHeaderMedia.value && !templateHeaderFile.value) {
    toast.error(t('chat.headerMediaRequired'))
    return
  }

  // Validate all button URL params are filled
  const missingButton = templateButtonUrlParams.value.some(b => !b.value?.trim())
  if (missingButton) {
    toast.error(t('chat.parameterRequired'))
    return
  }

  // Build button params map: button index -> value
  const buttonParams: Record<string, string> | undefined =
    templateButtonUrlParams.value.length > 0
      ? Object.fromEntries(templateButtonUrlParams.value.map(b => [String(b.index), b.value]))
      : undefined

  // Header value goes in its own payload field so a positional {{1}} header
  // doesn't overwrite a positional {{1}} body parameter in the flat map.
  const headerParams: Record<string, string> | undefined =
    templateHeaderParamName.value && templateHeaderParamValue.value
      ? { [templateHeaderParamName.value]: templateHeaderParamValue.value }
      : undefined

  isSendingTemplate.value = true
  try {
    await contactsStore.sendTemplate(
      contactsStore.currentContact.id,
      selectedTemplate.value.name,
      templateParamValues.value,
      selectedAccount.value || undefined,
      templateHeaderFile.value || undefined,
      buttonParams,
      headerParams
    )
    toast.success(t('chat.templateSent'))
    templateDialogOpen.value = false
    selectedTemplate.value = null
    templateParamNames.value = []
    templateParamValues.value = {}
    templateHeaderParamName.value = null
    templateHeaderParamValue.value = ''
    clearTemplateHeaderMedia()
    templateButtonUrlParams.value = []
  } catch (error: any) {
    const message = error.response?.data?.message || t('chat.templateSendFailed')
    toast.error(message)
  } finally {
    isSendingTemplate.value = false
  }
}

// Reaction handling
const reactionPickerMessageId = ref<string | null>(null)
const quickReactionEmojis = ['👍', '❤️', '😂', '😮', '😢', '🙏']

async function sendReaction(messageId: string, emoji: string) {
  if (!contactsStore.currentContact) return

  try {
    const response = await messagesService.sendReaction(
      contactsStore.currentContact.id,
      messageId,
      emoji
    )
    // Update will come via WebSocket, but we can update locally for immediate feedback
    const data = response.data.data || response.data
    contactsStore.updateMessageReactions(messageId, data.reactions)
  } catch (error) {
    toast.error(t('chat.reactionFailed'))
  }
  reactionPickerMessageId.value = null
}

function _toggleReactionPicker(messageId: string) {
  if (reactionPickerMessageId.value === messageId) {
    reactionPickerMessageId.value = null
  } else {
    reactionPickerMessageId.value = messageId
  }
}
void _toggleReactionPicker // Suppress unused warning

function replyToMessage(message: Message) {
  contactsStore.setReplyingTo(message)
  nextTick(() => {
    messageInputRef.value?.focus()
  })
}

// Watch for slash commands in message input
watch(messageInput, (val) => {
  // A command takes the slash; canned responses keep it otherwise. Both on
  // screen at once would be two menus fighting over one keystroke.
  if (isCommanding.value) {
    cannedPickerOpen.value = false
    cannedSearchQuery.value = ''
    return
  }
  if (val.startsWith('/')) {
    const query = val.slice(1) // Remove the leading /
    cannedSearchQuery.value = query
    cannedPickerOpen.value = true
  } else if (cannedPickerOpen.value) {
    // Close picker if user removes the /
    cannedPickerOpen.value = false
    cannedSearchQuery.value = ''
  }
})

async function assignContactToUser(userId: string | null) {
  if (!contactsStore.currentContact) return

  try {
    await contactsService.assign(contactsStore.currentContact.id, userId)
    toast.success(userId ? t('chat.contactAssigned') : t('chat.contactUnassigned'))
    // Update current contact with new assignment
    contactsStore.currentContact = {
      ...contactsStore.currentContact,
      assigned_user_id: userId || undefined
    }
    // Refresh contacts list
    await contactsStore.fetchContacts()
  } catch (error: any) {
    const message = error.response?.data?.message || t('chat.assignFailed')
    toast.error(message)
  }
}

async function transferToAgent() {
  if (!contactsStore.currentContact) return

  isTransferring.value = true
  try {
    await chatbotService.createTransfer({
      contact_id: contactsStore.currentContact.id,
      whatsapp_account: (contactsStore.currentContact as any).whatsapp_account,
      source: 'manual'
    })
    toast.success(t('chat.transferSuccess'), {
      description: t('chat.transferSuccessDesc')
    })
    // Refresh transfers store (WebSocket will also update, but this ensures immediate sync)
    await transfersStore.fetchTransfers({ status: 'active' })
  } catch (error: any) {
    const message = error.response?.data?.message || t('chat.transferFailed')
    toast.error(message)
  } finally {
    isTransferring.value = false
  }
}

async function resumeChatbot() {
  if (!activeTransferId.value) return

  const currentContactId = contactsStore.currentContact?.id
  isResuming.value = true
  try {
    await chatbotService.resumeTransfer(activeTransferId.value)
    toast.success(t('chat.resumeSuccess'), {
      description: t('chat.resumeSuccessDesc')
    })
    // Refresh transfers store to update UI
    await transfersStore.fetchTransfers({ status: 'active' })
    // Refresh contacts list (assignment may have changed)
    await contactsStore.fetchContacts()

    // Check if current contact is still in the list (may have been unassigned)
    if (currentContactId) {
      const stillExists = contactsStore.contacts.some(c => c.id === currentContactId)
      if (!stillExists) {
        // Contact no longer visible to this user, navigate away
        contactsStore.setCurrentContact(null)
        contactsStore.clearMessages()
        router.push('/inbox')
      }
    }
  } catch (error: any) {
    const message = error.response?.data?.message || t('chat.resumeFailed')
    toast.error(message)
  } finally {
    isResuming.value = false
  }
}

function scrollToBottom(instant = false) {
  nextTick(() => {
    if (messagesEndRef.value) {
      messagesEndRef.value.scrollIntoView({
        behavior: instant ? 'instant' : 'smooth',
        block: 'end'
      })
    }
  })
}


function getMessageStatusIcon(status: string) {
  switch (status) {
    case 'sent':
      return Check
    case 'delivered':
      return CheckCheck
    case 'read':
      return CheckCheck
    case 'failed':
      return AlertCircle
    default:
      return Clock
  }
}

function getMessageStatusClass(status: string) {
  switch (status) {
    case 'read':
      return 'text-blue-400' // Bright blue for read
    case 'failed':
      return 'text-destructive'
    default:
      return 'text-muted-foreground' // Gray for sent/delivered
  }
}

function formatMessageTime(dateStr: string) {
  const date = new Date(dateStr)
  return date.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })
}

function formatContactTime(dateStr?: string) {
  if (!dateStr) return ''
  const date = new Date(dateStr)
  const now = new Date()
  const diffDays = Math.floor((now.getTime() - date.getTime()) / 86400000)

  if (diffDays === 0) {
    return date.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })
  } else if (diffDays === 1) {
    return 'Yesterday'
  } else if (diffDays < 7) {
    return date.toLocaleDateString('en-US', { weekday: 'short' })
  }
  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

function getDateLabel(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const messageDate = new Date(date.getFullYear(), date.getMonth(), date.getDate())
  const diffDays = Math.floor((today.getTime() - messageDate.getTime()) / 86400000)

  if (diffDays === 0) {
    return 'Today'
  } else if (diffDays === 1) {
    return 'Yesterday'
  }
  return date.toLocaleDateString('en-US', { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' })
}

function shouldShowDateSeparator(index: number): boolean {
  const messages = contactsStore.messages
  if (index === 0) return true

  const currentDate = new Date(messages[index].created_at)
  const prevDate = new Date(messages[index - 1].created_at)

  return currentDate.toDateString() !== prevDate.toDateString()
}

function getMessageContent(message: Message): string {
  if (message.message_type === 'text') {
    return message.content?.body || ''
  }
  if (message.message_type === 'button_reply' || message.message_type === 'nfm_reply') {
    // Button/flow reply stores the response text in content
    if (typeof message.content === 'string') {
      return message.content
    }
    return message.content?.body || ''
  }
  if (message.message_type === 'interactive' || message.message_type === 'flow') {
    // Interactive/flow messages store body text in content (string) or content.body or interactive_data.body
    if (typeof message.content === 'string') {
      return message.content
    }
    if (message.interactive_data?.body) {
      return message.interactive_data.body
    }
    return message.content?.body || '[Interactive Message]'
  }
  // For media messages, return caption if available (media is displayed inline)
  if (message.message_type === 'image' || message.message_type === 'video' || message.message_type === 'sticker') {
    return message.content?.body || ''
  }
  if (message.message_type === 'audio') {
    return '' // Audio doesn't have captions
  }
  if (message.message_type === 'document') {
    return message.content?.body || ''
  }
  if (message.message_type === 'template') {
    // Show actual content if available (campaign messages), otherwise fallback
    return message.content?.body || '[Template Message]'
  }
  if (message.message_type === 'location') {
    return '' // Location is displayed as a map/card, not text
  }
  if (message.message_type === 'contacts') {
    return '' // Contacts are displayed as a card, not text
  }
  if (message.message_type === 'unsupported') {
    return '' // Displayed as a visual card, not text
  }
  return '[Message]'
}

interface LocationData {
  latitude: number
  longitude: number
  name?: string
  address?: string
}

interface ContactData {
  name: string
  phones?: string[]
}

function getLocationData(message: Message): LocationData | null {
  if (message.message_type !== 'location') return null
  try {
    // Content is stored as JSON string in body
    const body = message.content?.body || message.content
    if (typeof body === 'string') {
      return JSON.parse(body)
    }
    return body as LocationData
  } catch {
    return null
  }
}

function getContactsData(message: Message): ContactData[] {
  if (message.message_type !== 'contacts') return []
  try {
    // Content is stored as JSON string in body
    const body = message.content?.body || message.content
    if (typeof body === 'string') {
      return JSON.parse(body)
    }
    return body as ContactData[]
  } catch {
    return []
  }
}

function getGoogleMapsUrl(location: LocationData): string {
  return `https://www.google.com/maps?q=${location.latitude},${location.longitude}`
}

function getInteractiveButtons(message: Message): Array<{ id: string; title: string; type: string; url: string }> {
  if (!message.interactive_data) {
    return []
  }
  // Support both interactive and template messages with buttons
  if (message.message_type !== 'interactive' && message.message_type !== 'template') {
    return []
  }
  // Handle both "buttons" (<=3) and "rows" (>3 list format)
  const items = message.interactive_data.buttons || message.interactive_data.rows
  if (!items || !Array.isArray(items)) {
    return []
  }
  return items.map((btn: any) => ({
    id: btn.reply?.id || btn.id || '',
    title: btn.reply?.title || btn.title || btn.text || '',
    type: btn.type || 'QUICK_REPLY',
    url: btn.url || ''
  }))
}

interface CTAUrlData {
  type: 'cta_url'
  body: string
  button_text: string
  url: string
}

function getCTAUrlData(message: Message): CTAUrlData | null {
  if (message.message_type !== 'interactive' || !message.interactive_data) {
    return null
  }
  if (message.interactive_data.type !== 'cta_url') {
    return null
  }
  return {
    type: 'cta_url',
    body: message.interactive_data.body || '',
    button_text: (message.interactive_data as any).button_text || 'Open',
    url: (message.interactive_data as any).url || ''
  }
}

interface VoiceCallData {
  display_text: string
  ttl_minutes?: number
}

function getVoiceCallData(message: Message): VoiceCallData | null {
  if (message.message_type !== 'interactive' || !message.interactive_data) {
    return null
  }
  if (message.interactive_data.type !== 'voice_call') {
    return null
  }
  return {
    display_text: (message.interactive_data as any).display_text || 'Call',
    ttl_minutes: (message.interactive_data as any).ttl_minutes,
  }
}

function getFlowButtonText(message: Message): string | null {
  if (message.message_type !== 'flow') {
    return null
  }
  if (!message.interactive_data) {
    return null
  }
  return (message.interactive_data as any).button_text || 'Open'
}

function isMediaMessage(message: Message): boolean {
  return ['image', 'video', 'audio', 'document'].includes(message.message_type)
}

function getMediaUrl(message: Message): string {
  if (!message.media_url) return ''
  const basePath = ((window as any).__BASE_PATH__ ?? '').replace(/\/$/, '')
  return `${basePath}/api/media/${message.id}`
}

// Every media attachment in the open conversation, in chronological order, that
// the in-app viewer can show (images, stickers, video, documents/PDF, plus
// template header media). This is the gallery the lightbox pages through.
const viewableMedia = computed(() =>
  contactsStore.messages.filter(
    m =>
      !!m.media_url &&
      (['image', 'sticker', 'video', 'document'].includes(m.message_type) ||
        m.message_type === 'template'),
  ),
)

// Only PDFs gain anything from the lightbox — docx/xlsx/zip have no inline
// viewer, so routing them through the modal would just add a click before the
// same download. Those bubbles keep their one-click download link; the viewer
// still shows them (with a download card) when paging through the gallery.
function isPreviewableDocument(message: Message): boolean {
  const mime = message.media_mime_type || ''
  const name = (message.media_filename || '').toLowerCase()
  return mime.includes('pdf') || name.endsWith('.pdf')
}

// Open the in-app viewer at the clicked attachment instead of a new browser tab.
function openMediaPreview(message: Message) {
  const idx = viewableMedia.value.findIndex(m => m.id === message.id)
  if (idx === -1) return
  mediaViewerIndex.value = idx
  mediaViewerOpen.value = true
}

function handleImageError(event: Event) {
  const img = event.target as HTMLImageElement
  img.style.display = 'none'
}

function handleMediaError(event: Event, mediaType: string) {
  console.error(`Failed to load ${mediaType}:`, event)
}

// File upload functions
function openFilePicker() {
  fileInputRef.value?.click()
}

async function handleFileSelect(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // reset so the same file can be selected again
  if (!file) return

  // Validate file type
  const allowedTypes = ['image/', 'video/', 'audio/', 'application/pdf', 'application/msword', 'application/vnd.openxmlformats-officedocument']
  const isAllowed = allowedTypes.some(type => file.type.startsWith(type))
  if (!isAllowed) {
    toast.error(t('chat.unsupportedFileType'), {
      description: t('chat.unsupportedFileTypeDesc')
    })
    return
  }

  // Compress images client-side so they fit the Cloud API's 5 MB image limit
  // (Meta accepts only jpeg/png for `image` messages). No-op for non-images.
  let outFile = file
  if (file.type.startsWith('image/')) {
    try {
      outFile = await compressImage(file)
    } catch {
      outFile = file
    }
  }

  // Per-type size validation: images 5 MB (Meta's limit), other media 14.5 MB
  // (under the 15 MB fasthttp request body cap).
  const type = getMediaType(outFile.type)
  const maxSize = type === 'image' ? 5 * 1024 * 1024 : 14.5 * 1024 * 1024
  if (outFile.size > maxSize) {
    toast.error(t('chat.fileTooLarge'), {
      description: type === 'image' ? t('chat.fileTooLargeImage') : t('chat.fileTooLargeMedia')
    })
    return
  }

  selectedFile.value = outFile
  mediaCaption.value = ''

  // Create preview URL for images and videos
  if (outFile.type.startsWith('image/') || outFile.type.startsWith('video/')) {
    filePreviewUrl.value = URL.createObjectURL(outFile)
  } else {
    filePreviewUrl.value = null
  }

  isMediaDialogOpen.value = true
}

function closeMediaDialog() {
  isMediaDialogOpen.value = false
  if (filePreviewUrl.value) {
    URL.revokeObjectURL(filePreviewUrl.value)
    filePreviewUrl.value = null
  }
  selectedFile.value = null
  mediaCaption.value = ''
}

function getMediaType(mimeType: string): string {
  if (mimeType.startsWith('image/')) return 'image'
  if (mimeType.startsWith('video/')) return 'video'
  if (mimeType.startsWith('audio/')) return 'audio'
  return 'document'
}

async function sendMediaMessage() {
  if (!selectedFile.value || !contactsStore.currentContact) return

  isUploadingMedia.value = true
  try {
    const formData = new FormData()
    formData.append('file', selectedFile.value)
    formData.append('contact_id', contactsStore.currentContact.id)
    formData.append('type', getMediaType(selectedFile.value.type))
    if (mediaCaption.value.trim()) {
      formData.append('caption', mediaCaption.value.trim())
    }
    if (selectedAccount.value) {
      formData.append('whatsapp_account', selectedAccount.value)
    }

    const basePath = ((window as any).__BASE_PATH__ ?? '').replace(/\/$/, '')
    const response = await fetch(`${basePath}/api/messages/media`, {
      method: 'POST',
      credentials: 'include',
      headers: getRequestHeaders({ csrf: true }),
      body: formData
    })

    if (!response.ok) {
      const error = await response.json()
      throw new Error(error.message || 'Failed to send media')
    }

    const result = await response.json()

    // Add the message to the store (addMessage has duplicate checking for WebSocket)
    if (result.data) {
      contactsStore.addMessage(result.data)
      scrollToBottom()
    }

    toast.success(t('chat.mediaSent'))
    closeMediaDialog()
  } catch (error: any) {
    toast.error(t('chat.mediaFailed'), {
      description: error.message || t('chat.mediaFailedDesc')
    })
  } finally {
    isUploadingMedia.value = false
  }
}
</script>

<template>
  <div class="flex h-full bg-[#0a0a0b] light:bg-gray-50">
    <!-- Contacts List (on mobile it takes the full width until a conversation is open) -->
    <div
      :class="[
        'w-full md:w-80 shrink-0 border-r border-white/[0.08] light:border-gray-200 flex-col bg-[#0a0a0b] light:bg-white',
        contactsStore.currentContact ? 'hidden md:flex' : 'flex'
      ]"
    >
      <!-- Search Header: same height as the main sidebar's logo row and page headers -->
      <div class="border-b border-white/[0.08] light:border-gray-200">
        <div class="flex h-16 items-center gap-1.5 px-3">
          <div class="relative flex-1">
            <Search class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-white/50 light:text-gray-500" aria-hidden="true" />
            <Input
              v-model="contactsStore.searchQuery"
              :placeholder="$t('chat.searchContacts') + '…'"
              :aria-label="$t('chat.searchContacts')"
              class="pl-8 h-8 text-sm bg-white/[0.04] border-white/[0.1] text-white placeholder:text-white/50 light:bg-gray-50 light:border-gray-200 light:text-gray-900 light:placeholder:text-gray-500"
            />
          </div>
          <!-- Add Contact -->
          <Tooltip v-if="canWriteContacts">
            <TooltipTrigger as-child>
              <Button
                variant="ghost"
                size="icon"
                :aria-label="$t('chat.addContact')"
                class="h-8 w-8 shrink-0 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100"
                @click="openAddContactDialog"
              >
                <UserPlus class="h-4 w-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{{ $t('chat.addContact') }}</TooltipContent>
          </Tooltip>
          <!-- Tag Filter -->
          <Popover v-model:open="isTagFilterOpen">
            <PopoverTrigger as-child>
              <Button
                variant="ghost"
                size="icon"
                class="h-8 w-8 shrink-0 relative"
                :aria-label="$t('chat.filterByTags')"
                :class="{
                  'text-emerald-400 bg-emerald-500/10 light:text-emerald-700 light:bg-emerald-50': contactsStore.selectedTags.length > 0,
                  'text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100':
                    contactsStore.selectedTags.length === 0
                }"
              >
                <Filter class="h-4 w-4" />
                <span v-if="contactsStore.selectedTags.length > 0" class="absolute -top-1 -right-1 h-4 w-4 rounded-full bg-emerald-500 text-[10px] text-white flex items-center justify-center">
                  {{ contactsStore.selectedTags.length }}
                </span>
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" class="w-56 p-2">
              <div class="space-y-2">
                <div class="flex items-center justify-between px-1">
                  <span class="text-sm font-medium">{{ $t('chat.filterByTags') }}</span>
                  <Button
                    v-if="contactsStore.selectedTags.length > 0"
                    variant="ghost"
                    size="sm"
                    class="h-6 px-2 text-xs"
                    @click="clearTagFilter"
                  >
                    {{ $t('common.clear') }}
                  </Button>
                </div>
                <Separator />
                <div v-if="tagsStore.tags.length === 0" class="py-2 text-center text-sm text-muted-foreground">
                  {{ $t('chat.noTagsAvailable') }}
                </div>
                <div v-else class="space-y-1 max-h-48 overflow-y-auto">
                  <button
                    v-for="tag in tagsStore.tags"
                    :key="tag.name"
                    class="w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-sm hover:bg-white/[0.08] light:hover:bg-gray-100 transition-colors"
                    :class="contactsStore.selectedTags.includes(tag.name) && 'bg-white/[0.08] light:bg-gray-100'"
                    @click="toggleTagFilter(tag.name)"
                  >
                    <span :class="['w-2 h-2 rounded-full shrink-0', getTagColorClass(tag.color).split(' ')[0]]" />
                    <span class="flex-1 text-left truncate">{{ tag.name }}</span>
                    <Check
                      v-if="contactsStore.selectedTags.includes(tag.name)"
                      class="h-4 w-4 text-emerald-400 shrink-0"
                    />
                  </button>
                </div>
              </div>
            </PopoverContent>
          </Popover>
        </div>
        <!-- Active tag filters -->
        <div v-if="contactsStore.selectedTags.length > 0" class="flex flex-wrap gap-1 px-3 pb-2.5 -mt-2">
          <TagBadge
            v-for="tagName in contactsStore.selectedTags"
            :key="tagName"
            :color="tagsStore.getTagByName(tagName)?.color"
            class="cursor-pointer hover:opacity-80"
            @click="toggleTagFilter(tagName)"
          >
            {{ tagName }}
            <X class="h-3 w-3 ml-1" />
          </TagBadge>
        </div>
      </div>

      <!-- Which list: four queue views over conversations, plus the contact
           list, which is the only way to reach somebody nobody has messaged
           yet. -->
      <div class="border-b border-white/[0.08] px-2 py-2 light:border-gray-200">
        <div class="flex items-center gap-0.5 overflow-x-auto pb-1">
          <button
            v-for="v in (['mine', 'unassigned', 'bot', 'all'] as const)"
            :key="v"
            type="button"
            :class="[
              'sidebar-link shrink-0 rounded-md px-1.5 py-1 text-[11px] font-medium transition-colors duration-150',
              listView === v
                ? 'bg-white/[0.08] text-white light:bg-gray-100 light:text-gray-900'
                : 'text-white/55 hover:bg-white/[0.04] hover:text-white light:text-gray-600 light:hover:bg-gray-100/70 light:hover:text-gray-900'
            ]"
            :aria-pressed="listView === v"
            @click="listView = v"
          >
            {{ $t(`inbox.view${v.charAt(0).toUpperCase()}${v.slice(1)}`) }}
            <span
              v-if="queueCounts && (queueCounts as any)[v] > 0"
              class="ml-1 tabular-nums text-white/45 light:text-gray-400"
            >{{ (queueCounts as any)[v] }}</span>
          </button>

          <!-- Not a slice of the queue but a different list: every contact,
               including the ones nobody has messaged yet. Set apart on the
               right because it changes what the list is, not which part of it
               you are looking at. -->
          <Tooltip>
            <TooltipTrigger as-child>
              <button
                type="button"
                :class="[
                  'sidebar-link ml-auto shrink-0 rounded-md p-1 transition-colors duration-150',
                  listView === 'contacts'
                    ? 'bg-white/[0.08] text-white light:bg-gray-100 light:text-gray-900'
                    : 'text-white/45 hover:bg-white/[0.04] hover:text-white light:text-gray-500 light:hover:bg-gray-100/70 light:hover:text-gray-900'
                ]"
                :aria-pressed="listView === 'contacts'"
                :aria-label="$t('inbox.viewContacts')"
                @click="listView = listView === 'contacts' ? 'all' : 'contacts'"
              >
                <Users class="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            </TooltipTrigger>
            <TooltipContent>{{ $t('inbox.viewContacts') }}</TooltipContent>
          </Tooltip>
        </div>

        <!-- Status and Pick next belong to the queue, so they appear with it. -->
        <div v-if="isQueueView" class="mt-1.5 flex items-center gap-1.5">
          <Select v-model="queueStatus">
            <SelectTrigger class="h-7 flex-1 text-[12px]">
              <SelectValue :placeholder="$t('inbox.statusActive')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="">{{ $t('inbox.statusActive') }}</SelectItem>
              <SelectItem value="open">{{ $t('inbox.statusOpen') }}</SelectItem>
              <SelectItem value="pending">{{ $t('inbox.statusPending') }}</SelectItem>
              <SelectItem value="snoozed">{{ $t('inbox.statusSnoozed') }}</SelectItem>
              <SelectItem value="resolved">{{ $t('inbox.statusResolved') }}</SelectItem>
            </SelectContent>
          </Select>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="outline"
                size="sm"
                class="h-7 shrink-0 px-2 text-[12px]"
                :disabled="!queueRows.length"
                @click="pickNext"
              >
                {{ $t('inbox.pickNext') }}
              </Button>
            </TooltipTrigger>
            <TooltipContent>{{ $t('inbox.pickNextHint') }}</TooltipContent>
          </Tooltip>
        </div>
      </div>

      <!-- Contacts -->
      <ScrollArea :ref="(el: any) => contactsScroll.scrollAreaRef.value = el" orientation="vertical" class="flex-1">
        <div class="py-1 w-full">
          <div
            v-for="row in listRows"
            :key="row.contactId"
            role="button"
            tabindex="0"
            :aria-current="contactsStore.currentContact?.id === row.contactId ? 'true' : undefined"
            :data-active="contactsStore.currentContact?.id === row.contactId"
            :class="[
              'sidebar-link nav-active-indicator group/row flex items-center gap-2.5 px-3 py-2 max-md:py-3 cursor-pointer transition-colors duration-150',
              contactsStore.currentContact?.id === row.contactId
                ? 'bg-white/[0.08] light:bg-gray-100'
                : 'hover:bg-white/[0.04] light:hover:bg-gray-100/70'
            ]"
            @click="openConversation(row.contactId)"
            @keydown.enter.prevent="openConversation(row.contactId)"
            @keydown.space.prevent="openConversation(row.contactId)"
          >
            <Avatar class="h-9 w-9 ring-2 ring-white/[0.1] light:ring-gray-200">
              <AvatarImage :src="row.avatarUrl" />
              <AvatarFallback :class="'text-xs text-white ' + getAvatarColor(row.name)">
                {{ getInitials(row.name) }}
              </AvatarFallback>
            </Avatar>
            <div class="flex-1 min-w-0">
              <div class="flex items-center justify-between gap-2">
                <p
                  class="flex-1 min-w-0 text-sm font-medium truncate text-white light:text-gray-900"
                  :title="row.name"
                >
                  {{ row.name }}
                </p>
                <span class="flex-shrink-0 text-[11px] tabular-nums text-white/50 light:text-gray-500">
                  {{ formatContactTime(row.lastMessageAt ?? undefined) }}
                </span>
              </div>
              <div class="flex items-center justify-between gap-2">
                <p class="flex-1 min-w-0 text-xs text-white/50 light:text-gray-500 truncate">
                  {{ row.preview || row.phone }}
                </p>
                <span
                  v-if="row.unread > 0"
                  class="flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-full px-1 text-[10px] font-semibold leading-none tabular-nums bg-emerald-500/20 text-emerald-400 light:bg-emerald-100 light:text-emerald-700"
                >
                  {{ row.unread > 99 ? '99+' : row.unread }}
                </span>
              </div>

              <!-- What the queue knows and a contact list cannot: who holds it,
                   how long the customer has waited, when a snooze ends. -->
              <div
                v-if="isQueueView"
                class="mt-0.5 flex items-center gap-2 text-[11px] text-white/45 light:text-gray-500"
              >
                <span v-if="row.handling === 'bot'" class="flex items-center gap-1">
                  <Bot class="h-3 w-3" aria-hidden="true" />{{ $t('inbox.botHandled') }}
                </span>
                <span v-else-if="row.handling === 'handoff_pending'" class="flex items-center gap-1 text-amber-400 light:text-amber-600">
                  <UserX class="h-3 w-3" aria-hidden="true" />{{ $t('inbox.handoffPending') }}
                </span>
                <span v-if="row.waitingSince" class="flex items-center gap-1">
                  <Clock class="h-3 w-3" aria-hidden="true" />{{ waitingFor(row.waitingSince) }}
                </span>
                <span v-if="row.snoozedUntil" class="flex items-center gap-1">
                  <Clock class="h-3 w-3" aria-hidden="true" />{{ formatContactTime(row.snoozedUntil ?? undefined) }}
                </span>
              </div>
            </div>

            <!-- Triage without opening the conversation: the whole point of a
                 queue is clearing the ones that need no reply. -->
            <div
              v-if="isQueueView"
              class="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity duration-150 focus-within:opacity-100 group-hover/row:opacity-100"
            >
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon"
                    class="h-6 w-6 text-white/50 hover:text-white light:text-gray-500 light:hover:text-gray-900"
                    :aria-label="$t('inbox.takeIt')"
                    @click.stop="takeConversation(row.contactId)"
                  >
                    <UserPlus class="h-3.5 w-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{{ $t('inbox.takeIt') }}</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon"
                    class="h-6 w-6 text-white/50 hover:text-white light:text-gray-500 light:hover:text-gray-900"
                    :aria-label="$t('inbox.snooze')"
                    @click.stop="snoozingContactId = row.contactId"
                  >
                    <Clock class="h-3.5 w-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{{ $t('inbox.snooze') }}</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon"
                    class="h-6 w-6 text-white/50 hover:text-emerald-400 light:text-gray-500 light:hover:text-emerald-600"
                    :aria-label="$t('inbox.resolve')"
                    @click.stop="resolveConversation(row.contactId)"
                  >
                    <Check class="h-3.5 w-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{{ $t('inbox.resolve') }}</TooltipContent>
              </Tooltip>
            </div>
          </div>

          <!-- Loading indicator for infinite scroll -->
          <div v-if="contactsStore.isLoadingMoreContacts" class="p-3 text-center">
            <Loader2 class="h-5 w-5 mx-auto animate-spin text-white/40 light:text-gray-400" />
          </div>

          <div v-if="listRows.length === 0 && !isQueueLoading" class="px-3 py-8 text-center text-white/50 light:text-gray-500">
            <Check v-if="isQueueView" class="h-6 w-6 mx-auto mb-1.5 text-emerald-400/70" />
            <User v-else class="h-6 w-6 mx-auto mb-1.5 opacity-50" />
            <p class="text-sm">{{ isQueueView ? $t('inbox.queueEmpty') : $t('chat.noContacts') }}</p>
          </div>
        </div>
      </ScrollArea>
    </div>

    <!-- Chat Area -->
    <div :class="['flex-1 min-w-0 flex-col bg-[#0f0f10] light:bg-gray-50', contactsStore.currentContact ? 'flex' : 'hidden md:flex']">
      <!-- No Contact Selected -->
      <div
        v-if="!contactsStore.currentContact"
        class="flex-1 flex items-center justify-center text-white/40 light:text-gray-500"
      >
        <div class="text-center">
          <div class="h-16 w-16 rounded-lg bg-gradient-to-br from-emerald-500 to-green-600 flex items-center justify-center mx-auto mb-4 shadow-lg shadow-emerald-500/20">
            <Send class="h-8 w-8 text-white" />
          </div>
          <h3 class="font-medium text-lg mb-1 text-white light:text-gray-900">{{ $t('chat.selectConversation') }}</h3>
          <p class="text-sm text-white/50 light:text-gray-500">{{ $t('chat.chooseContact') }}</p>
        </div>
      </div>

      <!-- Chat Interface -->
      <template v-else>
        <!-- Chat Header -->
        <div class="box-content h-16 flex-shrink-0 gap-2 px-4 max-md:px-2 border-b border-white/[0.08] light:border-gray-200 flex items-center justify-between bg-[#0f0f10] light:bg-white">
          <div class="flex min-w-0 items-center gap-2">
            <Button
              variant="ghost"
              size="icon"
              class="md:hidden h-8 w-8 shrink-0 text-white/60 hover:text-white hover:bg-white/[0.08] light:text-gray-600 light:hover:text-gray-900 light:hover:bg-gray-100"
              :aria-label="$t('chat.backToConversations')"
              @click="router.push('/inbox')"
            >
              <ArrowLeft class="h-4 w-4" />
            </Button>
            <Avatar class="max-sm:hidden h-8 w-8 shrink-0 ring-2 ring-white/[0.1] light:ring-gray-200">
              <AvatarImage :src="contactsStore.currentContact.avatar_url" />
              <AvatarFallback :class="'text-xs text-white ' + getAvatarColor(contactsStore.currentContact.name || contactsStore.currentContact.phone_number)">
                {{ getInitials(contactsStore.currentContact.name || contactsStore.currentContact.phone_number) }}
              </AvatarFallback>
            </Avatar>
            <div class="min-w-0">
              <div class="flex min-w-0 items-center gap-1.5">
                <p class="truncate text-sm font-medium text-white light:text-gray-900">
                  {{ contactsStore.currentContact.name || contactsStore.currentContact.phone_number }}
                </p>
                <Badge v-if="activeTransferId" class="text-[10px] h-5 bg-orange-500/20 text-orange-400 light:bg-orange-100 light:text-orange-700">
                  Paused
                </Badge>
                <Badge v-if="contactsStore.currentContact?.marketing_opt_out" class="text-[10px] h-5 bg-red-500/20 text-red-400 light:bg-red-100 light:text-red-700" :title="$t('chat.marketingOptOut')">
                  {{ $t('chat.marketingOptOut', 'Marketing Opt-out') }}
                </Badge>
              </div>
              <p class="text-[11px] text-white/50 light:text-gray-500">
                {{ contactsStore.currentContact.phone_number }}
              </p>
            </div>
          </div>
          <div class="flex items-center gap-1">
            <CallButton
              v-if="contactsStore.currentContact?.phone_number && selectedAccount"
              :contact-id="contactsStore.currentContact.id"
              :contact-phone="contactsStore.currentContact.phone_number"
              :contact-name="contactsStore.currentContact.name || contactsStore.currentContact.phone_number"
              :whatsapp-account="selectedAccount"
            />
            <Tooltip v-if="canAssignContacts">
              <TooltipTrigger as-child>
                <Button variant="ghost" size="icon" class="h-8 w-8 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100" @click="isAssignDialogOpen = true">
                  <UserPlus class="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.assignToAgent') }}</TooltipContent>
            </Tooltip>
            <Tooltip v-if="activeTransferId">
              <TooltipTrigger as-child>
                <Button variant="ghost" size="icon" class="h-8 w-8 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100" :disabled="isResuming" @click="resumeChatbot">
                  <Play class="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.resumeChatbot') }}</TooltipContent>
            </Tooltip>
            <!-- Custom Action Buttons -->
            <Tooltip v-for="action in customActions" :key="action.id">
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-8 w-8 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100"
                  :disabled="executingActionId === action.id"
                  @click="executeCustomAction(action)"
                >
                  <Loader2 v-if="executingActionId === action.id" class="h-4 w-4 animate-spin" />
                  <component v-else :is="getActionIcon(action.icon)" class="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{{ action.name }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon"
                  id="notes-button"
                  class="h-8 w-8 relative text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100"
                  :class="isNotesPanelOpen && 'bg-amber-500/10 text-amber-400 light:bg-amber-50 light:text-amber-600'"
                  @click="isNotesPanelOpen = !isNotesPanelOpen"
                >
                  <StickyNote class="h-4 w-4" />
                  <span
                    v-if="notesStore.notes.length > 0 && !isNotesPanelOpen"
                    id="notes-badge"
                    class="absolute -top-0.5 -right-0.5 h-4 min-w-[16px] rounded-full bg-amber-500 text-[10px] text-white flex items-center justify-center px-1"
                  >
                    {{ notesStore.notes.length }}
                  </span>
                </Button>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.internalNotes') }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon"
                  id="activity-toggle"
                  class="h-8 w-8 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100"
                  :class="showActivity && 'bg-white/[0.08] text-white light:bg-gray-100 light:text-gray-900'"
                  @click="toggleActivity"
                >
                  <Activity class="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.showActivity') }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon"
                  id="info-button"
                  class="h-8 w-8 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100"
                  :class="isInfoPanelOpen && 'bg-white/[0.08] text-white light:bg-gray-100 light:text-gray-900'"
                  @click="isInfoPanelOpen = !isInfoPanelOpen"
                >
                  <Info class="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.contactInfo') }}</TooltipContent>
            </Tooltip>
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="icon" class="h-8 w-8 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100">
                  <MoreVertical class="h-4 w-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuLabel>{{ $t('chat.contactOptions') }}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem v-if="canAssignContacts" @click="isAssignDialogOpen = true">
                  <UserPlus class="mr-2 h-4 w-4" />
                  <span>{{ $t('chat.assignToAgent') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem v-if="!activeTransferId" @click="transferToAgent" :disabled="isTransferring">
                  <UserX class="mr-2 h-4 w-4" />
                  <span>{{ $t('chat.transferToAgent') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem v-if="activeTransferId" @click="resumeChatbot" :disabled="isResuming">
                  <Play class="mr-2 h-4 w-4" />
                  <span>{{ $t('chat.resumeChatbot') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem @click="isInfoPanelOpen = !isInfoPanelOpen">
                  <Info class="mr-2 h-4 w-4" />
                  <span>{{ isInfoPanelOpen ? $t('chat.hideContactDetails') : $t('chat.viewContactDetails') }}</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>

        <!-- Account Tabs (shown when contact has messages from multiple WhatsApp accounts) -->
        <div
          v-if="orgAccounts.length > 1 && selectedAccount"
          class="flex-shrink-0 px-4 py-2 border-b border-white/[0.08] light:border-gray-200 bg-[#0a0a0b] light:bg-gray-50"
        >
          <div class="inline-flex items-center gap-1 rounded-lg bg-white/[0.06] light:bg-gray-100 p-1">
            <button
              v-for="acct in orgAccounts"
              :key="acct.name"
              :class="[
                'rounded-md px-3 py-1 text-xs font-medium whitespace-nowrap transition-all',
                acct.name === selectedAccount
                  ? 'bg-emerald-600 text-white shadow-sm'
                  : 'bg-white/[0.08] text-white/70 hover:text-white/90 hover:bg-white/[0.12] light:bg-gray-200 light:text-gray-600 light:hover:text-gray-800 light:hover:bg-gray-300'
              ]"
              @click="switchAccount(acct.name)"
            >
              {{ acct.name }}
            </button>
          </div>
        </div>

        <!-- "This may be the same person" (plan 06). Under the header, where
             an agent reading a thread that looks oddly short will see it —
             a data-quality page nobody opens is the wrong place for it. -->
        <DuplicateBanner
          :contact-id="contactsStore.currentContact?.id"
          @merged="contactsStore.fetchContacts()"
        />

        <!-- Messages -->
        <div class="relative flex-1 min-h-0 overflow-hidden">
          <!-- Loading overlay while switching contacts / loading the first page -->
          <Spinner v-if="contactsStore.isLoadingMessages" overlay />

          <!-- Sticky date header -->
          <Transition name="sticky-date">
            <div
              v-if="showStickyDate"
              class="absolute top-2 left-1/2 -translate-x-1/2 z-10 px-3 py-1 bg-white/[0.08] light:bg-gray-200 backdrop-blur-sm rounded-full text-[11px] text-white/50 light:text-gray-600 font-medium shadow-sm"
            >
              {{ stickyDate }}
            </div>
          </Transition>

          <ScrollArea :ref="(el: any) => messagesScroll.scrollAreaRef.value = el" class="h-full p-3 chat-background">
            <div class="space-y-2">
              <!-- Loading indicator for older messages -->
              <div v-if="contactsStore.isLoadingOlderMessages" class="flex justify-center py-2">
                <div class="flex items-center gap-2 text-white/40 light:text-gray-500 text-sm">
                  <div class="h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" />
                  <span>{{ $t('chat.loadingOlderMessages') }}...</span>
                </div>
              </div>
              <template
                v-for="(message, index) in contactsStore.messages"
                :key="message.id"
              >
                <!-- Date separator -->
                <div
                  v-if="shouldShowDateSeparator(index)"
                  class="flex items-center justify-center my-4"
                  :data-date-separator="getDateLabel(message.created_at)"
                >
                  <div class="px-3 py-1 bg-white/[0.06] light:bg-gray-200 rounded-full text-[11px] text-white/40 light:text-gray-600 font-medium">
                    {{ getDateLabel(message.created_at) }}
                  </div>
                </div>

                <!-- What happened around the conversation since the previous
                     message (plan 10, 4.1). The wording is the timeline's, so
                     the thread and the profile cannot disagree. -->
                <div
                  v-for="pill in activityPills.get(message.id) || []"
                  :key="`pill-${pill.id}`"
                  class="flex items-center justify-center my-2"
                  data-activity-pill
                >
                  <div class="max-w-[80%] px-3 py-1 rounded-full bg-white/[0.04] light:bg-gray-100 text-[11px] text-white/45 light:text-gray-600 flex items-center gap-1.5">
                    <Activity class="h-3 w-3 shrink-0" />
                    <span class="truncate">{{ pill.summary }}</span>
                  </div>
                </div>

                <!-- Unread divider (WhatsApp-style; appears above the first
                     message that arrived while the tab was hidden) -->
                <div
                  v-if="newMessagesCount > 0 && message.id === firstUnreadId"
                  class="flex items-center justify-center my-4"
                >
                  <div class="px-3 py-1 bg-white/[0.06] light:bg-gray-200 rounded-full text-[11px] text-white/40 light:text-gray-600 font-medium">
                    {{ newMessagesCount }} {{ newMessagesCount === 1 ? $t('chat.unreadMessage', 'unread message') : $t('chat.unreadMessages', 'unread messages') }}
                  </div>
                </div>

              <!-- Message bubble -->
              <div
                :id="`message-${message.id}`"
                :class="[
                  'flex group',
                  message.direction === 'outgoing' ? 'justify-end' : 'justify-start'
                ]"
              >
              <div
                :class="[
                  'chat-bubble',
                  message.direction === 'outgoing' ? 'chat-bubble-outgoing' : 'chat-bubble-incoming'
                ]"
              >
                <!-- Reply preview (if this message is replying to another) -->
                <div
                  v-if="message.is_reply && message.reply_to_message"
                  class="reply-preview cursor-pointer text-xs"
                  @click="scrollToMessage(message.reply_to_message_id)"
                >
                  <p class="font-medium">
                    {{ message.reply_to_message.direction === 'incoming' ? (contactsStore.currentContact?.profile_name || contactsStore.currentContact?.name || 'Customer') : 'You' }}
                  </p>
                  <p class="truncate">
                    {{ getReplyPreviewContent(message) }}
                  </p>
                </div>
                <!-- Template header media (image/video/document shown above template text) -->
                <div v-if="message.message_type === 'template' && message.media_url" class="mb-2">
                  <img
                    v-if="message.media_mime_type?.startsWith('image/')"
                    :src="getMediaUrl(message)"
                    alt="Template header"
                    class="max-w-[280px] max-h-[300px] rounded-lg cursor-pointer object-cover"
                    @click="openMediaPreview(message)"
                    @error="handleImageError($event)"
                  />
                  <video
                    v-else-if="message.media_mime_type?.startsWith('video/')"
                    :src="getMediaUrl(message)"
                    controls
                    class="max-w-[280px] max-h-[300px] rounded-lg"
                  />
                  <button
                    v-else-if="isPreviewableDocument(message)"
                    type="button"
                    class="flex items-center gap-2 px-3 py-2 bg-background/50 rounded-lg hover:bg-background/80 transition-colors cursor-pointer text-left w-full"
                    @click="openMediaPreview(message)"
                  >
                    <FileText class="h-5 w-5 text-muted-foreground" />
                    <span class="text-sm truncate max-w-[200px]">{{ message.media_filename || 'Document' }}</span>
                  </button>
                  <a
                    v-else
                    :href="getMediaUrl(message)"
                    :download="message.media_filename || 'document'"
                    class="flex items-center gap-2 px-3 py-2 bg-background/50 rounded-lg hover:bg-background/80 transition-colors"
                  >
                    <FileText class="h-5 w-5 text-muted-foreground" />
                    <span class="text-sm truncate max-w-[200px]">{{ message.media_filename || 'Document' }}</span>
                  </a>
                </div>
                <!-- Image message -->
                <div v-else-if="message.message_type === 'image' && message.media_url" class="mb-2">
                  <img
                    :src="getMediaUrl(message)"
                    :alt="message.content?.body || 'Image'"
                    class="max-w-[280px] max-h-[300px] rounded-lg cursor-pointer object-cover"
                    @click="openMediaPreview(message)"
                    @error="handleImageError($event)"
                  />
                </div>
                <!-- Sticker message -->
                <div v-else-if="message.message_type === 'sticker' && message.media_url" class="mb-2">
                  <img
                    :src="getMediaUrl(message)"
                    alt="Sticker"
                    class="max-w-[128px] max-h-[128px] cursor-pointer"
                    @click="openMediaPreview(message)"
                    @error="handleImageError($event)"
                  />
                </div>
                <!-- Video message -->
                <div v-else-if="message.message_type === 'video' && message.media_url" class="mb-2">
                  <video
                    :src="getMediaUrl(message)"
                    controls
                    class="max-w-[280px] max-h-[300px] rounded-lg"
                    @error="handleMediaError($event, 'video')"
                  />
                </div>
                <!-- Audio message -->
                <div v-else-if="message.message_type === 'audio' && message.media_url" class="mb-2">
                  <audio
                    :src="getMediaUrl(message)"
                    controls
                    class="max-w-[280px]"
                    @error="handleMediaError($event, 'audio')"
                  />
                </div>
                <!-- Document message -->
                <div v-else-if="message.message_type === 'document' && message.media_url" class="mb-2">
                  <button
                    v-if="isPreviewableDocument(message)"
                    type="button"
                    class="flex items-center gap-2 px-3 py-2 bg-background/50 rounded-lg hover:bg-background/80 transition-colors cursor-pointer text-left w-full"
                    @click="openMediaPreview(message)"
                  >
                    <FileText class="h-5 w-5 text-muted-foreground" />
                    <span class="text-sm truncate max-w-[200px]">
                      {{ message.media_filename || 'Document' }}
                    </span>
                  </button>
                  <a
                    v-else
                    :href="getMediaUrl(message)"
                    :download="message.media_filename || 'document'"
                    class="flex items-center gap-2 px-3 py-2 bg-background/50 rounded-lg hover:bg-background/80 transition-colors"
                  >
                    <FileText class="h-5 w-5 text-muted-foreground" />
                    <span class="text-sm truncate max-w-[200px]">
                      {{ message.media_filename || 'Document' }}
                    </span>
                  </a>
                </div>
                <!-- Location message -->
                <div v-else-if="message.message_type === 'location' && getLocationData(message)" class="mb-2">
                  <a
                    :href="getGoogleMapsUrl(getLocationData(message)!)"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="flex items-center gap-3 px-3 py-3 bg-background/50 rounded-lg hover:bg-background/80 transition-colors"
                  >
                    <div class="h-10 w-10 rounded-full bg-red-900/30 light:bg-red-100 flex items-center justify-center shrink-0">
                      <MapPin class="h-5 w-5 text-red-500" />
                    </div>
                    <div class="flex-1 min-w-0">
                      <p v-if="getLocationData(message)?.name" class="text-sm font-medium truncate">
                        {{ getLocationData(message)?.name }}
                      </p>
                      <p v-else class="text-sm font-medium">Location</p>
                      <p v-if="getLocationData(message)?.address" class="text-xs text-muted-foreground truncate">
                        {{ getLocationData(message)?.address }}
                      </p>
                      <p class="text-xs text-muted-foreground">
                        {{ getLocationData(message)?.latitude.toFixed(6) }}, {{ getLocationData(message)?.longitude.toFixed(6) }}
                      </p>
                    </div>
                    <ExternalLink class="h-4 w-4 text-muted-foreground shrink-0" />
                  </a>
                </div>
                <!-- Contacts message -->
                <div v-else-if="message.message_type === 'contacts' && getContactsData(message).length > 0" class="mb-2 space-y-2">
                  <div
                    v-for="(contact, idx) in getContactsData(message)"
                    :key="idx"
                    class="flex items-center gap-3 px-3 py-2 bg-background/50 rounded-lg"
                  >
                    <div class="h-10 w-10 rounded-full bg-primary/10 flex items-center justify-center shrink-0">
                      <User class="h-5 w-5 text-primary" />
                    </div>
                    <div class="flex-1 min-w-0">
                      <p class="text-sm font-medium truncate">{{ contact.name }}</p>
                      <div v-if="contact.phones?.length" class="flex items-center gap-1 text-xs text-muted-foreground">
                        <Phone class="h-3 w-3" />
                        <span class="truncate">{{ contact.phones.join(', ') }}</span>
                      </div>
                    </div>
                  </div>
                </div>
                <!-- Unsupported message -->
                <div v-else-if="message.message_type === 'unsupported'" class="mb-2">
                  <div class="flex items-center gap-2 px-3 py-2 bg-muted/50 rounded-lg text-muted-foreground">
                    <AlertCircle class="h-4 w-4 shrink-0" />
                    <span class="text-sm italic">{{ $t('chat.unsupportedMessage') }}</span>
                  </div>
                </div>
                <!-- Button reply - WhatsApp style -->
                <div v-if="message.message_type === 'button_reply'" class="button-reply-bubble">
                  <span class="whitespace-pre-wrap break-words">{{ getMessageContent(message) }}</span>
                  <span class="chat-bubble-time"><span>{{ formatMessageTime(message.created_at) }}</span></span>
                </div>
                <!-- Text content (for text messages or captions) -->
                <span v-else-if="getMessageContent(message)" class="whitespace-pre-wrap break-words">{{ getMessageContent(message) }}<span class="chat-bubble-time"><span>{{ formatMessageTime(message.created_at) }}</span><component v-if="message.direction === 'outgoing'" :is="getMessageStatusIcon(message.status)" :class="['h-4 w-4 status-icon', getMessageStatusClass(message.status)]" /></span></span>
                <!-- Fallback for media without URL -->
                <span v-else-if="isMediaMessage(message) && !message.media_url" class="text-muted-foreground italic">[{{ message.message_type.charAt(0).toUpperCase() + message.message_type.slice(1) }}]<span class="chat-bubble-time"><span>{{ formatMessageTime(message.created_at) }}</span><component v-if="message.direction === 'outgoing'" :is="getMessageStatusIcon(message.status)" :class="['h-4 w-4 status-icon', getMessageStatusClass(message.status)]" /></span></span>
                <!-- Interactive buttons - WhatsApp style -->
                <div
                  v-if="getInteractiveButtons(message).length > 0"
                  class="interactive-buttons mt-2 -mx-2 -mb-1.5 border-t"
                >
                  <template v-for="(btn, index) in getInteractiveButtons(message)" :key="btn.id">
                    <a
                      v-if="btn.type === 'URL' && btn.url"
                      :href="btn.url"
                      target="_blank"
                      rel="noopener noreferrer"
                      :class="['py-2 text-sm text-center font-medium cursor-pointer flex items-center justify-center gap-1.5', index > 0 && 'border-t']"
                    >
                      <ExternalLink class="h-3.5 w-3.5" />
                      {{ btn.title }}
                    </a>
                    <div
                      v-else
                      :class="['py-2 text-sm text-center font-medium cursor-pointer', index > 0 && 'border-t']"
                    >
                      {{ btn.title }}
                    </div>
                  </template>
                </div>
                <!-- CTA URL button - WhatsApp style -->
                <a
                  v-if="getCTAUrlData(message)"
                  :href="getCTAUrlData(message)?.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="interactive-buttons mt-2 -mx-2 -mb-1.5 border-t block"
                >
                  <div class="py-2 text-sm text-center font-medium cursor-pointer flex items-center justify-center gap-1.5">
                    <ExternalLink class="h-3.5 w-3.5" />
                    {{ getCTAUrlData(message)?.button_text }}
                  </div>
                </a>
                <!-- Voice call button - WhatsApp style, non-clickable in our chat -->
                <div
                  v-if="getVoiceCallData(message)"
                  class="interactive-buttons mt-2 -mx-2 -mb-1.5 border-t"
                >
                  <div class="py-2 text-sm text-center font-medium flex items-center justify-center gap-1.5">
                    <PhoneCall class="h-3.5 w-3.5" />
                    {{ getVoiceCallData(message)?.display_text }}
                  </div>
                </div>
                <!-- Flow button - WhatsApp style -->
                <div
                  v-if="getFlowButtonText(message)"
                  class="interactive-buttons mt-2 -mx-2 -mb-1.5 border-t"
                >
                  <div class="py-2 text-sm text-center font-medium">
                    {{ getFlowButtonText(message) }}
                  </div>
                </div>
                <!-- Time for messages without text content -->
                <span v-if="!getMessageContent(message) && !(isMediaMessage(message) && !message.media_url)" class="chat-bubble-time block clear-both">
                  <span>{{ formatMessageTime(message.created_at) }}</span>
                  <component
                    v-if="message.direction === 'outgoing'"
                    :is="getMessageStatusIcon(message.status)"
                    :class="['h-4 w-4 status-icon', getMessageStatusClass(message.status)]"
                  />
                </span>
                <!-- Reactions display -->
                <div
                  v-if="message.reactions && message.reactions.length > 0"
                  class="reactions-display flex flex-wrap gap-1 mt-1"
                >
                  <span
                    v-for="(reaction, idx) in message.reactions"
                    :key="idx"
                    class="reaction-badge"
                    :title="reaction.from_phone || reaction.from_user || ''"
                  >
                    {{ reaction.emoji }}
                  </span>
                </div>
                <!-- Failed message error (not for template messages) -->
                <span
                  v-if="message.status === 'failed' && message.direction === 'outgoing' && message.message_type !== 'template'"
                  class="flex items-center gap-1 mt-1 text-xs text-destructive"
                >
                  <AlertCircle class="h-3 w-3" />
                  <span>{{ message.error_message || 'Failed to send' }}</span>
                </span>
                <!-- Failed template message indicator (no retry) -->
                <span
                  v-if="message.status === 'failed' && message.direction === 'outgoing' && message.message_type === 'template'"
                  class="flex items-center gap-1 mt-1 text-xs text-destructive"
                >
                  <AlertCircle class="h-3 w-3" />
                  <span>{{ message.error_message || 'Failed to send' }}</span>
                </span>
              </div>
              <!-- Action buttons for incoming messages -->
              <div v-if="message.direction === 'incoming'" class="flex flex-col gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity self-center ml-1">
                <Popover :open="reactionPickerMessageId === message.id" @update:open="(open: boolean) => reactionPickerMessageId = open ? message.id : null">
                  <PopoverTrigger as-child>
                    <Button variant="ghost" size="icon" class="h-6 w-6">
                      <SmilePlus class="h-3 w-3" />
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent side="top" class="w-auto p-2">
                    <div class="flex gap-1">
                      <button
                        v-for="emoji in quickReactionEmojis"
                        :key="emoji"
                        class="text-lg hover:bg-muted p-1 rounded-md cursor-pointer"
                        @click="sendReaction(message.id, emoji)"
                      >
                        {{ emoji }}
                      </button>
                    </div>
                  </PopoverContent>
                </Popover>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-6 w-6"
                  @click="replyToMessage(message)"
                >
                  <Reply class="h-3 w-3" />
                </Button>
                <!-- A Popover, matching the reaction picker beside it: the
                     same component in the same place behaves the same way. -->
                <Popover
                  :open="messageActionsFor === message.id"
                  @update:open="(open: boolean) => messageActionsFor = open ? message.id : null"
                >
                  <PopoverTrigger as-child>
                    <Button variant="ghost" size="icon" class="h-6 w-6" data-message-actions>
                      <MoreVertical class="h-3 w-3" />
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent side="top" align="end" class="w-56 p-1">
                    <p class="px-2 py-1.5 text-xs text-muted-foreground">{{ $t('chat.messageActions') }}</p>
                    <button
                      class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                      @click="createTaskFromMessage(message)"
                    >
                      {{ $t('chat.messageActionTask') }}
                    </button>
                    <button
                      class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                      @click="noteAboutMessage(message)"
                    >
                      {{ $t('chat.messageActionNote') }}
                    </button>
                    <button
                      v-if="canSeeDealsFromChat"
                      class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                      @click="createDealFromMessage(message)"
                    >
                      {{ $t('chat.messageActionDeal') }}
                    </button>
                    <template v-if="messageActionFields.length">
                      <div class="my-1 h-px bg-border" />
                      <p class="px-2 py-1.5 text-xs text-muted-foreground">{{ $t('chat.messageActionCopyTo') }}</p>
                      <button
                        v-for="field in messageActionFields"
                        :key="field.id"
                        class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                        @click="copyMessageToField(message, field)"
                      >
                        {{ field.label }}
                      </button>
                    </template>
                  </PopoverContent>
                </Popover>
              </div>
              <!-- Reply button for outgoing messages (shown on hover) -->
              <div v-if="message.direction === 'outgoing'" class="flex flex-col gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity self-center ml-1">
                <Popover :open="reactionPickerMessageId === message.id" @update:open="(open: boolean) => reactionPickerMessageId = open ? message.id : null">
                  <PopoverTrigger as-child>
                    <Button variant="ghost" size="icon" class="h-6 w-6">
                      <SmilePlus class="h-3 w-3" />
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent side="top" class="w-auto p-2">
                    <div class="flex gap-1">
                      <button
                        v-for="emoji in quickReactionEmojis"
                        :key="emoji"
                        class="text-lg hover:bg-muted p-1 rounded-md cursor-pointer"
                        @click="sendReaction(message.id, emoji)"
                      >
                        {{ emoji }}
                      </button>
                    </div>
                  </PopoverContent>
                </Popover>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-6 w-6"
                  @click="replyToMessage(message)"
                >
                  <Reply class="h-3 w-3" />
                </Button>
                <!-- A Popover, matching the reaction picker beside it: the
                     same component in the same place behaves the same way. -->
                <Popover
                  :open="messageActionsFor === message.id"
                  @update:open="(open: boolean) => messageActionsFor = open ? message.id : null"
                >
                  <PopoverTrigger as-child>
                    <Button variant="ghost" size="icon" class="h-6 w-6" data-message-actions>
                      <MoreVertical class="h-3 w-3" />
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent side="top" align="end" class="w-56 p-1">
                    <p class="px-2 py-1.5 text-xs text-muted-foreground">{{ $t('chat.messageActions') }}</p>
                    <button
                      class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                      @click="createTaskFromMessage(message)"
                    >
                      {{ $t('chat.messageActionTask') }}
                    </button>
                    <button
                      class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                      @click="noteAboutMessage(message)"
                    >
                      {{ $t('chat.messageActionNote') }}
                    </button>
                    <button
                      v-if="canSeeDealsFromChat"
                      class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                      @click="createDealFromMessage(message)"
                    >
                      {{ $t('chat.messageActionDeal') }}
                    </button>
                    <template v-if="messageActionFields.length">
                      <div class="my-1 h-px bg-border" />
                      <p class="px-2 py-1.5 text-xs text-muted-foreground">{{ $t('chat.messageActionCopyTo') }}</p>
                      <button
                        v-for="field in messageActionFields"
                        :key="field.id"
                        class="w-full text-left px-2 py-1.5 text-sm rounded-md hover:bg-muted"
                        @click="copyMessageToField(message, field)"
                      >
                        {{ field.label }}
                      </button>
                    </template>
                  </PopoverContent>
                </Popover>
                <Button
                  v-if="message.status === 'failed' && message.message_type !== 'template'"
                  variant="ghost"
                  size="icon"
                  class="h-6 w-6 text-destructive hover:text-destructive"
                  :disabled="retryingMessageId === message.id"
                  @click="retryMessage(message)"
                  title="Retry sending"
                >
                  <Loader2 v-if="retryingMessageId === message.id" class="h-3 w-3 animate-spin" />
                  <RotateCw v-else class="h-3 w-3" />
                </Button>
              </div>
            </div>
            </template>

            <!-- Anything that happened after the last message. -->
            <div
              v-for="pill in trailingPills"
              :key="`pill-tail-${pill.id}`"
              class="flex items-center justify-center my-2"
              data-activity-pill
            >
              <div class="max-w-[80%] px-3 py-1 rounded-full bg-white/[0.04] light:bg-gray-100 text-[11px] text-white/45 light:text-gray-600 flex items-center gap-1.5">
                <Activity class="h-3 w-3 shrink-0" />
                <span class="truncate">{{ pill.summary }}</span>
              </div>
            </div>

            <div ref="messagesEndRef" />
          </div>
        </ScrollArea>
        </div>

        <!-- Service window closing soon -->
        <div
          v-if="isServiceWindowClosing"
          class="px-4 py-2 border-t border-amber-500/20 bg-amber-500/10 flex items-center gap-2"
        >
          <Clock class="h-4 w-4 text-amber-500 shrink-0" />
          <span class="text-sm text-amber-600 dark:text-amber-400 flex-1">{{ serviceWindowCountdown }}</span>
        </div>

        <!-- Service window expired banner -->
        <div
          v-if="isServiceWindowExpired"
          class="px-4 py-2.5 border-t border-red-500/20 bg-red-500/10 flex items-center gap-2"
        >
          <Clock class="h-4 w-4 text-red-500 shrink-0" />
          <span class="text-sm text-red-500 flex-1">{{ $t('chat.serviceWindowExpired') }}</span>
          <Button variant="outline" size="sm" class="border-red-500/30 text-red-500 hover:bg-red-500/10 shrink-0" @click="openTemplatePicker">
            {{ $t('chat.sendTemplateAction') }}
          </Button>
        </div>

        <!-- Commands the typed slash matches (plan 10, 4.1). A list rather
             than a menu: the agent is already typing, and the only thing they
             need is to know the command exists and what it will do. -->
        <div
          v-if="isCommanding"
          class="px-4 py-2 border-t border-white/[0.08] light:border-gray-200 bg-white/[0.04] light:bg-gray-50 flex flex-wrap items-center gap-x-4 gap-y-1"
          data-command-hints
        >
          <span
            v-for="command in commandMatches"
            :key="command.name"
            class="text-[11px] text-white/50 light:text-gray-600"
          >
            <span class="font-mono text-white/80 light:text-gray-900">/{{ command.name }}</span>
            <span v-if="command.takesText" class="font-mono text-white/30 light:text-gray-400"> …</span>
            — {{ $t(command.hint) }}
          </span>
          <span class="text-[11px] text-white/30 light:text-gray-400 ml-auto">{{ $t('chat.commandEnter') }}</span>
        </div>

        <!-- Reply indicator -->
        <div
          v-if="contactsStore.replyingTo"
          class="px-4 py-2 border-t border-white/[0.08] light:border-gray-200 bg-white/[0.04] light:bg-gray-50 flex items-center justify-between"
        >
          <div class="flex-1 min-w-0">
            <p class="text-xs font-medium text-white/50 light:text-gray-500">
              Replying to {{ contactsStore.replyingTo.direction === 'incoming' ? (contactsStore.currentContact?.profile_name || contactsStore.currentContact?.name || 'Customer') : 'Yourself' }}
            </p>
            <p class="text-sm truncate text-white/70 light:text-gray-700">
              {{ getMessageContent(contactsStore.replyingTo) || '[Media]' }}
            </p>
          </div>
          <button class="w-6 h-6 rounded-md hover:bg-white/[0.08] light:hover:bg-gray-200 flex items-center justify-center shrink-0 transition-colors" @click="contactsStore.clearReplyingTo">
            <X class="h-4 w-4 text-white/50 light:text-gray-500" />
          </button>
        </div>

        <!-- Message Input -->
        <div class="p-4 border-t border-white/[0.08] light:border-gray-200 bg-[#0f0f10] light:bg-white">
          <form @submit.prevent="sendMessage" class="flex items-center gap-2 p-2 rounded-lg bg-white/[0.06] light:bg-gray-100 border border-white/[0.08] light:border-gray-200">
            <Tooltip>
              <TooltipTrigger as-child>
                <span>
                  <Popover v-model:open="emojiPickerOpen">
                    <PopoverTrigger as-child>
                      <button type="button" class="w-9 h-9 rounded-lg hover:bg-white/[0.08] light:hover:bg-gray-200 flex items-center justify-center transition-colors">
                        <Smile class="w-[18px] h-[18px] text-white/40 light:text-gray-500" />
                      </button>
                    </PopoverTrigger>
                    <PopoverContent side="top" align="start" class="w-auto p-0">
                      <EmojiPicker
                        :native="true"
                        :disable-skin-tones="true"
                        :theme="isDark ? 'dark' : 'light'"
                        @select="insertEmoji($event.i)"
                      />
                    </PopoverContent>
                  </Popover>
                </span>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.emoji') }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <span>
                  <CannedResponsePicker
                    :external-open="cannedPickerOpen"
                    :external-search="cannedSearchQuery"
                    @select="handleCannedSelect"
                    @close="closeCannedPicker"
                  />
                </span>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.cannedResponses') }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <span ref="templatePickerRef">
                  <TemplatePicker
                    :selected-account="selectedAccount"
                    @select-with-params="handleTemplateWithParams"
                  />
                </span>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.sendTemplate') }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <button type="button" class="w-9 h-9 rounded-lg hover:bg-white/[0.08] light:hover:bg-gray-200 flex items-center justify-center transition-colors" @click="openFilePicker">
                  <Paperclip class="w-[18px] h-[18px] text-white/40 light:text-gray-500" />
                </button>
              </TooltipTrigger>
              <TooltipContent>{{ $t('chat.attachFile') }}</TooltipContent>
            </Tooltip>
            <input
              ref="fileInputRef"
              type="file"
              accept="image/*,video/*,audio/*,.pdf,.doc,.docx"
              class="hidden"
              @change="handleFileSelect"
            />
            <textarea
              ref="messageInputRef"
              v-model="messageInput"
              :placeholder="$t('chat.typeMessage') + '...'"
              rows="1"
              class="flex-1 bg-transparent text-[14px] text-white light:text-gray-900 placeholder:text-white/30 light:placeholder:text-gray-400 focus:outline-none resize-none min-h-[36px] max-h-[120px] py-2 overflow-y-auto"
              @keydown.enter.exact.prevent="sendMessage"
              @input="autoResizeTextarea"
            />
            <button type="submit" class="w-9 h-9 rounded-lg bg-emerald-600 hover:bg-emerald-500 light:bg-emerald-500 light:hover:bg-emerald-600 flex items-center justify-center transition-colors disabled:opacity-50" :disabled="!messageInput.trim() || isSending">
              <Send class="w-4 h-4 text-white" />
            </button>
          </form>
        </div>
      </template>
    </div>

    <!-- Notes Side Panel -->
    <ConversationNotes
      v-if="contactsStore.currentContact && isNotesPanelOpen"
      :contact-id="contactsStore.currentContact.id"
      @close="isNotesPanelOpen = false"
    />

    <!-- Contact Info Panel -->
    <ContactInfoPanel
      v-if="contactsStore.currentContact && isInfoPanelOpen"
      :contact="contactsStore.currentContact"
      :session-data="contactSessionData"
      @close="isInfoPanelOpen = false"
      @tags-updated="(tags) => contactsStore.updateContactTags(contactsStore.currentContact!.id, tags)"
    />

    <!-- Template Params Dialog -->
    <Dialog v-model:open="templateDialogOpen">
      <DialogContent class="max-w-sm">
        <DialogHeader>
          <DialogTitle>{{ templateParamNames.length > 0 ? $t('chat.fillParameters') : $t('chat.preview') }}</DialogTitle>
          <DialogDescription>
            {{ selectedTemplate?.display_name || selectedTemplate?.name }}
          </DialogDescription>
        </DialogHeader>
        <div class="py-4 space-y-3">
          <!-- Header media upload -->
          <HeaderMediaUpload
            v-if="templateNeedsHeaderMedia"
            :file="templateHeaderFile"
            :preview-url="templateHeaderPreview"
            :accept-types="templateHeaderAccept"
            :label="selectedTemplate?.header_type === 'IMAGE' ? $t('chat.headerImage') : selectedTemplate?.header_type === 'VIDEO' ? $t('chat.headerVideo') : $t('chat.headerDocument')"
            @change="handleTemplateHeaderFile"
            @clear="clearTemplateHeaderMedia"
          />

          <div v-if="showHeaderParamInput" class="space-y-1">
            <label class="text-sm font-medium flex items-center gap-1.5">
              <span>{{ templateHeaderParamName }}</span>
              <span class="text-[10px] uppercase tracking-wider text-muted-foreground bg-muted px-1.5 py-0.5 rounded-md">
                {{ $t('chat.headerParamBadge', 'Header') }}
              </span>
            </label>
            <Input
              v-model="templateHeaderParamValue"
              :placeholder="templateHeaderParamName ?? ''"
              class="h-9"
            />
          </div>
          <div v-for="param in templateParamNames" :key="param" class="space-y-1">
            <label class="text-sm font-medium">{{ param }}</label>
            <Input
              v-model="templateParamValues[param]"
              :placeholder="param"
              class="h-9"
            />
          </div>
          <div v-for="(btnParam, idx) in templateButtonUrlParams" :key="`btn-${btnParam.index}`" class="space-y-1">
            <label class="text-sm font-medium">
              {{ btnParam.type === 'COPY_CODE' ? `Coupon Code (${btnParam.text})` : $t('chat.urlButtonParam', { button: btnParam.text }) }}
            </label>
            <Input
              v-model="templateButtonUrlParams[idx].value"
              :placeholder="btnParam.type === 'COPY_CODE' ? 'WELCOME10' : $t('chat.urlButtonParamPlaceholder')"
              class="h-9"
            />
          </div>
          <div v-if="templatePreview" class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground">{{ $t('chat.preview') }}</label>
            <div class="chat-bubble chat-bubble-outgoing ml-auto" style="max-width: 100%;">
              <img v-if="templateHeaderPreview" :src="templateHeaderPreview" class="rounded-lg mb-2 max-h-40 w-full object-cover" />
              <span class="whitespace-pre-wrap break-words text-sm">{{ templatePreview }}</span>
              <div
                v-if="selectedTemplate?.buttons?.length"
                class="interactive-buttons mt-2 -mx-2 -mb-1.5 border-t"
              >
                <div
                  v-for="(btn, index) in selectedTemplate.buttons"
                  :key="index"
                  :class="['py-2 text-sm text-center font-medium', Number(index) > 0 && 'border-t']"
                >
                  {{ btn.text }}
                </div>
              </div>
            </div>
          </div>
        </div>
        <div class="flex justify-end gap-2">
          <Button variant="outline" @click="templateDialogOpen = false">{{ $t('common.cancel') }}</Button>
          <Button @click="sendTemplateMessage" :disabled="isSendingTemplate">
            <Loader2 v-if="isSendingTemplate" class="h-4 w-4 mr-2 animate-spin" />
            {{ $t('chat.send') }}
          </Button>
        </div>
      </DialogContent>
    </Dialog>

    <!-- Canned Response Preview Dialog -->
    <Dialog v-model:open="cannedDialogOpen">
      <DialogContent class="max-w-sm">
        <!-- DialogContent doesn't forward $attrs (multi-root via DialogPortal),
             so wrap the body in a div carrying the stable id used by e2e. -->
        <div id="canned-response-dialog">
        <DialogHeader>
          <DialogTitle>{{ cannedParamNames.length > 0 ? $t('chat.fillParameters') : $t('chat.preview') }}</DialogTitle>
          <DialogDescription>
            {{ selectedCannedResponse?.name }}
          </DialogDescription>
        </DialogHeader>
        <div class="py-4 space-y-3">
          <div v-for="param in cannedParamNames" :key="param" class="space-y-1">
            <label class="text-sm font-medium" :for="`canned-response-param-${param}`">{{ param }}</label>
            <Input
              :id="`canned-response-param-${param}`"
              v-model="cannedParamValues[param]"
              :placeholder="param"
              class="h-9 canned-response-param"
            />
          </div>
          <div v-if="cannedPreview || cannedPreviewButtons.length" class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground">{{ $t('chat.preview') }}</label>
            <div id="canned-response-preview" class="chat-bubble chat-bubble-outgoing ml-auto" style="max-width: 100%;">
              <span v-if="cannedPreview" class="whitespace-pre-wrap break-words text-sm">{{ cannedPreview }}</span>
              <PreviewButtonGroup
                v-if="cannedPreviewButtons.length"
                :buttons="cannedPreviewButtons"
                disabled
              />
            </div>
          </div>
        </div>
        <div class="flex justify-end gap-2">
          <Button id="canned-response-cancel" variant="outline" @click="cannedDialogOpen = false">{{ $t('common.cancel') }}</Button>
          <Button id="canned-response-send" :disabled="isSendingCanned" @click="sendCannedResponse">
            <Loader2 v-if="isSendingCanned" class="h-4 w-4 mr-2 animate-spin" />
            {{ $t('chat.send') }}
          </Button>
        </div>
        </div>
      </DialogContent>
    </Dialog>

    <!-- Assign Contact Dialog -->
    <Dialog v-model:open="isAssignDialogOpen" @update:open="(open) => !open && (assignSearchQuery = '')">
      <DialogContent class="max-w-sm">
        <DialogHeader>
          <DialogTitle>{{ $t('chat.assignContact') }}</DialogTitle>
          <DialogDescription>
            {{ $t('chat.assignContactDesc') }}
          </DialogDescription>
        </DialogHeader>
        <div class="py-4 space-y-3">
          <!-- Search input -->
          <div class="relative">
            <Search class="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input
              v-model="assignSearchQuery"
              :placeholder="$t('chat.searchUsers') + '...'"
              class="pl-9 h-9"
            />
          </div>
          <Button
            v-if="contactsStore.currentContact?.assigned_user_id"
            variant="outline"
            class="w-full justify-start"
            @click="assignContactToUser(null); isAssignDialogOpen = false"
          >
            <UserMinus class="mr-2 h-4 w-4" />
            {{ $t('chat.unassignContact') }}
          </Button>
          <Separator />
          <ScrollArea class="max-h-[280px]">
            <div class="space-y-1">
              <Button
                v-for="user in filteredAssignableUsers"
                :key="user.id"
                :variant="contactsStore.currentContact?.assigned_user_id === user.id ? 'secondary' : 'ghost'"
                class="w-full justify-start"
                @click="assignContactToUser(user.id); isAssignDialogOpen = false"
              >
                <User class="mr-2 h-4 w-4" />
                <span>{{ user.full_name }}</span>
                <Check
                  v-if="contactsStore.currentContact?.assigned_user_id === user.id"
                  class="ml-auto h-4 w-4 text-primary"
                />
                <Badge v-else variant="outline" class="ml-auto text-xs">
                  {{ user.role?.name }}
                </Badge>
              </Button>
              <p v-if="filteredAssignableUsers.length === 0" class="text-sm text-muted-foreground text-center py-4">
                {{ $t('chat.noUsersFound') }}
              </p>
            </div>
          </ScrollArea>
        </div>
      </DialogContent>
    </Dialog>

    <!-- Media Preview Dialog -->
    <!-- Snoozing from the queue. Presets because the useful answers are few,
         and a free field because sometimes they are not. -->
    <Dialog :open="!!snoozingContactId" @update:open="open => !open && (snoozingContactId = null)">
      <DialogContent class="sm:max-w-[400px]">
        <DialogHeader><DialogTitle>{{ $t('inbox.snoozeTitle') }}</DialogTitle></DialogHeader>
        <div class="space-y-3">
          <div class="grid grid-cols-2 gap-2">
            <Button
              v-for="preset in snoozePresets"
              :key="preset.key"
              variant="outline"
              size="sm"
              class="justify-between"
              @click="applySnooze(preset.at())"
            >
              <span>{{ $t(`inbox.snoozePresets.${preset.key}`) }}</span>
              <span class="text-xs text-muted-foreground">{{ presetTime(preset.at()) }}</span>
            </Button>
          </div>
          <div class="space-y-1.5">
            <Label>{{ $t('inbox.snoozeUntil') }}</Label>
            <Input v-model="snoozeUntilValue" type="datetime-local" />
            <p class="text-xs text-muted-foreground">{{ $t('inbox.snoozeHint') }}</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="snoozingContactId = null">{{ $t('common.cancel') }}</Button>
          <Button :disabled="!snoozeUntilValue" @click="applySnooze(new Date(snoozeUntilValue))">
            {{ $t('inbox.snooze') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="isMediaDialogOpen">
      <DialogContent class="max-w-md">
        <DialogHeader>
          <DialogTitle>{{ $t('chat.sendMedia') }}</DialogTitle>
          <DialogDescription>
            {{ selectedFile?.name }}
          </DialogDescription>
        </DialogHeader>
        <div class="py-4 space-y-4">
          <!-- Image preview -->
          <div v-if="selectedFile?.type.startsWith('image/') && filePreviewUrl" class="flex justify-center">
            <img
              :src="filePreviewUrl"
              :alt="selectedFile.name"
              class="max-w-full max-h-[300px] rounded-lg object-contain"
            />
          </div>
          <!-- Video preview -->
          <div v-else-if="selectedFile?.type.startsWith('video/') && filePreviewUrl" class="flex justify-center">
            <video
              :src="filePreviewUrl"
              controls
              class="max-w-full max-h-[300px] rounded-lg"
            />
          </div>
          <!-- Audio preview -->
          <div v-else-if="selectedFile?.type.startsWith('audio/')" class="flex justify-center">
            <div class="flex items-center gap-3 px-4 py-3 bg-muted rounded-lg">
              <div class="h-10 w-10 rounded-full bg-primary/10 flex items-center justify-center">
                <Paperclip class="h-5 w-5 text-primary" />
              </div>
              <div>
                <p class="font-medium text-sm">{{ selectedFile.name }}</p>
                <p class="text-xs text-muted-foreground">{{ $t('chat.audioFile') }}</p>
              </div>
            </div>
          </div>
          <!-- Document preview -->
          <div v-else-if="selectedFile" class="flex justify-center">
            <div class="flex items-center gap-3 px-4 py-3 bg-muted rounded-lg">
              <div class="h-10 w-10 rounded-full bg-primary/10 flex items-center justify-center">
                <FileText class="h-5 w-5 text-primary" />
              </div>
              <div>
                <p class="font-medium text-sm truncate max-w-[200px]">{{ selectedFile.name }}</p>
                <p class="text-xs text-muted-foreground">
                  {{ (selectedFile.size / 1024).toFixed(1) }} KB
                </p>
              </div>
            </div>
          </div>

          <!-- Caption input (not for audio) -->
          <div v-if="selectedFile && !selectedFile.type.startsWith('audio/')">
            <Textarea
              v-model="mediaCaption"
              :placeholder="$t('chat.mediaCaption') + '...'"
              class="min-h-[60px] max-h-[100px] resize-none"
              :rows="2"
            />
          </div>

          <!-- Actions -->
          <div class="flex justify-end gap-2">
            <Button variant="outline" @click="closeMediaDialog" :disabled="isUploadingMedia">
              {{ $t('common.cancel') }}
            </Button>
            <Button @click="sendMediaMessage" :disabled="isUploadingMedia">
              <Send v-if="!isUploadingMedia" class="mr-2 h-4 w-4" />
              <span v-if="isUploadingMedia">{{ $t('chat.sending') }}...</span>
              <span v-else>{{ $t('chat.send') }}</span>
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>

    <!-- Add Contact Dialog -->
    <CreateContactDialog v-model:open="isAddContactOpen" @created="onContactCreated" />

    <!-- In-app media viewer (lightbox) -->
    <MediaViewerDialog
      v-model:open="mediaViewerOpen"
      v-model:index="mediaViewerIndex"
      :items="viewableMedia"
    />
  </div>
</template>

<style scoped>
.sticky-date-enter-active,
.sticky-date-leave-active {
  transition: opacity 0.3s ease;
}

.sticky-date-enter-from,
.sticky-date-leave-to {
  opacity: 0;
}
</style>
