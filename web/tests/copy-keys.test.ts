// The web half of spec/features/configuration-parity#REQ:copy-catalogue and
// its AC:copy-key-missing-fails: scans every .vue/.ts source file for a
// t('literal key') call and fails, naming the file and key, when that
// literal is absent from copy/en.json. This is the Vite-side mirror of
// copy/copy_ast_test.go's Go AST scan.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import en from '../../copy/en.json'

const here = dirname(fileURLToPath(import.meta.url))
const webRoot = join(here, '..')

function collectSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === 'dist') continue
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      collectSourceFiles(full, out)
    } else if (/\.(vue|ts)$/.test(entry) && !entry.endsWith('.test.ts')) {
      out.push(full)
    }
  }
  return out
}

describe('t() literal keys', () => {
  it('every literal t(\'key\') referenced in src/ and apps/ exists in copy/en.json', () => {
    const catalogue = en as Record<string, string>
    const keyPattern = /\bt\(\s*['"]([^'"]+)['"]/g
    const missing: string[] = []

    for (const dir of ['src', 'apps']) {
      for (const file of collectSourceFiles(join(webRoot, dir))) {
        const content = readFileSync(file, 'utf-8')
        for (const match of content.matchAll(keyPattern)) {
          const key = match[1]
          if (!(key in catalogue)) {
            missing.push(`${file}: t('${key}')`)
          }
        }
      }
    }

    expect(missing, missing.join('\n')).toEqual([])
  })
})

// t() throws on a missing key, and the scan above sees only literal keys, so
// `skills.state.${target.state}` is checked here against the states the API
// document declares (SkillTarget['state'] in src/api.ts): each has its text,
// so a state added to the type without copy fails here, not on a person's
// screen.
describe('skill states', () => {
  it('every state declared in src/api.ts has skills.state.<state> in copy/en.json', () => {
    const catalogue = en as Record<string, string>
    const source = readFileSync(join(webRoot, 'src', 'api.ts'), 'utf-8')
    const declared = /export interface SkillTarget \{[^}]*?state: ([^\n]+)\n/.exec(source)?.[1]
    expect(declared, 'could not find SkillTarget.state in src/api.ts').toBeTruthy()
    const states = [...declared!.matchAll(/'([a-z_]+)'/g)].map((match) => match[1])
    expect(states.length).toBeGreaterThanOrEqual(6)
    const missing = states.filter((state) => !(`skills.state.${state}` in catalogue)).map((state) => `skills.state.${state}`)
    expect(missing, `states without text: ${missing.join(', ')}`).toEqual([])
  })
})
