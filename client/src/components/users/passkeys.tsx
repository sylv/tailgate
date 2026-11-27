import { FingerprintIcon, KeyIcon } from "@phosphor-icons/react";
import type { FC } from "react";
import type { Key, Passkey } from "../../api";
import { Ago } from "../../lib/time";
import { device, passkeyName } from "../../lib/passkeys";
import { Button } from "../../ui/button";
import { Tooltip } from "../../ui/tooltip";
import { Row } from "../section";

/** "made 3 days ago, used just now" */
export const Used: FC<{ verb: string; created: number; used: number | null }> = ({ verb, created, used }) => (
  <>
    {verb} <Ago seconds={created} />,{" "}
    {used ? (
      <>
        used <Ago seconds={used} />
      </>
    ) : (
      "never used"
    )}
  </>
);

/** a sign-in method in a list, with a button for admins. the dashboard shows them too, so
 * the confirm dialog stays in the admin pages' chunk */
const MethodRow: FC<{
  passkey: boolean;
  name: string;
  detail: React.ReactNode;
  action?: string;
  onAction?: () => void;
}> = ({ passkey, name, detail, action, onAction }) => {
  const Icon = passkey ? FingerprintIcon : KeyIcon;
  return (
    <Row className="flex items-center gap-3 py-2.5">
      <Tooltip label={passkey ? "Passkey" : "tailgate key"}>
        <Icon aria-label={passkey ? "Passkey" : "tailgate key"} className="size-5 shrink-0 text-fg-muted" />
      </Tooltip>
      <div className="min-w-0 flex-1">
        <div className="truncate">{name}</div>
        <div className="text-[13px] text-fg-muted">{detail}</div>
      </div>
      {onAction && (
        <Button variant="ghost" size="sm" className="-mr-1.5 text-danger" onClick={onAction}>
          {action}
        </Button>
      )}
    </Row>
  );
};

export const PasskeyRows: FC<{ passkeys: Passkey[]; onRemove?: (p: Passkey) => void }> = ({
  passkeys,
  onRemove,
}) =>
  passkeys.map((p) => {
    const on = device(p.userAgent);
    return (
      <MethodRow
        key={p.id}
        passkey
        name={passkeyName(p.aaguid)}
        detail={
          <>
            {on && `${on}, `}
            <Used verb={on ? "added" : "Added"} created={p.createdAt} used={p.lastUsedAt} />
          </>
        }
        action="Remove"
        onAction={onRemove && (() => onRemove(p))}
      />
    );
  });

export const KeyRows: FC<{ keys: Key[]; onRevoke?: (k: Key) => void }> = ({ keys, onRevoke }) =>
  keys.map((k) => (
    <MethodRow
      key={k.id}
      passkey={false}
      name={k.label || "Key"}
      detail={<Used verb="Made" created={k.createdAt} used={k.lastUsedAt} />}
      action="Revoke"
      onAction={onRevoke && (() => onRevoke(k))}
    />
  ));
