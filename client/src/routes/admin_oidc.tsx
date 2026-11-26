import { CaretDownIcon, PlusIcon } from "@phosphor-icons/react";
import { useState } from "react";
import { api, type OIDCClient, type OIDCClients } from "../api";
import { PageHeader } from "../components/admin-shell";
import { Confirm } from "../components/confirm";
import { ClientForm, type Created } from "../components/oidc/client-form";
import { Empty, ErrorText, List, Row } from "../components/section";
import { POLL, useLoad } from "../hooks/use-load";
import { Button } from "../ui/button";
import { CopyField } from "../ui/copy-field";
import { Dialog } from "../ui/dialog";
import { Labelled } from "../ui/input";

const blank: OIDCClient = { id: "", name: "", service: "", redirectURIs: [], createdAt: 0 };

export function OIDCRoute() {
  const [list, error, reload] = useLoad(() => api<OIDCClients>("GET", "/api/admin/oidc-clients"), POLL);
  // "new" while adding, else the id of the client being edited
  const [open, setOpen] = useState<string>();
  const [created, setCreated] = useState<Created>();
  const [deleting, setDeleting] = useState<OIDCClient>();

  const done = (c?: Created) => {
    setOpen(undefined);
    if (c) setCreated(c);
    reload();
  };

  return (
    <>
      <PageHeader
        title="OIDC"
        actions={
          <Button variant="primary" icon={<PlusIcon weight="bold" />} onClick={() => setOpen("new")}>
            Add client
          </Button>
        }
      />
      <ErrorText>{error}</ErrorText>
      {list && (
        <div className="flex flex-col gap-8">
          <Labelled label="Issuer URL" className="max-w-md">
            <CopyField value={list.issuer} label="issuer URL" />
          </Labelled>
          {list.clients.length === 0 && open !== "new" ? (
            <Empty>No clients yet</Empty>
          ) : (
            <List>
              {open === "new" && (
                <Row className="py-4">
                  <ClientForm client={blank} onDone={done} />
                </Row>
              )}
              {list.clients.map((c) =>
                open === c.id ? (
                  <Row key={c.id} className="py-4">
                    <ClientForm client={c} onDone={done} onDelete={() => setDeleting(c)} />
                  </Row>
                ) : (
                  <li key={c.id}>
                    <button
                      onClick={() => setOpen(c.id)}
                      className="group grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-x-6 px-4 py-3 text-left hover:bg-raised md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]"
                    >
                      <span className="min-w-0">
                        <span className="block truncate underline-offset-2 group-hover:underline">
                          {c.name}
                        </span>
                        <span className="block truncate text-[13px] text-fg-muted">For {c.service}</span>
                      </span>
                      <span className="hidden min-w-0 text-[13px] text-fg-muted md:block">
                        {c.redirectURIs.map((u) => (
                          <span key={u} className="block truncate font-mono text-xs">
                            {u}
                          </span>
                        ))}
                      </span>
                      <CaretDownIcon className="size-4 text-fg-faint group-hover:text-fg" />
                    </button>
                  </li>
                ),
              )}
            </List>
          )}
        </div>
      )}
      <Confirm
        open={!!deleting}
        onClose={() => setDeleting(undefined)}
        title={`Delete ${deleting?.name}?`}
        description="The app can't sign anyone in until it has a new client"
        action="Delete client"
        onConfirm={async () => {
          await api("DELETE", "/api/admin/oidc-clients/" + deleting!.id);
          done();
        }}
      />
      <Dialog
        open={!!created}
        onOpenChange={(o) => !o && setCreated(undefined)}
        title="Client added"
        description="Copy both into the app now, the secret is only shown once"
        footer={
          <Button variant="primary" onClick={() => setCreated(undefined)}>
            Done
          </Button>
        }
      >
        {created && (
          <div className="flex flex-col gap-3">
            <Labelled label="Client ID">
              <CopyField value={created.id} label="client ID" />
            </Labelled>
            <Labelled label="Client secret">
              <CopyField value={created.secret} label="client secret" />
            </Labelled>
          </div>
        )}
      </Dialog>
    </>
  );
}
