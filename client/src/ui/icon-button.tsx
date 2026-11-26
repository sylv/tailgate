import { Slot } from "radix-ui";
import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { cn } from "../lib/utils";
import { BUTTON_VARIANTS, FOCUS_RING, type ButtonVariant } from "./button";
import { Spinner } from "./spinner";
import { Tooltip } from "./tooltip";

// a square button holding only an icon. its label is read out and shown as a tooltip

export type IconButtonSize = "sm" | "md" | "lg";

const SIZES: Record<IconButtonSize, string> = {
  sm: "size-7 [&_svg]:size-4",
  md: "size-8 [&_svg]:size-[18px]",
  lg: "size-9 [&_svg]:size-5",
};

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string;
  variant?: ButtonVariant;
  size?: IconButtonSize;
  tooltip?: boolean;
  pressed?: boolean;
  loading?: boolean;
  asChild?: boolean;
  children: ReactNode;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(
  (
    {
      label,
      variant = "ghost",
      size = "md",
      tooltip = true,
      pressed,
      loading,
      asChild,
      className,
      children,
      disabled,
      ...props
    },
    ref,
  ) => {
    const Component = asChild ? Slot.Root : "button";
    const button = (
      <Component
        ref={ref}
        type={asChild ? undefined : "button"}
        aria-label={label}
        aria-pressed={pressed}
        disabled={disabled || loading}
        className={cn(
          "relative inline-flex shrink-0 items-center justify-center rounded-md select-none",
          "disabled:pointer-events-none disabled:opacity-45",
          FOCUS_RING,
          BUTTON_VARIANTS[variant],
          SIZES[size],
          pressed && "bg-raised text-fg",
          className,
        )}
        {...props}
      >
        {loading ? <Spinner /> : children}
      </Component>
    );
    return tooltip ? <Tooltip label={label}>{button}</Tooltip> : button;
  },
);
IconButton.displayName = "IconButton";
