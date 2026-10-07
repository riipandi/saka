import * as fs from 'node:fs'
import { createRequire } from 'node:module'
import * as path from 'node:path'
import { pathToFileURL } from 'node:url'
import type { ReactElement, ReactNode } from 'react'
import { render } from 'react-email'
import type { Plugin } from 'vite'
import { loadCache, planRebuild, pruneOutputs, saveCache, sha256 } from './email-cache.ts'
import type { TemplateCacheEntry } from './email-cache.ts'

type EmailTemplate = ((props: unknown) => ReactNode) & { TemplateProps?: unknown }

export interface PluginEmailOptions {
  /** Directory containing the React Email templates (default: "email/templates"). */
  templateDir?: string
  /** Directory the compiled Go templates are written to (default: "web/email"). */
  outputDir?: string
  /** Recompile on template change in dev (default: true). */
  watch?: boolean
  /** Debounce for template changes in ms (default: 300). */
  delay?: number
  log?: boolean
}

interface PluginEmailDefaults {
  templateDir: string
  outputDir: string
  watch: boolean
  delay: number
  log: boolean
}

const defaults: PluginEmailDefaults = {
  templateDir: 'email/templates',
  outputDir: 'web/email',
  watch: true,
  delay: 300,
  log: true
}

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

// Printed relative to the working directory, so a line names the path a
// developer would type; a path outside it stays absolute.
function displayPath(target: string): string {
  const rel = path.relative(process.cwd(), target)
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
  console.error(`${PREFIX} ${C.red}${msg}${C.reset}`)
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
      const pkg = JSON.parse(require.resolve(`${name}/package.json`) as unknown as string) as {
        version?: string
      }
      return `${name}@${pkg.version ?? 'unknown'}`
    } catch {
      return `${name}@unresolved`
    }
  })
  return sha256(parts.join('|'))
}

function getFirstExport(module: Record<string, unknown>): unknown {
  const keys = Object.keys(module)
  if (keys.length === 0) return undefined
  const firstKey = keys[0]
  if (firstKey === undefined) return undefined
  return module[firstKey]
}

// Template files are .tsx (JSX), so they must go through a TypeScript
// transform before we can import them in-process. tsx is already a
// devDependency; its ESM API registers the loader for the current thread.
async function importTemplate(file: string, cacheBust: number): Promise<Record<string, unknown>> {
  const { tsImport } = await import('tsx/esm/api')
  const mod = await tsImport(`${pathToFileURL(file).href}?t=${cacheBust}`, import.meta.url)
  return mod as Record<string, unknown>
}

async function buildTemplateFile(
  Component: (props: unknown) => ReactNode,
  templateProps: unknown,
  templateName: string,
  outputDir: string,
  isPlainText: boolean
): Promise<void> {
  const element = Component(templateProps) as ReactElement

  // `plainText` is a discriminated union, so it must be a literal, not a boolean.
  const rendered = isPlainText
    ? await render(element, { plainText: true })
    : await render(element, { plainText: false })

  // Normalize quotes
  const normalized = rendered.replace(/&quot;/g, '"')

  const goTemplate = `{{define "root"}}${normalized}{{end}}`
  const suffix = isPlainText ? '_text.tmpl' : '_html.tmpl'
  const templatePath = path.join(outputDir, `${templateName}${suffix}`)

  fs.writeFileSync(templatePath, goTemplate)
}

export default function VitePluginEmail(userOptions: PluginEmailOptions = {}): Plugin {
  if (process.env.VITEST) {
    return { name: 'vite-plugin-email' }
  }

  const opts: PluginEmailDefaults = { ...defaults, ...userOptions }

  // The templates and the compiled output are addressed from the directory
  // vite was started from, which is not vite's root when the SPA sits in a
  // subdirectory.
  const projectRoot = process.cwd()
  const toolchain = toolchainDigest()
  let command: 'serve' | 'build' = 'serve'
  let timer: ReturnType<typeof setTimeout> | null = null
  let isBuilding = false
  let hasPendingChanges = false
  let disposed = false

  // Compiles one template into its _html and _text pair and records the
  // pair in the cache. Returns the entry on success, null on failure
  // (already logged); a failed template writes no entry, so the next run
  // owes it again.
  async function buildOne(
    absTemplates: string,
    absOutput: string,
    file: string,
    cache: Record<string, TemplateCacheEntry>
  ): Promise<string | null> {
    const templateName = file.replace('.tsx', '')
    const start = Date.now()

    try {
      const importedModule = await importTemplate(path.join(absTemplates, file), Date.now())
      const Component = (importedModule.default ?? getFirstExport(importedModule)) as EmailTemplate

      if (!Component) {
        throw new Error('no component export found')
      }

      if (!Component.TemplateProps) {
        throw new Error('no TemplateProps export found')
      }

      await buildTemplateFile(Component, Component.TemplateProps, templateName, absOutput, false) // HTML
      await buildTemplateFile(Component, Component.TemplateProps, templateName, absOutput, true) // Text

      cache[file] = {
        hash: sha256(fs.readFileSync(path.join(absTemplates, file))),
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

  // The one pass every path shares — dev start, watch reconcile, and
  // `vite build`. A template compiles again only when its source changed,
  // the toolchain changed, or its output vanished; everything the cache
  // vouches for is left alone, so the Go embed sees no churn.
  async function reconcile(): Promise<boolean> {
    const absTemplates = path.resolve(projectRoot, opts.templateDir)
    const absOutput = path.resolve(projectRoot, opts.outputDir)

    if (!fs.existsSync(absTemplates)) {
      logError(`templates directory not found: ${displayPath(absTemplates)}`)
      return false
    }

    fs.mkdirSync(absOutput, { recursive: true })

    const cache = loadCache(absOutput)
    cache.toolchain = toolchain
    const files = fs.readdirSync(absTemplates).filter((file) => file.endsWith('.tsx'))
    const plan = planRebuild(cache, files, absTemplates, absOutput, toolchain)

    if (plan.removed.length > 0) {
      pruneOutputs(absOutput, plan.removed)
    }

    const nothingToDo = plan.stale.length === 0 && plan.removed.length === 0
    if (nothingToDo) {
      log(`  ${C.green}✓ ${plan.cached.length} template(s) current${C.reset}\n`)
      return true
    }

    log('building email templates...')
    logInfo('templates', displayPath(absTemplates))
    logInfo('output', displayPath(absOutput))
    if (plan.cached.length > 0) {
      logInfo('cached', `${plan.cached.length}/${files.length}`)
    }

    const startedAt = Date.now()
    let built = 0
    let failed = 0

    for (const file of plan.stale) {
      const name = await buildOne(absTemplates, absOutput, file, cache.templates)
      if (name === null) {
        failed++
      } else {
        built++
      }
    }

    if (built > 0 || plan.removed.length > 0) {
      log(
        `${C.green}built ${built}/${plan.stale.length} template(s) → ${displayPath(absOutput)} in ${formatDuration(Date.now() - startedAt)}${C.reset}\n`
      )
    }

    saveCache(absOutput, cache)
    return failed === 0
  }

  async function rebuild() {
    if (isBuilding) {
      hasPendingChanges = true
      return
    }

    isBuilding = true
    const ok = await reconcile()
    isBuilding = false

    if (!ok) return

    if (hasPendingChanges) {
      hasPendingChanges = false
      await rebuild()
    }
  }

  function schedule() {
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => {
      timer = null
      void rebuild()
    }, opts.delay)
  }

  function cleanup() {
    if (disposed) return
    disposed = true
    if (timer) clearTimeout(timer)
    timer = null
  }

  function shouldWatch(filePath: string): boolean {
    if (!filePath.endsWith('.tsx')) return false
    const rel = path.relative(path.resolve(projectRoot, opts.templateDir), filePath)
    return !rel.startsWith('..')
  }

  return {
    name: 'vite-plugin-email',
    configResolved(config) {
      command = config.command
    },
    configureServer(server) {
      void rebuild()

      if (!opts.watch) return

      // Vite watches its own root (web/); the templates live outside it.
      server.watcher.add(path.resolve(projectRoot, opts.templateDir))
      log(`watching ${displayPath(path.resolve(projectRoot, opts.templateDir))} for changes...`)

      // A change or an addition marks its template for the next pass; the
      // reconcile reads the sources fresh, so the debounce needs no names.
      const onFile = (file: string) => {
        if (disposed || !shouldWatch(file)) return
        schedule()
      }

      server.watcher.on('change', onFile)
      server.watcher.on('add', onFile)
      // A removed template cannot be rebuilt; the reconcile prunes the
      // compiled pairs the lock still names.
      server.watcher.on('unlink', onFile)

      server.httpServer?.once('close', cleanup)
    },
    closeBundle: {
      // Runs before plugin-golang's closeBundle so the Go binary always
      // embeds freshly compiled templates (web/embed.go: email/*.tmpl).
      sequential: true,
      order: 'pre',
      async handler() {
        if (command !== 'build') {
          // Dev server shutdown/restart — just drop pending timers.
          cleanup()
          return
        }

        const ok = await reconcile()
        if (!ok) {
          logError('email template build failed, aborting go binary build')
          process.exitCode = 1
          throw new Error('email template build failed')
        }
      }
    }
  }
}
