import {
  prLink,
  taskText,
  slackURL,
  jobCostUSD,
  currentJobID,
  currentPhase,
  items,
  toolIdx,
} from "../state/job.js";

/**
 * Mutable batching state. These track in-flight accumulations across
 * sequential addEvt calls, then get flushed into typed items.
 */
let pendingReads = [];
let pendingGlobs = [];
let pendingThinking = []; // [{thinking, ts}] — duration resolved on next non-thinking
let codeBlock = null; // {lang, lines}
let ccIdx = -1;
let ccLabel = "Claude Code";

export function resetEventState() {
  pendingReads = [];
  pendingGlobs = [];
  pendingThinking = [];
  codeBlock = null;
  ccIdx = -1;
  ccLabel = "Claude Code";
}

function flushReads() {
  if (!pendingReads.length) return;
  if (pendingReads.length === 1) {
    pushCCItem({ type: "tool", data: pendingReads[0] });
  } else {
    pushCCItem({
      type: "read-group",
      count: pendingReads.length,
      reads: pendingReads.map((rd) => {
        let inp = {};
        try { inp = JSON.parse(rd.tool_input || "{}"); } catch {}
        let fp = inp.file_path || inp.path || "";
        fp = fp.replace(/^\/workspace\/[^/]+\//, "");
        return fp || rd.tool_input || "";
      }),
    });
  }
  pendingReads = [];
}

function flushGlobs() {
  if (!pendingGlobs.length) return;
  if (pendingGlobs.length === 1) {
    pushCCItem({ type: "tool", data: pendingGlobs[0] });
  } else {
    pushCCItem({
      type: "glob-group",
      count: pendingGlobs.length,
      patterns: pendingGlobs.map((gl) => {
        let inp = {};
        try { inp = JSON.parse(gl.tool_input || "{}"); } catch {}
        return inp.pattern || inp.path || gl.tool_input || "";
      }),
    });
  }
  pendingGlobs = [];
}

function flushCodeBlock() {
  if (!codeBlock) return;
  pushCCItem({
    type: "codeblock",
    lang: codeBlock.lang,
    code: codeBlock.lines.join("\n"),
  });
  codeBlock = null;
}

function updateThinkingDurations() {
  if (!pendingThinking.length) return;
  const now = Date.now();
  // Mutate existing items to fill in duration.
  const cur = items.value;
  for (const entry of pendingThinking) {
    if (entry.itemIdx !== undefined && cur[entry.itemIdx]) {
      cur[entry.itemIdx] = { ...cur[entry.itemIdx], duration: now - entry.ts };
    }
  }
  items.value = [...cur]; // Trigger reactivity.
  pendingThinking = [];
}

function pushCCItem(item) {
  item.ccIdx = ccIdx;
  items.value = [...items.value, item];
}

function pushItem(item) {
  items.value = [...items.value, item];
}

/** Determine the short argument string for a tool call. */
function toolArg(name, input) {
  const keys = ["file_path", "command", "pattern", "path", "glob", "query", "description"];
  for (const k of keys) {
    const v = input[k];
    if (typeof v === "string" && v) {
      const s = v.replace(/^\/workspace\/[^/]+\//, "");
      return s.length > 72 ? s.slice(0, 72) + "\u2026" : s;
    }
  }
  return "";
}

export { toolArg };

/**
 * Process a single SSE event. Appends typed items to the `items` signal.
 * Must be called sequentially (order matters for code block accumulation, etc).
 */
export function addEvt(ev) {
  const d = ev.data || {};

  // Flush pending buffers on non-cc-line events.
  if (ev.type !== "claude_code_line") {
    flushReads();
    flushGlobs();
    flushCodeBlock();
    updateThinkingDurations();
  }

  // Accumulate cost.
  if (ev.type === "llm_response" && d.cost_usd !== undefined) {
    jobCostUSD.value += d.cost_usd;
  }
  if (
    (ev.type === "job_completed" || ev.type === "job_error") &&
    d.total_cost_usd !== undefined
  ) {
    jobCostUSD.value = d.total_cost_usd;
  }

  // Extract PR link from create_pull_request completion.
  if (
    ev.type === "tool_completed" &&
    d.tool_name === "create_pull_request" &&
    !d.is_error
  ) {
    const mm = (d.result_preview || "").match(
      /https:\/\/github\.com\/[^\s]+\/pull\/\d+/
    );
    if (mm) prLink.value = mm[0];
  }

  // job_started
  if (ev.type === "job_started") {
    taskText.value = d.task || "";
    slackURL.value = d.slack_thread_url || "";
    currentJobID.value = ev.job_id || "";
    return;
  }

  // plan_generated
  if (ev.type === "plan_generated") {
    pushItem({ type: "approve", status: "pending" });
    return;
  }

  // plan_approved
  if (ev.type === "plan_approved") {
    // Find last approve item and mark it done.
    const cur = [...items.value];
    for (let i = cur.length - 1; i >= 0; i--) {
      if (cur[i].type === "approve" && cur[i].status === "pending") {
        cur[i] = { ...cur[i], status: "approved", approvedBy: d.approved_by || "unknown" };
        break;
      }
    }
    items.value = cur;
    return;
  }

  // plan_superseded
  if (ev.type === "plan_superseded") {
    const cur = [...items.value];
    for (let i = cur.length - 1; i >= 0; i--) {
      if (cur[i].type === "approve" && cur[i].status === "pending") {
        cur[i] = { ...cur[i], status: "superseded" };
        break;
      }
    }
    items.value = cur;
    return;
  }

  // phase_changed
  if (ev.type === "phase_changed") {
    currentPhase.value = d.phase || "";
    // Question answered — mark latest question as answered.
    if (d.phase !== "awaiting_question") {
      const cur = [...items.value];
      for (let i = cur.length - 1; i >= 0; i--) {
        if (cur[i].type === "question" && !cur[i].answered) {
          cur[i] = { ...cur[i], answered: true };
          break;
        }
      }
      items.value = cur;
    }
    return;
  }

  // Skip internal plumbing events.
  if (
    ev.type === "slack_notification" ||
    ev.type === "llm_call" ||
    ev.type === "llm_response"
  ) {
    return;
  }

  // Claude Code output line.
  if (ev.type === "claude_code_line") {
    // AskUserQuestion renders as a top-level question card.
    if (d.tool_name === "AskUserQuestion") {
      let aqInput = {};
      try { aqInput = JSON.parse(d.tool_input || "{}"); } catch {}
      let aqText = "";
      if (aqInput.questions && aqInput.questions.length > 0) {
        aqText = aqInput.questions[0].question || "";
      }
      if (aqText) {
        // Mark previous question as answered.
        const cur = [...items.value];
        for (let i = cur.length - 1; i >= 0; i--) {
          if (cur[i].type === "question" && !cur[i].answered) {
            cur[i] = { ...cur[i], answered: true };
            break;
          }
        }
        items.value = cur;
        pushItem({ type: "question", text: aqText, answered: false });
      }
      return;
    }

    updateThinkingDurations();

    if (d.tool_name === "Read") {
      flushGlobs();
      pendingReads.push(d);
    } else if (d.tool_name === "Glob") {
      flushReads();
      pendingGlobs.push(d);
    } else {
      flushReads();
      flushGlobs();

      // Thinking block.
      if (d.thinking !== undefined) {
        const itemIdx = items.value.length;
        pushCCItem({
          type: "thinking",
          text: d.thinking,
          ts: d.thinking_ts || Date.now(),
          duration: null,
        });
        pendingThinking.push({ itemIdx, ts: d.thinking_ts || Date.now() });
        return;
      }

      // Tool error.
      if (d.tool_error !== undefined) {
        pushCCItem({ type: "tool-error", text: d.tool_error });
        return;
      }

      // Sub-agent completions.
      if (d.agents_finished) {
        pushCCItem({
          type: "agents",
          count: d.agents_finished,
          agents: d.agents || [],
        });
        return;
      }

      // Tool call.
      if (d.tool_name) {
        pushCCItem({ type: "tool", data: d });
        return;
      }

      // Text line.
      const txt = (d.text || "").trimEnd();
      if (!txt) return;

      // Fenced code block.
      const fenceMatch = txt.match(/^```(\w*)$/);
      if (fenceMatch && !codeBlock) {
        codeBlock = { lang: fenceMatch[1], lines: [] };
        return;
      }
      if (codeBlock) {
        if (txt === "```") {
          flushCodeBlock();
        } else {
          codeBlock.lines.push(d.text || "");
        }
        return;
      }

      // Blockquote.
      const bq = txt.match(/^>\s?(.*)$/);
      if (bq) {
        pushCCItem({ type: "quote", text: bq[1] });
        return;
      }

      // Unordered list item.
      const ul = txt.match(/^(\s*)[*-]\s+(.+)$/);
      if (ul) {
        pushCCItem({ type: "list-item", indent: ul[1].length, text: ul[2], ordered: false });
        return;
      }

      // Ordered list item.
      const ol = txt.match(/^(\s*)(\d+)\.\s+(.+)$/);
      if (ol) {
        pushCCItem({ type: "list-item", indent: ol[1].length, num: ol[2], text: ol[3], ordered: true });
        return;
      }

      // Plain text.
      pushCCItem({ type: "text", text: txt });
    }
    return;
  }

  // tool_started
  if (ev.type === "tool_started") {
    const isCCTool =
      d.tool_name === "implement_changes" ||
      d.tool_name === "run_tests" ||
      d.tool_name === "generate_plan";

    if (isCCTool) {
      ccIdx = toolIdx.value;
      ccLabel =
        d.tool_name === "run_tests"
          ? "Run Tests"
          : d.tool_name === "generate_plan"
            ? "Planning"
            : "Claude Code";

      pushItem({
        type: "cc-section",
        idx: ccIdx,
        label: ccLabel,
        isTerminal: d.tool_name === "run_tests",
        completed: false,
        isError: false,
        duration: null,
      });
    } else {
      let desc = d.input || "";
      if (desc.length > 64) desc = desc.slice(0, 64) + "\u2026";
      pushItem({
        type: "step",
        idx: toolIdx.value,
        toolName: d.tool_name,
        desc,
        status: "running",
        duration: "",
        prURL: null,
      });
    }
    return;
  }

  // tool_completed
  if (ev.type === "tool_completed") {
    const isCCTool =
      d.tool_name === "implement_changes" ||
      d.tool_name === "run_tests" ||
      d.tool_name === "generate_plan";

    if (isCCTool) {
      // Find the cc-section item and mark it completed.
      const cur = [...items.value];
      for (let i = cur.length - 1; i >= 0; i--) {
        if (cur[i].type === "cc-section" && cur[i].idx === ccIdx) {
          cur[i] = {
            ...cur[i],
            completed: true,
            isError: !!d.is_error,
            duration: d.duration_ms !== undefined ? d.duration_ms : null,
          };
          break;
        }
      }
      items.value = cur;
    } else {
      // Find the matching step item and update it.
      const cur = [...items.value];
      for (let i = cur.length - 1; i >= 0; i--) {
        if (cur[i].type === "step" && cur[i].idx === toolIdx.value) {
          let desc = cur[i].desc;
          let stepPrURL = null;
          if (d.tool_name === "create_pull_request" && !d.is_error) {
            const m = (d.result_preview || "").match(
              /https:\/\/github\.com\/[^\s]+\/pull\/\d+/
            );
            if (m) {
              stepPrURL = m[0];
              desc = m[0].replace("https://github.com/", "");
            }
          }
          cur[i] = {
            ...cur[i],
            status: d.is_error ? "error" : "ok",
            duration:
              d.duration_ms !== undefined
                ? d.duration_ms
                : "",
            prURL: stepPrURL,
            desc,
          };
          break;
        }
      }
      items.value = cur;
    }

    toolIdx.value++;
    return;
  }

  // job_completed
  if (ev.type === "job_completed") {
    // Remove pending approve buttons.
    items.value = items.value.map((it) =>
      it.type === "approve" && it.status === "pending"
        ? { ...it, status: "removed" }
        : it
    );
    pushItem({ type: "footer", isError: false, data: d });
    return;
  }

  // job_error
  if (ev.type === "job_error") {
    items.value = items.value.map((it) =>
      it.type === "approve" && it.status === "pending"
        ? { ...it, status: "removed" }
        : it
    );
    pushItem({ type: "footer", isError: true, data: d });
    return;
  }
}
