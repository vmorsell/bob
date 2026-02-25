import { describe, it, expect } from "vitest";
import { h, enc } from "./html.js";

describe("h", () => {
  it("returns plain text unchanged", () => {
    expect(h("hello world")).toBe("hello world");
  });

  it("escapes ampersands", () => {
    expect(h("a & b")).toBe("a &amp; b");
  });

  it("escapes angle brackets", () => {
    expect(h("<script>alert('xss')</script>")).toBe(
      "&lt;script&gt;alert('xss')&lt;/script&gt;"
    );
  });

  it("escapes double quotes", () => {
    expect(h('"quoted"')).toBe("&quot;quoted&quot;");
  });

  it("escapes all special chars combined", () => {
    expect(h('<a href="x">&')).toBe("&lt;a href=&quot;x&quot;&gt;&amp;");
  });

  it("coerces non-string input to string", () => {
    expect(h(42)).toBe("42");
    expect(h(true)).toBe("true");
  });
});

describe("enc", () => {
  it("returns plain text unchanged", () => {
    expect(enc("hello")).toBe("hello");
  });

  it("encodes spaces", () => {
    expect(enc("hello world")).toBe("hello%20world");
  });

  it("encodes special characters", () => {
    expect(enc("a/b?c=d&e")).toBe("a%2Fb%3Fc%3Dd%26e");
  });

  it("coerces non-string input", () => {
    expect(enc(123)).toBe("123");
  });
});
