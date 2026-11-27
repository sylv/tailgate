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
  const { appIDs } = useAdmin();
  const [u, setU] = useState(user);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // apps that were granted but have no container right now stay listed
  const names = [...new Set([...appIDs, ...user.apps])].toSorted();
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
      <Section title="Apps">
        <Checkbox
          label="Every app"
          description={u.admin ? "Admins can reach every app" : "Including ones added later"}
          checked={u.allApps}
          disabled={u.admin}
          onCheckedChange={(allApps) => setU({ ...u, allApps })}
        />
        {!u.allApps && (
          <div className="grid animate-enter gap-x-4 gap-y-2.5 rounded-md border border-line p-4 sm:grid-cols-2">
            {names.length === 0 && <p className="text-[13px] text-fg-muted">No apps yet</p>}
            {names.map((name) => (
              <Checkbox
                key={name}
                label={name}
                checked={u.apps.includes(name)}
                onCheckedChange={(on) =>
                  setU({ ...u, apps: on ? [...u.apps, name] : u.apps.filter((s) => s !== name) })
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
