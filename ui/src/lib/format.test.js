import { describe, it, expect, vi, afterEach } from "vitest";
import { fmtCost, fmtTokens, fmtDuration, relTime } from "./format.js";

describe("fmtCost", () => {
  it("returns empty string for undefined", () => {
    expect(fmtCost(undefined)).toBe("");
  });

  it("returns empty string for null", () => {
    expect(fmtCost(null)).toBe("");
  });

  it("formats zero", () => {
    expect(fmtCost(0)).toBe("$0.00");
  });

  it("returns sub-penny indicator for small positive values", () => {
    expect(fmtCost(0.005)).toBe("<$0.01");
    expect(fmtCost(0.001)).toBe("<$0.01");
  });

  it("formats the penny boundary exactly", () => {
    expect(fmtCost(0.01)).toBe("$0.01");
  });

  it("formats normal values with two decimals", () => {
    expect(fmtCost(1.5)).toBe("$1.50");
    expect(fmtCost(42)).toBe("$42.00");
  });

  it("formats negative values (not sub-penny path)", () => {
    expect(fmtCost(-0.5)).toBe("$-0.50");
  });
});

describe("fmtTokens", () => {
  it("returns '0' for undefined, null, and 0", () => {
    expect(fmtTokens(undefined)).toBe("0");
    expect(fmtTokens(null)).toBe("0");
    expect(fmtTokens(0)).toBe("0");
  });

  it("returns plain number below 1k", () => {
    expect(fmtTokens(500)).toBe("500");
    expect(fmtTokens(1)).toBe("1");
  });

  it("formats thousands with k suffix", () => {
    expect(fmtTokens(1000)).toBe("1.0k");
    expect(fmtTokens(1500)).toBe("1.5k");
    expect(fmtTokens(999_999)).toBe("1000.0k");
  });

  it("formats millions with M suffix", () => {
    expect(fmtTokens(1_000_000)).toBe("1.0M");
    expect(fmtTokens(2_500_000)).toBe("2.5M");
  });
});

describe("fmtDuration", () => {
  it("returns empty string for undefined and null", () => {
    expect(fmtDuration(undefined)).toBe("");
    expect(fmtDuration(null)).toBe("");
  });

  it("formats sub-second as milliseconds", () => {
    expect(fmtDuration(500)).toBe("500ms");
    expect(fmtDuration(0)).toBe("0ms");
  });

  it("formats seconds with one decimal", () => {
    expect(fmtDuration(1000)).toBe("1.0s");
    expect(fmtDuration(5400)).toBe("5.4s");
  });

  it("formats minutes and seconds", () => {
    expect(fmtDuration(60_000)).toBe("1m 0s");
    expect(fmtDuration(90_500)).toBe("1m 30s");
  });
});

describe("relTime", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns empty string for falsy input", () => {
    expect(relTime("")).toBe("");
    expect(relTime(null)).toBe("");
    expect(relTime(undefined)).toBe("");
  });

  it("formats seconds ago", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:30Z"));
    expect(relTime("2026-01-01T00:00:00Z")).toBe("30s ago");
  });

  it("formats minutes ago", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:05:00Z"));
    expect(relTime("2026-01-01T00:00:00Z")).toBe("5m ago");
  });

  it("formats hours ago", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T03:00:00Z"));
    expect(relTime("2026-01-01T00:00:00Z")).toBe("3h ago");
  });

  it("formats days ago", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-04T00:00:00Z"));
    expect(relTime("2026-01-01T00:00:00Z")).toBe("3d ago");
  });
});
