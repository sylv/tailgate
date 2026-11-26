import { Tooltip as T } from "radix-ui";
import type { FC, ReactElement, ReactNode } from "react";

/** a short label for something that only has an icon */
export const Tooltip: FC<{
  label: ReactNode;
  side?: "top" | "bottom" | "left" | "right";
  children: ReactElement;
}> = ({ label, side = "top", children }) => (
  <T.Root>
    <T.Trigger asChild>{children}</T.Trigger>
    <T.Portal>
      <T.Content
        side={side}
        sideOffset={6}
        collisionPadding={8}
        className="z-[100] origin-(--radix-tooltip-content-transform-origin) rounded-sm border border-line bg-raised px-2 py-1 text-xs font-medium text-fg shadow-pop data-[state=closed]:animate-pop-out data-[state=delayed-open]:animate-pop-in data-[state=instant-open]:animate-pop-in"
      >
        {label}
      </T.Content>
    </T.Portal>
  </T.Root>
);

export const TooltipProvider: FC<{ children: ReactNode }> = ({ children }) => (
  <T.Provider delayDuration={400} skipDelayDuration={300}>
    {children}
  </T.Provider>
);
