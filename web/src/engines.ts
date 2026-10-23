// The storage catalogue as the console shows it. The server sends the
// engines already in order (inGitDB and SQLite pinned, then by name); the
// console only filters, with the rule setup.FilterEngines uses in Go
// (database-setup-and-providers#REQ:catalogue-order-and-filter).
import type { Engine } from './api'

export function filterEngines(engines: Engine[], filter: string): Engine[] {
  const needle = filter.trim().toLowerCase()
  return engines.filter((engine) => `${engine.id}\n${engine.name}\n${engine.description}`.toLowerCase().includes(needle))
}

/** The default location for a new database under the data home, as setup.DefaultPath builds it. */
export function defaultLocation(dataHome: string, engine: string, name: string): string {
  const separator = dataHome.includes('\\') && !dataHome.includes('/') ? '\\' : '/'
  const base = dataHome.endsWith(separator) ? dataHome : dataHome + separator
  return base + name + (engine === 'sqlite' ? '.sqlite' : '')
}

/** The name an edit_name remedy suggests (`ovdb databases create notes-2`), as setup.SuggestedName reads it. */
export function suggestedName(next: { command?: string; action?: string }): string {
  const fields = (next.command ?? '').trim().split(/\s+/)
  if (next.action !== 'edit_name' || fields.length < 4 || fields[1] !== 'databases' || fields[2] !== 'create' || fields[3].startsWith('<')) {
    return ''
  }
  return fields[3]
}

/** The database name a folder or file suggests: its name without an extension, when that is a valid name (as the TUI's nameFrom). */
export function nameFromLocation(location: string): string {
  const parts = location.trim().split(/[\\/]+/).filter((part) => part !== '')
  const base = parts.at(-1) ?? ''
  const dot = base.lastIndexOf('.')
  const name = dot > 0 ? base.slice(0, dot) : base
  return /^[a-zA-Z0-9][a-zA-Z0-9_-]*$/.test(name) ? name : ''
}
