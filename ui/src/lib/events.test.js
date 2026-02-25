import { describe, it, expect, beforeEach } from "vitest";
import { toolArg, addEvt, resetEventState } from "./events.js";
import {
  resetJobState,
  prLink,
  taskText,
  slackURL,
  jobCostUSD,
  currentJobID,
  currentPhase,
  items,
  toolIdx,
} from "../state/job.js";

// ─── toolArg ────────────────────────────────────────────────────────────────

describe("toolArg", () => {
  it("extracts file_path and strips workspace prefix", () => {
    expect(toolArg("Read", { file_path: "/workspace/repo/src/main.go" })).toBe(
      "src/main.go"
    );
  });

  it("uses command when no file_path", () => {
    expect(toolArg("Bash", { command: "npm test" })).toBe("npm test");
  });

  it("uses pattern key", () => {
    expect(toolArg("Grep", { pattern: "TODO" })).toBe("TODO");
  });

  it("truncates long values at 72 chars", () => {
    const long = "a".repeat(100);
    const result = toolArg("Read", { file_path: long });
    expect(result.length).toBe(73); // 72 + ellipsis
    expect(result.endsWith("\u2026")).toBe(true);
  });

  it("returns empty string for empty object", () => {
    expect(toolArg("Unknown", {})).toBe("");
  });

  it("prefers file_path over command", () => {
    expect(
      toolArg("Bash", { file_path: "/workspace/r/foo.js", command: "ls" })
    ).toBe("foo.js");
  });

  it("skips non-string values", () => {
    expect(toolArg("X", { file_path: 42, command: "echo hi" })).toBe(
      "echo hi"
    );
  });

  it("skips empty string values", () => {
    expect(toolArg("X", { file_path: "", command: "echo hi" })).toBe(
      "echo hi"
    );
  });
});

// ─── addEvt ─────────────────────────────────────────────────────────────────

describe("addEvt", () => {
  beforeEach(() => {
    resetJobState();
    resetEventState();
  });

  // — job_started —

  it("job_started sets taskText, slackURL, currentJobID", () => {
    addEvt({
      type: "job_started",
      job_id: "j1",
      data: { task: "fix bug", slack_thread_url: "https://slack/t" },
    });
    expect(taskText.value).toBe("fix bug");
    expect(slackURL.value).toBe("https://slack/t");
    expect(currentJobID.value).toBe("j1");
  });

  // — llm_response cost accumulation —

  it("llm_response accumulates cost", () => {
    addEvt({ type: "llm_response", data: { cost_usd: 0.01 } });
    addEvt({ type: "llm_response", data: { cost_usd: 0.02 } });
    expect(jobCostUSD.value).toBeCloseTo(0.03);
  });

  // — job_completed overwrites cost —

  it("job_completed with total_cost_usd overwrites cost", () => {
    jobCostUSD.value = 0.5;
    addEvt({ type: "job_completed", data: { total_cost_usd: 1.23 } });
    expect(jobCostUSD.value).toBeCloseTo(1.23);
  });

  // — phase_changed —

  it("phase_changed updates currentPhase", () => {
    addEvt({ type: "phase_changed", data: { phase: "implementing" } });
    expect(currentPhase.value).toBe("implementing");
  });

  // — plan_generated —

  it("plan_generated pushes approve item with pending status", () => {
    addEvt({ type: "plan_generated", data: {} });
    expect(items.value).toHaveLength(1);
    expect(items.value[0]).toMatchObject({ type: "approve", status: "pending" });
  });

  // — plan_approved —

  it("plan_approved marks last pending approve as approved", () => {
    addEvt({ type: "plan_generated", data: {} });
    addEvt({
      type: "plan_approved",
      data: { approved_by: "alice" },
    });
    expect(items.value[0]).toMatchObject({
      type: "approve",
      status: "approved",
      approvedBy: "alice",
    });
  });

  // — plan_superseded —

  it("plan_superseded marks last pending approve as superseded", () => {
    addEvt({ type: "plan_generated", data: {} });
    addEvt({ type: "plan_superseded", data: {} });
    expect(items.value[0]).toMatchObject({
      type: "approve",
      status: "superseded",
    });
  });

  // — tool_started (non-CC) —

  it("tool_started for non-CC tool pushes step with running status", () => {
    addEvt({
      type: "tool_started",
      data: { tool_name: "git_clone", input: "my-repo" },
    });
    expect(items.value).toHaveLength(1);
    expect(items.value[0]).toMatchObject({
      type: "step",
      toolName: "git_clone",
      status: "running",
    });
  });

  // — tool_started (CC: generate_plan) —

  it("tool_started for generate_plan pushes cc-section", () => {
    addEvt({
      type: "tool_started",
      data: { tool_name: "generate_plan" },
    });
    expect(items.value).toHaveLength(1);
    expect(items.value[0]).toMatchObject({
      type: "cc-section",
      label: "Planning",
      completed: false,
    });
  });

  // — tool_completed (non-CC) —

  it("tool_completed marks step as ok", () => {
    addEvt({
      type: "tool_started",
      data: { tool_name: "git_clone", input: "" },
    });
    addEvt({
      type: "tool_completed",
      data: { tool_name: "git_clone", is_error: false, duration_ms: 500 },
    });
    expect(items.value[0]).toMatchObject({ status: "ok", duration: 500 });
  });

  it("tool_completed marks step as error when is_error", () => {
    addEvt({
      type: "tool_started",
      data: { tool_name: "git_clone", input: "" },
    });
    addEvt({
      type: "tool_completed",
      data: { tool_name: "git_clone", is_error: true },
    });
    expect(items.value[0].status).toBe("error");
  });

  // — tool_completed (CC) marks cc-section completed —

  it("tool_completed for CC tool marks cc-section completed", () => {
    addEvt({
      type: "tool_started",
      data: { tool_name: "generate_plan" },
    });
    addEvt({
      type: "tool_completed",
      data: { tool_name: "generate_plan", is_error: false, duration_ms: 3000 },
    });
    expect(items.value[0]).toMatchObject({
      type: "cc-section",
      completed: true,
      isError: false,
      duration: 3000,
    });
  });

  // — tool_completed create_pull_request extracts PR link —

  it("tool_completed for create_pull_request extracts PR link", () => {
    addEvt({
      type: "tool_started",
      data: { tool_name: "create_pull_request", input: "" },
    });
    addEvt({
      type: "tool_completed",
      data: {
        tool_name: "create_pull_request",
        is_error: false,
        result_preview:
          "Created PR https://github.com/org/repo/pull/42 successfully",
      },
    });
    expect(prLink.value).toBe("https://github.com/org/repo/pull/42");
    expect(items.value[0].prURL).toBe("https://github.com/org/repo/pull/42");
  });

  // — claude_code_line with text —

  it("claude_code_line with text pushes text CC item", () => {
    // Need a CC section active for ccIdx to be valid.
    addEvt({ type: "tool_started", data: { tool_name: "implement_changes" } });
    addEvt({ type: "claude_code_line", data: { text: "Hello world" } });
    const textItems = items.value.filter((i) => i.type === "text");
    expect(textItems).toHaveLength(1);
    expect(textItems[0].text).toBe("Hello world");
  });

  // — claude_code_line with tool_name —

  it("claude_code_line with tool_name pushes tool CC item", () => {
    addEvt({ type: "tool_started", data: { tool_name: "implement_changes" } });
    addEvt({
      type: "claude_code_line",
      data: { tool_name: "Edit", tool_input: '{"file_path":"/workspace/r/f.js"}' },
    });
    const toolItems = items.value.filter((i) => i.type === "tool");
    expect(toolItems).toHaveLength(1);
    expect(toolItems[0].data.tool_name).toBe("Edit");
  });

  // — claude_code_line with tool_error —

  it("claude_code_line with tool_error pushes tool-error CC item", () => {
    addEvt({ type: "tool_started", data: { tool_name: "implement_changes" } });
    addEvt({
      type: "claude_code_line",
      data: { tool_error: "permission denied" },
    });
    const errItems = items.value.filter((i) => i.type === "tool-error");
    expect(errItems).toHaveLength(1);
    expect(errItems[0].text).toBe("permission denied");
  });

  // — job_error removes pending approves and adds error footer —

  it("job_error removes pending approves and pushes error footer", () => {
    addEvt({ type: "plan_generated", data: {} });
    addEvt({ type: "job_error", data: { error: "boom", total_cost_usd: 0.5 } });
    expect(items.value[0]).toMatchObject({ type: "approve", status: "removed" });
    expect(items.value[1]).toMatchObject({ type: "footer", isError: true });
    expect(jobCostUSD.value).toBeCloseTo(0.5);
  });

  // — skipped events —

  it("slack_notification does not push items", () => {
    addEvt({ type: "slack_notification", data: {} });
    expect(items.value).toHaveLength(0);
  });

  it("llm_call does not push items", () => {
    addEvt({ type: "llm_call", data: {} });
    expect(items.value).toHaveLength(0);
  });

  it("llm_response does not push items (only accumulates cost)", () => {
    addEvt({ type: "llm_response", data: { cost_usd: 0.01 } });
    expect(items.value).toHaveLength(0);
  });
});
