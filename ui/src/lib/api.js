export async function fetchJobs() {
  const r = await fetch("/api/jobs");
  return r.json();
}

export async function fetchStats() {
  try {
    const r = await fetch("/api/stats");
    return r.json();
  } catch {
    return null;
  }
}

export async function fetchJobEvents(id) {
  const r = await fetch("/api/jobs/" + encodeURIComponent(id));
  if (!r.ok) throw new Error("Job not found");
  return r.json();
}

export async function approveJob(id) {
  const r = await fetch("/api/jobs/" + encodeURIComponent(id) + "/approve", {
    method: "POST",
  });
  return r.json();
}
