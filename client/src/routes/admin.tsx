import { useState } from "react";
import { type Me, type Apps } from "../api";
import { AdminContext, AdminShell } from "../components/admin-shell";
import { needsAttention } from "../components/apps/status";
import { NarrowPage } from "../components/narrow-page";
import { PasskeyPrompt, SignInForm } from "../components/sign-in";
import { useApi } from "../hooks/use-api";
import { Redirect, usePath } from "../lib/router";
import { Button } from "../ui/button";
import { OIDCRoute } from "./admin_oidc";
import { RulesRoute } from "./admin_rules";
import { AppsRoute } from "./admin_apps";
import { UserRoute } from "./admin_user";
import { UsersRoute } from "./admin_users";

export default function AdminRoutes() {
  const [me, meError] = useApi<Me>("/api/me");
  const [prompt, setPrompt] = useState(false);

  if (meError) return <NarrowPage title="Something went wrong" description={meError} />;
  if (!me) return null;
  if (!me.user) {
    location.replace("/login");
    return null;
  }
  if (!me.user.admin) {
    return (
      <NarrowPage
        brand
        title="Admins only"
        description={`You're signed in as ${me.user.name}, who isn't an admin`}
      >
        <Button variant="link" asChild>
          <a href="/">Your apps</a>
        </Button>
      </NarrowPage>
    );
  }
  // a tailnet login is any device its owner left unlocked, so changing tailgate takes a key
  if (me.via === "tailnet") {
    if (prompt) return <PasskeyPrompt />;
    return (
      <NarrowPage
        brand
        title="Sign in to use the admin pages"
        description={`You're on the tailnet as ${me.login}. The admin pages need your key or a passkey too`}
      >
        <SignInForm me={me} offerPasskey={() => setPrompt(true)} />
      </NarrowPage>
    );
  }
  return <Admin me={me} />;
}

function Admin({ me }: { me: Me }) {
  const [apps, appsError, reloadApps] = useApi<Apps>("/api/admin/apps", true);
  const appIDs = apps?.apps.filter((s) => !s.error).map((s) => s.id) ?? [];
  const attention = !!apps && needsAttention(apps);
  return (
    <AdminContext value={{ me, apps, reloadApps, appIDs }}>
      <AdminShell attention={attention} name={me.user!.name}>
        {appsError && <p className="mb-6 text-[13px] text-danger">{appsError}</p>}
        <Page />
      </AdminShell>
    </AdminContext>
  );
}

function Page() {
  const [, , page, id] = usePath().split("/");
  switch (page) {
    case "users":
      return id ? <UserRoute id={id} /> : <UsersRoute />;
    case "apps":
      return <AppsRoute />;
    case "rules":
      return <RulesRoute />;
    case "oidc":
      return <OIDCRoute />;
  }
  return <Redirect to="/admin/users" />;
}
