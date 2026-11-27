import { lazy, StrictMode, Suspense, type FC } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/figtree/wght.css";
import "./globals.css";
import { followLinks, usePath } from "./lib/router";
import { DashboardRoute } from "./routes/dashboard";
import { LoginRoute } from "./routes/login";
import { setFavicon } from "./ui/logo";

// the admin pages are their own chunk, funnel visitors never load them
const AdminRoutes = lazy(() => import("./routes/admin"));
const SetupRoute = lazy(() => import("./routes/setup").then((m) => ({ default: m.SetupRoute })));

setFavicon();

// the pages, by the first part of their path, which the server serves the ui on too.
// links between them change page in place. the login pages aren't in here on purpose:
// GET /login sends someone already signed in straight on to the app they came from (or
// home), which only the server can do, so links to it load it from the server
const pages = new Map<string, [title: string, page: FC]>([
  ["", ["Your apps", () => <DashboardRoute methods={false} />]],
  ["sign-in-methods", ["Sign-in methods", () => <DashboardRoute methods />]],
  ["setup", ["tailgate", SetupRoute]],
  ["admin", ["tailgate", AdminRoutes]],
]);
const pageOf = (path: string) => pages.get(path.split("/")[1]);

followLinks((path) => !!pageOf(path));

function App() {
  const [title, Page] = pageOf(usePath()) ?? ["Sign in", LoginRoute];
  // the login page names the app once it knows it
  document.title = title;
  return <Page />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Suspense>
      <App />
    </Suspense>
  </StrictMode>,
);
