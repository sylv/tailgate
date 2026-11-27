import { ArrowUpRightIcon } from "@phosphor-icons/react";
import { useState } from "react";
import { api, signOut, type Me } from "../api";
import { NarrowPage } from "../components/narrow-page";
import { Note } from "../components/note";
import { Empty, ErrorText, List } from "../components/section";
import { KeyRows, PasskeyRows } from "../components/users/passkeys";
import { useLoad } from "../hooks/use-load";
import { addPasskey, cancelled, passkeysSupported } from "../lib/passkeys";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

export function DashboardRoute() {
  const [me, error, reload] = useLoad(() => api<Me>("GET", "/api/me"), true);
  const [methods, setMethods] = useState(false);

  if (error) return <NarrowPage title="Something went wrong" description={error} />;
  if (!me) return null;
  document.title = "Your apps";
  if (!me.user) {
    location.replace("/login");
    return null;
  }

  return (
    <NarrowPage
      me={me}
      title={me.user.name}
      description={me.via === "tailnet" ? `Signed in through the tailnet as ${me.login}` : "Signed in"}
    >
      {methods ? (
        <SignInMethods me={me} reload={reload} />
      ) : me.apps.length === 0 ? (
        <Empty>You can't use any apps yet</Empty>
      ) : (
        <List>
          {me.apps.map((s) => {
            // funnel visitors can't reach a tailnet only app, the link would time out
            const why = !s.url ? "Starting" : me.ingress === "funnel" && !s.funnel ? "Tailnet only" : "";
            return (
              <li key={s.name}>
                <a
                  href={why ? undefined : s.url}
                  className={cn(
                    "group flex h-11 items-center justify-between gap-4 px-4",
                    why ? "pointer-events-none" : "hover:bg-raised",
                  )}
                >
                  <span
                    className={cn(
                      "truncate underline-offset-2 group-hover:underline",
                      why && "text-fg-muted",
                    )}
                  >
                    {s.name}
                  </span>
                  {why ? (
                    <span className="shrink-0 text-[13px] text-fg-faint">{why}</span>
                  ) : (
                    <ArrowUpRightIcon className="size-4 shrink-0 text-fg-faint group-hover:text-fg" />
                  )}
                </a>
              </li>
            );
          })}
        </List>
      )}
      {me.user.admin && me.ingress === "funnel" && (
        <Note tone="info" className="mt-4">
          The admin page is only available on the tailnet. Open this page from a device on your tailnet to use
          it
        </Note>
      )}
      <div className="mt-4 flex gap-4">
        {me.user.admin && me.ingress !== "funnel" && (
          <Button variant="link" asChild>
            <a href="/admin">Admin</a>
          </Button>
        )}
        {hasMethods(me) && (
          <Button variant="link" onClick={() => setMethods(!methods)}>
            {methods ? "Apps" : "Sign-in methods"}
          </Button>
        )}
        <span className="flex-1" />
        {me.via !== "tailnet" && (
          <Button variant="link" onClick={() => signOut("/login")}>
            Sign out
          </Button>
        )}
      </div>
    </NarrowPage>
  );
}

const canAddPasskey = (me: Me) => me.passkeysOn && me.via === "session" && passkeysSupported();
const hasMethods = (me: Me) => (me.keys?.length ?? 0) + (me.passkeys?.length ?? 0) > 0 || canAddPasskey(me);

// how the user signs in, and a way to add a passkey from a key or passkey session
function SignInMethods({ me, reload }: { me: Me; reload: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const keys = me.keys ?? [];
  const passkeys = me.passkeys ?? [];
  const canAdd = canAddPasskey(me);
  return (
    <section className="flex flex-col gap-3">
      {keys.length + passkeys.length > 0 && (
        <List>
          <PasskeyRows passkeys={passkeys} />
          <KeyRows keys={keys} />
        </List>
      )}
      {canAdd && (
        <Button
          className="w-fit"
          loading={busy}
          onClick={async () => {
            setBusy(true);
            setError("");
            try {
              await addPasskey();
              reload();
            } catch (err) {
              if (!cancelled(err)) setError((err as Error).message);
            }
            setBusy(false);
          }}
        >
          Add a passkey
        </Button>
      )}
      <ErrorText>{error}</ErrorText>
    </section>
  );
}
