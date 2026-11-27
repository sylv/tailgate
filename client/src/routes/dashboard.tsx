import { ArrowUpRightIcon } from "@phosphor-icons/react";
import { useLayoutEffect, useRef, useState, type FC, type ReactNode } from "react";
import { signOut, type Me } from "../api";
import { NarrowPage } from "../components/narrow-page";
import { Note } from "../components/note";
import { Empty, ErrorText } from "../components/section";
import { Methods } from "../components/users/methods";
import { useApi } from "../hooks/use-api";
import { addPasskey, cancelled, passkeysSupported } from "../lib/passkeys";
import { clsx } from "clsx";
import { Button } from "../ui/button";

export function DashboardRoute({ methods }: { methods: boolean }) {
  const [me, error, reload] = useApi<Me>("/api/me", true);

  if (error) return <NarrowPage title="Something went wrong" description={error} />;
  if (!me) return null;
  if (!me.user) {
    location.replace("/login");
    return null;
  }

  return (
    <NarrowPage
      title={me.user.name}
      description={me.via === "tailnet" ? `Signed in through the tailnet as ${me.login}` : "Signed in"}
    >
      {methods ? (
        <SignInMethods me={me} reload={reload} />
      ) : me.apps.length === 0 ? (
        <Empty>You can't use any apps yet</Empty>
      ) : (
        <ScrollList>
          {me.apps.map((s) => (
            <li key={s.name}>
              <a
                href={s.why ? undefined : s.url}
                className={clsx(
                  "group flex h-11 items-center justify-between gap-4 px-4",
                  s.why ? "pointer-events-none" : "hover:bg-raised",
                )}
              >
                <span
                  className={clsx(
                    "truncate underline-offset-2 group-hover:underline",
                    s.why && "text-fg-muted",
                  )}
                >
                  {s.name}
                </span>
                {s.why ? (
                  <span className="shrink-0 text-[13px] text-fg-faint">{s.why}</span>
                ) : (
                  <ArrowUpRightIcon className="size-4 shrink-0 text-fg-faint group-hover:text-fg" />
                )}
              </a>
            </li>
          ))}
        </ScrollList>
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
        {(methods || hasMethods(me)) && (
          <Button variant="link" asChild>
            <a href={methods ? "/" : "/sign-in-methods"}>{methods ? "Apps" : "Sign-in methods"}</a>
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

const canAddPasskey = (me: Me) => me.canAddPasskey && passkeysSupported();
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
      <Methods keys={keys} passkeys={passkeys} reload={reload} list={ScrollList} />
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

// a list that stops growing before it pushes the page past the window, and fades
// the edge it has more rows past
const ScrollList: FC<{ children: ReactNode }> = ({ children }) => {
  const ref = useRef<HTMLUListElement>(null);
  const [fade, setFade] = useState({ top: false, bottom: false });
  const update = () => {
    const el = ref.current;
    if (!el) return;
    const top = el.scrollTop > 0;
    const bottom = el.scrollTop + el.clientHeight < el.scrollHeight - 1;
    setFade((f) => (f.top === top && f.bottom === bottom ? f : { top, bottom }));
  };
  // rows coming and going don't resize the list once it's at its max height
  useLayoutEffect(update);
  useLayoutEffect(() => {
    const observer = new ResizeObserver(update);
    observer.observe(ref.current!);
    return () => observer.disconnect();
  }, []);
  return (
    <div className="rounded-md border border-line">
      <ul
        ref={ref}
        onScroll={update}
        className="max-h-[max(8rem,calc(82dvh_-_8rem))] divide-y divide-line overflow-y-auto"
        style={{
          maskImage: `linear-gradient(to bottom, ${fade.top ? "transparent" : "black"}, black 2rem, black calc(100% - 2rem), ${fade.bottom ? "transparent" : "black"})`,
        }}
      >
        {children}
      </ul>
    </div>
  );
};
