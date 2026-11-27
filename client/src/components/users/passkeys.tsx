import { FingerprintIcon, KeyIcon } from "@phosphor-icons/react";
import { lazy, Suspense, useState, type FC, type ReactNode } from "react";
import { api, type Key, type Passkey } from "../../api";
import { Ago } from "../../lib/time";
import { device, passkeyName } from "../../lib/passkeys";
import { Button } from "../../ui/button";
import { Tooltip } from "../../ui/tooltip";
import { Row } from "../section";

/** "made 3 days ago, used just now". the current one leaves out when it was used, since
 * that's only saved every few minutes and it's in use right now */
export const Used: FC<{ verb: string; created: number; used: number | null; current?: boolean }> = ({
  verb,
  created,
  used,
  current,
}) => (
  <>
    {verb} <Ago seconds={created} />
    {!current && (
      <>
        ,{" "}
        {used ? (
          <>
            used <Ago seconds={used} />
          </>
        ) : (
          "never used"
        )}
      </>
    )}
  </>
);

// funnel visitors only need the dialog if they remove something
const Confirm = lazy(() => import("../confirm").then((m) => ({ default: m.Confirm })));

/** a user's keys and passkeys, for the user on their dashboard and for admins */
export const Methods: FC<{
  keys: Key[];
  passkeys: Passkey[];
  reload: () => void;
  list: FC<{ children: ReactNode }>;
}> = ({ keys, passkeys, reload, list: List }) => {
  const [removing, setRemoving] = useState<{ passkey?: Passkey; key?: Key }>();
  if (keys.length + passkeys.length === 0) return null;
  return (
    <>
      <List>
        {passkeys.map((p) => {
          const on = device(p.userAgent);
          return (
            <MethodRow
              key={p.id}
              passkey
              name={passkeyName(p.aaguid)}
              detail={
                <>
                  {on && `${on}, `}
                  <Used
                    verb={on ? "added" : "Added"}
                    created={p.createdAt}
                    used={p.lastUsedAt}
                    current={p.current}
                  />
                </>
              }
              current={p.current}
              action="Remove"
              onAction={p.removable ? () => setRemoving({ passkey: p }) : undefined}
            />
          );
        })}
        {keys.map((k) => (
          <MethodRow
            key={k.id}
            passkey={false}
            name={k.label || "Key"}
            detail={<Used verb="Made" created={k.createdAt} used={k.lastUsedAt} current={k.current} />}
            current={k.current}
            action="Revoke"
            onAction={k.removable ? () => setRemoving({ key: k }) : undefined}
          />
        ))}
      </List>
      {/* its own boundary, or the page blanks while the dialog's chunk loads */}
      {removing && (
        <Suspense>
          <Confirm
            open
            onClose={() => setRemoving(undefined)}
            title={removing.passkey ? "Remove this passkey?" : "Revoke this key?"}
            description="Every session made with it ends"
            action={removing.passkey ? "Remove" : "Revoke"}
            onConfirm={async () => {
              await api(
                "DELETE",
                removing.passkey ? "/api/passkeys/" + removing.passkey.id : "/api/keys/" + removing.key!.id,
              );
              reload();
            }}
          />
        </Suspense>
      )}
    </>
  );
};

const MethodRow: FC<{
  passkey: boolean;
  name: string;
  detail: ReactNode;
  current: boolean;
  action: string;
  onAction?: () => void;
}> = ({ passkey, name, detail, current, action, onAction }) => {
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
      {current ? (
        <span className="shrink-0 text-[13px] text-fg-faint">Current</span>
      ) : (
        onAction && (
          <Button variant="ghost" size="sm" className="-mr-1.5 text-danger" onClick={onAction}>
            {action}
          </Button>
        )
      )}
    </Row>
  );
};
