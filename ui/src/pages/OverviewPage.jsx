import { useEffect } from "preact/hooks";
import { signal } from "@preact/signals";
import { fetchJobs, fetchStats } from "../lib/api.js";
import { fmtCost, relTime } from "../lib/format.js";
import { StatusPip } from "../components/StatusPip.jsx";
import { StatsBar } from "../components/StatsBar.jsx";
import "../styles/overview.css";

const PHASE_LABELS = {
  planning: "Planning",
  awaiting_approval: "Awaiting Approval",
  awaiting_question: "Awaiting Question",
  implementing: "Implementing",
};

const jobs = signal([]);
const stats = signal(null);
const loaded = signal(false);
const error = signal(false);

async function loadOverview() {
  try {
    const [j, s] = await Promise.all([fetchJobs(), fetchStats()]);
    jobs.value = j || [];
    stats.value = s;
    loaded.value = true;
    error.value = false;
  } catch {
    error.value = true;
  }
}

export function OverviewPage() {
  useEffect(() => {
    document.title = "Bob";
    loadOverview();
    const iv = setInterval(loadOverview, 10000);
    return () => clearInterval(iv);
  }, []);

  if (error.value) {
    return <div class="placeholder">Failed to load.</div>;
  }
  if (!loaded.value) {
    return <div class="placeholder">Loading&hellip;</div>;
  }
  if (!jobs.value.length) {
    return (
      <div class="placeholder">
        No jobs yet &mdash; mention Bob in Slack with a task to get started.
      </div>
    );
  }

  return (
    <div>
      <div class="page-head">
        <h1 class="page-title">Jobs</h1>
      </div>
      <StatsBar jobs={jobs.value} stats={stats.value} />
      <div class="job-list">
        {jobs.value.map((j) => (
          <a key={j.id} class="job-row" href={"/jobs/" + encodeURIComponent(j.id)}>
            <StatusPip status={j.status} phase={j.phase} />
            <span class="job-row-task">{j.task || "(untitled)"}</span>
            {j.status === "running" && j.phase && PHASE_LABELS[j.phase] && (
              <span class="job-row-phase">{PHASE_LABELS[j.phase]}</span>
            )}
            {j.cost_usd ? (
              <span class="job-row-cost">{fmtCost(j.cost_usd)}</span>
            ) : null}
            <span class="job-row-time">{relTime(j.started_at)}</span>
          </a>
        ))}
      </div>
    </div>
  );
}
