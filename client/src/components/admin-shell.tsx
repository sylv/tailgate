import { SignOutIcon, WarningCircleIcon } from "@phosphor-icons/react";
import { createContext, useContext, type FC, type ReactNode } from "react";
import { signOut, type Me, type Apps } from "../api";
import { IconButton } from "../ui/icon-button";
import { Wordmark } from "../ui/logo";
import { NavTab } from "../ui/nav-tabs";

export interface AdminData {
  me: Me;
  apps: Apps | undefined;
  reloadApps: () => void;
  /** apps that are routed now, for pickers */
  appIDs: string[];
}

export const AdminContext = createContext<AdminData | null>(null);
export const useAdmin = () => useContext(AdminContext)!;

const tabs = [
  ["users", "Users"],
  ["apps", "Apps"],
  ["rules", "Rules"],
  ["oidc", "OIDC"],
] as const;

// the wordmark, the tabs in the middle and who's signed in, over a centred page
export const AdminShell: FC<{
  attention: boolean;
  name: string;
  children: ReactNode;
}> = ({ attention, name, children }) => (
  <div className="flex min-h-dvh flex-col">
    <header className="mx-auto grid w-full max-w-5xl grid-cols-[1fr_auto] items-center gap-y-3 px-4 pt-5 md:grid-cols-[1fr_auto_1fr] md:px-8">
      <a href="/" className="w-fit">
        <Wordmark />
      </a>
      <nav
        aria-label="Admin"
        className="no-scrollbar col-span-2 row-start-2 -mx-4 flex gap-0.5 overflow-x-auto px-4 md:col-span-1 md:col-start-2 md:row-start-1 md:mx-0 md:px-0"
      >
        {tabs.map(([path, label]) => (
          <NavTab key={path} to={`/admin/${path}`}>
            {label}
            {path === "apps" && attention && (
              <WarningCircleIcon weight="fill" className="size-4 text-warning" aria-label="Needs attention" />
            )}
          </NavTab>
        ))}
      </nav>
      <div className="flex items-center gap-1 justify-self-end md:col-start-3 md:row-start-1">
        <a href="/" className="text-[13px] text-fg-muted underline-offset-2 hover:text-fg hover:underline">
          {name}
        </a>
        <IconButton label="Sign out" size="sm" className="-mr-1.5" onClick={() => signOut("/login")}>
          <SignOutIcon />
        </IconButton>
      </div>
    </header>
    <main className="mx-auto w-full max-w-5xl flex-1 px-4 pt-10 pb-24 md:px-8">{children}</main>
  </div>
);

/** a page's title, with its actions level with it on the right */
export const PageHeader: FC<{ title: ReactNode; description?: ReactNode; actions?: ReactNode }> = ({
  title,
  description,
  actions,
}) => (
  <div className="mb-8 flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
    <div className="min-w-0">
      <h1 className="text-2xl">{title}</h1>
      {description && <p className="mt-1 max-w-xl text-sm text-fg-muted">{description}</p>}
    </div>
    {actions && <div className="flex items-center gap-2">{actions}</div>}
  </div>
);
