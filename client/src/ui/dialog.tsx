import { XIcon } from "@phosphor-icons/react";
import { AnimatePresence, motion, useDragControls, type PanInfo } from "motion/react";
import { useEffect, useId, useRef, type FC, type PointerEvent, type ReactNode } from "react";
import { useIsMobile } from "../hooks/use-mobile";
import { cn } from "../lib/utils";
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
const EASE_OUT = [0.22, 1, 0.36, 1] as const;

export const Dialog: FC<DialogProps> = (props) => (
  <AnimatePresence>{props.open && <Panel key="dialog" {...props} />}</AnimatePresence>
);

const Panel: FC<DialogProps> = ({
  onOpenChange,
  title,
  description,
  footer,
  size = "md",
  children,
  className,
}) => {
  const mobile = useIsMobile();
  const drag = useDragControls();
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

  const startDrag = (event: PointerEvent, fromBody: boolean) => {
    if (!mobile) return;
    const scroller = body.current;
    if (fromBody && scroller && scroller.scrollHeight > scroller.clientHeight) return;
    drag.start(event);
  };

  const endDrag = (_: unknown, info: PanInfo) => {
    const height = panel.current?.offsetHeight ?? 400;
    if (info.offset.y > Math.min(160, height * 0.25) || info.velocity.y > 600) close();
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
      <motion.div
        className="fixed inset-0 bg-black/70"
        onClick={close}
        initial={{ opacity: 0 }}
        animate={{ opacity: 1, transition: { duration: 0.18, ease: EASE_OUT } }}
        exit={{ opacity: 0, transition: { duration: 0.12 } }}
      />
      <motion.div
        ref={panel}
        className={cn(
          "fixed flex flex-col shadow-pop outline-none",
          mobile
            ? // the sheet's colour carries on below it, for when it's stretched upwards
              "inset-x-0 bottom-0 max-h-[92dvh] rounded-t-lg border-t border-line bg-raised after:absolute after:inset-x-0 after:top-full after:h-[50dvh] after:bg-raised"
            : cn(
                "top-1/2 left-1/2 max-h-[85dvh] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-line bg-raised",
                WIDTHS[size],
              ),
          className,
        )}
        {...(mobile
          ? {
              initial: { y: "100%" },
              animate: { y: 0, transition: { duration: 0.24, ease: EASE_OUT } },
              exit: { y: "100%", transition: { duration: 0.18, ease: [0.5, 0, 0.75, 0] } },
              drag: "y" as const,
              dragControls: drag,
              dragListener: false,
              dragConstraints: { top: 0, bottom: 0 },
              dragElastic: { top: 0.08, bottom: 1 },
              dragTransition: { bounceStiffness: 500, bounceDamping: 40 },
              onDragEnd: endDrag,
            }
          : {
              initial: { opacity: 0, y: 10, scale: 0.97 },
              animate: { opacity: 1, y: 0, scale: 1, transition: { duration: 0.18, ease: EASE_OUT } },
              exit: { opacity: 0, y: 4, scale: 0.98, transition: { duration: 0.1 } },
            })}
      >
        <div
          className={cn("flex shrink-0 flex-col", mobile && "touch-none")}
          onPointerDown={(e) => startDrag(e, false)}
        >
          {mobile && (
            <div aria-hidden className="flex h-5 items-end justify-center">
              <span className="h-1.5 w-10 rounded-full bg-line" />
            </div>
          )}
          <div className={cn("flex items-start gap-4 px-5 pb-3", mobile ? "pt-2" : "pt-4")}>
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
      </motion.div>
    </dialog>
  );
};
