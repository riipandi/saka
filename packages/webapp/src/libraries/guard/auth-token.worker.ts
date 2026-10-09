import * as Comlink from 'comlink'
import { createAuthEngine } from './auth-engine'

/**
 * Auth token worker — hosts the {@link createAuthEngine auth engine} off the
 * main thread. Refresh orchestration (single-flight, proactive timer) runs
 * here. Workers have no cookie access, so the engine reports every token
 * custody change to its listener and the main thread persists the pair (see
 * `auth-cookies.ts`).
 */
Comlink.expose(createAuthEngine())
