import { CaretDownIcon } from "@phosphor-icons/react";
import type { FC, LiHTMLAttributes, ReactNode } from "react";
import { clsx } from "clsx";

// the path a request takes, drawn as a line down the left with each step on it.
// the marker column hangs into the page's gutter, so the text lines up with the
// page's edge and the line sits outside it

export const Flow: FC<{ children: ReactNode }> = ({ children }) => (
  <ol className="relative flex flex-col before:absolute before:top-3 before:bottom-3 before:left-[11px] before:w-px before:bg-line-strong md:-ml-11">
    {children}
  </ol>
);

export const FlowStep: FC<LiHTMLAttributes<HTMLLIElement> & { marker: ReactNode }> = ({
  marker,
  children,
  className,
  ...props
}) => (
  <li
    className={clsx("relative grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-3 md:gap-x-5", className)}
    {...props}
  >
    <span className="relative flex h-[38px] justify-center">{marker}</span>
    <div className="min-w-0">{children}</div>
  </li>
);

/** where the request is. big, so the stages read as sections and the rules as what happens
 * in them, with a chevron for the way traffic goes */
export const Stage: FC<{ title: string; description?: string; className?: string }> = ({
  title,
  description,
  className,
}) => (
  <li className={clsx("relative mb-1 grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-3 md:gap-x-5", className)}>
    <span className="relative flex h-12 items-center justify-center">
      <span className="flex size-6 items-center justify-center rounded-full border border-line-strong bg-raised text-fg">
        <CaretDownIcon weight="bold" className="size-3" />
      </span>
    </span>
    <div className="min-h-12 pt-2">
      <h2 className="text-2xl">{title}</h2>
      {description && <p className="mt-0.5 text-[13px] text-fg-faint">{description}</p>}
    </div>
  </li>
);

export const Dot: FC<{ className?: string }> = ({ className }) => (
  <span className={clsx("mt-[15px] size-2 rounded-full ring-4 ring-surface", className)} />
);
