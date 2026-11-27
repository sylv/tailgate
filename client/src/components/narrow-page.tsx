import type { FC, ReactNode } from "react";
import type { Me } from "../api";
import { Wordmark } from "../ui/logo";

// one narrow column in the middle of the window, a little above centre. only admins
// see the wordmark, everyone else sees the name of the app they're signing in to
export const NarrowPage: FC<{
  title?: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  brand?: boolean;
  // me puts how the visitor got here and the request id in the footer, for when they need help
  me?: Me;
}> = ({ title, description, children, brand, me }) => (
  <main className="mx-auto flex min-h-dvh w-full max-w-sm flex-col justify-center px-4 pt-12 pb-[18vh]">
    {brand && (
      <a href="/" className="mb-10 w-fit">
        <Wordmark />
      </a>
    )}
    {title && <h1 className="text-xl">{title}</h1>}
    {description && <p className="mt-1 text-sm text-fg-muted">{description}</p>}
    {children && <div className="mt-6">{children}</div>}
    {me && (
      <footer className="fixed inset-x-0 bottom-4 px-4 text-center text-xs text-fg-faint">
        {me.ingress === "funnel" ? "Through Funnel" : "On the tailnet"}, request{" "}
        <span className="font-mono select-all">{me.requestId}</span>
      </footer>
    )}
  </main>
);
