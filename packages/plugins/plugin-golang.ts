import { spawn, type ChildProcess } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import type { Plugin, ViteDevServer } from 'vite'

/** One build target: a Go binary variant identified by its mode name. */
export interface GoTargetOptions {
  /** Directory the binary is written to (e.g. "build/debug"). */
  outputDir: string
  /** Build tags; by convention includes the mode name ("debug"/"release"). */
  buildTags?: string[]
  buildFlags?: string[]
  ldflags?: string[]
}

export interface GoBuildOptions {
  /** Package to build (default: the plugin's packagePath). */
  packagePath?: string
  /** Binary file name inside the target's outputDir (default: packageName). */
  outputBin?: string
  /** Directory that must exist before a production build runs (default: "web/output"). */
  embedDir?: string
  /**
   * Targets built by `vite build`, in insertion order. Default: debug + release.
   * The dev server only ever builds and runs one of them — see devTarget.
   */
  targets?: Record<string, GoTargetOptions>
  /** Which target the dev server builds and runs (default: "debug"). */
  devTarget?: string
}

export interface PluginGolangOptions {
  packageName: string
  /** Go toolchain binary (default: "go"). */
  cmd?: string
  packagePath?: string
  /** Arguments the built binary is started with in dev (default: []). */
  binArgs?: string[]
  delay?: number
  killDelay?: number
  stopOnError?: boolean
  excludeDir?: string[]
  excludeRegex?: string[]
  extensions?: string[]
  log?: boolean
  build?: GoBuildOptions
}

interface ResolvedTarget {
  name: string
  outputDir: string
  /** Absolute-ish path of the binary, as passed to `go build -o`. */
  binPath: string
  args: string[]
  buildTags: string[]
  buildFlags: string[]
  ldflags: string[]
}

interface GoPluginDefaults {
  cmd: string
  packagePath: string
  binArgs: string[]
  delay: number
  killDelay: number
  stopOnError: boolean
  excludeDir: string[]
  excludeRegex: string[]
  extensions: string[]
  log: boolean
  build: Required<Pick<GoBuildOptions, 'outputBin' | 'embedDir' | 'devTarget'>> & {
    targets: Record<string, GoTargetOptions>
  }
}

const defaults: GoPluginDefaults = {
  cmd: 'go',
  packagePath: '.',
  binArgs: [],
  delay: 1000,
  killDelay: 300,
  stopOnError: false,
  excludeDir: ['.git', 'vendor', 'node_modules', 'web/output', 'temp', 'tmp', 'build', 'dist'],
  excludeRegex: ['_test\\.go$'],
  extensions: ['go', 'tmpl'],
  log: true,
  build: {
    outputBin: '',
    embedDir: 'web/output',
    devTarget: 'debug',
    targets: {
      debug: { outputDir: 'build/debug', buildTags: ['debug'] },
      release: {
        outputDir: 'build/release',
        buildTags: ['release'],
        buildFlags: ['-trimpath', '-buildmode=pie', '-buildvcs=false'],
        ldflags: ['-w -s -extldflags -static']
      }
    }
  }
}

const C = {
  reset: '\x1b[0m',
  dim: '\x1b[2m',
  green: '\x1b[32m',
  red: '\x1b[31m',
  cyan: '\x1b[36m'
} as const

const PREFIX = `${C.cyan}[go]${C.reset}`

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function formatFileSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
  return `${(bytes / 1024).toFixed(1)} KB`
}

// Printed relative to the working directory, so a line names the path a
// developer would type; a path outside it stays absolute.
function displayPath(target: string): string {
  const rel = path.relative(process.cwd(), target)
  if (rel === '') return '.'
  return rel.startsWith('..') ? target : rel
}

// One arg builder for both paths (dev rebuilds and production targets): flags
// before the package argument, so every target gets exactly the same shape.
function resolveTarget(
  name: string,
  target: GoTargetOptions,
  packagePath: string,
  outputBin: string
): ResolvedTarget {
  const buildTags = target.buildTags ?? []
  const buildFlags = target.buildFlags ?? []
  const ldflags = target.ldflags ?? []
  const binPath = `${target.outputDir}/${outputBin}`

  const args = ['build']
  if (buildTags.length > 0) args.push('-tags', buildTags.join(','))
  args.push(...buildFlags)
  if (ldflags.length > 0) args.push('-ldflags', ldflags.join(' '))
  args.push('-o', binPath, packagePath)

  return { name, outputDir: target.outputDir, binPath, args, buildTags, buildFlags, ldflags }
}

function formatBuildInfo(target: ResolvedTarget): Array<{ label: string; value: string }> {
  const tags = target.buildTags.length > 0 ? target.buildTags.join(', ') : 'none'

  const lines: Array<{ label: string; value: string }> = [
    { label: 'target', value: target.name },
    { label: 'tags', value: tags }
  ]

  if (target.buildFlags.length > 0) {
    lines.push({ label: 'flags', value: target.buildFlags.join(' ') })
  }

  for (const [index, flag] of target.ldflags.entries()) {
    lines.push({ label: `ldflags[${index}]`, value: flag })
  }

  lines.push({ label: 'output', value: displayPath(target.binPath) })

  return lines
}

interface GoBuildResult {
  code: number | null
  output: string
  duration: number
}

// CGO_ENABLED=0 because the release link flags pass -extldflags -static,
// which no platform links when cgo is on. It is also what .goreleaser.yaml
// sets, so both build paths produce the same binary. A dependency that has a
// cgo path and a pure-Go path picks the pure-Go one, which is what makes the
// static link work rather than a reason to drop the flag.
function runGoBuild(cmd: string, args: string[], cwd: string): Promise<GoBuildResult> {
  return new Promise((resolve) => {
    const startTime = Date.now()
    const buildProcess = spawn(cmd, args, {
      stdio: 'pipe',
      cwd,
      env: { ...process.env, CGO_ENABLED: '0' }
    })
    let output = ''

    buildProcess.stdout?.on('data', (data: Buffer) => {
      output += data.toString()
    })

    buildProcess.stderr?.on('data', (data: Buffer) => {
      output += data.toString()
    })

    buildProcess.on('close', (code) => {
      resolve({ code, output, duration: Date.now() - startTime })
    })
  })
}

export default function VitePlugin(userOptions: PluginGolangOptions): Plugin {
  if (process.env.VITEST || process.env.SKIP_GO_BUILD) {
    return { name: 'vite-plugin-go' }
  }

  const opts = {
    cmd: userOptions.cmd ?? defaults.cmd,
    packageName: userOptions.packageName,
    binArgs: userOptions.binArgs ?? defaults.binArgs,
    delay: userOptions.delay ?? defaults.delay,
    killDelay: userOptions.killDelay ?? defaults.killDelay,
    stopOnError: userOptions.stopOnError ?? defaults.stopOnError,
    excludeDir: userOptions.excludeDir ?? defaults.excludeDir,
    excludeRegex: userOptions.excludeRegex ?? defaults.excludeRegex,
    extensions: userOptions.extensions ?? defaults.extensions,
    log: userOptions.log ?? defaults.log
  }

  // Precedence: build.packagePath > packagePath > "." — the defaults must not
  // shadow an explicit top-level packagePath.
  const packagePath =
    userOptions.build?.packagePath ?? userOptions.packagePath ?? defaults.packagePath
  const outputBin = userOptions.build?.outputBin ?? userOptions.packageName
  const embedDir = userOptions.build?.embedDir ?? defaults.build.embedDir
  const devTargetName = userOptions.build?.devTarget ?? defaults.build.devTarget
  const targetOptions = userOptions.build?.targets ?? defaults.build.targets

  const targets = Object.fromEntries(
    Object.entries(targetOptions).map(([name, target]) => [
      name,
      resolveTarget(name, target, packagePath, outputBin)
    ])
  ) as Record<string, ResolvedTarget>

  // The dev server runs exactly one target; `vite build` runs all of them.
  // An unknown devTarget falls back to the first configured target.
  const devName = devTargetName in targets ? devTargetName : (Object.keys(targets)[0] ?? 'debug')
  const devTarget: ResolvedTarget = targets[devName] ?? {
    name: devName,
    outputDir: 'build/debug',
    binPath: `build/debug/${outputBin}`,
    args: ['build', '-o', `build/debug/${outputBin}`, packagePath],
    buildTags: [devName],
    buildFlags: [],
    ldflags: []
  }

  const excludePatterns = [
    ...opts.excludeDir.map((d) => new RegExp(`[\\/]${path.normalize(d)}[\\/]`)),
    ...opts.excludeRegex.map((r) => new RegExp(r))
  ]

  // The Go module and the config file live in the directory vite was started
  // from, which is not vite's root when the SPA sits in a subdirectory.
  const projectRoot = process.cwd()
  let command: 'serve' | 'build' = 'serve'
  let goProcess: ChildProcess | null = null
  let buildTimer: ReturnType<typeof setTimeout> | null = null
  let isBuilding = false
  let hasPendingChanges = false
  let onSignal: (() => void) | null = null
  let disposed = false

  function log(msg: string) {
    if (opts.log) console.log(`${PREFIX} ${msg}`)
  }

  function logInfo(label: string, value: string, width?: number) {
    // Default gutter fits the longest common label; callers may pass an exact width.
    const w = width ?? (label.endsWith(':') ? 10 : 9)
    log(`${C.dim}${label.padEnd(w)}${C.reset}${value}`)
  }

  function logOutput(output: string) {
    for (const line of output.split('\n')) {
      if (line.trim()) console.error(`${PREFIX} ${C.red}${line}${C.reset}`)
    }
  }

  function killGo() {
    if (!goProcess) return
    const proc = goProcess
    goProcess = null
    try {
      process.kill(-proc.pid!, 'SIGTERM')
    } catch {
      proc.kill('SIGTERM')
    }
  }

  function dispose() {
    if (disposed) return
    disposed = true
    if (buildTimer) clearTimeout(buildTimer)
    killGo()
    if (onSignal) {
      process.removeListener('SIGINT', onSignal)
      process.removeListener('SIGTERM', onSignal)
      onSignal = null
    }
    log('stopped')
  }

  function startBinary() {
    const binPath = path.resolve(projectRoot, devTarget.binPath)
    const proc = spawn(binPath, opts.binArgs, {
      cwd: projectRoot,
      stdio: 'inherit',
      detached: true
    })
    goProcess = proc

    proc.on('error', (err) => {
      log(`failed to start binary: ${err.message}`)
      goProcess = null
    })

    proc.on('exit', (code, signal) => {
      // goProcess was already cleared by killGo() → expected shutdown, no log.
      if (goProcess !== proc) return
      goProcess = null
      if (!disposed) log(`binary exited unexpectedly (${signal ?? `exit code ${code}`})`)
    })

    if (proc.pid) log(`started (pid ${proc.pid})`)
  }

  async function runBuild(target: ResolvedTarget, stage: 'initial' | 'rebuild'): Promise<boolean> {
    isBuilding = true
    log(
      stage === 'rebuild' ? `rebuilding (${target.name})...` : `building ${target.name} binary...`
    )

    const { code, output, duration } = await runGoBuild(opts.cmd, target.args, projectRoot)
    isBuilding = false

    if (code !== 0) {
      log(`${C.red}build failed (exit code ${code}) in ${formatDuration(duration)}${C.reset}`)
      logOutput(output)
      return false
    }

    log(
      `${C.green}${stage === 'rebuild' ? 'rebuilt' : 'built'} in ${formatDuration(duration)}${C.reset}`
    )
    return true
  }

  function buildAndStart() {
    if (isBuilding || disposed) return
    if (buildTimer) clearTimeout(buildTimer)

    buildTimer = setTimeout(() => {
      void runBuild(devTarget, 'rebuild').then((ok) => {
        buildTimer = null

        if (!ok && opts.stopOnError) killGo()

        if (hasPendingChanges) {
          hasPendingChanges = false
          buildAndStart()
          return
        }

        if (ok) {
          setTimeout(() => {
            if (disposed) return
            killGo()
            startBinary()
          }, opts.killDelay)
        }
      })
    }, opts.delay)
  }

  async function initialBuild() {
    const ok = await runBuild(devTarget, 'initial')
    if (disposed || !ok) return
    startBinary()
  }

  function shouldWatch(filePath: string): boolean {
    const ext = path.extname(filePath).slice(1)
    if (!opts.extensions.includes(ext)) return false
    if (excludePatterns.some((re) => re.test(filePath))) return false
    return true
  }

  return {
    name: 'vite-plugin-go',
    configResolved(config) {
      command = config.command
    },
    configureServer(_server: ViteDevServer) {
      // Vite watches its own root (web/); the Go sources and the module live
      // above it, so the tree has to be added explicitly.
      _server.watcher.add(projectRoot)
      void initialBuild()

      _server.watcher.on('change', (file: string) => {
        if (disposed) return
        if (!shouldWatch(file)) return

        if (isBuilding) {
          hasPendingChanges = true
          return
        }

        buildAndStart()
      })

      onSignal = () => {
        dispose()
        process.exit(0)
      }
      process.on('SIGINT', onSignal)
      process.on('SIGTERM', onSignal)
    },
    closeBundle: {
      sequential: true,
      order: 'post',
      async handler() {
        // Vite also fires closeBundle when the dev server shuts down or
        // restarts — only run the production builds for `vite build`.
        if (command !== 'build') {
          dispose()
          return
        }

        const embedPath = path.resolve(projectRoot, embedDir)
        if (!fs.existsSync(embedPath)) {
          log(`embed directory "${displayPath(embedPath)}" not found, skipping go builds`)
          process.exitCode = 1
          return
        }

        for (const target of Object.values(targets)) {
          fs.mkdirSync(path.resolve(projectRoot, target.outputDir), { recursive: true })

          log(`building binary (${target.name})...`)

          const infoLines = formatBuildInfo(target)
          const gutter = Math.max(...infoLines.map((line) => line.label.length)) + 1
          for (const line of infoLines) {
            logInfo(line.label, line.value, gutter)
          }

          const { code, output, duration } = await runGoBuild(opts.cmd, target.args, projectRoot)

          if (code !== 0) {
            log(`${C.red}build failed (exit code ${code}) in ${formatDuration(duration)}${C.reset}`)
            logOutput(output)
            process.exitCode = 1
            return
          }

          let size = ''
          try {
            size = ` (${formatFileSize(fs.statSync(path.resolve(projectRoot, target.binPath)).size)})`
          } catch {
            // binary missing after a successful build is highly unlikely; keep summary short
          }

          log(
            `${C.green}binary built → ${displayPath(target.binPath)}${size} in ${formatDuration(duration)}${C.reset}\n`
          )
        }
      }
    }
  }
}
