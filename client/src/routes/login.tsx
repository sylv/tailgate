import { useState } from "react";
import { api, signOut, type Me } from "../api";
import { NarrowPage } from "../components/narrow-page";
import { ErrorText } from "../components/section";
import { useLoad } from "../hooks/use-load";
import { Button } from "../ui/button";
import { Field, Input } from "../ui/input";

export function LoginRoute() {
  const q = new URLSearchParams(location.search);
  // an app signing in through oidc names its service like ?to= does
  const target: Record<string, string> =
    location.pathname === "/oidc/authorize"
      ? { client: q.get("client_id") ?? "" }
      : { to: q.get("to") ?? "" };
  const [me, loadError] = useLoad(() => api<Me>("GET", "/api/me?" + new URLSearchParams(target)));
  const [key, setKey] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  if (loadError) return <NarrowPage title="Can't sign in" description={loadError} />;
  if (!me) return null;
  const service = me.target;
  const signIn = service ? `Sign in to ${service}` : "Sign in";
  document.title = me.error ? "Can't sign in" : me.user ? `No access to ${service ?? "this"}` : signIn;

  if (me.error) {
    return (
      <NarrowPage title="Can't sign in" description={me.error}>
        <Button variant="link" asChild>
          <a href="/">Go to your services</a>
        </Button>
      </NarrowPage>
    );
  }

  if (me.user) {
    return (
      <NarrowPage
        title={`No access to ${service ?? "this"}`}
        description={
          <>
            You're signed in as <span className="text-fg">{me.user.name}</span>, who can't use{" "}
            {service ?? "it"}
          </>
        }
      >
        <div className="flex items-center gap-4">
          {me.via !== "tailnet" && <Button onClick={() => signOut(location.href)}>Sign out</Button>}
          <Button variant="link" asChild>
            <a href="/">Your services</a>
          </Button>
        </div>
      </NarrowPage>
    );
  }

  return (
    <NarrowPage title={signIn} description="Use the key you were given">
      <form
        className="flex flex-col gap-4"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          try {
            await api("POST", "/api/login", { key });
            // the server sends a signed in visitor on to the service, or says why it can't
            location.reload();
          } catch (err) {
            setError((err as Error).message);
            setBusy(false);
          }
        }}
      >
        {/* password managers want a username to file the key under */}
        <input type="text" name="username" autoComplete="username" value="tailgate" readOnly hidden />
        <Field label="Key">
          <Input
            type="password"
            name="password"
            autoComplete="current-password"
            placeholder="tg_…"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            aria-invalid={!!error || undefined}
            required
            autoFocus
          />
        </Field>
        <ErrorText>{error}</ErrorText>
        <Button type="submit" variant="primary" loading={busy} className="mt-2 w-full">
          Sign in
        </Button>
      </form>
    </NarrowPage>
  );
}
