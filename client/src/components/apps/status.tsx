import { ExclamationMarkIcon, HourglassSimpleIcon, PauseIcon } from "@phosphor-icons/react";
import type { FC } from "react";
import type { NodeInfo, App, Apps } from "../../api";
import { cn } from "../../lib/utils";
import { Tooltip } from "../../ui/tooltip";

export type Tone = "ok" | "idle" | "warning" | "danger" | "unused";

export interface Status {
  tone: Tone;
  icon: "none" | "pause" | "wait" | "alert";
  label: string;
}

const toneOf = (n: NodeInfo): Tone => {
  switch (n.state) {
    case "online":
      return "ok";
    case "needs signing":
    case "needs approval":
    case "logged out":
    case "funnel not allowed":
    case "wrong name":
      return "warning";
    case "failed":
    case "auth key invalid":
      return "danger";
    case "cert pending":
      return "warning";
  }
  // starting is on its way
  return n.nodeError ? "danger" : "idle";
};

const sentence = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

export const nodeStatus = (n: NodeInfo): Status => {
  const tone = toneOf(n);
  // a cert on its way is yellow but needs nothing doing, so it waits instead of asking for attention
  const waiting = (tone === "idle" && !!n.state) || (n.state === "cert pending" && !n.nodeError);
  return {
    tone,
    icon: waiting ? "wait" : tone === "warning" || tone === "danger" ? "alert" : "none",
    label: sentence(n.state || "no node"),
  };
};

export const appStatus = (s: App): Status => {
  if (s.error) return { tone: "danger", icon: "alert", label: "Its labels have errors" };
  const status = nodeStatus(s);
  if (status.icon === "alert") return status;
  if (!s.address) return { tone: "idle", icon: "pause", label: "Container stopped" };
  return status;
};

export const needsAttention = (s: Apps) =>
  s.problems.length > 0 ||
  s.unused.length > 0 ||
  nodeStatus(s.tailgate).icon === "alert" ||
  s.apps.some((x) => appStatus(x).icon === "alert");

export const toneText: Record<Tone, string> = {
  ok: "text-success",
  idle: "text-fg-faint",
  warning: "text-warning",
  danger: "text-danger",
  unused: "text-brand-bright",
};

/** a ring in the status's colour, empty when it's fine, a pause when the container is
 * stopped, an hourglass while the node is on its way, a ! when something needs doing */
export const StatusRing: FC<{ status: Status }> = ({ status }) => (
  <Tooltip label={status.label}>
    <span
      role="img"
      aria-label={status.label}
      className={cn(
        "flex size-5 items-center justify-center rounded-full border border-current",
        toneText[status.tone],
      )}
    >
      {status.icon === "pause" && <PauseIcon weight="fill" className="size-2.5" />}
      {status.icon === "wait" && <HourglassSimpleIcon weight="fill" className="size-2.5" />}
      {status.icon === "alert" && <ExclamationMarkIcon weight="bold" className="size-3" />}
    </span>
  </Tooltip>
);
