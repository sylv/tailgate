import type { FC, ReactNode } from "react";
import { Wordmark } from "../ui/logo";

// the server puts how the visitor came and which request loaded the page in it
const { request, ingress }: { request?: string; ingress?: string } = JSON.parse(
  document.getElementById("tailgate")?.textContent || "{}",
);

// one narrow column in the middle of the window, a little above centre. only admins
// see the wordmark, everyone else sees the name of the app they're signing in to.
// the footer is for when a visitor needs help
export const NarrowPage: FC<{
  title?: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  brand?: boolean;
}> = ({ title, description, children, brand }) => (
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
    {request && (
      <footer className="pb-4 text-center text-xs text-fg-faint">
        request <span className="font-mono select-all">{request}</span> {ingress === "funnel" ? "via Funnel" : "via tailnet"},
      </footer>
    )}
  </div>
);
