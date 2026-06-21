// openbox control plane — Cloudflare Worker front door.
//
// Every request is forwarded to one container-backed Durable Object (a
// singleton) that runs the Go `openbox control` binary on port 8080. The Worker
// holds no logic of its own; the container does all the work, backed by Neon
// Postgres and a CA key supplied as a secret. See wrangler.jsonc.

import { Container, getContainer } from "@cloudflare/containers";

// CONTROL + OPENBOX_PUBLIC_URL come from wrangler.jsonc (typed via `wrangler
// types` into Cloudflare.Env). Secrets are added with `wrangler secret put` and
// aren't in config, so declare them here.
type Env = Cloudflare.Env & {
  DATABASE_URL: string; // Neon postgres:// URL          (secret)
  OPENBOX_CA_KEY: string; // CA private key PEM            (secret)
  OPENBOX_MESH_AUTHKEY?: string; // optional tailnet pre-auth key (secret)
  OPENBOX_MESH_CONTROL?: string; // optional coordination URL     (var/secret)
};

export class OpenboxControl extends Container<Env> {
  defaultPort = 8080; // `openbox control` listens here (OPENBOX_ADDR=:8080)
  sleepAfter = "1h"; // node heartbeats keep it warm; cold start ~2-3s otherwise
  enableInternet = true; // needs egress: Neon Postgres + reaching nodes
  pingEndpoint = "/health"; // health check

  constructor(ctx: DurableObjectState<Cloudflare.Env>, env: Env) {
    super(ctx, env);
    // Pass config/secrets through to the Go process as environment variables.
    this.envVars = {
      OPENBOX_ADDR: ":8080",
      OPENBOX_DB: env.DATABASE_URL,
      OPENBOX_CA_KEY: env.OPENBOX_CA_KEY,
      OPENBOX_PUBLIC_URL: env.OPENBOX_PUBLIC_URL ?? "https://opbx.net",
      // Optional embedded mesh so the web console can reach overlay nodes.
      ...(env.OPENBOX_MESH_AUTHKEY
        ? {
            OPENBOX_MESH: "1",
            OPENBOX_MESH_AUTHKEY: env.OPENBOX_MESH_AUTHKEY,
            ...(env.OPENBOX_MESH_CONTROL ? { OPENBOX_MESH_CONTROL: env.OPENBOX_MESH_CONTROL } : {}),
          }
        : {}),
    };
  }
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    // One instance owns the DB + CA: route everything to the same container.
    const container = getContainer(env.CONTROL, "singleton");
    return container.fetch(request);
  },
};
