export function fmtCost(usd) {
  if (usd === undefined || usd === null) return "";
  if (usd < 0.01 && usd > 0) return "<$0.01";
  return "$" + usd.toFixed(2);
}

export function fmtTokens(n) {
  if (n === undefined || n === null || n === 0) return "0";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return String(n);
}

export function fmtDuration(ms) {
  if (ms === undefined || ms === null) return "";
  if (ms < 1000) return ms + "ms";
  var s = ms / 1000;
  if (s < 60) return s.toFixed(1) + "s";
  var m = Math.floor(s / 60);
  var rem = Math.floor(s % 60);
  return m + "m " + rem + "s";
}

export function relTime(ts) {
  if (!ts) return "";
  var diff = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
  if (diff < 60) return diff + "s ago";
  if (diff < 3600) return Math.floor(diff / 60) + "m ago";
  if (diff < 86400) return Math.floor(diff / 3600) + "h ago";
  return Math.floor(diff / 86400) + "d ago";
}
