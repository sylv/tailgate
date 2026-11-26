import { useState, type FC } from "react";
import { api, type NewKey, type UserDetail } from "../../api";
import { ago } from "../../lib/time";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { Confirm } from "../confirm";
import { ErrorText, List, Row, Section } from "../section";

export const Keys: FC<{ user: UserDetail; reload: () => void; onKey: (k: NewKey) => void }> = ({
  user,
  reload,
  onKey,
}) => {
  const [label, setLabel] = useState("");
  const [error, setError] = useState("");
  const [revoking, setRevoking] = useState<string>();

  return (
    <Section title="Keys">
      {user.keys.length > 0 && (
        <List>
          {user.keys.map((k) => (
            <Row key={k.id} className="flex items-center gap-3 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="truncate">{k.label || "Key"}</div>
                <div className="text-[13px] text-fg-muted">
                  Made {ago(k.createdAt)}, {k.lastUsedAt ? `used ${ago(k.lastUsedAt)}` : "never used"}
                </div>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="-mr-1.5 text-danger"
                onClick={() => setRevoking(k.id)}
              >
                Revoke
              </Button>
            </Row>
          ))}
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
