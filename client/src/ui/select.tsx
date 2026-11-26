import type { ReactNode } from "react";
import { cn } from "../lib/utils";
import { FOCUS_RING } from "./button";
import { CaretUpDownIcon } from "@phosphor-icons/react";
import { Menu, MenuContent, MenuRadioGroup, MenuRadioItem, MenuTrigger } from "./menu";

// a bordered button showing the choice, opening the menu's panel. built on the
// menu rather than radix's select, whose scroll lock the csp blocks

export interface SelectOption<V extends string> {
  value: V;
  label: ReactNode;
  description?: ReactNode;
}

export const Select = <V extends string>({
  value,
  onValueChange,
  options,
  label,
  placeholder,
  size = "md",
  className,
}: {
  value: V | undefined;
  onValueChange: (value: V) => void;
  options: SelectOption<V>[];
  label: string;
  placeholder?: string;
  size?: "sm" | "md";
  className?: string;
}) => {
  const current = options.find((o) => o.value === value);
  return (
    <Menu>
      <MenuTrigger
        aria-label={label}
        className={cn(
          "inline-flex min-w-0 items-center gap-2 rounded-md border border-line text-fg select-none",
          "hover:bg-raised active:bg-pressed data-[state=open]:bg-raised",
          FOCUS_RING,
          size === "md"
            ? "h-8 pr-2 pl-3 text-sm [&_svg]:size-4"
            : "h-7 pr-1.5 pl-2.5 text-[13px] [&_svg]:size-3.5",
          className,
        )}
      >
        <span className={cn("min-w-0 flex-1 truncate text-left font-medium", !current && "text-fg-faint")}>
          {current?.label ?? placeholder}
        </span>
        <CaretUpDownIcon className="text-fg-faint" />
      </MenuTrigger>
      <MenuContent className="min-w-(--radix-dropdown-menu-trigger-width)">
        <MenuRadioGroup value={value} onValueChange={(v) => onValueChange(v as V)}>
          {options.map((o) => (
            <MenuRadioItem key={o.value} value={o.value} description={o.description}>
              {o.label}
            </MenuRadioItem>
          ))}
        </MenuRadioGroup>
      </MenuContent>
    </Menu>
  );
};
