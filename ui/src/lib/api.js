function resolveToken() {
  const fromURL = new URLSearchParams(window.location.search).get("token");
  if (fromURL) {
    sessionStorage.setItem("bob_token", fromURL);
    return fromURL;
  }
  return sessionStorage.getItem("bob_token") || "";
}

const apiToken = resolveToken();

function authHeaders() {
  if (!apiToken) return {};
  return { Authorization: "Bearer " + apiToken };
}

export function tokenQueryParam() {
  if (!apiToken) return "";
  return "token=" + encodeURIComponent(apiToken);
}

export async function fetchJobs() {
  const r = await fetch("/api/jobs", { headers: authHeaders() });
  return r.json();
}

export async function fetchStats() {
  try {
    const r = await fetch("/api/stats", { headers: authHeaders() });
    return r.json();
  } catch {
    return null;
  }
}

export async function fetchJobEvents(id) {
  const r = await fetch("/api/jobs/" + encodeURIComponent(id), { headers: authHeaders() });
  if (!r.ok) throw new Error("Job not found");
  return r.json();
}

export async function approveJob(id) {
  const r = await fetch("/api/jobs/" + encodeURIComponent(id) + "/approve", {
    method: "POST",
    headers: authHeaders(),
  });
  return r.json();
}
