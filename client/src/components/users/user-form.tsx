import { useState, type FC } from "react";
import type { User } from "../../api";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import { Field, Input } from "../../ui/input";
import { useAdmin } from "../admin-shell";
import { ErrorText, Section } from "../section";

// who someone is and what they can reach. save only shows once something changed
export const UserForm: FC<{ user: User; onSave: (u: User) => Promise<void>; saveLabel: string }> = ({
  user,
  onSave,
  saveLabel,
}) => {
  const { serviceIDs } = useAdmin();
  const [u, setU] = useState(user);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // services that were granted but have no container right now stay listed
  const names = [...new Set([...serviceIDs, ...user.services])].toSorted();
  const dirty = !user.id || JSON.stringify(u) !== JSON.stringify(user);

  return (
    <form
      className="flex flex-col gap-10"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        try {
          await onSave(u);
        } catch (err) {
          setError((err as Error).message);
        }
        setBusy(false);
      }}
    >
      <Section title="Profile">
        <Field label="Name" hint="Lowercase letters, digits, dots, dashes and underscores">
          <Input
            value={u.name}
            onChange={(e) => setU({ ...u, name: e.target.value })}
            required
            autoFocus={!u.id}
          />
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Note">
            <Input
              value={u.note}
              placeholder="They're a little silly"
              onChange={(e) => setU({ ...u, note: e.target.value })}
            />
          </Field>
          <Field label="Email" hint="Optional, but needed to sign in to apps through OIDC">
            <Input type="email" value={u.email} onChange={(e) => setU({ ...u, email: e.target.value })} />
          </Field>
        </div>
      </Section>
      <Section title="Services">
        <Checkbox
          label="Every service"
          description={u.admin ? "Admins can reach every service" : "Including ones added later"}
          checked={u.allServices}
          disabled={u.admin}
          onCheckedChange={(allServices) => setU({ ...u, allServices })}
        />
        {!u.allServices && (
          <div className="grid animate-enter gap-x-4 gap-y-2.5 rounded-md border border-line p-4 sm:grid-cols-2">
            {names.length === 0 && <p className="text-[13px] text-fg-muted">No services yet</p>}
            {names.map((name) => (
              <Checkbox
                key={name}
                label={name}
                checked={u.services.includes(name)}
                onCheckedChange={(on) =>
                  setU({ ...u, services: on ? [...u.services, name] : u.services.filter((s) => s !== name) })
                }
              />
            ))}
          </div>
        )}
      </Section>
      {dirty && (
        <div className="flex animate-enter items-center justify-end gap-3">
          <ErrorText className="mr-auto">{error}</ErrorText>
          {user.id && (
            <Button variant="ghost" onClick={() => setU(user)}>
              Discard
            </Button>
          )}
          <Button type="submit" variant="primary" loading={busy}>
            {saveLabel}
          </Button>
        </div>
      )}
    </form>
  );
};
