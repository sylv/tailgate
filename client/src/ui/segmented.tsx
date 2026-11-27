import { ToggleGroup } from "radix-ui";
import { clsx } from "clsx";
import { FOCUS_RING } from "./button";

// a choice between a few values: bordered like a field, the picked segment filled.
// the pick changes at once, nothing slides

export const Segmented = <V extends string>({
  value,
  onValueChange,
  options,
  label,
  className,
}: {
  value: V;
  onValueChange: (value: V) => void;
  options: { value: V; label: string }[];
  label: string;
  className?: string;
}) => (
  <ToggleGroup.Root
    type="single"
    value={value}
    // a single toggle group lets the picked one be clicked off, this can't be empty
    onValueChange={(next) => next && onValueChange(next as V)}
    aria-label={label}
    className={clsx(
      "inline-flex h-7 shrink-0 items-center gap-0.5 rounded-md border border-line p-0.5",
      className,
    )}
  >
    {options.map((o) => (
      <ToggleGroup.Item
        key={o.value}
        value={o.value}
        className={clsx(
          "inline-flex h-full items-center rounded-sm px-2 text-xs font-medium text-fg-faint select-none outline-none",
          "hover:text-fg aria-checked:bg-hover aria-checked:text-fg",
          FOCUS_RING,
        )}
      >
        {o.label}
      </ToggleGroup.Item>
    ))}
  </ToggleGroup.Root>
);
