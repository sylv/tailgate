import { ArrowUpRightIcon } from "@phosphor-icons/react";
import { api, signOut, type Me } from "../api";
import { NarrowPage } from "../components/narrow-page";
import { Note } from "../components/note";
import { Empty, List } from "../components/section";
import { POLL, useLoad } from "../hooks/use-load";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

export function DashboardRoute() {
  const [me, error] = useLoad(() => api<Me>("GET", "/api/me"), POLL);

  if (error) return <NarrowPage title="Something went wrong" description={error} />;
  if (!me) return null;
  document.title = "Your services";
  if (!me.user) {
    location.replace("/login");
    return null;
  }

  return (
    <NarrowPage
      me={me}
      title={me.user.name}
      description={
        me.via === "tailnet" ? `Signed in through the tailnet as ${me.login}` : "Signed in with a key"
      }
    >
      {me.services.length === 0 ? (
        <Empty>You can't use any services yet</Empty>
      ) : (
        <List>
          {me.services.map((s) => (
            <li key={s.name}>
              <a
                href={s.url || undefined}
                className={cn(
                  "group flex h-11 items-center justify-between gap-4 px-4",
                  s.url ? "hover:bg-raised" : "pointer-events-none",
                )}
              >
                <span
                  className={cn(
                    "truncate underline-offset-2 group-hover:underline",
                    !s.url && "text-fg-muted",
                  )}
                >
                  {s.name}
                </span>
                {s.url ? (
                  <ArrowUpRightIcon className="size-4 shrink-0 text-fg-faint group-hover:text-fg" />
                ) : (
                  <span className="text-[13px] text-fg-faint">Starting</span>
                )}
              </a>
            </li>
          ))}
        </List>
      )}
      {me.user.admin && me.ingress === "funnel" && (
        <Note tone="info" className="mt-4">
          The admin page is only available on the tailnet. Open this page from a device on your tailnet to use
          it
        </Note>
      )}
      <div className="mt-4 flex justify-between">
        {me.user.admin && me.ingress !== "funnel" ? (
          <Button variant="link" asChild>
            <a href="/admin">Admin</a>
          </Button>
        ) : (
          <span />
        )}
        {me.via !== "tailnet" && (
          <Button variant="link" onClick={() => signOut("/login")}>
            Sign out
          </Button>
        )}
      </div>
    </NarrowPage>
  );
}
