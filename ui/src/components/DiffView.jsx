import { h as esc } from "../lib/html.js";

const MAX_LINES = 24;

export function DiffView({ toolName, filePath, oldString, newString }) {
  const short = (filePath || "").replace(/^\/workspace\/[^/]+\//, "") || filePath;
  const oldLines = (oldString || "").split("\n");
  const newLines = (newString || "").split("\n");
  const oldOver = oldLines.length > MAX_LINES;
  const newOver = newLines.length > MAX_LINES;
  const oldShow = oldOver ? oldLines.slice(0, MAX_LINES) : oldLines;
  const newShow = newOver ? newLines.slice(0, MAX_LINES) : newLines;

  return (
    <div class="cc-diff">
      <div class="cc-diff-hdr">
        <span class="cc-arrow">&middot;</span>
        <span class="cc-nm">{toolName}</span>
        <span class="cc-diff-path">{short}</span>
      </div>
      <div class="diff-lines">
        {oldShow.map((l, i) => (
          <div key={"d" + i} class="dl dl-del">
            {"- " + l}
          </div>
        ))}
        {oldOver && <div class="dl dl-sep">&hellip; truncated &hellip;</div>}
        {newShow.map((l, i) => (
          <div key={"a" + i} class="dl dl-add">
            {"+ " + l}
          </div>
        ))}
        {newOver && <div class="dl dl-sep">&hellip; truncated &hellip;</div>}
      </div>
    </div>
  );
}
