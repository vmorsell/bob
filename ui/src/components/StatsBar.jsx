import { fmtCost, fmtTokens } from "../lib/format.js";

export function StatsBar({ jobs, stats }) {
  const parts = [];
  parts.push(jobs.length + " job" + (jobs.length !== 1 ? "s" : ""));
  if (stats) {
    if (stats.total_cost_usd)
      parts.push(fmtCost(stats.total_cost_usd) + " spent");
    if (stats.total_input_tokens)
      parts.push(fmtTokens(stats.total_input_tokens) + " input");
    if (stats.total_output_tokens)
      parts.push(fmtTokens(stats.total_output_tokens) + " output");
  }
  return (
    <div class="stats-bar">
      {parts.map((p, i) => (
        <>
          {i > 0 && <span class="meta-sep">&middot;</span>}
          <span>{p}</span>
        </>
      ))}
    </div>
  );
}
