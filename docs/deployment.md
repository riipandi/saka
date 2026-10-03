# Deployment Guidelines

## Create app instances

```sh
# Create Fly.io app
fly apps create saka --org personal
```

## Attach Postgres database

```sh
# Create volume for the data.
fly postgres create --name saka-db --region sjc --password $(openssl rand -hex 8)

# Attach Postgres database
fly postgres attach saka-db -a saka
```

## Launch and deploy

```sh
# Prepare deploy configuration file
cp deploy/fly.toml.example fly.toml
sed -i '' 's/CHANGEME_APP_NAME/saka/g' fly.toml

# Prepare environment variables
cp .env.example .env.production
pnpm --silent generate:key

# Load secrets from dotenv file then initialize deployment
sed -e '/^[[:space:]]*#/d' -e '/^$/d' .env.production | fly secrets import
fly secrets list

# Check the config file before deploying it. The file is the single source of
# truth and is required at runtime, so validate it against the same environment
# the app will see.
go run ./cmd config:validate --config-file=app.config.json

# Ship the config file with the app. It holds env: directives, never a secret,
# so it can live in the image or in the repository.

# Initialize deployment
fly deploy --remote-only --no-public-ips --now --skip-release-command

# Update deployment
fly deploy --remote-only --now
```

## Setup custom domain

Point DNS A Record to the assigned IP address.
Or, if using subdomain you can point `saka.fly.dev` CNAME record.

```sh
# Allocate IPv4 (required)
fly ips allocate-v4 -a saka --shared
fly ips allocate-v4 -a saka # dedicated

# Allocate IPv6 (optional)
fly ips allocate-v6 -a saka

# List allocated IPs
fly ips list -a saka

# Assign custom domain
fly certs create app.example.com -a saka
```
