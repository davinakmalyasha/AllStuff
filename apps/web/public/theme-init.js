// FOUC guard: apply the stored theme before first paint. Default is LIGHT.
//
// This lives in a file rather than inline in index.html on purpose. An inline
// script needs either 'unsafe-inline' in script-src - which would undo the point
// of having a CSP at all - or a sha256 hash that has to be recomputed by hand on
// every edit, and a stale hash fails silently in the sense that the browser
// simply blocks the script and the guard stops working. This is served from
// 'self' as a classic blocking script, so it runs before the body paints with
// no hash to maintain and no inline allowance to grant.
;(function () {
  try {
    var stored = localStorage.getItem('bv.theme')
    var dark = stored ? stored === 'dark' : false
    document.documentElement.classList.toggle('dark', dark)
    var meta = document.getElementById('meta-theme-color')
    if (meta) meta.setAttribute('content', dark ? '#0a0a0a' : '#ffffff')
  } catch (e) {}
})()