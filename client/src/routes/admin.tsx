import { Navigate, Route, Routes } from "react-router";
import { api, type Me, type Services } from "../api";
import { AdminContext, AdminShell } from "../components/admin-shell";
import { needsAttention } from "../components/services/status";
import { NarrowPage } from "../components/narrow-page";
import { POLL, useLoad } from "../hooks/use-load";
import { Button } from "../ui/button";
import { TooltipProvider } from "../ui/tooltip";
import { OIDCRoute } from "./admin_oidc";
import { RulesRoute } from "./admin_rules";
import { ServicesRoute } from "./admin_services";
import { UserRoute } from "./admin_user";
import { UsersRoute } from "./admin_users";

export default function AdminRoutes() {
  const [me, meError] = useLoad(() => api<Me>("GET", "/api/me"));
  const [services, servicesError, reloadServices] = useLoad(
    () => api<Services>("GET", "/api/admin/services"),
    POLL,
  );

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
          <a href="/">Your services</a>
        </Button>
      </NarrowPage>
    );
  }

  const serviceNames = services?.services.filter((s) => !s.error).map((s) => s.name) ?? [];
  const attention = !!services && needsAttention(services);
  return (
    <AdminContext value={{ me, services, reloadServices, serviceNames }}>
      <TooltipProvider>
        <AdminShell attention={attention} name={me.user.name} canSignOut={me.via !== "tailnet"}>
          {servicesError && <p className="mb-6 text-[13px] text-danger">{servicesError}</p>}
          <Routes>
            <Route index element={<Navigate to="users" replace />} />
            <Route path="users" element={<UsersRoute />} />
            <Route path="users/:id" element={<UserRoute />} />
            <Route path="services" element={<ServicesRoute />} />
            <Route path="rules" element={<RulesRoute />} />
            <Route path="oidc" element={<OIDCRoute />} />
            <Route path="*" element={<Navigate to="users" replace />} />
          </Routes>
        </AdminShell>
      </TooltipProvider>
    </AdminContext>
  );
}
