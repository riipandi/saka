package oidc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/storage"
)

// ErrLogoMissing is a logo read for a client that has none.
var ErrLogoMissing = errors.New("oidc: the client has no logo")

// The logo kinds the upload accepts, with the extension each one's storage
// key carries. The bytes decide, not a declared type — the same rule the
// profile pictures run on. An SVG is refused, not sniffed for: the format
// is a script host, and a public endpoint would serve it as one.
var logoKinds = []struct {
	magic []byte
	mime  string
	ext   string
}{
	{magic: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, mime: "image/png", ext: "png"},
	{magic: []byte{0xff, 0xd8, 0xff}, mime: "image/jpeg", ext: "jpg"},
	{magic: []byte("RIFF"), mime: "image/webp", ext: "webp"}, // the WEBP form sits at offset 8
}

// sniffLogoType reads the logo's kind off its magic bytes, answering the
// content type the manifest records and the extension the key carries. The
// WebP check reaches past the RIFF form to the format field, so a WAV audio
// file — the other RIFF resident — is refused.
func sniffLogoType(data []byte) (mime, ext string, ok bool) {
	for _, kind := range logoKinds {
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

// LogoFile is one client's logo opened for reading, with the content type
// the staging metadata recorded.
type LogoFile struct {
	Body        io.ReadCloser
	ContentType string
}

// UploadLogo replaces a client's logo. The bytes are sniffed for their kind
// before anything is stored, then staged into the storage engine and synced
// in the request — a logo is small, and the read that follows must see it.
// The replaced file is deleted before the new one is stored, the order that
// cannot leave an object no manifest row names. The staging metadata names
// no owner: an upload-finished notice about an operator's own administrative
// write is mail nobody asked for.
func (s *Service) UploadLogo(ctx context.Context, id string, data []byte) error {
	if s.pictures == nil {
		return ErrLogosUnavailable
	}
	mime, ext, ok := sniffLogoType(data)
	if !ok {
		return ErrUnsupportedLogo
	}

	row, err := s.repo.GetClient(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrClientNotFound
	}
	if err != nil {
		return err
	}

	key, keyErr := storage.Key("oidc-logos", id+"."+ext)
	if keyErr != nil {
		// A client id the contract's pattern held and an extension from
		// the fixed table cannot form an invalid key; the fallback keeps
		// the validator's contract honest.
		key = "oidc-logos/" + id + "." + ext
	}
	ref := storage.DefaultBucketName + "/" + key
	bucket, objKey, refErr := storage.SplitRef(ref)
	if refErr != nil {
		return fmt.Errorf("oidc: logo reference: %w", refErr)
	}
	if row.LogoPath != nil && *row.LogoPath != ref {
		if delBucket, delKey, delErr := storage.SplitRef(*row.LogoPath); delErr == nil {
			if err := s.pictures.Delete(ctx, delBucket, delKey); err != nil {
				return fmt.Errorf("oidc: delete the replaced logo: %w", err)
			}
		}
	}
	metadata := map[string]any{"content_type": mime}
	if err := s.pictures.Stage(ctx, bucket, objKey, bytes.NewReader(data), metadata); err != nil {
		return fmt.Errorf("oidc: stage logo: %w", err)
	}
	if err := s.pictures.Sync(ctx, ref); err != nil {
		return fmt.Errorf("oidc: sync logo: %w", err)
	}

	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, err := s.repo.SetLogoPath(ctx, tx, id, &ref); err != nil {
			return err
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientLogoUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": id, "content_type": mime},
		})
		return nil
	})
}

// DeleteLogo removes a client's logo. A client without one is the same
// success that records nothing — the state it names is the one it is in.
func (s *Service) DeleteLogo(ctx context.Context, id string) error {
	row, err := s.repo.GetClient(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrClientNotFound
	}
	if err != nil {
		return err
	}
	if row.LogoPath == nil || *row.LogoPath == "" {
		return nil
	}
	if s.pictures != nil {
		if delBucket, delKey, refErr := storage.SplitRef(*row.LogoPath); refErr == nil {
			if err := s.pictures.Delete(ctx, delBucket, delKey); err != nil {
				return fmt.Errorf("oidc: delete logo: %w", err)
			}
		}
	}
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, err := s.repo.SetLogoPath(ctx, tx, id, nil); err != nil {
			return err
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientLogoDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": id},
		})
		return nil
	})
}

// Logo opens the stored logo for reading. The transport answers the bytes
// publicly — the sign-in page fetches them through an <img> tag — so the
// unknown-client and no-logo states are the caller's distinctions to make.
func (s *Service) Logo(ctx context.Context, id string) (LogoFile, error) {
	if s.pictures == nil {
		return LogoFile{}, ErrLogosUnavailable
	}
	row, err := s.repo.GetClient(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return LogoFile{}, ErrClientNotFound
	}
	if err != nil {
		return LogoFile{}, err
	}
	if row.LogoPath == nil || *row.LogoPath == "" {
		return LogoFile{}, ErrLogoMissing
	}
	bucket, key, refErr := storage.SplitRef(*row.LogoPath)
	if refErr != nil {
		return LogoFile{}, ErrLogoMissing
	}
	manifest, err := s.pictures.Manifest(ctx, bucket, key)
	if err != nil {
		return LogoFile{}, fmt.Errorf("oidc: read logo manifest: %w", err)
	}
	body, err := s.pictures.Open(ctx, bucket, key)
	if err != nil {
		return LogoFile{}, fmt.Errorf("oidc: read logo: %w", err)
	}
	contentType, _ := manifest.Metadata["content_type"].(string)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return LogoFile{Body: body, ContentType: contentType}, nil
}

// logoURL renders the address a client's logo answers at, the URL the views
// carry so a UI renders the image without composing the path itself.
func (s *Service) logoURL(view ClientView) *string {
	if !view.HasLogo {
		return nil
	}
	url := s.baseURL + "/oidc/clients/" + view.ID + "/logo"
	return &url
}
