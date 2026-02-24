import { useState } from "preact/hooks";
import { approveJob } from "../lib/api.js";
import { currentJobID } from "../state/job.js";
import "../styles/approve.css";

export function ApproveButton({ status, approvedBy }) {
  const [submitting, setSubmitting] = useState(false);

  if (status === "removed") return null;

  if (status === "approved") {
    return <div class="approve-done">{"\u2713"} Approved by {approvedBy}</div>;
  }
  if (status === "superseded") {
    return <div class="superseded-label">Revision requested</div>;
  }

  const handleClick = () => {
    if (!currentJobID.value) return;
    setSubmitting(true);
    approveJob(currentJobID.value).catch(() => setSubmitting(false));
  };

  return (
    <div class="approve-wrap">
      <button
        class="approve-btn"
        disabled={submitting}
        onClick={handleClick}
      >
        {submitting ? "Approving\u2026" : "Approve Plan"}
      </button>
    </div>
  );
}
