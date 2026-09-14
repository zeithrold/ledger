Scalar.createApiReference('#app', {
  url: document.querySelector('#app').dataset.specUrl,
  agent: { disabled: true },
  showDeveloperTools: "never",
  persistAuth: false,
  telemetry: false,
  withDefaultFonts: false,
});
