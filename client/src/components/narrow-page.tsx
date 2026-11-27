import { useState, type FC, type ReactNode } from "react";
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
  <div className="mx-auto flex min-h-dvh w-full max-w-sm flex-col px-4">
    <main className="my-auto pt-12 pb-[18vh]">
      {brand && (
        <a href="/" className="mb-10 block w-fit">
          <Wordmark />
        </a>
      )}
      {title && <h1 className="text-xl">{title}</h1>}
      {description && <p className="mt-1 text-sm text-fg-muted">{description}</p>}
      {children && <div className="mt-6">{children}</div>}
    </main>
    {me && <Footer me={me} />}
  </div>
);

// the request id is the page's first, live reloads would change it
const Footer: FC<{ me: Me }> = ({ me }) => {
  const [requestId] = useState(me.requestId);
  return (
    <footer className="pb-4 text-center text-xs text-fg-faint">
      {me.ingress === "funnel" ? "Through Funnel" : "On the tailnet"}, request{" "}
      <span className="font-mono select-all">{requestId}</span>
    </footer>
  );
};
