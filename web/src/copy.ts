// t() is the web half of spec/features/configuration-parity#REQ:copy-catalogue:
// it renders the same copy/en.json the Go binary embeds via copy/copy.go's
// uicopy.T, so CLI, TUI and web console never drift on wording. Vite inlines
// en.json into the build (a static JSON import), so the console and TODO app
// ship no separate copy fetch.
import en from '../../copy/en.json'

const catalogue = en as Record<string, string>

export type CopyParams = Record<string, string>

/**
 * Renders the copy catalogue entry named by key, replacing each "{name}"
 * placeholder in the template with params[name]. A placeholder with no
 * matching entry in params is left in place, matching copy/copy.go's T.
 *
 * Throws when key is absent from copy/en.json. Every call site passes a
 * string literal, so a missing key is a build-time defect:
 * tests/copy-keys.test.ts scans src/ and apps/ for every literal key passed
 * to this function and fails naming the one that is missing, so this should
 * never throw outside that demonstration.
 */
export function t(key: string, params: CopyParams = {}): string {
  const template = catalogue[key]
  if (template === undefined) {
    throw new Error(`Missing copy key: ${key}`)
  }
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    Object.prototype.hasOwnProperty.call(params, name) ? params[name] : match,
  )
}
