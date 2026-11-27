import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import "@fontsource-variable/figtree/wght.css";
import "./globals.css";
import { DashboardRoute } from "./routes/dashboard";
import { LoginRoute } from "./routes/login";
import { setFavicon } from "./ui/logo";
import { TooltipProvider } from "./ui/tooltip";

// the admin pages are their own chunk, funnel visitors never load them
const AdminRoutes = lazy(() => import("./routes/admin"));
const SetupRoute = lazy(() => import("./routes/setup").then((m) => ({ default: m.SetupRoute })));

setFavicon();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <TooltipProvider>
        <Suspense>
          <Routes>
            <Route path="/" element={<DashboardRoute />} />
            <Route path="/login" element={<LoginRoute />} />
            <Route path="/oidc/authorize" element={<LoginRoute />} />
            <Route path="/setup" element={<SetupRoute />} />
            <Route path="/admin/*" element={<AdminRoutes />} />
          </Routes>
        </Suspense>
      </TooltipProvider>
    </BrowserRouter>
  </StrictMode>,
);
