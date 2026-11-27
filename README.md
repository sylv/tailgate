# tailgate

tailgate is a reverse proxy hooked into tailscale. Stick `tailgate.id=photos` and `tailgate.port=2283` on a container and its available at `https://photos.your-tailnet.ts.net` over your tailnet by default, or publicly by setting `tailgate.funnel=true` via [tailscale funnel](https://tailscale.com/docs/features/tailscale-funnel)

Funnel users who aren't on your tailnet sign in with a key, or taillscale users accessing tailgate apps over your tailnet can login automatically once an admin links their tailscale login to a tailgate user. Rules let you apply redirects, block requests, forward headers to the app, and a lot more. an OIDC provider lets supported apps sign people in with the same auth tailgate uses.

**the tl;dr** is I wanted Cloudflare Tunnel and Cloudflare Access, but using tailscale. so thats what this is - it replaces my previous cloudflare tunnel (for public access) + traefik (for internal access) with a single proxy that can handle auth and tailnet/public requests.

<sup>AI was used heavily to make tailgate, you can decide how that affects your choice.</sup>

<sup>apps on your tailnet are [tailscale services](https://tailscale.com/kb/1552/tailscale-services) hosted by the tailgate node. funnel only serves a node's own name, so each funnel app gets its own node, which shows up as its own device in your tailscale admin console.</sup>

<table>
  <tr>
    <td><img src=".github/screenshots/overview-user.png" alt="A user's page, with the apps they can reach, their keys and tailnet logins"></td>
    <td><img src=".github/screenshots/overview-rules.png" alt="The rules page"></td>
  </tr>
</table>

## Features

- Every container gets its own `*.ts.net` name and HTTPS cert
- Auth sits in front of each app, or can be disabled for apps that handle it on their own
- App accesss is decided per user
- Tailscale users can be tied to tailgate users, letting them bypass signing in for requests from their devices
- Keys in request headers for scripts, mobile apps and MCP clients
- Custom rules that let you block, redirect, skip, or add and remove headers by various expressions
- An OIDC provider for supported apps (Immich, Grafana, etc) so users only need one tailgate login to access multiple apps
- Works with tailnet lock

## Setup

### 1. tailscale

tailgate's nodes and tailscale services all carry `tag:tailgate`. add this to your tailnet policy:

```jsonc
{
  // create the tailgate tag
  "tagOwners": { "tag:tailgate": ["autogroup:admin"] },
  // allow funnel on tailgate's nodes
  "nodeAttrs": [{ "target": ["tag:tailgate"], "attr": ["funnel"] }],
  // let the tailgate node host the tailscale services it makes
  "autoApprovers": { "services": { "tag:tailgate": ["tag:tailgate"] } },
  "grants": [
    // let tailnet members reach tailgate's nodes and tailscale services
    { "src": ["autogroup:member"], "dst": ["tag:tailgate"], "ip": ["443"] },
    // let the tailgate node see its funnel nodes, so it notices when you delete one
    { "src": ["tag:tailgate"], "dst": ["tag:tailgate"], "ip": ["443"] },
  ],
  // you may want to ensure tailgate nodes cannot talk to other nodes in your network
}
```

without the `autoApprovers` line, approve each tailscale service on the services page of the tailscale admin console. MagicDNS and HTTPS certificates have to be on too

then create the credential tailgate uses, in the tailscale admin console under Settings, Trust credentials:

1. press "New credential" and fill in the settings step
2. on the scopes step, pick "Custom scopes" and tick:
   - Auth Keys, write, with the tag `tag:tailgate`. tailgate makes a single use key for each new node
   - Services, write, with the tag `tag:tailgate`. tailgate makes a tailscale service for each app on your tailnet, and deletes it once no container uses it
3. pass the secret, `tskey-client-...`, as `TS_CLIENT_SECRET`

tailgate checks the credential when it starts and won't start unless it has exactly these scopes and only the tag `tag:tailgate`, so a leaked secret can't do more. tailscale services without `tag:tailgate` are never touched

with tailnet lock on, every new node waits on the apps page until you sign it with the command shown there. that's the tailgate node on its first start and each funnel app's node, since apps on your tailnet don't get nodes

### 2A. with the docker proxy

for security, it is *strongly recommended* to use the tailgate docker proxy, which helps isolate root access if tailgate ever had a vulnerability.

```yaml
services:
  tailgate-docker:
    container_name: tailgate-docker
    image: ghcr.io/sylv/tailgate:v1
    command: ["docker-proxy"]
    user: "0"
    network_mode: none
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - tailgate-docker:/run/tailgate

  tailgate:
    # on initial start, you'll want to "mkdir -p ./.tailgate && sudo chown -R 65532:65532 ./.tailgate"
    # or else the directory permissions won't allow tailgate to write to the data directory.
    # or just use docker volumes instead of a host mount. whatever
    container_name: tailgate
    image: ghcr.io/sylv/tailgate:v1
    restart: unless-stopped
    depends_on:
      - tailgate-docker
    environment:
      - TZ=Australia/Perth
      - TS_CLIENT_SECRET=tskey-client-...
      - DOCKER_HOST=unix:///run/tailgate/docker.sock
    volumes:
      - tailgate-docker:/run/tailgate
      - ./.tailgate:/data

volumes:
  tailgate-docker:
```

### 2B. as root

if you want a simpler setup, you can run tailgate as root and pass the docker socket in directly. you probably shouldn't, but its a free country.

```yaml
services:
  tailgate:
    image: ghcr.io/sylv/tailgate:v1
    user: "0"
    restart: unless-stopped
    environment:
      - TZ=Australia/Perth
      - TS_CLIENT_SECRET=tskey-client-...
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./.tailgate:/data
```

The data volume stores users, keys and node states. Keep it persistent.

### 3. first start

tailgate logs a setup link like `https://tailgate.your-tailnet.ts.net/setup?key=...`. open it from a device on your tailnet, pick the admin's name and save the key it gives you. until then no apps start and funnel stays off, and each restart logs a new setup link. if tailscale names the node `tailgate-1` because the name is taken, tailgate waits and logs what to rename, same as for apps. the admin page is at `https://tailgate.your-tailnet.ts.net/admin` and is not accessible outside of the tailnet.

```sh
# if you ever lose a key, reset it like this. it removes all of that user's keys and passkeys (signing them out everywhere) and prints one new key.
docker exec tailgate tailgate reset-key <user>
# if you need to make someone an admin, or change the admin, use this
docker exec tailgate tailgate set-admin <user> true|false
```

## usage

> [!WARNING] every certificate is published in public certificate transparency logs, which anyone can search publicly. that includes apps only on your tailnet, so the name of every app and your tailnets name are public, and stay public after you rename or remove an app. dont put anything in `tailgate.hostname` you wouldnt want people to see

### exposing a container

```yaml
services:
  immich:
    image: ghcr.io/immich-app/immich-server
    labels:
      # what tailgate keeps this app's user access, rules, OIDC clients and node state under. don't change it
      - tailgate.id=photos # required
      # the app's name on your tailnet, so its address. change it any time.
      # if another device has the name, the app waits with an error on the admin page until you free it
      - tailgate.hostname=pics # default = tailgate.id
      # the port tailgate connects to wihin the container
      - tailgate.port=2283 # required
      # whether to make this app publicly accessible. if true, the app gets its own node with tailscale funnel.
      # otherwise it's a tailscale service on the tailgate node and only users on your tailnet can access it.
      - tailgate.funnel=true # default = false
      # if the app itself has auth or you like living on the edge, you can disable tailgates auth.
      # for something like homeassistant which handles its own auth and does not support OIDC without extensions, you'll likely want this.
      - tailgate.dangerouslyDisableAuth=true # default = false
      # by default tailgate asks search engines not to index the app with an X-Robots-Tag header.
      # set this to false for something you want showing up in search results, like a public blog.
      # tailgate's own pages are never indexed
      - tailgate.noindex=false # default = true
      # the network tailgate reaches the container on, like traefik.docker.network. tailgate has to be on it too.
      # if the container isn't on it, the app won't start and the admin page lists the networks it is on.
      - tailgate.network=sylv_proxy # default = TAILGATE_NETWORK, or the container's first network by name
```

tailgate connects to the container's IP on `tailgate.network`, then `TAILGATE_NETWORK`, then the container's first network sorted by name, so tailgate has to be on that network too. containers with `network_mode: container:...` or `service:...` are reached through the container they borrow the network from, which can set `tailgate.network` itself, and `network_mode: host` ones through the Docker bridge's gateway

if you delete a funnel app's device in the tailscale admin console, the app stops and waits for you to press "Log in again" on the apps page, so it doesn't come back on its own. the tailgate node logs back in by itself

tailgate never deletes devices. when no container uses an app anymore, or an app stops using its node because funnel was turned off, its node is listed under "Left in Tailscale" on the apps page. delete its device in the tailscale admin console and tailgate forgets it, which needs the `tag:tailgate` to `tag:tailgate` grant from the setup. an app moving off its node waits for that, since tailscale won't make a tailscale service while a device has the name. tailscale services are tailgate's own, so they're deleted as soon as no container uses them. turning funnel on deletes the app's tailscale service and starts a node. each switch needs a new certificate, and let's encrypt gives each name 5 a week

on a first start every app needs a certificate, so tailgate fetches new ones two at a time. let's encrypt fails orders that all arrive at once

### users and keys

admins add users and tick the apps each one can reach (or just all apps). a new user gets a key to send them. they sign in with it on the login page, which shows the apps they can use

<table>
  <tr>
    <td><img src=".github/screenshots/login.png" alt="The login page, signing in to photos"></td>
    <td><img src=".github/screenshots/dashboard.png" alt="ezra's apps after signing in"></td>
  </tr>
</table>

<img src=".github/screenshots/users.png" alt="The users page">

<details>
<summary>A user's page</summary>
<img src=".github/screenshots/user.png" alt="ezra's page, with their apps, keys and tailnet logins">
</details>

link a tailnet login like `you@github` to a user and that logins devices are signed in as them, no key needed. tagged devices, subnet routers and exit nodes never sign anyone in as they speak for more than one person.

apps and scripts can send a key with each request instead:

- `Authorization: Bearer tg_...`, which also works for MCP clients like Claude
- `X-Tailgate-Key: tg_...`, for apps that already use `Authorization`, like the Immich mobile app's custom headers

tailgate strips its keys and cookie before the request reaches the app

#### passkeys

> [!WARNING] passkeys belong to the tailgate node's name, so changing `TAILGATE_HOSTNAME` or your tailnet's name breaks every passkey. sign in with a key and add them again. users may also forget their keys, but lets be honest, they were going to do that anyway.

after signing in with a key on supported devices, tailgate offers to add a passkey. the login page then has a "Sign in with a passkey" button. each user's keys and passkeys are listed under "Sign-in methods" on their dashboard. users can remove their own there, and admins can remove them on the user's page. set `TAILGATE_PASSKEYS=false` to turn passkeys off

the admin pages need a key or passkey session. an admin signed in by their tailnet login needs to re-auth with one of those.

### rules

rules run on every request to an app, top to bottom. Each one has an [expr](https://expr-lang.org/docs/language-definition) condition and effects like `block`, `redirect`, `skip`, and setting or removing request and response headers. rules before the login see every request, and rules after it know who the user is

Over Funnel, rules before the login also run on tailgate's own pages, like the login page, where `app.id` is `""`. On the tailnet they don't, so a bad rule can't lock you out of the admin page. The admin page isn't on Funnel at all

Every app and tailgate itself answer `/.tailgate/trace` with what tailgate sees of your request, like cloudflare's `/cdn-cgi/trace`: your IP, country, ingress, how you're signed in and the request id. It runs before the rules, so it works even when they block you

| stage     | expression                                                                     | effect                                |
| --------- | ------------------------------------------------------------------------------ | ------------------------------------- |
| pre auth  | `ingress == "funnel" && country not in ["AU", "NO", "CA"]`                     | block                                 |
| pre auth  | `app.id == "wisp" && path startsWith "/mcp" && !inCidr(ip, "160.79.104.0/21")` | block                                 |
| post auth | `app.id == "lyra"`                                                             | set-request-header X-User = user.name |

set-request-header removes the header the client sent from every request by default, also when the rule doesn't match, so nobody can send their own `X-User` to the app. untick "Remove the client's value on every request" for a header the app should still get from clients

<img src=".github/screenshots/rules.png" alt="The rules page">

the editor autocompletes every value and function, and checks each rule as you type

<img src=".github/screenshots/editor.png" alt="Editing a rule, with autocomplete and an error">

country info uses [DB-IP](https://db-ip.com). the image has the database built in, and each rebuild gets the latest month

### OIDC

OIDC can be used so that apps use tailgate for auth instead of implementing their own. This requires:
- The app to support [OIDC](https://openid.net/developers/how-connect-works/)
- The Tailgate user to have an email set
- The Tailgate user to have access to the app

generally setup will vary by app. create an OIDC profile for the app, then configure the app to talk to tailgate. it varies by app. good luck! :) 

<img src=".github/screenshots/oidc.png" alt="The OIDC clients page">

the apps server has to reach the tailgate node to swap codes for tokens, so the server's tailnet device needs a grant to `tag:tailgate` on port 443.

### env variables

| Variable             | Default                       |                                                                                |
| -------------------- | ----------------------------- | ------------------------------------------------------------------------------ |
| `TS_CLIENT_SECRET`   | required                      | The secret of the [trust credential](#0-tailscale)                             |
| `TAILGATE_HOSTNAME`  | `tailgate`                    | The name of the node with the login page, admin pages and OIDC                 |
| `TAILGATE_DATA_DIR`  | `/data`                       | The database and every node's state                                            |
| `TAILGATE_NETWORK`   |                               | The network to reach containers on when they don't set `tailgate.network`      |
| `TAILGATE_PASSKEYS`  | `true`                        | Whether users can add passkeys and sign in with them                           |
| `TAILGATE_LOG_JSON`  | `false`                       | Log JSON instead of coloured text                                              |
| `TAILGATE_LOG_LEVEL` | `info`                        | `debug` also shows tailscale's own logs for each node, for connection problems |
| `DOCKER_HOST`        | `unix:///var/run/docker.sock` | Where Docker is, as `unix://` or `tcp://`                                      |
| `NO_COLOR`           |                               | Set to anything to log text without colour                                     |
| `TZ`                 | `UTC`                         | The time zone for `now()` in rules and log timestamps                          |

## future

- running several tailgate instances for high availability. today each app's node and tailscale service has one host, and sessions, codes and cert fetches live in one process, so two instances would fight over the same names (?)
- giving the oauth client `devices:core` again. read access would show whether a tailscale service is approved for the tailgate node, instead of waiting 20 seconds before saying it needs approval. write access would let tailgate approve itself, so the `autoApprovers` line goes, and delete the old device that holds an app's name. write access can also delete or authorize any device in the tailnet, which is why tailgate doesn't already use it.
