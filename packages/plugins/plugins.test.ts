import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import { describe, expect, test } from 'vitest'
import { loadCache, planRebuild, pruneOutputs, saveCache, sha256 } from './email-cache.ts'
import emailPlugin from './plugin-email.ts'
import golangPlugin from './plugin-golang.ts'

describe('vite plugins', () => {
  test('email plugin exposes its name', () => {
    expect(emailPlugin().name).toBe('vite-plugin-email')
  })

  test('go plugin exposes its name', () => {
    expect(golangPlugin({ packageName: 'saka' }).name).toBe('vite-plugin-go')
  })
})

describe('email build cache', () => {
  const files = {
    sources: ['a.tsx', 'b.tsx'] as string[],
    outputs: ['a_html.tmpl', 'a_text.tmpl', 'b_html.tmpl', 'b_text.tmpl'] as string[]
  }

  function sandbox() {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'email-cache-'))
    const templates = path.join(dir, 'templates')
    const output = path.join(dir, 'output')
    fs.mkdirSync(templates)
    fs.mkdirSync(output)
    return { dir, templates, output }
  }

  function writeSources(dir: string, body = '') {
    fs.writeFileSync(path.join(dir, 'a.tsx'), `export default () => null${body}`)
    fs.writeFileSync(path.join(dir, 'b.tsx'), 'export default () => null')
  }

  function writeOutputs(dir: string, names: string[]) {
    for (const name of names)
      fs.writeFileSync(path.join(dir, name), `{{define "root"}}${name}{{end}}`)
  }

  function filledCache(templates: string, output: string, toolchain = 't1') {
    const cache = loadCache(output)
    cache.toolchain = toolchain
    for (const file of files.sources) {
      cache.templates[file] = {
        hash: sha256(fs.readFileSync(path.join(templates, file))),
        outputs: [file.replace('.tsx', '_html.tmpl'), file.replace('.tsx', '_text.tmpl')],
        builtAt: 0
      }
    }
    saveCache(output, cache)
    return cache
  }

  test('an empty cache builds everything once, then nothing', () => {
    const { templates, output } = sandbox()
    writeSources(templates)
    writeOutputs(output, files.outputs)

    const first = planRebuild(loadCache(output), files.sources, templates, output, 't1')
    expect(first.stale).toEqual(['a.tsx', 'b.tsx'])

    filledCache(templates, output)
    const second = planRebuild(loadCache(output), files.sources, templates, output, 't1')
    expect(second.stale).toEqual([])
    expect(second.cached).toEqual(['a.tsx', 'b.tsx'])
  })

  test('a changed source recompiles only its own template', () => {
    const { templates, output } = sandbox()
    writeSources(templates)
    writeOutputs(output, files.outputs)
    filledCache(templates, output)

    fs.writeFileSync(path.join(templates, 'a.tsx'), 'export default () => <p>changed</p>')

    const plan = planRebuild(loadCache(output), files.sources, templates, output, 't1')
    expect(plan.stale).toEqual(['a.tsx'])
    expect(plan.cached).toEqual(['b.tsx'])
  })

  test('a removed source prunes its compiled pair', () => {
    const { templates, output } = sandbox()
    writeSources(templates)
    writeOutputs(output, files.outputs)
    filledCache(templates, output)

    fs.rmSync(path.join(templates, 'a.tsx'))

    const plan = planRebuild(
      loadCache(output),
      files.sources.filter((f) => f !== 'a.tsx'),
      templates,
      output,
      't1'
    )
    expect(plan.removed).toEqual(['a_html.tmpl', 'a_text.tmpl'])

    pruneOutputs(output, plan.removed)
    expect(fs.existsSync(path.join(output, 'a_html.tmpl'))).toBe(false)
    expect(fs.existsSync(path.join(output, 'b_html.tmpl'))).toBe(true)
  })

  test('a toolchain bump obsoletes the whole cache', () => {
    const { templates, output } = sandbox()
    writeSources(templates)
    writeOutputs(output, files.outputs)
    filledCache(templates, output)

    const plan = planRebuild(loadCache(output), files.sources, templates, output, 't2')
    expect(plan.stale).toEqual(files.sources)
  })

  test('a vanished output recompiles despite a matching hash', () => {
    const { templates, output } = sandbox()
    writeSources(templates)
    writeOutputs(output, files.outputs)
    filledCache(templates, output)

    fs.rmSync(path.join(output, 'a_text.tmpl'))

    const plan = planRebuild(loadCache(output), files.sources, templates, output, 't1')
    expect(plan.stale).toEqual(['a.tsx'])
  })

  test('the lock round-trips and never leaves a tmp file', () => {
    const { templates, output } = sandbox()
    writeSources(templates)
    filledCache(templates, output)

    const reloaded = loadCache(output)
    expect(Object.keys(reloaded.templates)).toEqual(['a.tsx', 'b.tsx'])
    expect(fs.readdirSync(output).filter((f) => f.endsWith('.tmp'))).toEqual([])
    // An unreadable lock is an empty cache.
    expect(loadCache(path.join(output, 'no-such-dir')).templates).toEqual({})
  })
})
