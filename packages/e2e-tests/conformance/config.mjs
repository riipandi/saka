// The conformance rehearsal's configuration: where the suite, the issuer,
// and the account live, and which client each profile rides. Every value is
// an environment override away — the defaults are the compose-dev stack's
// shapes, the same ones the seeder (`migrate:seed`) writes the clients for.
//
//   CONFORMANCE_SUITE_URL   the suite's front (default the prebuilt image's host)
//   SAKA_ISSUER             the issuer URL the suite drives (the nginx TLS proxy)
//   SAKA_API                the issuer's RPC origin the runner mints its bearer at
//   SAKA_USER / SAKA_PASS   the account the bearer is minted with
//   CONFORMANCE_ALIAS       the fixed alias the plans and the seeded
//                           clients' registrations share (default saka)

export const suiteBase = process.env.CONFORMANCE_SUITE_URL ?? 'https://localhost.emobix.co.uk:8443'

export const issuer = process.env.SAKA_ISSUER ?? 'https://saka.localhost.test:3443'

export const apiBase = process.env.SAKA_API ?? 'http://localhost:3080'

export const admin = {
  user: process.env.SAKA_USER ?? 'admin',
  pass: process.env.SAKA_PASS ?? 'Expecto-Patronum-9'
}

export const alias = process.env.CONFORMANCE_ALIAS ?? 'saka'

// The alias is baked into the seeder's client registrations
// (internal/database/seeders), so the runner and the seeder must name the
// same one — changing this default without re-seeding leaves every
// redirect_uri unregistered.

// The clients the seeder writes: one per profile the rehearsal runs. The
// provider authenticates every confidential client by basic and post
// alike — the plan's client_auth_type decides which the suite presents.
export const clients = {
  basic: {
    client_id: 'suite-basic',
    client_secret: 'saka-suite-basic',
    auth: 'client_secret_basic',
    alias: `${alias}-basic`
  },
  config: {
    client_id: 'suite-config',
    client_secret: 'saka-suite-config',
    auth: 'client_secret_post',
    alias: `${alias}-config`
  },
  logout: {
    client_id: 'suite-logout',
    client_secret: 'saka-suite-logout',
    auth: 'client_secret_basic',
    alias: `${alias}-logout`
  }
}

// The account the driver signs the SPA in as — the seeder's fixture.
export const account = {
  username: process.env.SAKA_SUITE_USER ?? 'suite',
  password: process.env.SAKA_SUITE_PASS ?? 'Expecto-Suite-9'
}

// buildConfiguration is the test configuration document the plan create
// and the module create both carry: the fixed alias, the issuer's
// discovery URL, and the static clients.
export function buildConfiguration(planName) {
  return {
    alias,
    description: `saka ${planName}`,
    server: {
      discoveryUrl: `${issuer}/.well-known/openid-configuration`,
      login_hint: account.username
    },
    // The plans' static registration fields: a basic client, the
    // second basic client, and the secret_post one. The provider
    // authenticates every confidential client by basic and post
    // alike, so the same three serve every plan.
    client: {
      client_id: clients.basic.client_id,
      client_secret: clients.basic.client_secret
    },
    client2: {
      client_id: clients.logout.client_id,
      client_secret: clients.logout.client_secret
    },
    client_secret_post: {
      client_id: clients.config.client_id,
      client_secret: clients.config.client_secret
    },
    consent: {}
  }
}
