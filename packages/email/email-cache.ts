import { createHash } from 'node:crypto'
import * as fs from 'node:fs'
import * as path from 'node:path'

/**
 * The email build's cache: a content-addressed lock beside the compiled
 * output. A template compiles again only when its source changed, when
 * the toolchain that renders it changed, or when its output vanished —
 * the three ways the compiled bytes could differ from what is on disk.
 *
 * The lock is a local artifact (gitignored beside the .tmpl pairs it
 * describes): it describes the outputs, it does not replace them. A fresh
 * clone simply builds once.
 */

export const CACHE_FILE = '.build-cache.json'
export const CACHE_VERSION = 1

export interface TemplateCacheEntry {
  /** SHA-256 of the template source the outputs were rendered from. */
  hash: string
  /** The output files a successful build wrote for this template. */
  outputs: string[]
  /** When the entry was compiled (ms epoch; informational). */
  builtAt: number
}

export interface BuildCache {
  version: number
  /** Digest of the toolchain the render depends on; a mismatch obsoletes everything. */
  toolchain: string
  templates: Record<string, TemplateCacheEntry>
}

/** What a reconcile pass found. `stale` compiles, the rest stays. */
export interface RebuildPlan {
  /** Template file names (basename) whose compile is required. */
  stale: string[]
  /** Template file names whose on-disk output is current. */
  cached: string[]
  /** Template names that vanished from the source; their outputs go. */
  removed: string[]
}

export function sha256(content: string | Buffer): string {
  return createHash('sha256').update(content).digest('hex')
}

export function cachePath(absOutput: string): string {
  return path.join(absOutput, CACHE_FILE)
}

export function loadCache(absOutput: string): BuildCache {
  try {
    const raw = fs.readFileSync(cachePath(absOutput), 'utf8')
    const parsed = JSON.parse(raw) as BuildCache
    if (parsed.version === CACHE_VERSION && typeof parsed.toolchain === 'string') {
      return parsed
    }
  } catch {
    // A missing or unreadable lock is an empty cache: everything builds once.
  }
  return { version: CACHE_VERSION, toolchain: '', templates: {} }
}

/**
 * Writes the lock atomically — a build that dies mid-write leaves the
 * previous lock, whose worst case is one extra compile next run.
 */
export function saveCache(absOutput: string, cache: BuildCache): void {
  const target = cachePath(absOutput)
  const tmp = `${target}.${process.pid}.tmp`
  fs.writeFileSync(tmp, JSON.stringify(cache, null, 2) + '\n')
  fs.renameSync(tmp, target)
}

/**
 * Answers the work a reconcile pass owes. An entry is stale unless the
 * hash matches the source on disk, the toolchain digest matches, and
 * every output the entry names still exists — a lock that lies about
 * output that was cleaned must not skip a compile.
 */
export function planRebuild(
  cache: BuildCache,
  files: string[],
  absTemplates: string,
  absOutput: string,
  toolchain: string
): RebuildPlan {
  const plan: RebuildPlan = { stale: [], cached: [], removed: [] }
  const obsolete = cache.toolchain !== toolchain

  for (const file of files) {
    const entry = cache.templates[file]
    const outputsMissing =
      !entry || entry.outputs.some((out) => !fs.existsSync(path.join(absOutput, out)))

    if (
      obsolete ||
      !entry ||
      entry.hash !== sha256(fs.readFileSync(path.join(absTemplates, file))) ||
      outputsMissing
    ) {
      plan.stale.push(file)
    } else {
      plan.cached.push(file)
    }
  }

  for (const [file, entry] of Object.entries(cache.templates)) {
    if (!files.includes(file)) {
      plan.removed.push(...entry.outputs)
      delete cache.templates[file]
    }
  }

  return plan
}

/** Deletes the compiled pairs of the templates that vanished. */
export function pruneOutputs(absOutput: string, outputs: string[]): void {
  for (const out of outputs) {
    try {
      fs.rmSync(path.join(absOutput, out), { force: true })
    } catch {
      // A file that is already gone is the state the prune wanted.
    }
  }
}
