# Keycloak / OIDC Integration

Marauder ships with a first-class OpenID Connect (Authorization Code +
PKCE-equivalent) flow. This guide walks through the dev compose profile
that bundles a working Keycloak realm so you can verify the entire flow
end-to-end without leaving Docker.

## Quick start (dev)

```bash
cd deploy
cp .env.example .env
# Generate secrets if you haven't already:
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$(openssl rand -base64 32)|" .env

# Bring up the regular stack PLUS Keycloak:
docker compose --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.sso.yml \
  up -d
```

This starts:

- `db` — Postgres
- `backend` — Marauder Go backend, with `MARAUDER_OIDC_ENABLED=true`
  and the Keycloak issuer URL injected
- `frontend` — React static bundle
- `gateway` — nginx, exposing the stack on `localhost:6688`
- `keycloak` — Keycloak 26.0 with the `marauder` realm pre-imported

The first start of Keycloak takes ~30 seconds while it imports the
realm. Watch with `docker logs -f deploy-keycloak-1`.

## Test user

The realm import provisions one test user:

| Field | Value |
|---|---|
| Username | `alice` |
| Password | `marauder` |
| Email | `alice@example.com` |
| Realm | `marauder` |

## Try the flow

1. Open <http://localhost:6688> — you should see the Marauder login screen.
2. Click **"Sign in with Keycloak"** at the bottom.
3. You'll be redirected to Keycloak (`localhost:8643`) where you log
   in as `alice / marauder`.
4. Keycloak redirects back to
   `http://localhost:6688/api/v1/auth/oidc/callback?code=...&state=...`.
5. Marauder validates the state cookie, exchanges the code for tokens,
   verifies the ID token's signature against Keycloak's JWKS, and
   either finds the existing user (by `(issuer, subject)` pair) or
   provisions a new one with the default `user` role.
6. Marauder issues its own JWT pair (ES256, signed with the master-key-
   encrypted private key) and 302-redirects to
   `/oidc-callback#access_token=...&refresh_token=...&...`.
7. The SPA picks up the tokens from the URL fragment, stores them in
   the zustand auth store, fetches `/auth/me`, and lands on the
   dashboard.

## What gets logged

Each successful OIDC sign-in writes an `auth.login` audit entry with
the actor (the OIDC username/email), IP, and User-Agent. The audit
log is visible at `/audit` for any admin user.

## Wiring your own provider

If you don't want to use the bundled Keycloak, just set five env vars
on the backend container:

```env
MARAUDER_OIDC_ENABLED=true
MARAUDER_OIDC_ISSUER=https://your.idp.example.com/realms/marauder
MARAUDER_OIDC_CLIENT_ID=marauder
MARAUDER_OIDC_CLIENT_SECRET=...
MARAUDER_OIDC_REDIRECT_URL=https://marauder.cc/api/v1/auth/oidc/callback
```

Marauder uses [`coreos/go-oidc`](https://github.com/coreos/go-oidc)
under the hood and discovers everything else from the issuer's
`/.well-known/openid-configuration`. Any provider that speaks OIDC
discovery should work — Authentik, Authelia, Auth0, Okta, Google,
Microsoft Entra, etc.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `oidc state mismatch` after returning from the IdP | Cookies are being stripped (cross-site context). Make sure the redirect URL host matches the host in your browser address bar. |
| `verify id_token: ...` | Issuer URL mismatch or clock skew. Marauder uses `coreos/go-oidc` which is strict about both. |
| Stuck on the "Signing you in..." page | Open the browser console; the SPA expects four URL-fragment parameters. The most common cause is the backend redirect URL pointing to the wrong host. |
| `oidc discovery` error at backend startup | The issuer URL is unreachable from the backend container. Inside the dev compose, use `http://keycloak:8643/realms/marauder` (the container name), not `localhost`. |

## Limitations

Marauder's v0.3 OIDC implementation is the **authorization code flow
without PKCE**. PKCE will land in v0.4 alongside the option to use
public clients. Until then, treat the client secret as a real secret
and don't deploy this to a fully-untrusted network.
