import { clsx } from "clsx";
import type { FC, ReactNode } from "react";
import { usePath } from "../lib/router";
import { FOCUS_RING } from "./button";

// tabs that are places. the current one is filled, the others underline under the pointer
export const NavTab: FC<{ href: string; children: ReactNode }> = ({ href, children }) => {
  const path = usePath();
  const active = path === href || path.startsWith(href + "/");
  return (
    <a
      href={href}
      aria-current={active ? "page" : undefined}
      className={clsx(
        "flex h-8 shrink-0 items-center gap-1.5 rounded-md px-3 text-sm font-medium whitespace-nowrap underline-offset-4",
        FOCUS_RING,
        active ? "bg-raised text-fg" : "text-fg-muted hover:text-fg hover:underline",
      )}
    >
      {children}
    </a>
  );
};
