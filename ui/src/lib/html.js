/** Escape HTML special characters. */
export function h(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/** URL-encode a string. */
export function enc(s) {
  return encodeURIComponent(String(s));
}
