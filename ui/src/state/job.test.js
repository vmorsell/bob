import { describe, it, expect } from "vitest";
import {
  resetJobState,
  autoScroll,
  prLink,
  taskText,
  slackURL,
  jobCostUSD,
  isLive,
  currentJobID,
  currentPhase,
  items,
  toolIdx,
} from "./job.js";

describe("resetJobState", () => {
  it("resets all signals to defaults after mutation", () => {
    // Mutate everything.
    autoScroll.value = false;
    prLink.value = "https://github.com/org/repo/pull/1";
    taskText.value = "some task";
    slackURL.value = "https://slack/t";
    jobCostUSD.value = 42;
    isLive.value = true;
    currentJobID.value = "job-123";
    currentPhase.value = "implementing";
    items.value = [{ type: "step" }, { type: "text" }];
    toolIdx.value = 5;

    resetJobState();

    expect(autoScroll.value).toBe(true);
    expect(prLink.value).toBeNull();
    expect(taskText.value).toBe("");
    expect(slackURL.value).toBe("");
    expect(jobCostUSD.value).toBe(0);
    expect(isLive.value).toBe(false);
    expect(currentJobID.value).toBe("");
    expect(currentPhase.value).toBe("");
    expect(items.value).toEqual([]);
    expect(toolIdx.value).toBe(0);
  });
});
