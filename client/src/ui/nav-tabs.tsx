import type { ComponentProps, FC } from "react";
import { NavLink } from "react-router";
import { cn } from "../lib/utils";
import { FOCUS_RING } from "./button";

// tabs that are places. the current one is filled, the others underline under the pointer
export const NavTab: FC<ComponentProps<typeof NavLink>> = ({ className, ...props }) => (
  <NavLink
    className={({ isActive }) =>
      cn(
        "flex h-8 shrink-0 items-center gap-1.5 rounded-md px-3 text-sm font-medium whitespace-nowrap underline-offset-4",
        FOCUS_RING,
        isActive ? "bg-raised text-fg" : "text-fg-muted hover:text-fg hover:underline",
        className as string,
      )
    }
    {...props}
  />
);
