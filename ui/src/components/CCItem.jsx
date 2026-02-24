import { h as esc } from "../lib/html.js";
import { renderMd } from "../lib/markdown.js";
import { fmtDuration } from "../lib/format.js";
import { toolArg } from "../lib/events.js";
import { DiffView } from "./DiffView.jsx";
import { TodoList } from "./TodoList.jsx";

export function CCItem({ item }) {
  switch (item.type) {
    case "thinking":
      return <ThinkingItem text={item.text} duration={item.duration} />;
    case "tool-error":
      return <div class="cc-error">&nbsp;<span>{item.text}</span></div>;
    case "agents":
      return <AgentsItem count={item.count} agents={item.agents} />;
    case "tool":
      return <ToolItem data={item.data} />;
    case "read-group":
      return <ReadGroup count={item.count} reads={item.reads} />;
    case "glob-group":
      return <GlobGroup count={item.count} patterns={item.patterns} />;
    case "codeblock":
      return <CodeBlock lang={item.lang} code={item.code} />;
    case "quote":
      return (
        <div
          class="cc-quote"
          dangerouslySetInnerHTML={{ __html: renderMd(item.text) }}
        />
      );
    case "list-item":
      return <ListItem item={item} />;
    case "text":
      return (
        <div
          class="cc-text"
          dangerouslySetInnerHTML={{ __html: renderMd(item.text) }}
        />
      );
    default:
      return null;
  }
}

function ThinkingItem({ text, duration }) {
  const durStr = duration != null ? fmtDuration(duration) : "\u2026";
  return (
    <details class="cc-thinking">
      <summary>
        Thought for <span class="cc-thinking-dur">{durStr}</span>
      </summary>
      <pre class="cc-thinking-body">{text}</pre>
    </details>
  );
}

function AgentsItem({ count, agents }) {
  return (
    <details class="cc-agents">
      <summary>
        {count} sub-agent{count !== 1 ? "s" : ""} finished
      </summary>
      <div class="cc-agents-body">
        {agents.map((a, i) => (
          <div key={i} class="cc-agents-item">
            {a.description || ""}
          </div>
        ))}
      </div>
    </details>
  );
}

function ToolItem({ data }) {
  const name = data.tool_name || "";
  if (name === "ExitPlanMode" || name === "EnterPlanMode") return null;
  if (name === "AskUserQuestion") return null;

  let input = {};
  try { input = JSON.parse(data.tool_input || "{}"); } catch {}

  // Edit/Write to .claude/plans/ — show as simple row.
  if (
    (name === "Edit" || name === "Write") &&
    input.file_path &&
    input.file_path.indexOf(".claude/plans/") !== -1
  ) {
    const arg = toolArg(name, input);
    return (
      <div class="cc-row">
        <span class="cc-arrow">&middot;</span>
        <span class="cc-nm">{name}</span>
        {arg && <span class="cc-arg">{arg}</span>}
      </div>
    );
  }

  // Edit/Write — diff view.
  if (name === "Edit" || name === "Write") {
    return (
      <DiffView
        toolName={name}
        filePath={input.file_path || ""}
        oldString={input.old_string || ""}
        newString={input.new_string || input.content || ""}
      />
    );
  }

  // TodoWrite.
  if (name === "TodoWrite") {
    return <TodoList todos={input.todos || []} />;
  }

  // Simple one-line tool row.
  const arg = toolArg(name, input);
  return (
    <div class="cc-row">
      <span class="cc-arrow">&middot;</span>
      <span class="cc-nm">{name}</span>
      {arg && <span class="cc-arg">{arg}</span>}
    </div>
  );
}

function ReadGroup({ count, reads }) {
  return (
    <details class="cc-group">
      <summary>Read {count} files</summary>
      <ul>
        {reads.map((r, i) => (
          <li key={i}>{r}</li>
        ))}
      </ul>
    </details>
  );
}

function GlobGroup({ count, patterns }) {
  return (
    <details class="cc-group">
      <summary>Glob {count} patterns</summary>
      <ul>
        {patterns.map((p, i) => (
          <li key={i}>{p}</li>
        ))}
      </ul>
    </details>
  );
}

function CodeBlock({ lang, code }) {
  return (
    <div class="cc-codeblock">
      {lang && <div class="cc-codeblock-hdr">{lang}</div>}
      <pre>{code}</pre>
    </div>
  );
}

function ListItem({ item }) {
  const sym = item.ordered ? item.num + "." : "\u2022";
  return (
    <div class="cc-li" style={{ paddingLeft: 18 + item.indent * 10 + "px" }}>
      <span class="cc-li-sym">{sym}</span>
      <span dangerouslySetInnerHTML={{ __html: renderMd(item.text) }} />
    </div>
  );
}
