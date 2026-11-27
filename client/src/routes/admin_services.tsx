import { useState, type ReactNode } from "react";
import { api, type NodeInfo, type Service } from "../api";
import { PageHeader, useAdmin } from "../components/admin-shell";
import { Confirm } from "../components/confirm";
import { Empty, List } from "../components/section";
import { Note } from "../components/note";
import { nodeStatus, serviceStatus, StatusRing, type Status } from "../components/services/status";
import { until } from "../lib/time";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
import { CopyField } from "../ui/copy-field";
import { ExternalLink } from "../ui/external-link";

const COLUMNS = "md:grid-cols-[minmax(0,1fr)_9rem_5rem_1.25rem]";

export function ServicesRoute() {
  const { services, reloadServices } = useAdmin();
  const [confirming, setConfirming] = useState(false);
  const [removed, setRemoved] = useState<string[]>();
  if (!services) return null;
  const funnel = services.services.some((s) => s.funnel && !s.error);

  return (
    <>
      <PageHeader
        title="Services"
        actions={
          services.unused.length > 0 && (
            <Button variant="danger" onClick={() => setConfirming(true)}>
              Remove unused nodes
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-col gap-2 empty:hidden">
        {services.problems.map((p) => (
          <Note key={p} tone="danger">
            {p}
          </Note>
        ))}
        {removed && removed.length > 0 && (
          <Note tone="info">
            Logged out {removed.join(", ")}. Tailscale keeps the devices, so delete them in its{" "}
            <ExternalLink href="https://login.tailscale.com/admin/machines" className="text-fg">
              admin console
            </ExternalLink>
          </Note>
        )}
      </div>
      <div className={cn("hidden gap-x-6 px-4 pb-2 text-xs text-fg-faint md:grid", COLUMNS)}>
        <span>Name</span>
        <span>Reachable on</span>
        <span>Login</span>
      </div>
      <List>
        <Row
          name="tailgate"
          url={services.tailgate.url}
          details={["This page and the login"]}
          reach={funnel ? "Funnel" : "Tailnet"}
          status={nodeStatus(services.tailgate)}
        >
          <NodeNotes node={services.tailgate} />
        </Row>
        {services.services.map((s) => (
          <ServiceRow key={s.container} service={s} />
        ))}
        {services.unused.map((name) => (
          <Row
            key={name}
            name={name}
            details={["Unused node"]}
            status={{ tone: "unused", icon: "alert", label: "Unused" }}
          >
            <Note tone="unused">No container uses this name, but its node is still signed in</Note>
          </Row>
        ))}
      </List>
      {services.services.length === 0 && <Empty>No containers have tailgate labels yet</Empty>}
      <Confirm
        open={confirming}
        onClose={() => setConfirming(false)}
        title="Remove unused nodes?"
        description={`${services.unused.join(", ")} will be logged out and their state deleted. Tailscale keeps the devices, so you may have to delete them in its admin console too`}
        action="Remove"
        onConfirm={async () => {
          setRemoved((await api<{ removed: string[] }>("POST", "/api/admin/remove-unused-nodes")).removed);
          reloadServices();
        }}
      />
    </>
  );
}

function ServiceRow({ service: s }: { service: Service }) {
  const details = [
    s.id && s.hostname !== s.id && `id ${s.id}`,
    `container ${s.container}${s.upstream ? `, ${s.upstream}` : s.error ? "" : ", not running"}`,
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
      reach={s.error ? undefined : s.funnel ? "Funnel" : "Tailnet"}
      login={s.error ? undefined : s.dangerouslyDisableAuth ? "Off" : "On"}
      status={serviceStatus(s)}
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

// id is the service's, the tailgate node has none and logs back in by itself
function NodeNotes({ node, id }: { node: NodeInfo; id?: string }) {
  const { reloadServices } = useAdmin();
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
                  await api("POST", `/api/admin/services/${encodeURIComponent(id)}/login`);
                  reloadServices();
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
            {node.retryAt && <span className="mt-1 block">Trying again {until(node.retryAt)}</span>}
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
