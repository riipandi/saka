import * as fs from 'node:fs'
import { createRequire } from 'node:module'
import * as path from 'node:path'
import { pathToFileURL } from 'node:url'
import { isValidElement, type ReactNode } from 'react'
import { render } from 'react-email'
import { loadCache, planRebuild, pruneOutputs, saveCache, sha256 } from './email-cache.ts'
import type { TemplateCacheEntry } from './email-cache.ts'

type EmailTemplate = ((props: unknown) => ReactNode) & { TemplateProps?: unknown }

/**
 * The email package's build: renders the React Email templates into the Go
 * templates the binary embeds (`web/email/<name>_{html,text}.tmpl` — the
 * go:embed contract web/embed.go reads). Plain Node, no Vite pass: the
 * templates render in-process (tsx loader + react-email), they are never
 * bundled.
 *
 * The build is incremental — see `email-cache.ts`: a template compiles
 * again only when its source changed, the toolchain that renders it
 * changed, or its output vanished. Run with `pnpm exec vp run email#build`
 * (the Taskfile sequences this pass before the webapp pass so the binary
 * embeds fresh templates). Template editing in dev happens in the React
 * Email UI (`pnpm exec vp run email#dev`), which has its own watcher.
 */

// The templates and the compiled output live beside this script, wherever
// it is run from.
const templatesDir = path.resolve(import.meta.dirname, 'templates')
const outputDir = path.resolve(import.meta.dirname, '../../web/email')
const projectRoot = path.resolve(import.meta.dirname, '../..')

// Log style kept in sync with plugins/plugin-golang.ts
const C = {
  reset: '\x1b[0m',
  dim: '\x1b[2m',
  green: '\x1b[32m',
  red: '\x1b[31m',
  cyan: '\x1b[36m'
} as const

const PREFIX = `${C.cyan}[email]${C.reset}`

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

// Printed relative to the repo root, so a line names the path a developer
// would type; a path outside it stays absolute.
function displayPath(target: string): string {
  const rel = path.relative(projectRoot, target)
  if (rel === '') return '.'
  return rel.startsWith('..') ? target : rel
}

function log(msg: string) {
  console.log(`${PREFIX} ${msg}`)
}

function logInfo(label: string, value: string) {
  console.log(`${PREFIX} ${C.dim}${label.padEnd(10)}${C.reset}${value}`)
}

function logError(msg: string) {
  console.error(`${PREFIX} ${C.red}${msg}`)
}

/**
 * The rendered bytes depend on the template source and the stack that
 * renders it. A dependency bump — a patch that changes a render detail —
 * must obsolete every entry, not silently keep stale output; hashing the
 * resolved versions of the packages the render rides is the honest
 * fingerprint. A package that does not resolve contributes its name only,
 * which downgrades precision, never correctness.
 */
function toolchainDigest(): string {
  const require = createRequire(import.meta.url)
  const parts = ['react-email', 'react', 'tsx'].map((name) => {
    try {
      const pkg: { version?: unknown } = JSON.parse(require.resolve(`${name}/package.json`))
      return `${name}@${typeof pkg.version === 'string' ? pkg.version : 'unknown'}`
    } catch {
      return `${name}@unresolved`
    }
  })
  return sha256(parts.join('|'))
}

function getFirstExport(module: Record<string, unknown>): unknown {
  const keys = Object.keys(module)
  const firstKey = keys[0]
  if (keys.length === 0 || firstKey === undefined) return undefined
  return module[firstKey]
}

function isEmailTemplate(value: unknown): value is EmailTemplate {
  return typeof value === 'function'
}

// Template files are .tsx (JSX), so they must go through a TypeScript
// transform before we can import them in-process. tsx is already a
// devDependency; its ESM API registers the loader for the current thread.
async function importTemplate(file: string, cacheBust: number): Promise<Record<string, unknown>> {
  const { tsImport } = await import('tsx/esm/api')
  const mod: Record<string, unknown> = await tsImport(
    `${pathToFileURL(file).href}?t=${cacheBust}`,
    import.meta.url
  )
  return mod
}

async function buildTemplateFile(
  Component: (props: unknown) => ReactNode,
  templateProps: unknown,
  templateName: string,
  isPlainText: boolean
): Promise<void> {
  const element = Component(templateProps)
  if (!isValidElement(element)) {
    throw new Error('template did not render to a React element')
  }

  // `plainText` is a discriminated union, so it must be a literal, not a boolean.
  const rendered = isPlainText
    ? await render(element, { plainText: true })
    : await render(element, { plainText: false })

  // Normalize quotes
  const normalized = rendered.replace(/&quot;/g, '"')

  const goTemplate = `{{define "root"}}${normalized}{{end}}`
  const suffix = isPlainText ? '_text.tmpl' : '_html.tmpl'
  fs.writeFileSync(path.join(outputDir, `${templateName}${suffix}`), goTemplate)
}

// Compiles one template into its _html and _text pair and records the
// pair in the cache. Returns the entry on success, null on failure
// (already logged); a failed template writes no entry, so the next run
// owes it again.
async function buildOne(
  file: string,
  cache: Record<string, TemplateCacheEntry>
): Promise<string | null> {
  const templateName = file.replace('.tsx', '')
  const start = Date.now()

  try {
    const importedModule = await importTemplate(path.join(templatesDir, file), Date.now())
    const candidate: unknown = importedModule.default ?? getFirstExport(importedModule)
    if (!isEmailTemplate(candidate)) {
      throw new Error('no component export found')
    }
    const Component = candidate

    if (!Component.TemplateProps) {
      throw new Error('no TemplateProps export found')
    }

    await buildTemplateFile(Component, Component.TemplateProps, templateName, false) // HTML
    await buildTemplateFile(Component, Component.TemplateProps, templateName, true) // Text

    cache[file] = {
      hash: sha256(fs.readFileSync(path.join(templatesDir, file))),
      outputs: [`${templateName}_html.tmpl`, `${templateName}_text.tmpl`],
      builtAt: Date.now()
    }

    log(`  ${C.green}✓ ${templateName}${C.reset} in ${formatDuration(Date.now() - start)}`)
    return templateName
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    logError(`  ✗ ${templateName}: ${message}`)
    return null
  }
}

// The one pass every run shares. A template compiles again only when its
// source changed, the toolchain changed, or its output vanished; everything
// the cache vouches for is left alone, so the Go embed sees no churn.
async function reconcile(): Promise<boolean> {
  if (!fs.existsSync(templatesDir)) {
    logError(`templates directory not found: ${displayPath(templatesDir)}`)
    return false
  }

  fs.mkdirSync(outputDir, { recursive: true })

  const cache = loadCache(outputDir)
  cache.toolchain = toolchainDigest()
  const files = fs.readdirSync(templatesDir).filter((file) => file.endsWith('.tsx'))
  const plan = planRebuild(cache, files, templatesDir, outputDir, cache.toolchain)

  if (plan.removed.length > 0) {
    pruneOutputs(outputDir, plan.removed)
  }

  const nothingToDo = plan.stale.length === 0 && plan.removed.length === 0
  if (nothingToDo) {
    log(`  ${C.green}✓ ${plan.cached.length} template(s) current${C.reset}`)
    return true
  }

  log('building email templates...')
  logInfo('templates', displayPath(templatesDir))
  logInfo('output', displayPath(outputDir))
  if (plan.cached.length > 0) {
    logInfo('cached', `${plan.cached.length}/${files.length}`)
  }

  const startedAt = Date.now()
  let built = 0
  let failed = 0

  for (const file of plan.stale) {
    const name = await buildOne(file, cache.templates)
    if (name === null) {
      failed++
    } else {
      built++
    }
  }

  if (built > 0 || plan.removed.length > 0) {
    log(
      `${C.green}built ${built}/${plan.stale.length} template(s) → ${displayPath(outputDir)} in ${formatDuration(Date.now() - startedAt)}${C.reset}`
    )
  }

  saveCache(outputDir, cache)
  return failed === 0
}

const ok = await reconcile()
if (!ok) {
  logError('email template build failed — the webapp pass must not run against stale embeds')
  process.exit(1)
}
