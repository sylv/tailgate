import { InfoIcon, WarningCircleIcon, WarningIcon } from "@phosphor-icons/react";
import type { FC, ReactNode } from "react";
import { clsx } from "clsx";

export type Tone = "ok" | "idle" | "warning" | "danger" | "unused";

export const toneText: Record<Tone, string> = {
  ok: "text-success",
  idle: "text-fg-faint",
  warning: "text-warning",
  danger: "text-danger",
  unused: "text-brand-bright",
};

// a warning or an error under the thing it's about, always an icon and text
export const Note: FC<{
  tone: "warning" | "danger" | "unused" | "info";
  children: ReactNode;
  className?: string;
}> = ({ tone, children, className }) => {
  const Icon = tone === "danger" ? WarningCircleIcon : tone === "info" ? InfoIcon : WarningIcon;
  return (
    <div
      className={clsx(
        "flex items-start gap-2 text-[13px]",
        tone === "info" ? "text-fg-muted" : toneText[tone],
        className,
      )}
    >
      <Icon className="mt-0.5 size-4 shrink-0" />
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
};
