// mirrors the json in web.go and store/store.go

export interface User {
  id: string;
  name: string;
  note: string;
  email: string;
  admin: boolean;
  allServices: boolean;
  services: string[];
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

export interface UserDetail extends User {
  keys: Key[];
  logins: Login[];
}

export interface Me {
  user: User | null;
  via?: "session" | "key" | "tailnet";
  login?: string;
  services: { name: string; url: string }[];
  // the service from ?to=, and why it can't be signed in to
  target?: string;
  error?: string;
}

export interface NewKey {
  key: string;
}

export interface NodeInfo {
  state?: string;
  url?: string;
  signCommand?: string;
  nodeError?: string;
}

export interface Service extends NodeInfo {
  name: string;
  container: string;
  port: number;
  funnel: boolean;
  dangerouslyDisableAuth: boolean;
  // where requests are proxied, missing while the container isn't running
  upstream?: string;
  error?: string;
}

export interface Services {
  tailgate: NodeInfo;
  problems: string[];
  services: Service[];
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
  services: string[];
}

export interface OIDCClient {
  id: string;
  name: string;
  service: string;
  redirectURIs: string[];
  createdAt: number;
}

export interface OIDCClients {
  issuer: string;
  clients: OIDCClient[];
}
