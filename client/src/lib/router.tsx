import { useEffect, useSyncExternalStore } from "react";

// pushState doesn't fire popstate, so navigate fires it for usePath to hear
const subscribe = (fn: () => void) => {
  addEventListener("popstate", fn);
  return () => removeEventListener("popstate", fn);
};

export const usePath = () => useSyncExternalStore(subscribe, () => location.pathname);

export function navigate(to: string, replace = false) {
  history[replace ? "replaceState" : "pushState"](null, "", to);
  dispatchEvent(new PopStateEvent("popstate"));
}

export const Redirect = ({ to }: { to: string }) => {
  useEffect(() => navigate(to, true), [to]);
  return null;
};

/** makes plain links to the pages isPage says yes to change page without loading it again */
export function followLinks(isPage: (path: string) => boolean) {
  addEventListener("click", (e) => {
    const a = (e.target as Element).closest?.("a");
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)
      return;
    if (a.target || a.hasAttribute("download") || a.origin !== location.origin || !isPage(a.pathname)) return;
    e.preventDefault();
    navigate(a.pathname + a.search);
  });
}
