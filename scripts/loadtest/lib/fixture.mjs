// The storage fixture: one small object in its own bucket, uploaded through
// the tus surface the browser uses, so the storage-get scenario serves real
// bytes through the real mount. Every step is best-effort — a target without
// the storage engine live answers the scenario with a skip, not a failure.

import { Counter } from 'k6/metrics';
import http from 'k6/http';

import { BASE_URL, STORAGE_BODY, STORAGE_BUCKET, STORAGE_KEY } from './config.mjs';
import { procedures, callRPC } from './rpc.mjs';

// fixtureSkipped counts the scenario iterations a missing fixture spared —
// the run's summary shows the count, so a skip is visible, not silent.
export const fixtureSkipped = new Counter('loadtest_storage_skipped');

const tusBase = `${BASE_URL}/api/uploads`;
const tusVersion = '1.0.0';

// metadata renders the Upload-Metadata header: comma-separated pairs, the
// value base64 — the protocol's own encoding, padding optional.
function metadata(pairs) {
	return Object.entries(pairs)
		.map(([name, value]) => `${name} ${btoa(value)}`)
		.join(',');
}

export function setupStorage(token) {
	const fixture = {
		available: false,
		url: '',
	};

	// The bucket is idempotent: a name the server already holds fails the
	// create with `failed_precondition` (or an `already exists` refusal) —
	// either way the fixture proceeds to the upload.
	const created = callRPC('storage-fixture', procedures.createBucket, { name: STORAGE_BUCKET }, token);
	if (!created.ok && !/exist|precondition/.test(created.code)) {
		return fixture;
	}

	// Creation-with-upload: the POST declares the length and carries the
	// first (only) chunk, so one request stages and stores the whole file.
	const res = http.post(`${tusBase}`, STORAGE_BODY, {
		headers: {
			Authorization: `Bearer ${token}`,
			'Tus-Resumable': tusVersion,
			'Upload-Length': String(STORAGE_BODY.length),
			'Upload-Metadata': metadata({
				bucket: STORAGE_BUCKET,
				key: STORAGE_KEY,
				filename: 'ping.bin',
				filetype: 'application/octet-stream',
			}),
		},
	});
	if (res.status !== 201 && res.status !== 200) {
		return fixture;
	}

	fixture.available = true;
	fixture.url = `${BASE_URL}/storage/${STORAGE_BUCKET}/${STORAGE_KEY}`;
	return fixture;
}
