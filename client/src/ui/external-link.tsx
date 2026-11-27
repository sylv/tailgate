import { ArrowSquareOutIcon } from "@phosphor-icons/react";
import type { AnchorHTMLAttributes, FC } from "react";
import { clsx } from "clsx";

/** a link that opens in a new tab, with the icon that says so */
export const ExternalLink: FC<AnchorHTMLAttributes<HTMLAnchorElement>> = ({
  className,
  children,
  ...props
}) => (
  <a
    target="_blank"
    rel="noreferrer"
    className={clsx("inline-flex items-center gap-1 underline-offset-2 hover:underline", className)}
    {...props}
  >
    {children}
    <ArrowSquareOutIcon className="size-3.5 shrink-0 opacity-60" />
  </a>
);
