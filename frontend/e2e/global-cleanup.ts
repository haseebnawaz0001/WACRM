import { Client } from 'pg'

/**
 * Wipe leftover E2E test data before the suite runs.
 *
 * Why: tests don't currently clean up after themselves (no per-test DB
 * isolation, CI gives a fresh DB but local dev runs accumulate). Stale
 * rows cause two problems:
 *   1. Strict-mode violations when a substring filter (e.g. picking
 *      role "Agent") matches both the system role and leftover rows
 *      named "E2E Transfer Agent 1777979885793" or
 *      "E2E-queue-pickup-XXX-pickup-agent-role".
 *   2. Slow-growing table sizes that make local list-view tests fragile.
 *
 * What we delete: anything whose name / email matches a known E2E prefix.
 * Patterns:
 *   - 'E2E-%'           — current framework prefix (createTestScope)
 *   - 'E2E %'           — legacy 'E2E Transfer Agent <ts>' style
 *   - 'e2e-%@e2e.test'  — current framework email
 *   - 'e2e-%@test.com'  — legacy generateUniqueEmail() before .e2e.test domain
 *   - 'e2e_%'           — templates seeded by SQL in template-sending.spec.ts
 *
 * Ordering: child tables first so FKs don't reject parent deletes. Within
 * each table we use a single statement; failures (FK violation, unknown
 * column, …) are logged and the cleanup continues — better to over-clean
 * than to leave half a state.
 */

const E2E_NAME_PREDICATE = `(name LIKE 'E2E-%' OR name LIKE 'E2E %')`
// @e2e.test is reserved for the framework's email factory — anything in
// that domain is safe to delete. Legacy generateUniqueEmail() landed on
// @test.com with an `e2e-` prefix; we match those too. Stable seeded users
// (admin@test.com, manager@test.com, agent@test.com) don't have the
// `e2e-` prefix so they're never touched.
const E2E_USER_EMAIL_PREDICATE = `(email LIKE '%@e2e.test' OR email LIKE 'e2e-%@test.com')`

// A contact is identified by its phone number, not its name. The webhook
// rewrites profile_name from the payload it is given, and a contact created
// from a phone number alone never had a name to match — so a name-only
// predicate walked past both and the demo database grew a little every run.
// Every phone the framework hands out sits in the reserved 9199 range (1555 is
// the range an older generator in journeys.spec.ts used). Names still count,
// for fixtures created before the range existed, including the two literal
// names those webhook payloads used to hard-code.
const E2E_LEGACY_CONTACT_NAMES = `('Message Actions', 'Journey Conversation')`
const E2E_CONTACT_PREDICATE = `(profile_name LIKE 'E2E-%' OR profile_name LIKE 'E2E %' OR profile_name IN ${E2E_LEGACY_CONTACT_NAMES} OR phone_number LIKE '9199%' OR phone_number LIKE '1555%')`

// Statements run sequentially. Each is best-effort: a failure logs and
// the loop continues. Phrased as "DELETE ... USING <child>" or scoped to
// the prefix so stable rows (admin@test.com, system roles) are untouched.
const CLEANUP_STATEMENTS: Array<{ label: string; sql: string }> = [
  // The CRM tables reference contacts too, and were added after this list was
  // written — so every run ended with "violates foreign key constraint
  // fk_tasks_contact" and left the contacts behind.
  {
    label: 'tasks on E2E contacts',
    sql: `DELETE FROM tasks WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'deal stage history on E2E contacts',
    sql: `DELETE FROM deal_stage_history WHERE deal_id IN (SELECT id FROM deals WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE}))`,
  },
  {
    label: 'deals on E2E contacts',
    sql: `DELETE FROM deals WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'custom field values of E2E contacts',
    sql: `DELETE FROM custom_field_values WHERE entity_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'activity of E2E contacts',
    sql: `DELETE FROM contact_activities WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'outbox events of E2E contacts',
    sql: `DELETE FROM crm_event_outbox WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'merge records of E2E contacts',
    sql: `DELETE FROM contact_merges WHERE primary_contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE}) OR secondary_contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'identities of E2E contacts',
    sql: `DELETE FROM contact_identities WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  // Messages next — they reference contacts and users.
  {
    label: 'messages of E2E contacts',
    sql: `DELETE FROM messages WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  // Notes are scoped to contact + user.
  {
    label: 'conversation_notes for E2E contacts',
    sql: `DELETE FROM conversation_notes WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  // Agent transfers reference contacts + users + teams.
  // Everything else that points at a contact. These were missing because the
  // old name-only predicate never matched a contact that had a conversation,
  // so the FK they hold was never reached.
  {
    label: 'conversations of E2E contacts',
    sql: `DELETE FROM conversations WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'chatbot_sessions of E2E contacts',
    sql: `DELETE FROM chatbot_sessions WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'call_transfers of E2E contacts',
    sql: `DELETE FROM call_transfers WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'call_logs of E2E contacts',
    sql: `DELETE FROM call_logs WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'call_permissions of E2E contacts',
    sql: `DELETE FROM call_permissions WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'agent_transfers for E2E contacts',
    sql: `DELETE FROM agent_transfers WHERE contact_id IN (SELECT id FROM contacts WHERE ${E2E_CONTACT_PREDICATE})`,
  },
  {
    label: 'agent_transfers assigned to E2E users',
    sql: `DELETE FROM agent_transfers WHERE agent_id IN (SELECT id FROM users WHERE ${E2E_USER_EMAIL_PREDICATE})`,
  },
  // User org memberships first — covers both E2E users (so we can delete
  // them) and memberships pointing at E2E roles or orgs (so the role / org
  // delete below isn't blocked by FK).
  {
    label: 'user_organizations for E2E users',
    sql: `DELETE FROM user_organizations WHERE user_id IN (SELECT id FROM users WHERE ${E2E_USER_EMAIL_PREDICATE})`,
  },
  {
    label: 'user_organizations referencing E2E roles',
    sql: `DELETE FROM user_organizations WHERE role_id IN (SELECT id FROM custom_roles WHERE ${E2E_NAME_PREDICATE})`,
  },
  {
    label: 'user_organizations in E2E orgs',
    sql: `DELETE FROM user_organizations WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE})`,
  },
  {
    label: 'team_members for E2E users',
    sql: `DELETE FROM team_members WHERE user_id IN (SELECT id FROM users WHERE ${E2E_USER_EMAIL_PREDICATE})`,
  },
  // canned_responses created by an E2E user keep the user row pinned via
  // FK; nuke them first.
  {
    label: 'canned_responses authored by E2E users',
    sql: `DELETE FROM canned_responses WHERE created_by_id IN (SELECT id FROM users WHERE ${E2E_USER_EMAIL_PREDICATE})`,
  },
  // canned_responses authored by users in E2E orgs (covers users we'll
  // delete via the org-membership path below).
  {
    label: 'canned_responses authored by users in E2E orgs',
    sql: `DELETE FROM canned_responses WHERE created_by_id IN (SELECT id FROM users WHERE role_id IN (SELECT id FROM custom_roles WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE})))`,
  },
  // Now the entity tables.
  //
  // Contacts come before users: contacts.assigned_user_id points at a user, so
  // deleting the users first fails on fk_contacts_assigned_user and then takes
  // the roles down with it (fk_users_role), which is how every run used to end
  // with its users and custom roles still sitting in the database.
  {
    label: 'E2E contacts',
    sql: `DELETE FROM contacts WHERE ${E2E_CONTACT_PREDICATE}`,
  },
  // A demo contact an E2E user was assigned to outlives this run, so the
  // reference is released rather than the contact deleted.
  {
    label: 'assignments of demo contacts to E2E users',
    sql: `UPDATE contacts SET assigned_user_id = NULL WHERE assigned_user_id IN (SELECT id FROM users WHERE ${E2E_USER_EMAIL_PREDICATE})`,
  },
  {
    label: 'assignments of demo contacts to users in E2E orgs',
    sql: `UPDATE contacts SET assigned_user_id = NULL WHERE assigned_user_id IN (SELECT id FROM users WHERE role_id IN (SELECT id FROM custom_roles WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE})))`,
  },
  {
    label: 'E2E users (by email pattern)',
    sql: `DELETE FROM users WHERE ${E2E_USER_EMAIL_PREDICATE}`,
  },
  // Catch users in E2E orgs that didn't match the email predicate (e.g.
  // legacy `org1-admin-<ts>@test.com` pattern from older spec versions).
  // Match by their role pointing at a role in an E2E org.
  {
    label: 'users with roles in E2E orgs',
    sql: `DELETE FROM users WHERE role_id IN (SELECT id FROM custom_roles WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE}))`,
  },
  {
    label: 'role_permissions for E2E roles',
    sql: `DELETE FROM role_permissions WHERE custom_role_id IN (SELECT id FROM custom_roles WHERE ${E2E_NAME_PREDICATE})`,
  },
  {
    label: 'E2E custom roles',
    sql: `DELETE FROM custom_roles WHERE ${E2E_NAME_PREDICATE}`,
  },
  {
    label: 'E2E teams',
    sql: `DELETE FROM teams WHERE ${E2E_NAME_PREDICATE}`,
  },
  {
    label: 'E2E tags',
    sql: `DELETE FROM tags WHERE ${E2E_NAME_PREDICATE}`,
  },
  {
    label: 'E2E keyword rules',
    sql: `DELETE FROM keyword_rules WHERE ${E2E_NAME_PREDICATE}`,
  },
  {
    label: 'E2E AI contexts',
    sql: `DELETE FROM ai_contexts WHERE ${E2E_NAME_PREDICATE}`,
  },
  {
    label: 'E2E custom actions',
    sql: `DELETE FROM custom_actions WHERE ${E2E_NAME_PREDICATE}`,
  },
  {
    label: 'E2E webhooks',
    sql: `DELETE FROM webhooks WHERE ${E2E_NAME_PREDICATE}`,
  },
  {
    label: 'E2E templates',
    sql: `DELETE FROM templates WHERE name LIKE 'e2e_%' OR display_name LIKE 'E2E-%' OR display_name LIKE 'E2E %'`,
  },
  {
    label: 'E2E whatsapp_accounts',
    sql: `DELETE FROM whatsapp_accounts WHERE name LIKE 'e2e-%' OR name LIKE 'E2E-%'`,
  },
  {
    label: 'chatbot_settings for E2E orgs',
    sql: `DELETE FROM chatbot_settings WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE})`,
  },
  // custom_roles in E2E orgs may not match name-based cleanup (a default
  // role auto-created with the org carries the system name). Strip them
  // by org_id so the organization delete can succeed.
  {
    label: 'role_permissions for roles in E2E orgs',
    sql: `DELETE FROM role_permissions WHERE custom_role_id IN (SELECT id FROM custom_roles WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE}))`,
  },
  {
    label: 'custom_roles in E2E orgs',
    sql: `DELETE FROM custom_roles WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE})`,
  },
  {
    label: 'widgets in E2E orgs',
    sql: `DELETE FROM widgets WHERE organization_id IN (SELECT id FROM organizations WHERE ${E2E_NAME_PREDICATE})`,
  },
  {
    label: 'E2E organizations',
    sql: `DELETE FROM organizations WHERE ${E2E_NAME_PREDICATE}`,
  },
]

export async function cleanupE2EData(connectionString: string): Promise<void> {
  const client = new Client({ connectionString })
  try {
    await client.connect()
  } catch (err) {
    console.log(`  ⚠️  Skipping E2E cleanup — couldn't connect to DB: ${(err as Error).message}`)
    return
  }

  let totalDeleted = 0
  for (const { label, sql } of CLEANUP_STATEMENTS) {
    try {
      const result = await client.query(sql)
      const count = result.rowCount ?? 0
      if (count > 0) {
        console.log(`  🧹 Removed ${count} ${label}`)
      }
      totalDeleted += count
    } catch (err) {
      // Don't abort: an unknown column or FK glitch shouldn't block the
      // rest of the cleanup. Log so a curious dev sees what's left.
      console.log(`  ⚠️  Cleanup of ${label} failed: ${(err as Error).message}`)
    }
  }

  await client.end()

  if (totalDeleted === 0) {
    console.log('  ✨ No leftover E2E rows found')
  } else {
    console.log(`  ✨ Cleaned up ${totalDeleted} stale E2E rows`)
  }
}
