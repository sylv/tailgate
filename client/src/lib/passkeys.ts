import { api } from "../api";
import aaguids from "./aaguids.json";

// go-webauthn sends options as { publicKey }, in the json form the browser parses itself
interface Begin<T> {
  id: string;
  options: { publicKey: T };
}

export const passkeysSupported = () => typeof PublicKeyCredential !== "undefined";

/** whether the device has a built in authenticator, like a fingerprint reader or screen lock */
export const platformAuthenticator = async () =>
  passkeysSupported() && (await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable());

/** signs in with a passkey the browser picks. conditional offers the passkeys in the key
 * field's autofill instead of asking, until signal aborts it */
export async function signInWithPasskey(conditional?: AbortSignal) {
  const { id, options } = await api<Begin<PublicKeyCredentialRequestOptionsJSON>>(
    "POST",
    "/api/passkeys/login",
  );
  const cred = await navigator.credentials.get({
    publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(options.publicKey),
    mediation: conditional ? "conditional" : undefined,
    signal: conditional,
  });
  if (!(cred instanceof PublicKeyCredential)) throw new Error("No passkey was picked");
  await api("POST", `/api/passkeys/login/${id}`, cred.toJSON());
}

export async function addPasskey() {
  const { id, options } = await api<Begin<PublicKeyCredentialCreationOptionsJSON>>("POST", "/api/passkeys");
  const cred = await navigator.credentials.create({
    publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options.publicKey),
  });
  if (!(cred instanceof PublicKeyCredential)) throw new Error("No passkey was made");
  await api("POST", `/api/passkeys/${id}`, cred.toJSON());
}

/** the authenticator's name, like "iCloud Keychain", from the public aaguid list */
export const passkeyName = (aaguid: string) => (aaguids as Record<string, string>)[aaguid] ?? "Passkey";

/** "Chrome on macOS" from a user agent */
export function device(ua: string) {
  const browser = [
    ["Edg/", "Edge"],
    ["Firefox/", "Firefox"],
    ["Chrome/", "Chrome"],
    ["Safari/", "Safari"],
  ].find(([s]) => ua.includes(s))?.[1];
  const os = [
    ["iPhone", "iPhone"],
    ["iPad", "iPad"],
    ["Android", "Android"],
    ["CrOS", "ChromeOS"],
    ["Mac OS X", "macOS"],
    ["Windows", "Windows"],
    ["Linux", "Linux"],
  ].find(([s]) => ua.includes(s))?.[1];
  return [browser, os].filter(Boolean).join(" on ");
}

/** a failed or cancelled browser prompt, which isn't worth an error */
export const cancelled = (err: unknown) =>
  err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError");
