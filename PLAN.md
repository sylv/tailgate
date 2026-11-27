# Plan

## Doing

### Security

- set-request-header gets a "Remove the client's value on every request" option, on by default for new effects. It strips the header even when the rule doesn't match, so a client can't forge `X-User`. Response headers don't need it, they come from the app
- Admin API only accepts key sessions. A tailnet-linked admin gets an inline key form on the admin page
- Callback codes are tied to a state cookie on the app host, so someone else's callback link can't sign you in as them
- Strip `Authorization` whenever its trimmed token starts with `tg_`
- Sessions get an absolute lifetime counted from creation
- The WhoIs cache only keeps successes. A peer that isn't found is anonymous instead of a 500

### Live UI

- One SSE stream, `GET /api/events`, sends a version that is bumped on node state changes, reconcile, admin writes, prune and rules saves. It replaces the 2s polling
- The tailnet logins dropdown loads when opened
- `<Ago>` and `<Until>` components run their own timer, so only the time text re-renders
- Set up the React compiler so components are memoized automatically
- The dashboard only links apps that are online. The apps page shows real funnel state, not the label
- Rules saves send a version and get a 409 when stale. The user form stops remounting and losing edits

### Events instead of polling

- Docker events are filtered to start, die, destroy, create, rename, update, connect and disconnect
- `SetFunnel` only pokes the node when the value changes
- Drop the hourly Docker recheck, the event watcher already lists again on reconnect
- Deleted devices come from the tailgate node's netmap instead of the 30s API poll. This needs a `tag:tailgate` to `tag:tailgate` grant, and a self check shows a problem when a funnel node isn't a visible peer. Drop the Devices Core read scope
- Unused Tailscale services tagged `tag:tailgate` are deleted when the last container using them is removed. Services without the tag are ignored

### Tailscale internals

- A `nodes` table stores each app's device ID. It replaces `deviceID()` reading `tailscaled.state` and the `os.Stat` checks in `Host` and `Unused`
- A `certs` table records each cert tailgate fetched and when it expires. Certs it has are fetched right away, new ones wait in a queue of 2. It replaces `cached()` reading tailscale's cert files

### Simplify

- Compression through `gzhttp` on `web()` and `appHandler`. Delete `compress.go`, the precompressed UI assets and brotli. Check websockets still work through it
- Bundle the GeoIP database in the image and require it at startup. Delete the downloader and `Problem()`
- One label spelling, camel case as in the README. Drop `spellings` and the lookup in `networkLabel`
- Load the OIDC signing key at startup, outside `g.mu`
- Split `hosts("")` into `selfHost()` and `appHost(id)`
- Identify once in `web()` and drop the `identified` memo on `request`

### SQLite

- `synchronous(NORMAL)`, `temp_store(MEMORY)` and `_txlock=immediate`

### Passkeys

- `TAILGATE_PASSKEYS`, on by default
- After a key sign in, prompt to add a passkey only when the device has a built-in authenticator and the user has none. "Not now" with a "Don't ask again" checkbox that sets a flag on the user
- The login page has a "Sign in with a passkey" button, with the key field and submit button below it. The key field also offers passkeys in autofill
- A "Sign-in methods" list with keys and passkeys on the dashboard. Admins can remove either from the user page
- Each passkey stores its authenticator ID, shown by name like "iCloud Keychain" from the public AAGUID list, plus created and last used times and the last user agent, parsed when shown
- `reset-key` also removes passkeys
- README notes that changing `TAILGATE_HOSTNAME` or the tailnet name breaks passkeys

### Other

- Request logs include the source IP
- Shut servers down with `Shutdown` and a short timeout instead of `Close`
- README gets a Future section for HA and running several tailgate instances
- Serve Tailscale services in process with an empty serve handler and `srv.Listen` on the service IPs, instead of a localhost socket with a PROXY header. Tested on blip with IPv4 and IPv6

## Scrapped

- Refusing to start with host networking. Tailscale services are served in process instead, so there's no localhost socket
- Rewriting migration 0003
- Keeping the GeoIP downloader, using a host GeoIP source or a CI job to refresh it. Renovate rebuilds keep it fresh
- Access and admin roles from policy file app capabilities, for now
- The `cache_size`, `mmap_size`, `page_size` and `auto_vacuum` pragmas
- Matching devices by hostname and trusting `X-Forwarded-For` on services like tsdproxy does

## Later

- Admin role from a policy file capability
- HA and several tailgate instances
- A way back in for users who lost their key and have no passkey on hand
