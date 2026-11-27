// mirrors the json in web.go and store/store.go

export interface User {
  id: string;
  name: string;
  note: string;
  email: string;
  admin: boolean;
  allApps: boolean;
  apps: string[];
}

export interface Key {
  id: string;
  label: string;
  createdAt: number;
  lastUsedAt: number | null;
}

export interface Login {
  // a string since tailscale's ids don't fit in a js number
  id: string;
  name: string;
}

export interface Passkey {
  id: string;
  // the authenticator model, named from lib/aaguids.json
  aaguid: string;
  createdAt: number;
  lastUsedAt: number | null;
  // the browser it was last used from
  userAgent: string;
}

export interface UserDetail extends User {
  keys: Key[];
  passkeys: Passkey[];
  logins: Login[];
}

export interface Me {
  user: User | null;
  via?: "session" | "key" | "tailnet";
  login?: string;
  // url is "" until the app is online, funnel is whether it can be opened from the internet
  apps: { name: string; url: string; funnel: boolean }[];
  // the app from ?to=, and why it can't be signed in to
  target?: string;
  error?: string;
  // how the user signs in
  keys?: Key[];
  passkeys?: Passkey[];
  // TAILGATE_PASSKEYS
  passkeysOn: boolean;
  ingress: "tailnet" | "funnel";
  requestId: string;
}

export interface NewKey {
  key: string;
}

export interface NodeInfo {
  state?: string;
  // whether funnel is really on, not just asked for
  funnelOn?: boolean;
  url?: string;
  signCommand?: string;
  nodeError?: string;
  // when a cert is tried again, unix seconds
  retryAt?: number;
  // a tailscale service on the tailgate node, instead of its own node
  tailscaleService?: boolean;
}

export interface App extends NodeInfo {
  id: string;
  hostname: string;
  container: string;
  port: number;
  funnel: boolean;
  dangerouslyDisableAuth: boolean;
  // where requests are proxied, missing while the container isn't running
  address?: string;
  error?: string;
}

export interface Apps {
  tailgate: NodeInfo;
  problems: string[];
  apps: App[];
  unused: string[];
}

export async function signOut(then: string) {
  await api("POST", "/api/logout");
  location.href = then;
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: method === "GET" ? {} : { "Content-Type": "application/json" },
    body: method === "GET" ? undefined : JSON.stringify(body ?? {}),
  });
  const data = await res.json().catch(() => ({ error: `${res.status} ${res.statusText}` }));
  if (!res.ok) throw new Error(data.error);
  return data as T;
}

export interface Effect {
  type: string;
  name?: string;
  value?: string;
  status?: number;
  // set-request-header only, removes the client's own value from every request
  strip?: boolean;
}

export interface Rule {
  id: string;
  section: "pre" | "post";
  name: string;
  condition: string;
  effects: Effect[];
  mode: "on" | "log" | "off";
}

// field is name, condition, effects or effects.N, and line and column are 1-based
export interface RuleError {
  rule: number;
  field: string;
  line?: number;
  column?: number;
  message: string;
}

export interface RuleList {
  rules: Rule[];
  // a hash of the saved rules, sent back with a save so a stale page can't overwrite them
  version: string;
  // why the saved rules can't be loaded
  error?: string;
  // what stopped a save
  errors?: RuleError[];
}

export interface SchemaValue {
  name: string;
  type: string;
  desc: string;
  pre: boolean;
}

export interface Schema {
  values: SchemaValue[];
  // the routed apps' ids and hostnames
  apps: string[];
  hostnames: string[];
}

export interface OIDCClient {
  id: string;
  name: string;
  app: string;
  redirectURIs: string[];
  createdAt: number;
}

export interface OIDCClients {
  issuer: string;
  clients: OIDCClient[];
}
