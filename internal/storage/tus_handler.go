package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// TusVersion is the protocol version the handler speaks. Every request and
// every answer carries it: a client that names a version the server does
// not support is refused before its bytes travel, and the version header is
// what the answer names so a client can verify the pair.
const TusVersion = "1.0.0"

// TusMedia is the media type a PATCH body carries — the one the protocol
// allows, so a body of any other kind is refused before a byte lands.
const TusMedia = "application/offset+octet-stream"

// TusMaxMetadataBytes is the Upload-Metadata header the handler accepts.
// The pairs it carries name the bucket, the key, and the file's own record;
// a header longer than this is not a file's metadata, it is abuse.
const TusMaxMetadataBytes = 4096

// TusHandler serves the resumable-upload protocol on /api/uploads: the
// creation (POST), the offset probe (HEAD), the chunk append (PATCH), and
// the termination (DELETE), plus the OPTIONS discovery every client starts
// with. The protocol's own headers carry the state — the offset is the
// staging file's size, the length is the manifest metadata's record — so
// there is no second book beside the engine's.
//
// Authentication is the transport's bearer middleware; the handler reads
// nothing about the caller. Authorization is the creation-time check the
// bucket's existence and limits make; any authenticated account may upload
// into any bucket until a per-bucket model exists.
type TusHandler struct {
	manager *Manager
	log     *slog.Logger

	// locks serializes the appends per upload: the offset claim a PATCH
	// makes is only the whole concurrency story if two chunks for one file
	// cannot interleave between the check and the write. The map grows by
	// upload and never shrinks — a session's entry is a few dozen bytes,
	// and a deployment's live sessions are not a legion.
	locks sync.Map
}

// NewTusHandler builds the protocol handler over the engine.
func NewTusHandler(manager *Manager, log *slog.Logger) *TusHandler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &TusHandler{manager: manager, log: log}
}

// ServeHTTP routes the protocol's methods. The version handshake wraps
// every answer and every refusal, so a client learns the server's version
// even from a failure.
func (h *TusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Tus-Resumable", TusVersion)
	if r.URL.Path == "/api/uploads" {
		switch r.Method {
		case http.MethodOptions:
			h.options(w)
		case http.MethodPost:
			h.create(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	ref := strings.TrimPrefix(r.URL.Path, "/api/uploads/")
	switch r.Method {
	case http.MethodOptions:
		h.options(w)
	case http.MethodHead:
		h.head(w, r, ref)
	case http.MethodPatch:
		h.patch(w, r, ref)
	case http.MethodDelete:
		h.terminate(w, r, ref)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// options answers the discovery: the version the server speaks and the
// extensions it implements. No body.
func (h *TusHandler) options(w http.ResponseWriter) {
	w.Header().Set("Tus-Version", TusVersion)
	w.Header().Set("Tus-Extension", "creation-with-upload,termination,expiration")
	w.WriteHeader(http.StatusNoContent)
}

// create opens a session. The target names itself in the Upload-Metadata —
// the bucket and the key are the pair the Location answers — and the
// declared length and content type ride beside them. A body on the
// creation request is creation-with-upload: the first chunk, appended at
// offset zero before the answer carries its new offset.
func (h *TusHandler) create(w http.ResponseWriter, r *http.Request) {
	metadata, err := parseUploadMetadata(r.Header.Get("Upload-Metadata"))
	if err != nil {
		tusFail(w, http.StatusBadRequest, "malformed Upload-Metadata")
		return
	}
	bucket := metadata["bucket"]
	key := metadata["key"]
	if bucket == "" || key == "" {
		tusFail(w, http.StatusBadRequest, "the Upload-Metadata must name a bucket and a key")
		return
	}

	length, err := uploadLength(r.Header.Get("Upload-Length"))
	if err != nil {
		tusFail(w, http.StatusBadRequest, err.Error())
		return
	}

	bucketRow, err := h.manager.Bucket(r.Context(), bucket)
	if errors.Is(err, ErrNotFound) {
		tusFail(w, http.StatusNotFound, "no such bucket")
		return
	}
	if err != nil {
		tusFail(w, http.StatusInternalServerError, "the upload could not begin")
		return
	}
	if bucketRow.FileSizeLimit != nil && length > *bucketRow.FileSizeLimit {
		tusFail(w, http.StatusRequestEntityTooLarge, "the upload exceeds the bucket's size limit")
		return
	}
	if fileType := metadata["filetype"]; len(bucketRow.AllowedMimeTypes) > 0 && fileType != "" {
		if !allowedMime(bucketRow.AllowedMimeTypes, fileType) {
			tusFail(w, http.StatusUnsupportedMediaType, "the content type is not allowed in this bucket")
			return
		}
	}

	// The file's own record rides the metadata: the content type is what
	// the stored object is served with, and the session's declared length
	// is what completion and expiry read.
	store := map[string]any{
		"content_type": "application/octet-stream",
		"tus_upload":   "true",
	}
	for _, name := range []string{"filename", "filetype"} {
		if value := metadata[name]; value != "" {
			store[name] = value
		}
	}
	if fileType := metadata["filetype"]; fileType != "" {
		store["content_type"] = fileType
	}

	if err := h.manager.TusBegin(r.Context(), bucket, key, length, store); err != nil {
		h.refuse(w, r, "the upload could not begin", err)
		return
	}

	offset := int64(0)
	if length > 0 && (r.ContentLength > 0 || r.Header.Get("Transfer-Encoding") == "chunked") {
		first, appendErr := h.appendChunk(r.Context(), bucket, key, 0, r.Body)
		if appendErr != nil {
			h.refuse(w, r, "the first chunk could not be written", appendErr)
			return
		}
		offset = first
	}

	w.Header().Set("Location", "/api/uploads/"+bucket+"/"+key)
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	w.WriteHeader(http.StatusCreated)
}

// head answers the offset a resuming client continues from. No body: the
// headers are the state, and Cache-Control keeps any intermediary from
// answering a later HEAD with an older one.
func (h *TusHandler) head(w http.ResponseWriter, r *http.Request, ref string) {
	bucket, key, err := SplitRef(ref)
	if err != nil {
		tusFail(w, http.StatusNotFound, "no such upload")
		return
	}
	offset, err := h.manager.TusOffset(r.Context(), bucket, key)
	if errors.Is(err, ErrNotFound) {
		tusFail(w, http.StatusNotFound, "no such upload")
		return
	}
	if err != nil {
		tusFail(w, http.StatusInternalServerError, "the offset could not be read")
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// patch appends one chunk at the offset the request claims. A mismatched
// claim is a conflict, a body of the wrong kind is a refusal, and the
// answer carries the offset the file has reached — which, when it equals
// the declared length, is also the completion: the queue takes over.
func (h *TusHandler) patch(w http.ResponseWriter, r *http.Request, ref string) {
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != TusMedia {
		tusFail(w, http.StatusUnsupportedMediaType, "the chunk must be "+TusMedia)
		return
	}
	claimed, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || claimed < 0 {
		tusFail(w, http.StatusBadRequest, "the Upload-Offset header must name the file's current offset")
		return
	}

	bucket, key, splitErr := SplitRef(ref)
	if splitErr != nil {
		tusFail(w, http.StatusNotFound, "no such upload")
		return
	}

	// The per-upload lock makes the claim-then-append pair one step: two
	// chunks for one file serialize, so neither can pass the offset check
	// against a size the other is about to change.
	unlock := h.lockUpload(bucket + "/" + key)
	defer unlock()

	offset, err := h.appendChunk(r.Context(), bucket, key, claimed, r.Body)
	if errors.Is(err, ErrOffsetMismatch) {
		tusFail(w, http.StatusConflict, "the Upload-Offset does not match the upload's current offset")
		return
	}
	if err != nil {
		h.refuse(w, r, "the chunk could not be written", err)
		return
	}

	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	w.WriteHeader(http.StatusNoContent)
}

// terminate ends a session without storing anything.
func (h *TusHandler) terminate(w http.ResponseWriter, r *http.Request, ref string) {
	bucket, key, err := SplitRef(ref)
	if err != nil {
		tusFail(w, http.StatusNotFound, "no such upload")
		return
	}
	if err := h.manager.TusDiscard(r.Context(), bucket, key); err != nil {
		if errors.Is(err, ErrNotFound) {
			tusFail(w, http.StatusNotFound, "no such upload")
			return
		}
		h.refuse(w, r, "the upload could not be terminated", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// appendChunk writes one chunk through the engine and completes the
// session when the file has reached its declared length. The completion's
// enqueue failure is logged, not returned: the answer the client waits on
// is the offset, and the staging row the sweep reads is the durable
// recovery — a lost enqueue is a delayed upload, not a failed one.
func (h *TusHandler) appendChunk(ctx context.Context, bucket, key string, claimed int64, body io.Reader) (int64, error) {
	offset, err := h.manager.TusAppend(ctx, bucket, key, claimed, body)
	if err != nil {
		return offset, err
	}
	if h.isComplete(ctx, bucket, key, offset) {
		if completeErr := h.manager.TusComplete(ctx, bucket, key); completeErr != nil && !errors.Is(completeErr, ErrLengthMismatch) {
			h.log.ErrorContext(ctx, "tus: enqueue upload", "ref", bucket+"/"+key, "err", completeErr)
		}
	}
	return offset, nil
}

// isComplete reads the session's declared length and answers whether the
// offset has reached it. A session without a declared length never
// completes by offset alone — the client owes the file a termination or an
// expiry, and the engine's own length check on completion still guards it.
func (h *TusHandler) isComplete(ctx context.Context, bucket, key string, offset int64) bool {
	manifest, err := h.manager.Manifest(ctx, bucket, key)
	if err != nil {
		return false
	}
	length := tusLength(manifest.Metadata)
	return length >= 0 && offset >= length
}

// lockUpload pins one upload's appends behind a mutex for the request's
// span.
func (h *TusHandler) lockUpload(ref string) func() {
	entry, _ := h.locks.LoadOrStore(ref, &sync.Mutex{})
	mu, _ := entry.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// refuse answers a failure the client cannot fix by resending: the log
// names the cause, the answer names nothing a caller could aim at.
func (h *TusHandler) refuse(w http.ResponseWriter, r *http.Request, message string, err error) {
	h.log.ErrorContext(r.Context(), "tus: request failed", "path", r.URL.Path, "cause", err)
	tusFail(w, http.StatusInternalServerError, message)
}

// tusFail answers a protocol refusal: a plain text body the tus clients
// read, not the JSON envelope the API's own routes carry.
func tusFail(w http.ResponseWriter, code int, message string) {
	http.Error(w, message, code)
}

// uploadLength reads the declared size a creation carries. The protocol
// requires the header on creation; a missing or malformed one is the
// client's error to fix.
func uploadLength(raw string) (int64, error) {
	if raw == "" {
		return 0, errors.New("the Upload-Length header is required")
	}
	length, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || length < 0 {
		return 0, errors.New("the Upload-Length header must be a non-negative integer")
	}
	return length, nil
}

// parseUploadMetadata reads the comma-separated `key base64value` pairs a
// creation carries into its map. A pair whose value is not valid base64,
// or a header longer than the bound, is a malformed creation.
func parseUploadMetadata(raw string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if len(raw) > TusMaxMetadataBytes {
		return nil, errors.New("the Upload-Metadata header is too long")
	}
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), " ", 2)
		name := parts[0]
		if name == "" {
			return nil, errors.New("the Upload-Metadata carries a pair with no name")
		}
		value := ""
		if len(parts) == 2 && parts[1] != "" {
			// The protocol's examples ride standard base64, but clients
			// disagree on the padding; both readings are tried before the
			// pair is called malformed.
			decoded, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
			}
			if err != nil {
				return nil, fmt.Errorf("the Upload-Metadata pair %q is not valid base64", name)
			}
			value = string(decoded)
		}
		out[name] = value
	}
	return out, nil
}

// allowedMime answers whether the declared type is one the bucket accepts.
// A wildcard's absence is deliberate: the list is exact, the way the
// bucket's limits are.
func allowedMime(allowed []string, declared string) bool {
	base, _, err := mime.ParseMediaType(declared)
	if err != nil {
		base = declared
	}
	for _, candidate := range allowed {
		if strings.EqualFold(candidate, base) {
			return true
		}
	}
	return false
}
