import { useEffect } from "preact/hooks";
import { autoScroll } from "../state/job.js";

/** Track page-level auto-scroll: stick to bottom unless user scrolled up. */
export function useAutoScroll() {
  useEffect(() => {
    const onScroll = () => {
      autoScroll.value =
        window.scrollY + window.innerHeight >=
        document.body.scrollHeight - 160;
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);
}

export function scrollToBottomIfAuto() {
  if (autoScroll.value) {
    window.scrollTo(0, document.body.scrollHeight);
  }
}
