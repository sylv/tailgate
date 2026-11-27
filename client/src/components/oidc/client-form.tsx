import { useState, type FC } from "react";
import { api, type OIDCClient } from "../../api";
import { Button } from "../../ui/button";
import { CopyField } from "../../ui/copy-field";
import { Field, Input, Labelled, Textarea } from "../../ui/input";
import { Select } from "../../ui/select";
import { useAdmin } from "../admin-shell";
import { ErrorText } from "../section";

export type Created = { id: string; secret: string };

export const ClientForm: FC<{
  client: OIDCClient;
  onDone: (created?: Created) => void;
  onDelete?: () => void;
}> = ({ client, onDone, onDelete }) => {
  const { serviceIDs } = useAdmin();
  const [c, setC] = useState(client);
  const [uris, setURIs] = useState(client.redirectURIs.join("\n"));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const names = [...new Set([...serviceIDs, client.service].filter(Boolean))].toSorted();

  return (
    <form
      className="flex animate-enter flex-col gap-4"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        const redirectURIs = uris
          .split("\n")
          .map((u) => u.trim())
          .filter(Boolean);
        try {
          const res = await api<Created>(
            c.id ? "PUT" : "POST",
            "/api/admin/oidc-clients" + (c.id ? "/" + c.id : ""),
            { ...c, redirectURIs },
          );
          onDone(c.id ? undefined : res);
        } catch (err) {
          setError((err as Error).message);
          setBusy(false);
        }
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Name">
          <Input
            value={c.name}
            placeholder="Immich"
            onChange={(e) => setC({ ...c, name: e.target.value })}
            required
            autoFocus={!c.id}
          />
        </Field>
        <Labelled label="Service">
          <Select
            label="Service"
            placeholder="Choose a service"
            value={c.service || undefined}
            options={names.map((n) => ({ value: n, label: n }))}
            onValueChange={(service) => setC({ ...c, service })}
          />
        </Labelled>
      </div>
      {c.id && (
        <Labelled label="Client ID">
          <CopyField value={c.id} label="client ID" />
        </Labelled>
      )}
      <Field label="Redirect URIs" hint="One per line">
        <Textarea
          className="font-mono text-[13px]"
          value={uris}
          placeholder={"https://photos.example.ts.net/auth/login\napp.immich:///oauth-callback"}
          onChange={(e) => setURIs(e.target.value)}
          spellCheck={false}
          required
        />
      </Field>
      <div className="flex flex-wrap items-center justify-end gap-2">
        {onDelete && (
          <Button variant="ghost" className="mr-auto -ml-3.5 text-danger" onClick={onDelete}>
            Delete
          </Button>
        )}
        <ErrorText className={onDelete ? undefined : "mr-auto"}>{error}</ErrorText>
        <Button variant="ghost" onClick={() => onDone()}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" loading={busy} disabled={!c.service}>
          {c.id ? "Save" : "Add client"}
        </Button>
      </div>
    </form>
  );
};
