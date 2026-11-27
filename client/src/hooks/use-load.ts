import { useEffect, useState } from "react";

// one event stream per page, shared by everything that shows live data. the server sends a
// version that moves whenever something it shows changes
let source: EventSource | undefined;
let version: string | undefined;
const listeners = new Set<() => void>();

function subscribe(fn: () => void) {
  listeners.add(fn);
  if (!source) {
    source = new EventSource("/api/events");
    // the first version is what the page just loaded, and a reconnect sends the latest
    source.addEventListener("message", (e) => {
      if (version !== undefined && e.data !== version) for (const l of listeners) l();
      version = e.data;
    });
  }
  return () => {
    listeners.delete(fn);
    if (listeners.size === 0) {
      source?.close();
      source = version = undefined;
    }
  };
}

/** loads on mount and on reload(), and when live, whenever the server says something
 * changed. an answer that didn't change doesn't re-render */
export function useLoad<T>(load: () => Promise<T>, live = false): [T | undefined, string, () => void] {
  const [data, setData] = useState<T>();
  const [error, setError] = useState("");
  const [count, setCount] = useState(0);
  useEffect(() => {
    let latest = 0;
    let last = "";
    const run = () => {
      const n = ++latest;
      load().then(
        (d) => {
          if (n !== latest) return;
          const json = JSON.stringify(d);
          if (json !== last) setData(d);
          last = json;
          setError("");
        },
        (e: Error) => n === latest && setError(e.message),
      );
    };
    run();
    const unsubscribe = live ? subscribe(run) : undefined;
    return () => {
      latest = -1;
      unsubscribe?.();
    };
  }, [count]);
  return [data, error, () => setCount((c) => c + 1)];
}
