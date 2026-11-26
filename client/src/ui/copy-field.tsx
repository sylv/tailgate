import { useState, type FC } from "react";
import { cn } from "../lib/utils";
import { IconButton } from "./icon-button";
import { CheckIcon, CopyIcon } from "@phosphor-icons/react";

// a value to copy somewhere else: mono, selectable in one click, a copy button at its end
export const CopyField: FC<{ value: string; label: string; className?: string }> = ({
  value,
  label,
  className,
}) => {
  const [copied, setCopied] = useState(false);
  return (
    <div className={cn("flex h-8 min-w-0 items-center rounded-md border border-line pl-3", className)}>
      <code className="min-w-0 flex-1 truncate font-mono text-[13px] select-all">{value}</code>
      <IconButton
        label={copied ? "Copied" : `Copy ${label}`}
        size="sm"
        className="mr-0.5"
        onClick={() =>
          navigator.clipboard.writeText(value).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          })
        }
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </IconButton>
    </div>
  );
};
