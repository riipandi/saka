-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- The signing key set: JSON Web Key Sets for signing/verifying JWTs.
-- The database is the one signing authority — the environment carries
-- no key-pair material.
-- --------------------------------------------------------

DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'jwt_algorithm') THEN
    CREATE TYPE public.jwt_algorithm AS ENUM (
        'HS256',
        'HS384',
        'HS512',
        'RS256',
        'RS384',
        'RS512',
        'ES256',
        'ES384',
        'ES512'
    );
END IF; END$$;

CREATE TABLE IF NOT EXISTS public.jwks (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    key_id TEXT NOT NULL UNIQUE, -- kid
    algorithm public.jwt_algorithm NOT NULL DEFAULT 'HS256',
    key_type TEXT NOT NULL, -- e.g. RSA, EC, oct
    public_key BYTEA NOT NULL, -- public key for verification (encrypted)
    private_key BYTEA, -- nullable, only for internal use (encrypted)
    -- seal_fp records a non-secret fingerprint of the key that sealed
    -- private_key (crypto.Cipher.Fingerprint). A reader whose current
    -- seal fingerprint differs knows the secret key rotated under the
    -- row and retires it instead of failing every decrypt.
    seal_fp TEXT NOT NULL DEFAULT '',
    use_for TEXT NOT NULL DEFAULT 'sig', -- 'sig' (signature) or 'enc' (encryption)
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    -- Recoverable private key material is always sealed with the
    -- canonical enc: prefix; convert_from fails closed on non-UTF8
    -- bytes, so unprefixed or malformed values are rejected.
    CONSTRAINT chk_jwks_private_key_enc CHECK (private_key IS NULL OR convert_from(private_key, 'UTF8') LIKE 'enc:%')
) USING heap;

CREATE TRIGGER trg_jwks_updated_at BEFORE UPDATE ON public.jwks FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_jwks_expires_at ON public.jwks(expires_at);
CREATE INDEX IF NOT EXISTS idx_jwks_active_algorithm_use_for ON public.jwks(is_active, use_for, algorithm);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_jwks_active_algorithm_use_for;
DROP INDEX IF EXISTS idx_jwks_expires_at;

DROP TRIGGER IF EXISTS trg_jwks_updated_at ON public.jwks;
DROP TABLE IF EXISTS public.jwks;

DROP TYPE IF EXISTS public.jwt_algorithm;

-- +goose StatementEnd
