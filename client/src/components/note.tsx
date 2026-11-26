import { InfoIcon, WarningCircleIcon, WarningIcon } from "@phosphor-icons/react";
import type { FC, ReactNode } from "react";
import { cn } from "../lib/utils";
import { toneText } from "./services/status";

// a warning or an error under the thing it's about, always an icon and text
export const Note: FC<{
  tone: "warning" | "danger" | "unused" | "info";
  children: ReactNode;
  className?: string;
}> = ({ tone, children, className }) => {
  const Icon = tone === "danger" ? WarningCircleIcon : tone === "info" ? InfoIcon : WarningIcon;
  return (
    <div
      className={cn(
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
