import { XIcon } from "@phosphor-icons/react";
import { useEffect, useId, useRef, useState, type FC, type PointerEvent, type ReactNode } from "react";
import { useIsMobile } from "../hooks/use-mobile";
import { clsx } from "clsx";
import { IconButton } from "./icon-button";

// lyra's dialog. the header and footer never scroll away, only the body between them does.
// wide, it's a raised panel in the middle over a black scrim. on a phone it's a sheet from
// the bottom that follows the finger when dragged by its header (or anywhere, when the body
// has nothing to scroll), closing past a quarter of its height or on a flick.
// it's a native <dialog> rather than radix's, whose scroll lock injects a <style> tag the
// csp blocks. the <dialog> is a transparent full-screen layer and draws its own scrim, so
// both can animate out before it closes

export interface DialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  /** buttons at the bottom, right-aligned and always visible */
  footer?: ReactNode;
  size?: "sm" | "md" | "lg";
  children?: ReactNode;
  className?: string;
}

const WIDTHS = { sm: "max-w-sm", md: "max-w-lg", lg: "max-w-3xl" };

// it stays up after open goes false until its exit animation ends
export const Dialog: FC<DialogProps> = (props) => {
  const [shown, setShown] = useState(props.open);
  if (props.open && !shown) setShown(true);
  return shown ? <Panel {...props} onExited={() => setShown(false)} /> : null;
};

const Panel: FC<DialogProps & { onExited: () => void }> = ({
  open,
  onOpenChange,
  onExited,
  title,
  description,
  footer,
  size = "md",
  children,
  className,
}) => {
  const mobile = useIsMobile();
  const dialog = useRef<HTMLDialogElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const body = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const close = () => onOpenChange(false);

  useEffect(() => {
    const d = dialog.current!;
    d.showModal();
    // focus goes to the first field rather than the close button
    panel.current
      ?.querySelector<HTMLElement>("input:not([type=hidden]):not(:disabled), textarea:not(:disabled)")
      ?.focus();
    return () => d.close();
  }, []);

  // the sheet follows the finger down, and a little way up. on the window rather than
  // captured, so a tap on the close button still clicks it
  const startDrag = (e: PointerEvent, fromBody: boolean) => {
    const p = panel.current!;
    const scroller = body.current;
    if (!mobile || !open || (fromBody && scroller && scroller.scrollHeight > scroller.clientHeight)) return;
    const start = e.clientY;
    let last = { y: start, t: e.timeStamp };
    let speed = 0;
    p.style.transition = "none";
    const move = (m: globalThis.PointerEvent) => {
      speed = (m.clientY - last.y) / Math.max(1, m.timeStamp - last.t);
      last = { y: m.clientY, t: m.timeStamp };
      const dy = m.clientY - start;
      p.style.transform = `translateY(${dy > 0 ? dy : dy * 0.08}px)`;
    };
    const end = () => {
      removeEventListener("pointermove", move);
      removeEventListener("pointerup", end);
      removeEventListener("pointercancel", end);
      if (last.y - start > Math.min(160, p.offsetHeight * 0.25) || speed > 0.6) return close();
      p.style.transition = "transform 200ms var(--ease-out)";
      p.style.transform = "";
    };
    addEventListener("pointermove", move);
    addEventListener("pointerup", end);
    addEventListener("pointercancel", end);
  };

  return (
    <dialog
      ref={dialog}
      aria-labelledby={titleId}
      // escape asks to close, so the exit animation plays
      onCancel={(e) => {
        e.preventDefault();
        close();
      }}
      className="fixed inset-0 m-0 h-dvh max-h-none w-screen max-w-none overflow-hidden bg-transparent p-0 text-fg backdrop:bg-transparent"
    >
      <div
        className={clsx("fixed inset-0 bg-black/70", open ? "animate-fade-in" : "animate-fade-out")}
        onClick={close}
      />
      <div
        ref={panel}
        // an exit that starts mid drag goes on from where the finger left it
        onAnimationEnd={(e) => !open && e.target === e.currentTarget && onExited()}
        className={clsx(
          "fixed flex flex-col shadow-pop outline-none",
          mobile
            ? // the sheet's colour carries on below it, for when it's stretched upwards
              clsx(
                "inset-x-0 bottom-0 max-h-[92dvh] rounded-t-lg border-t border-line bg-raised after:absolute after:inset-x-0 after:top-full after:h-[50dvh] after:bg-raised",
                open ? "animate-sheet-in" : "animate-sheet-out",
              )
            : clsx(
                "top-1/2 left-1/2 max-h-[85dvh] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-line bg-raised",
                WIDTHS[size],
                open ? "animate-dialog-in" : "animate-dialog-out",
              ),
          className,
        )}
      >
        <div
          className={clsx("flex shrink-0 flex-col", mobile && "touch-none")}
          onPointerDown={(e) => startDrag(e, false)}
        >
          {mobile && (
            <div aria-hidden className="flex h-5 items-end justify-center">
              <span className="h-1.5 w-10 rounded-full bg-line" />
            </div>
          )}
          <div className={clsx("flex items-start gap-4 px-5 pb-3", mobile ? "pt-2" : "pt-4")}>
            <div className="min-w-0 flex-1 pt-0.5">
              <h2 id={titleId} className="text-base">
                {title}
              </h2>
              {description && <p className="mt-1 text-sm text-fg-muted">{description}</p>}
            </div>
            <IconButton label="Close" size="sm" tooltip={false} className="-mr-1.5" onClick={close}>
              <XIcon />
            </IconButton>
          </div>
        </div>
        {children && (
          <div
            ref={body}
            className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 pb-5 text-sm"
            onPointerDown={(e) => startDrag(e, true)}
          >
            {children}
          </div>
        )}
        {footer && (
          <div className="flex shrink-0 flex-wrap justify-end gap-2 border-t border-line px-5 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
            {footer}
          </div>
        )}
      </div>
    </dialog>
  );
};
