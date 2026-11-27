import { useState, type ReactNode } from "react";
import { api, type NodeInfo, type App } from "../api";
import { PageHeader, useAdmin } from "../components/admin-shell";
import { Empty, List, Section } from "../components/section";
import { Note } from "../components/note";
import { nodeStatus, appStatus, StatusRing, type Status } from "../components/apps/status";
import { Until } from "../lib/time";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
import { CopyField } from "../ui/copy-field";
import { ExternalLink } from "../ui/external-link";

const COLUMNS = "md:grid-cols-[minmax(0,1fr)_9rem_5rem_1.25rem]";

export function AppsRoute() {
  const { apps } = useAdmin();
  if (!apps) return null;

  return (
    <>
      <PageHeader title="Apps" />
      <div className="mb-4 flex flex-col gap-2 empty:hidden">
        {apps.problems.map((p) => (
          <Note key={p} tone="danger">
            {p}
          </Note>
        ))}
      </div>
      <div className={cn("hidden gap-x-6 px-4 pb-2 text-xs text-fg-faint md:grid", COLUMNS)}>
        <span>Name</span>
        <span>Reachable on</span>
        <span>Login</span>
      </div>
      <List>
        <Row
          name="tailgate"
          url={apps.tailgate.url}
          details={["This page and the login"]}
          reach={apps.tailgate.funnelOn ? "Funnel" : "Tailnet"}
          status={nodeStatus(apps.tailgate)}
        >
          <NodeNotes node={apps.tailgate} />
        </Row>
        {apps.apps.map((s) => (
          <AppRow key={s.container} app={s} />
        ))}
      </List>
      {apps.apps.length === 0 && <Empty>No containers have tailgate labels yet</Empty>}
      {apps.unused.length > 0 && <LeftInTailscale names={apps.unused} />}
    </>
  );
}

function AppRow({ app: s }: { app: App }) {
  const details = [
    s.id && s.hostname !== s.id && `id ${s.id}`,
    `container ${s.container}${s.address ? `, ${s.address}` : s.error ? "" : ", not running"}`,
    !!s.tailscaleService && "Tailscale service on the tailgate node",
    !s.error &&
      s.funnel &&
      s.dangerouslyDisableAuth &&
      "Funnel enabled with login off, anyone on the internet can access it",
  ];
  return (
    <Row
      name={s.hostname || s.id || s.container}
      url={s.url}
      details={details}
      reach={s.error ? undefined : s.funnelOn ? "Funnel" : "Tailnet"}
      login={s.error ? undefined : s.dangerouslyDisableAuth ? "Off" : "On"}
      status={appStatus(s)}
    >
      {s.error && (
        <Note tone="danger">
          <span className="whitespace-pre-wrap">{s.error}</span>
        </Note>
      )}
      <NodeNotes node={s} id={s.id} />
    </Row>
  );
}

function Row({
  name,
  url,
  details,
  reach,
  login,
  status,
  children,
}: {
  name: string;
  url?: string;
  details: (string | false)[];
  reach?: "Funnel" | "Tailnet";
  login?: string;
  status: Status;
  children?: ReactNode;
}) {
  return (
    <li className="px-4 py-3">
      <div className={cn("grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-6", COLUMNS)}>
        <div className="min-w-0">
          {url ? (
            <ExternalLink href={url} className="text-fg">
              {name}
            </ExternalLink>
          ) : (
            <span>{name}</span>
          )}
          {details.filter(Boolean).map((d) => (
            <div key={d as string} className="truncate text-[13px] text-fg-muted">
              {d}
            </div>
          ))}
          {/* the columns' facts, on a phone where the columns are hidden */}
          {reach && (
            <div className="text-[13px] text-fg-muted md:hidden">
              {reach === "Funnel" ? "On Funnel" : "Tailnet only"}
              {login === "Off" && ", login off"}
            </div>
          )}
        </div>
        <span className="hidden text-[13px] text-fg-muted md:block">
          {reach === "Funnel" ? <span className="text-fg">Tailnet and Funnel</span> : reach}
        </span>
        <span className="hidden text-[13px] text-fg-muted md:block">{login}</span>
        <StatusRing status={status} />
      </div>
      {children && <div className="mt-2.5 flex flex-col gap-2.5 empty:hidden">{children}</div>}
    </li>
  );
}

// id is the app's, the tailgate node has none and logs back in by itself
function NodeNotes({ node, id }: { node: NodeInfo; id?: string }) {
  const { reloadApps } = useAdmin();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <>
      {node.state === "logged out" && id ? (
        <Note tone="warning">
          <span className="block">{node.nodeError}</span>
          <span className="mt-2 flex items-center gap-3">
            <Button
              size="sm"
              loading={busy}
              onClick={async () => {
                setBusy(true);
                setError("");
                try {
                  await api("POST", `/api/admin/apps/${encodeURIComponent(id)}/login`);
                  reloadApps();
                } catch (err) {
                  setError((err as Error).message);
                }
                setBusy(false);
              }}
            >
              Log in again
            </Button>
            {error && <span className="text-danger">{error}</span>}
          </span>
        </Note>
      ) : node.state === "needs signing" ? (
        <Note tone="warning">
          This node needs signing before it can serve requests. Run the command below on a signing node.{" "}
          <ExternalLink href="https://tailscale.com/kb/1226/tailnet-lock">Tailnet lock docs</ExternalLink>
        </Note>
      ) : node.state === "funnel not allowed" ||
        node.state === "wrong name" ||
        node.state === "needs approval" ? (
        <Note tone="warning">
          {node.nodeError || "Funnel isn't allowed for this node, so it's only on the tailnet"}
        </Note>
      ) : (
        node.nodeError && (
          <Note tone={node.state === "cert pending" ? "warning" : "danger"}>
            <span className="block">{node.nodeError}</span>
            {node.retryAt && (
              <span className="mt-1 block">
                Trying again <Until seconds={node.retryAt} />
              </span>
            )}
          </Note>
        )
      )}
      {node.state === "cert pending" && !node.nodeError && (
        <Note tone="info">Getting a certificate from Let's Encrypt, this usually takes under a minute</Note>
      )}
      {node.state === "needs signing" && node.signCommand && (
        <CopyField value={node.signCommand} label="command" />
      )}
    </>
  );
}

// nodes no container uses anymore, by their app's id. tailgate can't delete devices,
// so it waits for them to be deleted in the admin console and then forgets them
function LeftInTailscale({ names }: { names: string[] }) {
  return (
    <Section
      className="mt-8"
      title="Left in Tailscale"
      description="No container uses these nodes anymore. Delete their devices in the Tailscale admin console and tailgate forgets them"
    >
      <List>
        {names.map((name) => (
          <Row
            key={name}
            name={name}
            details={["Node"]}
            status={{ tone: "unused", icon: "alert", label: "Unused" }}
          >
            <ExternalLink href="https://login.tailscale.com/admin/machines" className="text-[13px]">
              Delete it in the admin console
            </ExternalLink>
          </Row>
        ))}
      </List>
    </Section>
  );
}
