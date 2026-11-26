import type { FC, ReactNode } from "react";
import { cn } from "../lib/utils";

// a part of an admin page: a heading with its actions level with it on the right,
// an optional muted line, then what it holds
export const Section: FC<{
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
}> = ({ title, description, actions, children, className }) => (
  <section className={cn("flex flex-col gap-3", className)}>
    <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
      <div className="min-w-0">
        <h2 className="text-base">{title}</h2>
        {description && <p className="mt-0.5 text-[13px] text-fg-muted">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
    {children}
  </section>
);

/** rows in one bordered box, divided by 1px lines */
export const List: FC<{ children: ReactNode; className?: string }> = ({ children, className }) => (
  <ul className={cn("divide-y divide-line rounded-md border border-line", className)}>{children}</ul>
);

export const Row: FC<{ children: ReactNode; className?: string }> = ({ children, className }) => (
  <li className={cn("px-4 py-3", className)}>{children}</li>
);

export const Empty: FC<{ children: ReactNode }> = ({ children }) => (
  <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-fg-muted">
    {children}
  </p>
);

export const ErrorText: FC<{ children: ReactNode; className?: string }> = ({ children, className }) =>
  children ? <p className={cn("text-[13px] text-danger", className)}>{children}</p> : null;
