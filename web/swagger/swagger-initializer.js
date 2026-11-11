// Kept as a separate file (no inline script) so a strict Content-Security-Policy can be used.
window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "/api/openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    persistAuthorization: false,
    validatorUrl: null, // never call the public validator: offline by design
  });
});
