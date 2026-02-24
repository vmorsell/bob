import { h } from "./html.js";

/**
 * Simple inline markdown: bold+code, bold, italic, backtick code.
 * Input is HTML-escaped first, so output is safe for dangerouslySetInnerHTML.
 */
export function renderMd(raw) {
  var s = h(raw);
  // **`code`** combined bold+code
  s = s.replace(
    /\*\*\x60([^\x60]+)\x60\*\*/g,
    '<strong><code class="cc-code">$1</code></strong>'
  );
  // **bold**
  s = s.replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>");
  // *italic*
  s = s.replace(/\*([^*\n]+)\*/g, "<em>$1</em>");
  // `code`
  s = s.replace(
    /\x60([^\x60\n]+)\x60/g,
    '<code class="cc-code">$1</code>'
  );
  return s;
}
