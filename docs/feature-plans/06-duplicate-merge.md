# 06 — Duplicate Detection and Merge

| | |
|---|---|
| **Priority** | **P2** (raise to P1 before large CSV imports) |
| **Phase** | 3 |
| **Effort** | 2.5 engineer-weeks |
| **Depends on** | F7 phone normalisation, 01 (fields, import), 02 (profile + timeline), 03 (conversations), 04 (tasks), F2/F3 events & activity. 07 deals are re-pointed if present |
| **Unlocks** | Trustworthy data for 05 segments, 08 automation and 09 reporting |
| **Status** | Proposed |

## 1. Problem
The same person can exist more than once:
- **different phone formats** (`03219173161` vs `923219173161` vs `+92 321 9173161`), because the only normalisation today is stripping `+`
- **imports** that bypass normalisation (`import_export.go:131`, where soft-deleted duplicates even cause unique-violation errors)
- **API "send template by phone"**, which does not strip `+` (`messages.go:789-804`)
- **the same customer on different WhatsApp numbers or with a username-based BSUID**

Contacts are unique on `(organization_id, phone_number)`. That index is **not partial**, so soft-deleted rows keep their phone, and `GetOrCreateContact` **restores** a soft-deleted match on inbound (`contactutil.go:18`). A naive "merge = soft delete the loser" would resurrect the loser on its next message.

## 2. Goals
1. **Detect** likely duplicates automatically (normalised phone, BSUID, email, near-identical name + phone) on create, import and in a periodic scan.
2. **Review** duplicate suggestions in a dedicated UI and on the contact profile.
3. **Merge** two (or more, pairwise) contacts into a primary contact, keeping **all history of both**: messages, calls, notes, conversations, transfers, sessions, tasks, deals, campaign sends, activity, field values, tags.
4. **Route future traffic:** messages to or from the merged contact's phone or BSUID resolve to the primary.
5. **Import dedupe** uses the same matcher, with options skip / update / create.

### Non-goals (v1)
- Automatic merging without human confirmation (except exact normalised-phone duplicates inside a single import file, which are collapsed before insert).
- Un-merge. The merge snapshot enables manual recovery; there is no one-click undo.
- Cross-organisation dedupe.

## 3. Current state — tables referencing a contact
| Table | Link | Re-point strategy |
|---|---|---|
| `messages` | `contact_id` | update to primary |
| `conversations` (03) | `contact_id` | update; resolve secondary's active conversation (`resolution_reason='merged'`) before update if both active |
| `agent_transfers` | `contact_id` + `phone_number` | update `contact_id`; keep historical `phone_number`; if both have an active transfer, keep the primary's, and expire the secondary's (`status='expired'`, note "merged") |
| `chatbot_sessions` | `contact_id` + `phone_number` | update `contact_id`; cancel the secondary's active session |
| `call_logs` | `contact_id` (nullable) + `caller_phone` | update `contact_id` |
| `call_transfers` | `contact_id` + `caller_phone` | update `contact_id` |
| `call_permissions` | `contact_id` | update; keep the latest accepted permission if both |
| `conversation_notes` | `contact_id` | update |
| `bulk_message_recipients` | `contact_id` (F8) + `phone_number` | update `contact_id` |
| `tasks` (04) | `contact_id` | update |
| `deals` (07) | `contact_id` | update |
| `custom_field_values` (01) | `entity_id` | resolve conflicts (4.3), move the rest |
| `contact_activities` (F3) | `contact_id` | update |
| `audit_logs` | `resource_type='contact'`, `resource_id` | **not** rewritten (audit integrity); the profile audit panel queries both IDs via `contact_identities` |
| Redis sticky-call keys | org + phone (`call_webhook.go:514`) | delete keys for the secondary phone |
| `messages.metadata.reactions[].from_phone` | text | left as is (historical) |

## 4. Design

### 4.1 Data model
```sql
-- Alternate identifiers that resolve to a contact
contact_identities (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  contact_id uuid not null,
  type varchar(16) not null,             -- phone | bsuid | email
  value varchar(255) not null,           -- raw (phone as WhatsApp ID / BSUID / email lower-cased)
  normalized varchar(255) not null,      -- phoneutil.Normalize / lower(email) / bsuid
  source varchar(16) not null,           -- merge | manual | import
  created_at timestamptz not null default now()
);
create unique index idx_contact_identities_unique on contact_identities (organization_id, type, normalized);
create index idx_contact_identities_contact on contact_identities (contact_id);

contacts.merged_into_id uuid null       -- set on the secondary; index (merged_into_id)

contact_duplicate_candidates (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  contact_a_id uuid not null,            -- lower uuid first (canonical pair order)
  contact_b_id uuid not null,
  reasons jsonb not null,                -- ["phone_normalized", "email", "name_phone_suffix"]
  score smallint not null,               -- 0-100
  status varchar(12) not null default 'pending', -- pending | dismissed | merged
  resolved_by_id uuid null, resolved_at timestamptz null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index idx_dup_pair on contact_duplicate_candidates (organization_id, contact_a_id, contact_b_id);
create index idx_dup_pending on contact_duplicate_candidates (organization_id, status, score desc);

contact_merges (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  primary_contact_id uuid not null,
  secondary_contact_id uuid not null,
  merged_by_id uuid not null,
  snapshot jsonb not null,               -- secondary contact row, field values, tags, per-table moved row counts
  field_resolution jsonb not null,       -- chosen values per field
  created_at timestamptz not null default now()
);
```

### 4.2 Detection
**Matcher** `internal/dedupe.Matcher`, with signals and scores:

| Signal | Score | Notes |
|---|---|---|
| Same `phone_normalized` (F7) | 100 | Near-certain; different WhatsApp IDs for the same number |
| Same BSUID | 100 | |
| Same email (`field.email`, lower-cased) | 70 | Families share emails, so a suggestion only |
| Phone last 9 digits equal **and** name similarity ≥ 0.8 (`pg_trgm` `similarity`) | 60 | Catches missing country codes when the org has no default country |
| Same name (≥ 0.9) **and** same company field | 40 | Weak; only shown when combined with another signal |

Pairs scoring ≥ 60 (or any 100 signal) become `pending` candidates. A dismissed pair is never re-suggested unless a **new** signal appears (the reason set grows).

**When detection runs:**
1. **On create** (inbound `GetOrCreateContact` after insert, `CreateContact`, import rows): synchronous exact checks (`phone_normalized`, BSUID, email). Heavier similarity checks run in the scan job.
2. **Scheduler job `duplicate_scanner`** (F4, hourly, leader-locked):
   - incremental over contacts updated since the last run
   - self-join on `phone_normalized`
   - email via `custom_field_values` for `field.email`
   - trigram similarity using a `pg_trgm` GIN index on `lower(profile_name)` (enable the extension in `getIndexes`: `CREATE EXTENSION IF NOT EXISTS pg_trgm`; if the extension is not permitted, skip that signal)
3. **Notifications:** when an org gains new score-100 candidates, admins with `contacts:merge` receive one digest notification per day (F5 `merge_suggestions`).

**Routing of future traffic** (fixes the resurrection bug). `contactutil.GetOrCreateContact` / `FindContact` lookup order:
1. exact `phone_number` (Unscoped); if the found row has `merged_into_id` → follow the chain (max 5 hops) to the live primary and **never restore** it
2. `phone_normalized`
3. `contact_identities (type=phone|bsuid)`
4. otherwise create

The same logic applies to the marketing-preference webhook (BSUID/phone), call-permission replies and send-by-phone.

### 4.3 Merge operation
`POST /api/contacts/{primaryId}/merge` `{ "secondary_id": "…", "field_resolution": { "profile_name": "secondary", "field.company": "primary", "field.email": "secondary" } }`

**Field resolution defaults** (UI pre-selects them; the user can override):

| Attribute | Default rule |
|---|---|
| `profile_name` | non-empty, most recently updated |
| Custom fields | primary's value if set, else secondary's; dropdowns same rule |
| `lifecycle_stage` | the more advanced stage by option position (e.g. customer > lead) |
| `tags` | union |
| `assigned_user_id` (contact owner) | primary's if set, else secondary's |
| `marketing_opt_out` | **true if either is true** (compliance-safe) |
| `last_message_at`, `last_inbound_at`, `last_message_preview` | from the most recent |
| `metadata` | shallow merge; primary wins on key conflicts; conflicting secondary values kept under `metadata._merged_<shortid>` |
| `whatsapp_account` | from the most recent inbound |

**Transaction** (single DB transaction, `SERIALIZABLE` or row locks on both contacts in UUID order to avoid deadlocks)
1. Lock both contact rows. Validate same org, both live, not already merged, `primary != secondary`.
2. Write `contact_merges.snapshot` (secondary row + field values + tags + per-table counts).
3. Apply field resolution to the primary (`customfields.SetValues`, tags union, contact columns).
4. **Handle active states:**
   - conversations (both active → resolve secondary's)
   - transfers (expire secondary's if both active)
   - chatbot sessions (cancel secondary's active)
5. Re-point every table in section 3 (`UPDATE … SET contact_id = $primary WHERE contact_id = $secondary`), recording row counts.
6. **Identities:**
   - insert `contact_identities` for the secondary's `phone_number` (type phone), BSUID and email (if different)
   - also insert the primary's own phone as an identity row if missing (keeps lookups uniform)
7. Mark the secondary: `merged_into_id = primary`, `deleted_at = now()`. The phone stays on the row, which remains in place because of the non-partial unique index. This is harmless now because lookups follow `merged_into_id`.
8. Update `contact_duplicate_candidates` involving the secondary: the pair → `merged`; other pairs re-keyed to the primary or deleted if they become self-pairs.
9. `PublishTx` `contact.merged` (`data`: secondary summary + moved counts) on the primary. Activity is recorded; the webhook is sent. `logAudit('contact', primary, updated, …)` and `logAudit('contact', secondary, deleted, …)`.
10. **After commit:**
    - Redis: delete sticky-call keys for the secondary phone
    - invalidate contact caches
    - WS `contact_updated` (primary) and a `contact_merged` hint so open tabs on the secondary redirect to the primary

**Limits:**
- 50,000 messages re-pointed in one transaction is fine. Above 250k messages on a secondary, the API returns 422 `merge_too_large`.
- Multi-contact merge = sequential pairwise merges from the UI (each is its own transaction and snapshot).

**Permissions:** new action `contacts:merge` (admin, manager). Both contacts must be accessible.

### 4.4 Import dedupe (extends 01)
- **Before insert:** collapse rows within the file that share `phone_normalized` (keep the last, report "merged N rows in file").
- **Existing contacts:** match via the lookup chain above. Import option `on_match`: `skip` (default) | `update` (fields, tags union, name if empty) | `create_anyway`, which creates a new contact only if the WhatsApp ID differs and flags a duplicate candidate.
- The result summary lists created / updated / skipped / flagged-duplicate counts, with a link to the Duplicates page filtered to the import.

### 4.5 Frontend
**Duplicates page** (`/contacts/duplicates`, a child route of the Contacts module, reached from a Contacts header button with a pending-count badge; `contacts:merge`)
- List of pending pairs sorted by score: both contacts side by side (name, phone, account, last message, message count, tags, lifecycle), reasons as chips, score.
- Actions: **Review & merge**, **Not a duplicate** (dismiss), bulk dismiss.
- Filter by reason and "from import X".

**Merge dialog** (`components/contacts/MergeContactsDialog.vue`)
- Two columns (A / B) with a "Keep as primary" toggle.
- Per-attribute radio rows for conflicting values only (identical values are shown collapsed). Tags show as union; opt-out is shown locked "Opted out (from B)".
- Impact summary: "Moves 48 messages, 3 calls, 2 notes, 1 open task; B's open conversation will be resolved."
- Typed confirmation for large moves (> 1,000 messages): type the primary's name.

**Contact profile (02)**
- A "Possible duplicate" banner when pending candidates exist (links to the merge dialog).
- "⋯ → Merge with…" opens a contact picker (search) for manual merges.
- Timeline renderer `TimelineMerge.vue`.
- Visiting a merged contact URL redirects to the primary with a toast "This contact was merged into Mia Thompson".

**Contacts list:** "Duplicates (12)" button in the header.

## 5. Migration & rollout
1. F7 must be complete (`phone_normalized` backfilled).
2. Schema: `contact_identities`, `contacts.merged_into_id`, candidates, merges; `pg_trgm` extension + name trigram index (best-effort).
3. F1 migration: permission `contacts:merge` for admin/manager; seed identities for existing BSUIDs (type bsuid) so BSUID lookups work uniformly.
4. Deploy the lookup-chain change **before** enabling the merge UI (it is safe on its own).
5. Run `duplicate_scanner` once manually after deploy for a full initial scan, batched per org.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | Lookup chain in `contactutil` (merged_into, normalized, identities) + all call sites + tests | 2 d |
| M2 | Matcher, candidates model, on-create exact checks, scanner job, notifications | 2.5 d |
| M3 | Merge transaction (resolution, active-state handling, re-pointing, identities, snapshot, events) + extensive tests | 3 d |
| M4 | Import dedupe options + summary | 1 d |
| M5 | Duplicates page, merge dialog, profile banner/redirect, timeline renderer | 3 d |

## 7. Testing
- **Lookup:** an inbound message from a merged secondary's phone lands on the primary; the secondary is **not** restored; a chain of two merges resolves.
- **Merge integrity:** fixtures with rows in every referencing table; after merge, counts on the primary equal the sum and nothing references the secondary (except audit_logs); the snapshot contains the correct counts.
- **Active states:** both with active conversations and transfers → one active remains, the other resolved/expired with a note.
- **Compliance:** opt-out true on either side → primary opted out.
- **Concurrency:** merging A→B and B→A simultaneously → exactly one succeeds, the other gets 409 (row locks in UUID order).
- **Scanner:** idempotent across runs; a dismissed pair is not re-created without a new reason.
- **E2E** (`e2e/tests/contacts/duplicates.spec.ts`): import a CSV containing a `0`-prefixed variant of an existing number → flagged → merge → timeline shows both histories → old URL redirects.

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Wrong merge destroys data separation | Human confirmation, impact summary, snapshot for manual recovery, audit logs untouched |
| Long transactions lock hot rows (`messages`) | Size limit, batched updates within the transaction are still one tx; run off-peak guidance for huge merges; 422 above limit |
| `pg_trgm` unavailable on managed Postgres | Signal is optional; exact signals still work |
| Which WhatsApp ID to send to after merge? | The primary's `phone_number`; the secondary's phone remains an identity for inbound routing only. The merge dialog lets users pick which contact is primary (i.e. which number to message) |
| Should score-100 duplicates auto-merge? | Not in v1; revisit with an org setting once merge reliability is proven |

## 9. Acceptance criteria
- [ ] Contacts that differ only by phone formatting are flagged as duplicates on create/import and by the hourly scan.
- [ ] Admins/managers can review, dismiss and merge duplicates; merge moves all history (messages, calls, notes, conversations, transfers, sessions, tasks, deals, campaign sends, activity, field values, tags) to the primary.
- [ ] Opt-out is preserved if either contact opted out; active conversations and transfers are consolidated without violating the one-active rule.
- [ ] Future messages from the secondary's phone or BSUID route to the primary, and merged contacts are never auto-restored.
- [ ] The primary's timeline shows a merge entry and both contacts' history; visiting the secondary's URL redirects.
- [ ] CSV import collapses in-file duplicates and offers skip/update/create-anyway for matches, reporting counts.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

- **Lookup chain:** the chain, and "never auto-restore merged contacts", live in the **Contact Lifecycle service** (S2). All paths use it: inbound, reactions, echoes, calls, the campaign worker, send-by-phone, import, marketing preferences and call-permission replies.
- **Re-pointing** comes from the **entityrefs registry** (S8), so new tables are covered automatically. It adds:
  - `automation_runs.contact_id`
  - `automation_contact_state` (upsert on primary-key collision)
  - `notifications.entity_id`
  - `tasks.call_log_id` consistency
  - in-flight outbox events for the secondary contact (re-keyed before relay)
- **Locking:** merge takes both contacts' advisory locks in UUID order, then conversation → transfer rows (S5 lock order).
- **Automation:** events emitted with `reason=merged` are excluded from automation triggers by default.
- **Chat:** the "Possible duplicate" banner also appears in the chat header. An open `/chat/<secondary>` handles the `contact_merged` WS event by redirecting to the primary.
- **Restore policy:** manually deleted contacts that write again are created fresh with a `restored_candidate` duplicate flag (pending product decision, 10 §8.3).
