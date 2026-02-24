import { signal } from "@preact/signals";

export const autoScroll = signal(true);
export const prLink = signal(null);
export const taskText = signal("");
export const slackURL = signal("");
export const jobCostUSD = signal(0);
export const isLive = signal(false);
export const currentJobID = signal("");
export const currentPhase = signal("");

// The rendered event items — array of typed objects consumed by the timeline.
export const items = signal([]);

// Tracking counters.
export const toolIdx = signal(0);

export function resetJobState() {
  autoScroll.value = true;
  prLink.value = null;
  taskText.value = "";
  slackURL.value = "";
  jobCostUSD.value = 0;
  isLive.value = false;
  currentJobID.value = "";
  currentPhase.value = "";
  items.value = [];
  toolIdx.value = 0;
}
