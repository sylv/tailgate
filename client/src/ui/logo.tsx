import type { FC } from "react";
import { clsx } from "clsx";

// phosphor's barricade in bold, inlined so the favicon doesn't pull in every weight
const PATH =
  "M224,60H32A20,20,0,0,0,12,80v72a20,20,0,0,0,20,20H52v28a12,12,0,0,0,24,0V172H180v28a12,12,0,0,0,24,0V172h20a20,20,0,0,0,20-20V80A20,20,0,0,0,224,60Zm-4,59L185,84h35Zm-43,29L113,84H151l64,64Zm-72,0L41,84H79l64,64ZM36,113l35,35H36Z";

export const setFavicon = () => {
  const link = document.createElement("link");
  link.rel = "icon";
  link.type = "image/svg+xml";
  link.href = `data:image/svg+xml,${encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" fill="#a98cfa"><path d="${PATH}"/></svg>`,
  )}`;
  document.head.append(link);
};

export const LogoMark: FC<{ className?: string }> = ({ className }) => (
  <svg viewBox="0 0 256 256" fill="currentColor" aria-hidden className={clsx("shrink-0", className)}>
    <path d={PATH} />
  </svg>
);

export const Wordmark: FC<{ className?: string }> = ({ className }) => (
  <span className={clsx("flex items-center gap-2 text-fg", className)}>
    <LogoMark className="size-5 text-brand-bright" />
    <span className="text-[15px] font-semibold tracking-tight">tailgate</span>
  </span>
);
