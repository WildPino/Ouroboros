// A frame of the page tries to move the top-level document away, then
// itself.
try {
  window.top.location.href = "https://example.com/from-frame";
} catch (e) {
  // refused
}
setTimeout(() => {
  location.href = "https://example.com/frame-nav";
}, 300);
