import { useEffect, useState } from "react";

/** loads on mount and on reload(), then every `poll` ms while the tab is visible.
 * an answer that didn't change doesn't re-render */
export function useLoad<T>(load: () => Promise<T>, poll?: number): [T | undefined, string, () => void] {
  const [data, setData] = useState<T>();
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let stale = false;
    let last = "";
    const run = () =>
      load().then(
        (d) => {
          if (stale) return;
          const json = JSON.stringify(d);
          if (json !== last) setData(d);
          last = json;
          setError("");
        },
        (e: Error) => !stale && setError(e.message),
      );
    run();
    const timer = poll ? setInterval(() => !document.hidden && run(), poll) : undefined;
    return () => {
      stale = true;
      clearInterval(timer);
    };
  }, [version]);
  return [data, error, () => setVersion((v) => v + 1)];
}

export const POLL = 2000;
