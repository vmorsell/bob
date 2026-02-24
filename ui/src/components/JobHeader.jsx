import { h } from "../lib/html.js";
import { fmtCost } from "../lib/format.js";
import { PhaseBadge } from "./PhaseBadge.jsx";

export function JobHeader({ taskText, slackURL, prLink, jobCostUSD, currentPhase, isLive }) {
  const meta = [];

  if (slackURL) {
    meta.push(
      <a href={slackURL} target="_blank">
        Slack thread &#8599;
      </a>
    );
  }
  if (prLink) {
    meta.push(
      <a href={prLink} target="_blank">
        Pull request &#8599;
      </a>
    );
  }
  if (jobCostUSD > 0) {
    meta.push(<span>{fmtCost(jobCostUSD)}</span>);
  }
  if (currentPhase && currentPhase !== "done") {
    meta.push(<PhaseBadge phase={currentPhase} />);
  }
  if (isLive) {
    meta.push(
      <span class="job-live">
        <span class="live-pip" /> Live
      </span>
    );
  }

  return (
    <div class="job-hdr">
      <a href="/" class="job-back">
        &larr; Jobs
      </a>
      <div class="job-hdr-title">{taskText || "Loading\u2026"}</div>
      {meta.length > 0 && (
        <div class="job-hdr-meta">
          {meta.map((m, i) => (
            <>
              {i > 0 && <span class="meta-sep">&middot;</span>}
              {m}
            </>
          ))}
        </div>
      )}
    </div>
  );
}
