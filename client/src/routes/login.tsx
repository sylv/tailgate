import { useState } from "react";
import { signOut, type Me } from "../api";
import { NarrowPage } from "../components/narrow-page";
import { PasskeyPrompt, SignInForm } from "../components/sign-in";
import { useApi } from "../hooks/use-api";
import { Button } from "../ui/button";

export function LoginRoute() {
  const q = new URLSearchParams(location.search);
  // an app signing in through oidc names its app like ?to= does
  const target: Record<string, string> =
    location.pathname === "/oidc/authorize"
      ? { client: q.get("client_id") ?? "" }
      : { to: q.get("to") ?? "" };
  const [me, loadError] = useApi<Me>("/api/me?" + new URLSearchParams(target));
  const [prompt, setPrompt] = useState(false);

  if (loadError) return <NarrowPage title="Can't sign in" description={loadError} />;
  if (!me) return null;
  const app = me.target;
  const signIn = app ? `Sign in to ${app}` : "Sign in";
  document.title = me.error ? "Can't sign in" : me.user ? `No access to ${app ?? "this"}` : signIn;

  if (me.error) {
    return (
      <NarrowPage title="Can't sign in" description={me.error} me={me}>
        <Button variant="link" asChild>
          <a href="/">Go to your apps</a>
        </Button>
      </NarrowPage>
    );
  }

  if (me.user) {
    return (
      <NarrowPage
        me={me}
        title={`No access to ${app ?? "this"}`}
        description={
          <>
            You're signed in as <span className="text-fg">{me.user.name}</span>, who can't use {app ?? "it"}
          </>
        }
      >
        <div className="flex items-center gap-4">
          {me.via !== "tailnet" && <Button onClick={() => signOut(location.href)}>Sign out</Button>}
          <Button variant="link" asChild>
            <a href="/">Your apps</a>
          </Button>
        </div>
      </NarrowPage>
    );
  }

  if (prompt) return <PasskeyPrompt me={me} />;

  return (
    <NarrowPage
      title={signIn}
      description={me.passkeysOn ? "Use a passkey or the key you were given" : "Use the key you were given"}
      me={me}
    >
      <SignInForm me={me} offerPasskey={() => setPrompt(true)} />
    </NarrowPage>
  );
}
