import type { FC } from "react";
import { clsx } from "clsx";

/** Something is happening. A ring with a gap, turning; it keeps turning under reduced motion,
 * since it's information, not decoration. Sized by its font size or a size class. */
export const Spinner: FC<{ className?: string; label?: string }> = ({ className, label = "Loading" }) => (
  <output aria-label={label} className="contents">
    <svg
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden
      className={clsx("size-[1em] shrink-0 animate-spin [animation-duration:700ms]", className)}
    >
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  </output>
);
