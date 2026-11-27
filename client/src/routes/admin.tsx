import { useState } from "react";
import { Navigate, Route, Routes } from "react-router";
import { type Me, type Apps } from "../api";
import { AdminContext, AdminShell } from "../components/admin-shell";
import { needsAttention } from "../components/apps/status";
import { NarrowPage } from "../components/narrow-page";
import { PasskeyPrompt, SignInForm } from "../components/sign-in";
import { useApi } from "../hooks/use-api";
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
    if (prompt) return <PasskeyPrompt me={me} />;
    return (
      <NarrowPage
        brand
        me={me}
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
        <Routes>
          <Route index element={<Navigate to="users" replace />} />
          <Route path="users" element={<UsersRoute />} />
          <Route path="users/:id" element={<UserRoute />} />
          <Route path="apps" element={<AppsRoute />} />
          <Route path="rules" element={<RulesRoute />} />
          <Route path="oidc" element={<OIDCRoute />} />
          <Route path="*" element={<Navigate to="users" replace />} />
        </Routes>
      </AdminShell>
    </AdminContext>
  );
}
