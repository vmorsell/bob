export function StatusPip({ status, phase }) {
  const cls =
    status === "running" && phase ? "pip-" + phase : "pip-" + (status || "running");
  return <span class={"status-pip " + cls} />;
}
