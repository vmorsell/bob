import { useRef, useCallback } from "preact/hooks";
import { fmtDuration } from "../lib/format.js";
import { CCItem } from "./CCItem.jsx";
import "../styles/cc.css";

export function CCSection({ item, ccItems }) {
  const contentRef = useRef(null);
  const expandedRef = useRef(false);
  const userScrolledRef = useRef(false);
  const atBottomRef = useRef(true);

  const handleLabelClick = useCallback(() => {
    expandedRef.current = !expandedRef.current;
    const sec = contentRef.current?.parentElement;
    if (sec) {
      sec.classList.toggle("expanded");
      if (!expandedRef.current && contentRef.current) {
        contentRef.current.scrollTop = contentRef.current.scrollHeight;
      }
    }
  }, []);

  const handleScroll = useCallback(() => {
    const el = contentRef.current;
    if (!el) return;
    userScrolledRef.current = true;
    atBottomRef.current =
      el.scrollHeight - el.clientHeight - el.scrollTop < 4;
  }, []);

  // Auto-scroll the inner content box when collapsed and not user-scrolled.
  const setContentRef = useCallback((el) => {
    contentRef.current = el;
    if (el && !expandedRef.current) {
      if (!userScrolledRef.current || atBottomRef.current) {
        el.scrollTop = el.scrollHeight;
      }
    }
  }, [ccItems]); // Re-run when items change.

  let statusIcon = null;
  let durStr = "";
  if (item.completed) {
    statusIcon = item.isError ? (
      <span class="cc-label-status" style="color:var(--red)">{"\u2717"}</span>
    ) : (
      <span class="cc-label-status" style="color:var(--green)">{"\u2713"}</span>
    );
    if (item.duration != null) {
      durStr = fmtDuration(item.duration);
    }
  }

  return (
    <div class={"cc-section" + (item.isTerminal ? " cc-section-terminal" : "")}>
      <div class="cc-label" onClick={handleLabelClick}>
        {item.label}
        {statusIcon}
        {durStr && <span class="cc-label-dur">{durStr}</span>}
      </div>
      <div class="cc-content" ref={setContentRef} onScroll={handleScroll}>
        {ccItems.map((ci, i) => (
          <CCItem key={i} item={ci} />
        ))}
      </div>
    </div>
  );
}
