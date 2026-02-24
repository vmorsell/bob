import { fmtDuration } from "../lib/format.js";

export function StepRow({ toolName, desc, status, duration, prURL }) {
  let dot;
  if (status === "running") {
    dot = (
      <span class="step-dot">
        <span class="step-pulse" />
      </span>
    );
  } else if (status === "error") {
    dot = <span class="step-dot" style="color:var(--red)">{"\u2717"}</span>;
  } else {
    dot = <span class="step-dot" style="color:var(--green)">{"\u2713"}</span>;
  }

  let descEl = null;
  if (prURL) {
    descEl = (
      <span class="step-desc">
        <a href={prURL} target="_blank">
          {desc}
        </a>
      </span>
    );
  } else if (desc) {
    descEl = <span class="step-desc">{desc}</span>;
  }

  const durStr =
    typeof duration === "number" ? fmtDuration(duration) : duration || "";

  return (
    <div class="step-line">
      {dot}
      <span class="step-nm">{toolName}</span>
      {descEl}
      <span class="step-dur">{durStr}</span>
    </div>
  );
}
