// Package appconfig is the application-configuration area: the deployment's
// own settings surface, in both senses the word carries.
//
// The system configuration is the JSON file's: the configuration endpoint
// (`GET /api/configuration`) publishes the deployment's resolved
// configuration read-only — the public subset to anyone, the full
// non-secret document to an administrator — and the test-email RPC sends
// its smoke message. The database-backed settings are the product flows':
// the settings feature keeps catalog items whose overrides are editable at
// runtime, sealing a sealed item under the deployment's shared cipher.
//
// It is an area of its own rather than a feature of identity because the
// settings it serves are the deployment's, not any account's. The area owns
// one table — settings — and reads accounts through the identity area's
// user package, the way the audit-log reader does.
//
// The area owns its own wiring, like every other: the registry names it and
// knows nothing about its services.
package appconfig

import (
	"fmt"
	"log/slog"

	"github.com/samber/do/v2"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/cache"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/kernel"
	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/crypto"
)

// Package registers the services this area owns.
//
// The composition root applies it while the container is built, so it only
// registers: a service is constructed when something resolves it. The
// mailer and the pool are infrastructure the registry's prewarm walk
// resolves, so a process that reaches the listener has them. The cipher is
// the deployment's shared one, built from the secret key; a run without a
// secret key carries a nil cipher, and the feature refuses the sensitive
// write rather than storing a value it cannot protect.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*Service, error) {
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*fwaudit.Recorder](i)
		mail := do.MustInvoke[*fwmailer.Service](i)
		log := do.MustInvoke[*slog.Logger](i)
		return NewService(pool, recorder, mail, log), nil
	}),
	do.Lazy(func(i do.Injector) (*Settings, error) {
		cfg := do.MustInvoke[*config.Config](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*fwaudit.Recorder](i)
		kvCache := do.MustInvoke[cache.Cache](i)
		cipher, err := settingsCipher(cfg.App.SecretKey)
		if err != nil {
			return nil, err
		}
		return NewSettings(pool, cipher, recorder, kvCache)
	}),
)

// settingsCipher builds the cipher a sealed value encrypts under. An empty
// secret key is the run that carries none — the feature answers that at the
// call site — while a key that is set but unreadable is a broken
// deployment, and it fails the run.
func settingsCipher(secretKey string) (*crypto.Cipher, error) {
	if secretKey == "" {
		return nil, nil
	}
	cipher, err := crypto.NewCipherFromHex(secretKey)
	if err != nil {
		return nil, fmt.Errorf("appconfig: read the cipher key: %w", err)
	}
	return cipher, nil
}

// Mount resolves what this area needs and builds the module the router
// mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	cfg := do.MustInvoke[*config.Config](i)
	return NewModule(
		*cfg,
		do.MustInvoke[*Service](i),
		do.MustInvoke[*Settings](i),
	), nil
}
