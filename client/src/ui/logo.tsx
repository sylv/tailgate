import type { FC } from "react";
import { cn } from "../lib/utils";

// a field gate: two posts, two rails and a brace, in the text's colour
const PATHS = ["M4 3V21M20 3V21", "M4 7H20M4 17H20", "M4 17L20 7"];

const svg = (colour: string, width: number) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="${colour}" stroke-width="${width}" stroke-linecap="round" stroke-linejoin="round">${PATHS.map((d) => `<path d="${d}"/>`).join("")}</svg>`;

export const setFavicon = () => {
  const link = document.createElement("link");
  link.rel = "icon";
  link.type = "image/svg+xml";
  link.href = `data:image/svg+xml,${encodeURIComponent(svg("#a98cfa", 2.4))}`;
  document.head.append(link);
};

export const LogoMark: FC<{ className?: string }> = ({ className }) => (
  <svg
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth={2}
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden
    className={cn("shrink-0", className)}
  >
    {PATHS.map((d) => (
      <path key={d} d={d} />
    ))}
  </svg>
);

export const Wordmark: FC<{ className?: string }> = ({ className }) => (
  <span className={cn("flex items-center gap-2 text-fg", className)}>
    <LogoMark className="size-5" />
    <span className="text-[15px] font-semibold tracking-tight">tailgate</span>
  </span>
);
