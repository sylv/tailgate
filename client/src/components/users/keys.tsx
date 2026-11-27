import { useState, type FC } from "react";
import { api, type NewKey, type UserDetail } from "../../api";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { Confirm } from "../confirm";
import { ErrorText, List, Section } from "../section";
import { KeyRows, PasskeyRows } from "./passkeys";

export const Keys: FC<{ user: UserDetail; reload: () => void; onKey: (k: NewKey) => void }> = ({
  user,
  reload,
  onKey,
}) => {
  const [label, setLabel] = useState("");
  const [error, setError] = useState("");
  const [revoking, setRevoking] = useState<string>();
  const [removing, setRemoving] = useState<string>();

  return (
    <Section title="Keys and passkeys">
      {user.keys.length + user.passkeys.length > 0 && (
        <List>
          <PasskeyRows passkeys={user.passkeys} onRemove={(p) => setRemoving(p.id)} />
          <KeyRows keys={user.keys} onRevoke={(k) => setRevoking(k.id)} />
        </List>
      )}
      <form
        className="flex gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setError("");
          try {
            onKey(await api<NewKey>("POST", `/api/admin/users/${user.id}/keys`, { label }));
            setLabel("");
            reload();
          } catch (err) {
            setError((err as Error).message);
          }
        }}
      >
        <Input placeholder="Label, like iPad" value={label} onChange={(e) => setLabel(e.target.value)} />
        <Button type="submit">Add key</Button>
      </form>
      <ErrorText>{error}</ErrorText>
      <Confirm
        open={!!removing}
        onClose={() => setRemoving(undefined)}
        title="Remove this passkey?"
        description="Every session made with it ends"
        action="Remove"
        onConfirm={async () => {
          await api("DELETE", "/api/admin/passkeys/" + removing);
          reload();
        }}
      />
      <Confirm
        open={!!revoking}
        onClose={() => setRevoking(undefined)}
        title="Revoke this key?"
        description="Every session made with it ends"
        action="Revoke"
        onConfirm={async () => {
          await api("DELETE", "/api/admin/keys/" + revoking);
          reload();
        }}
      />
    </Section>
  );
};
