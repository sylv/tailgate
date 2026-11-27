import { useState, type FC } from "react";
import { api, type NewKey, type UserDetail } from "../../api";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { ErrorText, List, Section } from "../section";
import { Methods } from "./passkeys";

export const Keys: FC<{ user: UserDetail; reload: () => void; onKey: (k: NewKey) => void }> = ({
  user,
  reload,
  onKey,
}) => {
  const [label, setLabel] = useState("");
  const [error, setError] = useState("");

  return (
    <Section title="Keys and passkeys">
      <Methods keys={user.keys} passkeys={user.passkeys} reload={reload} list={List} />
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
    </Section>
  );
};
