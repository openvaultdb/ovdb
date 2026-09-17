// The web half of internal/datapath: paths inside a database written the way
// people type them in the CLI (`/lists/to-buy/items`), with ids escaped as
// record.EscapeID escapes them (spec/features/database-context-navigation
// #REQ:path-resolution).

const escapes: Record<string, string> = { '%2F': '/', '%2E': '.', '%24': '$', '%23': '#', '%5B': '[', '%5D': ']' }
const reverse: [string, string][] = [
  ['.', '%2E'],
  ['$', '%24'],
  ['#', '%23'],
  ['[', '%5B'],
  [']', '%5D'],
  ['/', '%2F'],
]

export type Kind = 'root' | 'collection' | 'record'

/** One id or collection name as the CLI shows it. */
export function escapeId(id: string): string {
  return reverse.reduce((s, [raw, escaped]) => s.split(raw).join(escaped), id)
}

/**
 * The id an escaped segment names, or null when it can't be one: a stray %,
 * a control character (U+0000–U+001F, U+007F), or a `/`-separated part that
 * is empty, `.` or `..` (the same rules as internal/datapath).
 */
export function unescapeSegment(segment: string): string | null {
  const id = decodeSegment(segment)
  if (id === null || /[\u0000-\u001f\u007f]/.test(id)) return null
  return id.split('/').some((part) => part === '' || part === '.' || part === '..') ? null : id
}

function decodeSegment(segment: string): string | null {
  let out = ''
  for (let i = 0; i < segment.length; i++) {
    if (segment[i] !== '%') {
      out += segment[i]
      continue
    }
    const raw = escapes[segment.slice(i, i + 3).toUpperCase()]
    if (raw === undefined) return null
    out += raw
    i += 2
  }
  return out
}

export function kindOf(segments: string[]): Kind {
  if (segments.length === 0) return 'root'
  return segments.length % 2 === 1 ? 'collection' : 'record'
}

/** `/lists/to-buy` from unescaped segments. */
export function display(segments: string[]): string {
  return '/' + segments.map(escapeId).join('/')
}

/** A server key path (escaped, no leading slash). */
export function keyOf(segments: string[]): string {
  return segments.map(escapeId).join('/')
}

/** The unescaped id at the end of a key the server returned. */
export function keyId(key: string): string {
  const last = key.slice(key.lastIndexOf('/') + 1)
  return unescapeSegment(last) ?? last
}

/** The data API URL path of the record at segments. */
export function recordURL(db: string, segments: string[]): string {
  return `/v1/databases/${encodeURIComponent(db)}/records/` + segments.map((s) => urlSegment(escapeId(s))).join('/')
}

// Percent-encodes everything but unreserved characters and the % of an
// escape, like datapath.Path.URLKey.
function urlSegment(escaped: string): string {
  return Array.from(new TextEncoder().encode(escaped))
    .map((byte) => {
      const c = String.fromCharCode(byte)
      return /[A-Za-z0-9\-_.~%]/.test(c) ? c : '%' + byte.toString(16).toUpperCase().padStart(2, '0')
    })
    .join('')
}

/**
 * One shell argument for a command people copy, quoted when needed: POSIX
 * single quotes, or PowerShell's on Windows (paths.QuoteArg in Go).
 */
export function quoteArg(value: string, windows = isWindows()): string {
  if (value !== '' && /^[A-Za-z0-9/._:%-]+$/.test(value)) return value
  return windows ? `'${value.split("'").join("''")}'` : `'${value.split("'").join("'\\''")}'`
}

function isWindows(): boolean {
  return typeof navigator !== 'undefined' && /win/i.test(navigator.platform ?? '')
}

/** The console route for a database path: /browse/<db>/<escaped segments>. */
export function browseRoute(db: string, segments: string[] = []): string {
  return '/browse/' + [db, ...segments.map(escapeId)].map(encodeURIComponent).join('/')
}

/** Reads a /browse route; null segments means an invalid path. */
export function parseBrowseRoute(pathname: string): { db: string; segments: string[] | null } | null {
  const parts = pathname.replace(/^\/browse\/?/, '').split('/').filter((p) => p !== '')
  if (parts.length === 0) return null
  let decoded: string[]
  try {
    decoded = parts.map(decodeURIComponent)
  } catch {
    return { db: parts[0], segments: null }
  }
  const segments = decoded.slice(1).map(unescapeSegment)
  return { db: decoded[0], segments: segments.some((s) => s === null) ? null : (segments as string[]) }
}
