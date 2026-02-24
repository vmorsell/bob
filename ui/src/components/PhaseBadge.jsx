import "../styles/phase.css";

const LABELS = {
  planning: "Planning",
  awaiting_approval: "Awaiting Approval",
  awaiting_question: "Awaiting Question",
  implementing: "Implementing",
  done: "Done",
};

export function PhaseBadge({ phase }) {
  if (!phase || !LABELS[phase]) return null;
  return (
    <span class={"phase-badge phase-badge-" + phase}>{LABELS[phase]}</span>
  );
}
