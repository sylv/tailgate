import { useState, type FC } from "react";
import { api, type Login, type UserDetail } from "../../api";
import { Button } from "../../ui/button";
import { Select } from "../../ui/select";
import { ErrorText, List, Row, Section } from "../section";

export const Logins: FC<{ user: UserDetail; reload: () => void }> = ({ user, reload }) => {
  const [error, setError] = useState("");
  // the tailnet's logins are only read when the picker opens
  const [logins, setLogins] = useState<Login[]>();
  const linkable = logins?.filter((l) => !user.logins.some((ul) => ul.id === l.id)) ?? [];

  async function run(f: () => Promise<unknown>) {
    setError("");
    try {
      await f();
      reload();
    } catch (err) {
      setError((err as Error).message);
    }
  }

  return (
    <Section
      title="Tailnet logins"
      description={`Untagged devices owned by these Tailscale users are signed in as ${user.name || "this user"} automatically, without a key`}
    >
      {user.logins.length > 0 && (
        <List>
          {user.logins.map((l) => (
            <Row key={l.id} className="flex items-center gap-3 py-2!">
              <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{l.name}</span>
              <Button
                variant="ghost"
                size="sm"
                className="-mr-1.5"
                onClick={() => run(() => api("DELETE", "/api/admin/logins/" + l.id))}
              >
                Unlink
              </Button>
            </Row>
          ))}
        </List>
      )}
      <Select
        label="Link a login"
        placeholder="Link a login"
        value={undefined}
        className="w-fit"
        options={linkable.map((l) => ({ value: l.id, label: l.name }))}
        empty={logins ? "No other logins on the tailnet" : "Loading…"}
        onOpenChange={(open) =>
          open &&
          api<Login[]>("GET", "/api/admin/logins").then(setLogins, (err: Error) => setError(err.message))
        }
        onValueChange={(id) =>
          run(() =>
            api(
              "POST",
              `/api/admin/users/${user.id}/logins`,
              linkable.find((l) => l.id === id),
            ),
          )
        }
      />
      <ErrorText>{error}</ErrorText>
    </Section>
  );
};
