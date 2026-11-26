import { Slot } from "radix-ui";
import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { cn } from "../lib/utils";
import { Spinner } from "./spinner";

// slim and flat. one primary per view at most, secondary is a 1px border, ghost has
// nothing until hovered, link is text that underlines because it opens something

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "link";
export type ButtonSize = "sm" | "md" | "lg";

export const FOCUS_RING =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand-bright";

const BASE =
  "relative inline-flex shrink-0 items-center justify-center gap-2 rounded-md font-medium whitespace-nowrap select-none " +
  "disabled:pointer-events-none disabled:opacity-45 aria-disabled:pointer-events-none aria-disabled:opacity-45 " +
  FOCUS_RING;

export const BUTTON_VARIANTS: Record<ButtonVariant, string> = {
  primary: "bg-brand text-on-brand hover:bg-brand-hover active:bg-brand-pressed",
  secondary: "border border-line text-fg hover:bg-raised active:bg-pressed",
  ghost: "text-fg-muted hover:bg-raised hover:text-fg active:bg-pressed",
  danger: "border border-line text-danger hover:bg-raised active:bg-pressed",
  link: "h-auto! px-0! text-fg-muted underline-offset-2 hover:text-fg hover:underline active:text-fg-muted",
};

export const BUTTON_SIZES: Record<ButtonSize, string> = {
  sm: "h-7 px-2.5 text-[13px] [&_svg]:size-4",
  md: "h-8 px-3.5 text-sm [&_svg]:size-4",
  lg: "h-9 px-4 text-sm [&_svg]:size-[18px]",
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  icon?: ReactNode;
  iconEnd?: ReactNode;
  loading?: boolean;
  asChild?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      variant = "secondary",
      size = "md",
      icon,
      iconEnd,
      loading,
      asChild,
      className,
      children,
      disabled,
      ...props
    },
    ref,
  ) => {
    const classes = cn(BASE, BUTTON_VARIANTS[variant], BUTTON_SIZES[size], className);
    if (asChild) {
      return (
        <Slot.Root ref={ref} className={classes} {...props}>
          {icon}
          <Slot.Slottable>{children}</Slot.Slottable>
          {iconEnd}
        </Slot.Root>
      );
    }
    return (
      <button
        ref={ref}
        type="button"
        className={classes}
        disabled={disabled || loading}
        aria-busy={loading || undefined}
        {...props}
      >
        {loading ? <Spinner /> : icon}
        {children}
        {iconEnd}
      </button>
    );
  },
);
Button.displayName = "Button";
