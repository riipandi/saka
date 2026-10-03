//go:build debug

// The tus file-upload probe page's body, debug build only: the transport's
// devtool surface serves it at /debug/file-upload, and a release build
// carries none of this (its counterpart refuses the path with the 404
// envelope).

package web

// UploadProbePages maps the devtool's upload routes to their bodies.
var UploadProbePages = map[string]string{
	"/debug/file-upload": fileUploadProbePage,
}

// fileUploadProbePage drives the tus endpoint with tus-js-client's UMD
// bundle from the CDN: the page picks a file, names the bucket and key,
// and streams it through POST/HEAD/PATCH while logging every offset the
// server reports. The token is the access token a sign-in answers with.
const fileUploadProbePage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Saka file-upload probe</title>
<style>` + webauthnProbeStyle + `</style>
</head>
<body>
<h2>File upload (tus)</h2>
<p>Development instrument — drives <code>/api/uploads</code> with tus-js-client.
The token is the access token a sign-in answers with:
<code>scripts/curl-rpc.sh http://localhost:3080/rpc/saka.authn.v1.AuthService/SignIn '{"identity":"admin","password":"…"}'</code></p>
<field><input id="token" size="80" placeholder="access token"></field>
<field><input id="bucket" size="30" value="default"></field>
<field><input id="key" size="40" placeholder="object key, e.g. logo.png"></field>
<field><input id="file" type="file"></field>
<button id="upload" data-testid="upload">Upload</button>
<button id="pause" data-testid="pause" disabled>Pause</button>
<button id="resume" data-testid="resume" disabled>Resume</button>
<button id="terminate" data-testid="terminate">Terminate</button>
<button id="status">Probe offset (HEAD)</button>
<div id="log" data-testid="log"></div>
<script src="https://cdn.jsdelivr.net/npm/tus-js-client@latest/dist/tus.min.js"></script>
<script>
const log = (kind, message) => {
  const line = document.createElement('div');
  line.className = kind;
  line.textContent = message;
  document.getElementById('log').appendChild(line);
};

let upload = null;

const startUpload = () => {
  const file = document.getElementById('file').files[0];
  const bucket = document.getElementById('bucket').value.trim();
  const key = document.getElementById('key').value.trim();
  const token = document.getElementById('token').value.trim();
  if (!file || !bucket || !key) { log('err', 'pick a file and name bucket and key'); return; }
  const metadata = { bucket, key, filename: file.name, filetype: file.type || 'application/octet-stream' };
  upload = new tus.Upload(file, {
    endpoint: '/api/uploads/' + bucket + '/' + key,
    metadata,
    headers: token ? { Authorization: 'Bearer ' + token } : {},
    retryDelays: null,
    onProgress: (sent, total) => log('ok', 'progress: ' + sent + ' / ' + total),
    onSuccess: () => {
      log('ok', 'done: ' + upload.url);
      log('ok', 'served at /storage/' + bucket + '/' + key);
      document.getElementById('pause').disabled = true;
      document.getElementById('resume').disabled = true;
    },
    onError: (error) => log('err', 'upload: ' + error.message),
  });
  upload.start();
  document.getElementById('pause').disabled = false;
};

document.getElementById('upload').onclick = startUpload;
document.getElementById('pause').onclick = () => {
  if (!upload) return;
  upload.abort();
  log('ok', 'paused at the last offset the server confirmed');
  document.getElementById('pause').disabled = true;
  document.getElementById('resume').disabled = false;
};
document.getElementById('resume').onclick = () => {
  if (!upload) return;
  log('ok', 'resuming from the server-reported offset');
  upload.start();
  document.getElementById('resume').disabled = true;
  document.getElementById('pause').disabled = false;
};
document.getElementById('terminate').onclick = async () => {
  const bucket = document.getElementById('bucket').value.trim();
  const key = document.getElementById('key').value.trim();
  const token = document.getElementById('token').value.trim();
  const response = await fetch('/api/uploads/' + bucket + '/' + key, {
    method: 'DELETE',
    headers: token ? { Authorization: 'Bearer ' + token } : {},
  });
  log(response.ok ? 'ok' : 'err', 'DELETE ' + response.status);
};

document.getElementById('status').onclick = async () => {
  const bucket = document.getElementById('bucket').value.trim();
  const key = document.getElementById('key').value.trim();
  const token = document.getElementById('token').value.trim();
  const response = await fetch('/api/uploads/' + bucket + '/' + key, {
    method: 'HEAD',
    headers: token ? { Authorization: 'Bearer ' + token } : {},
  });
  log(response.ok ? 'ok' : 'err', 'HEAD ' + response.status + ' offset=' + response.headers.get('Upload-Offset'));
};
</script>
</body>
</html>`
