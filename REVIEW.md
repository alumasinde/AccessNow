# Backend review - fixes applied

Verified: go build, go vet, gofmt clean; 28 packages pass (new regression tests added).

## Fixed
1. Login rate limit bypass: RealIP middleware trusted X-Forwarded-For from anyone. Now opt-in via TRUST_PROXY_HEADERS=true (only behind a proxy you control). Audit IP no longer reads the header.
2. Lockout race: failed-login counter and lock decision are now one atomic SQL statement.
3. Login enumeration: unknown tenant and suspended-account responses no longer reveal existence before the password is verified.
4. SSRF (webhooks + sync): private-address check now runs before connect (dialer Control), covers CGNAT/IPv4-in-IPv6/reserved ranges; webhooks no longer follow redirects.
5. Production config guard: refuses to start with COOKIE_SECURE=false, empty ALLOWED_ORIGINS, or "*".
6. Gate revalidation bypass: substring path match replaced by exact segment match.
7. Request bodies: hard 1 MiB cap with a clear error; trailing JSON rejected.
8. CSV export formula injection neutralised.
9. Shutdown waits for background loops before closing the DB pool.

## Not changed - needs your decision
- Webhook secret_ref / sync credentials_ref share one global env namespace across tenants (tenant A can name tenant B's ACCESSNOW_SYNC_* variable and have it sent to A's URL). Fix: require a tenant-bound name such as ACCESSNOW_SYNC_T<tenantID>_... (breaks existing env var names).
- Audit hash chain covers action/subject/time only, not actor, reason, before/after, IP. Changing the formula invalidates existing chains; needs a versioned hash + migration.
- LIKE searches do not escape % and _. Argon2id t=1 (m=64MiB) is on the light side.
