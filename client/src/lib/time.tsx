import { useEffect, useState, type FC } from "react";

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 31536000],
  ["month", 2592000],
  ["week", 604800],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
];

const fmt = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

/** the time in unix seconds, every few seconds, so only what shows it re-renders */
function useNow(every: number) {
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now() / 1000), every);
    return () => clearInterval(timer);
  }, [every]);
  return now;
}

/** "3 days ago" from unix seconds, "just now" under a minute */
export const Ago: FC<{ seconds: number }> = ({ seconds }) => {
  const d = useNow(30_000) - seconds;
  for (const [unit, size] of units) if (d >= size) return fmt.format(-Math.floor(d / size), unit);
  return "just now";
};

/** "in 40 minutes" from unix seconds, "soon" under a minute */
export const Until: FC<{ seconds: number }> = ({ seconds }) => {
  const d = seconds - useNow(10_000);
  for (const [unit, size] of units) if (d >= size) return fmt.format(Math.floor(d / size), unit);
  return "soon";
};
