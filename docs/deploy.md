---
title: Hosted deployment
description: Run the openbox control plane as a live service on Cloudflare Containers with a Neon Postgres database, deployed by GitHub Actions.
---

This is how `opbx.net` runs — a hosted control plane you don't have to keep a
machine online for. The architecture:

```text
  opbx.net        ─► Cloudflare Worker ─► Container (Durable Object)
                                            │  runs `openbox control` (Go)
                                            ├─► Neon Postgres   (registry/sessions/tokens)
                                            └─► CA key          (secret env var)
  docs.opbx.net   ─► Cloudflare Pages (this site)
```

The container is **stateless**: all durable state lives in Neon, and the SSH CA
key is injected as a secret. That's what lets it run on Cloudflare Containers,
whose disk resets on every restart.

:::caution[The CA key is the root of trust]
Whoever holds the CA private key can mint user certs for **every** node. Store it
only as a Cloudflare secret, and keep an encrypted backup somewhere safe. Rotating
it invalidates all node host certs (nodes must re-enroll).
:::

## Prerequisites

- A Cloudflare account (Workers Paid plan — Containers requires it).
- A [Neon](https://neon.tech) project (free tier is fine).
- `wrangler` (`npm i -g wrangler`) and a local `openbox` binary for `ca-keygen`.

## 1. Create the database

In Neon, create a database and copy its connection string. It looks like:

```
postgres://user:pass@ep-xxx.region.aws.neon.tech/openbox?sslmode=require
```

The control plane creates its own tables on first boot — no manual migration.

## 2. Mint the CA key

```sh
openbox ca-keygen > openbox-ca.pem      # keep this secret + backed up
```

## 3. Set the secrets

From the `cloudflare/` directory:

```sh
cd cloudflare
npm ci
wrangler secret put DATABASE_URL        # paste the Neon URL
wrangler secret put OPENBOX_CA_KEY       # paste the contents of openbox-ca.pem
# optional: if your nodes are on a Tailscale/Headscale mesh and you want the
# web console to reach them, also set a pre-auth key:
wrangler secret put OPENBOX_MESH_AUTHKEY
```

## 4. First deploy

```sh
wrangler deploy
```

wrangler builds the container image from the repo-root `Dockerfile`, pushes it to
Cloudflare's registry, and rolls out the Worker + container Durable Object. After
this, deploys happen automatically from GitHub Actions (see step 7).

## 5. Bind the domains

- **App** — in the Cloudflare dashboard, add a custom domain / route mapping
  `opbx.net` to the `openbox-control` Worker.
- **Docs** — create a Pages project named `openbox-docs` (the
  [docs workflow](https://github.com/n1rna/openbox/blob/main/.github/workflows/deploy-docs.yaml)
  deploys to it) and bind `docs.opbx.net` to it.

## 6. Grab the bootstrap token

On the very first boot with an empty database, the control plane creates a user
and prints a login command. Read it from the container logs:

```sh
wrangler tail openbox-control --format pretty
# look for:  openbox login --server https://opbx.net --token obx_…
```

Then, from any machine:

```sh
openbox login --server https://opbx.net --token obx_…
openbox whoami
```

If you miss the token, delete the row in Neon (`DELETE FROM users;`) and redeploy
to re-bootstrap.

## 7. Wire up CD

Add two repository secrets in GitHub (Settings → Secrets → Actions):

| Secret | Value |
|---|---|
| `CLOUDFLARE_API_TOKEN` | a token with Workers Scripts, Containers, and Pages edit permissions |
| `CLOUDFLARE_ACCOUNT_ID` | your Cloudflare account id |

From then on:

- Pushing changes to the Go control plane, `Dockerfile`, or `cloudflare/` triggers
  [`deploy-control.yaml`](https://github.com/n1rna/openbox/blob/main/.github/workflows/deploy-control.yaml)
  → rebuilds + redeploys the container.
- Pushing changes to `docs/` or `website/` triggers
  [`deploy-docs.yaml`](https://github.com/n1rna/openbox/blob/main/.github/workflows/deploy-docs.yaml)
  → redeploys this docs site to Cloudflare Pages.

## Notes

- **Web console reachability.** The browser console proxies exec through the
  control plane to a node. A Cloudflare-hosted control plane can only reach nodes
  that are publicly reachable or on a shared mesh — set `OPENBOX_MESH_AUTHKEY` (step 3)
  so the container joins your overlay. Plain CLI dispatch is unaffected: the CLI
  talks to nodes directly, the control plane only brokers identity.
- **Self-hosting instead.** The same binary runs the control plane anywhere with a
  disk: `openbox control` uses a local SQLite file by default. The DSN in
  `OPENBOX_DB` (or `--db`) selects SQLite (a path) vs Postgres (a `postgres://` URL).
