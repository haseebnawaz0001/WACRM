import { test, expect, type Page } from '@playwright/test'
import { ApiHelper, loginAsAdmin } from '../../helpers'
import {
  createTestScope,
  createUserWithPermissions,
  loginAs,
  SUPER_ADMIN,
  type TestUserHandle,
} from '../../framework'

// Helper to read the visible sidebar menu items as plain text.
/**
 * What the sidebar offers this user: the text of each entry and, for the ones
 * that are links, where it goes.
 *
 * These tests used to match on the word in the label — 'chat' for the
 * conversations item, 'settings' for the admin section. Both words have since
 * been rewritten: the chat and the inbox merged into one destination called
 * Inbox, and the sixteen settings pages moved behind a section called Manage.
 * The assertions went red for a rename while the permission gating they exist
 * to protect was working perfectly, which is the wrong thing to be sensitive
 * to. A destination outlives its label.
 */
async function getSidebarMenuItems(page: Page): Promise<string[]> {
  const items: string[] = []
  const navLinks = page.locator('aside a[role="menuitem"], aside nav a, aside nav button[class*="justify-start"]')
  const count = await navLinks.count()
  for (let i = 0; i < count; i++) {
    const text = await navLinks.nth(i).textContent()
    if (text && text.trim()) items.push(text.trim().toLowerCase())
  }
  return items
}

/** Whether the sidebar offers a link into the given path. */
async function hasNavTo(page: Page, path: string): Promise<boolean> {
  return (await page.locator(`aside a[href="${path}"], aside a[href^="${path}/"]`).count()) > 0
}

/**
 * Whether the admin section is on offer.
 *
 * It is a button rather than a link — it opens a drawer holding all sixteen
 * pages — so there is no href to look for.
 */
async function hasManageSection(page: Page): Promise<boolean> {
  const items = await getSidebarMenuItems(page)
  if (items.some(item => item.includes('manage') || item.includes('settings'))) return true
  return (await page.locator('aside').getByRole('button', { name: /^manage$/i }).count()) > 0
}

test.describe('Custom Role with Limited Permissions', () => {
  const scope = createTestScope('permissions-limited')
  let api: ApiHelper
  let user: TestUserHandle

  test.beforeAll(async ({ request }) => {
    api = new ApiHelper(request)
    await api.login(SUPER_ADMIN.email, SUPER_ADMIN.password)
    user = await createUserWithPermissions(api, scope, {
      permissions: [{ resource: 'chat', action: 'read' }],
    })
  })

  test.afterAll(async () => {
    await api.deleteUser(user.user.id).catch(() => {})
    await api.deleteRole(user.role.id).catch(() => {})
  })

  test('user with limited role sees only permitted menu items', async ({ page }) => {
    await loginAs(page, user)
    await page.waitForSelector('aside nav')
    await page.waitForTimeout(500)

    const menuItems = await getSidebarMenuItems(page)

    expect(await hasNavTo(page, '/inbox')).toBeTruthy()
    expect(await hasManageSection(page)).toBeFalsy()
    expect(menuItems.some((item) => item.includes('analytics') || item.includes('dashboard'))).toBeFalsy()
  })

  test('user with limited role is redirected from unauthorized pages', async ({ page }) => {
    await loginAs(page, user)
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')
    expect(page.url()).not.toContain('/settings')
  })

  test('user with limited role can access permitted pages', async ({ page }) => {
    await loginAs(page, user)
    // /chat is the pre-merge address and redirects to the surface that
    // replaced it; links written before the merge still have to work.
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    expect(page.url()).toContain('/inbox')
    await expect(page.locator('body')).not.toContainText('forbidden', { ignoreCase: true })
  })

  test('user lands on first accessible page after login', async ({ page }) => {
    await loginAs(page, user)
    // Home is the landing page for anyone without `analytics`: they cannot
    // open the Dashboard, and dropping them straight into the inbox skipped
    // their tasks and notifications entirely.
    expect(page.url()).toContain('/home')
  })
})

test.describe('Role with Settings Access', () => {
  const scope = createTestScope('permissions-settings')
  let api: ApiHelper
  let user: TestUserHandle

  test.beforeAll(async ({ request }) => {
    api = new ApiHelper(request)
    await api.login(SUPER_ADMIN.email, SUPER_ADMIN.password)
    user = await createUserWithPermissions(api, scope, {
      permissions: [
        { resource: 'chat', action: 'read' },
        { resource: 'users', action: 'read' },
        { resource: 'users', action: 'create' },
        { resource: 'settings.general', action: 'read' },
      ],
    })
  })

  test.afterAll(async () => {
    await api.deleteUser(user.user.id).catch(() => {})
    await api.deleteRole(user.role.id).catch(() => {})
  })

  test('user with settings permission sees Settings menu', async ({ page }) => {
    await loginAs(page, user)
    await page.waitForSelector('aside nav')
    await page.waitForTimeout(500)

    expect(await hasManageSection(page)).toBeTruthy()
  })

  test('user with users:read can access users page', async ({ page }) => {
    await loginAs(page, user)
    await page.goto('/settings/users')
    await page.waitForLoadState('networkidle')
    expect(page.url()).toContain('/settings/users')
    await expect(page.locator('table, [role="table"]').first()).toBeVisible()
  })

  test('user with users:create sees Add button', async ({ page }) => {
    await loginAs(page, user)
    await page.goto('/settings/users')
    await page.waitForLoadState('networkidle')
    const addButton = page.locator('button').filter({ hasText: /add|create/i })
    await expect(addButton.first()).toBeVisible()
  })
})

// Uses admin@test.com (the canonical admin-role user) deliberately —
// testing the admin role's behavior, not super-admin's.
test.describe('Admin vs Limited Role Comparison', () => {
  test('admin sees all menu items', async ({ page }) => {
    await loginAsAdmin(page)
    await page.waitForSelector('aside nav')
    await page.waitForTimeout(500)

    expect(await hasNavTo(page, '/inbox')).toBeTruthy()
    expect(await hasManageSection(page)).toBeTruthy()
  })

  test('admin can access all settings pages', async ({ page }) => {
    await loginAsAdmin(page)

    await page.goto('/settings/users')
    await page.waitForLoadState('networkidle')
    expect(page.url()).toContain('/settings/users')

    await page.goto('/settings/roles')
    await page.waitForLoadState('networkidle')
    expect(page.url()).toContain('/settings/roles')

    await page.goto('/settings')
    await page.waitForLoadState('networkidle')
    expect(page.url()).toContain('/settings')
  })
})

test.describe('Dynamic Role Updates', () => {
  const scope = createTestScope('permissions-dynamic')
  let api: ApiHelper
  let user: TestUserHandle

  test.beforeAll(async ({ request }) => {
    api = new ApiHelper(request)
    await api.login(SUPER_ADMIN.email, SUPER_ADMIN.password)
    user = await createUserWithPermissions(api, scope, {
      permissions: [{ resource: 'chat', action: 'read' }],
    })
  })

  test.afterAll(async () => {
    await api.deleteUser(user.user.id).catch(() => {})
    await api.deleteRole(user.role.id).catch(() => {})
  })

  test('user initially has limited access', async ({ page }) => {
    await loginAs(page, user)
    await page.waitForSelector('aside nav')

    expect(await hasNavTo(page, '/inbox')).toBeTruthy()
    expect(await hasManageSection(page)).toBeFalsy()
  })
})
