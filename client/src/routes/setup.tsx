import { useState } from "react";
import { api, type NewKey } from "../api";
import { NarrowPage } from "../components/narrow-page";
import { ErrorText } from "../components/section";
import { Button } from "../ui/button";
import { CopyField } from "../ui/copy-field";
import { Field, Input, Labelled } from "../ui/input";

export function SetupRoute() {
  const token = new URLSearchParams(location.search).get("token") ?? "";
  const [name, setName] = useState("");
  const [key, setKey] = useState<NewKey>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  if (key) {
    return (
      <NarrowPage
        brand
        title={`${name} is an admin`}
        description="This key signs in as them. Copy it now, it's only shown once"
      >
        <div className="flex flex-col gap-6">
          <Labelled label="Key">
            <CopyField value={key.key} label="key" />
          </Labelled>
          <Button variant="primary" asChild className="w-full">
            <a href="/login">Sign in</a>
          </Button>
        </div>
      </NarrowPage>
    );
  }

  return (
    <NarrowPage
      brand
      title="Set up tailgate"
      description="Create the first admin, who can reach every service"
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          try {
            setKey(await api<NewKey>("POST", "/api/setup", { token, name }));
          } catch (err) {
            setError((err as Error).message);
          }
          setBusy(false);
        }}
      >
        <Field label="Name" hint="Lowercase letters, digits, dots, dashes and underscores">
          <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
        </Field>
        <ErrorText>{error}</ErrorText>
        <Button type="submit" variant="primary" loading={busy} className="mt-2 w-full">
          Create admin
        </Button>
      </form>
    </NarrowPage>
  );
}
