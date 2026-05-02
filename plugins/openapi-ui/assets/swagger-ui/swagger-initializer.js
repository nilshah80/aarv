// Initializer served as an external script (not inlined) so a
// Content-Security-Policy of `script-src 'self'` is sufficient — no
// per-script-hash maintenance burden when assets are updated.
//
// Reads the spec URL from the data-spec-url attribute on the script tag
// referencing this file. Falls back to /openapi.json if the attribute
// is missing.
(function () {
  var script = document.currentScript;
  var specURL = (script && script.getAttribute('data-spec-url')) || '/openapi.json';
  window.addEventListener('load', function () {
    if (typeof SwaggerUIBundle !== 'function') return;
    window.ui = SwaggerUIBundle({
      url: specURL,
      dom_id: '#swagger-ui',
      deepLinking: true,
      presets: SwaggerUIBundle.presets ? [SwaggerUIBundle.presets.apis] : undefined,
    });
  });
})();
