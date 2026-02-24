import { h as esc } from "../lib/html.js";
import { fmtCost, fmtDuration } from "../lib/format.js";

export function JobFooter({ isError, data }) {
  const d = data || {};
  const icon = isError ? "\u2717" : "\u2713";
  const msg = isError ? d.error || "Job failed" : d.final_response || "Done";

  const metaParts = [];
  if (d.total_duration_ms !== undefined)
    metaParts.push(fmtDuration(d.total_duration_ms));
  if (d.total_cost_usd !== undefined)
    metaParts.push(fmtCost(d.total_cost_usd));
  const meta = metaParts.join(" \u00b7 ");

  return (
    <div class={"job-footer " + (isError ? "job-footer-err" : "job-footer-ok")}>
      <span class="job-footer-icon">{icon}</span>
      <span class="job-footer-msg">{msg}</span>
      {meta && <span class="job-footer-meta">{meta}</span>}
    </div>
  );
}
