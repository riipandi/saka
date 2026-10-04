package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"uuid"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/storage"
	"github.com/riipandi/saka/internal/audit"
)

// The bundled default picture answers for every account that has none. It is
// a frontend asset — `public/images/default-picture.png`, shipped in
// the compiled SPA — and the read answers it by redirect, so the picture the
// account without one shows is the one the frontend already bundles. The
// path is relative, so the redirect resolves against whatever host the
// client reached: the API's own in production, the dev server through its
// proxy.
const DefaultPicturePath = "/images/default-picture.png"

// The picture kinds the update accepts, with the extension each one's storage
// key carries. The bytes decide, not a declared type: the kind is read off the
// magic bytes, so a renamed archive never lands in the picture slot — and the
// extension names what the bytes are rather than what the client called the
// file it sent.
var pictureKinds = []struct {
	magic []byte
	mime  string
	ext   string
}{
	{magic: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, mime: "image/png", ext: "png"},
	{magic: []byte{0xff, 0xd8, 0xff}, mime: "image/jpeg", ext: "jpg"},
	{magic: []byte("RIFF"), mime: "image/webp", ext: "webp"}, // the WEBP form sits at offset 8
}

// The failures the picture procedures report. The handler maps them to
// connect codes, the way the account failures are mapped.
var (
	// ErrUnsupportedPicture is an update whose bytes name no accepted image
	// kind.
	ErrUnsupportedPicture = errors.New("user: the picture is not a PNG, JPEG, or WebP image")

	// ErrPicturesUnavailable is a picture procedure a run without the
	// storage engine cannot serve.
	ErrPicturesUnavailable = errors.New("user: picture storage is not available")
)

// Picture is one account's picture opened for reading. Default marks the
// bundled picture answering for an account that has none of its own: its
// body is empty, and the transport answers it with the asset the SPA ships.
type Picture struct {
	Body        io.ReadCloser
	ContentType string
	Default     bool
}

// pictureKey composes the bucket-scoped reference one account's picture
// lives under: `<bucket>/pictures/<id>.<ext>`. The extension travels in the
// name so the object says what it is wherever it is listed — the backend's
// browser, the local deployment's file tree — without a lookup. It comes
// from the sniffed bytes, never from the name the client sent. The bucket is
// the seeded default until the storage default-bucket setting is wired in.
func pictureKey(userID uuid.UUID, ext string) string {
	key, err := storage.Key("pictures", userID.String()+"."+ext)
	if err != nil {
		// A UUID and an extension from the fixed table above cannot form an
		// invalid key; the fallback is here for the validator's contract, not
		// for this composition.
		key = "pictures/" + userID.String() + "." + ext
	}
	return storage.DefaultBucketName + "/" + key
}

// sniffPictureType reads the picture's kind off its magic bytes, answering the
// content type the manifest records and the extension the key carries. The
// WebP check reaches past the RIFF form to the format field, so a WAV audio
// file — the other RIFF resident — is refused.
func sniffPictureType(data []byte) (mime, ext string, ok bool) {
	for _, kind := range pictureKinds {
		if !bytes.HasPrefix(data, kind.magic) {
			continue
		}
		if kind.mime == "image/webp" && (len(data) < 12 || string(data[8:12]) != "WEBP") {
			continue
		}
		return kind.mime, kind.ext, true
	}
	return "", "", false
}

// UpdateProfilePicture replaces an account's picture. The bytes are sniffed
// for their kind before anything is stored, then staged into the storage
// engine and synced in the request — a picture is small, and the read that
// follows the update must see it — so the replayed queue task the watcher
// also schedules finds nothing left to do. The account row names the stored
// object last: a crash before it leaves an orphan the garbage collection
// sweeps, never a picture the account cannot read.
//
// The account's reference is the object row's identity, not a path: the row
// owns the bucket and key, so a backend or key change never rewrites the
// account table. An upload that changes the image's kind also changes the
// key, and the replaced object is deleted **before** the new one is staged —
// the other order would leave a manifest row nothing names, and the row is
// what garbage collection keeps a file for. Losing the race instead costs
// nothing the client can see: the account reads the bundled default, the
// state a reset produces and the read already answers for a reference the
// engine no longer holds.
func (s *Service) UpdateProfilePicture(ctx context.Context, id string, data []byte) error {
	if s.pictures == nil {
		return ErrPicturesUnavailable
	}
	userID, err := parseWire(id)
	if err != nil {
		return ErrUserNotFound
	}
	// The row is read for two reasons: it establishes the account exists —
	// the guard decided who may write this picture, and a reference for an
	// account that is gone would leave an object nothing names — and it
	// names the object this upload replaces.
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("user: read for picture update: %w", err)
	}
	return s.storePicture(ctx, userID, row, data)
}

// storePicture is the write pipeline both picture writers share: sniff
// the bytes, delete the replaced object, stage and sync the new one, and
// point the account's reference at it.
func (s *Service) storePicture(ctx context.Context, userID uuid.UUID, row UserSchema, data []byte) error {
	mime, ext, ok := sniffPictureType(data)
	if !ok {
		return ErrUnsupportedPicture
	}

	// owner is the fact the upload-finished notice addresses: the staging
	// caller names the account its file belongs to, and the notice rides the
	// manifest's metadata. user_id stays beside it — the older record —
	// because removing a key from free-form metadata is a change no reader
	// asked for.
	metadata := map[string]any{"content_type": mime, "owner": userID.String(), "user_id": userID.String()}
	bucket, key, err := storage.SplitRef(pictureKey(userID, ext))
	if err != nil {
		return fmt.Errorf("user: picture reference: %w", err)
	}
	if row.PictureBucket != nil && row.PictureKey != nil &&
		(*row.PictureBucket != bucket || *row.PictureKey != key) {
		err = s.pictures.Delete(ctx, *row.PictureBucket, *row.PictureKey)
		if err != nil {
			return fmt.Errorf("user: delete the replaced picture: %w", err)
		}
	}
	err = s.pictures.Stage(ctx, bucket, key, bytes.NewReader(data), metadata)
	if err != nil {
		return fmt.Errorf("user: stage picture: %w", err)
	}
	err = s.pictures.Sync(ctx, bucket+"/"+key)
	if err != nil {
		return fmt.Errorf("user: sync picture: %w", err)
	}
	manifest, err := s.pictures.Manifest(ctx, bucket, key)
	if err != nil {
		return fmt.Errorf("user: picture manifest: %w", err)
	}
	objectID, err := storage.ParseObjectID(manifest.ID)
	if err != nil {
		return fmt.Errorf("user: picture reference: %w", err)
	}
	if _, err := s.repo.SetPictureFileID(ctx, s.pool, userID, objectID.String()); err != nil {
		return err
	}
	// The record is written after the row names the object: the object row
	// is what makes the picture the account reads, so a record written
	// before the row would describe a change that had not landed.
	s.audit.Record(ctx, s.pool, fwaudit.Entry{
		Event:  audit.EventProfilePictureUpdated,
		Status: fwaudit.StatusSuccess,
		UserID: userID.String(),
		Payload: map[string]string{
			"content_type": mime,
			"bytes":        strconv.Itoa(len(data)),
		},
	})
	s.log.Info("user: profile picture updated",
		slog.String("user_id", userID.String()), slog.Int("bytes", len(data)))
	return nil
}

// ResetProfilePicture removes an account's picture: the referenced object is
// deleted from the engine and the row's reference cleared, so the account
// falls back to the bundled default. An account without a picture resets as
// a no-op — the answer the client asked for is already the state.
func (s *Service) ResetProfilePicture(ctx context.Context, id string) error {
	if s.pictures == nil {
		return ErrPicturesUnavailable
	}
	userID, err := parseWire(id)
	if err != nil {
		return ErrUserNotFound
	}
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("user: read for picture reset: %w", err)
	}
	if row.PictureBucket != nil && row.PictureKey != nil {
		if err := s.pictures.Delete(ctx, *row.PictureBucket, *row.PictureKey); err != nil {
			return fmt.Errorf("user: delete picture: %w", err)
		}
	}
	if _, err := s.repo.SetPictureFileID(ctx, s.pool, userID, ""); err != nil {
		return err
	}
	s.audit.Record(ctx, s.pool, fwaudit.Entry{
		Event:  audit.EventProfilePictureReset,
		Status: fwaudit.StatusSuccess,
		UserID: userID.String(),
	})
	s.log.Info("user: profile picture reset", slog.String("user_id", userID.String()))
	return nil
}

// ProfilePicture opens one account's picture for reading. An account without
// one — the picture join's nils — answers the bundled default, the same
// state a reset lands in. The content type travels from the manifest's
// metadata, the value the update recorded when it staged the bytes.
func (s *Service) ProfilePicture(ctx context.Context, id string) (Picture, error) {
	userID, err := parseWire(id)
	if err != nil {
		return Picture{}, ErrUserNotFound
	}
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return Picture{}, ErrUserNotFound
	}
	if err != nil {
		return Picture{}, fmt.Errorf("user: read for picture: %w", err)
	}
	if row.PictureBucket == nil || row.PictureKey == nil {
		return Picture{Body: io.NopCloser(bytes.NewReader(nil)), Default: true}, nil
	}
	bucket, key := *row.PictureBucket, *row.PictureKey

	manifest, err := s.pictures.Manifest(ctx, bucket, key)
	if errors.Is(err, storage.ErrNotFound) {
		// The reference names a key the engine holds no file for: the picture
		// was lost without its row. The bundled default answers rather than
		// an error, because the state the client sees is the one a reset
		// produces.
		return Picture{Body: io.NopCloser(bytes.NewReader(nil)), Default: true}, nil
	}
	if err != nil {
		return Picture{}, fmt.Errorf("user: read picture manifest: %w", err)
	}
	mime, _ := manifest.Metadata["content_type"].(string)
	body, err := s.pictures.Open(ctx, bucket, key)
	if errors.Is(err, storage.ErrNotFound) {
		s.log.WarnContext(ctx, "user: the picture is missing from the backend",
			slog.String("user_id", userID.String()),
			slog.String("bucket", bucket), slog.String("key", key))
		return Picture{Body: io.NopCloser(bytes.NewReader(nil)), Default: true}, nil
	}
	if err != nil {
		return Picture{}, fmt.Errorf("user: open picture: %w", err)
	}
	return Picture{Body: body, ContentType: mime}, nil
}
