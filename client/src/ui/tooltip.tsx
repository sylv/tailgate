import {
  cloneElement,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FC,
  type HTMLAttributes,
  type ReactElement,
} from "react";

// once one tooltip has shown, the next comes up straight away for a moment
let closedAt = 0;

/** a short label for something that only has an icon. it shows above it after a moment
 * under the mouse or straight away on keyboard focus. it's a popover so it's in the top
 * layer, over dialogs too */
export const Tooltip: FC<{ label: string; children: ReactElement<HTMLAttributes<HTMLElement>> }> = ({
  label,
  children,
}) => {
  const [anchor, setAnchor] = useState<HTMLElement>();
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const tip = useRef<HTMLDivElement>(null);

  const show = (el: HTMLElement, now: boolean) => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setAnchor(el), now || Date.now() - closedAt < 300 ? 0 : 400);
  };
  const hide = () => {
    clearTimeout(timer.current);
    setAnchor((a) => {
      if (a) closedAt = Date.now();
      return undefined;
    });
  };

  useLayoutEffect(() => {
    const t = tip.current;
    if (!anchor || !t) return;
    t.showPopover();
    const a = anchor.getBoundingClientRect();
    const { width, height } = t.getBoundingClientRect();
    const above = a.top - height - 6;
    const left = Math.min(Math.max(8, a.left + a.width / 2 - width / 2), innerWidth - width - 8);
    t.style.left = `${left}px`;
    t.style.top = `${above < 8 ? a.bottom + 6 : above}px`;
  }, [anchor]);
  useEffect(() => () => clearTimeout(timer.current), []);

  const own = children.props;
  return (
    <>
      {cloneElement(children, {
        onPointerEnter: (e) => {
          own.onPointerEnter?.(e);
          if (e.pointerType === "mouse") show(e.currentTarget, false);
        },
        onPointerLeave: (e) => {
          own.onPointerLeave?.(e);
          hide();
        },
        onPointerDown: (e) => {
          own.onPointerDown?.(e);
          hide();
        },
        onFocus: (e) => {
          own.onFocus?.(e);
          if (e.currentTarget.matches(":focus-visible")) show(e.currentTarget, true);
        },
        onBlur: (e) => {
          own.onBlur?.(e);
          hide();
        },
        onKeyDown: (e) => {
          own.onKeyDown?.(e);
          if (e.key === "Escape") hide();
        },
      })}
      {anchor && (
        <div
          ref={tip}
          popover="manual"
          role="tooltip"
          className="pointer-events-none fixed m-0 animate-pop-in rounded-sm border border-line bg-raised px-2 py-1 text-xs font-medium text-fg shadow-pop"
        >
          {label}
        </div>
      )}
    </>
  );
};
