// The storage fixture: one small object in its own bucket, uploaded through
// the tus surface the browser uses, so the storage-get scenario serves real
// bytes through the real mount. Every step is best-effort — a target without
// the storage engine live answers the scenario with a skip, not a failure.

import http from 'k6/http'
import { Counter } from 'k6/metrics'
import { BASE_URL, STORAGE_BODY, STORAGE_BUCKET, STORAGE_KEY } from './config.mjs'
import { callRPC, procedures } from './rpc.mjs'

// fixtureSkipped counts the scenario iterations a missing fixture spared —
// the run's summary shows the count, so a skip is visible, not silent.
export const fixtureSkipped = new Counter('loadtest_storage_skipped')

const tusBase = `${BASE_URL}/api/uploads`
const tusVersion = '1.0.0'

// metadata renders the Upload-Metadata header: comma-separated pairs, the
// value base64 — the protocol's own encoding, padding optional.
const b64alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'

// b64 is base64 by hand: k6's runtime exposes no btoa, and the protocol's
// pairs are short strings, so a table walk is honest and dependency-free.
function b64(input) {
  const bytes = []
  for (const ch of input) bytes.push(ch.codePointAt(0))
  let out = ''
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i],
      b1 = bytes[i + 1],
      b2 = bytes[i + 2]
    out += b64alphabet[b0 >> 2]
    out += b64alphabet[((b0 & 3) << 4) | ((b1 ?? 0) >> 4)]
    out += b1 === undefined ? '=' : b64alphabet[((b1 & 15) << 2) | ((b2 ?? 0) >> 6)]
    out += b2 === undefined ? '=' : b64alphabet[b2 & 63]
  }
  return out
}

function metadata(pairs) {
  return Object.entries(pairs)
    .map(([name, value]) => `${name} ${b64(value)}`)
    .join(',')
}

export function setupStorage(token) {
  const fixture = {
    available: false,
    url: ''
  }

  // The bucket is idempotent: a name the server already holds fails the
  // create with `failed_precondition` (or an `already exists` refusal) —
  // either way the fixture proceeds to the upload.
  const created = callRPC(
    'storage-fixture',
    procedures.createBucket,
    { name: STORAGE_BUCKET },
    token
  )
  if (!created.ok && !/exist|precondition/.test(created.code)) {
    return fixture
  }

  // Creation-with-upload: the POST declares the length and carries the
  // first (only) chunk, so one request stages and stores the whole file.
  const res = http.post(tusBase, STORAGE_BODY, {
    headers: {
      Authorization: `Bearer ${token}`,
      'Tus-Resumable': tusVersion,
      'Upload-Length': String(STORAGE_BODY.length),
      'Upload-Metadata': metadata({
        bucket: STORAGE_BUCKET,
        key: STORAGE_KEY,
        filename: 'ping.bin',
        filetype: 'application/octet-stream'
      })
    }
  })
  if (res.status !== 201 && res.status !== 200) {
    return fixture
  }

  fixture.available = true
  fixture.url = `${BASE_URL}/storage/${STORAGE_BUCKET}/${STORAGE_KEY}`
  return fixture
}
