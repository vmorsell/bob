import { describe, it, expect } from "vitest";
import { renderMd } from "./markdown.js";

describe("renderMd", () => {
  it("returns empty string for empty input", () => {
    expect(renderMd("")).toBe("");
  });

  it("returns plain text unchanged (after HTML escaping)", () => {
    expect(renderMd("hello world")).toBe("hello world");
  });

  it("renders bold", () => {
    expect(renderMd("**bold**")).toBe("<strong>bold</strong>");
  });

  it("renders italic", () => {
    expect(renderMd("*italic*")).toBe("<em>italic</em>");
  });

  it("renders inline code", () => {
    expect(renderMd("`code`")).toBe('<code class="cc-code">code</code>');
  });

  it("renders bold+code combined", () => {
    expect(renderMd("**`boldcode`**")).toBe(
      '<strong><code class="cc-code">boldcode</code></strong>'
    );
  });

  it("renders mixed inline styles", () => {
    const input = "**bold** and *italic* and `code`";
    const out = renderMd(input);
    expect(out).toContain("<strong>bold</strong>");
    expect(out).toContain("<em>italic</em>");
    expect(out).toContain('<code class="cc-code">code</code>');
  });

  it("HTML-escapes input before applying markdown", () => {
    expect(renderMd("<script>alert(1)</script>")).toBe(
      "&lt;script&gt;alert(1)&lt;/script&gt;"
    );
  });

  it("does not transform unclosed bold markers", () => {
    const result = renderMd("**unclosed");
    expect(result).not.toContain("<strong>");
    expect(result).toBe("**unclosed");
  });
});
