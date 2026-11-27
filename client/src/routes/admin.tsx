import { Navigate, Route, Routes } from "react-router";
import { api, type Me, type Apps } from "../api";
import { AdminContext, AdminShell } from "../components/admin-shell";
import { needsAttention } from "../components/apps/status";
import { NarrowPage } from "../components/narrow-page";
import { POLL, useLoad } from "../hooks/use-load";
import { Button } from "../ui/button";
import { TooltipProvider } from "../ui/tooltip";
import { OIDCRoute } from "./admin_oidc";
import { RulesRoute } from "./admin_rules";
import { AppsRoute } from "./admin_apps";
import { UserRoute } from "./admin_user";
import { UsersRoute } from "./admin_users";

export default function AdminRoutes() {
  const [me, meError] = useLoad(() => api<Me>("GET", "/api/me"));
  const [apps, appsError, reloadApps] = useLoad(() => api<Apps>("GET", "/api/admin/apps"), POLL);

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

  const appIDs = apps?.apps.filter((s) => !s.error).map((s) => s.id) ?? [];
  const attention = !!apps && needsAttention(apps);
  return (
    <AdminContext value={{ me, apps, reloadApps, appIDs }}>
      <TooltipProvider>
        <AdminShell attention={attention} name={me.user.name} canSignOut={me.via !== "tailnet"}>
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
      </TooltipProvider>
    </AdminContext>
  );
}
