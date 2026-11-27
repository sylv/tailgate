import { useCallback, useEffect, useSyncExternalStore } from "react";
import { request } from "../api";

interface Entry {
  state: { data?: unknown; error: string };
  json?: string;
  // the server's data version the answer is from, 0 until it says
  version: number;
  inflight?: Promise<void>;
  // again is set when a forced refresh came in while a fetch was running, since that
  // fetch may have started before the change
  again?: boolean;
  listeners: Set<() => void>;
  // how many mounted hooks want this kept up to date with the event stream
  live: number;
}

const cache = new Map<string, Entry>();

function entry(path: string) {
  let e = cache.get(path);
  if (!e) cache.set(path, (e = { state: { error: "" }, version: 0, listeners: new Set(), live: 0 }));
  return e;
}

function set(e: Entry, state: Partial<Entry["state"]>) {
  e.state = { ...e.state, ...state };
  for (const l of e.listeners) l();
}

// the latest version from the event stream, 0 while it's closed
let latest = 0;

// a signed out answer has no version, and the stream doesn't run then
const stale = (e: Entry) => e.live > 0 && e.version > 0 && e.version < latest;

/** fetches path in the background and swaps the answer in. a running fetch is shared,
 * unless force asks for one that starts after now */
export function revalidate(path: string, force = false): void {
  const e = entry(path);
  if (e.inflight) {
    e.again ||= force;
    return;
  }
  let again = false;
  e.inflight = request("GET", path)
    .then(
      ({ data, version }) => {
        // an answer that didn't change doesn't re-render
        const json = JSON.stringify(data);
        if (json !== e.json) set(e, { data, error: "" });
        else if (e.state.error) set(e, { error: "" });
        e.json = json;
        e.version = version;
        // the stream moved on while this was loading
        again = stale(e);
      },
      (err: Error) => set(e, { error: err.message }),
    )
    .finally(() => {
      e.inflight = undefined;
      if (again || e.again) {
        e.again = false;
        revalidate(path);
      }
    });
}

// one event stream per page, shared by every live hook. it sends the server's data
// version when it connects and whenever it moves
let source: EventSource | undefined;
let streams = 0;

function stream() {
  if (streams++ === 0) {
    source = new EventSource("/api/events");
    source.addEventListener("message", (msg) => {
      latest = Number(msg.data);
      for (const [path, e] of cache) if (stale(e)) revalidate(path);
    });
  }
  return () => {
    if (--streams === 0) {
      source?.close();
      source = undefined;
      latest = 0;
    }
  };
}

/** GETs path, shared and cached by path across the page. shows the cached answer right
 * away and refreshes it on mount, on reload(), and when live, whenever the server says
 * something changed */
export function useApi<T>(path: string, live = false): [T | undefined, string, () => void] {
  const subscribe = useCallback(
    (fn: () => void) => {
      const e = entry(path);
      e.listeners.add(fn);
      return () => e.listeners.delete(fn);
    },
    [path],
  );
  const state = useSyncExternalStore(subscribe, () => entry(path).state);
  useEffect(() => {
    revalidate(path);
    if (!live) return;
    const e = entry(path);
    e.live++;
    const close = stream();
    return () => {
      e.live--;
      close();
    };
  }, [path, live]);
  return [state.data as T | undefined, state.error, () => revalidate(path, true)];
}
