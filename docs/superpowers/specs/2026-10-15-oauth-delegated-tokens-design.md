# Delegated OAuth 2.1 tokens with operation-level consent

- **Date:** 2026-10-15
- **Issue:** [loopingz/webda.cli#14](https://github.com/loopingz/webda.cli/issues/14) (related: webda.cli#7 named profiles)
- **Repositories:** `webda.io` (server, MCP), `webda.cli` (Go client), `loopcenter` (API server, Tauri desktop/mobile app)
- **Supersedes:** the deferred "spec 2" of `2026-10-03-webda-mcp-design.md` (OAuth authorization server + MCP resource-server side)

## 1. Goal

A native client (the Webda CLI, the loopcenter desktop/mobile app, an MCP client) obtains a token through browser
sign-in and consent, then calls a Webda API on the user's behalf. On the consent page the user can narrow the token to
a subset of operations. The restriction is enforced on every call, on top of the user's own permissions, so a token
can never exceed what the user may do. Tokens expire, refresh, and can be revoked with near-immediate effect.

### Success criteria

- `webda-cli auth` and the loopcenter app sign in via standard OAuth 2.1 (authorization code + PKCE), with a device-code
  fallback for the CLI on headless machines.
- The consent page lists the operations requested, filtered to what the user can call; the user can untick any.
- A call to an operation outside the granted scope fails with `403 insufficient_scope` on every transport (REST,
  `/operations`, GraphQL, gRPC, MCP).
- Revoking a client takes effect cluster-wide within `revocationCacheTtl` (default 10 s).
- Consent, device and result pages are overridable by the application (loopcenter hosts them in its React SPA).

### Non-goals

- OpenID Connect login for third parties (`id_token`, userinfo).
- Confidential clients (client secrets, `private_key_jwt`), `client_credentials` grant.
- Dynamic Client Registration (RFC 7591). Unknown clients use Client ID Metadata Documents instead.
- Moving loopcenter's plaintext config secrets to a secret store (tracked separately).

## 2. Concepts

### Scopes

A scope is a space-separated list of tokens:

- **Operation globs:** operation ids with `*` wildcards on dot-separated segments: `Task.*`, `Auth.Me`, `*`. Matching
  reuses `@webda/iam`'s glob semantics (moved to `@webda/utils` so core can use it without depending on IAM).
- **`offline_access`:** a refresh token is issued only when requested and granted.

Unknown non-glob tokens are rejected with `invalid_scope`. A missing `scope` means the client's configured
`defaultScope` (for `webda-cli` and `loopcenter`: `* offline_access`).

Scope algebra (one module, `core/src/oauth/scope.ts`, fully unit-tested):

- `covers(granted, requested)`: every requested glob is matched by some granted glob (`Task.*` covers `Task.Get` and
  `Task.*`; `*` covers everything; `Task.Get` does not cover `Task.*`).
- `intersect(a, b)`: the globs of `a` covered by `b`, plus those of `b` covered by `a`.
- `expand(globs, operationIds)`: the concrete operation ids matched.
- `collapse(selectedIds, requestedGlobs, allIds)`: when every id matched by a requested glob is selected, emit the glob
  (so operations added later stay covered); otherwise emit the selected ids.

### Enforcement

`Session` gains `scopes?: string[]` and `clientId?: string`, set by `loadFromToken` from the access-token claims.
A core `OperationAuthorizer` (registered by `TokenService`) refuses with `insufficient_scope` when `session.scopes` is
set and no glob matches the operation id. Because authorizers only veto, the existing `permission` WebdaQL, IAM and
model `canAct` checks still run: effective access is `user permissions ∩ token scope`.

`canCallOperation` (probe mode) goes through the same authorizer, so listings (`GET /operations`, OpenAPI filtering,
MCP `tools/list`, GraphQL) only show what the token may call. Nested `callOperation` calls reuse the context and are
therefore also scope-checked; operations that must run internally regardless (e.g. a model hook calling another
operation) use the existing internal-context mechanism, which carries no token session.

Refusal response: `403` with
`WWW-Authenticate: Bearer error="insufficient_scope", scope="<operationId>"` and a JSON body
`{ "error": "insufficient_scope", "operation": "<operationId>" }`.

## 3. Server: `webda.io`

### 3.1 Core changes (`@webda/core`)

**`TokenService`**

- `issue()` accepts optional `{ clientId, scope, name }`, adds `scope` and `client_id` claims to the access JWT and
  stores them on the `RefreshToken`.
- `refresh(token, { scope? })` rotates as today; an optional `scope` may only narrow (`covers(stored, requested)`,
  otherwise `invalid_scope`).
- **Revocation denylist:**
  - New model `RevokedAccess { key, notBefore?, expiresAt }`.
  - `revokeFamily(fam)` writes `fam:<fam>`; `revokeUser(userId)` writes `user:<userId>` with `notBefore = now`.
    `expiresAt = now + accessTtl`.
  - `verifyAccess()` after signature verification does two primary-key gets (`fam:<claims.fam>`, `user:<claims.sub>`);
    rejects on a family hit, or on a user hit with `iat < notBefore`.
  - Cache: hits cached until `expiresAt` (revocation is permanent); misses cached for `revocationCacheTtl`.
  - Parameters: `revocationCheck` (default `true`), `revocationCacheTtl` (default `10` s, `0` disables the miss cache).
  - Applies to every access token, including today's `Authentication` logins (logout, password change, refresh reuse
    become effective within the cache TTL).
- Expired `RevokedAccess` rows are removed via store TTL where supported, else by a sweep at init and every
  `accessTtl`.

**`RefreshToken` model:** adds `clientId?`, `scope?`, `name?` (e.g. `"webda-cli on remi-mbp"`), `lastUsedAt?`.

**`Session`:** adds `scopes?`, `clientId?` (not persisted in cookies; only set for bearer sessions).

**Operations transport (`RestOperationsTransport` sibling: `OperationsTransport`, `Webda/OperationsTransport`)**

- `GET <url>` (default `/operations`): `{ version, operations: { [id]: { id, summary, description, input, output,
  destructive?, streaming? } } }`, filtered by `canCallOperation`. Schemas come from the operation definitions.
- `POST <url>/{id}`: JSON body as input, runs `callOperation`; streaming operations follow the same rules as the REST
  transport.
- Unknown id → 404; refused → 403 (scope refusal as above).

### 3.2 New package `@webda/oauth`

**Service `OAuthServer`** (`Webda/OAuthServer`). Parameters:

| Parameter                                    | Default                    | Meaning                                                                    |
| -------------------------------------------- | -------------------------- | -------------------------------------------------------------------------- |
| `url`                                        | `/oauth`                   | Route prefix                                                               |
| `issuer`                                     | request origin             | `issuer` in metadata; set explicitly behind proxies                        |
| `clients`                                    | `{ "webda-cli": … }`       | Static public clients (below)                                              |
| `clientMetadataDocuments`                    | `true`                     | Accept HTTPS `client_id` URLs                                              |
| `clientMetadataHosts`                        | `[]` (any public host)     | Allowed hosts for metadata documents                                       |
| `clientMetadataTimeout` / `…CacheTtl`        | `5` s / `3600` s           | Fetch timeout and cache                                                    |
| `loginUrl`                                   | required                   | App login page; receives `?redirect=<consentUrl>`                          |
| `consentUrl` / `deviceUrl` / `resultUrl`     | built-in pages under `url` | Overridable UI                                                             |
| `accessTtl` / `refreshTtl`                   | `TokenService` values      | Token lifetimes for OAuth clients                                          |
| `codeTtl`                                    | `60` s                     | Authorization code lifetime                                                |
| `deviceCodeTtl` / `devicePollInterval`       | `600` s / `5` s            | Device flow                                                                |
| `rememberConsent` / `rememberConsentTtl`     | `true` / `90` days         | Remembered consent                                                         |
| `scopesSupported`                            | top-level operation groups | Advertised in metadata                                                     |

**Static client** shape: `{ name, redirect_uris: string[], logo?, client_uri?, defaultScope?, allowedScopes?,
deviceFlow?: boolean }`.

- `redirect_uris` entries: `http://127.0.0.1/<path>` or `http://localhost/<path>` match **any port** (RFC 8252 §7.3);
  private-use schemes (`loopcenter://auth/callback`) and `https` URIs match exactly.
- `allowedScopes` caps what the client can ever be granted (default `*`).
- Built-in default: `webda-cli` with `redirect_uris: ["http://127.0.0.1/callback"]`, `deviceFlow: true`,
  `defaultScope: "* offline_access"`. Overridable or removable (`clients: { "webda-cli": null }`).

**Client ID Metadata Documents:** when `client_id` is an `https://` URL, fetch it (timeout, max 64 KB, no redirects,
resolved IP must not be private/loopback/link-local unless the host is in `clientMetadataHosts`), require
`client_id` in the document to equal the URL, read `client_name`, `redirect_uris`, `logo_uri`, `client_uri`. Such
clients never get `deviceFlow`. The consent page shows the client's host prominently.

**Models**

- `OAuthCode { hash (PK), clientId, redirectUri, codeChallenge, userId, identId, provider, amr, mfa, scope, name,
  expiresAt, usedAt?, family? }`.
- `OAuthDeviceCode { userCode (PK), secretHash, clientId, requestedScope, status: pending|approved|denied, userId?,
  grantedScope?, name?, expiresAt, lastPolledAt? }`.
- `OAuthGrant { uuid = "<userId>:<clientId>" (PK), userId, clientId, scope, createdAt, lastUsedAt, expiresAt }`.

**Routes**

- `GET /.well-known/oauth-authorization-server` (RFC 8414): `issuer`, `authorization_endpoint`, `token_endpoint`,
  `revocation_endpoint`, `device_authorization_endpoint`, `response_types_supported: ["code"]`,
  `grant_types_supported: ["authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"]`,
  `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`,
  `scopes_supported`, `client_id_metadata_document_supported: true`, and `webda_result_uri`, `webda_operations_uri`.
- `GET <url>/authorize`, `GET|POST <url>/consent` (built-in page), `GET|POST <url>/device` (built-in page),
  `GET <url>/done` (built-in result page), `POST <url>/token`, `POST <url>/revoke`, `POST <url>/device_authorization`.

**Operations** (callable from the built-in pages or an app UI, any transport, browser session):

- `OAuth.Consent.Get()` → `{ flow: "code"|"device", client: { id, name, logo?, uri?, host? }, user: { id, name },
  hostname?, operations: [{ group, ids: [{ id, summary, preselected }] }], offlineAccess, rememberable }`.
- `OAuth.Consent.Decide({ allow, operations, remember? })` → `{ redirect }`. Requires the CSRF token bound to the
  pending request.
- `OAuth.Device.Lookup({ userCode })` → binds the pending device request to the browser and returns `{ redirect }` to
  the consent URL. Rate-limited.
- `OAuth.Sessions()` → the user's refresh families with a `clientId` (`{ family, clientId, name, scope, createdAt,
  lastUsedAt }`).
- `OAuth.Grants()` → remembered consents.
- `OAuth.Revoke({ family? , clientId? })` → revokes one family, or every family of a client plus its grant.
- `OAuth.Forget({ clientId })` → deletes the grant only.

### 3.3 Authorization-code flow

1. **`GET /oauth/authorize`** with `response_type=code`, `client_id`, `redirect_uri`, `code_challenge`,
   `code_challenge_method=S256`, `state`, `scope?`, `prompt?` (`consent` | `none`), `hostname?` (Webda extension,
   shown on the consent page).
   - Unknown client or non-matching `redirect_uri` → error page, **never redirect**.
   - Other validation errors (`response_type`, missing/non-S256 PKCE, `invalid_scope`, scope outside
     `allowedScopes`) → redirect with `error`, `error_description`, `state`.
   - The validated request is stored in an encrypted, HttpOnly, SameSite=Lax cookie (10 min, path `url`), with a
     random CSRF token.
   - Not logged in → redirect to `loginUrl?redirect=<consentUrl>` (or `error=login_required` with `prompt=none`).
   - Logged in and a valid `OAuthGrant` covers the request and `prompt != consent` → skip to step 3 with
     `granted = requested ∩ grant.scope`.
   - `prompt=none` without a covering grant → `error=consent_required`.
   - Otherwise → redirect to `consentUrl`.
2. **Consent** (`OAuth.Consent.Get` / `OAuth.Consent.Decide`).
   - Operations = `expand(requested, allOperationIds)` filtered by `canCallOperation(probe)` for the user, grouped by
     first segment. Pre-selected: all, or the grant's scope when a partial grant exists (new ones highlighted).
   - Nothing left → "nothing to grant", only Deny.
   - Deny → `redirect_uri?error=access_denied&state=…`.
   - Allow → `granted = collapse(selected, requested) ∩ requested ∩ client.allowedScopes` (+ `offline_access` if
     requested). With `remember`, upsert `OAuthGrant` (scope = granted, `expiresAt = now + rememberConsentTtl`).
3. Create `OAuthCode` (random 32 bytes, stored hashed, `codeTtl`) carrying the session's identity (`userId`,
   `identId`, `provider`, `amr`, `mfa`) and redirect to `redirect_uri?code=…&state=…`.
4. **`POST /oauth/token`** `grant_type=authorization_code`, `code`, `code_verifier`, `client_id`, `redirect_uri`.
   - Consume the code atomically: conditional update setting `usedAt` (only if unset), then record the issued
     `family` on it. Missing/expired → `invalid_grant`.
   - A code presented again (`usedAt` set) → `invalid_grant`, and the recorded family is revoked (RFC 6749 §4.1.2).
     Used codes are kept until `expiresAt + accessTtl`, then swept.
   - Check `client_id`, `redirect_uri`, `S256(code_verifier) == code_challenge`.
   - `TokenService.issue(identity, { clientId, scope, name })`. Response
     `{ access_token, token_type: "Bearer", expires_in, scope, refresh_token? }`.
5. **Refresh:** `grant_type=refresh_token`, `refresh_token`, `client_id`, `scope?` (narrowing only). Rotation and reuse
   detection as `TokenService` today; `client_id` must match the stored one.
6. **Revoke** (RFC 7009): `token`, `token_type_hint?`, `client_id`. Refresh token → revoke its family; access token →
   revoke the family from its `fam` claim. Always `200`.

All token-endpoint errors are RFC 6749 JSON (`invalid_request`, `invalid_client`, `invalid_grant`,
`unsupported_grant_type`, `invalid_scope`) with `Cache-Control: no-store`.

### 3.4 Device-authorization flow (RFC 8628)

1. `POST /oauth/device_authorization` with `client_id`, `scope?`, `hostname?`. Client must have `deviceFlow`.
   - `user_code`: 8 characters from `BCDFGHJKLMNPQRSTVWXZ`, displayed `XXXX-XXXX`; input normalized (case, dashes,
     spaces).
   - `device_code = <userCode>.<base64url 32 random bytes>`; only the secret's SHA-256 is stored.
   - Response `{ device_code, user_code, verification_uri, verification_uri_complete, expires_in, interval }`.
2. Browser: `deviceUrl` (`?user_code=` prefilled from `verification_uri_complete`) → `OAuth.Device.Lookup` (rate limit:
   5 failures per session and 20 per IP per 10 min) → login if needed → consent with `flow: "device"`.
   - Consent **never** skipped for device flow, even with a grant.
   - The page states: "Only continue if you started this sign-in yourself on **<hostname>**".
   - Decide sets `status` to `approved` (with `grantedScope`, `userId`, identity) or `denied`.
3. Polling `POST /oauth/token` `grant_type=urn:ietf:params:oauth:grant-type:device_code`, `device_code`, `client_id`:
   - `pending` → `authorization_pending`; polled faster than `interval` (from `lastPolledAt`) → `slow_down` (interval
     +5 s for this code); `denied` → `access_denied` (record deleted); expired → `expired_token`.
   - `approved` → conditional delete, then issue tokens as in 3.3 step 4.

### 3.5 Built-in pages

Plain server-rendered HTML with inline CSS, no framework, no external assets. They only use the operations above, so an
app replacing them (via `consentUrl`, `deviceUrl`, `resultUrl`) has the same capabilities.

- **Consent:** client name/logo/host, signed-in user, hostname, operation groups as collapsible checkbox lists with
  a group checkbox, refresh-token notice, "Remember for this app", Allow / Deny.
- **Device:** code entry, then hands over to consent.
- **Result (`/done`):** `?status=success|error&error=…&client=…`, "You can close this window".

### 3.6 MCP (`@webda/mcp`)

- `OAuthAuthenticator` (`authenticator: "OAuth"`): requires a bearer session; fills
  `authInfo = { token, clientId: session.clientId, scopes: session.scopes ?? [], extra: { session } }`.
- `GET /.well-known/oauth-protected-resource` (RFC 9728): `{ resource, authorization_servers: [issuer],
  scopes_supported, bearer_methods_supported: ["header"] }`.
- Unauthenticated MCP request → `401` with `WWW-Authenticate: Bearer resource_metadata="<url>"`.
- Scope enforcement is the core authorizer; `tools/list` is filtered by `canCallOperation`.

## 4. Client: `webda.cli`

- **Profiles** (`~/.webdacli/config.yaml`): values become either a URL string (backward compatible) or an object
  `{ url, client_id?, scope? }`. `setup` gains `--client-id` and `--scope`. Defaults: `client_id: webda-cli`,
  `scope: "* offline_access"`.
- **Discovery:** `GET <url>/.well-known/oauth-authorization-server`, cached per profile. 404 → error "this server does
  not support OAuth sign-in".
- **`auth`** (`--scope`, `--device`, `--prompt`):
  - Browser flow: listen on `127.0.0.1:0` (loopback only), `state` and PKCE S256, open
    `authorization_endpoint` with `redirect_uri=http://127.0.0.1:<port>/callback` and `hostname`. On callback, check
    `state`, exchange the code, then `302` the browser to `webda_result_uri` (fallback: minimal built-in page). Times
    out after 5 min.
  - Device flow when `--device`, when no browser can be opened, or in an SSH session (`SSH_CONNECTION`), or on Linux
    without `DISPLAY`/`WAYLAND_DISPLAY`: print `verification_uri`, the code and a terminal QR code of
    `verification_uri_complete`, poll honouring `interval` / `slow_down`.
- **`logout`:** `POST revocation_endpoint` with the refresh token, then delete it from the token store.
- **Token store:** `TokenInfo` drops `Sequence`, adds `ExpiresAt` and `Scope`. Existing backends (keyring,
  SSH-agent file, machine file) are unchanged. An old token with a `Sequence` is discarded and triggers `auth`.
- **Refresh** (`webdaclient`): `grant_type=refresh_token` to `token_endpoint`, storing the rotated refresh token;
  existing pre-expiry refresh and retry-on-401 behaviour kept, now using `expires_in`.
- **Operations:** fetch `webda_operations_uri` (or `<url>/operations`); on 404, fall back to the OpenAPI document at
  `<url>/` and map each `operationId` to its method and path, filling path parameters from flags, `GET` flags as query,
  others as JSON body. The execution layer dispatches by how the operation was discovered.
- **`403 insufficient_scope`:** print "this token does not cover `<operation>`; run `<bin> auth --scope …`".
- README "Authentication" section rewritten (also fixing the stale `.tok` path).

## 5. loopcenter

### 5.1 API server (`packages/api`)

- Bump `@webda/*` to the release containing this work; add `@webda/oauth`.
- Configure `OAuthServer`:
  - `loginUrl: "/auth/loopingz"` (Google login; `redirect` added to its `authorized_uris`).
  - `consentUrl: "/oauth/consent"`, `deviceUrl: "/oauth/device"`, `resultUrl: "/oauth/done"` (SPA routes, 5.2).
  - `clients`: `loopcenter` (`http://127.0.0.1/callback`, `loopcenter://auth/callback`, `deviceFlow: false`) and
    `webda-cli` (default definition).
- Configure `OperationsTransport` at `/operations`.
- `AuthorizerService.checkRequest`: if the session is already logged in (core `loadFromToken` accepted the bearer),
  allow; otherwise keep the legacy `jwtVerify` path for old tokens and `lzci_` CI tokens. The email allowlist in
  `applyPolicy` still applies since login goes through `Authentication`.
- `/auth/cli` and `/auth/cli/confirm` stay, logged as deprecated, until all clients are migrated (rollout step 4).
- `/my/tokens` replaced by `OAuth.Sessions`.

### 5.2 SPA (`packages/loopcenter/frontend`)

New public routes in `HostRoutes`, rendered outside `AuthGuard`:

- `/oauth/consent`: `OAuth.Consent.Get` / `Decide`, same features as the built-in page, app styling.
- `/oauth/device`: code entry, `OAuth.Device.Lookup`.
- `/oauth/done`: result page.
- Settings → "Connected apps": `OAuth.Sessions`, `OAuth.Grants`, with Revoke and Forget.

`client_login.html` is deleted in rollout step 4.

### 5.3 Tauri desktop and mobile app

- **Desktop:** Rust command `oauth_authorize(authorizeUrl)` replaces `cli_auth_listen`: binds `127.0.0.1:0`, opens the
  system browser, waits (5 min) for `/callback`, checks `state` (passed in by TS), returns the `code`, answers the
  browser with `302` to `resultUrl`. PKCE generation, discovery and the code exchange live in TypeScript
  (`platform/src/oauth.ts`), shared with mobile.
- **Mobile:** same request with `redirect_uri=loopcenter://auth/callback`; the existing deep-link handler resolves the
  pending request by `state`.
- **`api()`:** refreshes before expiry and once on 401, then retries; concurrent refreshes are de-duplicated.
- **Logout:** `/oauth/revoke`, then clear storage.
- **Token storage moves to the OS keychain:**
  - Desktop: Rust `keyring` crate (macOS Keychain, Windows Credential Manager, Secret Service), service
    `com.loopingz.loopcenter`, one entry holding `{ refresh_token, access_token, expires_at, scope }`.
  - iOS Keychain / Android Keystore through a small Tauri mobile plugin exposing `get`/`set`/`delete`.
  - Migration on first start: read `~/.loopcenter/auth.json`, discard it if it carries a legacy `sequence` (the user
    signs in again), delete `auth.json` and `access_token`.

## 6. Rollout

1. **webda.io:** core changes, `@webda/oauth`, MCP; release.
2. **loopcenter server + SPA:** bump, configure `OAuthServer` and `OperationsTransport`, add SPA routes; `/auth/cli`
   still live.
3. **loopcenter app** and **webda.cli** (independent): switch to OAuth.
4. **loopcenter cleanup:** remove `/auth/cli`, `client_login.html`, the legacy `RefreshToken` model and legacy bearer
   path once the old clients are no longer seen in logs.

## 7. Testing

**webda.io (Vitest)**

- Scope algebra: `covers`, `intersect`, `expand`, `collapse`, invalid tokens.
- Scope authorizer: refusal on every transport helper; token never exceeds the user (user lacks permission, token
  `*`); probe listings filtered.
- `TokenService`: claims, narrowing refresh, denylist (family, user `notBefore`, cache hit/miss timing,
  `revocationCheck: false`), sweep.
- `OAuthServer` over HTTP with `WebdaTest`:
  - Code flow end to end; unknown client and bad redirect (no redirect); missing or plain PKCE; wrong verifier;
    code reuse revokes family; expired code; deny; `allowedScopes` cap; loopback any-port and exact custom scheme.
  - Remembered consent: full skip, partial pre-selection, `prompt=consent`, `prompt=none` errors, expiry, Forget,
    Revoke.
  - Device flow: pending, `slow_down`, approve, deny, expiry, user-code normalization, lookup rate limit, never
    skipping consent.
  - Client metadata documents: valid, mismatched `client_id`, private IP blocked, size limit, timeout, cache.
  - Built-in pages render and post correctly; CSRF rejection.
- `OperationsTransport`: listing filtered by scope, call, 404, 403 body.
- MCP: protected-resource metadata, 401 challenge, `authInfo.scopes`.

**webda.cli (`go test`):** `httptest` fake authorization server: discovery, loopback with state mismatch, result
redirect, device flow with fake clock and `slow_down`, refresh rotation, logout, legacy token discard,
`insufficient_scope` hint, `/operations` and OpenAPI fallback dispatch, profile config backward compatibility.

**loopcenter:** API tests for the configured clients and `AuthorizerService` bearer pass-through; Vitest for the SPA
consent/device/done routes and `platform/src/oauth.ts` (PKCE, refresh de-duplication); Rust unit tests for the
callback listener and keyring storage (mock backend); existing beta smoke tests extended with an OAuth sign-in.
