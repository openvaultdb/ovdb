// Increment 8 browser journeys: AI agent skills in the web console against a
// real ovdb binary whose home has Claude Code and no Codex
// (spec/features/ai-agent-skills AC:ui-shows-targets-before-install,
// AC:web-cannot-target-arbitrary-dir; todo-demo AC:next-actions-after-install;
// configuration-parity AC:journey-d-passes, with Explore data joining in
// increment 7).
import { existsSync, readdirSync, rmSync } from 'node:fs'
import { join } from 'node:path'

import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'
import { ovdb, primary, signedInContext } from './ovdb'

const out = (name: string) => join(import.meta.dirname, 'screenshots', name + '.png')
const userHome = () => process.env.OVDB_E2E_USER_HOME!
const claudeSkills = () => join(userHome(), '.claude', 'skills')

function resetDemo() {
  if ((JSON.parse(ovdb('demo', 'status', '--json')) as { installed: boolean }).installed) {
    ovdb('databases', 'remove', 'todo', '--yes')
  }
  rmSync(join(process.env.OVDB_DATA_HOME!, 'demos'), { recursive: true, force: true })
}

function resetSkills() {
  rmSync(claudeSkills(), { recursive: true, force: true })
}

async function expectAccessible(page: Page, name: string) {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze()
  expect(
    results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`),
    `axe violations on ${name}`,
  ).toEqual([])
}

type Listed = { records: { data: { title: string } }[] }
const titles = (path: string) => (JSON.parse(ovdb('list', path, '--db', 'todo', '--json')) as Listed).records.map((r) => r.data.title)

test('Journey D: Try a demo, open the app, install the TODO skill after the consent step, and see an agent’s change', async ({ browser }) => {
  resetDemo()
  resetSkills()
  const context = await signedInContext(browser, { viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(primary() + '/demo')
  await page.getByTestId('install-demo').click()
  const result = page.getByTestId('demo-result')
  await expect(result.getByRole('link')).toHaveText(['Open TODO app', 'Install TODO AI skill (ask the person first)', 'Done'])

  const app = await context.newPage()
  await app.goto(primary() + '/apps/todo/')
  await expect(app.getByRole('heading', { name: 'To buy' })).toBeVisible()

  await result.getByRole('link', { name: 'Install TODO AI skill (ask the person first)' }).click()
  await expect(page).toHaveURL(primary() + '/skills?skill=todo-demo&from=/demo')
  const consent = page.getByTestId('skill-consent')
  await expect(page.getByRole('heading', { name: 'Install the TODO AI skill?' })).toBeFocused()
  await expect(consent).toContainText('Lets your AI agent read and change your To buy and To watch lists.')
  const dir = join(claudeSkills(), 'openvaultdb-todo-demo')
  await expect(consent.locator('[data-harness="claude"] code')).toHaveText(dir)
  await expect(consent.getByRole('checkbox', { name: /Claude Code/ })).toBeChecked()
  await expect(consent.locator('[data-harness="codex"]')).toHaveText('Codex — not found')
  expect(existsSync(claudeSkills())).toBe(false)
  await expectAccessible(page, 'skill consent')

  // Not now goes back to the Result and writes nothing.
  await page.getByTestId('not-now').click()
  await expect(page).toHaveURL(primary() + '/demo')
  expect(existsSync(claudeSkills())).toBe(false)

  await page.getByTestId('demo-install-skill').click()
  await page.getByTestId('install-skill').click()
  const installed = page.getByTestId('skill-result')
  await expect(installed).toContainText('Installed the TODO AI skill')
  await expect(installed).toContainText(`Claude Code: ${dir}`)
  await expect(installed).toBeFocused()
  expect(readdirSync(claudeSkills()).sort()).toEqual(['.cli-helpers-skills-sync.json', 'openvaultdb-todo-demo'])
  await expectAccessible(page, 'skill installed')

  // The agent step: "add tea to my shopping list and Arrival to my watch
  // list" is, by the skill, these two commands.
  ovdb('add', '/lists/to-buy/items', '{"title":"Tea","done":false}', '--db', 'todo', '--json')
  ovdb('add', '/lists/to-watch/items', '{"title":"Arrival","done":false}', '--db', 'todo', '--json')
  await expect(app.locator('[data-list="to-buy"]').getByText('Tea', { exact: true })).toBeVisible({ timeout: 3000 })
  await expect(app.locator('[data-list="to-watch"]').getByText('Arrival', { exact: true })).toBeVisible({ timeout: 3000 })
  expect(titles('/lists/to-buy/items')).toContain('Tea')
  await context.close()
})

test('the web console cannot name a directory or an agent the server did not find', async ({ browser }) => {
  resetSkills()
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/skills')
  const outside = join(process.env.OVDB_DATA_HOME!, 'not-skills')
  const answers = await page.evaluate(async (dir) => {
    const post = async (body: unknown) => {
      const response = await fetch('/api/local/v1/skills/install', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      return [response.status, ((await response.json()) as { error?: { code: string } }).error?.code]
    }
    return [
      await post({ skill: 'openvaultdb', dir }),
      await post({ skill: 'openvaultdb', targets: [{ harness: 'claude', skills_dir: dir }] }),
      await post({ skill: 'openvaultdb', harnesses: ['codex'] }),
    ]
  }, outside)
  expect(answers).toEqual([
    [400, 'invalid_argument'],
    [400, 'invalid_argument'],
    [400, 'invalid_argument'],
  ])
  expect(existsSync(outside)).toBe(false)
  expect(existsSync(join(userHome(), '.codex'))).toBe(false)
  expect(existsSync(claudeSkills())).toBe(false)
  await context.close()
})

test('AI agent skills and the consent step at 360, 1280 and 1920 px in light and dark', async ({ browser }) => {
  resetSkills()
  for (const viewport of [{ width: 360, height: 740 }, { width: 1280, height: 800 }, { width: 1920, height: 1080 }]) {
    for (const colorScheme of ['light', 'dark'] as const) {
      const suffix = `${viewport.width}x${viewport.height}-${colorScheme}`
      const context = await signedInContext(browser, { viewport, colorScheme })
      const page = await context.newPage()
      await page.goto(primary() + '/skills')
      await page.locator('[data-skill="todo-demo"]').waitFor()
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
      await page.screenshot({ path: out(`skills-${suffix}`), fullPage: true })
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width)
      if (viewport.width !== 1920) await expectAccessible(page, `skills ${suffix}`)
      await page.getByTestId('offer-todo-demo').click()
      await page.getByTestId('skill-consent').waitFor()
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
      await page.screenshot({ path: out(`skill-consent-${suffix}`), fullPage: true })
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width)
      if (viewport.width !== 1920) await expectAccessible(page, `skill consent ${suffix}`)
      await context.close()
    }
  }
  expect(existsSync(claudeSkills())).toBe(false)
})
