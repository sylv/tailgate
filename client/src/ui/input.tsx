import { forwardRef, type InputHTMLAttributes, type ReactNode, type TextareaHTMLAttributes } from "react";
import { cn } from "../lib/utils";

// a 1px border on whatever the field sits on, lighter under the pointer and
// brand-bright while typing. all instant

export const FIELD =
  "w-full min-w-0 rounded-md border border-line bg-transparent text-fg placeholder:text-fg-faint outline-none " +
  "hover:border-line-strong focus-visible:border-brand-bright focus-visible:ring-1 focus-visible:ring-brand-bright focus-visible:ring-inset " +
  "aria-invalid:border-danger aria-invalid:ring-1 aria-invalid:ring-danger disabled:opacity-45";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => (
    <input ref={ref} className={cn(FIELD, "h-8 px-3 text-sm", className)} {...props} />
  ),
);
Input.displayName = "Input";

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => (
    <textarea ref={ref} className={cn(FIELD, "min-h-20 px-3 py-2 text-sm", className)} {...props} />
  ),
);
Textarea.displayName = "Textarea";

export const Field = ({
  label,
  hint,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
  className?: string;
}) => (
  <label className={cn("flex flex-col gap-1.5", className)}>
    <span className="text-[13px] font-medium text-fg-muted">{label}</span>
    {children}
    {hint && <span className="text-xs text-fg-faint">{hint}</span>}
  </label>
);

/** a label over a value that isn't a field. a <label> around a copy field would
 * copy when its text is clicked */
export const Labelled = ({
  label,
  children,
  className,
}: {
  label: ReactNode;
  children: ReactNode;
  className?: string;
}) => (
  <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
    <span className="text-[13px] font-medium text-fg-muted">{label}</span>
    {children}
  </div>
);
