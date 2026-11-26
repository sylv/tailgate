import { Checkbox as C } from "radix-ui";
import { useId, type FC, type ReactNode } from "react";
import { cn } from "../lib/utils";
import { FOCUS_RING } from "./button";
import { CheckIcon } from "@phosphor-icons/react";

// an 18px box that fills with the brand when on. with a label the whole row is the target
export const Checkbox: FC<{
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  label: ReactNode;
  description?: ReactNode;
  disabled?: boolean;
  className?: string;
}> = ({ checked, onCheckedChange, label, description, disabled, className }) => {
  const id = useId();
  return (
    <label
      htmlFor={id}
      className={cn("flex cursor-pointer items-start gap-2.5", disabled && "pointer-events-none", className)}
    >
      <span className="flex h-5 items-center">
        <C.Root
          id={id}
          checked={checked}
          onCheckedChange={(v) => onCheckedChange(v === true)}
          disabled={disabled}
          className={cn(
            "inline-flex size-[18px] shrink-0 items-center justify-center rounded-[5px] border-[1.5px] border-line-strong hover:border-fg-muted",
            "data-[state=checked]:border-brand data-[state=checked]:bg-brand data-[state=checked]:hover:border-brand-hover data-[state=checked]:hover:bg-brand-hover",
            "disabled:opacity-45",
            FOCUS_RING,
          )}
        >
          <C.Indicator className="flex text-white">
            <CheckIcon weight="bold" className="size-3" />
          </C.Indicator>
        </C.Root>
      </span>
      <span className={cn("min-w-0 flex-1", disabled && "opacity-45")}>
        <span className="block text-sm text-fg">{label}</span>
        {description && <span className="mt-0.5 block text-[13px] text-fg-muted">{description}</span>}
      </span>
    </label>
  );
};
