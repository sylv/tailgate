import { DropdownMenu as M } from "radix-ui";
import type { ComponentProps, FC, ReactNode } from "react";
import { cn } from "../lib/utils";
import { CheckIcon } from "@phosphor-icons/react";

// a raised panel that pops in from its trigger. never modal: radix's scroll lock
// injects a <style> tag, which the csp blocks
export const Menu: FC<ComponentProps<typeof M.Root>> = (props) => <M.Root modal={false} {...props} />;
export const MenuTrigger = M.Trigger;
export const MenuRadioGroup = M.RadioGroup;

export const POPOVER_PANEL =
  "z-[90] min-w-44 overflow-hidden rounded-lg border border-line bg-raised p-1 text-sm text-fg shadow-pop outline-none " +
  "data-[state=open]:animate-pop-in data-[state=closed]:animate-pop-out";

export const MenuContent: FC<ComponentProps<typeof M.Content>> = ({
  className,
  sideOffset = 6,
  align = "start",
  collisionPadding = 8,
  ...props
}) => (
  <M.Portal>
    <M.Content
      sideOffset={sideOffset}
      align={align}
      collisionPadding={collisionPadding}
      className={cn(
        POPOVER_PANEL,
        "max-h-(--radix-dropdown-menu-content-available-height) origin-(--radix-dropdown-menu-content-transform-origin) overflow-y-auto",
        className,
      )}
      {...props}
    />
  </M.Portal>
);

export const MENU_ITEM =
  "relative flex h-8 cursor-pointer items-center gap-2.5 rounded-md px-2 outline-none select-none " +
  "data-highlighted:bg-hover data-disabled:pointer-events-none data-disabled:opacity-45 " +
  "[&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-fg-muted";

export const MenuItem: FC<ComponentProps<typeof M.Item> & { icon?: ReactNode; danger?: boolean }> = ({
  className,
  icon,
  danger,
  children,
  ...props
}) => (
  <M.Item className={cn(MENU_ITEM, danger && "text-danger [&_svg]:text-danger", className)} {...props}>
    {icon}
    <span className="min-w-0 flex-1 truncate">{children}</span>
  </M.Item>
);

export const MenuRadioItem: FC<ComponentProps<typeof M.RadioItem> & { description?: ReactNode }> = ({
  className,
  children,
  description,
  ...props
}) => (
  <M.RadioItem className={cn(MENU_ITEM, "pr-8", description && "h-auto py-1.5", className)} {...props}>
    <span className="min-w-0 flex-1">
      <span className="block truncate">{children}</span>
      {description && <span className="block truncate text-xs text-fg-muted">{description}</span>}
    </span>
    <M.ItemIndicator className="absolute right-2.5 flex">
      <CheckIcon weight="bold" className="text-fg!" />
    </M.ItemIndicator>
  </M.RadioItem>
);

export const MenuLabel: FC<ComponentProps<typeof M.Label>> = ({ className, ...props }) => (
  <M.Label className={cn("px-2 pt-2 pb-1 text-xs text-fg-faint", className)} {...props} />
);

export const MenuSeparator: FC<ComponentProps<typeof M.Separator>> = ({ className, ...props }) => (
  <M.Separator className={cn("mx-1 my-1.5 h-px bg-line", className)} {...props} />
);
