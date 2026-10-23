// local-server-and-web-console#REQ:security-headers: console and apps render
// record values and copy as text only. This is the lint that enforces it.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

function vueFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) vueFiles(full, out)
    else if (/\.(vue|ts)$/.test(entry)) out.push(full)
  }
  return out
}

describe('text-only rendering', () => {
  it('no component uses v-html or innerHTML', () => {
    const offenders = ['src', 'apps']
      .flatMap((dir) => vueFiles(join(webRoot, dir)))
      .filter((file) => /v-html|innerHTML|insertAdjacentHTML/.test(readFileSync(file, 'utf8')))
    expect(offenders).toEqual([])
  })
})
