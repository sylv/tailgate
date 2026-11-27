import { useEffect, useRef, useState, type FC } from "react";
import { api, type Me } from "../api";
import {
  addPasskey,
  cancelled,
  passkeysSupported,
  platformAuthenticator,
  signInWithPasskey,
} from "../lib/passkeys";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Field, Input } from "../ui/input";
import { NarrowPage } from "./narrow-page";
import { ErrorText } from "./section";

// the server sends a signed in visitor on to the app, or says why it can't
const done = () => location.reload();

/** a passkey button over the key field. offerPasskey is called after a key sign in
 * when the user should be asked to add a passkey */
export const SignInForm: FC<{ me: Me; offerPasskey: () => void }> = ({ me, offerPasskey }) => {
  const [key, setKey] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // the browser takes one passkey request at a time, so the button stops autofill's and
  // bumps attempt to start it again once its own prompt ends
  const [attempt, setAttempt] = useState(0);
  const autofill = useRef<AbortController>(undefined);
  const passkeys = me.passkeysOn && passkeysSupported();

  // passkeys show up in the key field's autofill while the page is open
  useEffect(() => {
    if (!passkeys) return;
    const abort = new AbortController();
    autofill.current = abort;
    PublicKeyCredential.isConditionalMediationAvailable?.().then((ok) => {
      if (!ok || abort.signal.aborted) return;
      signInWithPasskey(abort.signal).then(
        done,
        (err) => !cancelled(err) && setError((err as Error).message),
      );
    });
    return () => abort.abort();
  }, [passkeys, attempt]);

  async function withPasskey() {
    autofill.current?.abort();
    setError("");
    try {
      await signInWithPasskey();
      done();
    } catch (err) {
      if (!cancelled(err)) setError((err as Error).message);
      setAttempt((a) => a + 1);
    }
  }

  return (
    <div className="flex flex-col gap-6">
      {passkeys && (
        <Button className="w-full" onClick={withPasskey}>
          Sign in with a passkey
        </Button>
      )}
      <form
        className="flex flex-col gap-4"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          try {
            const res = await api<{ offerPasskey: boolean }>("POST", "/api/login", { key });
            if (res.offerPasskey && (await platformAuthenticator())) offerPasskey();
            else done();
          } catch (err) {
            setError((err as Error).message);
            setBusy(false);
          }
        }}
      >
        {/* password managers want a username to file the key under */}
        <input type="text" name="username" autoComplete="username" value="tailgate" readOnly hidden />
        <Field label="Key">
          <Input
            type="password"
            name="password"
            autoComplete="current-password webauthn"
            placeholder="tg_…"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            aria-invalid={!!error || undefined}
            required
            autoFocus={!passkeys}
          />
        </Field>
        <ErrorText>{error}</ErrorText>
        <Button type="submit" variant="primary" loading={busy} className="mt-2 w-full">
          Sign in
        </Button>
      </form>
    </div>
  );
};

/** asks to add a passkey after a key sign in, then carries on */
export const PasskeyPrompt: FC = () => {
  const [never, setNever] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <NarrowPage
      title="Add a passkey?"
      description="Next time, sign in on this device with your fingerprint, face or screen lock instead of your key"
    >
      <div className="flex flex-col gap-4">
        <Button
          variant="primary"
          loading={busy}
          className="w-full"
          onClick={async () => {
            setBusy(true);
            setError("");
            try {
              await addPasskey();
              done();
            } catch (err) {
              if (!cancelled(err)) setError((err as Error).message);
              setBusy(false);
            }
          }}
        >
          Add a passkey
        </Button>
        <ErrorText>{error}</ErrorText>
        <div className="flex items-center justify-between gap-4">
          <Checkbox label="Don't ask again" checked={never} onCheckedChange={setNever} />
          <Button
            variant="link"
            onClick={async () => {
              if (never) await api("POST", "/api/passkeys/skip").catch(() => {});
              done();
            }}
          >
            Not now
          </Button>
        </div>
      </div>
    </NarrowPage>
  );
};
